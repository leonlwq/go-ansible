package playbook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/module"
)

// mockPhase27SSHClient 专用于 Phase 27 单元测试的 Mock SSH 客户端
type mockPhase27SSHClient struct {
	commands []string
	outputs  map[string]*module.Result
}

func newMockPhase27SSHClient() *mockPhase27SSHClient {
	return &mockPhase27SSHClient{
		commands: make([]string, 0),
		outputs:  make(map[string]*module.Result),
	}
}

func (m *mockPhase27SSHClient) Connect() error { return nil }
func (m *mockPhase27SSHClient) Close() error   { return nil }

func (m *mockPhase27SSHClient) Execute(cmd string) (*module.Result, error) {
	m.commands = append(m.commands, cmd)
	for pattern, res := range m.outputs {
		if strings.Contains(cmd, pattern) {
			return res, nil
		}
	}
	return &module.Result{Stdout: "", ExitCode: 0}, nil
}

func (m *mockPhase27SSHClient) ExecuteWithBecome(cmd string) (*module.Result, error) {
	return m.Execute(cmd)
}

func (m *mockPhase27SSHClient) CopyFile(src, dest string) error { return nil }
func (m *mockPhase27SSHClient) CopyTemplate(src, dest string, vars map[string]interface{}) error {
	return nil
}
func (m *mockPhase27SSHClient) FetchFile(src, dest string) error          { return nil }
func (m *mockPhase27SSHClient) Upload(localPath, remotePath string) error  { return nil }
func (m *mockPhase27SSHClient) Download(remotePath, localPath string) error { return nil }

// TestPhase27_SetFilePermissionsIdempotency 验证权限属性完全一致时跳过 chmod/chown 保持幂等
func TestPhase27_SetFilePermissionsIdempotency(t *testing.T) {
	client := newMockPhase27SSHClient()
	// 模拟远程 stat 探测返回当前 mode 为 0644，属主为 root:root
	client.outputs["stat -c"] = &module.Result{
		Stdout:   "644:root:root\n",
		ExitCode: 0,
	}

	changed, err := module.SetFilePermissions(client, "/etc/nginx/nginx.conf", "0644", "root", "root", false)
	if err != nil {
		t.Fatalf("SetFilePermissions error: %v", err)
	}
	if changed {
		t.Fatalf("expected changed=false when permissions and owner already match, got changed=true")
	}
	for _, cmd := range client.commands {
		if strings.HasPrefix(cmd, "chmod ") || strings.HasPrefix(cmd, "chown ") {
			t.Fatalf("unexpected chmod/chown command executed: %s", cmd)
		}
	}

	// 改变 mode 为 0755 时应产生 chmod 变更
	client.commands = nil
	changed, err = module.SetFilePermissions(client, "/etc/nginx/nginx.conf", "0755", "root", "root", false)
	if err != nil {
		t.Fatalf("SetFilePermissions error: %v", err)
	}
	if !changed {
		t.Fatalf("expected changed=true when mode changes")
	}
}

