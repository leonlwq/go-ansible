package playbook

import (
	"sync"
	"testing"

	"go-ansible/pkg/inventory"
)

// 1. 验证动态 Jinja2 模板化 notify Handlers 解析与触发 (单个变量与循环 item)
func TestPhase37_DynamicNotifyHandlers(t *testing.T) {
	inv := inventory.NewInventory()
	h := &inventory.Host{Name: "web1"}
	inv.AddHost(h)

	exec := NewExecutor(inv)

	hNginx := &Handler{Name: "restart nginx"}
	hRedis := &Handler{Name: "restart redis"}
	handlers := []*Handler{hNginx, hRedis}

	// (a) notify: "restart {{ service_name }}"
	vars := map[string]interface{}{
		"service_name": "nginx",
	}
	exec.notifyHandlers("restart {{ service_name }}", h, vars, nil, handlers)

	exec.mu.RLock()
	if len(exec.pendingHandlers) != 1 {
		t.Fatalf("expected 1 pending handler, got %d", len(exec.pendingHandlers))
	}
	if exec.pendingHandlers[0].handler.Name != "restart nginx" {
		t.Fatalf("expected handler 'restart nginx', got: %s", exec.pendingHandlers[0].handler.Name)
	}
	exec.mu.RUnlock()

	// (b) 循环任务中的动态 notify: "restart {{ item }}"
	loopVars := map[string]interface{}{
		"item": "redis",
	}
	exec.notifyHandlers("restart {{ item }}", h, loopVars, nil, handlers)

	exec.mu.RLock()
	if len(exec.pendingHandlers) != 2 {
		t.Fatalf("expected 2 pending handlers, got %d", len(exec.pendingHandlers))
	}
	if exec.pendingHandlers[1].handler.Name != "restart redis" {
		t.Fatalf("expected handler 'restart redis', got: %s", exec.pendingHandlers[1].handler.Name)
	}
	exec.mu.RUnlock()

	// (c) 重复 notify 去重校验
	exec.notifyHandlers("restart {{ service_name }}", h, vars, nil, handlers)
	exec.mu.RLock()
	if len(exec.pendingHandlers) != 2 {
		t.Fatalf("expected duplicate notify to be deduplicated, got %d", len(exec.pendingHandlers))
	}
	exec.mu.RUnlock()
}

// 2. 验证 Handler 本身 Name/Listen 包含变量模板时的动态匹配触发
func TestPhase37_TemplatedHandlerNameMatch(t *testing.T) {
	inv := inventory.NewInventory()
	h := &inventory.Host{Name: "node1"}
	inv.AddHost(h)

	exec := NewExecutor(inv)

	hDynamic := &Handler{Name: "reload {{ app_name }}"}
	hListen := &Handler{Name: "restart all", Listen: "notify_{{ cluster_env }}"}
	handlers := []*Handler{hDynamic, hListen}

	vars := map[string]interface{}{
		"app_name":    "payment",
		"cluster_env": "production",
	}

	// 精确匹配动态 Handler Name
	exec.notifyHandlers("reload payment", h, vars, nil, handlers)
	// 广播匹配动态 Listen
	exec.notifyHandlers("notify_production", h, vars, nil, handlers)

	exec.mu.RLock()
	defer exec.mu.RUnlock()
	if len(exec.pendingHandlers) != 2 {
		t.Fatalf("expected 2 pending handlers for templated names, got %d", len(exec.pendingHandlers))
	}
	if exec.pendingHandlers[0].handler.Name != "reload {{ app_name }}" {
		t.Errorf("expected reload {{ app_name }}, got %s", exec.pendingHandlers[0].handler.Name)
	}
	if exec.pendingHandlers[1].handler.Name != "restart all" {
		t.Errorf("expected restart all, got %s", exec.pendingHandlers[1].handler.Name)
	}
}

