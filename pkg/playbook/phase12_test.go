package playbook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go-ansible/pkg/inventory"
)

// TestFactCachingPersistentFileAndTimeout 测试持久化 JSON 文件事实缓存、超时失效与 FlushCache
func TestFactCachingPersistentFileAndTimeout(t *testing.T) {
	cacheDir := t.TempDir()

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	e := NewExecutor(inv)
	defer e.Close()

	// 配置 2 秒超时
	e.SetFactCaching("jsonfile", cacheDir, 2)

	host, err := inv.GetHost("localhost")
	if err != nil {
		t.Fatalf("expected localhost host in inventory: %v", err)
	}
	hosts := []*inventory.Host{host}

	// 1. 首次收集事实
	e.gatherFacts(hosts)

	cacheFile := filepath.Join(cacheDir, "localhost.json")
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		t.Fatalf("expected cache file %s to be created, err: %v", cacheFile, err)
	}

	var cached map[string]interface{}
	if err := json.Unmarshal(data, &cached); err != nil {
		t.Fatalf("failed to parse cached json: %v", err)
	}
	if len(cached) == 0 {
		t.Fatal("cached facts should not be empty")
	}

	// 2. 模拟修改缓存内容，注入自定义标记字段
	cached["ansible_test_custom_marker"] = "phase12_cached_ok"
	updatedData, err := json.MarshalIndent(cached, "", "  ")
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	if err := os.WriteFile(cacheFile, updatedData, 0644); err != nil {
		t.Fatalf("write cache error: %v", err)
	}

	// 清空内存事实，强制重新读取缓存文件
	e.mu.Lock()
	e.facts = make(map[string]map[string]interface{})
	e.mu.Unlock()

	// 再次 gatherFacts，应该直接命中本地 json 缓存文件
	e.gatherFacts(hosts)

	e.mu.RLock()
	reloaded := e.facts["localhost"]
	e.mu.RUnlock()
	if reloaded == nil {
		t.Fatal("expected facts to be loaded from cache file")
	}
	if reloaded["ansible_test_custom_marker"] != "phase12_cached_ok" {
		t.Fatalf("expected custom marker from cache file, got: %v", reloaded["ansible_test_custom_marker"])
	}

	// 3. 测试超时失效
	// 人工修改文件时间戳为 5 秒前
	pastTime := time.Now().Add(-5 * time.Second)
	if err := os.Chtimes(cacheFile, pastTime, pastTime); err != nil {
		t.Fatalf("chtimes error: %v", err)
	}

	if _, found := e.getCachedFacts("localhost"); found {
		t.Fatal("expected expired cached facts to return found=false")
	}

	// 4. 测试 FlushCache
	e.FlushCache()
	if _, err := os.Stat(cacheFile); !os.IsNotExist(err) {
		t.Fatalf("expected cache file to be removed after FlushCache, but got err: %v", err)
	}
	e.mu.RLock()
	factCount := len(e.facts)
	e.mu.RUnlock()
	if factCount != 0 {
		t.Fatalf("expected in-memory facts to be empty after FlushCache, got: %d", factCount)
	}
}

// TestFactCachingMemory 测试内存事实缓存与超时
func TestFactCachingMemory(t *testing.T) {
	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	e := NewExecutor(inv)
	defer e.Close()

	e.SetFactCaching("memory", "", 1)

	// 注入事实
	e.saveCachedFacts("localhost", map[string]interface{}{
		"ansible_mem_test": "val123",
	})

	cached, found := e.getCachedFacts("localhost")
	if !found || cached["ansible_mem_test"] != "val123" {
		t.Fatalf("expected memory cache hit, got: %v, %v", found, cached)
	}

	// 人工修改时间戳为 2 秒前测试超时
	e.mu.Lock()
	e.factCacheTimestamps["localhost"] = time.Now().Add(-2 * time.Second)
	e.mu.Unlock()

	if _, found := e.getCachedFacts("localhost"); found {
		t.Fatal("expected memory cache to be expired")
	}

	e.FlushCache()
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.facts) != 0 || len(e.factCacheTimestamps) != 0 {
		t.Fatal("expected empty memory facts after FlushCache")
	}
}

