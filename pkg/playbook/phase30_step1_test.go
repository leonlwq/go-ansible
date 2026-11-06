package playbook

import (
	"testing"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/module"
	"go-ansible/pkg/ssh"
)

// 1. 测试 INI [group:children] 状态机与前置声明组名不被误当作主机
func TestPhase30_INIChildrenForwardDeclaration(t *testing.T) {
	iniContent := `
[all:children]
web_servers
db_servers

[web_servers]
web1 ansible_connection=local
web2 ansible_connection=local

[db_servers]
db1 ansible_connection=local
`
	inv, err := inventory.ParseINI([]byte(iniContent))
	if err != nil {
		t.Fatalf("ParseINI failed: %v", err)
	}

	allGroup := inv.Groups["all"]
	if allGroup == nil {
		t.Fatalf("expected 'all' group to exist")
	}

	// 确认 web_servers 与 db_servers 是 all 的子组，而不是 all 组下的主机
	var hasChildWeb, hasChildDB bool
	for _, child := range allGroup.Children {
		if child.Name == "web_servers" {
			hasChildWeb = true
		}
		if child.Name == "db_servers" {
			hasChildDB = true
		}
	}
	if !hasChildWeb || !hasChildDB {
		t.Errorf("expected all.Children to contain web_servers and db_servers")
	}

	for _, h := range allGroup.Hosts {
		if h.Name == "web_servers" || h.Name == "db_servers" {
			t.Errorf("all.Hosts should NOT contain group names as host objects, found %s", h.Name)
		}
	}

	// 确认实际主机存在
	if inv.AllHosts["web1"] == nil || inv.AllHosts["web2"] == nil || inv.AllHosts["db1"] == nil {
		t.Errorf("expected actual hosts web1, web2, db1 in AllHosts")
	}

	// 确认组层级正确解析
	allHosts, err := inv.GetGroupHosts("all")
	if err != nil {
		t.Fatalf("GetGroupHosts('all') failed: %v", err)
	}
	if len(allHosts) != 3 {
		t.Errorf("expected 3 hosts recursively in all group, got %d", len(allHosts))
	}
}

// 2. 测试 Task/Block Become 三态指针 *bool 支持显式关闭 become: false
func TestPhase30_TaskBecomeFalseOverride(t *testing.T) {
	bTrue := true
	bFalse := false

	inv := inventory.NewInventory()
	h := &inventory.Host{
		Name:       "localhost",
		Connection: "local",
		Become:     false,
	}
	inv.AddHost(h)

	var recordedBecomes []bool

	exec := NewExecutor(inv)
	exec.registry.Register(&phase30CustomModule{
		name: "become_tracker",
		onExecute: func(client ssh.Connection, params map[string]interface{}) {
			isBecome, _ := params["become"].(bool)
			recordedBecomes = append(recordedBecomes, isBecome)
		},
	})

	play := &Play{
		Name:   "test play become override",
		Hosts:  "localhost",
		Become: true, // Play 级开启 become
		Tasks: []*Task{
			{
				Name:       "task 1: explicit become false",
				ModuleName: "become_tracker",
				Become:     &bFalse, // 显式关闭 become
			},
			{
				Name:       "task 2: inherit play become true",
				ModuleName: "become_tracker",
				Become:     nil, // 缺省，应继承 Play 级 become: true
			},
			{
				Name:       "task 3: explicit become true",
				ModuleName: "become_tracker",
				Become:     &bTrue, // 显式开启
			},
		},
	}

	res, err := exec.ExecutePlay(play)
	if err != nil {
		t.Fatalf("ExecutePlay failed: %v", err)
	}
	if res == nil {
		t.Fatalf("expected non-nil result")
	}

	if len(recordedBecomes) != 3 {
		t.Fatalf("expected 3 tasks executed, got %d", len(recordedBecomes))
	}

	// Task 1 必须为 false (被 become: false 显式覆盖)
	if recordedBecomes[0] != false {
		t.Errorf("task 1: expected Become == false when explicitly set to become: false, got true")
	}

	// Task 2 必须为 true (继承 Play 级 become: true)
	if recordedBecomes[1] != true {
		t.Errorf("task 2: expected Become == true when inherited from Play, got false")
	}

	// Task 3 必须为 true (显式 become: true)
	if recordedBecomes[2] != true {
		t.Errorf("task 3: expected Become == true when explicitly set to become: true, got false")
	}
}

