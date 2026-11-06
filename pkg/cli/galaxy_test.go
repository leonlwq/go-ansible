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

func TestGalaxyRoleInstallLocalDir(t *testing.T) {
	tempDir := t.TempDir()
	srcRolesDir := filepath.Join(tempDir, "src_roles")
	destRolesDir := filepath.Join(tempDir, "installed_roles")

	// 1. Initialize a role in srcRolesDir
	opts := &GalaxyOptions{
		InitPath: srcRolesDir,
	}
	if err := runGalaxyRoleInit("nginx_common", opts); err != nil {
		t.Fatalf("failed to init role: %v", err)
	}

	srcRolePath := filepath.Join(srcRolesDir, "nginx_common")

	// 2. Install from local directory
	installOpts := &GalaxyOptions{
		RolesPath: destRolesDir,
	}
	if err := runGalaxyRoleInstall([]string{srcRolePath}, installOpts); err != nil {
		t.Fatalf("runGalaxyRoleInstall failed: %v", err)
	}

	destTasksPath := filepath.Join(destRolesDir, "nginx_common", "tasks", "main.yml")
	if _, err := os.Stat(destTasksPath); err != nil {
		t.Fatalf("expected %s to exist after install", destTasksPath)
	}

	// 3. Re-install without force should skip cleanly
	if err := runGalaxyRoleInstall([]string{srcRolePath}, installOpts); err != nil {
		t.Fatalf("expected re-install without force to skip cleanly, got: %v", err)
	}

	// 4. Re-install with force
	installOpts.Force = true
	if err := runGalaxyRoleInstall([]string{srcRolePath}, installOpts); err != nil {
		t.Fatalf("expected re-install with force to succeed, got: %v", err)
	}
}

func TestGalaxyRequirementsFile(t *testing.T) {
	tempDir := t.TempDir()

	// List format
	reqListYAML := `
- src: /local/path/role_a
  name: custom_a
- src: /local/path/ansible-role-b
`
	reqListPath := filepath.Join(tempDir, "requirements_list.yml")
	if err := os.WriteFile(reqListPath, []byte(reqListYAML), 0o644); err != nil {
		t.Fatalf("failed to write requirements: %v", err)
	}

	reqs, err := parseRequirementsFile(reqListPath)
	if err != nil {
		t.Fatalf("parseRequirementsFile(list) failed: %v", err)
	}
	if len(reqs) != 2 {
		t.Fatalf("expected 2 reqs, got %d", len(reqs))
	}
	if reqs[0].Name != "custom_a" || reqs[0].Src != "/local/path/role_a" {
		t.Errorf("unexpected req[0]: %+v", reqs[0])
	}
	if reqs[1].Name != "b" {
		t.Errorf("expected role name 'b' from 'ansible-role-b', got: %s", reqs[1].Name)
	}

	// Dict format with roles: [...]
	reqDictYAML := `
roles:
  - src: https://github.com/geerlingguy/ansible-role-redis.git
    version: 1.8.0
  - simple.shorthand
`
	reqDictPath := filepath.Join(tempDir, "requirements_dict.yml")
	if err := os.WriteFile(reqDictPath, []byte(reqDictYAML), 0o644); err != nil {
		t.Fatalf("failed to write requirements dict: %v", err)
	}

	dictReqs, err := parseRequirementsFile(reqDictPath)
	if err != nil {
		t.Fatalf("parseRequirementsFile(dict) failed: %v", err)
	}
	if len(dictReqs) != 2 {
		t.Fatalf("expected 2 dict reqs, got %d", len(dictReqs))
	}
	if dictReqs[0].Name != "redis" || dictReqs[0].Version != "1.8.0" {
		t.Errorf("unexpected dictReqs[0]: %+v", dictReqs[0])
	}
	if dictReqs[1].Src != "simple.shorthand" || dictReqs[1].Name != "shorthand" {
		t.Errorf("unexpected dictReqs[1]: %+v", dictReqs[1])
	}
}

func TestGalaxyExtractRoleNameAndResolveGitURL(t *testing.T) {
	tests := []struct {
		src          string
		expectedName string
		expectedURL  string
	}{
		{
			src:          "https://github.com/geerlingguy/ansible-role-mysql.git",
			expectedName: "mysql",
			expectedURL:  "https://github.com/geerlingguy/ansible-role-mysql.git",
		},
		{
			src:          "geerlingguy.apache",
			expectedName: "apache",
			expectedURL:  "https://github.com/geerlingguy/ansible-role-apache.git",
		},
		{
			src:          "myorg/myrepo",
			expectedName: "myrepo",
			expectedURL:  "https://github.com/myorg/myrepo.git",
		},
		{
			src:          "git@github.com:example/ansible-role-k8s.git",
			expectedName: "k8s",
			expectedURL:  "git@github.com:example/ansible-role-k8s.git",
		},
	}

	for _, tc := range tests {
		name := extractRoleName(tc.src)
		if name != tc.expectedName {
			t.Errorf("extractRoleName(%q) = %q, expected %q", tc.src, name, tc.expectedName)
		}
		url := resolveGitURL(tc.src)
		if url != tc.expectedURL {
			t.Errorf("resolveGitURL(%q) = %q, expected %q", tc.src, url, tc.expectedURL)
		}
	}
}

func TestGalaxyInstallCLICommands(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "src")
	rolesDir := filepath.Join(tempDir, "roles")

	// Init source role
	initCmd := NewGalaxyCmd("test-ver")
	initCmd.SetArgs([]string{"role", "init", "worker_node", "--init-path", srcDir})
	if err := initCmd.Execute(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	srcRolePath := filepath.Join(srcDir, "worker_node")

	// 1. Test "ansible-galaxy role install <local_path> -p <rolesDir>"
	installRoleCmd := NewGalaxyCmd("test-ver")
	installRoleCmd.SetArgs([]string{"role", "install", srcRolePath, "-p", rolesDir})
	if err := installRoleCmd.Execute(); err != nil {
		t.Fatalf("role install failed: %v", err)
	}

	tasksPath := filepath.Join(rolesDir, "worker_node", "tasks", "main.yml")
	if _, err := os.Stat(tasksPath); err != nil {
		t.Fatalf("expected installed role tasks at %s", tasksPath)
	}

	// 2. Test "ansible-galaxy install <local_path> -p <rolesDir> --force"
	installShorthandCmd := NewGalaxyCmd("test-ver")
	installShorthandCmd.SetArgs([]string{"install", srcRolePath, "-p", rolesDir, "--force"})
	if err := installShorthandCmd.Execute(); err != nil {
		t.Fatalf("install shorthand failed: %v", err)
	}
}
