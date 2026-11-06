package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPhase33_GalaxyRoleRemove 测试 ansible-galaxy role remove 与 direct remove
func TestPhase33_GalaxyRoleRemove(t *testing.T) {
	tmpRolesDir, err := os.MkdirTemp("", "galaxy-roles-*")
	if err != nil {
		t.Fatalf("failed to create temp roles dir: %v", err)
	}
	defer os.RemoveAll(tmpRolesDir)

	// 创建模拟 role
	roleDir := filepath.Join(tmpRolesDir, "test_role")
	if err := os.MkdirAll(roleDir, 0755); err != nil {
		t.Fatalf("failed to create test role dir: %v", err)
	}

	opts := &GalaxyOptions{
		RolesPath: tmpRolesDir,
	}

	// 1. 删除存在的 role
	if err := runGalaxyRoleRemove([]string{"test_role"}, opts); err != nil {
		t.Fatalf("runGalaxyRoleRemove failed: %v", err)
	}

	if _, err := os.Stat(roleDir); !os.IsNotExist(err) {
		t.Fatalf("expected role directory to be removed, but still exists")
	}

	// 2. 删除不存在的 role 幂等跳过不报错
	if err := runGalaxyRoleRemove([]string{"non_existent_role"}, opts); err != nil {
		t.Fatalf("runGalaxyRoleRemove on non-existent role should not return error, got: %v", err)
	}
}