// TestAnsibleLoopVariables 测试循环上下文内置的 ansible_loop 变量
func TestAnsibleLoopVariables(t *testing.T) {
	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	e := NewExecutor(inv)
	defer e.Close()

	falseVal := false
	play := &Play{
		Name:        "Test ansible_loop Context Variables",
		Hosts:       "localhost",
		GatherFacts: &falseVal,
		Tasks: []*Task{
			{
				Name:       "Inspect loop vars for {{ item }}",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "item={{ item }} idx={{ ansible_loop.index }} idx0={{ ansible_loop.index0 }} first={{ ansible_loop.first }} last={{ ansible_loop.last }} len={{ ansible_loop.length }} rev={{ ansible_loop.revindex }} rev0={{ ansible_loop.revindex0 }}",
				},
				Loop: []interface{}{"apple", "banana", "cherry"},
				LoopControl: LoopControl{
					Extended: true,
				},
			},
		},
	}

	pb := &Playbook{Plays: []*Play{play}}
	res, err := e.Execute(pb)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	hostRes := res.Plays[0].Hosts["localhost"]
	if len(hostRes.Tasks) != 3 {
		t.Fatalf("expected 3 item tasks, got %d", len(hostRes.Tasks))
	}

	// 验证第 1 项 (apple)
	msg0 := hostRes.Tasks[0].Message
	expected0 := "item=apple idx=1 idx0=0 first=true last=false len=3 rev=3 rev0=2"
	if msg0 != expected0 {
		t.Errorf("item 0 mismatch: expected %q, got %q", expected0, msg0)
	}

	// 验证第 2 项 (banana)
	msg1 := hostRes.Tasks[1].Message
	expected1 := "item=banana idx=2 idx0=1 first=false last=false len=3 rev=2 rev0=1"
	if msg1 != expected1 {
		t.Errorf("item 1 mismatch: expected %q, got %q", expected1, msg1)
	}

	// 验证第 3 项 (cherry)
	msg2 := hostRes.Tasks[2].Message
	expected2 := "item=cherry idx=3 idx0=2 first=false last=true len=3 rev=1 rev0=0"
	if msg2 != expected2 {
		t.Errorf("item 2 mismatch: expected %q, got %q", expected2, msg2)
	}
}

// TestLoopWhenCondition 测试循环任务中的 when 条件按 item 级生效
func TestLoopWhenCondition(t *testing.T) {
	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	e := NewExecutor(inv)
	defer e.Close()

	falseVal := false
	play := &Play{
		Name:        "Test Loop with When",
		Hosts:       "localhost",
		GatherFacts: &falseVal,
		Tasks: []*Task{
			{
				Name:       "Run only on first item",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "first item={{ item }}",
				},
				Loop: []interface{}{"alpha", "beta", "gamma"},
				When: "ansible_loop.first",
			},
			{
				Name:       "Skip item beta",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "processed {{ item }}",
				},
				Loop: []interface{}{"alpha", "beta", "gamma"},
				When: "item != 'beta'",
			},
		},
	}

	pb := &Playbook{Plays: []*Play{play}}
	res, err := e.Execute(pb)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	tasks := res.Plays[0].Hosts["localhost"].Tasks
	if len(tasks) != 6 {
		t.Fatalf("expected 6 item task results, got %d", len(tasks))
	}

	// Task 1: only first item should run, 2 and 3 skipped
	if tasks[0].Skipped {
		t.Errorf("expected task 1 item 0 to run, but was skipped")
	}
	if !tasks[1].Skipped {
		t.Errorf("expected task 1 item 1 to be skipped")
	}
	if !tasks[2].Skipped {
		t.Errorf("expected task 1 item 2 to be skipped")
	}

	// Task 2: alpha and gamma should run, beta skipped
	if tasks[3].Skipped {
		t.Errorf("expected task 2 item 0 (alpha) to run")
	}
	if !tasks[4].Skipped {
		t.Errorf("expected task 2 item 1 (beta) to be skipped")
	}
	if tasks[5].Skipped {
		t.Errorf("expected task 2 item 2 (gamma) to run")
	}
}