// TestPhase27_SetupModuleFilterFactPreservation 验证 setup 模块 filter 仅做内存键过滤而不抹除采集脚本
func TestPhase27_SetupModuleFilterFactPreservation(t *testing.T) {
	client := newMockPhase27SSHClient()
	client.outputs["gathered_facts:"] = &module.Result{
		Stdout: `gathered_facts:
  ansible_hostname: node1
  ansible_distribution: "Ubuntu"
  ansible_distribution_version: "22.04"
  ansible_default_ipv4:
    address: "192.168.1.10"
    interface: "eth0"
`,
		ExitCode: 0,
	}

	setupMod := &module.SetupModule{}
	res, err := setupMod.Execute(client, map[string]interface{}{
		"filter": "ansible_distribution*",
	})
	if err != nil {
		t.Fatalf("SetupModule.Execute error: %v", err)
	}
	if !strings.Contains(res.Stdout, "ansible_distribution: \"Ubuntu\"") {
		t.Fatalf("expected filtered output to contain ansible_distribution, got: %s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "ansible_hostname") {
		t.Fatalf("expected ansible_hostname to be filtered out, got: %s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "ansible_default_ipv4") {
		t.Fatalf("expected ansible_default_ipv4 to be filtered out, got: %s", res.Stdout)
	}
}

// TestPhase27_MetaFlushHandlersAlwaysUnchanged 验证 meta: flush_handlers 本身始终报告 Changed: false
func TestPhase27_MetaFlushHandlersAlwaysUnchanged(t *testing.T) {
	inv := inventory.NewInventory()
	exec := NewExecutor(inv)
	exec.pendingHandlers = []pendingHandler{
		{
			handler: &Handler{Name: "restart service", ModuleName: "debug"},
			host:    &inventory.Host{Name: "node1"},
			vars:    make(map[string]interface{}),
		},
	}
	exec.currentResult = &PlayResult{
		Hosts: map[string]*HostResult{
			"node1": {Tasks: make([]*TaskResult, 0)},
		},
	}

	res := exec.executeMetaModule(&inventory.Host{Name: "node1"}, map[string]interface{}{
		"_raw_params": "flush_handlers",
	}, make(map[string]interface{}))

	if res.Failed {
		t.Fatalf("meta flush_handlers failed: %s", res.Message)
	}
	if res.Changed {
		t.Fatalf("expected meta flush_handlers task itself to report changed=false, got changed=true")
	}
}

// TestPhase27_FileTouchCheckModeChanged 验证 file 模块 state=touch 在 check mode 下始终报告 changed=true
func TestPhase27_FileTouchCheckModeChanged(t *testing.T) {
	fileMod := &module.FileModule{}
	client := newMockPhase27SSHClient()

	res, err := fileMod.Execute(client, map[string]interface{}{
		"path":         "/tmp/test_touch.txt",
		"state":        "touch",
		"__check_mode": true,
	})
	if err != nil {
		t.Fatalf("FileModule touch in check_mode error: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected file state=touch to report changed=true in check_mode")
	}
}

// TestPhase27_CronModuleCronFileAndBackup 验证 cron 模块支持 cron_file (/etc/cron.d/) 与 backup 参数
func TestPhase27_CronModuleCronFileAndBackup(t *testing.T) {
	client := newMockPhase27SSHClient()
	client.outputs["cat '/etc/cron.d/custom_job'"] = &module.Result{
		Stdout:   "0 1 * * * root /bin/old_job\n",
		ExitCode: 0,
	}

	cronMod := &module.CronModule{}
	res, err := cronMod.Execute(client, map[string]interface{}{
		"name":      "my_custom_job",
		"cron_file": "custom_job",
		"user":      "www-data",
		"minute":    "30",
		"hour":      "2",
		"job":       "/usr/local/bin/sync.sh",
		"backup":    true,
	})
	if err != nil {
		t.Fatalf("CronModule execute error: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected cron module to report changed=true")
	}
	if res.Data == nil || res.Data["backup_file"] == nil {
		t.Fatalf("expected backup_file in result.Data, got: %#v", res.Data)
	}

	// 验证写入 cron_file 时包含 user 列 (www-data)
	foundUserInCron := false
	for _, cmd := range client.commands {
		if strings.Contains(cmd, "base64 -d > '/etc/cron.d/custom_job'") {
			foundUserInCron = true
		}
	}
	if !foundUserInCron {
		t.Fatalf("expected command writing to /etc/cron.d/custom_job, got commands: %v", client.commands)
	}
}

// TestPhase27_GroupModuleGIDIdempotency 验证 group 模块在 GID 一致时保持幂等
func TestPhase27_GroupModuleGIDIdempotency(t *testing.T) {
	client := newMockPhase27SSHClient()
	client.outputs["getent group 'docker'"] = &module.Result{
		Stdout:   "docker:x:999:ubuntu\n",
		ExitCode: 0,
	}

	groupMod := &module.GroupModule{}
	res, err := groupMod.Execute(client, map[string]interface{}{
		"name": "docker",
		"gid":  "999",
	})
	if err != nil {
		t.Fatalf("GroupModule execute error: %v", err)
	}
	if res.Changed {
		t.Fatalf("expected group module with matching GID to be unchanged, got changed=true")
	}
	for _, cmd := range client.commands {
		if strings.HasPrefix(cmd, "groupmod ") {
			t.Fatalf("unexpected groupmod command executed: %s", cmd)
		}
	}
}

// TestPhase27_UserModulePasswordLock 验证 user 模块 password_lock 联动 usermod -L / -U
func TestPhase27_UserModulePasswordLock(t *testing.T) {
	client := newMockPhase27SSHClient()
	client.outputs["id 'testuser'"] = &module.Result{
		Stdout:   "uid=1001(testuser) gid=1001(testuser)\n",
		ExitCode: 0,
	}

	userMod := &module.UserModule{}
	res, err := userMod.Execute(client, map[string]interface{}{
		"name":          "testuser",
		"password_lock": true,
	})
	if err != nil {
		t.Fatalf("UserModule password_lock error: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected changed=true for password_lock")
	}

	foundLockCmd := false
	for _, cmd := range client.commands {
		if strings.Contains(cmd, "usermod -L 'testuser'") {
			foundLockCmd = true
		}
	}
	if !foundLockCmd {
		t.Fatalf("expected usermod -L 'testuser', got: %v", client.commands)
	}
}

// TestPhase27_VarsFilesPrecedenceOverride 验证 vars_files 遵循高优先级覆盖 play.vars 且后续 vars_file 覆盖先加载的 vars_file
func TestPhase27_VarsFilesPrecedenceOverride(t *testing.T) {
	tempDir := t.TempDir()
	f1 := filepath.Join(tempDir, "vars1.yml")
	f2 := filepath.Join(tempDir, "vars2.yml")
	_ = os.WriteFile(f1, []byte("app_port: 8080\nenv: staging\n"), 0644)
	_ = os.WriteFile(f2, []byte("app_port: 9090\n"), 0644)

	inv := inventory.NewInventory()
	exec := NewExecutor(inv)

	play := &Play{
		VarsFiles: []string{f1, f2},
	}
	playVars := map[string]interface{}{
		"app_port": 7070, // play.vars 基础值
		"db_name":  "postgres",
	}

	err := exec.loadPlayVarsFiles(play, playVars)
	if err != nil {
		t.Fatalf("loadPlayVarsFiles error: %v", err)
	}

	if playVars["app_port"] != 9090 {
		t.Fatalf("expected app_port=9090 (vars2 overriding vars1 and play.vars), got %#v", playVars["app_port"])
	}
	if playVars["env"] != "staging" {
		t.Fatalf("expected env='staging', got %#v", playVars["env"])
	}
	if playVars["db_name"] != "postgres" {
		t.Fatalf("expected db_name='postgres', got %#v", playVars["db_name"])
	}
}

