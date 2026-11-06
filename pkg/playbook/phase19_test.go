package playbook

import (
	"strings"
	"testing"

	"go-ansible/pkg/inventory"
)

// TestSetupGathersNetworkAndEnvFacts 验证 setup 模块采集的核心网络与环境事实
func TestSetupGathersNetworkAndEnvFacts(t *testing.T) {
	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	exec := NewExecutor(inv)
	defer exec.Close()

	trueVal := true
	play := &Play{
		Name:        "Test Setup Facts",
		Hosts:       "localhost",
		GatherFacts: &trueVal,
		Tasks: []*Task{
			{
				Name:       "Output Default IPv4 and Env",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "IP={{ ansible_default_ipv4.address }} USER={{ ansible_user_id }} HOME={{ ansible_env.HOME }}",
				},
				Register: "debug_out",
			},
		},
	}

	pb := &Playbook{Plays: []*Play{play}}
	res, err := exec.Execute(pb)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	hostRes := res.Plays[0].Hosts["localhost"]
	debugTask := hostRes.Tasks[0]
	msg := debugTask.Stdout
	if msg == "" {
		msg = debugTask.Message
	}
	t.Logf("Debug msg: %s", msg)

	if !strings.Contains(msg, "IP=") || strings.Contains(msg, "{{") {
		t.Errorf("expected resolved IP in msg, got %q", msg)
	}
	if !strings.Contains(msg, "USER=") || strings.Contains(msg, "{{ ansible_user_id }}") {
		t.Errorf("expected resolved USER in msg, got %q", msg)
	}
	if !strings.Contains(msg, "HOME=") || strings.Contains(msg, "{{ ansible_env.HOME }}") {
		t.Errorf("expected resolved HOME in msg, got %q", msg)
	}
}

// TestPlaybookAuthorizedKeyTask 测试在 Playbook 编排中原生执行 authorized_key 模块
func TestPlaybookAuthorizedKeyTask(t *testing.T) {
	tmpDir := t.TempDir()
	authKeysPath := tmpDir + "/test_auth_keys"

	yamlPlaybook := `
- name: Test Authorized Key Playbook
  hosts: localhost
  gather_facts: false
  tasks:
    - name: Ensure Deployer Public Key is Configured
      authorized_key:
        user: testuser
        key: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPlaybookKey... dev@company.org"
        path: ` + authKeysPath + `
        state: present
`

	parser := NewParser("")
	pb, err := parser.Parse([]byte(yamlPlaybook))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	exec := NewExecutor(inv)
	defer exec.Close()

	res, err := exec.Execute(pb)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	hostRes := res.Plays[0].Hosts["localhost"]
	if len(hostRes.Tasks) != 1 {
		t.Fatalf("expected 1 task result, got %d", len(hostRes.Tasks))
	}

	taskRes := hostRes.Tasks[0]
	if !taskRes.Changed {
		t.Errorf("expected task changed=true on initial run")
	}

	// 再次执行该 Playbook 验证幂等性
	res2, err := exec.Execute(pb)
	if err != nil {
		t.Fatalf("Execute (idempotency) error: %v", err)
	}
	taskRes2 := res2.Plays[0].Hosts["localhost"].Tasks[0]
	if taskRes2.Changed {
		t.Errorf("expected task changed=false on second run (idempotency)")
	}
}

// TestHandlersNotifyingHandlers 验证 Handlers 链式级联触发特性
func TestHandlersNotifyingHandlers(t *testing.T) {
	tmpDir := t.TempDir()
	f1 := tmpDir + "/file1.txt"
	f2 := tmpDir + "/file2.txt"

	yamlPlaybook := `
- name: Test Handlers Notifying Handlers
  hosts: localhost
  gather_facts: false
  tasks:
    - name: Trigger first handler
      copy:
        content: "trigger initial"
        dest: ` + f1 + `
      notify: First Handler

  handlers:
    - name: First Handler
      copy:
        content: "trigger second"
        dest: ` + f2 + `
      notify: Second Handler

    - name: Second Handler
      debug:
        msg: "Cascade handler successfully triggered"
`

	parser := NewParser("")
	pb, err := parser.Parse([]byte(yamlPlaybook))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	exec := NewExecutor(inv)
	defer exec.Close()

	res, err := exec.Execute(pb)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	hostRes := res.Plays[0].Hosts["localhost"]
	// Tasks should contain:
	// 1. "Trigger first handler"
	// 2. "First Handler"
	// 3. "Second Handler"
	if len(hostRes.Tasks) != 3 {
		t.Fatalf("expected 3 executed tasks (1 main task + 2 handlers), got %d: %+v", len(hostRes.Tasks), hostRes.Tasks)
	}

	if hostRes.Tasks[0].Name != "Trigger first handler" {
		t.Errorf("expected task[0] to be 'Trigger first handler', got %s", hostRes.Tasks[0].Name)
	}
	if hostRes.Tasks[1].Name != "First Handler" {
		t.Errorf("expected task[1] to be 'First Handler', got %s", hostRes.Tasks[1].Name)
	}
	if hostRes.Tasks[2].Name != "Second Handler" {
		t.Errorf("expected task[2] to be 'Second Handler', got %s", hostRes.Tasks[2].Name)
	}
}

