package ssh

import (
	"strings"
	"testing"

	"go-ansible/pkg/inventory"
)

func TestBuildBecomeCommand(t *testing.T) {
	// 1. Default sudo without password
	h1 := &inventory.Host{Name: "h1"}
	cmd1 := BuildBecomeCommand("whoami", h1)
	if !strings.Contains(cmd1, "sudo -n -u root /bin/sh -c") || !strings.Contains(cmd1, "'whoami'") {
		t.Errorf("unexpected cmd1: %s", cmd1)
	}

	// 2. Sudo with password and custom user
	h2 := &inventory.Host{
		Name:       "h2",
		BecomeUser: "deploy",
		BecomePass: "secret123",
	}
	cmd2 := BuildBecomeCommand("ls -la /var/www", h2)
	if !strings.Contains(cmd2, "printf '%s\\n' 'secret123' | sudo -S -p '' -u deploy /bin/sh -c") {
		t.Errorf("unexpected cmd2: %s", cmd2)
	}

	// 3. Su with password
	h3 := &inventory.Host{
		Name:         "h3",
		BecomeMethod: "su",
		BecomeUser:   "postgres",
		BecomePass:   "pgpass",
	}
	cmd3 := BuildBecomeCommand("psql", h3)
	if !strings.Contains(cmd3, "printf '%s\\n' 'pgpass' | su - postgres -c") {
		t.Errorf("unexpected cmd3: %s", cmd3)
	}

	// 4. Custom become_exe / custom become_method
	h4 := &inventory.Host{
		Name:         "h4",
		BecomeMethod: "doas",
		BecomeExe:    "doas",
	}
	cmd4 := BuildBecomeCommand("reboot", h4)
	if !strings.Contains(cmd4, "doas /bin/sh -c 'reboot'") {
		t.Errorf("unexpected cmd4: %s", cmd4)
	}

	// 5. BecomeFlags
	h5 := &inventory.Host{
		Name:        "h5",
		BecomeFlags: "-H -S",
	}
	cmd5 := BuildBecomeCommand("whoami", h5)
	if !strings.Contains(cmd5, "sudo -n -H -S -u root /bin/sh -c") {
		t.Errorf("unexpected cmd5: %s", cmd5)
	}
}
