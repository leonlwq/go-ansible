package ssh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/inventory"
)

// 10. 测试 BuildBecomeCommand 的 BecomeFlags 与 sudo -p ''
func TestPhase34_BecomeFlagsAndPrompt(t *testing.T) {
	host := &inventory.Host{
		Name:        "web-worker",
		BecomeUser:  "deploy",
		BecomePass:  "MySecretPass",
		BecomeFlags: "--preserve-env=PATH -H",
	}

	cmd := BuildBecomeCommand("systemctl restart nginx", host)
	if !strings.Contains(cmd, "-p ''") {
		t.Fatalf("expected sudo -p '' to be present to suppress password prompts, got: %s", cmd)
	}
	if !strings.Contains(cmd, "--preserve-env=PATH -H") {
		t.Fatalf("expected become flags to be included in command, got: %s", cmd)
	}
	if !strings.Contains(cmd, "-u deploy") {
		t.Fatalf("expected -u deploy, got: %s", cmd)
	}
}

// 11. 测试 LocalConnection.Upload 目标为既有目录时自动拼接文件名
func TestPhase34_LocalConnection_UploadToDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "phase34-upload-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	srcFile := filepath.Join(tmpDir, "source.txt")
	content := "hello phase34"
	if err := os.WriteFile(srcFile, []byte(content), 0644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	destDir := filepath.Join(tmpDir, "target_dir")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatalf("create target dir: %v", err)
	}

	conn := &LocalConnection{}
	// 上传到 destDir（注意没有以 / 结尾）
	if err := conn.Upload(srcFile, destDir); err != nil {
		t.Fatalf("upload to directory failed: %v", err)
	}

	expectedDestFile := filepath.Join(destDir, "source.txt")
	data, err := os.ReadFile(expectedDestFile)
	if err != nil {
		t.Fatalf("failed to read expected destination file %s: %v", expectedDestFile, err)
	}
	if string(data) != content {
		t.Fatalf("expected content %q, got %q", content, string(data))
	}
}

// 16. 测试 Client.IsAlive 探测
func TestPhase34_Client_IsAlive(t *testing.T) {
	client := &Client{
		connected: false,
	}
	if client.IsAlive() {
		t.Fatalf("expected disconnected client to return IsAlive() == false")
	}
}