// TestDelegateFacts 验证 delegate_to 场景下 delegate_facts 的语义与正确归属
func TestDelegateFacts(t *testing.T) {
	yamlPlaybook := `
- name: Test Delegate Facts Behavior
  hosts: web1
  gather_facts: false
  tasks:
    - name: Delegated task without delegate_facts (default false)
      set_fact:
        cluster_role: primary
      delegate_to: db1
      register: reg_task1

    - name: Delegated task with delegate_facts true
      set_fact:
        db_engine: postgres
      delegate_to: db1
      delegate_facts: true
      register: reg_task2
`

	parser := NewParser("")
	pb, err := parser.Parse([]byte(yamlPlaybook))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	inv := inventory.NewInventory()
	inv.AllHosts["web1"] = &inventory.Host{
		Name:       "web1",
		Address:    "localhost",
		Connection: "local",
	}
	inv.AllHosts["db1"] = &inventory.Host{
		Name:       "db1",
		Address:    "localhost",
		Connection: "local",
	}

	exec := NewExecutor(inv)
	defer exec.Close()

	res, err := exec.Execute(pb)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if len(res.Plays) == 0 || res.Plays[0].Hosts["web1"] == nil {
		t.Fatalf("expected play execution on web1")
	}

	// 1. delegate_facts: false -> facts 和 register 必须归属于当前执行主机 web1
	web1Facts := exec.facts["web1"]
	if web1Facts == nil || web1Facts["cluster_role"] != "primary" {
		t.Errorf("expected cluster_role=primary on web1, got %v", web1Facts)
	}
	if web1Facts["reg_task1"] == nil {
		t.Errorf("expected reg_task1 registered on web1")
	}
	if db1Facts := exec.facts["db1"]; db1Facts != nil && db1Facts["cluster_role"] != nil {
		t.Errorf("cluster_role should NOT be present on db1 when delegate_facts is false")
	}

	// 2. delegate_facts: true -> facts 归属于委派主机 db1，而 register 按照 Ansible 规范必须归属于当前执行主机 web1
	db1Facts := exec.facts["db1"]
	if db1Facts == nil || db1Facts["db_engine"] != "postgres" {
		t.Errorf("expected db_engine=postgres on db1, got %v", db1Facts)
	}
	if web1Facts["reg_task2"] == nil {
		t.Errorf("expected reg_task2 registered on web1 (current play host)")
	}
	if db1Facts["reg_task2"] != nil {
		t.Errorf("reg_task2 should NOT be registered on delegated host db1")
	}
	if web1Facts["db_engine"] != nil {
		t.Errorf("db_engine should NOT be present on web1 when delegate_facts is true")
	}
}

// TestServiceFactsInPlaybook 验证 service_facts 模块在剧本运行中收集事实并注入 host facts 体系
func TestServiceFactsInPlaybook(t *testing.T) {
	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	exec := NewExecutor(inv)
	defer exec.Close()

	falseVal := false
	play := &Play{
		Name:        "Test Service Facts",
		Hosts:       "localhost",
		GatherFacts: &falseVal,
		Tasks: []*Task{
			{
				Name:       "Gather service facts",
				ModuleName: "service_facts",
			},
			{
				Name:       "Verify service facts accessible in variables",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "Service count={{ services | length }}",
				},
				Register: "svc_check",
			},
		},
	}

	pb := &Playbook{Plays: []*Play{play}}
	res, err := exec.Execute(pb)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	hostFacts := exec.facts["localhost"]
	if hostFacts == nil {
		t.Fatalf("expected localhost facts to be initialized")
	}

	if hostFacts["services"] == nil {
		t.Errorf("expected 'services' in host facts")
	}

	if hostFacts["ansible_facts"] == nil {
		t.Errorf("expected 'ansible_facts' map in host facts")
	} else if afMap, ok := hostFacts["ansible_facts"].(map[string]interface{}); !ok || afMap["services"] == nil {
		t.Errorf("expected 'ansible_facts.services' in host facts")
	}

	hostRes := res.Plays[0].Hosts["localhost"]
	if len(hostRes.Tasks) < 2 {
		t.Fatalf("expected at least 2 task results")
	}
	debugTask := hostRes.Tasks[1]
	msg := debugTask.Stdout
	if msg == "" {
		msg = debugTask.Message
	}
	t.Logf("Debug msg output: %s", msg)
	if !strings.Contains(msg, "Service count=") || strings.Contains(msg, "{{") {
		t.Errorf("expected resolved message, got: %q", msg)
	}
}

