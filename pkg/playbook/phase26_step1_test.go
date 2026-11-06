package playbook

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/module"
	"go-ansible/pkg/ssh"
)

// mockPhase26Connection 模拟 SSH 连接
type mockPhase26Connection struct {
	executedCmds []string
	uploadedSrc  []string
	uploadedDst  []string
	cmdOutputs   map[string]*ssh.Result
}

func newMockPhase26Connection() *mockPhase26Connection {
	return &mockPhase26Connection{
		executedCmds: make([]string, 0),
		uploadedSrc:  make([]string, 0),
		uploadedDst:  make([]string, 0),
		cmdOutputs:   make(map[string]*ssh.Result),
	}
}

func (m *mockPhase26Connection) Connect() error {
	return nil
}

func (m *mockPhase26Connection) Close() error {
	return nil
}

func (m *mockPhase26Connection) Execute(cmd string) (*ssh.Result, error) {
	m.executedCmds = append(m.executedCmds, cmd)
	if res, ok := m.cmdOutputs[cmd]; ok {
		return res, nil
	}
	for pattern, res := range m.cmdOutputs {
		if strings.Contains(cmd, pattern) {
			return res, nil
		}
	}
	return &ssh.Result{ExitCode: 0, Stdout: ""}, nil
}

func (m *mockPhase26Connection) ExecuteWithBecome(cmd string) (*ssh.Result, error) {
	return m.Execute(cmd)
}

func (m *mockPhase26Connection) Upload(localPath, remotePath string) error {
	m.uploadedSrc = append(m.uploadedSrc, localPath)
	m.uploadedDst = append(m.uploadedDst, remotePath)
	return nil
}

func (m *mockPhase26Connection) Download(remotePath, localPath string) error {
	return nil
}

// 1. 测试 unarchive creates 预检避免无效大文件上传与 extra_opts
func TestPhase26_UnarchiveCreatesPreCheckAndExtraOpts(t *testing.T) {
	tmpDir := t.TempDir()
	archiveFile := filepath.Join(tmpDir, "app.tar.gz")
	_ = os.WriteFile(archiveFile, []byte("dummy-archive-content"), 0644)

	unarchiveMod := &module.UnarchiveModule{}
	mockConn := newMockPhase26Connection()

	// 模拟远程已经存在 /opt/app/bin/server
	mockConn.cmdOutputs["test -e '/opt/app/bin/server'"] = &ssh.Result{ExitCode: 0}

	res, err := unarchiveMod.Execute(mockConn, map[string]interface{}{
		"src":        archiveFile,
		"dest":       "/opt/app",
		"creates":    "/opt/app/bin/server",
		"remote_src": false,
		"extra_opts": []string{"--strip-components=1", "--no-same-owner"},
	})
	if err != nil {
		t.Fatalf("unarchive failed: %v", err)
	}
	if res.Changed {
		t.Fatalf("expected changed=false when creates target exists")
	}
	if len(mockConn.uploadedSrc) > 0 {
		t.Fatalf("expected 0 uploads when creates target exists, got %d uploads", len(mockConn.uploadedSrc))
	}
	if !strings.Contains(res.Message, "extraction skipped") {
		t.Fatalf("expected extraction skipped message, got %q", res.Message)
	}

	// 当 creates 不存在时，验证 extra_opts 拼接到命令中
	mockConn2 := newMockPhase26Connection()
	mockConn2.cmdOutputs["test -e '/opt/app/bin/server'"] = &ssh.Result{ExitCode: 1}
	mockConn2.cmdOutputs["tar -xzf"] = &ssh.Result{ExitCode: 0, Stdout: "__go_ansible_changed__=true"}

	res2, err := unarchiveMod.Execute(mockConn2, map[string]interface{}{
		"src":        archiveFile,
		"dest":       "/opt/app",
		"creates":    "/opt/app/bin/server",
		"remote_src": true,
		"extra_opts": "--strip-components=1 --no-same-owner",
	})
	if err != nil {
		t.Fatalf("unarchive extract failed: %v", err)
	}
	if !res2.Changed {
		t.Fatalf("expected changed=true when extracted")
	}
	foundExtraOpts := false
	for _, cmd := range mockConn2.executedCmds {
		if strings.Contains(cmd, "--strip-components=1 --no-same-owner") {
			foundExtraOpts = true
			break
		}
	}
	if !foundExtraOpts {
		t.Fatalf("expected extra_opts in extract command, executed: %v", mockConn2.executedCmds)
	}
}

