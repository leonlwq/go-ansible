package playbook

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/module"
	"go-ansible/pkg/ssh"
)

// 1. 测试 Inventory 主机范围表达式展开 [01:10], [a:c], [1:5:2] 以及 IPv6
func TestPhase28_HostRangeExpansion(t *testing.T) {
	iniContent := `
[web]
web[01:03].prod.example.com ansible_port=2222 ansible_user=deployer

[db]
db-[a:c] ansible_user=postgres

[cache]
cache[1:5:2]

[ipv6_nodes]
[2001:db8::1]:2222
`
	inv, err := inventory.ParseINI([]byte(iniContent))
	if err != nil {
		t.Fatalf("failed to parse INI with ranges: %v", err)
	}

	// 1. web 组：前导 0 保持
	webHosts, err := inv.GetGroupHosts("web")
	if err != nil {
		t.Fatalf("failed to get web hosts: %v", err)
	}
	if len(webHosts) != 3 {
		t.Fatalf("expected 3 web hosts, got %d", len(webHosts))
	}
	expectedWebNames := []string{"web01.prod.example.com", "web02.prod.example.com", "web03.prod.example.com"}
	for i, h := range webHosts {
		if h.Name != expectedWebNames[i] {
			t.Errorf("expected web host %d to be %q, got %q", i, expectedWebNames[i], h.Name)
		}
		if h.Port != 2222 {
			t.Errorf("expected port 2222 for %s, got %d", h.Name, h.Port)
		}
		if h.User != "deployer" {
			t.Errorf("expected user deployer for %s, got %q", h.Name, h.User)
		}
	}

	// 2. db 组：字母范围 [a:c]
	dbHosts, err := inv.GetGroupHosts("db")
	if err != nil {
		t.Fatalf("failed to get db hosts: %v", err)
	}
	if len(dbHosts) != 3 {
		t.Fatalf("expected 3 db hosts, got %d", len(dbHosts))
	}
	expectedDbNames := []string{"db-a", "db-b", "db-c"}
	for i, h := range dbHosts {
		if h.Name != expectedDbNames[i] {
			t.Errorf("expected db host %d to be %q, got %q", i, expectedDbNames[i], h.Name)
		}
		if h.User != "postgres" {
			t.Errorf("expected user postgres for %s, got %q", h.Name, h.User)
		}
	}

	// 3. cache 组：带步长 [1:5:2]
	cacheHosts, err := inv.GetGroupHosts("cache")
	if err != nil {
		t.Fatalf("failed to get cache hosts: %v", err)
	}
	if len(cacheHosts) != 3 {
		t.Fatalf("expected 3 cache hosts, got %d", len(cacheHosts))
	}
	expectedCacheNames := []string{"cache1", "cache3", "cache5"}
	for i, h := range cacheHosts {
		if h.Name != expectedCacheNames[i] {
			t.Errorf("expected cache host %d to be %q, got %q", i, expectedCacheNames[i], h.Name)
		}
	}

	// 4. ipv6_nodes 组：IPv6 带端口
	ipv6Hosts, err := inv.GetGroupHosts("ipv6_nodes")
	if err != nil {
		t.Fatalf("failed to get ipv6 hosts: %v", err)
	}
	if len(ipv6Hosts) != 1 {
		t.Fatalf("expected 1 ipv6 host, got %d", len(ipv6Hosts))
	}
	if ipv6Hosts[0].Name != "2001:db8::1" {
		t.Errorf("expected IPv6 host to be '2001:db8::1', got %q", ipv6Hosts[0].Name)
	}
	if ipv6Hosts[0].Port != 2222 {
		t.Errorf("expected port 2222 for IPv6 host, got %d", ipv6Hosts[0].Port)
	}
}