// TestLookupWithFiltersInPlaybook 验证 Playbook 变量表达式中 lookup 配合过滤管道的能力
func TestLookupWithFiltersInPlaybook(t *testing.T) {
	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	exec := NewExecutor(inv)
	defer exec.Close()

	resolved := exec.resolveString("val={{ lookup('env', 'PATH') | upper }}", map[string]interface{}{})
	if !strings.Contains(resolved, "val=") || strings.Contains(resolved, "{{") {
		t.Fatalf("expected resolved filtered lookup, got: %s", resolved)
	}
	if !strings.Contains(resolved, "BIN") {
		t.Fatalf("expected PATH in upper case to contain BIN, got: %s", resolved)
	}
}

// TestPackageFactsInPlaybook 验证 package_facts 收集软件包事实并注入 host facts
func TestPackageFactsInPlaybook(t *testing.T) {
	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	exec := NewExecutor(inv)
	defer exec.Close()

	falseVal := false
	play := &Play{
		Name:        "Test Package Facts",
		Hosts:       "localhost",
		GatherFacts: &falseVal,
		Tasks: []*Task{
			{
				Name:       "Gather package facts",
				ModuleName: "package_facts",
			},
			{
				Name:       "Verify packages accessible in debug",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "Packages count={{ packages | length }}",
				},
				Register: "pkg_debug",
			},
		},
	}

	pb := &Playbook{Plays: []*Play{play}}
	res, err := exec.Execute(pb)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	hostFacts := exec.facts["localhost"]
	if hostFacts == nil {
		t.Fatalf("expected localhost facts to be initialized")
	}

	if hostFacts["packages"] == nil {
		t.Errorf("expected 'packages' in host facts")
	}

	if hostFacts["ansible_facts"] == nil {
		t.Errorf("expected 'ansible_facts' in host facts")
	} else if afMap, ok := hostFacts["ansible_facts"].(map[string]interface{}); !ok || afMap["packages"] == nil {
		t.Errorf("expected 'ansible_facts.packages' in host facts")
	}

	hostRes := res.Plays[0].Hosts["localhost"]
	if len(hostRes.Tasks) < 2 {
		t.Fatalf("expected at least 2 task results")
	}
	debugTask := hostRes.Tasks[1]
	msg := debugTask.Stdout
	if msg == "" {
		msg = debugTask.Message
	}
	t.Logf("Debug msg output: %s", msg)
	if !strings.Contains(msg, "Packages count=") || strings.Contains(msg, "{{") {
		t.Errorf("expected resolved packages count, got: %q", msg)
	}
}

// TestTempfileInPlaybook 验证 tempfile 模块在 Playbook 编排中的注册、引用与生命周期管理
func TestTempfileInPlaybook(t *testing.T) {
	yamlPlaybook := `
- name: Test Tempfile Workflow
  hosts: localhost
  gather_facts: false
  tasks:
    - name: Create Temporary File
      tempfile:
        state: file
        suffix: .test.conf
      register: tmp_info

    - name: Write Config to Tempfile
      copy:
        content: "bind_port=8080\n"
        dest: "{{ tmp_info.path }}"
      register: copy_out

    - name: Check Tempfile Stats
      stat:
        path: "{{ tmp_info.path }}"
      register: stat_out

    - name: Clean Up Tempfile
      file:
        path: "{{ tmp_info.path }}"
        state: absent
`

	parser := NewParser("")
	pb, err := parser.Parse([]byte(yamlPlaybook))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	exec := NewExecutor(inv)
	defer exec.Close()

	res, err := exec.Execute(pb)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	hostRes := res.Plays[0].Hosts["localhost"]
	if len(hostRes.Tasks) != 4 {
		t.Fatalf("expected 4 tasks, got %d", len(hostRes.Tasks))
	}

	// Task 0: tempfile
	tempTask := hostRes.Tasks[0]
	if !tempTask.Changed {
		t.Errorf("expected tempfile task to be changed")
	}
	tmpData := tempTask.Data
	if tmpData == nil || tmpData["path"] == nil {
		t.Fatalf("expected tempfile Data to contain 'path', got: %#v", tempTask.Data)
	}
	filePath := tmpData["path"].(string)
	if !strings.HasSuffix(filePath, ".test.conf") {
		t.Errorf("expected tempfile path to have suffix '.test.conf', got: %s", filePath)
	}

	// Task 1: copy
	copyTask := hostRes.Tasks[1]
	if !copyTask.Changed {
		t.Errorf("expected copy task to be changed")
	}

	// Task 2: stat
	statTask := hostRes.Tasks[2]
	statData := statTask.Data
	if statData == nil || statData["stat"] == nil {
		t.Fatalf("expected stat task data to contain 'stat', got: %#v", statTask.Data)
	}
	statMap, _ := statData["stat"].(map[string]interface{})
	if exists, _ := statMap["exists"].(bool); !exists {
		t.Errorf("expected file %s to exist in stat task", filePath)
	}

	// Task 3: file absent
	delTask := hostRes.Tasks[3]
	if !delTask.Changed {
		t.Errorf("expected delete task to be changed")
	}
}
