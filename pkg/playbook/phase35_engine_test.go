package playbook

import (
	"os"
	"path/filepath"
	"testing"

	"go-ansible/pkg/inventory"
)

// 1. 验证 is changed / is not changed / is not skipped / is not unreachable 条件判断
func TestPhase35_ConditionIsChangedAndNegatedTests(t *testing.T) {
	inv := inventory.NewInventory()
	inv.EnsureLocalhost()
	exec := NewExecutor(inv)
	defer exec.Close()

	vars := map[string]interface{}{
		"changed_res": map[string]interface{}{
			"changed": true,
			"failed":  false,
			"skipped": false,
		},
		"noop_res": map[string]interface{}{
			"changed": false,
			"failed":  false,
			"skipped": false,
		},
	}

	if !exec.evaluateWhen("changed_res is changed", vars) {
		t.Fatalf("expected 'changed_res is changed' to be true")
	}
	if !exec.evaluateWhen("changed_res is change", vars) {
		t.Fatalf("expected 'changed_res is change' to be true")
	}
	if exec.evaluateWhen("noop_res is changed", vars) {
		t.Fatalf("expected 'noop_res is changed' to be false")
	}
	if !exec.evaluateWhen("noop_res is not changed", vars) {
		t.Fatalf("expected 'noop_res is not changed' to be true")
	}
	if !exec.evaluateWhen("noop_res is not skipped", vars) {
		t.Fatalf("expected 'noop_res is not skipped' to be true")
	}
	if !exec.evaluateWhen("noop_res is not unreachable", vars) {
		t.Fatalf("expected 'noop_res is not unreachable' to be true")
	}
}

