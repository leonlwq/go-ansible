package module

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/ssh"
)

func TestBlockinfileModule(t *testing.T) {
	tempDir := t.TempDir()
	targetFile := filepath.Join(tempDir, "hosts.conf")

	initialContent := "127.0.0.1 localhost\n::1 localhost\n"
	if err := os.WriteFile(targetFile, []byte(initialContent), 0644); err != nil {
		t.Fatalf("failed to create initial file: %v", err)
	}

	conn := ssh.NewLocalConnection(&inventory.Host{Name: "localhost"})
	mod := &BlockinfileModule{}

	// 1. Insert new block
	res, err := mod.Execute(conn, map[string]interface{}{
		"path":  targetFile,
		"block": "192.168.1.10 app1.example.com\n192.168.1.11 app2.example.com",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res.Changed {
		t.Errorf("expected changed=true on first insert")
	}

	data, _ := os.ReadFile(targetFile)
	content := string(data)
	if !strings.Contains(content, "# BEGIN ANSIBLE MANAGED BLOCK") || !strings.Contains(content, "app1.example.com") {
		t.Errorf("expected block content in file, got:\n%s", content)
	}

	// 2. Idempotency test (no change)
	res2, err := mod.Execute(conn, map[string]interface{}{
		"path":  targetFile,
		"block": "192.168.1.10 app1.example.com\n192.168.1.11 app2.example.com",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if res2.Changed {
		t.Errorf("expected changed=false on identical re-run, but got changed=true")
	}

	// 3. Update block content
	res3, err := mod.Execute(conn, map[string]interface{}{
		"path":  targetFile,
		"block": "192.168.1.99 app-new.example.com",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res3.Changed {
		t.Errorf("expected changed=true on block update")
	}
	data3, _ := os.ReadFile(targetFile)
	if !strings.Contains(string(data3), "app-new.example.com") || strings.Contains(string(data3), "app1.example.com") {
		t.Errorf("expected updated content in file, got:\n%s", string(data3))
	}

	// 4. Remove block (state: absent)
	res4, err := mod.Execute(conn, map[string]interface{}{
		"path":  targetFile,
		"state": "absent",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res4.Changed {
		t.Errorf("expected changed=true on state: absent")
	}
	data4, _ := os.ReadFile(targetFile)
	if strings.Contains(string(data4), "ANSIBLE MANAGED BLOCK") {
		t.Errorf("expected block to be removed, got:\n%s", string(data4))
	}
}

func TestBlockinfileInsertBeforeBOF(t *testing.T) {
	tempDir := t.TempDir()
	targetFile := filepath.Join(tempDir, "config.ini")
	_ = os.WriteFile(targetFile, []byte("existing_line=1\n"), 0644)

	conn := ssh.NewLocalConnection(&inventory.Host{Name: "localhost"})
	mod := &BlockinfileModule{}

	res, err := mod.Execute(conn, map[string]interface{}{
		"path":         targetFile,
		"block":        "first_line=0",
		"insertbefore": "BOF",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res.Changed {
		t.Errorf("expected changed=true")
	}

	data, _ := os.ReadFile(targetFile)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 3 || lines[0] != "# BEGIN ANSIBLE MANAGED BLOCK" {
		t.Errorf("expected block at BOF, got:\n%s", string(data))
	}
}

func TestBlockinfileCheckModeAndDiff(t *testing.T) {
	tempDir := t.TempDir()
	targetFile := filepath.Join(tempDir, "test.txt")
	_ = os.WriteFile(targetFile, []byte("line1\n"), 0644)

	conn := ssh.NewLocalConnection(&inventory.Host{Name: "localhost"})
	mod := &BlockinfileModule{}

	res, err := mod.Execute(conn, map[string]interface{}{
		"path":        targetFile,
		"block":       "dry_run_block",
		"check_mode":  true,
		"diff":        true,
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res.Changed {
		t.Errorf("expected changed=true in check mode")
	}
	if res.Diff == "" {
		t.Errorf("expected non-empty diff")
	}

	// File should NOT have been modified
	data, _ := os.ReadFile(targetFile)
	if strings.Contains(string(data), "dry_run_block") {
		t.Errorf("file was modified in check mode!")
	}
}

func TestLineinfileValidate(t *testing.T) {
	tempDir := t.TempDir()
	targetFile := filepath.Join(tempDir, "sudoers.test")
	_ = os.WriteFile(targetFile, []byte("root ALL=(ALL) ALL\n"), 0644)

	conn := ssh.NewLocalConnection(&inventory.Host{Name: "localhost"})
	mod := &LineinfileModule{}

	// 1. Validation failure: command exits non-zero (false %s)
	resFail, err := mod.Execute(conn, map[string]interface{}{
		"path":     targetFile,
		"line":     "bad syntax line",
		"validate": "false %s",
	})
	if err == nil && (resFail == nil || !resFail.Failed) {
		t.Errorf("expected validate failure, got success")
	}
	// Verify target file was not modified
	data, _ := os.ReadFile(targetFile)
	if strings.Contains(string(data), "bad syntax line") {
		t.Errorf("target file was modified despite validation failure!")
	}

	// 2. Validation success: command exits 0 (sh -c 'test -f %s')
	resPass, err := mod.Execute(conn, map[string]interface{}{
		"path":     targetFile,
		"line":     "admin ALL=(ALL) NOPASSWD: ALL",
		"validate": "sh -c 'test -f %s'",
	})
	if err != nil || (resPass != nil && resPass.Failed) {
		t.Fatalf("expected validate pass, got err: %v", err)
	}
	dataPass, _ := os.ReadFile(targetFile)
	if !strings.Contains(string(dataPass), "admin ALL=(ALL)") {
		t.Errorf("expected line to be written after validation pass, got:\n%s", string(dataPass))
	}
}

func TestDnfModuleRegistered(t *testing.T) {
	reg := NewRegistry()
	mod, err := reg.Get("dnf")
	if err != nil {
		t.Fatalf("dnf module not registered: %v", err)
	}
	if mod.Name() != "dnf" {
		t.Errorf("expected module name 'dnf', got %q", mod.Name())
	}
	if !SupportsCheckMode("dnf") {
		t.Errorf("expected dnf to support check mode")
	}
}
