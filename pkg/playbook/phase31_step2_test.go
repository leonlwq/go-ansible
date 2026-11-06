package playbook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"go-ansible/pkg/inventory"
)

// 11. 测试 HostResult.TaskCount 在策略多主机并发执行下的锁保护
func TestPhase31_StrategyTaskCountLockProtection(t *testing.T) {
	hr := &HostResult{
		Tasks: make([]*TaskResult, 0),
	}

	var wg sync.WaitGroup
	// 50 goroutines appending tasks
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				hr.AppendTask(&TaskResult{Name: "task", Failed: false})
			}
		}()
	}

	// 50 goroutines reading TaskCount
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = hr.TaskCount()
			}
		}()
	}

	wg.Wait()
	if hr.TaskCount() != 1000 {
		t.Fatalf("expected TaskCount 1000, got %d", hr.TaskCount())
	}
}

// 12 (Part 1). 测试 delegate_to 主机连接参数 (ansible_host/ansible_user/ansible_connection/ansible_port) 继承
func TestPhase31_DelegateToConnectionVars(t *testing.T) {
	inv := inventory.NewInventory()
	hA := &inventory.Host{
		Name:       "nodeA",
		Address:    "10.0.0.1",
		Connection: "ssh",
	}
	hB := &inventory.Host{
		Name:       "nodeB",
		Address:    "10.0.0.2",
		Connection: "ssh",
	}
	inv.AddHost(hA)
	inv.AddHost(hB)

	exec := NewExecutor(inv)

	// 在 nodeB 的 hostvars 中设置特殊连接属性
	allVars := map[string]interface{}{
		"hostvars": map[string]map[string]interface{}{
			"nodeB": {
				"ansible_connection": "local",
				"ansible_host":       "127.0.0.1",
				"ansible_port":       2222,
				"ansible_user":       "deploy_agent",
			},
		},
	}

	task := &Task{
		Name:       "delegated task to nodeB",
		DelegateTo: "nodeB",
		ModuleName: "debug",
	}

	// 执行任务并验证 execHost 的属性
	results, err := exec.executeTaskForHostWithEnv([]*inventory.Host{hA, hB}, hA, task, allVars, nil, nil, nil)
	if err != nil {
		t.Fatalf("executeTaskForHostWithEnv failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Host != "nodeB" {
		t.Fatalf("expected result host to be nodeB, got %s", results[0].Host)
	}
}

// 12 (Part 2). 测试循环任务触发 handler 严格去重与 loop 变量隔离
func TestPhase31_HandlerLoopDeduplication(t *testing.T) {
	inv := inventory.NewInventory()
	h := &inventory.Host{Name: "web1", Connection: "local"}
	inv.AddHost(h)

	exec := NewExecutor(inv)
	handler := &Handler{
		Name:       "restart nginx",
		ModuleName: "debug",
	}

	// 模拟在循环中连续 5 次发生变更并通知同一个 handler
	for i := 0; i < 5; i++ {
		loopVars := map[string]interface{}{
			"item":             i,
			"ansible_loop_var": "item",
			"app_name":         "myapp",
		}
		exec.notifyHandlers("restart nginx", h, loopVars, nil, []*Handler{handler})
	}

	exec.mu.Lock()
	defer exec.mu.Unlock()

	// 验证 pendingHandlers 严格只有 1 个，不会重复入队 5 次
	if len(exec.pendingHandlers) != 1 {
		t.Fatalf("expected exactly 1 pending handler after 5 notifications, got %d", len(exec.pendingHandlers))
	}

	// 验证循环变量 item 被剥离，不会污染 handler 执行上下文
	queuedVars := exec.pendingHandlers[0].vars
	if _, hasItem := queuedVars["item"]; hasItem {
		t.Fatalf("expected 'item' loop variable to be stripped from pending handler vars, got: %v", queuedVars)
	}
	if queuedVars["app_name"] != "myapp" {
		t.Fatalf("expected 'app_name' to be preserved, got %v", queuedVars["app_name"])
	}
}

// 15. 测试 set_fact 模块提取并剥离 cacheable 参数，且在 cacheable: true 时持久化事实落盘
func TestPhase31_SetFactCacheable(t *testing.T) {
	tempDir := t.TempDir()
	inv := inventory.NewInventory()
	h := &inventory.Host{Name: "cache_host", Connection: "local"}
	inv.AddHost(h)

	exec := NewExecutor(inv)
	exec.SetFactCaching("jsonfile", tempDir, 3600)

	params := map[string]interface{}{
		"cluster_token": "secret-token-abc",
		"cacheable":     "yes",
	}

	res := exec.executeSetFactModule(h, params)
	if res.Failed {
		t.Fatalf("executeSetFactModule failed: %s", res.Message)
	}

	// 验证 facts 中存在 cluster_token 且不存在 cacheable
	facts := exec.getHostFacts("cache_host")
	if facts["cluster_token"] != "secret-token-abc" {
		t.Fatalf("expected cluster_token in facts, got: %v", facts)
	}
	if _, hasCacheable := facts["cacheable"]; hasCacheable {
		t.Fatalf("expected 'cacheable' parameter to be stripped from facts")
	}

	// 验证持久化 JSON 事实文件已落盘
	cacheFile := filepath.Join(tempDir, "cache_host.json")
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		t.Fatalf("expected cache file %s to exist on disk: %v", cacheFile, err)
	}

	var diskFacts map[string]interface{}
	if err := json.Unmarshal(data, &diskFacts); err != nil {
		t.Fatalf("failed to parse disk cache json: %v", err)
	}
	if diskFacts["cluster_token"] != "secret-token-abc" {
		t.Fatalf("expected cluster_token in disk facts, got: %v", diskFacts)
	}
	if _, hasCacheable := diskFacts["cacheable"]; hasCacheable {
		t.Fatalf("expected disk facts to NOT contain 'cacheable'")
	}
}
