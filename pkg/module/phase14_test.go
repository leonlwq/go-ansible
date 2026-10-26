package module

import (
	"encoding/base64"
	"strings"
	"testing"

	"go-ansible/pkg/ssh"
)

type mockPhase14Conn struct {
	executedCmds []string
	files        map[string]string
	runtimeVars  map[string]string
	hostname     string
	mounts       string
}

func newMockPhase14Conn() *mockPhase14Conn {
	return &mockPhase14Conn{
		executedCmds: make([]string, 0),
		files:        make(map[string]string),
		runtimeVars:  make(map[string]string),
		hostname:     "original-host",
		mounts:       "",
	}
}

func (c *mockPhase14Conn) Connect() error { return nil }
func (c *mockPhase14Conn) Close() error   { return nil }

func (c *mockPhase14Conn) ExecuteWithBecome(cmd string) (*ssh.Result, error) {
	return c.Execute(cmd)
}

func (c *mockPhase14Conn) Upload(local, remote string) error   { return nil }
func (c *mockPhase14Conn) Download(remote, local string) error { return nil }

func (c *mockPhase14Conn) Execute(cmd string) (*ssh.Result, error) {
	c.executedCmds = append(c.executedCmds, cmd)

	// 处理读取文件 base64 命令 (buildReadFileBase64Command)
	if strings.Contains(cmd, "base64 <") {
		for path, content := range c.files {
			if strings.Contains(cmd, shellQuote(path)) {
				encoded := base64.StdEncoding.EncodeToString([]byte(content))
				return &ssh.Result{ExitCode: 0, Stdout: encoded}, nil
			}
		}
		return &ssh.Result{ExitCode: 0, Stdout: ""}, nil
	}

	// 处理写入文件 cat << 'EOF' | base64 -d > path
	if strings.Contains(cmd, "base64 -d") {
		for path := range c.files {
			if strings.Contains(cmd, shellQuote(path)) {
				lines := strings.Split(cmd, "\n")
				if len(lines) >= 3 {
					payload := lines[1]
					data, err := base64.StdEncoding.DecodeString(payload)
					if err == nil {
						c.files[path] = string(data)
						return &ssh.Result{ExitCode: 0}, nil
					}
				}
			}
		}
		// 若是新文件
		for _, token := range strings.Fields(cmd) {
			if strings.HasPrefix(token, "'/") && strings.HasSuffix(token, "'") {
				target := token[1 : len(token)-1]
				lines := strings.Split(cmd, "\n")
				if len(lines) >= 3 {
					payload := lines[1]
					data, err := base64.StdEncoding.DecodeString(payload)
					if err == nil {
						c.files[target] = string(data)
						return &ssh.Result{ExitCode: 0}, nil
					}
				}
			}
		}
		return &ssh.Result{ExitCode: 0}, nil
	}

	// 处理 sysctl -n
	if strings.HasPrefix(cmd, "sysctl -n ") {
		param := strings.TrimSpace(strings.TrimPrefix(cmd, "sysctl -n "))
		param = strings.TrimSuffix(param, " 2>/dev/null")
		param = strings.Trim(strings.TrimSpace(param), "'\"")
		val, ok := c.runtimeVars[param]
		if ok {
			return &ssh.Result{ExitCode: 0, Stdout: val + "\n"}, nil
		}
		return &ssh.Result{ExitCode: 1, Stderr: "unknown key\n"}, nil
	}

	// 处理 sysctl -w
	if strings.HasPrefix(cmd, "sysctl -w ") {
		sub := strings.TrimPrefix(cmd, "sysctl -w ")
		sub = strings.TrimSuffix(sub, " 2>/dev/null || true")
		parts := strings.SplitN(sub, "=", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.Trim(strings.TrimSpace(parts[1]), "'\"")
			c.runtimeVars[k] = v
		}
		return &ssh.Result{ExitCode: 0}, nil
	}

	// 处理 sysctl -p
	if strings.HasPrefix(cmd, "sysctl -p") {
		return &ssh.Result{ExitCode: 0, Stdout: "sysctl reloaded\n"}, nil
	}

	// 处理 hostname 查询
	if cmd == "hostname 2>/dev/null" {
		return &ssh.Result{ExitCode: 0, Stdout: c.hostname + "\n"}, nil
	}

	// 处理 hostnamectl set-hostname
	if strings.HasPrefix(cmd, "hostnamectl set-hostname ") {
		newH := strings.TrimPrefix(cmd, "hostnamectl set-hostname ")
		newH = strings.Trim(strings.TrimSpace(newH), "'\"")
		c.hostname = newH
		return &ssh.Result{ExitCode: 0}, nil
	}

	// 处理 hostname <name>
	if strings.HasPrefix(cmd, "hostname ") && !strings.Contains(cmd, "hostnamectl") {
		newH := strings.TrimPrefix(cmd, "hostname ")
		newH = strings.TrimSuffix(newH, " 2>/dev/null || true")
		newH = strings.Trim(strings.TrimSpace(newH), "'\"")
		c.hostname = newH
		return &ssh.Result{ExitCode: 0}, nil
	}

	// 处理 command -v hostnamectl
	if strings.Contains(cmd, "command -v hostnamectl") {
		return &ssh.Result{ExitCode: 0, Stdout: "/usr/bin/hostnamectl\n"}, nil
	}

	// 处理 cat /proc/mounts
	if strings.Contains(cmd, "/proc/mounts") {
		return &ssh.Result{ExitCode: 0, Stdout: c.mounts}, nil
	}

	// 处理 mount
	if strings.HasPrefix(cmd, "mount ") {
		return &ssh.Result{ExitCode: 0}, nil
	}

	// 处理 umount
	if strings.HasPrefix(cmd, "umount ") {
		return &ssh.Result{ExitCode: 0}, nil
	}

	// 默认返回成功
	return &ssh.Result{ExitCode: 0}, nil
}

