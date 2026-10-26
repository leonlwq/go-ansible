package playbook

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"go-ansible/pkg/inventory"
)

func parseTestPlaybook(t *testing.T, yamlContent string) *Playbook {
	p := NewParser("")
	pb, err := p.Parse([]byte(yamlContent))
	if err != nil {
		t.Fatalf("failed to parse playbook: %v", err)
	}
	return pb
}

func TestVarsPrompt_BasicAndDefault(t *testing.T) {
	yamlContent := `
- name: test vars prompt
  hosts: localhost
  vars_prompt:
    - name: username
      prompt: "Enter username"
      private: false
    - name: app_port
      prompt: "Enter port"
      default: 8080
      private: false
  tasks:
    - name: check vars
      debug:
        msg: "user={{ username }}, port={{ app_port }}"
`
	playbook := parseTestPlaybook(t, yamlContent)

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	exec := NewExecutor(inv)
	// 模拟输入：username 输入 "admin"，app_port 直接回车（取默认值 8080）
	input := "admin\n\n"
	exec.Stdin = strings.NewReader(input)
	var stdout bytes.Buffer
	exec.Stdout = &stdout

	res, err := exec.Execute(playbook)
	if err != nil {
		t.Fatalf("failed to execute playbook: %v", err)
	}

	hostRes, ok := res.Plays[0].Hosts["localhost"]
	if !ok || len(hostRes.Tasks) == 0 {
		t.Fatalf("expected task results on localhost")
	}

	taskRes := hostRes.Tasks[0]
	if taskRes.Failed {
		t.Fatalf("task failed: %s", taskRes.Message)
	}
	expectedMsg := "user=admin, port=8080"
	if taskRes.Stdout != expectedMsg && taskRes.Message != expectedMsg {
		t.Fatalf("expected msg %q, got stdout=%q, msg=%q", expectedMsg, taskRes.Stdout, taskRes.Message)
	}
}

func TestVarsPrompt_Confirm(t *testing.T) {
	yamlContent := `
- name: test vars prompt confirm
  hosts: localhost
  vars_prompt:
    - name: password
      prompt: "Enter password"
      confirm: true
      private: true
  tasks:
    - name: print password
      debug:
        msg: "password is {{ password }}"
`
	playbook := parseTestPlaybook(t, yamlContent)

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	exec := NewExecutor(inv)
	// 第一次故意不匹配 ("pass1" != "pass2")，第二次匹配 ("mypass" == "mypass")
	input := "pass1\npass2\nmypass\nmypass\n"
	exec.Stdin = strings.NewReader(input)
	var stdout bytes.Buffer
	exec.Stdout = &stdout

	res, err := exec.Execute(playbook)
	if err != nil {
		t.Fatalf("failed to execute playbook: %v", err)
	}

	taskRes := res.Plays[0].Hosts["localhost"].Tasks[0]
	if taskRes.Failed {
		t.Fatalf("task failed: %s", taskRes.Message)
	}
	if taskRes.Stdout != "password is mypass" && taskRes.Message != "password is mypass" {
		t.Fatalf("expected msg %q, got stdout=%q, msg=%q", "password is mypass", taskRes.Stdout, taskRes.Message)
	}
}

func TestVarsPrompt_Encrypt(t *testing.T) {
	yamlContent := `
- name: test vars prompt encrypt
  hosts: localhost
  vars_prompt:
    - name: secret_token
      prompt: "Enter secret"
      encrypt: sha256
      private: false
  tasks:
    - name: print encrypted
      debug:
        msg: "{{ secret_token }}"
`
	playbook := parseTestPlaybook(t, yamlContent)

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	exec := NewExecutor(inv)
	rawPass := "secure_secret_123"
	exec.Stdin = strings.NewReader(rawPass + "\n")
	var stdout bytes.Buffer
	exec.Stdout = &stdout

	res, err := exec.Execute(playbook)
	if err != nil {
		t.Fatalf("failed to execute playbook: %v", err)
	}

	expectedHashBytes := sha256.Sum256([]byte(rawPass))
	expectedHash := hex.EncodeToString(expectedHashBytes[:])

	taskRes := res.Plays[0].Hosts["localhost"].Tasks[0]
	if taskRes.Stdout != expectedHash && taskRes.Message != expectedHash {
		t.Fatalf("expected sha256 hash %q, got stdout=%q, msg=%q", expectedHash, taskRes.Stdout, taskRes.Message)
	}
}

