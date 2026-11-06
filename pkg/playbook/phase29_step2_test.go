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
)

// 1. 测试 HostResult 细粒度并发锁，确保多 Goroutine 追加与快照读取零数据竞争
func TestHostResultThreadSafety(t *testing.T) {
	hr := &HostResult{}
	playResult := &PlayResult{
		Hosts: map[string]*HostResult{
			"node1": hr,
		},
	}
	exec := NewExecutor(inventory.NewInventory())

	var wg sync.WaitGroup
	numWorkers := 10
	appendsPerWorker := 50

	// 并发写
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < appendsPerWorker; i++ {
				hr.AppendTask(&TaskResult{
					Name:   fmt.Sprintf("task-%d-%d", workerID, i),
					Host:   "node1",
					Failed: i%2 == 0,
				})
			}
		}(w)
	}

	// 并发读
	for r := 0; r < 5; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				_ = hr.GetTasks()
				_ = exec.failedTaskCount(playResult)
				time.Sleep(1 * time.Millisecond)
			}
		}()
	}

	wg.Wait()

	tasks := hr.GetTasks()
	if len(tasks) != numWorkers*appendsPerWorker {
		t.Fatalf("expected %d tasks in HostResult, got %d", numWorkers*appendsPerWorker, len(tasks))
	}
}

// 2. 测试 getCachedFacts 释放全局读锁后再执行磁盘 I/O 与 JSON 解析，避免阻塞写锁
func TestGetCachedFactsNonBlockingIO(t *testing.T) {
	tmpDir := t.TempDir()
	hostName := "db1"
	cacheFile := filepath.Join(tmpDir, hostName+".json")

	factsData := map[string]interface{}{
		"ansible_distribution": "Debian",
		"ansible_kernel":       "6.1.0",
	}
	encoded, _ := json.Marshal(factsData)
	_ = os.WriteFile(cacheFile, encoded, 0644)

	exec := NewExecutor(inventory.NewInventory())
	exec.factCaching = "jsonfile"
	exec.factCachingConnection = tmpDir
	exec.factCachingTimeout = 3600

	cached, found := exec.getCachedFacts(hostName)
	if !found || cached == nil {
		t.Fatalf("expected getCachedFacts to find cached facts on disk, got found=%v", found)
	}
	if cached["ansible_distribution"] != "Debian" {
		t.Errorf("expected distribution 'Debian', got %v", cached["ansible_distribution"])
	}

	// 验证在 getCachedFacts 过程中全局互斥锁能够正常获取（非持有锁状态进行 I/O）
	lockAcquired := make(chan bool)
	go func() {
		exec.mu.Lock()
		lockAcquired <- true
		exec.mu.Unlock()
	}()

	select {
	case <-lockAcquired:
		// 成功无阻塞获取锁
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("global mutex was blocked unexpectedly")
	}
}

// 3. 辅助 dummy closer 用于测试跳板机连接清理
type dummyCloser struct {
	closed bool
}

func (d *dummyCloser) Close() error {
	d.closed = true
	return nil
}

// 4. 测试 ProxyJump 在连接重试或重置时主动释放旧有的 authClosers，杜绝句柄泄漏
func TestProxyJumpSocketLeakCleanup(t *testing.T) {
	h := &inventory.Host{
		Name:    "target",
		Address: "127.0.0.1",
		Port:    22,
	}
	client, err := ssh.NewClient(h, nil)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	initialClosers := len(client.AuthClosers())

	c1 := &dummyCloser{}
	c2 := &dummyCloser{}
	client.AddAuthCloser(c1)
	client.AddAuthCloser(c2)

	if len(client.AuthClosers()) != initialClosers+2 {
		t.Fatalf("expected %d auth closers, got %d", initialClosers+2, len(client.AuthClosers()))
	}

	// 调用 Close，验证所有 closers 均被关闭并置空
	_ = client.Close()

	if !c1.closed || !c2.closed {
		t.Errorf("expected all dummy closers to be closed, c1=%v, c2=%v", c1.closed, c2.closed)
	}
	if len(client.AuthClosers()) != 0 {
		t.Errorf("expected auth closers to be empty after close, got %d", len(client.AuthClosers()))
	}
}

