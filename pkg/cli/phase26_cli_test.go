package cli

import (
	"path/filepath"
	"testing"

	"go-ansible/pkg/module"
)

// TestPhase26_CLIDocAndGalaxy 测试 ansible-doc 与 ansible-galaxy CLI 新增能力
func TestPhase26_CLIDocAndGalaxy(t *testing.T) {
	// 1. ansible-doc package, blockinfile, dnf 文档注册校验
	docPackage, ok := module.GetModuleDoc("package")
	if !ok || docPackage == nil {
		t.Fatalf("failed to find module doc for package")
	}
	docBlock, ok := module.GetModuleDoc("blockinfile")
	if !ok || docBlock == nil {
		t.Fatalf("failed to find module doc for blockinfile")
	}
	docDnf, ok := module.GetModuleDoc("dnf")
	if !ok || docDnf == nil {
		t.Fatalf("failed to find module doc for dnf")
	}

	// 2. ansible-galaxy role init, list, info
	tmpDir := t.TempDir()
	rolesDir := filepath.Join(tmpDir, "roles")

	galaxyCmd := NewGalaxyCmd("v0.1.0-test")
	galaxyCmd.SetArgs([]string{"role", "init", "test_role", "--init-path", rolesDir})
	if err := galaxyCmd.Execute(); err != nil {
		t.Fatalf("ansible-galaxy role init failed: %v", err)
	}

	// 验证 role list
	galaxyListCmd := NewGalaxyCmd("v0.1.0-test")
	galaxyListCmd.SetArgs([]string{"role", "list", "--roles-path", rolesDir})
	if err := galaxyListCmd.Execute(); err != nil {
		t.Fatalf("ansible-galaxy role list failed: %v", err)
	}

	// 验证 role info
	galaxyInfoCmd := NewGalaxyCmd("v0.1.0-test")
	galaxyInfoCmd.SetArgs([]string{"role", "info", "test_role", "--roles-path", rolesDir})
	if err := galaxyInfoCmd.Execute(); err != nil {
		t.Fatalf("ansible-galaxy role info failed: %v", err)
	}
}
