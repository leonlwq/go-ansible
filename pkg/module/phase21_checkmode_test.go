package module

import (
	"strings"
	"testing"

	"go-ansible/pkg/ssh"
)

type mockCheckModeConn struct {
	executedCmds []string
	customOutput map[string]*ssh.Result
}

func newMockCheckModeConn() *mockCheckModeConn {
	return &mockCheckModeConn{
		customOutput: make(map[string]*ssh.Result),
	}
}

func (c *mockCheckModeConn) Connect() error { return nil }
func (c *mockCheckModeConn) Close() error   { return nil }
func (c *mockCheckModeConn) Execute(cmd string) (*ssh.Result, error) {
	c.executedCmds = append(c.executedCmds, cmd)
	for pattern, res := range c.customOutput {
		if strings.Contains(cmd, pattern) {
			return res, nil
		}
	}
	return &ssh.Result{ExitCode: 0}, nil
}
func (c *mockCheckModeConn) ExecuteWithBecome(cmd string) (*ssh.Result, error) {
	return c.Execute(cmd)
}
func (c *mockCheckModeConn) Upload(src, dest string) error   { return nil }
func (c *mockCheckModeConn) Download(src, dest string) error { return nil }

func TestUserAndGroupCheckMode(t *testing.T) {
	// 1. UserModule in Check Mode
	conn := newMockCheckModeConn()
	conn.customOutput["id 'devops'"] = &ssh.Result{ExitCode: 1} // User does not exist

	userMod := &UserModule{}
	res, err := userMod.Execute(conn, map[string]interface{}{
		"name":         "devops",
		"state":        "present",
		"__check_mode": true,
	})
	if err != nil {
		t.Fatalf("UserModule.Execute error: %v", err)
	}
	if !res.Changed {
		t.Errorf("expected user module to report changed=true in check mode")
	}
	for _, cmd := range conn.executedCmds {
		if strings.Contains(cmd, "useradd") || strings.Contains(cmd, "usermod") {
			t.Fatalf("check mode must not execute useradd/usermod, got command: %s", cmd)
		}
	}

	// 2. GroupModule in Check Mode
	connGroup := newMockCheckModeConn()
	connGroup.customOutput["getent group 'devops'"] = &ssh.Result{ExitCode: 1} // Group does not exist

	groupMod := &GroupModule{}
	resGroup, err := groupMod.Execute(connGroup, map[string]interface{}{
		"name":         "devops",
		"state":        "present",
		"__check_mode": true,
	})
	if err != nil {
		t.Fatalf("GroupModule.Execute error: %v", err)
	}
	if !resGroup.Changed {
		t.Errorf("expected group module to report changed=true in check mode")
	}
	for _, cmd := range connGroup.executedCmds {
		if strings.Contains(cmd, "groupadd") || strings.Contains(cmd, "groupmod") {
			t.Fatalf("check mode must not execute groupadd/groupmod, got command: %s", cmd)
		}
	}
}

func TestYumAndAptCheckMode(t *testing.T) {
	// 1. YumModule Check Mode
	connYum := newMockCheckModeConn()
	connYum.customOutput["rpm -q 'nginx'"] = &ssh.Result{ExitCode: 1} // Not installed

	yumMod := &YumModule{}
	resYum, err := yumMod.Execute(connYum, map[string]interface{}{
		"name":         "nginx",
		"state":        "present",
		"__check_mode": true,
	})
	if err != nil {
		t.Fatalf("YumModule.Execute error: %v", err)
	}
	if !resYum.Changed {
		t.Errorf("expected yum to report changed=true in check mode when package missing")
	}
	for _, cmd := range connYum.executedCmds {
		if strings.Contains(cmd, "yum install") || strings.Contains(cmd, "yum makecache") {
			t.Fatalf("check mode must not execute yum install/makecache, got: %s", cmd)
		}
	}

	// 2. AptModule Check Mode
	connApt := newMockCheckModeConn()
	connApt.customOutput["dpkg -s 'nginx'"] = &ssh.Result{ExitCode: 1} // Not installed

	aptMod := &AptModule{}
	resApt, err := aptMod.Execute(connApt, map[string]interface{}{
		"name":         "nginx",
		"state":        "present",
		"__check_mode": true,
	})
	if err != nil {
		t.Fatalf("AptModule.Execute error: %v", err)
	}
	if !resApt.Changed {
		t.Errorf("expected apt to report changed=true in check mode when package missing")
	}
	for _, cmd := range connApt.executedCmds {
		if strings.Contains(cmd, "apt-get install") || strings.Contains(cmd, "apt-get update") {
			t.Fatalf("check mode must not execute apt-get install/update, got: %s", cmd)
		}
	}
}

