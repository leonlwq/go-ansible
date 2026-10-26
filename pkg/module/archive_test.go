package module

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveValidate(t *testing.T) {
	mod := &ArchiveModule{}

	if err := mod.Validate(map[string]interface{}{}); err == nil {
		t.Fatal("expected missing path/dest validation error")
	}
	if err := mod.Validate(map[string]interface{}{
		"path":   "/tmp/app",
		"dest":   "/tmp/app.tar.gz",
		"format": "tar.gz",
	}); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if err := mod.Validate(map[string]interface{}{
		"path":   "/tmp/app",
		"dest":   "/tmp/app.rar",
		"format": "rar",
	}); err == nil {
		t.Fatal("expected unsupported archive format error")
	}
}

func TestCreateArchiveCommand(t *testing.T) {
	cmd, err := createArchiveCommand(archiveSpec{
		Paths:  []string{"/srv/app", "/srv/conf/app.yml"},
		Dest:   "/tmp/app.tar.gz",
		Format: "tar.gz",
	})
	if err != nil {
		t.Fatalf("createArchiveCommand() error = %v", err)
	}
	if !strings.Contains(cmd, "tar -cf -") || !strings.Contains(cmd, "gzip -n") {
		t.Fatalf("unexpected tar.gz archive command: %q", cmd)
	}

	cmd, err = createArchiveCommand(archiveSpec{
		Paths:  []string{"/srv/app"},
		Dest:   "/tmp/app.zip",
		Format: "zip",
	})
	if err != nil {
		t.Fatalf("createArchiveCommand(zip) error = %v", err)
	}
	if !strings.Contains(cmd, "zip -qr") {
		t.Fatalf("unexpected zip archive command: %q", cmd)
	}
}

func TestBuildArchiveCommandIncludesChecksumCompare(t *testing.T) {
	cmd, err := buildArchiveCommand(archiveSpec{
		Paths:  []string{"/srv/app"},
		Dest:   "/tmp/app.tar.gz",
		Format: "tar.gz",
		Backup: true,
		Remove: true,
		Mode:   "0644",
	})
	if err != nil {
		t.Fatalf("buildArchiveCommand() error = %v", err)
	}
	if !strings.Contains(cmd, "existing_checksum=") || !strings.Contains(cmd, changeMarkerFalse) {
		t.Fatalf("expected checksum-based idempotence, got %q", cmd)
	}
	if !strings.Contains(cmd, "cp -p '/tmp/app.tar.gz' '/tmp/app.tar.gz'.bak") {
		t.Fatalf("expected backup command, got %q", cmd)
	}
	if !strings.Contains(cmd, "rm -rf '/srv/app'") {
		t.Fatalf("expected remove command, got %q", cmd)
	}
}

func TestArchiveCommandLocalTarGz(t *testing.T) {
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("tar is required for archive local execution test")
	}
	if _, err := exec.LookPath("gzip"); err != nil {
		t.Skip("gzip is required for archive local execution test")
	}

	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "source")
	destPath := filepath.Join(dir, "bundle.tar.gz")
	extractDir := filepath.Join(dir, "extract")

	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeFile(t, filepath.Join(sourceDir, "app.txt"), "hello\n")

	cmd, err := buildArchiveCommand(archiveSpec{
		Paths:  []string{sourceDir},
		Dest:   destPath,
		Format: "tar.gz",
	})
	if err != nil {
		t.Fatalf("buildArchiveCommand() error = %v", err)
	}

	output := runShell(t, cmd, dir)
	if !strings.Contains(output, changeMarkerTrue) {
		t.Fatalf("expected first archive to change, got %q", output)
	}
	if _, err := os.Stat(destPath); err != nil {
		t.Fatalf("expected archive file to exist: %v", err)
	}

	output = runShell(t, cmd, dir)
	if !strings.Contains(output, changeMarkerFalse) {
		t.Fatalf("expected second archive to be idempotent, got %q", output)
	}

	writeFile(t, filepath.Join(sourceDir, "app.txt"), "world\n")
	output = runShell(t, cmd, dir)
	if !strings.Contains(output, changeMarkerTrue) {
		t.Fatalf("expected source change to rebuild archive, got %q", output)
	}

	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(extractDir) error = %v", err)
	}
	runCommand(t, dir, "tar", "-xzf", destPath, "-C", extractDir)
	assertFileContent(t, filepath.Join(extractDir, strings.TrimPrefix(sourceDir, "/"), "app.txt"), "world\n")
}

func TestArchiveCommandRemoveSources(t *testing.T) {
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("tar is required for archive remove test")
	}
	if _, err := exec.LookPath("gzip"); err != nil {
		t.Skip("gzip is required for archive remove test")
	}

	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "source")
	destPath := filepath.Join(dir, "bundle.tar.gz")

	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeFile(t, filepath.Join(sourceDir, "app.txt"), "hello\n")

	cmd, err := buildArchiveCommand(archiveSpec{
		Paths:  []string{sourceDir},
		Dest:   destPath,
		Format: "tar.gz",
		Remove: true,
	})
	if err != nil {
		t.Fatalf("buildArchiveCommand() error = %v", err)
	}

	output := runShell(t, cmd, dir)
	if !strings.Contains(output, changeMarkerTrue) {
		t.Fatalf("expected archive command to change, got %q", output)
	}
	if _, err := os.Stat(sourceDir); !os.IsNotExist(err) {
		t.Fatalf("expected sourceDir to be removed, stat err=%v", err)
	}
}

func TestRegistryIncludesArchiveModule(t *testing.T) {
	registry := NewRegistry()
	if _, err := registry.Get("archive"); err != nil {
		t.Fatalf("expected archive module to be registered: %v", err)
	}
}
