package playbook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/inventory"
)

// TestTaskLevelCheckModeOverride 验证在全局 -C (Check Mode) 下，声明 check_mode: false 的任务能真实执行
func TestTaskLevelCheckModeOverride(t *testing.T) {
	tempDir := t.TempDir()
	flagFile := filepath.Join(tempDir, "must_exist.txt")
	dryRunFile := filepath.Join(tempDir, "must_not_exist.txt")

	yamlPlaybook := `
- name: Test CheckMode Override Play
  hosts: localhost
  gather_facts: false
  tasks:
    - name: Real execution in dry-run mode
      command: touch ` + flagFile + `
      check_mode: false
      register: real_res

    - name: Simulated execution in dry-run mode
      copy:
        content: "should not be written"
        dest: ` + dryRunFile + `
      register: dry_res
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
	exec.SetCheckMode(true) // 模拟命令行 -C / --check

	res, err := exec.Execute(pb)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	hostRes := res.Plays[0].Hosts["localhost"]
	if len(hostRes.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(hostRes.Tasks))
	}

	// Task 1 (check_mode: false) 必须真实执行且创建了文件
	if _, err := os.Stat(flagFile); err != nil {
		t.Errorf("expected flagFile %s to be created by check_mode: false task, err: %v", flagFile, err)
	}

	// Task 2 (继承全局 check_mode: true) 必须没有创建文件
	if _, err := os.Stat(dryRunFile); err == nil {
		t.Errorf("dryRunFile %s should NOT exist because task ran in check mode", dryRunFile)
	}
}

// TestTaskLevelCheckModeForced 验证在正常执行（非 -C）下，声明 check_mode: true 的任务被强制预演而不产生副作用
func TestTaskLevelCheckModeForced(t *testing.T) {
	tempDir := t.TempDir()
	dryRunPath := filepath.Join(tempDir, "dryrun_touch.txt")
	realPath := filepath.Join(tempDir, "real_touch.txt")

	yamlPlaybook := `
- name: Test Force CheckMode On Task
  hosts: localhost
  gather_facts: false
  tasks:
    - name: Force dry-run on task
      copy:
        content: "dryrun"
        dest: ` + dryRunPath + `
      check_mode: true

    - name: Real task
      copy:
        content: "real"
        dest: ` + realPath + `
      check_mode: false
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
	exec.SetCheckMode(false) // 正常执行模式

	res, err := exec.Execute(pb)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	hostRes := res.Plays[0].Hosts["localhost"]
	if len(hostRes.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(hostRes.Tasks))
	}

	// Task 1 声明了 check_mode: true，目标文件不能存在
	if _, err := os.Stat(dryRunPath); err == nil {
		t.Errorf("dryRunPath %s must NOT exist, check_mode: true was ignored", dryRunPath)
	}

	// Task 2 正常执行，目标文件必须存在
	if _, err := os.Stat(realPath); err != nil {
		t.Errorf("realPath %s must exist, err: %v", realPath, err)
	}
}

