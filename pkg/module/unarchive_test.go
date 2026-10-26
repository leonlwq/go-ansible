package module

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnarchiveValidate(t *testing.T) {
	mod := &UnarchiveModule{}

	if err := mod.Validate(map[string]interface{}{}); err == nil {
		t.Fatal("expected missing src/dest validation error")
	}
	if err := mod.Validate(map[string]interface{}{
		"src":  "/tmp/app.tar.gz",
		"dest": "/srv/app",
	}); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if err := mod.Validate(map[string]interface{}{
		"src":  "/tmp/app.rar",
		"dest": "/srv/app",
	}); err == nil {
		t.Fatal("expected unsupported archive format error")
	}
}

func TestArchiveExtractCommand(t *testing.T) {
	cmd, err := archiveExtractCommand("/tmp/app.tar.gz", "/srv/app")
	if err != nil {
		t.Fatalf("archiveExtractCommand() error = %v", err)
	}
	if !strings.Contains(cmd, "tar -xzf") {
		t.Fatalf("expected tar.gz extraction command, got %q", cmd)
	}

	cmd, err = archiveExtractCommand("/tmp/app.zip", "/srv/app")
	if err != nil {
		t.Fatalf("archiveExtractCommand(zip) error = %v", err)
	}
	if !strings.Contains(cmd, "unzip -o") {
		t.Fatalf("expected zip extraction command, got %q", cmd)
	}
}

func TestBuildUnarchiveCommandUsesCreatesGuard(t *testing.T) {
	cmd, err := buildUnarchiveCommand(unarchiveSpec{
		Src:     "/tmp/app.tar.gz",
		Dest:    "/srv/app",
		Creates: "/srv/app/bin/app",
	})
	if err != nil {
		t.Fatalf("buildUnarchiveCommand() error = %v", err)
	}
	if !strings.Contains(cmd, "[ -e '/srv/app/bin/app' ]") || !strings.Contains(cmd, changeMarkerFalse) {
		t.Fatalf("expected creates guard, got %q", cmd)
	}
}

func TestUnarchiveCommandLocalTarGz(t *testing.T) {
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("tar is required for unarchive local execution test")
	}

	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "source")
	archivePath := filepath.Join(dir, "app.tar.gz")
	destDir := filepath.Join(dir, "dest")
	createsPath := filepath.Join(destDir, "app.txt")

	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeFile(t, filepath.Join(sourceDir, "app.txt"), "hello\n")

	runCommand(t, dir, "tar", "-czf", archivePath, "-C", sourceDir, ".")

	cmd, err := buildUnarchiveCommand(unarchiveSpec{
		Src:     archivePath,
		Dest:    destDir,
		Creates: createsPath,
	})
	if err != nil {
		t.Fatalf("buildUnarchiveCommand() error = %v", err)
	}

	output := runShell(t, cmd, dir)
	if !strings.Contains(output, changeMarkerTrue) {
		t.Fatalf("expected first unarchive to change, got %q", output)
	}
	assertFileContent(t, createsPath, "hello\n")

	output = runShell(t, cmd, dir)
	if !strings.Contains(output, changeMarkerFalse) {
		t.Fatalf("expected creates guard to skip second unarchive, got %q", output)
	}
}