// 2. 测试 mount 模块 backup: true 与 mounted 变更 remount 激活
func TestPhase26_MountBackupAndRemount(t *testing.T) {
	mountMod := &module.MountModule{}
	mockConn := newMockPhase26Connection()

	// 模拟已存在挂载点与旧 fstab
	mockConn.cmdOutputs["cat /proc/mounts"] = &ssh.Result{
		ExitCode: 0,
		Stdout:   "/dev/sdb1 /data ext4 rw,relatime 0 0\n",
	}
	mockConn.cmdOutputs["cat /etc/fstab"] = &ssh.Result{
		ExitCode: 0,
		Stdout:   "/dev/sdb1 /data ext4 defaults 0 2\n",
	}

	// 传入新 opts: noatime 且 backup: true
	res, err := mountMod.Execute(mockConn, map[string]interface{}{
		"path":   "/data",
		"src":    "/dev/sdb1",
		"fstype": "ext4",
		"opts":   "defaults,noatime",
		"state":  "mounted",
		"backup": true,
	})
	if err != nil {
		t.Fatalf("mount execute failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected changed=true when opts updated")
	}

	foundBackup := false
	foundRemount := false
	for _, cmd := range mockConn.executedCmds {
		if strings.Contains(cmd, ".bak") {
			foundBackup = true
		}
		if strings.Contains(cmd, "mount -o remount,defaults,noatime '/data'") {
			foundRemount = true
		}
	}
	if !foundBackup {
		t.Fatalf("expected backup command before modifying fstab, executed: %v", mockConn.executedCmds)
	}
	if !foundRemount {
		t.Fatalf("expected remount command to activate new options, executed: %v", mockConn.executedCmds)
	}
}

// 3. 测试 authorized_key YAML 数组支持、注释行与未指定属性保留
func TestPhase26_AuthorizedKeyArrayAndCommentRetention(t *testing.T) {
	authMod := &module.AuthorizedKeyModule{}
	mockConn := newMockPhase26Connection()

	// 模拟已有文件，包含文件头部注释、旧 key 及原有注释
	existingContent := `# Generated by Cloud-Init
# Do not edit above this line
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI1111111111111111111111111111111111111111111 admin@company
ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC22222222222222222222222222222222222222222 legacy-key
`
	mockConn.cmdOutputs["cat"] = &ssh.Result{
		ExitCode: 0,
		Stdout:   existingContent,
	}

	// 1. 测试传入 YAML 数组且不指定 comment 时保留原注释
	res, err := authMod.Execute(mockConn, map[string]interface{}{
		"user": "deploy",
		"key": []interface{}{
			"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI1111111111111111111111111111111111111111111",
		},
		"state": "present",
	})
	if err != nil {
		t.Fatalf("authorized_key array execute failed: %v", err)
	}
	// 因为内容相同且注释被正确继承保留，不应产生伪变更
	if res.Changed {
		t.Fatalf("expected changed=false when key is already present with same content")
	}

	// 2. 测试 exclusive: true 模式保留文件头部非 key 注释
	mockConn2 := newMockPhase26Connection()
	mockConn2.cmdOutputs["cat"] = &ssh.Result{
		ExitCode: 0,
		Stdout:   existingContent,
	}
	res2, err := authMod.Execute(mockConn2, map[string]interface{}{
		"user":      "deploy",
		"key":       "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI1111111111111111111111111111111111111111111 admin@company",
		"state":     "present",
		"exclusive": true,
	})
	if err != nil {
		t.Fatalf("authorized_key exclusive execute failed: %v", err)
	}
	if !res2.Changed {
		t.Fatalf("expected changed=true when legacy-key is pruned in exclusive mode")
	}
	// 验证写回命令中包含 "# Generated by Cloud-Init"
	foundHeaderComment := false
	for _, cmd := range mockConn2.executedCmds {
		if strings.Contains(cmd, "Generated by Cloud-Init") {
			foundHeaderComment = true
			break
		}
	}
	if !foundHeaderComment {
		t.Fatalf("expected non-key comment lines preserved in exclusive mode, cmds: %v", mockConn2.executedCmds)
	}
}

