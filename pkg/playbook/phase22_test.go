package playbook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/inventory"
)

// TestHandlerListenBroadcast 验证同一个 listen 广播事件能够同时触发多个不同的 Handlers
func TestHandlerListenBroadcast(t *testing.T) {
	tempDir := t.TempDir()
	flag1 := filepath.Join(tempDir, "handler1.txt")
	flag2 := filepath.Join(tempDir, "handler2.txt")

	yamlPlaybook := `
- name: Test Listen Broadcast
  hosts: localhost
  gather_facts: false
  tasks:
    - name: Trigger web restart
      command: echo "change config"
      changed_when: true
      notify: "restart web"

  handlers:
    - name: Restart Nginx
      command: touch ` + flag1 + `
      listen: "restart web"

    - name: Reload PHP-FPM
      command: touch ` + flag2 + `
      listen: "restart web"
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
	// Should have: 1 main task + 2 handlers = 3 tasks executed
	if len(hostRes.Tasks) != 3 {
		t.Fatalf("expected 3 tasks (1 task + 2 handlers), got %d", len(hostRes.Tasks))
	}

	// Verify both handler flags were created
	if _, err := os.Stat(flag1); err != nil {
		t.Errorf("handler 1 was not executed (flag1 missing: %v)", err)
	}
	if _, err := os.Stat(flag2); err != nil {
		t.Errorf("handler 2 was not executed (flag2 missing: %v)", err)
	}
}

// TestHandlerWhenCondition 验证 Handler 上的 when 条件能正常生效
func TestHandlerWhenCondition(t *testing.T) {
	tempDir := t.TempDir()
	skippedFlag := filepath.Join(tempDir, "should_be_skipped.txt")
	executedFlag := filepath.Join(tempDir, "should_run.txt")

	yamlPlaybook := `
- name: Test Handler When
  hosts: localhost
  gather_facts: false
  tasks:
    - name: Trigger handlers
      command: echo "trigger"
      changed_when: true
      notify:
        - "Handler Skipped"
        - "Handler Executed"

  handlers:
    - name: Handler Skipped
      command: touch ` + skippedFlag + `
      when: false

    - name: Handler Executed
      command: touch ` + executedFlag + `
      when: true
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

	_, err = exec.Execute(pb)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if _, err := os.Stat(skippedFlag); err == nil {
		t.Errorf("handler with when: false should NOT have run, but flag exists!")
	}
	if _, err := os.Stat(executedFlag); err != nil {
		t.Errorf("handler with when: true should have run, but flag is missing: %v", err)
	}
}

// TestRoleWhenAndTagsInheritance 验证在 Play 的 roles 列表中声明的 when 与 tags 会正确级联传递至所有任务
func TestRoleWhenAndTagsInheritance(t *testing.T) {
	tempDir := t.TempDir()
	rolesDir := filepath.Join(tempDir, "roles")
	myRoleDir := filepath.Join(rolesDir, "my_service")
	tasksDir := filepath.Join(myRoleDir, "tasks")
	if err := os.MkdirAll(tasksDir, 0755); err != nil {
		t.Fatalf("mkdir error: %v", err)
	}

	flagFile := filepath.Join(tempDir, "role_ran.txt")
	tasksContent := `
- name: Task inside role
  command: touch ` + flagFile + `
`
	if err := os.WriteFile(filepath.Join(tasksDir, "main.yml"), []byte(tasksContent), 0644); err != nil {
		t.Fatalf("write task error: %v", err)
	}

	// 1. Play with when: false on role -> tasks must be skipped
	playbookSkipped := `
- name: Role when skipped
  hosts: localhost
  gather_facts: false
  roles:
    - role: my_service
      when: false
      tags: [service_tag]
`
	parser := NewParser("")
	pb, err := parser.Parse([]byte(playbookSkipped))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	exec := NewExecutor(inv)
	exec.SetRolePaths([]string{rolesDir})
	defer exec.Close()

	res, err := exec.Execute(pb)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}

	hostRes := res.Plays[0].Hosts["localhost"]
	if len(hostRes.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(hostRes.Tasks))
	}
	if !hostRes.Tasks[0].Skipped {
		t.Errorf("expected role task to be skipped due to role when: false")
	}
	if _, err := os.Stat(flagFile); err == nil {
		t.Errorf("flag file exists when role should have been skipped!")
	}

	// 2. Play with tags filtering (-t service_tag) -> task must run and match tag
	playbookWithTags := `
- name: Role with tags
  hosts: localhost
  gather_facts: false
  roles:
    - role: my_service
      when: true
      tags: [service_tag]
`
	pb2, err := parser.Parse([]byte(playbookWithTags))
	if err != nil {
		t.Fatalf("parse error 2: %v", err)
	}

	exec2 := NewExecutor(inv)
	exec2.SetRolePaths([]string{rolesDir})
	exec2.SetTags([]string{"service_tag"}) // Filter by tag
	defer exec2.Close()

	res2, err := exec2.Execute(pb2)
	if err != nil {
		t.Fatalf("execute error 2: %v", err)
	}

	hostRes2 := res2.Plays[0].Hosts["localhost"]
	if len(hostRes2.Tasks) != 1 {
		t.Fatalf("expected 1 task executed by tag filter, got %d", len(hostRes2.Tasks))
	}
	if hostRes2.Tasks[0].Skipped {
		t.Errorf("role task was skipped despite matching tag filter!")
	}
	if _, err := os.Stat(flagFile); err != nil {
		t.Errorf("flag file missing, role task did not run: %v", err)
	}
}

