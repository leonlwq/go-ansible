package playbook

import (
	"testing"

	"go-ansible/pkg/inventory"
)

// 1. 验证 Inventory.HostCount() 极轻量计数与 buildHostVarsSnapshot 快路径复用
func TestPhase38_InventoryHostCountAndSnapshotFastPath(t *testing.T) {
	inv := inventory.NewInventory()
	if count := inv.HostCount(); count != 0 {
		t.Fatalf("expected host count 0, got %d", count)
	}

	h1 := &inventory.Host{Name: "host1", Variables: map[string]string{"var1": "val1"}}
	h2 := &inventory.Host{Name: "host2", Variables: map[string]string{"var2": "val2"}}
	inv.AddHost(h1)
	inv.AddHost(h2)

	if count := inv.HostCount(); count != 2 {
		t.Fatalf("expected host count 2, got %d", count)
	}

	exec := NewExecutor(inv)
	// 首次调用构建快照 (慢路径)
	snap1 := exec.buildHostVarsSnapshot(h1, map[string]interface{}{"task_var": "t1"})
	if snap1 == nil || len(snap1) != 2 {
		t.Fatalf("expected 2 hosts in snap1, got %d", len(snap1))
	}
	if snap1["host1"]["task_var"] != "t1" {
		t.Fatalf("expected host1 task_var to be 't1', got %v", snap1["host1"]["task_var"])
	}

	// 二次调用命中无锁/读锁快路径（无 GetAllHosts 排序损耗）
	snap2 := exec.buildHostVarsSnapshot(h2, map[string]interface{}{"task_var": "t2"})
	if snap2 == nil || len(snap2) != 2 {
		t.Fatalf("expected 2 hosts in snap2, got %d", len(snap2))
	}
	if snap2["host2"]["task_var"] != "t2" {
		t.Fatalf("expected host2 task_var to be 't2', got %v", snap2["host2"]["task_var"])
	}
}

// 2. 验证 action: 内联字符串格式任务解析 (action: copy src=foo dest=bar)
func TestPhase38_ActionInlineString(t *testing.T) {
	raw := rawTask{
		"name":   "Test inline action",
		"action": "copy src=foo.txt dest=/tmp/bar.txt",
	}

	task, err := normalizeTask(raw)
	if err != nil {
		t.Fatalf("normalizeTask failed: %v", err)
	}

	if task.ModuleName != "copy" {
		t.Fatalf("expected moduleName 'copy', got %s", task.ModuleName)
	}
	if task.Params["src"] != "foo.txt" {
		t.Fatalf("expected src 'foo.txt', got %v", task.Params["src"])
	}
	if task.Params["dest"] != "/tmp/bar.txt" {
		t.Fatalf("expected dest '/tmp/bar.txt', got %v", task.Params["dest"])
	}
}

// 3. 验证 action: 字典结构格式任务与 args: 合并解析
func TestPhase38_ActionDictAndArgs(t *testing.T) {
	raw := rawTask{
		"name": "Test dict action with args",
		"action": map[string]interface{}{
			"module": "template",
			"src":    "app.conf.j2",
			"dest":   "/etc/app.conf",
		},
		"args": map[string]interface{}{
			"mode":  "0644",
			"owner": "root",
		},
	}

	task, err := normalizeTask(raw)
	if err != nil {
		t.Fatalf("normalizeTask failed: %v", err)
	}

	if task.ModuleName != "template" {
		t.Fatalf("expected moduleName 'template', got %s", task.ModuleName)
	}
	if task.Params["src"] != "app.conf.j2" {
		t.Fatalf("expected src 'app.conf.j2', got %v", task.Params["src"])
	}
	if task.Params["mode"] != "0644" {
		t.Fatalf("expected mode '0644' from args, got %v", task.Params["mode"])
	}
	if task.Params["owner"] != "root" {
		t.Fatalf("expected owner 'root' from args, got %v", task.Params["owner"])
	}
}

// 4. 验证 local_action: 字典结构格式与 localhost 委派自动配置
func TestPhase38_LocalActionDict(t *testing.T) {
	raw := rawTask{
		"name": "Test local_action dict",
		"local_action": map[string]interface{}{
			"module": "shell",
			"cmd":    "echo 'running locally'",
		},
	}

	task, err := normalizeTask(raw)
	if err != nil {
		t.Fatalf("normalizeTask failed: %v", err)
	}

	if task.ModuleName != "shell" {
		t.Fatalf("expected moduleName 'shell', got %s", task.ModuleName)
	}
	if task.DelegateTo != "localhost" {
		t.Fatalf("expected DelegateTo 'localhost', got %s", task.DelegateTo)
	}
	if task.Params["cmd"] != "echo 'running locally'" {
		t.Fatalf("expected cmd 'echo 'running locally'', got %v", task.Params["cmd"])
	}
}

// 5. 验证 Handler 中 action: 语法糖解析与广播支持
func TestPhase38_HandlerActionSupport(t *testing.T) {
	raw := rawTask{
		"name":   "restart service",
		"listen": "service_reload",
		"action": "service name=nginx state=restarted",
	}

	handler, err := normalizeHandler(raw)
	if err != nil {
		t.Fatalf("normalizeHandler failed: %v", err)
	}

	if handler.ModuleName != "service" {
		t.Fatalf("expected moduleName 'service', got %s", handler.ModuleName)
	}
	if handler.Params["name"] != "nginx" {
		t.Fatalf("expected param name 'nginx', got %v", handler.Params["name"])
	}
	if handler.Params["state"] != "restarted" {
		t.Fatalf("expected param state 'restarted', got %v", handler.Params["state"])
	}
}
