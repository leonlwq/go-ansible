package module

import (
	"strings"
	"testing"

	"go-ansible/pkg/ssh"
)

// mockPhase34Conn 模拟 SSH 连接用于测试
type mockPhase34Conn struct {
	executed []string
	outputs  map[string]string
	exitCode map[string]int
}

func (m *mockPhase34Conn) Execute(cmd string) (*ssh.Result, error) {
	m.executed = append(m.executed, cmd)
	code := 0
	if c, ok := m.exitCode[cmd]; ok {
		code = c
	}
	out := ""
	if o, ok := m.outputs[cmd]; ok {
		out = o
	}
	for k, v := range m.outputs {
		if strings.Contains(cmd, k) {
			out = v
			break
		}
	}
	return &ssh.Result{
		Stdout:   out,
		ExitCode: code,
	}, nil
}

func (m *mockPhase34Conn) Connect() error                               { return nil }
func (m *mockPhase34Conn) ExecuteWithBecome(cmd string) (*ssh.Result, error) {
	return m.Execute(cmd)
}
func (m *mockPhase34Conn) Upload(local, remote string) error   { return nil }
func (m *mockPhase34Conn) Download(remote, local string) error { return nil }
func (m *mockPhase34Conn) Close() error                        { return nil }

// 1. 测试 lineinfile 与 blockinfile 的 owner, group, mode 权限应用
func TestPhase34_LineinfileAndBlockinfile_Permissions(t *testing.T) {
	conn := &mockPhase34Conn{
		outputs: map[string]string{
			"cat ":   "line1\nline2\n",
			"stat ":  "mode=0600 owner=root group=root",
			"test -": "",
		},
	}

	lineMod := &LineinfileModule{}
	res, err := lineMod.Execute(conn, map[string]interface{}{
		"path":  "/etc/security/limits.conf",
		"line":  "line1", // content already exists
		"mode":  "0644",
		"owner": "root",
		"group": "wheel",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected changed=true due to permission update")
	}

	// 验证 blockinfile 同样应用 mode / owner / group
	blockMod := &BlockinfileModule{}
	bRes, err := blockMod.Execute(conn, map[string]interface{}{
		"path":   "/etc/sudoers.d/custom",
		"block":  "%wheel ALL=(ALL) NOPASSWD: ALL",
		"create": true,
		"mode":   "0440",
		"owner":  "root",
		"group":  "root",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bRes.Changed {
		t.Fatalf("expected changed=true on blockinfile create")
	}

	// 检查是否调用了 chmod 0440
	foundChmod := false
	for _, cmd := range conn.executed {
		if strings.Contains(cmd, "chmod 0440") || strings.Contains(cmd, "chmod 440") {
			foundChmod = true
			break
		}
	}
	if !foundChmod {
		t.Fatalf("expected chmod 0440 to be executed, executed commands: %v", conn.executed)
	}
}

// 2. 测试 user 模块 generate_ssh_key 参数支持
func TestPhase34_User_GenerateSSHKey(t *testing.T) {
	conn := &mockPhase34Conn{
		outputs: map[string]string{
			"id ":            "",
			"getent passwd": "deploy:x:1001:1001::/home/deploy:/bin/bash",
			"cat ":           "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC deploy@local",
		},
		exitCode: map[string]int{
			"id deploy 2>/dev/null": 0, // user already exists
			"test -f '/home/deploy/.ssh/id_rsa'": 1, // key doesn't exist yet
		},
	}

	userMod := &UserModule{}
	res, err := userMod.Execute(conn, map[string]interface{}{
		"name":             "deploy",
		"generate_ssh_key": true,
		"ssh_key_bits":     2048,
		"ssh_key_type":     "rsa",
		"ssh_key_comment":  "deploy-key",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected changed=true when ssh key is newly generated")
	}
	if res.Data == nil {
		t.Fatalf("expected Data map to be populated")
	}
	if res.Data["ssh_key_file"] != "/home/deploy/.ssh/id_rsa" {
		t.Fatalf("unexpected ssh_key_file: %v", res.Data["ssh_key_file"])
	}
	if !strings.Contains(res.Data["ssh_public_key"].(string), "AAAAB3NzaC1yc2E") {
		t.Fatalf("unexpected ssh_public_key: %v", res.Data["ssh_public_key"])
	}

	// 验证是否调用了 ssh-keygen
	foundKeygen := false
	for _, cmd := range conn.executed {
		if strings.Contains(cmd, "ssh-keygen") && strings.Contains(cmd, "-t rsa") {
			foundKeygen = true
			break
		}
	}
	if !foundKeygen {
		t.Fatalf("expected ssh-keygen command, got: %v", conn.executed)
	}
}

// 3. 测试 cron 模块写入 /etc/cron.d/ 时加固 0644 权限
func TestPhase34_Cron_D_Permissions(t *testing.T) {
	conn := &mockPhase34Conn{
		outputs: map[string]string{
			"cat ": "",
		},
	}

	cronMod := &CronModule{}
	res, err := cronMod.Execute(conn, map[string]interface{}{
		"name":      "hourly backup",
		"cron_file": "backup_job",
		"minute":    "0",
		"hour":      "*",
		"job":       "/usr/local/bin/backup.sh",
		"user":      "root",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected changed=true on new cron job")
	}

	foundChmod := false
	for _, cmd := range conn.executed {
		if strings.Contains(cmd, "chmod 0644") && strings.Contains(cmd, "/etc/cron.d/backup_job") {
			foundChmod = true
			break
		}
	}
	if !foundChmod {
		t.Fatalf("expected chmod 0644 on cron.d file, got executed: %v", conn.executed)
	}
}

// 4. 测试 sysctl 模块 sysctl_set: true 预检机制
func TestPhase34_Sysctl_PreSet(t *testing.T) {
	conn := &mockPhase34Conn{
		outputs: map[string]string{
			"sysctl -n": "0",
			"cat ":      "net.ipv4.ip_forward = 0",
		},
	}

	sysctlMod := &SysctlModule{}
	res, err := sysctlMod.Execute(conn, map[string]interface{}{
		"name":       "net.ipv4.ip_forward",
		"value":      "1",
		"sysctl_set": true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected changed=true")
	}

	// 验证预检 sysctl -w 执行时机早于写文件
	setIdx := -1
	writeIdx := -1
	for i, cmd := range conn.executed {
		if strings.Contains(cmd, "sysctl -w net.ipv4.ip_forward") {
			if setIdx == -1 {
				setIdx = i
			}
		}
		if strings.Contains(cmd, "> '/etc/sysctl.conf'") || strings.Contains(cmd, "GOANSIBLE_EOF") {
			if writeIdx == -1 {
				writeIdx = i
			}
		}
	}
	if setIdx == -1 || writeIdx == -1 || setIdx > writeIdx {
		t.Fatalf("expected sysctl -w (idx %d) before write file (idx %d), cmds: %v", setIdx, writeIdx, conn.executed)
	}
}

// 7. 测试 unarchive 模块 mode 八进制解析
func TestPhase34_Unarchive_ModeOctal(t *testing.T) {
	conn := &mockPhase34Conn{
		outputs: map[string]string{
			"test -e": "",
			"stat ":   "",
		},
	}

	unarchiveMod := &UnarchiveModule{}
	// 传入整数八进制 0755 (若未用 GetParamMode 则会变成十进制 "493")
	_, _ = unarchiveMod.Execute(conn, map[string]interface{}{
		"src":        "/tmp/app.tar.gz",
		"dest":       "/opt/app",
		"remote_src": true,
		"mode":       0755,
	})

	foundChmod := false
	for _, cmd := range conn.executed {
		if strings.Contains(cmd, "chmod -R 755") || strings.Contains(cmd, "chmod -R 0755") {
			foundChmod = true
			break
		}
	}
	if !foundChmod {
		t.Fatalf("expected chmod 755/0755 with proper octal representation, got: %v", conn.executed)
	}
}
