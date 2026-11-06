package playbook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/module"
)

type mockStep1SSHClient struct {
	commands []string
	outputs  map[string]*module.Result
}

func newMockStep1SSHClient() *mockStep1SSHClient {
	return &mockStep1SSHClient{
		commands: make([]string, 0),
		outputs:  make(map[string]*module.Result),
	}
}

func (m *mockStep1SSHClient) Connect() error {
	return nil
}

func (m *mockStep1SSHClient) Close() error {
	return nil
}

func (m *mockStep1SSHClient) Execute(cmd string) (*module.Result, error) {
	m.commands = append(m.commands, cmd)
	for pattern, res := range m.outputs {
		if strings.Contains(cmd, pattern) {
			return res, nil
		}
	}
	return &module.Result{Stdout: "", ExitCode: 0}, nil
}

func (m *mockStep1SSHClient) ExecuteWithBecome(cmd string) (*module.Result, error) {
	return m.Execute(cmd)
}

func (m *mockStep1SSHClient) Upload(localPath, remotePath string) error {
	return nil
}

func (m *mockStep1SSHClient) Download(remotePath, localPath string) error {
	return nil
}

// TestPhase24_UntilExhaustionFailsTask 测试 until 条件在耗尽重试次数后必须标记失败
func TestPhase24_UntilExhaustionFailsTask(t *testing.T) {
	exec := NewExecutor(nil)
	host := &inventory.Host{
		Name:       "localhost",
		Connection: "local",
	}

	// 任务执行 echo "still waiting"，until 条件要求 stdout 包含 "ready"，retries=2, delay=0
	task := &Task{
		Name:       "Test Until Failure",
		ModuleName: "command",
		Params: map[string]interface{}{
			"cmd": "echo 'still waiting'",
		},
		Until:   "result.stdout is defined and 'ready' in result.stdout",
		Retries: 2,
		Delay:   0,
	}

	vars := map[string]interface{}{
		"inventory_hostname": "localhost",
	}

	results, _ := exec.executeTaskForHostWithEnv([]*inventory.Host{host}, host, task, vars, nil, make(map[string]bool), nil)
	if len(results) == 0 {
		t.Fatalf("expected at least one task result")
	}

	lastRes := results[len(results)-1]
	if !lastRes.Failed {
		t.Errorf("expected lastRes.Failed to be true, got false")
	}
	if !strings.Contains(lastRes.Message, "all retries exhausted; until condition not met") {
		t.Errorf("expected message to mention exhausted retries, got %q", lastRes.Message)
	}
}

// TestPhase24_UntilConditionSucceeds 测试 until 条件在重试中匹配成功后正常返回
func TestPhase24_UntilConditionSucceeds(t *testing.T) {
	tmpDir := t.TempDir()
	probeFile := filepath.Join(tmpDir, "probe.txt")

	exec := NewExecutor(nil)
	host := &inventory.Host{
		Name:       "localhost",
		Connection: "local",
	}

	// 先创建文件写入 ok
	if err := os.WriteFile(probeFile, []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}

	task := &Task{
		Name:       "Test Until Success",
		ModuleName: "command",
		Params: map[string]interface{}{
			"cmd": "cat " + probeFile,
		},
		Until:   "'ok' in result.stdout",
		Retries: 3,
		Delay:   0,
	}

	vars := map[string]interface{}{
		"inventory_hostname": "localhost",
	}

	results, err := exec.executeTaskForHostWithEnv([]*inventory.Host{host}, host, task, vars, nil, make(map[string]bool), nil)
	if err != nil {
		t.Fatalf("unexpected error when until condition is met: %v", err)
	}
	if len(results) == 0 || results[0].Failed {
		t.Fatalf("expected successful result, got %+v", results)
	}
}

// TestPhase24_CommandAndShellChdirAndExecutable 测试 command 与 shell 模块的 chdir 与 executable 支持
func TestPhase24_CommandAndShellChdirAndExecutable(t *testing.T) {
	client := newMockStep1SSHClient()

	// 1. Command with chdir
	cmdMod := &module.CommandModule{}
	_, err := cmdMod.Execute(client, map[string]interface{}{
		"cmd":   "ls -la",
		"chdir": "/var/log",
	})
	if err != nil {
		t.Fatalf("CommandModule.Execute failed: %v", err)
	}
	if len(client.commands) == 0 || !strings.Contains(client.commands[len(client.commands)-1], "cd '/var/log' && ls -la") {
		t.Errorf("expected command to include chdir cd, got: %v", client.commands)
	}

	// 2. Shell with chdir and custom executable
	shellMod := &module.ShellModule{}
	_, err = shellMod.Execute(client, map[string]interface{}{
		"cmd":        "echo $0",
		"chdir":      "/opt/app",
		"executable": "/bin/bash",
	})
	if err != nil {
		t.Fatalf("ShellModule.Execute failed: %v", err)
	}
	lastCmd := client.commands[len(client.commands)-1]
	if !strings.HasPrefix(lastCmd, "/bin/bash -c ") {
		t.Errorf("expected shell executable /bin/bash, got: %s", lastCmd)
	}
	if !strings.Contains(lastCmd, "cd '/opt/app' && echo $0") {
		t.Errorf("expected shell command with chdir, got: %s", lastCmd)
	}

	// 3. Command with space in creates
	client.outputs["test -e '/path with spaces/file.txt'"] = &module.Result{ExitCode: 0}
	res, err := cmdMod.Execute(client, map[string]interface{}{
		"cmd":     "touch '/path with spaces/file.txt'",
		"creates": "/path with spaces/file.txt",
	})
	if err != nil {
		t.Fatalf("CommandModule creates failed: %v", err)
	}
	if res.Changed {
		t.Errorf("expected task to be skipped due to creates existing, but got changed=true")
	}
	if !strings.Contains(res.Message, "skipped") {
		t.Errorf("expected skip message, got %s", res.Message)
	}
}