func TestSysctlModule(t *testing.T) {
	mod := &SysctlModule{}

	// 1. Validate
	if err := mod.Validate(map[string]interface{}{}); err == nil {
		t.Fatal("expected error for missing name")
	}
	if err := mod.Validate(map[string]interface{}{"name": "net.ipv4.ip_forward", "state": "invalid"}); err == nil {
		t.Fatal("expected error for invalid state")
	}
	if err := mod.Validate(map[string]interface{}{"name": "net.ipv4.ip_forward", "state": "present"}); err == nil {
		t.Fatal("expected error for missing value when present")
	}

	// 2. Execute present - new key
	conn := newMockPhase14Conn()
	conn.files["/etc/sysctl.conf"] = "# System sysctl\nvm.swappiness = 10\n"
	conn.runtimeVars["net.ipv4.ip_forward"] = "0"

	res, err := mod.Execute(conn, map[string]interface{}{
		"name":        "net.ipv4.ip_forward",
		"value":       1,
		"state":       "present",
		"sysctl_file": "/etc/sysctl.conf",
		"reload":      true,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !res.Changed {
		t.Fatal("expected changed=true for new sysctl key")
	}
	if !strings.Contains(conn.files["/etc/sysctl.conf"], "net.ipv4.ip_forward = 1") {
		t.Fatalf("sysctl file not updated: %s", conn.files["/etc/sysctl.conf"])
	}

	// 3. Execute present - already set and matching
	res2, err := mod.Execute(conn, map[string]interface{}{
		"name":        "net.ipv4.ip_forward",
		"value":       "1",
		"state":       "present",
		"sysctl_file": "/etc/sysctl.conf",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res2.Changed {
		t.Fatal("expected changed=false when key and value already match")
	}

	// 4. Execute present - Check Mode
	resCheck, err := mod.Execute(conn, map[string]interface{}{
		"name":                "vm.swappiness",
		"value":               0,
		"state":               "present",
		"sysctl_file":         "/etc/sysctl.conf",
		"_ansible_check_mode": true,
		"_ansible_diff":       true,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !resCheck.Changed {
		t.Fatal("expected changed=true in check mode when value differs")
	}
	if resCheck.Diff == "" {
		t.Fatal("expected diff in check mode")
	}
	// Verify file was NOT modified in check mode
	if !strings.Contains(conn.files["/etc/sysctl.conf"], "vm.swappiness = 10") {
		t.Fatal("file should not be changed during check mode")
	}

	// 5. Execute absent
	resAbsent, err := mod.Execute(conn, map[string]interface{}{
		"name":        "net.ipv4.ip_forward",
		"state":       "absent",
		"sysctl_file": "/etc/sysctl.conf",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !resAbsent.Changed {
		t.Fatal("expected changed=true when removing key")
	}
	if strings.Contains(conn.files["/etc/sysctl.conf"], "net.ipv4.ip_forward") {
		t.Fatalf("key was not removed: %s", conn.files["/etc/sysctl.conf"])
	}
}

func TestHostnameModule(t *testing.T) {
	mod := &HostnameModule{}

	// 1. Validate
	if err := mod.Validate(map[string]interface{}{}); err == nil {
		t.Fatal("expected error for missing name")
	}

	// 2. Execute when hostname already matches
	conn := newMockPhase14Conn()
	conn.hostname = "k8s-node-1"

	res, err := mod.Execute(conn, map[string]interface{}{
		"name": "k8s-node-1",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.Changed {
		t.Fatal("expected changed=false when hostname is already set")
	}

	// 3. Execute Check Mode
	resCheck, err := mod.Execute(conn, map[string]interface{}{
		"name":                "k8s-master-01",
		"_ansible_check_mode": true,
		"_ansible_diff":       true,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !resCheck.Changed {
		t.Fatal("expected changed=true in check mode")
	}
	if conn.hostname != "k8s-node-1" {
		t.Fatal("hostname should not be changed in check mode")
	}

	// 4. Real update
	resReal, err := mod.Execute(conn, map[string]interface{}{
		"name": "k8s-master-01",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !resReal.Changed {
		t.Fatal("expected changed=true on real change")
	}
	if conn.hostname != "k8s-master-01" {
		t.Fatalf("hostname not updated, got %s", conn.hostname)
	}
	if conn.files["/etc/hostname"] != "k8s-master-01\n" {
		t.Fatalf("/etc/hostname not updated, got %q", conn.files["/etc/hostname"])
	}
}

func TestMountModule(t *testing.T) {
	mod := &MountModule{}

	// 1. Validate
	if err := mod.Validate(map[string]interface{}{}); err == nil {
		t.Fatal("expected error for missing path")
	}
	if err := mod.Validate(map[string]interface{}{"path": "/data", "state": "mounted"}); err == nil {
		t.Fatal("expected error for missing src and fstype")
	}

	// 2. Execute present
	conn := newMockPhase14Conn()
	conn.files["/etc/fstab"] = "# /etc/fstab\n/dev/sda1 / ext4 defaults 1 1\n"

	resPresent, err := mod.Execute(conn, map[string]interface{}{
		"path":   "/data",
		"src":    "/dev/sdb1",
		"fstype": "ext4",
		"opts":   "defaults,noatime",
		"state":  "present",
		"fstab":  "/etc/fstab",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !resPresent.Changed {
		t.Fatal("expected changed=true for new fstab entry")
	}
	if !strings.Contains(conn.files["/etc/fstab"], "/dev/sdb1") || !strings.Contains(conn.files["/etc/fstab"], "/data") {
		t.Fatalf("fstab not updated: %s", conn.files["/etc/fstab"])
	}

	// 3. Execute mounted when not mounted
	resMounted, err := mod.Execute(conn, map[string]interface{}{
		"path":   "/data",
		"src":    "/dev/sdb1",
		"fstype": "ext4",
		"opts":   "defaults,noatime",
		"state":  "mounted",
		"fstab":  "/etc/fstab",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !resMounted.Changed {
		t.Fatal("expected changed=true when mounting unmounted path")
	}

	// 4. Execute unmounted when mounted
	conn.mounts = "/dev/sdb1 /data ext4 rw,relatime 0 0\n"
	resUnmounted, err := mod.Execute(conn, map[string]interface{}{
		"path":  "/data",
		"state": "unmounted",
		"fstab": "/etc/fstab",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !resUnmounted.Changed {
		t.Fatal("expected changed=true when unmounting mounted path")
	}

	// 5. Execute absent (removes from fstab)
	resAbsent, err := mod.Execute(conn, map[string]interface{}{
		"path":  "/data",
		"state": "absent",
		"fstab": "/etc/fstab",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !resAbsent.Changed {
		t.Fatal("expected changed=true when removing from fstab")
	}
	if strings.Contains(conn.files["/etc/fstab"], "/data") {
		t.Fatalf("/data was not removed from fstab: %s", conn.files["/etc/fstab"])
	}
}