func TestVarsPrompt_ExtraVarsOverride(t *testing.T) {
	yamlContent := `
- name: test vars prompt extra vars override
  hosts: localhost
  vars_prompt:
    - name: env_target
      prompt: "Enter environment"
  tasks:
    - name: check env
      debug:
        msg: "target={{ env_target }}"
`
	playbook := parseTestPlaybook(t, yamlContent)

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	exec := NewExecutor(inv)
	// 命令行传入 extra_vars，不提供 Stdin 输入，验证不发生死锁或读取错误
	exec.SetExtraVars(map[string]interface{}{
		"env_target": "production",
	})
	var stdout bytes.Buffer
	exec.Stdout = &stdout

	res, err := exec.Execute(playbook)
	if err != nil {
		t.Fatalf("failed to execute playbook: %v", err)
	}

	taskRes := res.Plays[0].Hosts["localhost"].Tasks[0]
	if taskRes.Stdout != "target=production" && taskRes.Message != "target=production" {
		t.Fatalf("expected target=production, got stdout=%q, msg=%q", taskRes.Stdout, taskRes.Message)
	}
}

func TestDebugger_OnFailed_Redo(t *testing.T) {
	yamlContent := `
- name: test debugger on failed
  hosts: localhost
  tasks:
    - name: broken command
      shell: "exit 1"
      debugger: on_failed
    - name: next step
      debug:
        msg: "playbook finished successfully"
`
	playbook := parseTestPlaybook(t, yamlContent)

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	exec := NewExecutor(inv)
	// 交互调试序列：
	// 1. p result.rc -> 打印退出码
	// 2. u cmd="echo recovered" -> 修复命令
	// 3. r -> 重新执行任务
	inputCommands := "p result.rc\nu cmd=\"echo recovered\"\nr\n"
	exec.Stdin = strings.NewReader(inputCommands)
	var stdout bytes.Buffer
	exec.Stdout = &stdout

	res, err := exec.Execute(playbook)
	if err != nil {
		t.Fatalf("expected playbook to succeed after debugger redo, got err: %v", err)
	}

	hostRes := res.Plays[0].Hosts["localhost"]
	if len(hostRes.Tasks) != 2 {
		t.Fatalf("expected 2 tasks to complete, got %d", len(hostRes.Tasks))
	}
	if hostRes.Tasks[0].Failed {
		t.Fatalf("first task should have passed after redo, but marked failed: %s", hostRes.Tasks[0].Message)
	}
	if hostRes.Tasks[1].Stdout != "playbook finished successfully" && hostRes.Tasks[1].Message != "playbook finished successfully" {
		t.Fatalf("expected second task to run, got %#v", hostRes.Tasks[1])
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, "[Debugger] Task failed") {
		t.Fatalf("expected debugger entry notification, got output:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Task args updated: cmd = echo recovered") {
		t.Fatalf("expected update confirmation, got output:\n%s", outStr)
	}
}

func TestDebugger_Always_PrintAndContinue(t *testing.T) {
	yamlContent := `
- name: test debugger always
  hosts: localhost
  vars:
    app_status: "active"
  tasks:
    - name: inspect task
      debug:
        msg: "inspecting"
      debugger: always
    - name: follow-up
      debug:
        msg: "done"
`
	playbook := parseTestPlaybook(t, yamlContent)

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	exec := NewExecutor(inv)
	// 交互指令：
	// 1. p app_status
	// 2. c (continue)
	inputCommands := "p app_status\nc\n"
	exec.Stdin = strings.NewReader(inputCommands)
	var stdout bytes.Buffer
	exec.Stdout = &stdout

	res, err := exec.Execute(playbook)
	if err != nil {
		t.Fatalf("playbook failed: %v", err)
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, "active") {
		t.Fatalf("expected variable app_status output 'active', got:\n%s", outStr)
	}
	hostRes := res.Plays[0].Hosts["localhost"]
	if len(hostRes.Tasks) != 2 {
		t.Fatalf("expected 2 tasks executed, got %d", len(hostRes.Tasks))
	}
}

func TestDebugger_Quit(t *testing.T) {
	yamlContent := `
- name: test debugger quit
  hosts: localhost
  tasks:
    - name: fatal step
      shell: "exit 2"
      debugger: on_failed
    - name: should not run
      debug:
        msg: "unreachable"
`
	playbook := parseTestPlaybook(t, yamlContent)

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	exec := NewExecutor(inv)
	// 交互指令：直接输入 q 终止
	exec.Stdin = strings.NewReader("q\n")
	var stdout bytes.Buffer
	exec.Stdout = &stdout

	_, err := exec.Execute(playbook)
	if err == nil {
		t.Fatalf("expected playbook to return error when debugger quits")
	}
	if !strings.Contains(err.Error(), "debugger requested quit") {
		t.Fatalf("expected debugger quit error, got: %v", err)
	}
}

func TestDebugger_PlayLevelInheritance(t *testing.T) {
	yamlContent := `
- name: test debugger play level
  hosts: localhost
  debugger: on_failed
  tasks:
    - name: task 1
      shell: "exit 1"
    - name: task 2
      debug:
        msg: "done"
`
	playbook := parseTestPlaybook(t, yamlContent)

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	exec := NewExecutor(inv)
	// 测试 Play 级别 debugger 继承：输入 u cmd="echo ok" 然后 redo
	exec.Stdin = strings.NewReader("u cmd=\"echo ok\"\nr\n")
	var stdout bytes.Buffer
	exec.Stdout = &stdout

	res, err := exec.Execute(playbook)
	if err != nil {
		t.Fatalf("playbook failed: %v", err)
	}
	if res.Plays[0].Hosts["localhost"].Tasks[0].Failed {
		t.Fatalf("task 1 should succeed after redo")
	}
}

func TestDebugger_EnvironmentVariable(t *testing.T) {
	yamlContent := `
- name: test debugger env var
  hosts: localhost
  tasks:
    - name: will fail
      shell: "exit 3"
`
	playbook := parseTestPlaybook(t, yamlContent)

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	os.Setenv("ANSIBLE_ENABLE_TASK_DEBUGGER", "true")
	defer os.Unsetenv("ANSIBLE_ENABLE_TASK_DEBUGGER")

	exec := NewExecutor(inv)
	exec.Stdin = strings.NewReader("u cmd=\"echo env_fixed\"\nr\n")
	var stdout bytes.Buffer
	exec.Stdout = &stdout

	res, err := exec.Execute(playbook)
	if err != nil {
		t.Fatalf("playbook failed: %v", err)
	}
	if res.Plays[0].Hosts["localhost"].Tasks[0].Failed {
		t.Fatalf("task should have passed after debugger redo")
	}
}

func TestDebugger_LoopItem_Redo(t *testing.T) {
	yamlContent := `
- name: test debugger loop item redo
  hosts: localhost
  tasks:
    - name: loop with failure
      shell: "exit {{ 1 if item == 'bad' else 0 }}"
      loop:
        - "good"
        - "bad"
      debugger: on_failed
`
	playbook := parseTestPlaybook(t, yamlContent)

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	exec := NewExecutor(inv)
	// 当遇到 "bad" item 失败时，触发调试器，将其命令修改为 echo recovered 并 redo
	exec.Stdin = strings.NewReader("u cmd=\"echo recovered\"\nr\n")
	var stdout bytes.Buffer
	exec.Stdout = &stdout

	res, err := exec.Execute(playbook)
	if err != nil {
		t.Fatalf("playbook failed: %v", err)
	}

	taskResults := res.Plays[0].Hosts["localhost"].Tasks
	if len(taskResults) != 2 {
		t.Fatalf("expected 2 loop item results, got %d", len(taskResults))
	}
	if taskResults[0].Failed || taskResults[1].Failed {
		t.Fatalf("all loop items should have succeeded after debugger redo")
	}
}