// TestFailedWhenWithIgnoreErrors 验证 failed_when 触发但声明了 ignore_errors: true 时，节点继续执行后续任务
func TestFailedWhenWithIgnoreErrors(t *testing.T) {
	yamlPlaybook := `
- name: Test FailedWhen IgnoreErrors
  hosts: localhost
  gather_facts: false
  tasks:
    - name: Step 1 with intentional failed_when
      command: echo 'database connection timeout'
      register: step1
      failed_when: "'timeout' in step1.stdout"
      ignore_errors: true

    - name: Step 2 should continue
      command: echo 'pipeline recovered'
      register: step2
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
	if len(hostRes.Tasks) != 2 {
		t.Fatalf("expected 2 tasks to execute, got %d", len(hostRes.Tasks))
	}

	task1 := hostRes.Tasks[0]
	if task1.Failed {
		t.Errorf("task1.Failed should be false due to ignore_errors: true")
	}
	if !task1.Ignored {
		t.Errorf("task1.Ignored should be true")
	}

	task2 := hostRes.Tasks[1]
	if task2.Failed {
		t.Errorf("task2 should succeed, but failed: %s", task2.Message)
	}
	if !strings.Contains(task2.Stdout, "pipeline recovered") {
		t.Errorf("expected step 2 stdout to contain 'pipeline recovered', got: %q", task2.Stdout)
	}
}

// TestPlayLevelCheckMode 验证 Play 级 check_mode: true 作用于整个 Play 下的任务
func TestPlayLevelCheckMode(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "play_level_check.txt")

	yamlPlaybook := `
- name: Test Play Level CheckMode
  hosts: localhost
  gather_facts: false
  check_mode: true
  tasks:
    - name: Should be dry-run
      copy:
        content: "hello play check"
        dest: ` + testFile + `
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
	exec.SetCheckMode(false) // 全局关闭 check mode

	res, err := exec.Execute(pb)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	hostRes := res.Plays[0].Hosts["localhost"]
	if len(hostRes.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(hostRes.Tasks))
	}

	if _, err := os.Stat(testFile); err == nil {
		t.Errorf("testFile %s should NOT exist because play had check_mode: true", testFile)
	}
}

// TestHostVarsCacheAndGroupsPerformance 验证多主机场景下 hostvars 缓存与 groups 缓存的正确性与数据一致性
func TestHostVarsCacheAndGroupsPerformance(t *testing.T) {
	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	// 创建 3 个主机组与 30 个主机
	groupWeb := &inventory.Group{Name: "web", Hosts: make([]*inventory.Host, 0), Variables: map[string]string{"group_var": "web_tier"}}
	groupDB := &inventory.Group{Name: "db", Hosts: make([]*inventory.Host, 0), Variables: map[string]string{"group_var": "db_tier"}}
	inv.Groups["web"] = groupWeb
	inv.Groups["db"] = groupDB

	for i := 0; i < 15; i++ {
		hName := "web-" + strings.Repeat("0", 2-len(string(rune('0'+i)))) + string(rune('0'+i))
		h := &inventory.Host{
			Name:      hName,
			Address:   "10.0.1." + string(rune('1'+i)),
			Port:      22,
			Variables: map[string]string{"app_port": "8080", "tier": "web"},
		}
		inv.AllHosts[hName] = h
		groupWeb.Hosts = append(groupWeb.Hosts, h)
	}

	for i := 0; i < 15; i++ {
		hName := "db-" + strings.Repeat("0", 2-len(string(rune('0'+i)))) + string(rune('0'+i))
		h := &inventory.Host{
			Name:      hName,
			Address:   "10.0.2." + string(rune('1'+i)),
			Port:      3306,
			Variables: map[string]string{"db_port": "3306", "tier": "database"},
		}
		inv.AllHosts[hName] = h
		groupDB.Hosts = append(groupDB.Hosts, h)
	}

	yamlPlaybook := `
- name: Test HostVars Cache Consistency
  hosts: localhost
  gather_facts: false
  tasks:
    - name: Access web hostvar
      command: echo "{{ hostvars['web-00']['ansible_host'] }}"
      register: res_web_host

    - name: Access db hostvar
      command: echo "{{ hostvars['db-00']['db_port'] }}"
      register: res_db_port

    - name: Check web group count
      command: echo "{{ groups['web'] | length }}"
      register: res_web_count
`

	parser := NewParser("")
	pb, err := parser.Parse([]byte(yamlPlaybook))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	exec := NewExecutor(inv)
	defer exec.Close()

	res, err := exec.Execute(pb)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	hostRes := res.Plays[0].Hosts["localhost"]
	if len(hostRes.Tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(hostRes.Tasks))
	}

	if strings.TrimSpace(hostRes.Tasks[0].Stdout) != "10.0.1.1" {
		t.Errorf("expected web-00 ansible_host to be 10.0.1.1, got %q", hostRes.Tasks[0].Stdout)
	}

	if strings.TrimSpace(hostRes.Tasks[1].Stdout) != "3306" {
		t.Errorf("expected db-00 db_port to be 3306, got %q", hostRes.Tasks[1].Stdout)
	}

	if strings.TrimSpace(hostRes.Tasks[2].Stdout) != "15" {
		t.Errorf("expected groups['web'] length to be 15, got %q", hostRes.Tasks[2].Stdout)
	}
}