// TestPhase24_DnfModuleExecute 测试 DnfModule 原生调用 dnf 命令
func TestPhase24_DnfModuleExecute(t *testing.T) {
	client := newMockStep1SSHClient()
	dnfMod := &module.DnfModule{}

	// 测试 dnf install
	_, err := dnfMod.Execute(client, map[string]interface{}{
		"name":  "nginx",
		"state": "present",
	})
	if err != nil {
		t.Fatalf("DnfModule.Execute failed: %v", err)
	}

	foundInstall := false
	for _, cmd := range client.commands {
		if strings.Contains(cmd, "dnf install -y nginx") {
			foundInstall = true
			break
		}
	}
	if !foundInstall {
		t.Errorf("expected 'dnf install -y nginx' command, got: %v", client.commands)
	}

	// 测试 dnf upgrade (state=latest)
	client.commands = nil
	_, err = dnfMod.Execute(client, map[string]interface{}{
		"name":  "curl",
		"state": "latest",
	})
	if err != nil {
		t.Fatalf("DnfModule.Execute latest failed: %v", err)
	}
	foundUpgrade := false
	for _, cmd := range client.commands {
		if strings.Contains(cmd, "dnf upgrade -y curl") {
			foundUpgrade = true
			break
		}
	}
	if !foundUpgrade {
		t.Errorf("expected 'dnf upgrade -y curl' command, got: %v", client.commands)
	}
}

// TestPhase24_SetFilePermissionsGroupOnly 测试仅提供 group 时的 SetFilePermissions 处理
func TestPhase24_SetFilePermissionsGroupOnly(t *testing.T) {
	client := newMockStep1SSHClient()

	changed, err := module.SetFilePermissions(client, "/var/www/html", "0755", "", "www-data", false)
	if err != nil {
		t.Fatalf("SetFilePermissions failed: %v", err)
	}
	if !changed {
		t.Errorf("expected changed=true")
	}

	foundChmod := false
	foundChgrp := false
	for _, cmd := range client.commands {
		if strings.Contains(cmd, "chmod 0755 '/var/www/html'") {
			foundChmod = true
		}
		if strings.Contains(cmd, "chgrp www-data '/var/www/html'") {
			foundChgrp = true
		}
	}
	if !foundChmod {
		t.Errorf("expected chmod command, got: %v", client.commands)
	}
	if !foundChgrp {
		t.Errorf("expected chgrp command for standalone group, got: %v", client.commands)
	}
}

// TestPhase24_StdoutLinesInRegisteredVars 测试 stdout_lines 在注册变量和条件中正常注入
func TestPhase24_StdoutLinesInRegisteredVars(t *testing.T) {
	exec := NewExecutor(nil)
	host := &inventory.Host{
		Name:       "localhost",
		Connection: "local",
	}

	// 任务输出两行内容
	task := &Task{
		Name:       "Output multi lines",
		ModuleName: "command",
		Params: map[string]interface{}{
			"cmd": "printf 'line1\nline2\n'",
		},
		Register: "output_lines_var",
	}

	vars := map[string]interface{}{
		"inventory_hostname": "localhost",
	}

	_, err := exec.executeTaskForHostWithEnv([]*inventory.Host{host}, host, task, vars, nil, make(map[string]bool), nil)
	if err != nil {
		t.Fatalf("executeTaskForHostWithEnv failed: %v", err)
	}

	facts := exec.getHostFacts("localhost")
	regRaw, ok := facts["output_lines_var"]
	if !ok {
		t.Fatalf("expected output_lines_var to be registered in facts")
	}
	regMap, ok := regRaw.(map[string]interface{})
	if !ok {
		t.Fatalf("expected output_lines_var to be a map")
	}

	stdoutLinesRaw, ok := regMap["stdout_lines"]
	if !ok {
		t.Fatalf("expected stdout_lines key in registered output")
	}
	lines, ok := stdoutLinesRaw.([]string)
	if !ok {
		t.Fatalf("expected []string for stdout_lines, got %T", stdoutLinesRaw)
	}
	if len(lines) != 2 || lines[0] != "line1" || lines[1] != "line2" {
		t.Errorf("expected ['line1', 'line2'], got %v", lines)
	}
}

// TestPhase24_UntilDefaultRetriesAndDelay 测试当声明 until 但未指定 retries/delay 时自动补全 Ansible 默认值 (retries: 3, delay: 5)
func TestPhase24_UntilDefaultRetriesAndDelay(t *testing.T) {
	raw := rawTask{
		"name":  "test until defaults",
		"shell": "echo ok",
		"until": "result.rc == 0",
	}
	task, err := normalizeTask(raw)
	if err != nil {
		t.Fatalf("normalizeTask failed: %v", err)
	}
	if task.Retries != 3 {
		t.Errorf("expected default retries 3, got %d", task.Retries)
	}
	if task.Delay != 5 {
		t.Errorf("expected default delay 5, got %d", task.Delay)
	}
}

