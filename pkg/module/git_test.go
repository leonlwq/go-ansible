package module

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildGitSSHEnv(t *testing.T) {
	env := buildGitSSHEnv(gitSpec{
		KeyFile:       "/tmp/id_rsa",
		AcceptHostKey: true,
	})
	if !strings.Contains(env, "GIT_SSH_COMMAND") {
		t.Fatalf("expected GIT_SSH_COMMAND export, got %q", env)
	}
	if !strings.Contains(env, "StrictHostKeyChecking=no") || !strings.Contains(env, "UserKnownHostsFile=/dev/null") {
		t.Fatalf("expected relaxed host key options, got %q", env)
	}
	if !strings.Contains(env, "/tmp/id_rsa") {
		t.Fatalf("expected key file in env, got %q", env)
	}
}

func TestBuildGitCommandAbsent(t *testing.T) {
	cmd, err := buildGitCommand(gitSpec{
		Dest:  "/srv/app",
		State: "absent",
	})
	if err != nil {
		t.Fatalf("buildGitCommand() error = %v", err)
	}
	if !strings.Contains(cmd, "rm -rf") || !strings.Contains(cmd, changeMarkerTrue) {
		t.Fatalf("unexpected git absent command: %q", cmd)
	}
}

func TestGitModuleLocalCloneAndUpdate(t *testing.T) {
	baseDir := t.TempDir()
	originPath, workPath := initGitOrigin(t, baseDir)

	destPath := filepath.Join(baseDir, "clone")
	cmd, err := buildGitCommand(gitSpec{
		Repo:    originPath,
		Dest:    destPath,
		Version: "main",
		State:   "present",
		Clone:   true,
		Update:  true,
	})
	if err != nil {
		t.Fatalf("buildGitCommand() error = %v", err)
	}

	output := runShell(t, cmd, baseDir)
	if !strings.Contains(output, changeMarkerTrue) {
		t.Fatalf("expected initial clone to change, got %q", output)
	}
	if _, err := os.Stat(filepath.Join(destPath, ".git")); err != nil {
		t.Fatalf("expected cloned repo, stat error = %v", err)
	}

	output = runShell(t, cmd, baseDir)
	if !strings.Contains(output, changeMarkerFalse) {
		t.Fatalf("expected second run to be idempotent, got %q", output)
	}

	pushCommit(t, workPath, "update.txt", "second\n")
	output = runShell(t, cmd, baseDir)
	if !strings.Contains(output, changeMarkerTrue) {
		t.Fatalf("expected update to detect change, got %q", output)
	}

	head := strings.TrimSpace(runCommand(t, baseDir, "git", "-C", destPath, "rev-parse", "HEAD"))
	originHead := strings.TrimSpace(runCommand(t, baseDir, "git", "-C", workPath, "rev-parse", "HEAD"))
	if head != originHead {
		t.Fatalf("expected clone head %q to match origin head %q", head, originHead)
	}
}

func TestGitModuleCloneFalseNoop(t *testing.T) {
	baseDir := t.TempDir()
	destPath := filepath.Join(baseDir, "missing")

	cmd, err := buildGitCommand(gitSpec{
		Repo:   filepath.Join(baseDir, "origin.git"),
		Dest:   destPath,
		State:  "present",
		Clone:  false,
		Update: true,
	})
	if err != nil {
		t.Fatalf("buildGitCommand() error = %v", err)
	}

	output := runShell(t, cmd, baseDir)
	if !strings.Contains(output, changeMarkerFalse) {
		t.Fatalf("expected clone=false to noop, got %q", output)
	}
}

func TestRegistryIncludesGitModule(t *testing.T) {
	registry := NewRegistry()
	if _, err := registry.Get("git"); err != nil {
		t.Fatalf("expected git module to be registered: %v", err)
	}
}

func initGitOrigin(t *testing.T, baseDir string) (string, string) {
	t.Helper()

	seedPath := filepath.Join(baseDir, "seed")
	originPath := filepath.Join(baseDir, "origin.git")

	runCommand(t, baseDir, "git", "init", "--bare", "--initial-branch=main", originPath)
	runCommand(t, baseDir, "git", "init", "--initial-branch=main", seedPath)
	runCommand(t, seedPath, "git", "config", "user.email", "test@example.com")
	runCommand(t, seedPath, "git", "config", "user.name", "go-ansible")
	if err := os.WriteFile(filepath.Join(seedPath, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runCommand(t, seedPath, "git", "add", "README.md")
	runCommand(t, seedPath, "git", "commit", "-m", "initial")
	runCommand(t, seedPath, "git", "remote", "add", "origin", originPath)
	runCommand(t, seedPath, "git", "push", "-u", "origin", "main")

	return originPath, seedPath
}

func pushCommit(t *testing.T, repoPath, fileName, content string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(repoPath, fileName), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runCommand(t, repoPath, "git", "add", fileName)
	runCommand(t, repoPath, "git", "commit", "-m", "update "+fileName)
	runCommand(t, repoPath, "git", "push", "origin", "main")
}

func runShell(t *testing.T, script, dir string) string {
	t.Helper()

	cmd := exec.Command("/bin/bash", "-lc", script)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shell command failed: %v\nscript:\n%s\noutput:\n%s", err, script, output)
	}
	return string(output)
}

func runCommand(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %s %s\nerror: %v\noutput:\n%s", name, strings.Join(args, " "), err, output)
	}
	return string(output)
}
