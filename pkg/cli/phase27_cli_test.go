package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/config"
	"go-ansible/pkg/inventory"
)

// TestPhase27_AskBecomePassPrivilegeIsolation 验证 -K 仅注入提权密码而不越权激活全局 Become
func TestPhase27_AskBecomePassPrivilegeIsolation(t *testing.T) {
	origPrompt := promptPasswordFunc
	defer func() { promptPasswordFunc = origPrompt }()

	promptPasswordFunc = func(prompt string) (string, error) {
		return "secret_sudo_pass", nil
	}

	tempDir := t.TempDir()
	invFile := filepath.Join(tempDir, "hosts.ini")
	_ = os.WriteFile(invFile, []byte("[all]\nsrv1\n"), 0644)

	opts := &CommonCLIOptions{
		InventoryPaths: []string{invFile},
		AskBecomePass:  true,
		Become:         false,
	}

	env, err := opts.SetupExecutionEnv(&config.Config{})
	if err != nil {
		t.Fatalf("SetupExecutionEnv failed: %v", err)
	}

	h := env.Inventory.AllHosts["srv1"]
	if h == nil {
		t.Fatalf("srv1 host not found in inventory")
	}

	if h.Become {
		t.Fatalf("expected host.Become=false when only --ask-become-pass is supplied without -b")
	}
	if h.BecomePass != "secret_sudo_pass" {
		t.Fatalf("expected host.BecomePass='secret_sudo_pass', got %q", h.BecomePass)
	}

	// 显式指定 -b + -K 时应同时激活
	opts.Become = true
	env2, err2 := opts.SetupExecutionEnv(&config.Config{})
	if err2 != nil {
		t.Fatalf("SetupExecutionEnv with -b failed: %v", err2)
	}
	h2 := env2.Inventory.AllHosts["srv1"]
	if !h2.Become {
		t.Fatalf("expected host.Become=true when -b is explicitly supplied")
	}
}

// TestPhase27_InventoryGraphUngroupedHosts 验证 ansible-inventory --graph 准确呈现未分组主机
func TestPhase27_InventoryGraphUngroupedHosts(t *testing.T) {
	inv := inventory.NewInventory()
	h1 := &inventory.Host{Name: "standalone1"}
	inv.AllHosts["standalone1"] = h1
	inv.Groups["ungrouped"] = &inventory.Group{
		Name:  "ungrouped",
		Hosts: []*inventory.Host{h1},
	}

	var buf bytes.Buffer
	PrintInventoryGraphToForTest(&buf, inv, false)
	out := buf.String()
	if !strings.Contains(out, "@ungrouped:") || !strings.Contains(out, "standalone1") {
		t.Fatalf("expected graph output to contain @ungrouped: and standalone1, got: %s", out)
	}
}
