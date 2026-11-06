package playbook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/module"
	"go-ansible/pkg/ssh"
)

// 1. 测试 ansible_password 在清单与连接变量中的正确映射
func TestAnsiblePasswordMapping(t *testing.T) {
	iniContent := `
[web]
node1 ansible_host=10.0.0.1 ansible_password=secret123 ansible_user=root
node2 ansible_host=10.0.0.2 ansible_ssh_pass=legacy123
`
	inv, err := inventory.ParseINI([]byte(iniContent))
	if err != nil {
		t.Fatalf("ParseINI failed: %v", err)
	}

	h1 := inv.AllHosts["node1"]
	if h1 == nil || h1.Password != "secret123" {
		t.Fatalf("expected node1 Password to be 'secret123', got %v", h1)
	}

	h2 := inv.AllHosts["node2"]
	if h2 == nil || h2.Password != "legacy123" {
		t.Fatalf("expected node2 Password to be 'legacy123', got %v", h2)
	}
}

// 2. 测试核心魔术变量 playbook_dir, inventory_dir, inventory_file, omit 注入
func TestMagicVariablesInjection(t *testing.T) {
	tmpDir := t.TempDir()
	invFile := filepath.Join(tmpDir, "hosts.ini")
	_ = os.WriteFile(invFile, []byte("[all]\nhost1 ansible_host=127.0.0.1\n"), 0644)

	inv, err := inventory.ParseFile(invFile)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	exec := NewExecutor(inv)
	exec.baseDir = tmpDir

	h := inv.AllHosts["host1"]
	taskVars := exec.taskVarsForHost(h, nil)

	if taskVars["playbook_dir"] != tmpDir {
		t.Errorf("expected playbook_dir=%q, got %v", tmpDir, taskVars["playbook_dir"])
	}
	if taskVars["inventory_file"] != invFile {
		t.Errorf("expected inventory_file=%q, got %v", invFile, taskVars["inventory_file"])
	}
	if taskVars["inventory_dir"] != tmpDir {
		t.Errorf("expected inventory_dir=%q, got %v", tmpDir, taskVars["inventory_dir"])
	}
	if taskVars["omit"] != "__omit_place_holder__" {
		t.Errorf("expected omit='__omit_place_holder__', got %v", taskVars["omit"])
	}
}

// 3. 测试 omit 魔术变量在参数解析中自动省略未定义参数
func TestOmitMagicVariableBehavior(t *testing.T) {
	exec := NewExecutor(inventory.NewInventory())

	vars := map[string]interface{}{
		"omit":       "__omit_place_holder__",
		"given_mode": "0644",
	}

	params := map[string]interface{}{
		"dest":  "/tmp/foo",
		"mode":  "{{ unset_mode | default(omit) }}",
		"owner": "{{ given_mode | default(omit) }}",
	}

	resolved := exec.resolveVariables(params, vars)

	if _, exists := resolved["mode"]; exists {
		t.Errorf("expected 'mode' to be omitted when default(omit) is triggered, got: %v", resolved["mode"])
	}
	if resolved["owner"] != "0644" {
		t.Errorf("expected 'owner' to be '0644', got: %v", resolved["owner"])
	}
	if resolved["dest"] != "/tmp/foo" {
		t.Errorf("expected 'dest' to be '/tmp/foo', got: %v", resolved["dest"])
	}
}

// 4. 测试 normalizeFacts 结构化注入 ansible_facts 命名空间子字典
func TestAnsibleFactsNamespace(t *testing.T) {
	rawFacts := map[string]interface{}{
		"ansible_distribution":         "Ubuntu",
		"ansible_distribution_version": "22.04",
		"ansible_os_family":            "Debian",
		"custom_fact":                  "test_val",
	}

	normalized := normalizeFacts(rawFacts)
	afRaw, ok := normalized["ansible_facts"]
	if !ok || afRaw == nil {
		t.Fatalf("expected 'ansible_facts' namespace dict in facts, got nil")
	}

	af, ok := afRaw.(map[string]interface{})
	if !ok {
		t.Fatalf("expected ansible_facts to be map[string]interface{}, got %T", afRaw)
	}

	if af["distribution"] != "Ubuntu" {
		t.Errorf("expected af['distribution']='Ubuntu', got %v", af["distribution"])
	}
	if af["os_family"] != "Debian" {
		t.Errorf("expected af['os_family']='Debian', got %v", af["os_family"])
	}
	if af["ansible_distribution"] != "Ubuntu" {
		t.Errorf("expected af['ansible_distribution']='Ubuntu', got %v", af["ansible_distribution"])
	}
	if af["custom_fact"] != "test_val" {
		t.Errorf("expected af['custom_fact']='test_val', got %v", af["custom_fact"])
	}
}

// 5. 测试 Play gather_subset 与 gather_timeout 的解析与传递
func TestGatherSubsetAndTimeout(t *testing.T) {
	playbookYAML := `
- name: Test Play
  hosts: all
  gather_facts: true
  gather_subset:
    - min
    - "!network"
  gather_timeout: 15
  tasks:
    - name: Ping
      ping:
`
	pb, err := NewParser("").Parse([]byte(playbookYAML))
	if err != nil {
		t.Fatalf("ParsePlaybook failed: %v", err)
	}

	if len(pb.Plays) != 1 {
		t.Fatalf("expected 1 play, got %d", len(pb.Plays))
	}
	play := pb.Plays[0]

	if len(play.GatherSubset) != 2 || play.GatherSubset[0] != "min" || play.GatherSubset[1] != "!network" {
		t.Errorf("expected GatherSubset=['min', '!network'], got %#v", play.GatherSubset)
	}
	if play.GatherTimeout != 15 {
		t.Errorf("expected GatherTimeout=15, got %d", play.GatherTimeout)
	}
}

