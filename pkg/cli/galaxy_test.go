package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGalaxyRoleInit(t *testing.T) {
	tempDir := t.TempDir()

	opts := &GalaxyOptions{
		InitPath: tempDir,
		Force:    false,
	}

	roleName := "my_test_role"
	if err := runGalaxyRoleInit(roleName, opts); err != nil {
		t.Fatalf("runGalaxyRoleInit failed: %v", err)
	}

	roleDir := filepath.Join(tempDir, roleName)
	expectedFiles := []string{
		filepath.Join(roleDir, "README.md"),
		filepath.Join(roleDir, "defaults", "main.yml"),
		filepath.Join(roleDir, "handlers", "main.yml"),
		filepath.Join(roleDir, "meta", "main.yml"),
		filepath.Join(roleDir, "tasks", "main.yml"),
		filepath.Join(roleDir, "vars", "main.yml"),
		filepath.Join(roleDir, "tests", "inventory"),
		filepath.Join(roleDir, "tests", "test.yml"),
	}

	for _, f := range expectedFiles {
		if fi, err := os.Stat(f); err != nil || fi.Size() == 0 {
			t.Errorf("expected file %s to exist and be non-empty, err: %v", f, err)
		}
	}

	// Re-run without force should fail
	if err := runGalaxyRoleInit(roleName, opts); err == nil {
		t.Fatalf("expected error on re-init existing role without force")
	}

	// Re-run with force should succeed
	opts.Force = true
	if err := runGalaxyRoleInit(roleName, opts); err != nil {
		t.Fatalf("expected force re-init to succeed, got: %v", err)
	}
}

func TestGalaxyCmdExecution(t *testing.T) {
	tempDir := t.TempDir()

	cmd := NewGalaxyCmd("test-ver")
	cmd.SetArgs([]string{"role", "init", "web_cluster", "--init-path", tempDir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("cmd.Execute() failed: %v", err)
	}

	tasksPath := filepath.Join(tempDir, "web_cluster", "tasks", "main.yml")
	if _, err := os.Stat(tasksPath); err != nil {
		t.Fatalf("expected %s to exist", tasksPath)
	}
}