// TestHostVarsCacheInvalidationDynamicModules 验证动态模块 (set_fact, add_host, group_by, meta: clear_facts)
// 会精准触发缓存失效，使随后的任务能即时感知最新变量与组结构
func TestHostVarsCacheInvalidationDynamicModules(t *testing.T) {
	yamlPlaybook := `
- name: Dynamic Invalidation Test
  hosts: localhost
  gather_facts: false
  tasks:
    - name: Step 1 set_fact
      set_fact:
        dynamic_token: "secret-token-123"

    - name: Step 2 verify hostvars updated with set_fact
      command: echo "{{ hostvars['localhost']['dynamic_token'] }}"
      register: step2_token

    - name: Step 3 add_host dynamically
      add_host:
        name: "dynamic-worker-1"
        groups: "dynamic_workers"
        worker_ip: "192.168.10.50"

    - name: Step 4 verify dynamic host in groups and hostvars
      command: echo "{{ groups['dynamic_workers'] | length }} {{ hostvars['dynamic-worker-1']['worker_ip'] }}"
      register: step4_add_host

    - name: Step 5 group_by module
      group_by:
        key: "cluster_node"

    - name: Step 6 verify group_by reflected in groups
      command: echo "{{ groups['cluster_node'] | length }}"
      register: step6_group_by

    - name: Step 7 clear_facts
      meta: clear_facts

    - name: Step 8 verify dynamic_token is cleared
      command: echo "{{ hostvars['localhost']['dynamic_token'] | default('token_cleared') }}"
      register: step8_clear
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
	tasks := hostRes.Tasks
	if len(tasks) < 8 {
		t.Fatalf("expected at least 8 tasks, got %d", len(tasks))
	}

	// Task 2: Step 2 verify hostvars updated with set_fact
	if !strings.Contains(tasks[1].Stdout, "secret-token-123") {
		t.Errorf("Step 2 failed to read updated set_fact from hostvars: %q", tasks[1].Stdout)
	}

	// Task 4: Step 4 verify dynamic host in groups and hostvars
	if !strings.Contains(tasks[3].Stdout, "1 192.168.10.50") {
		t.Errorf("Step 4 failed: dynamic host or group missing after add_host: %q", tasks[3].Stdout)
	}

	// Task 6: Step 6 verify group_by reflected in groups
	if !strings.Contains(tasks[5].Stdout, "1") {
		t.Errorf("Step 6 failed: cluster_node not found in groups after group_by: %q", tasks[5].Stdout)
	}

	// Task 8: Step 8 verify dynamic_token is cleared
	if !strings.Contains(tasks[7].Stdout, "token_cleared") {
		t.Errorf("Step 8 failed: dynamic_token was not cleared: %q", tasks[7].Stdout)
	}
}

// BenchmarkHostVarsSnapshot 基准测试：100 个主机规模下 buildHostVarsSnapshot 的执行性能与内存开销
func BenchmarkHostVarsSnapshot(b *testing.B) {
	inv := inventory.NewInventory()
	for i := 0; i < 100; i++ {
		hName := "node-" + strings.Repeat("0", 3-len(string(rune('0'+i)))) + string(rune('0'+i))
		h := &inventory.Host{
			Name:      hName,
			Address:   "10.0.0." + string(rune('1'+i)),
			Port:      22,
			Variables: map[string]string{"env": "prod", "tier": "app"},
		}
		inv.AllHosts[hName] = h
	}
	exec := NewExecutor(inv)
	defer exec.Close()

	curHost := inv.AllHosts["node-000"]
	curVars := map[string]interface{}{"task_var_1": "abc", "task_var_2": 123}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		snapshot := exec.buildHostVarsSnapshot(curHost, curVars)
		if len(snapshot) != 100 {
			b.Fatalf("expected 100 hosts, got %d", len(snapshot))
		}
	}
}