// 2. 测试内联主机语法 (-i "192.168.1.10:2222," 以及范围)
func TestPhase28_InlineHostPortAndRange(t *testing.T) {
	// 单主机带端口
	inv1, err := inventory.ParseInlineHosts("192.168.1.10:2222,")
	if err != nil {
		t.Fatalf("failed to parse inline host with port: %v", err)
	}
	h1, ok := inv1.AllHosts["192.168.1.10"]
	if !ok {
		t.Fatalf("expected host '192.168.1.10' in inventory")
	}
	if h1.Port != 2222 {
		t.Fatalf("expected port 2222, got %d", h1.Port)
	}

	// 内联带范围与端口
	inv2, err := inventory.ParseInlineHosts("node[1:3]:8080,")
	if err != nil {
		t.Fatalf("failed to parse inline host range: %v", err)
	}
	if len(inv2.AllHosts) != 3 {
		t.Fatalf("expected 3 inline hosts, got %d", len(inv2.AllHosts))
	}
	for _, name := range []string{"node1", "node2", "node3"} {
		h, exists := inv2.AllHosts[name]
		if !exists {
			t.Errorf("expected host %s to exist", name)
		} else if h.Port != 8080 {
			t.Errorf("expected host %s to have port 8080, got %d", name, h.Port)
		}
	}
}

// 3. 测试组循环引用死锁与栈溢出防护
func TestPhase28_GroupCycleDetection(t *testing.T) {
	inv := inventory.NewInventory()
	groupA := &inventory.Group{
		Name:     "groupA",
		Hosts:    []*inventory.Host{{Name: "hostA"}},
		Children: make([]*inventory.Group, 0),
	}
	groupB := &inventory.Group{
		Name:     "groupB",
		Hosts:    []*inventory.Host{{Name: "hostB"}},
		Children: make([]*inventory.Group, 0),
	}

	// 构成互相引用的环形依赖: groupA -> groupB -> groupA
	groupA.Children = append(groupA.Children, groupB)
	groupB.Children = append(groupB.Children, groupA)

	inv.Groups["groupA"] = groupA
	inv.Groups["groupB"] = groupB

	// 获取组内主机，必须安全退出且不发生栈溢出崩溃
	hosts, err := inv.GetGroupHosts("groupA")
	if err != nil {
		t.Fatalf("GetHostsInGroup failed: %v", err)
	}
	if len(hosts) != 2 {
		t.Fatalf("expected 2 hosts (hostA, hostB), got %d", len(hosts))
	}
}

// 4. 测试祖先组变量继承与子组优先级覆盖
func TestPhase28_AncestorGroupVariablesInheritance(t *testing.T) {
	iniContent := `
[prod:vars]
app_env=production
timeout=10
cluster=k8s

[web:vars]
timeout=30

[prod:children]
web

[web]
web1
`
	inv, err := inventory.ParseINI([]byte(iniContent))
	if err != nil {
		t.Fatalf("failed to parse INI: %v", err)
	}

	web1, ok := inv.AllHosts["web1"]
	if !ok {
		t.Fatalf("host web1 not found")
	}

	vars := inv.ResolveHostVars(web1)
	// app_env 继承自祖先组 prod
	if vars["app_env"] != "production" {
		t.Errorf("expected app_env=production from parent group, got: %q", vars["app_env"])
	}
	if vars["cluster"] != "k8s" {
		t.Errorf("expected cluster=k8s from parent group, got: %q", vars["cluster"])
	}
	// timeout 应被子组 web 覆盖为 30（而不是 prod 的 10）
	if vars["timeout"] != "30" {
		t.Errorf("expected timeout=30 (child group override), got: %q", vars["timeout"])
	}
}

// 辅助 Mock 带有 Timeout 控制能力的 Connection
type mockTimeoutConn struct {
	timeout time.Duration
	cmds    []string
}

func (m *mockTimeoutConn) Connect() error                               { return nil }
func (m *mockTimeoutConn) Close() error                                 { return nil }
func (m *mockTimeoutConn) ExecuteWithBecome(cmd string) (*ssh.Result, error) { return m.Execute(cmd) }
func (m *mockTimeoutConn) Upload(local, remote string) error            { return nil }
func (m *mockTimeoutConn) Download(remote, local string) error          { return nil }
func (m *mockTimeoutConn) SetTimeout(d time.Duration)                   { m.timeout = d }
func (m *mockTimeoutConn) GetTimeout() time.Duration                    { return m.timeout }