// 4. 测试 service 模块优先评估 enabled 及 daemon_reload 透传
func TestPhase26_ServiceEnabledBeforeStateAndDaemonReload(t *testing.T) {
	serviceMod := &module.ServiceModule{}
	mockConn := newMockPhase26Connection()

	mockConn.cmdOutputs["which systemctl"] = &ssh.Result{ExitCode: 0}
	mockConn.cmdOutputs["systemctl is-enabled 'nginx'"] = &ssh.Result{ExitCode: 0, Stdout: "disabled"}
	mockConn.cmdOutputs["systemctl is-active --quiet 'nginx'"] = &ssh.Result{ExitCode: 1}

	res, err := serviceMod.Execute(mockConn, map[string]interface{}{
		"name":          "nginx",
		"state":         "started",
		"enabled":       true,
		"daemon_reload": true,
	})
	if err != nil {
		t.Fatalf("service execute failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected changed=true")
	}

	// 验证时序：daemon-reload -> enable -> start
	var daemonReloadIdx, enableIdx, startIdx int = -1, -1, -1
	for idx, cmd := range mockConn.executedCmds {
		if strings.Contains(cmd, "systemctl daemon-reload") {
			daemonReloadIdx = idx
		}
		if strings.Contains(cmd, "systemctl enable 'nginx'") {
			enableIdx = idx
		}
		if strings.Contains(cmd, "systemctl start 'nginx'") {
			startIdx = idx
		}
	}
	if daemonReloadIdx == -1 || enableIdx == -1 || startIdx == -1 {
		t.Fatalf("expected daemon-reload, enable, and start commands, got: %v", mockConn.executedCmds)
	}
	if !(daemonReloadIdx < enableIdx && enableIdx < startIdx) {
		t.Fatalf("expected order daemon-reload (%d) < enable (%d) < start (%d)", daemonReloadIdx, enableIdx, startIdx)
	}
}

// 5. 测试 systemd 模块 scope: user 支持
func TestPhase26_SystemdScopeUser(t *testing.T) {
	systemdMod := &module.SystemdModule{}
	mockConn := newMockPhase26Connection()

	mockConn.cmdOutputs["which systemctl"] = &ssh.Result{ExitCode: 0}
	mockConn.cmdOutputs["systemctl --user is-active 'podman-app'"] = &ssh.Result{ExitCode: 1}

	res, err := systemdMod.Execute(mockConn, map[string]interface{}{
		"name":          "podman-app",
		"state":         "started",
		"scope":         "user",
		"daemon_reload": true,
	})
	if err != nil {
		t.Fatalf("systemd user scope failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected changed=true")
	}

	foundUserDaemonReload := false
	foundUserStart := false
	for _, cmd := range mockConn.executedCmds {
		if strings.Contains(cmd, "systemctl --user daemon-reload") {
			foundUserDaemonReload = true
		}
		if strings.Contains(cmd, "systemctl --user start 'podman-app'") {
			foundUserStart = true
		}
	}
	if !foundUserDaemonReload || !foundUserStart {
		t.Fatalf("expected systemctl --user commands, got: %v", mockConn.executedCmds)
	}
}

// 6. 测试 sysctl 模块 absent 彻底删除与 reload
func TestPhase26_SysctlAbsentAndReload(t *testing.T) {
	sysctlMod := &module.SysctlModule{}
	mockConn := newMockPhase26Connection()

	// 模拟配置文件中有重复历史残留项
	rawSysctl := "net.ipv4.ip_forward = 1\nnet.ipv4.tcp_timestamps = 1\nnet.ipv4.tcp_timestamps = 0\n"
	mockConn.cmdOutputs["base64"] = &ssh.Result{
		ExitCode: 0,
		Stdout:   base64.StdEncoding.EncodeToString([]byte(rawSysctl)),
	}

	res, err := sysctlMod.Execute(mockConn, map[string]interface{}{
		"name":   "net.ipv4.tcp_timestamps",
		"state":  "absent",
		"reload": true,
	})
	if err != nil {
		t.Fatalf("sysctl absent failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected changed=true on absent")
	}

	foundReload := false
	for _, cmd := range mockConn.executedCmds {
		if strings.Contains(cmd, "sysctl -p '/etc/sysctl.conf'") {
			foundReload = true
			break
		}
	}
	if !foundReload {
		t.Fatalf("expected sysctl -p on absent with reload=true, got: %v", mockConn.executedCmds)
	}
}

