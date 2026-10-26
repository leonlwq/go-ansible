package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPullFlagsParsing(t *testing.T) {
	cmd := NewPullCmd("0.1.0")

	args := []string{
		"-U", "https://github.com/example/repo.git",
		"-C", "develop",
		"-d", "/tmp/pull_dir",
		"-i", "inventory.ini",
		"-s", "10",
		"-f",
		"-o",
		"--purge",
		"--clean",
		"--full",
		"--check",
		"-D",
		"-vv",
		"setup.yml",
	}

	cmd.SetArgs(args)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags() failed: %v", err)
	}

	url, _ := cmd.Flags().GetString("url")
	if url != "https://github.com/example/repo.git" {
		t.Errorf("expected url https://github.com/example/repo.git, got %s", url)
	}

	checkout, _ := cmd.Flags().GetString("checkout")
	if checkout != "develop" {
		t.Errorf("expected checkout develop, got %s", checkout)
	}

	dir, _ := cmd.Flags().GetString("directory")
	if dir != "/tmp/pull_dir" {
		t.Errorf("expected directory /tmp/pull_dir, got %s", dir)
	}

	sleep, _ := cmd.Flags().GetInt("sleep")
	if sleep != 10 {
		t.Errorf("expected sleep 10, got %d", sleep)
	}

	force, _ := cmd.Flags().GetBool("force")
	if !force {
		t.Errorf("expected force true")
	}

	onlyChanged, _ := cmd.Flags().GetBool("only-if-changed")
	if !onlyChanged {
		t.Errorf("expected only-if-changed true")
	}

	purge, _ := cmd.Flags().GetBool("purge")
	if !purge {
		t.Errorf("expected purge true")
	}

	clean, _ := cmd.Flags().GetBool("clean")
	if !clean {
		t.Errorf("expected clean true")
	}

	full, _ := cmd.Flags().GetBool("full")
	if !full {
		t.Errorf("expected full true")
	}

	check, _ := cmd.Flags().GetBool("check")
	if !check {
		t.Errorf("expected check true")
	}

	diff, _ := cmd.Flags().GetBool("diff")
	if !diff {
		t.Errorf("expected diff true")
	}
}

func TestResolvePullPlaybook(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Explicit playbook
	p1 := filepath.Join(tempDir, "custom.yml")
	_ = os.WriteFile(p1, []byte("---\n- hosts: all\n"), 0o644)

	res, err := resolvePullPlaybook(tempDir, "custom.yml", "myhost")
	if err != nil || res != p1 {
		t.Fatalf("expected %s, got %s, err: %v", p1, res, err)
	}

	// 2. Auto-detect hostname.yml
	pHost := filepath.Join(tempDir, "myhost.yml")
	_ = os.WriteFile(pHost, []byte("---\n- hosts: all\n"), 0o644)
	res, err = resolvePullPlaybook(tempDir, "", "myhost")
	if err != nil || res != pHost {
		t.Fatalf("expected %s, got %s, err: %v", pHost, res, err)
	}
	_ = os.Remove(pHost)

	// 3. Auto-detect local.yml
	pLocal := filepath.Join(tempDir, "local.yml")
	_ = os.WriteFile(pLocal, []byte("---\n- hosts: all\n"), 0o644)
	res, err = resolvePullPlaybook(tempDir, "", "myhost")
	if err != nil || res != pLocal {
		t.Fatalf("expected %s, got %s, err: %v", pLocal, res, err)
	}
	_ = os.Remove(pLocal)

	// 4. Auto-detect site.yml
	pSite := filepath.Join(tempDir, "site.yml")
	_ = os.WriteFile(pSite, []byte("---\n- hosts: all\n"), 0o644)
	res, err = resolvePullPlaybook(tempDir, "", "myhost")
	if err != nil || res != pSite {
		t.Fatalf("expected %s, got %s, err: %v", pSite, res, err)
	}
}

func TestResolvePullInventory(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Fallback to localhost,
	inv := resolvePullInventory(tempDir, "")
	if inv != "localhost," {
		t.Fatalf("expected fallback to localhost,, got %s", inv)
	}

	// 2. Explicit inventory
	inv = resolvePullInventory(tempDir, "192.168.1.1,")
	if inv != "192.168.1.1," {
		t.Fatalf("expected explicit 192.168.1.1,, got %s", inv)
	}

	// 3. Auto-detect hosts file
	hostsFile := filepath.Join(tempDir, "hosts")
	_ = os.WriteFile(hostsFile, []byte("[all]\nlocalhost\n"), 0o644)
	inv = resolvePullInventory(tempDir, "")
	if inv != hostsFile {
		t.Fatalf("expected auto-detected hosts file %s, got %s", hostsFile, inv)
	}
}

func TestPullEndToEndWithLocalGitRepo(t *testing.T) {
	tempDir := t.TempDir()
	sourceRepo := filepath.Join(tempDir, "remote_git")
	destRepo := filepath.Join(tempDir, "pulled_git")

	// Create a local git repository as the remote
	_ = os.MkdirAll(sourceRepo, 0o755)
	if err := runGit(sourceRepo, "init"); err != nil {
		t.Skip("git init failed (git not installed?), skipping test")
	}
	_ = runGit(sourceRepo, "config", "user.name", "Tester")
	_ = runGit(sourceRepo, "config", "user.email", "tester@example.com")

	playbookContent := `---
- name: Test Pull
  hosts: localhost
  connection: local
  gather_facts: false
  tasks:
    - name: Echo test
      command: echo "hello from ansible-pull"
`
	_ = os.WriteFile(filepath.Join(sourceRepo, "local.yml"), []byte(playbookContent), 0o644)
	_ = runGit(sourceRepo, "add", "local.yml")
	_ = runGit(sourceRepo, "commit", "-m", "initial commit")

	opts := &PullOptions{
		URL:       sourceRepo,
		Directory: destRepo,
		Full:      true,
	}

	if err := runPull(opts); err != nil {
		t.Fatalf("runPull failed: %v", err)
	}

	// Check that repo was cloned and local.yml exists
	if _, err := os.Stat(filepath.Join(destRepo, "local.yml")); err != nil {
		t.Fatalf("expected cloned local.yml to exist in %s", destRepo)
	}
}