func (m *mockTimeoutConn) Execute(cmd string) (*ssh.Result, error) {
	m.cmds = append(m.cmds, cmd)
	if strings.Contains(cmd, "boot_id") || strings.Contains(cmd, "uptime -s") {
		// 返回前置 Boot ID
		return &ssh.Result{Stdout: "boot-id-123", ExitCode: 0}, nil
	}
	return &ssh.Result{Stdout: "ok", ExitCode: 0}, nil
}

// 5. 测试 reboot 模块 msg 广播与 connect_timeout 动态下发
func TestPhase28_RebootModuleMsgAndConnectTimeout(t *testing.T) {
	rebootMod := &module.RebootModule{}
	conn := &mockTimeoutConn{timeout: 30 * time.Second}

	params := map[string]interface{}{
		"msg":             "Server patching in progress",
		"connect_timeout": 7,
		"reboot_timeout":  1,
	}

	res, _ := rebootMod.Execute(conn, params)
	if res == nil {
		t.Fatalf("expected non-nil result from reboot module")
	}

	// 验证结果数据包含 msg
	if res.Data["msg"] != "Server patching in progress" {
		t.Errorf("expected msg in result data, got: %v", res.Data["msg"])
	}

	// 验证下发命令包含广播消息
	var foundShutdown bool
	for _, cmd := range conn.cmds {
		if strings.Contains(cmd, "shutdown -r now 'Server patching in progress'") {
			foundShutdown = true
			break
		}
	}
	if !foundShutdown {
		t.Errorf("expected shutdown command with msg, commands were: %v", conn.cmds)
	}

	// 验证执行完毕后连接超时恢复为原始值 30s
	if conn.timeout != 30*time.Second {
		t.Errorf("expected conn timeout restored to 30s, got: %v", conn.timeout)
	}
}

// 辅助 Mock 普通 Connection 捕获执行命令
type mockPipConn struct {
	cmds []string
}

func (m *mockPipConn) Connect() error                               { return nil }
func (m *mockPipConn) Close() error                                 { return nil }
func (m *mockPipConn) ExecuteWithBecome(cmd string) (*ssh.Result, error) { return m.Execute(cmd) }
func (m *mockPipConn) Upload(local, remote string) error            { return nil }
func (m *mockPipConn) Download(remote, local string) error          { return nil }
func (m *mockPipConn) Execute(cmd string) (*ssh.Result, error) {
	m.cmds = append(m.cmds, cmd)
	if strings.Contains(cmd, "test -f") {
		return &ssh.Result{ExitCode: 1}, nil // 模拟 virtualenv 尚未创建
	}
	return &ssh.Result{Stdout: "Successfully installed", ExitCode: 0}, nil
}

// 6. 测试 pip 模块 editable, virtualenv_site_packages 与 umask
func TestPhase28_PipModuleEditableVirtualenvSitePackagesUmask(t *testing.T) {
	pipMod := &module.PipModule{}
	conn := &mockPipConn{}

	params := map[string]interface{}{
		"name":                     "flask",
		"editable":                 true,
		"virtualenv":               "/opt/myenv",
		"virtualenv_site_packages": true,
		"umask":                    "0022",
	}

	res, err := pipMod.Execute(conn, params)
	if err != nil {
		t.Fatalf("pip execute failed: %v", err)
	}
	if !res.Changed {
		t.Errorf("expected changed=true for virtualenv creation and install")
	}

	var foundVenvCreate, foundPipInstall bool
	for _, cmd := range conn.cmds {
		if strings.Contains(cmd, "--system-site-packages") && strings.Contains(cmd, "umask 0022 &&") {
			foundVenvCreate = true
		}
		if strings.Contains(cmd, "install -e 'flask'") && strings.Contains(cmd, "umask 0022 &&") {
			foundPipInstall = true
		}
	}
	if !foundVenvCreate {
		t.Errorf("expected venv creation command with --system-site-packages and umask, got: %v", conn.cmds)
	}
	if !foundPipInstall {
		t.Errorf("expected pip install command with -e and umask, got: %v", conn.cmds)
	}
}