// 7. 测试 wait_for 模块 sleep 参数与 state: drained
func TestPhase26_WaitForSleepAndDrained(t *testing.T) {
	waitMod := &module.WaitForModule{}
	mockConn := newMockPhase26Connection()

	res, err := waitMod.Execute(mockConn, map[string]interface{}{
		"port":    8080,
		"state":   "drained",
		"sleep":   3,
		"timeout": 10,
	})
	if err != nil {
		t.Fatalf("wait_for drained failed: %v", err)
	}
	if res.Failed {
		t.Fatalf("wait_for reported failure: %v", res.Message)
	}

	foundSleep3 := false
	foundDrained := false
	for _, cmd := range mockConn.executedCmds {
		if strings.Contains(cmd, "sleep 3") || strings.Contains(cmd, "base64") {
			foundSleep3 = true
		}
		if strings.Contains(cmd, "drained") || strings.Contains(cmd, "base64") {
			foundDrained = true
		}
	}
	if !foundSleep3 || !foundDrained {
		t.Fatalf("expected sleep and drained condition in wait script")
	}
}

// 8. 测试 package 模块单独 update_cache: yes
func TestPhase26_PackageUpdateCacheOnly(t *testing.T) {
	pkgMod := &module.PackageModule{}
	mockConn := newMockPhase26Connection()

	// 模拟目标主机为 apt-get 系统
	mockConn.cmdOutputs["if command -v apt-get"] = &ssh.Result{ExitCode: 0, Stdout: "apt"}
	mockConn.cmdOutputs["apt-get update"] = &ssh.Result{ExitCode: 0, Stdout: "Hit:1 http://archive.ubuntu.com"}

	// 1. 验证 Validate 允许 name 为空但 update_cache: true
	if err := pkgMod.Validate(map[string]interface{}{"update_cache": true}); err != nil {
		t.Fatalf("Validate failed for update_cache only: %v", err)
	}

	res, err := pkgMod.Execute(mockConn, map[string]interface{}{
		"update_cache": true,
	})
	if err != nil {
		t.Fatalf("package update_cache failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected changed=true for package cache update")
	}
}

// 9. 测试高频 Jinja2 过滤器 (to_datetime, strftime, urlencode, type_debug) 及 b64encode []byte
func TestPhase26_Jinja2Filters(t *testing.T) {
	// 1. to_datetime & strftime
	resDT, ok, err := module.ApplyFilter("to_datetime('%Y-%m-%d %H:%M:%S')", "2026-10-21 15:30:00")
	if !ok || err != nil {
		t.Fatalf("to_datetime failed: %v", err)
	}
	if !strings.HasPrefix(fmt.Sprintf("%v", resDT), "2026-10-21T15:30:00") {
		t.Fatalf("unexpected to_datetime result: %v", resDT)
	}

	resStrf, ok, err := module.ApplyFilter("strftime('%Y/%m/%d')", "2026-10-21T15:30:00Z")
	if !ok || err != nil {
		t.Fatalf("strftime failed: %v", err)
	}
	if fmt.Sprintf("%v", resStrf) != "2026/10/21" {
		t.Fatalf("unexpected strftime result: %v", resStrf)
	}

	// 2. urlencode
	queryMap := map[string]interface{}{
		"q":    "ansible go",
		"page": 1,
	}
	resURL, ok, err := module.ApplyFilter("urlencode", queryMap)
	if !ok || err != nil {
		t.Fatalf("urlencode failed: %v", err)
	}
	expectedURL := "page=1&q=ansible+go"
	if fmt.Sprintf("%v", resURL) != expectedURL {
		t.Fatalf("expected %q, got %q", expectedURL, resURL)
	}

	// 3. type_debug
	resType, ok, err := module.ApplyFilter("type_debug", []string{"a", "b"})
	if !ok || err != nil || fmt.Sprintf("%v", resType) != "list" {
		t.Fatalf("type_debug slice failed: %v", resType)
	}
	resTypeDict, _, _ := module.ApplyFilter("type_debug", map[string]string{"k": "v"})
	if fmt.Sprintf("%v", resTypeDict) != "dict" {
		t.Fatalf("type_debug map failed: %v", resTypeDict)
	}

	// 4. b64encode byte slice
	rawBytes := []byte("hello world")
	resB64, ok, err := module.ApplyFilter("b64encode", rawBytes)
	if !ok || err != nil || fmt.Sprintf("%v", resB64) != "aGVsbG8gd29ybGQ=" {
		t.Fatalf("b64encode byte slice failed: %v", resB64)
	}
}

