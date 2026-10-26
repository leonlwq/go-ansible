package module

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetURLValidate(t *testing.T) {
	mod := &GetURLModule{}

	if err := mod.Validate(map[string]interface{}{}); err == nil {
		t.Fatal("expected missing url/dest validation error")
	}
	if err := mod.Validate(map[string]interface{}{
		"url":      "https://example.com/app.tar.gz",
		"dest":     "/tmp/app.tar.gz",
		"checksum": "sha256:abcd",
	}); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if err := mod.Validate(map[string]interface{}{
		"url":      "https://example.com/app.tar.gz",
		"dest":     "/tmp/app.tar.gz",
		"checksum": "crc32:abcd",
	}); err == nil {
		t.Fatal("expected unsupported checksum algorithm error")
	}
}

func TestParseChecksumSpec(t *testing.T) {
	algo, value, err := parseChecksumSpec("sha256:ABCDEF")
	if err != nil {
		t.Fatalf("parseChecksumSpec() error = %v", err)
	}
	if algo != "sha256" || value != "abcdef" {
		t.Fatalf("unexpected checksum parse result: algo=%q value=%q", algo, value)
	}
}

func TestBuildGetURLCommand(t *testing.T) {
	cmd, err := buildGetURLCommand(getURLSpec{
		URL:           "https://example.com/app.tar.gz",
		Dest:          "/srv/app/app.tar.gz",
		Checksum:      "sha256:abcdef",
		Backup:        true,
		Force:         true,
		Timeout:       15,
		ValidateCerts: false,
		Mode:          "0644",
	})
	if err != nil {
		t.Fatalf("buildGetURLCommand() error = %v", err)
	}
	if !strings.Contains(cmd, "curl -fsSL -k") && !strings.Contains(cmd, "wget -q --no-check-certificate") {
		t.Fatalf("expected relaxed TLS flags, got %q", cmd)
	}
	if !strings.Contains(cmd, "expected_checksum=") || !strings.Contains(cmd, "checksum mismatch") {
		t.Fatalf("expected checksum verification, got %q", cmd)
	}
	if !strings.Contains(cmd, "cp -p '/srv/app/app.tar.gz' '/srv/app/app.tar.gz'.bak") {
		t.Fatalf("expected backup handling, got %q", cmd)
	}
	if !strings.Contains(cmd, "chmod 0644 '/srv/app/app.tar.gz'") {
		t.Fatalf("expected mode handling, got %q", cmd)
	}
}

func TestGetURLCommandLocalFileDownload(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		if _, wgetErr := exec.LookPath("wget"); wgetErr != nil {
			t.Skip("curl or wget is required for get_url local execution test")
		}
	}

	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.txt")
	destPath := filepath.Join(dir, "downloaded.txt")
	writeFile(t, sourcePath, "hello\n")

	cmd, err := buildGetURLCommand(getURLSpec{
		URL:           "file://" + sourcePath,
		Dest:          destPath,
		Timeout:       10,
		Force:         true,
		ValidateCerts: true,
	})
	if err != nil {
		t.Fatalf("buildGetURLCommand() error = %v", err)
	}

	output := runShell(t, cmd, dir)
	if !strings.Contains(output, changeMarkerTrue) {
		t.Fatalf("expected first download to change, got %q", output)
	}
	assertFileContent(t, destPath, "hello\n")

	output = runShell(t, cmd, dir)
	if !strings.Contains(output, changeMarkerFalse) {
		t.Fatalf("expected second download to be idempotent, got %q", output)
	}

	writeFile(t, sourcePath, "world\n")
	output = runShell(t, cmd, dir)
	if !strings.Contains(output, changeMarkerTrue) {
		t.Fatalf("expected changed source to trigger update, got %q", output)
	}
	assertFileContent(t, destPath, "world\n")
}

func TestGetURLCommandChecksumMismatch(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		if _, wgetErr := exec.LookPath("wget"); wgetErr != nil {
			t.Skip("curl or wget is required for get_url checksum test")
		}
	}

	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.txt")
	destPath := filepath.Join(dir, "downloaded.txt")
	writeFile(t, sourcePath, "hello\n")

	cmd, err := buildGetURLCommand(getURLSpec{
		URL:           "file://" + sourcePath,
		Dest:          destPath,
		Checksum:      "sha256:" + stringChecksum("different\n"),
		Timeout:       10,
		Force:         true,
		ValidateCerts: true,
	})
	if err != nil {
		t.Fatalf("buildGetURLCommand() error = %v", err)
	}

	command := exec.Command("/bin/bash", "-lc", cmd)
	command.Dir = dir
	output, execErr := command.CombinedOutput()
	if execErr == nil {
		t.Fatalf("expected checksum mismatch to fail, output=%q", string(output))
	}
	if !strings.Contains(string(output), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch output, got %q", string(output))
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func assertFileContent(t *testing.T, path, expected string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != expected {
		t.Fatalf("expected file content %q, got %q", expected, string(data))
	}
}