// 3. 测试 Handlers 刷新严格对齐 Play 声明顺序
func TestPhase30_HandlersDefinitionOrder(t *testing.T) {
	inv := inventory.NewInventory()
	h := &inventory.Host{Name: "localhost", Connection: "local"}
	inv.AddHost(h)

	var executionOrder []string

	exec := NewExecutor(inv)
	exec.registry.Register(&phase30CustomModule{
		name: "order_tracker",
		onExecute: func(client ssh.Connection, params map[string]interface{}) {
			if id, ok := params["id"].(string); ok {
				executionOrder = append(executionOrder, id)
			}
		},
	})

	play := &Play{
		Name:  "handler order test",
		Hosts: "localhost",
		Tasks: []*Task{
			{
				Name:       "trigger task",
				ModuleName: "order_tracker",
				Params:     map[string]interface{}{"id": "main_task"},
				Notify:     []string{"handler_gamma", "handler_alpha"}, // 逆序触发
			},
		},
		Handlers: []*Handler{
			{
				Name:       "handler_alpha",
				ModuleName: "order_tracker",
				Params:     map[string]interface{}{"id": "h_alpha"},
			},
			{
				Name:       "handler_beta",
				ModuleName: "order_tracker",
				Params:     map[string]interface{}{"id": "h_beta"},
			},
			{
				Name:       "handler_gamma",
				ModuleName: "order_tracker",
				Params:     map[string]interface{}{"id": "h_gamma"},
			},
		},
	}

	result, err := exec.ExecutePlay(play)
	if err != nil {
		t.Fatalf("ExecutePlay failed: %v", err)
	}
	if result == nil {
		t.Fatalf("expected non-nil result")
	}

	// 确认主任务执行后，handler_alpha 在 handler_gamma 之前执行（严格对齐 Play 中的声明顺序）
	if len(executionOrder) != 3 {
		t.Fatalf("expected 3 tasks executed (main_task, h_alpha, h_gamma), got %v", executionOrder)
	}
	if executionOrder[0] != "main_task" {
		t.Errorf("expected executionOrder[0] == 'main_task', got %s", executionOrder[0])
	}
	if executionOrder[1] != "h_alpha" {
		t.Errorf("expected executionOrder[1] == 'h_alpha', got %s", executionOrder[1])
	}
	if executionOrder[2] != "h_gamma" {
		t.Errorf("expected executionOrder[2] == 'h_gamma', got %s", executionOrder[2])
	}
}

// 4. 测试 Special Tag 'never' 穿透防护：复合标签 tags: ['deploy', 'never'] 在 -t deploy 时必须跳过
func TestPhase30_NeverSpecialTagProtection(t *testing.T) {
	exec := NewExecutor(inventory.NewInventory())
	compoundTags := []string{"deploy", "never"}

	// 1. 无标签筛选时：含 never 必须跳过
	exec.SetTags(nil)
	if exec.shouldRunTaskByTags(compoundTags) != false {
		t.Errorf("expected shouldRunTaskByTags == false with nil tags on compound never task")
	}

	// 2. 指定 -t deploy 时：含 never 仍然必须跳过（不能被普通标签激活）
	exec.SetTags([]string{"deploy"})
	if exec.shouldRunTaskByTags(compoundTags) != false {
		t.Errorf("expected shouldRunTaskByTags == false with -t deploy on compound never task")
	}

	// 3. 指定 -t all 时：含 never 仍然必须跳过
	exec.SetTags([]string{"all"})
	if exec.shouldRunTaskByTags(compoundTags) != false {
		t.Errorf("expected shouldRunTaskByTags == false with -t all on compound never task")
	}

	// 4. 显式指定 -t never 时：允许执行
	exec.SetTags([]string{"never"})
	if exec.shouldRunTaskByTags(compoundTags) != true {
		t.Errorf("expected shouldRunTaskByTags == true with -t never")
	}

	// 5. 显式指定 -t "deploy,never" 时：允许执行
	exec.SetTags([]string{"deploy", "never"})
	if exec.shouldRunTaskByTags(compoundTags) != true {
		t.Errorf("expected shouldRunTaskByTags == true with -t deploy,never")
	}
}

type phase30CustomModule struct {
	name      string
	onExecute func(client ssh.Connection, params map[string]interface{})
}

func (m *phase30CustomModule) Name() string {
	return m.name
}

func (m *phase30CustomModule) Validate(params map[string]interface{}) error {
	return nil
}

func (m *phase30CustomModule) Execute(client ssh.Connection, params map[string]interface{}) (*module.Result, error) {
	if m.onExecute != nil {
		m.onExecute(client, params)
	}
	return &module.Result{
		Changed: true,
		Stdout:  "tracked",
	}, nil
}