func TestServiceAndSystemdCheckMode(t *testing.T) {
	// 1. ServiceModule Check Mode
	connSvc := newMockCheckModeConn()
	connSvc.customOutput["systemctl is-active"] = &ssh.Result{ExitCode: 1, Stdout: "inactive\n"}

	svcMod := &ServiceModule{}
	resSvc, err := svcMod.Execute(connSvc, map[string]interface{}{
		"name":         "redis",
		"state":        "started",
		"__check_mode": true,
	})
	if err != nil {
		t.Fatalf("ServiceModule.Execute error: %v", err)
	}
	if !resSvc.Changed {
		t.Errorf("expected service module to report changed=true when inactive service started in check mode")
	}
	for _, cmd := range connSvc.executedCmds {
		if strings.Contains(cmd, "systemctl start") || strings.Contains(cmd, "service redis start") {
			t.Fatalf("check mode must not execute service start, got: %s", cmd)
		}
	}

	// 2. SystemdModule Check Mode: already started
	connSys := newMockCheckModeConn()
	connSys.customOutput["which systemctl"] = &ssh.Result{ExitCode: 0, Stdout: "/bin/systemctl\n"}
	connSys.customOutput["systemctl is-active 'redis'"] = &ssh.Result{ExitCode: 0, Stdout: "active\n"}

	sysMod := &SystemdModule{}
	resSys, err := sysMod.Execute(connSys, map[string]interface{}{
		"name":         "redis",
		"state":        "started",
		"__check_mode": true,
	})
	if err != nil {
		t.Fatalf("SystemdModule.Execute error: %v", err)
	}
	if resSys.Changed {
		t.Errorf("expected systemd module to report changed=false when service already active")
	}
}

func TestGetURLCheckMode(t *testing.T) {
	conn := newMockCheckModeConn()
	conn.customOutput["missing"] = &ssh.Result{ExitCode: 0, Stdout: "missing\n"}

	mod := &GetURLModule{}
	res, err := mod.Execute(conn, map[string]interface{}{
		"url":          "https://example.com/app.tar.gz",
		"dest":         "/opt/app.tar.gz",
		"__check_mode": true,
	})
	if err != nil {
		t.Fatalf("GetURLModule.Execute error: %v", err)
	}
	if !res.Changed {
		t.Errorf("expected get_url to report changed=true when dest missing")
	}
	for _, cmd := range conn.executedCmds {
		if strings.Contains(cmd, "curl") || strings.Contains(cmd, "wget") || strings.Contains(cmd, "mktemp") {
			t.Fatalf("check mode must not download files, got command: %s", cmd)
		}
	}
}

func TestCronModuleIdempotencyAndCheckMode(t *testing.T) {
	// 1. Check Mode
	conn := newMockCheckModeConn()
	conn.customOutput["crontab -l"] = &ssh.Result{ExitCode: 0, Stdout: "" /* empty crontab */}

	cronMod := &CronModule{}
	res, err := cronMod.Execute(conn, map[string]interface{}{
		"name":         "daily_backup",
		"job":          "/usr/local/bin/backup.sh",
		"minute":       "0",
		"hour":         "3",
		"__check_mode": true,
	})
	if err != nil {
		t.Fatalf("CronModule.Execute error: %v", err)
	}
	if !res.Changed {
		t.Errorf("expected cron module to report changed=true on new entry in check mode")
	}
	for _, cmd := range conn.executedCmds {
		if strings.Contains(cmd, "base64") || strings.Contains(cmd, "| crontab") {
			t.Fatalf("check mode must not write to crontab, got: %s", cmd)
		}
	}

	// 2. Pure Go updateCrontabContent idempotency
	existing := ""
	newContent, changed := updateCrontabContent(existing, "backup", "/bin/backup", "0 2 * * * /bin/backup", "present", false)
	if !changed {
		t.Fatalf("expected changed=true on initial insertion")
	}
	if !strings.Contains(newContent, "# Ansible: backup") || !strings.Contains(newContent, "0 2 * * * /bin/backup") {
		t.Fatalf("unexpected newContent: %q", newContent)
	}

	// Re-run with same content -> should be idempotent (changed == false)
	secondContent, secondChanged := updateCrontabContent(newContent, "backup", "/bin/backup", "0 2 * * * /bin/backup", "present", false)
	if secondChanged {
		t.Fatalf("expected second run to be idempotent (changed=false), got changed=true, content: %q", secondContent)
	}

	// Test disabled=true
	disabledContent, disabledChanged := updateCrontabContent(newContent, "backup", "/bin/backup", "0 2 * * * /bin/backup", "present", true)
	if !disabledChanged {
		t.Fatalf("expected changed=true when disabling entry")
	}
	if !strings.Contains(disabledContent, "#0 2 * * * /bin/backup") {
		t.Fatalf("expected disabled entry to start with #, got: %q", disabledContent)
	}

	// Test state=absent
	absentContent, absentChanged := updateCrontabContent(newContent, "backup", "/bin/backup", "0 2 * * * /bin/backup", "absent", false)
	if !absentChanged {
		t.Fatalf("expected changed=true when removing entry")
	}
	if strings.Contains(absentContent, "backup") {
		t.Fatalf("expected backup entry to be removed, got: %q", absentContent)
	}
}
