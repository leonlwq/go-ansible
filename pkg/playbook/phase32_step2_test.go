package playbook

import (
	"fmt"
	"sync"
	"testing"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/module"
	"go-ansible/pkg/ssh"
)

// 1. 验证 block 内部错误时，不会提前逃逸，而是保证 rescue 得到执行，且 always 必然执行
func TestPhase32_BlockRescueAlways_ErrorInBlockExecutedRescueAndAlways(t *testing.T) {
	registry := module.NewRegistry()

	// 注册模拟模块
	registry.Register(&testFailModule{})
	registry.Register(&testSuccessModule{})

	executor := NewExecutor(nil)
	executor.registry = registry

	host := &inventory.Host{
		Name:      "localhost",
		Variables: map[string]string{"ansible_connection": "local"},
	}
	allHosts := []*inventory.Host{host}

	// 构造 Block 任务：Block 内第 1 个任务成功，第 2 个任务失败；Rescue 成功；Always 成功
	blockTask := &Task{
		Name: "Test Block Execution",
		Block: []*Task{
			{
				Name:       "Block Step 1 Success",
				ModuleName: "test_success",
				Params:     map[string]interface{}{"msg": "step1"},
			},
			{
				Name:       "Block Step 2 Failure",
				ModuleName: "test_fail",
				Params:     map[string]interface{}{"msg": "fail inside block"},
			},
			{
				Name:       "Block Step 3 Should Be Skipped",
				ModuleName: "test_success",
				Params:     map[string]interface{}{"msg": "step3"},
			},
		},
		Rescue: []*Task{
			{
				Name:       "Rescue Step 1 Recover",
				ModuleName: "test_success",
				Params:     map[string]interface{}{"msg": "rescued successfully"},
			},
		},
		Always: []*Task{
			{
				Name:       "Always Step Clean Up",
				ModuleName: "test_success",
				Params:     map[string]interface{}{"msg": "always cleanup executed"},
			},
		},
	}

	hostResult := &HostResult{}
	err := executor.executeBlockTaskForHost(allHosts, host, blockTask, make(map[string]interface{}), nil, make(map[string]bool), nil, hostResult)
	if err != nil {
		t.Fatalf("expected block to be rescued without error, got err: %v", err)
	}

	tasks := hostResult.GetTasks()
	// 期望执行的任务列表：
	// 1: Block Step 1 Success (Success)
	// 2: Block Step 2 Failure (Failed)
	// (Step 3 Skipped)
	// 3: Rescue Step 1 Recover (Success, Rescued)
	// 4: Always Step Clean Up (Success)
	if len(tasks) != 4 {
		t.Fatalf("expected 4 tasks executed in block lifecycle, got %d", len(tasks))
	}

	if tasks[0].Name != "Block Step 1 Success" || tasks[0].Failed {
		t.Errorf("task 0 expected success, got: %+v", tasks[0])
	}
	if tasks[1].Name != "Block Step 2 Failure" || !tasks[1].Failed {
		t.Errorf("task 1 expected failure, got: %+v", tasks[1])
	}
	if tasks[2].Name != "Rescue Step 1 Recover" || tasks[2].Failed {
		t.Errorf("task 2 expected rescue success, got: %+v", tasks[2])
	}
	if tasks[3].Name != "Always Step Clean Up" || tasks[3].Failed {
		t.Errorf("task 3 expected always cleanup success, got: %+v", tasks[3])
	}

	// 验证最终宿主失败状态已被清除（Rescued）
	if executor.isHostFailed(host.Name) {
		t.Errorf("expected host to NOT be in failed state after rescue")
	}
}

// 2. 验证即便 Rescue 也失败，Always 仍然必须无条件执行
func TestPhase32_BlockRescueAlways_AlwaysRunsEvenIfRescueFails(t *testing.T) {
	registry := module.NewRegistry()
	registry.Register(&testFailModule{})
	registry.Register(&testSuccessModule{})

	executor := NewExecutor(nil)
	executor.registry = registry

	host := &inventory.Host{
		Name:      "localhost",
		Variables: map[string]string{"ansible_connection": "local"},
	}
	allHosts := []*inventory.Host{host}

	blockTask := &Task{
		Name: "Test Always Guaranteed",
		Block: []*Task{
			{
				Name:       "Block Fail",
				ModuleName: "test_fail",
			},
		},
		Rescue: []*Task{
			{
				Name:       "Rescue Fail",
				ModuleName: "test_fail",
			},
		},
		Always: []*Task{
			{
				Name:       "Always Clean",
				ModuleName: "test_success",
			},
		},
	}

	hostResult := &HostResult{}
	_ = executor.executeBlockTaskForHost(allHosts, host, blockTask, make(map[string]interface{}), nil, make(map[string]bool), nil, hostResult)

	tasks := hostResult.GetTasks()
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks (block, rescue, always), got %d", len(tasks))
	}
	if tasks[2].Name != "Always Clean" || tasks[2].Failed {
		t.Errorf("expected Always Clean to execute and succeed even after Rescue failed, got %+v", tasks[2])
	}
}