// 6. 测试 run_once 任务结果与 set_fact 广播到批次内所有活动主机
func TestRunOnceBroadcasting(t *testing.T) {
	inv := inventory.NewInventory()
	h1 := &inventory.Host{Name: "host1", Address: "127.0.0.1", Connection: "local"}
	h2 := &inventory.Host{Name: "host2", Address: "127.0.0.1", Connection: "local"}
	inv.AllHosts["host1"] = h1
	inv.AllHosts["host2"] = h2

	exec := NewExecutor(inv)
	allHosts := []*inventory.Host{h1, h2}

	// 1) 测试 run_once 命令执行的 registered 变量广播
	task1 := &Task{
		Name:       "Generate Token",
		ModuleName: "command",
		Params: map[string]interface{}{
			"cmd": "echo token_12345",
		},
		RunOnce:  true,
		Register: "token_res",
	}

	runOnceMap := make(map[string]bool)
	vars1 := exec.taskVarsForHost(h1, nil)
	vars2 := exec.taskVarsForHost(h2, nil)

	// Host 1 执行
	res1, err := exec.executeTaskForHostWithEnv(allHosts, h1, task1, vars1, nil, runOnceMap, nil)
	if err != nil {
		t.Fatalf("Host1 execute failed: %v", err)
	}
	if len(res1) == 0 || res1[0].Skipped {
		t.Fatalf("Host1 should execute task, got: %#v", res1)
	}

	// Host 2 触发时应被标记为 skipped
	res2, err := exec.executeTaskForHostWithEnv(allHosts, h2, task1, vars2, nil, runOnceMap, nil)
	if err != nil {
		t.Fatalf("Host2 execute failed: %v", err)
	}
	if len(res2) == 0 || !res2[0].Skipped {
		t.Fatalf("Host2 should be skipped due to run_once, got: %#v", res2)
	}

	// 校验 host2 上是否成功获得了广播的 registered 变量 token_res
	facts2 := exec.getHostFacts("host2")
	if facts2 == nil || facts2["token_res"] == nil {
		t.Fatalf("expected host2 to receive broadcasted token_res, got nil")
	}
	tokenRes2, ok := facts2["token_res"].(map[string]interface{})
	if !ok || !strings.Contains(tokenRes2["stdout"].(string), "token_12345") {
		t.Fatalf("expected host2 token_res stdout to contain 'token_12345', got %#v", tokenRes2)
	}

	// 2) 测试 run_once 下 set_fact 的广播
	taskSetFact := &Task{
		Name:       "Broadcast shared fact",
		ModuleName: "set_fact",
		Params: map[string]interface{}{
			"cluster_leader": "host1",
		},
		RunOnce: true,
	}

	runOnceFactMap := make(map[string]bool)
	_, err = exec.executeTaskForHostWithEnv(allHosts, h1, taskSetFact, vars1, nil, runOnceFactMap, nil)
	if err != nil {
		t.Fatalf("Host1 set_fact failed: %v", err)
	}

	facts2After := exec.getHostFacts("host2")
	if facts2After["cluster_leader"] != "host1" {
		t.Fatalf("expected host2 to have cluster_leader='host1', got: %v", facts2After["cluster_leader"])
	}
}

// 7. 测试 copy 模块 directory_mode 与目标路径 shell 引号安全包裹
func TestCopyDirectoryModeAndQuoting(t *testing.T) {
	tmpDir := t.TempDir()
	nestedDest := filepath.Join(tmpDir, "path with space", "sub dir", "test.txt")

	copyMod := &module.CopyModule{}
	localConn := &ssh.LocalConnection{}

	params := map[string]interface{}{
		"content":        "hello world",
		"dest":           nestedDest,
		"directory_mode": "0755",
	}

	res, err := copyMod.Execute(localConn, params)
	if err != nil {
		t.Fatalf("CopyModule.Execute failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected Changed=true, got false")
	}

	data, err := os.ReadFile(nestedDest)
	if err != nil {
		t.Fatalf("read destination file failed: %v", err)
	}
	if string(data) != "hello world" {
		t.Fatalf("expected content 'hello world', got %q", string(data))
	}
}

// 8. 测试 until 轮询探测注入 attempts 计数器
func TestUntilAttemptsCounter(t *testing.T) {
	exec := NewExecutor(inventory.NewInventory())

	task := &Task{
		Name: "Wait for condition",
		Until: "result.attempts >= 2",
		Retries: 3,
		Delay: 0,
	}

	vars := map[string]interface{}{}

	// 第一次调用 evaluateUntil，传入 attempt = 1 -> 返回 false
	ok1 := exec.evaluateUntil(task.Until, "result", &module.Result{ExitCode: 0}, nil, vars, 1)
	if ok1 {
		t.Fatalf("expected attempt 1 to fail condition (result.attempts >= 2)")
	}

	// 第二次调用 evaluateUntil，传入 attempt = 2 -> 返回 true
	ok2 := exec.evaluateUntil(task.Until, "result", &module.Result{ExitCode: 0}, nil, vars, 2)
	if !ok2 {
		t.Fatalf("expected attempt 2 to pass condition (result.attempts >= 2)")
	}
}