// 2. 验证运行期魔术变量 (ansible_check_mode, ansible_diff_mode, ansible_play_name, ansible_play_hosts_all, ansible_play_batch, ansible_version)
func TestPhase35_RuntimeMagicVariables(t *testing.T) {
	yamlPlaybook := `
- name: Magic Vars Verification Play
  hosts: localhost
  gather_facts: false
  tasks:
    - name: Capture Magic Vars in Normal Task
      set_fact:
        captured_play_name: "{{ ansible_play_name }}"
        captured_check: "{{ ansible_check_mode }}"
        captured_ver: "{{ ansible_version.full }}"
        captured_batch: "{{ ansible_play_batch | join(',') }}"

    - name: Capture Check Mode Override in Task
      check_mode: true
      set_fact:
        task_level_check: "{{ ansible_check_mode }}"
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

	if _, err := exec.Execute(pb); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	facts := exec.facts["localhost"]
	if facts["captured_play_name"] != "Magic Vars Verification Play" {
		t.Fatalf("expected ansible_play_name='Magic Vars Verification Play', got %v", facts["captured_play_name"])
	}
	if facts["captured_check"] != false && facts["captured_check"] != "false" {
		t.Fatalf("expected ansible_check_mode=false, got %v", facts["captured_check"])
	}
	if facts["task_level_check"] != true && facts["task_level_check"] != "true" {
		t.Fatalf("expected task-level ansible_check_mode=true, got %v", facts["task_level_check"])
	}
	if facts["captured_ver"] != "2.16.0" {
		t.Fatalf("expected ansible_version.full='2.16.0', got %v", facts["captured_ver"])
	}
	if facts["captured_batch"] != "localhost" {
		t.Fatalf("expected ansible_play_batch='localhost', got %v", facts["captured_batch"])
	}
}

// 3. 验证 include_role 支持 tasks_from / vars_from / handlers 注册以及 role_path / role_name 魔术变量与 apply 下发
func TestPhase35_IncludeRoleTasksFromHandlersAndApply(t *testing.T) {
	tmpDir := t.TempDir()
	roleDir := filepath.Join(tmpDir, "roles", "webapp")
	if err := os.MkdirAll(filepath.Join(roleDir, "tasks"), 0755); err != nil {
		t.Fatalf("MkdirAll tasks: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(roleDir, "vars"), 0755); err != nil {
		t.Fatalf("MkdirAll vars: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(roleDir, "handlers"), 0755); err != nil {
		t.Fatalf("MkdirAll handlers: %v", err)
	}

	// tasks/custom.yml
	customTasks := `
- name: Record Role Context
  set_fact:
    observed_role_name: "{{ role_name }}"
    observed_role_path: "{{ role_path }}"
    observed_custom_var: "{{ custom_port }}"
  changed_when: true
  notify: restart webapp
`
	if err := os.WriteFile(filepath.Join(roleDir, "tasks", "custom.yml"), []byte(customTasks), 0644); err != nil {
		t.Fatalf("WriteFile custom.yml: %v", err)
	}

	// vars/prod.yml
	if err := os.WriteFile(filepath.Join(roleDir, "vars", "prod.yml"), []byte("custom_port: 9443\n"), 0644); err != nil {
		t.Fatalf("WriteFile prod.yml: %v", err)
	}

	// handlers/main.yml
	handlerYAML := `
- name: restart webapp
  set_fact:
    webapp_restarted: true
`
	if err := os.WriteFile(filepath.Join(roleDir, "handlers", "main.yml"), []byte(handlerYAML), 0644); err != nil {
		t.Fatalf("WriteFile handlers/main.yml: %v", err)
	}

	playbookYAML := `
- name: Test Include Role Advanced Features
  hosts: localhost
  gather_facts: false
  tasks:
    - name: Include webapp with custom files and apply
      include_role:
        name: webapp
        tasks_from: custom.yml
        vars_from: prod
        apply:
          tags:
            - custom_tag
`
	pbPath := filepath.Join(tmpDir, "site.yml")
	if err := os.WriteFile(pbPath, []byte(playbookYAML), 0644); err != nil {
		t.Fatalf("WriteFile site.yml: %v", err)
	}

	parser := NewParser(tmpDir)
	pb, err := parser.ParseFile(pbPath)
	if err != nil {
		t.Fatalf("ParseFile error: %v", err)
	}

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()
	exec := NewExecutor(inv)
	exec.SetTags([]string{"custom_tag"})
	defer exec.Close()

	if _, err := exec.Execute(pb); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	facts := exec.facts["localhost"]
	if facts["observed_role_name"] != "webapp" {
		t.Fatalf("expected role_name='webapp', got %v", facts["observed_role_name"])
	}
	if facts["observed_role_path"] != roleDir {
		t.Fatalf("expected role_path=%q, got %v", roleDir, facts["observed_role_path"])
	}
	if facts["observed_custom_var"] != "9443" && facts["observed_custom_var"] != 9443 {
		t.Fatalf("expected custom_port=9443 from vars_from, got %v", facts["observed_custom_var"])
	}
	if facts["webapp_restarted"] != true {
		t.Fatalf("expected handler 'restart webapp' from include_role to be triggered and flushed, got %v", facts["webapp_restarted"])
	}
}

// 4. 验证 add_host 保留复杂结构变量并映射 ansible_connection / become 属性，以及 include_vars 递归过滤与 Block rescue 元数据
func TestPhase35_AddHostComplexVarsIncludeVarsFilteringAndBlockRescueMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	varsDir := filepath.Join(tmpDir, "vars_d")
	subDir := filepath.Join(varsDir, "nested")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("MkdirAll error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(varsDir, "app_main.yml"), []byte("app_tier: frontend\n"), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(varsDir, "ignore_me.yml"), []byte("ignored_var: true\n"), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "app_sub.yml"), []byte("sub_tier: edge\n"), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	playbookYAML := `
- name: Phase 35 Builtin & Block Rescue Test
  hosts: localhost
  gather_facts: false
  tasks:
    - name: Add dynamic host with structured vars and connection
      add_host:
        name: dyn1
        ansible_connection: local
        ansible_become: true
        ansible_become_user: deploy
        cluster_nodes:
          - n1
          - n2

    - name: Include vars with recursion and matching
      include_vars:
        dir: vars_d
        depth: 2
        files_matching: "^app_.*\\.yml$"
        ignore_files:
          - "ignore_me.yml"

    - name: Block with Rescue Metadata Check
      block:
        - name: Intentional Failing Task
          fail:
            msg: "simulated failure"
      rescue:
        - name: Record Failed Task Info
          set_fact:
            rescued_task_name: "{{ ansible_failed_task.name }}"
            rescued_task_module: "{{ ansible_failed_task.module }}"
            rescued_result_msg: "{{ ansible_failed_result.msg }}"
`
	pbPath := filepath.Join(tmpDir, "play.yml")
	if err := os.WriteFile(pbPath, []byte(playbookYAML), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	parser := NewParser(tmpDir)
	pb, err := parser.ParseFile(pbPath)
	if err != nil {
		t.Fatalf("ParseFile error: %v", err)
	}

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()
	exec := NewExecutor(inv)
	defer exec.Close()

	if _, err := exec.Execute(pb); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	// 验证 add_host 连接属性与结构化变量
	dynHost, err := inv.GetHost("dyn1")
	if err != nil || dynHost == nil {
		t.Fatalf("expected dyn1 host in inventory: %v", err)
	}
	if dynHost.Connection != "local" || !dynHost.Become || dynHost.BecomeUser != "deploy" {
		t.Fatalf("unexpected dyn1 connection/become fields: conn=%q become=%v user=%q", dynHost.Connection, dynHost.Become, dynHost.BecomeUser)
	}
	nodes, ok := exec.facts["dyn1"]["cluster_nodes"].([]interface{})
	if !ok || len(nodes) != 2 {
		t.Fatalf("expected structured slice in dyn1 facts, got %T (%#v)", exec.facts["dyn1"]["cluster_nodes"], exec.facts["dyn1"]["cluster_nodes"])
	}

	// 验证 include_vars 递归与过滤
	lhFacts := exec.facts["localhost"]
	if lhFacts["app_tier"] != "frontend" || lhFacts["sub_tier"] != "edge" {
		t.Fatalf("expected app_tier and sub_tier from recursive include_vars, got %#v", lhFacts)
	}
	if _, exists := lhFacts["ignored_var"]; exists {
		t.Fatalf("expected ignore_me.yml to be excluded")
	}

	// 验证 Block rescue 元数据
	if lhFacts["rescued_task_name"] != "Intentional Failing Task" {
		t.Fatalf("expected rescued_task_name='Intentional Failing Task', got %v", lhFacts["rescued_task_name"])
	}
	if lhFacts["rescued_task_module"] != "fail" {
		t.Fatalf("expected rescued_task_module='fail', got %v", lhFacts["rescued_task_module"])
	}
	if lhFacts["rescued_result_msg"] != "simulated failure" {
		t.Fatalf("expected rescued_result_msg='simulated failure', got %v", lhFacts["rescued_result_msg"])
	}
}