// 3. 验证 Handler.Listen 支持多广播主题列表解析与触发
func TestPhase32_HandlerListensList(t *testing.T) {
	handler := &Handler{
		Name: "multi topic handler",
	}
	raw := map[string]interface{}{
		"name":   "multi topic handler",
		"listen": []interface{}{"service_reload", "nginx_reload", "config_updated"},
	}
	extractHandlerCommonFields(raw, handler)

	if len(handler.Listens) != 3 {
		t.Fatalf("expected 3 listens topics, got %d: %v", len(handler.Listens), handler.Listens)
	}

	// 匹配各个主题
	if !handlerMatchesName(handler, "service_reload") {
		t.Errorf("expected match for 'service_reload'")
	}
	if !handlerMatchesName(handler, "nginx_reload") {
		t.Errorf("expected match for 'nginx_reload'")
	}
	if !handlerMatchesName(handler, "config_updated") {
		t.Errorf("expected match for 'config_updated'")
	}
	if handlerMatchesName(handler, "unrelated_topic") {
		t.Errorf("expected no match for 'unrelated_topic'")
	}
}

// 4. 验证 buildGroupsMap 高并发读取与动态失效互斥安全，消除 Data Race
func TestPhase32_BuildGroupsMapConcurrencySafety(t *testing.T) {
	inv := inventory.NewInventory()
	inv.Groups["web"] = &inventory.Group{
		Name: "web",
		Hosts: []*inventory.Host{
			{Name: "web1"},
			{Name: "web2"},
		},
	}
	inv.AllHosts["web1"] = &inventory.Host{Name: "web1"}
	inv.AllHosts["web2"] = &inventory.Host{Name: "web2"}

	executor := NewExecutor(inv)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			gm := executor.buildGroupsMap()
			if len(gm["web"]) != 2 {
				t.Errorf("expected 2 web hosts, got %d", len(gm["web"]))
			}
			if id%10 == 0 {
				executor.InvalidateGroupsCache()
			}
		}(i)
	}
	wg.Wait()

	finalMap := executor.buildGroupsMap()
	if len(finalMap["web"]) != 2 {
		t.Fatalf("expected 2 web hosts in final map, got %d", len(finalMap["web"]))
	}
}

// 5. 验证 Free 策略下 --step 单步交互生效，用户拒绝时任务跳过
func TestPhase32_StepModeInFreeStrategy(t *testing.T) {
	registry := module.NewRegistry()
	registry.Register(&testSuccessModule{})

	executor := NewExecutor(nil)
	executor.registry = registry
	executor.stepMode = true

	promptCount := 0
	executor.stepPromptFunc = func(taskName string) (string, error) {
		promptCount++
		return "n", nil // 用户输入 "n" 拒绝执行
	}

	host := &inventory.Host{
		Name:      "localhost",
		Variables: map[string]string{"ansible_connection": "local"},
	}
	allHosts := []*inventory.Host{host}
	tasks := []*Task{
		{
			Name:       "Interactive Task 1",
			ModuleName: "test_success",
		},
	}

	result := &PlayResult{
		Name:  "Free Strategy Step Test",
		Hosts: map[string]*HostResult{"localhost": {}},
	}

	err := executor.executeTasksFreeStrategy(allHosts, allHosts, tasks, make(map[string]interface{}), nil, make(map[string]bool), nil, result, false, 5)
	if err != nil {
		t.Fatalf("executeTasksFreeStrategy failed: %v", err)
	}

	if promptCount != 1 {
		t.Fatalf("expected promptCount == 1, got %d", promptCount)
	}

	hr := result.Hosts["localhost"]
	allTasks := hr.GetTasks()
	if len(allTasks) != 1 || !allTasks[0].Skipped {
		t.Fatalf("expected task to be skipped due to --step refusal, got: %+v", allTasks)
	}
}

// 6. 验证 flush_handlers 多主机批次互斥执行无并发冲突
func TestPhase32_FlushHandlersMutualExclusion(t *testing.T) {
	executor := NewExecutor(nil)
	result := &PlayResult{
		Name:  "Flush Handlers Test",
		Hosts: make(map[string]*HostResult),
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = executor.flushHandlers(result)
		}()
	}
	wg.Wait()
}

// 辅助测试模块
type testSuccessModule struct{}

func (m *testSuccessModule) Name() string { return "test_success" }
func (m *testSuccessModule) Validate(params map[string]interface{}) error {
	return nil
}
func (m *testSuccessModule) Execute(client ssh.Connection, params map[string]interface{}) (*module.Result, error) {
	return &module.Result{
		Changed: false,
		Stdout:  "ok",
	}, nil
}

type testFailModule struct{}

func (m *testFailModule) Name() string { return "test_fail" }
func (m *testFailModule) Validate(params map[string]interface{}) error {
	return nil
}
func (m *testFailModule) Execute(client ssh.Connection, params map[string]interface{}) (*module.Result, error) {
	return &module.Result{
		Failed:   true,
		ExitCode: 1,
		Stderr:   "simulated module execution error",
		Message:  "command failed with rc 1",
	}, fmt.Errorf("command failed with rc 1")
}