// 7. 测试 synchronize 模块参数：existing_only, partial 与 ssh args 转发
func TestPhase28_SynchronizeModuleNewArgs(t *testing.T) {
	syncMod := &module.SynchronizeModule{}
	var capturedArgs []string
	restore := module.SetRsyncCmdRunnerForTest(func(cmd *exec.Cmd) ([]byte, []byte, int, error) {
		capturedArgs = cmd.Args
		return []byte("<f+++++++++ test.txt\n"), nil, 0, nil
	})
	defer restore()

	h := &inventory.Host{
		Name:    "remote-node",
		Address: "10.0.0.5",
		User:    "deploy",
		Port:    2200,
		Variables: map[string]string{
			"ansible_ssh_extra_args": "-o StrictHostKeyChecking=no",
		},
	}
	mockConn := &mockHostConn{host: h}

	params := map[string]interface{}{
		"src":           "/local/path",
		"dest":          "/remote/path",
		"existing_only": true,
		"partial":       true,
	}

	res, err := syncMod.Execute(mockConn, params)
	if err != nil {
		t.Fatalf("synchronize execute failed: %v", err)
	}
	if !res.Changed {
		t.Errorf("expected changed=true")
	}

	fullCmd := strings.Join(capturedArgs, " ")
	if !strings.Contains(fullCmd, "--existing") {
		t.Errorf("expected --existing in rsync args, got: %s", fullCmd)
	}
	if !strings.Contains(fullCmd, "--partial") {
		t.Errorf("expected --partial in rsync args, got: %s", fullCmd)
	}
	if !strings.Contains(fullCmd, "-o StrictHostKeyChecking=no") {
		t.Errorf("expected ansible_ssh_extra_args forwarded into ssh command, got: %s", fullCmd)
	}
}

type mockHostConn struct {
	host *inventory.Host
}

func (m *mockHostConn) Connect() error                               { return nil }
func (m *mockHostConn) Close() error                                 { return nil }
func (m *mockHostConn) Execute(cmd string) (*ssh.Result, error)     { return &ssh.Result{ExitCode: 0}, nil }
func (m *mockHostConn) ExecuteWithBecome(cmd string) (*ssh.Result, error) { return &ssh.Result{ExitCode: 0}, nil }
func (m *mockHostConn) Upload(local, remote string) error            { return nil }
func (m *mockHostConn) Download(remote, local string) error          { return nil }
func (m *mockHostConn) Host() *inventory.Host                       { return m.host }

// 8. 测试 loop_control.pause 暂停控制
func TestPhase28_LoopControlPause(t *testing.T) {
	playbookYAML := `
- hosts: localhost
  gather_facts: false
  tasks:
    - name: Pausing loop
      debug:
        msg: "Item {{ item }}"
      loop:
        - 1
        - 2
        - 3
      loop_control:
        pause: 0.05
`
	inv := inventory.NewInventory()
	inv.AllHosts["localhost"] = &inventory.Host{Name: "localhost", Connection: "local"}
	executor := NewExecutor(inv)

	pb, err := NewParser("").Parse([]byte(playbookYAML))
	if err != nil {
		t.Fatalf("failed to parse playbook: %v", err)
	}

	start := time.Now()
	res, err := executor.Execute(pb)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("playbook execution failed: %v", err)
	}
	if len(res.Plays) == 0 {
		t.Fatalf("expected at least 1 play")
	}

	// 3 个项目之间有 2 次暂停，每次 0.05s = 50ms，总时间应大于等于 90ms
	if elapsed < 90*time.Millisecond {
		t.Errorf("expected loop to pause at least 90ms, elapsed: %v", elapsed)
	}
}