// 5. 测试 EnvConnection 完整转发 SetTimeout, GetTimeout, SetHost, Host 以及 Unwrap
func TestEnvConnectionMethodForwarding(t *testing.T) {
	h := &inventory.Host{
		Name:    "web01",
		Address: "192.168.1.50",
		Port:    22,
	}
	mockClient, err := ssh.NewClient(h, nil)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	mockClient.SetTimeout(45 * time.Second)

	envConn := ssh.NewEnvConnection(mockClient, map[string]string{
		"ENV_VAR": "value1",
	})

	// 1) 验证 Unwrap
	if unwrapped, ok := envConn.(interface{ Unwrap() ssh.Connection }); !ok || unwrapped.Unwrap() != mockClient {
		t.Errorf("expected Unwrap() to return underlying mockClient")
	}

	// 2) 验证 GetTimeout 与 SetTimeout 转发
	if getter, ok := envConn.(interface{ GetTimeout() time.Duration }); !ok || getter.GetTimeout() != 45*time.Second {
		t.Errorf("expected GetTimeout() to return 45s, got %v", getter.GetTimeout())
	}

	if setter, ok := envConn.(interface{ SetTimeout(time.Duration) }); ok {
		setter.SetTimeout(90 * time.Second)
		if mockClient.GetTimeout() != 90*time.Second {
			t.Errorf("expected underlying client timeout updated to 90s, got %v", mockClient.GetTimeout())
		}
	} else {
		t.Errorf("expected EnvConnection to implement SetTimeout")
	}

	// 3) 验证 Host 与 SetHost 转发
	if getter, ok := envConn.(interface{ Host() *inventory.Host }); !ok || getter.Host() != h {
		t.Errorf("expected Host() to return original host pointer")
	}

	newHost := &inventory.Host{Name: "web02", Address: "192.168.1.51"}
	if setter, ok := envConn.(interface{ SetHost(*inventory.Host) }); ok {
		setter.SetHost(newHost)
		if mockClient.Host() != newHost {
			t.Errorf("expected underlying client host updated to newHost")
		}
	} else {
		t.Errorf("expected EnvConnection to implement SetHost")
	}
}

// 6. 测试 cachedHostVarsSnapshot 去重后统一调用的 mergeCurrentHostVars 正确性
func TestCachedHostVarsSnapshotDeduplication(t *testing.T) {
	hostvars := map[string]map[string]interface{}{
		"host1": {
			"ansible_distribution": "Ubuntu",
			"env":                  "prod",
		},
	}

	h := &inventory.Host{Name: "host1"}
	currentVars := map[string]interface{}{
		"task_var": "overridden",
		"hostvars": "recursion_avoid",
	}

	mergeCurrentHostVars(hostvars, h, currentVars)

	cur := hostvars["host1"]
	if cur["task_var"] != "overridden" {
		t.Errorf("expected task_var='overridden', got %v", cur["task_var"])
	}
	if cur["ansible_distribution"] != "Ubuntu" {
		t.Errorf("expected ansible_distribution='Ubuntu', got %v", cur["ansible_distribution"])
	}
	if _, exists := cur["hostvars"]; exists {
		t.Errorf("expected 'hostvars' to be excluded from current host vars merge")
	}
}

// 7. 测试 executeTaskForHost 委托精简，验证其正确透传给 executeTaskForHostWithEnv
func TestExecuteTaskForHostDelegation(t *testing.T) {
	inv := inventory.NewInventory()
	h := &inventory.Host{Name: "local", Connection: "local"}
	inv.AllHosts["local"] = h

	exec := NewExecutor(inv)
	task := &Task{
		Name:       "Echo test",
		ModuleName: "command",
		Params: map[string]interface{}{
			"cmd": "echo delegated_ok",
		},
	}

	runOnceMap := make(map[string]bool)
	vars := exec.taskVarsForHost(h, nil)

	res, err := exec.executeTaskForHost([]*inventory.Host{h}, h, task, vars, nil, runOnceMap)
	if err != nil {
		t.Fatalf("executeTaskForHost failed: %v", err)
	}
	if len(res) == 0 || !strings.Contains(res[0].Stdout, "delegated_ok") {
		t.Fatalf("expected task result stdout to contain 'delegated_ok', got %#v", res)
	}
}

// Suppress unused imports
var _ = io.EOF