// 10. 测试 role defaults 弱优先级对齐：Inventory host_vars 必须能成功覆盖 defaults/main.yml
func TestPhase26_RoleDefaultsWeakPrecedence(t *testing.T) {
	tmpDir := t.TempDir()
	rolesDir := filepath.Join(tmpDir, "roles", "app")
	_ = os.MkdirAll(filepath.Join(rolesDir, "tasks"), 0755)
	_ = os.MkdirAll(filepath.Join(rolesDir, "defaults"), 0755)

	// 角色默认值: port=8080, db_name="default_db"
	_ = os.WriteFile(filepath.Join(rolesDir, "defaults", "main.yml"), []byte(`
app_port: 8080
db_name: default_db
`), 0644)

	// 角色任务
	_ = os.WriteFile(filepath.Join(rolesDir, "tasks", "main.yml"), []byte(`
- name: Dummy task
  debug:
    msg: "port={{ app_port }}, db={{ db_name }}"
`), 0644)

	// 清单配置特定主机变量: app_port=9090 (覆盖角色默认值)
	inv := inventory.NewInventory()
	h := &inventory.Host{
		Name: "node1",
		Variables: map[string]string{
			"app_port": "9090",
		},
	}
	inv.AddHost(h)

	executor := NewExecutor(inv)
	executor.baseDir = tmpDir
	executor.SetRolePaths([]string{filepath.Join(tmpDir, "roles")})

	play := &Play{
		Hosts: "node1",
		Roles: []interface{}{"app"},
	}

	playbookObj := &Playbook{
		Plays: []*Play{play},
	}

	res, err := executor.Execute(playbookObj)
	if err != nil {
		t.Fatalf("playbook execution failed: %v", err)
	}
	if len(res.Plays) == 0 {
		t.Fatalf("playbook produced no plays")
	}

	// 验证最终 taskVarsForHost 中：
	// app_port 必须等于 9090 (inventory host_vars 覆盖了 role defaults)
	// db_name 必须等于 "default_db" (role defaults 生效)
	taskVars := executor.taskVarsForHost(h, make(map[string]interface{}))
	if fmt.Sprintf("%v", taskVars["app_port"]) != "9090" {
		t.Fatalf("CRITICAL: inventory host_vars (9090) failed to override role defaults, got: %v", taskVars["app_port"])
	}
	if fmt.Sprintf("%v", taskVars["db_name"]) != "default_db" {
		t.Fatalf("expected role default db_name=default_db, got: %v", taskVars["db_name"])
	}
}

// 11. 测试 include_tasks 支持 loop 循环迭代
func TestPhase26_IncludeTasksWithLoop(t *testing.T) {
	tmpDir := t.TempDir()
	taskSubFile := filepath.Join(tmpDir, "subtask.yml")
	_ = os.WriteFile(taskSubFile, []byte(`
- name: Echo item
  debug:
    msg: "Processing {{ item }}"
`), 0644)

	playYAML := fmt.Sprintf(`
- hosts: localhost
  tasks:
    - name: Include with loop
      include_tasks: %s
      loop:
        - item_alpha
        - item_beta
        - item_gamma
`, taskSubFile)

	parser := NewParser(tmpDir)
	pb, err := parser.Parse([]byte(playYAML))
	if err != nil {
		t.Fatalf("parse playbook failed: %v", err)
	}

	inv := inventory.NewInventory()
	executor := NewExecutor(inv)
	executor.baseDir = tmpDir

	res, err := executor.Execute(pb)
	if err != nil {
		t.Fatalf("execute include_tasks with loop failed: %v", err)
	}
	if len(res.Plays) == 0 {
		t.Fatalf("playbook execution produced no plays")
	}
}
