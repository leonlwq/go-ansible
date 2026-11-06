package module

import (
	"testing"

	"go-ansible/pkg/ssh"
)

func TestServiceFactsParsing(t *testing.T) {
	systemdOutput := `TYPE:systemd
---UNITS---
  UNIT                         LOAD   ACTIVE SUB     DESCRIPTION
  cron.service                 loaded active running Regular background program processing daemon
  dbus.service                 loaded active running D-Bus System Message Bus
  ssh.service                  loaded active running OpenBSD Secure Shell server
  ufw.service                  loaded active exited  CLI firewall
  nginx.service                loaded inactive dead  A high performance web server
LOAD   = Reflects whether the unit definition was properly loaded.
5 loaded units listed.
---FILES---
UNIT FILE                    STATE           VENDOR PRESET
cron.service                 enabled         enabled
dbus.service                 static          -
ssh.service                  enabled         enabled
nginx.service                disabled        enabled
`

	services := parseServiceFactsOutput(systemdOutput)
	if len(services) == 0 {
		t.Fatalf("expected parsed services, got none")
	}

	// 1. Verify cron.service
	cronVal, ok := services["cron.service"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected cron.service in services")
	}
	if cronVal["state"] != "running" {
		t.Errorf("expected cron state running, got %v", cronVal["state"])
	}
	if cronVal["status"] != "enabled" {
		t.Errorf("expected cron status enabled, got %v", cronVal["status"])
	}
	if cronVal["source"] != "systemd" {
		t.Errorf("expected cron source systemd, got %v", cronVal["source"])
	}

	// 2. Verify short alias "cron" exists
	cronShort, ok := services["cron"].(map[string]interface{})
	if !ok || cronShort["state"] != "running" {
		t.Fatalf("expected alias cron to exist and match cron.service")
	}

	// 3. Verify inactive service nginx
	nginxVal, ok := services["nginx.service"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected nginx.service in services")
	}
	if nginxVal["state"] != "stopped" {
		t.Errorf("expected nginx state stopped, got %v", nginxVal["state"])
	}
	if nginxVal["status"] != "disabled" {
		t.Errorf("expected nginx status disabled, got %v", nginxVal["status"])
	}

	// 4. Test SysV init output parsing
	sysvOutput := `TYPE:sysv
 [ + ]  cron
 [ - ]  rsync
 [ ? ]  hwclock.sh
`
	sysvServices := parseServiceFactsOutput(sysvOutput)
	cronSysv, ok := sysvServices["cron"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected cron in sysv services")
	}
	if cronSysv["state"] != "running" || cronSysv["source"] != "sysv" {
		t.Errorf("unexpected cron sysv parsed: %#v", cronSysv)
	}
	rsyncSysv, ok := sysvServices["rsync"].(map[string]interface{})
	if !ok || rsyncSysv["state"] != "stopped" {
		t.Errorf("unexpected rsync sysv parsed: %#v", rsyncSysv)
	}

	// 5. Test launchd output parsing
	launchdOutput := `TYPE:launchd
PID	Status	Label
1234	0	homebrew.mxcl.redis
-	0	com.apple.Spotlight
`
	launchdServices := parseServiceFactsOutput(launchdOutput)
	redisLaunchd, ok := launchdServices["homebrew.mxcl.redis"].(map[string]interface{})
	if !ok || redisLaunchd["state"] != "running" || redisLaunchd["source"] != "launchd" {
		t.Errorf("unexpected redis launchd parsed: %#v", redisLaunchd)
	}
	spotlight, ok := launchdServices["com.apple.Spotlight"].(map[string]interface{})
	if !ok || spotlight["state"] != "stopped" {
		t.Errorf("unexpected spotlight launchd parsed: %#v", spotlight)
	}
}

func TestServiceFactsModuleExecution(t *testing.T) {
	client := &ssh.LocalConnection{}
	mod := &ServiceFactsModule{}

	if err := mod.Validate(nil); err != nil {
		t.Fatalf("validate failed: %v", err)
	}

	res, err := mod.Execute(client, map[string]interface{}{})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if res.Changed {
		t.Errorf("service_facts should never change target state (changed=false)")
	}
	if res.Data == nil {
		t.Fatalf("expected non-nil Data in result")
	}
	af, ok := res.Data["ansible_facts"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected ansible_facts in result Data")
	}
	if _, ok := af["services"].(map[string]interface{}); !ok {
		t.Fatalf("expected services dictionary in ansible_facts")
	}
}