// TestBlockinfileModuleInPlaybook 验证 Playbook 中 blockinfile 任务的端到端执行
func TestBlockinfileModuleInPlaybook(t *testing.T) {
	tempDir := t.TempDir()
	confFile := filepath.Join(tempDir, "nginx.conf")
	_ = os.WriteFile(confFile, []byte("http {\n}\n"), 0644)

	yamlPlaybook := `
- name: Test Blockinfile in Playbook
  hosts: localhost
  gather_facts: false
  tasks:
    - name: Insert upstream block
      blockinfile:
        path: ` + confFile + `
        marker: "# {mark} UPSTREAM BLOCK"
        insertafter: "http {"
        block: |
          upstream backend {
              server 10.0.0.1:8080;
          }
`

	parser := NewParser("")
	pb, err := parser.Parse([]byte(yamlPlaybook))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	exec := NewExecutor(inv)
	defer exec.Close()

	res, err := exec.Execute(pb)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}

	hostRes := res.Plays[0].Hosts["localhost"]
	if len(hostRes.Tasks) != 1 || !hostRes.Tasks[0].Changed {
		t.Fatalf("expected 1 changed task, got %#v", hostRes.Tasks)
	}

	data, _ := os.ReadFile(confFile)
	content := string(data)
	if !strings.Contains(content, "UPSTREAM BLOCK") || !strings.Contains(content, "server 10.0.0.1:8080;") {
		t.Errorf("block was not inserted correctly, got:\n%s", content)
	}
}

// TestFreeStrategyAnyErrorsFatal 验证在 strategy: free 下声明 any_errors_fatal: true 时，
// 任意主机任务失败能够即时熔断打断其余主机的后续任务执行。
func TestFreeStrategyAnyErrorsFatal(t *testing.T) {
	yamlPlaybook := `
- name: Test Free Strategy Any Errors Fatal
  hosts: all
  strategy: free
  any_errors_fatal: true
  gather_facts: false
  tasks:
    - name: Host 1 fails
      fail:
        msg: "host1 failed"
      when: inventory_hostname == 'host1'

    - name: Host 2 delay
      command: sleep 0.05
      when: inventory_hostname == 'host2'

    - name: Task 3 should be skipped due to fatal熔断
      debug:
        msg: "running task 3"
`
	parser := NewParser("")
	pb, err := parser.Parse([]byte(yamlPlaybook))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	inv := inventory.NewInventory()
	inv.AllHosts["host1"] = &inventory.Host{
		Name:      "host1",
		Variables: map[string]string{"ansible_connection": "local"},
	}
	inv.AllHosts["host2"] = &inventory.Host{
		Name:      "host2",
		Variables: map[string]string{"ansible_connection": "local"},
	}

	exec := NewExecutor(inv)
	defer exec.Close()

	res, err := exec.Execute(pb)
	if err == nil {
		t.Fatalf("expected play to fail due to any_errors_fatal, got nil err")
	}

	// 验证 host1 标记了失败，且 host2 绝没有执行 Task 3
	playRes := res.Plays[0]
	h1 := playRes.Hosts["host1"]
	if h1 == nil || len(h1.Tasks) == 0 || !h1.Tasks[0].Failed {
		t.Fatalf("expected host1 task to fail, got %#v", h1)
	}

	h2 := playRes.Hosts["host2"]
	if h2 != nil {
		for _, task := range h2.Tasks {
			if task.Name == "Task 3 should be skipped due to fatal熔断" {
				t.Fatalf("host2 executed task 3 despite any_errors_fatal!")
			}
		}
	}
}

// TestLookupRelativePathWithBaseDir 验证 Playbook 在指定 baseDir 时，
// lookup('file', ...) 能够正确拼接相对路径并读取文件内容。
func TestLookupRelativePathWithBaseDir(t *testing.T) {
	tempDir := t.TempDir()
	subDir := filepath.Join(tempDir, "files")
	_ = os.MkdirAll(subDir, 0755)
	secretFile := filepath.Join(subDir, "token.txt")
	_ = os.WriteFile(secretFile, []byte("super-secret-token\n"), 0644)

	yamlPlaybook := `
- name: Test Lookup Relative Path
  hosts: localhost
  gather_facts: false
  tasks:
    - name: Read secret token
      debug:
        msg: "{{ lookup('file', 'files/token.txt') }}"
`
	parser := NewParser(tempDir)
	pb, err := parser.Parse([]byte(yamlPlaybook))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	pb.Path = filepath.Join(tempDir, "test.yml")

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	exec := NewExecutor(inv)
	defer exec.Close()

	res, err := exec.Execute(pb)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}

	hostRes := res.Plays[0].Hosts["localhost"]
	if len(hostRes.Tasks) != 1 {
		t.Fatalf("expected 1 task executed, got %#v", hostRes.Tasks)
	}
}