// 3. 验证循环任务动态委派 delegate_to: "{{ item }}" 正确路由到目标主机
func TestPhase37_DynamicLoopDelegation(t *testing.T) {
	inv := inventory.NewInventory()
	targetHost := &inventory.Host{Name: "control_node"}
	worker1 := &inventory.Host{Name: "worker-1", Address: "192.168.1.11"}
	worker2 := &inventory.Host{Name: "worker-2", Address: "192.168.1.12"}
	inv.AddHost(targetHost)
	inv.AddHost(worker1)
	inv.AddHost(worker2)

	exec := NewExecutor(inv)

	task := &Task{
		Name:       "Check worker status",
		ModuleName: "debug",
		Params: map[string]interface{}{
			"msg": "status of {{ item }}",
		},
		Loop:       []interface{}{"worker-1", "worker-2"},
		DelegateTo: "{{ item }}",
	}

	playVars := map[string]interface{}{
		"ansible_play_hosts": []string{"control_node"},
	}

	results, err := exec.executeTask(targetHost, task, playVars, nil)
	if err != nil {
		t.Fatalf("executeTask failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// 核心断言：各循环项必须动态路由到对应的主机，而非全部退化为 control_node
	if results[0].Host != "worker-1" {
		t.Errorf("expected item 0 delegated to worker-1, got %s", results[0].Host)
	}
	if results[1].Host != "worker-2" {
		t.Errorf("expected item 1 delegated to worker-2, got %s", results[1].Host)
	}
}

// 4. 验证任务执行发生错误或 Task Error (err != nil) 时，主机会被精准标记为 markHostFailed
func TestPhase37_TaskErrorMarksHostFailed(t *testing.T) {
	inv := inventory.NewInventory()
	h1 := &inventory.Host{Name: "srv1"}
	h2 := &inventory.Host{Name: "srv2"}
	h3 := &inventory.Host{Name: "srv3"}
	inv.AddHost(h1)
	inv.AddHost(h2)
	inv.AddHost(h3)

	exec := NewExecutor(inv)

	// (a) 测试模块失败 (fail module 返回 Failed=true)
	failingTask := &Task{
		Name:       "Fail immediately",
		ModuleName: "fail",
		Params: map[string]interface{}{
			"msg": "critical cluster failure",
		},
	}
	playResult := &PlayResult{
		Hosts: make(map[string]*HostResult),
	}
	_ = exec.executeTaskForBatchHosts([]*inventory.Host{h1}, []*inventory.Host{h1}, failingTask, nil, nil, nil, nil, playResult, 1)
	if !exec.isHostFailed("srv1") {
		t.Errorf("expected srv1 to be marked failed from fail module")
	}

	// (b) 测试底层任务抛出 Go error (如 include_tasks 不存在的文件导致 err != nil)
	// 在 Linear 批次多协程并发路径下验证 markHostFailed
	errorTask := &Task{
		Name:       "Include missing file",
		ModuleName: "include_tasks",
		Params: map[string]interface{}{
			"_raw_params": "non_existent_path_xyz_999.yml",
		},
	}
	playResult2 := &PlayResult{
		Hosts: make(map[string]*HostResult),
	}
	errLinear := exec.executeTaskForBatchHosts([]*inventory.Host{h2}, []*inventory.Host{h2}, errorTask, nil, nil, nil, nil, playResult2, 2)
	if errLinear == nil {
		t.Fatalf("expected error from missing include_tasks")
	}
	if !exec.isHostFailed("srv2") {
		t.Errorf("expected srv2 to be marked failed in linear batch execution on err != nil")
	}

	// (c) 在 Free 策略并发路径下验证 markHostFailed
	execFree := NewExecutor(inv)
	playResult3 := &PlayResult{
		Hosts: make(map[string]*HostResult),
	}
	errFree := execFree.executeTasksFreeStrategy([]*inventory.Host{h3}, []*inventory.Host{h3}, []*Task{errorTask}, nil, nil, nil, nil, playResult3, false, 2)
	if errFree == nil {
		t.Fatalf("expected error from missing include_tasks in free strategy")
	}
	if !execFree.isHostFailed("srv3") {
		t.Errorf("expected srv3 to be marked failed in free strategy on err != nil")
	}
}

// 5. 验证 getTaskThrottleSem 高并发 RLock 读保护与无冲突获取
func TestPhase37_TaskThrottleSemConcurrency(t *testing.T) {
	exec := NewExecutor(inventory.NewInventory())
	task := &Task{
		Name:     "Throttled task",
		Throttle: 3,
	}

	sem1 := exec.getTaskThrottleSem(task)
	if sem1 == nil || cap(sem1) != 3 {
		t.Fatalf("expected sem with cap 3, got %v", sem1)
	}

	// 多 goroutine 并发读取同一个任务的信号量
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem := exec.getTaskThrottleSem(task)
			if sem != sem1 {
				t.Errorf("expected same semaphore instance")
			}
		}()
	}
	wg.Wait()
}
