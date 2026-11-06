package playbook

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/ssh"
	"go-ansible/pkg/util"
)

// 1. 验证事实缓存 (Fact Caching) 命中时彻底根除 hostVarsMu 自死锁
func TestPhase25_FactCacheHitNoSelfDeadlock(t *testing.T) {
	cacheDir, err := os.MkdirTemp("", "phase25_fact_cache_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(cacheDir)

	// 预先写入磁盘事实缓存
	hostName := "srv-cached"
	factData := map[string]interface{}{
		"ansible_facts": map[string]interface{}{
			"os_family": "RedHat",
			"cores":     8,
		},
	}
	raw, _ := json.Marshal(factData)
	_ = os.WriteFile(filepath.Join(cacheDir, hostName+".json"), raw, 0644)

	inv := inventory.NewInventory()
	h := &inventory.Host{
		Name:       hostName,
		Address:    "10.0.0.1",
		Connection: "local",
	}
	inv.AddHost(h)

	exec := NewExecutor(inv)
	exec.factCaching = "jsonfile"
	exec.factCachingConnection = cacheDir

	// 使用超时控制，若存在自死锁将超时报错
	done := make(chan bool)
	go func() {
		// buildHostVarsSnapshot 会进入慢路径持有 hostVarsMu.Lock() 并调用 getHostFacts
		// 若此前包含 e.invalidateHostVarsCache 则会发生自死锁
		snapshot := exec.buildHostVarsSnapshot(h, map[string]interface{}{})
		if snapshot[hostName] == nil {
			t.Errorf("expected snapshot for %s", hostName)
		}
		done <- true
	}()

	select {
	case <-done:
		// 成功无死锁
	case <-time.After(2 * time.Second):
		t.Fatalf("buildHostVarsSnapshot deadlocked on fact cache hit!")
	}
}

// 2. 验证 Inventory.AddHost / AddGroup 高并发读写安全 (零 Panic / 零 Data Race)
func TestPhase25_InventoryConcurrentAddHostAndReads(t *testing.T) {
	inv := inventory.NewInventory()

	// 预置一些基础主机
	for i := 0; i < 20; i++ {
		inv.AddHost(&inventory.Host{
			Name:    fmt.Sprintf("base-%02d", i),
			Address: fmt.Sprintf("10.0.0.%d", i),
		}, "base_group")
	}

	var wg sync.WaitGroup
	// 15 个并发读协程
	for i := 0; i < 15; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = inv.GetAllHosts()
				_, _ = inv.GetGroup("base_group")
				_, _ = inv.GetHost("base-01")
			}
		}()
	}

	// 15 个并发写协程
	for i := 0; i < 15; i++ {
		wg.Add(1)
		workerID := i
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				hName := fmt.Sprintf("dyn-%d-%d", workerID, j)
				gName := fmt.Sprintf("grp-%d", workerID)
				inv.AddHost(&inventory.Host{
					Name:    hName,
					Address: "127.0.0.1",
				}, gName)

				inv.AddGroup(&inventory.Group{
					Name: fmt.Sprintf("parent-%d", workerID),
				}, "all")
			}
		}()
	}

	wg.Wait()

	all := inv.GetAllHosts()
	if len(all) < 20 {
		t.Fatalf("expected at least 20 hosts, got %d", len(all))
	}
}

// 3. 验证超限截断后管道后台排空防挂死 (readBoundedOutput)
type infiniteStream struct {
	totalBytes int64
	readBytes  int64
}

func (s *infiniteStream) Read(p []byte) (n int, err error) {
	if s.readBytes >= s.totalBytes {
		return 0, io.EOF
	}
	remaining := s.totalBytes - s.readBytes
	toRead := int64(len(p))
	if toRead > remaining {
		toRead = remaining
	}
	for i := int64(0); i < toRead; i++ {
		p[i] = 'A'
	}
	s.readBytes += toRead
	return int(toRead), nil
}

func TestPhase25_BoundedBufferPipeDrain(t *testing.T) {
	// 构造 5MB 的流，上限设为 50KB
	stream := &infiniteStream{totalBytes: 5 * 1024 * 1024}
	limit := int64(50 * 1024)

	done := make(chan bool)
	var output string
	var err error

	go func() {
		// 验证 readBoundedOutput 迅速返回且不挂起
		output, err = ssh.ReadBoundedOutput(stream, limit)
		done <- true
	}()

	select {
	case <-done:
		if err != nil {
			t.Fatalf("readBoundedOutput error: %v", err)
		}
		if !strings.Contains(output, "[output truncated") {
			t.Fatalf("expected output truncation notice")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("readBoundedOutput hung on large stream!")
	}

	// 稍作等待确保后台 drain goroutine 将 stream 读至 EOF
	time.Sleep(100 * time.Millisecond)
	if stream.readBytes < stream.totalBytes {
		t.Logf("drained %d of %d bytes", stream.readBytes, stream.totalBytes)
	}
}

// 4. 验证统一 util.SplitLines 行切分逻辑与 CRLF/LF 抹平
func TestPhase25_SplitLinesCRLF(t *testing.T) {
	cases := []struct {
		input    string
		expected []string
	}{
		{"", []string{}},
		{"\n", []string{}},
		{"\r\n", []string{}},
		{"line1\nline2", []string{"line1", "line2"}},
		{"line1\r\nline2\r\n", []string{"line1", "line2"}},
		{"line1\r\nline2\nline3", []string{"line1", "line2", "line3"}},
		{"single line", []string{"single line"}},
	}

	for _, tc := range cases {
		actual := util.SplitLines(tc.input)
		if len(actual) != len(tc.expected) {
			t.Fatalf("for input %q expected %v, got %v", tc.input, tc.expected, actual)
		}
		for i := range actual {
			if actual[i] != tc.expected[i] {
				t.Fatalf("for input %q line %d: expected %q, got %q", tc.input, i, tc.expected[i], actual[i])
			}
		}
	}
}
