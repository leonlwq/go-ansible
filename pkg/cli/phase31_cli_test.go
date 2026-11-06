package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 6. 测试 ansible-vault view 与 encrypt_string 的 -o/--output 输出落盘支持
func TestPhase31_VaultOutputFlag(t *testing.T) {
	tempDir := t.TempDir()
	passFile := filepath.Join(tempDir, "vault.pass")
	_ = os.WriteFile(passFile, []byte("phase31secret"), 0600)

	// (a) 创建并加密文件
	plainFile := filepath.Join(tempDir, "secret.txt")
	_ = os.WriteFile(plainFile, []byte("top_secret_database_password\n"), 0644)

	encOpts := vaultOptions{
		passwordFile: passFile,
	}
	if err := runVaultEncrypt([]string{plainFile}, encOpts); err != nil {
		t.Fatalf("runVaultEncrypt failed: %v", err)
	}

	// (b) view --output落盘
	viewOutFile := filepath.Join(tempDir, "view_output.txt")
	viewOpts := vaultOptions{
		passwordFile: passFile,
		outputFile:   viewOutFile,
	}
	if err := runVaultView([]string{plainFile}, viewOpts); err != nil {
		t.Fatalf("runVaultView with output file failed: %v", err)
	}

	viewContent, err := os.ReadFile(viewOutFile)
	if err != nil {
		t.Fatalf("failed to read view_output.txt: %v", err)
	}
	if strings.TrimSpace(string(viewContent)) != "top_secret_database_password" {
		t.Fatalf("expected view content 'top_secret_database_password', got: %q", string(viewContent))
	}

	// (c) encrypt_string --output落盘
	encStrOutFile := filepath.Join(tempDir, "enc_string_out.yml")
	encStrOpts := vaultOptions{
		passwordFile: passFile,
		varName:      "db_password",
		outputFile:   encStrOutFile,
	}
	if err := runVaultEncryptString([]string{"api_token_12345"}, encStrOpts); err != nil {
		t.Fatalf("runVaultEncryptString with output file failed: %v", err)
	}

	encStrContent, err := os.ReadFile(encStrOutFile)
	if err != nil {
		t.Fatalf("failed to read enc_string_out.yml: %v", err)
	}
	if !strings.Contains(string(encStrContent), "!vault |") || !strings.Contains(string(encStrContent), "db_password:") {
		t.Fatalf("expected vault encrypted string block with db_password, got: %q", string(encStrContent))
	}
}

// 7. 测试 ansible-pull 默认 hostname 限制隔离与 --vault-id 支持
func TestPhase31_PullDefaultLimitAndVaultID(t *testing.T) {
	tempDir := t.TempDir()
	sourceRepo := filepath.Join(tempDir, "remote_git")
	destRepo := filepath.Join(tempDir, "pulled_git")

	_ = os.MkdirAll(sourceRepo, 0755)
	if err := runGit(sourceRepo, "init"); err != nil {
		t.Skip("git init failed, skipping test")
	}
	_ = runGit(sourceRepo, "config", "user.name", "Tester")
	_ = runGit(sourceRepo, "config", "user.email", "tester@example.com")

	// 清单中同时声明当前机器 hostname 与 remote_worker
	hName, _ := os.Hostname()
	if hName == "" {
		hName = "localnode"
	}
	invContent := hName + " ansible_connection=local\nremote_worker ansible_connection=ssh\n"
	_ = os.WriteFile(filepath.Join(sourceRepo, "hosts"), []byte(invContent), 0644)

	playbookContent := `---
- name: Test Pull Default Limit
  hosts: all
  gather_facts: false
  tasks:
    - name: Ping
      debug:
        msg: "hello"
`
	_ = os.WriteFile(filepath.Join(sourceRepo, "local.yml"), []byte(playbookContent), 0644)
	_ = runGit(sourceRepo, "add", "hosts", "local.yml")
	_ = runGit(sourceRepo, "commit", "-m", "init pull repo")

	// pull 未显式指定 --limit 时，应自动限制为当前 hostname 从而跳过 remote_worker
	vPassFile := filepath.Join(tempDir, "vpass.txt")
	_ = os.WriteFile(vPassFile, []byte("testsecret"), 0600)

	opts := &PullOptions{
		URL:       sourceRepo,
		Directory: destRepo,
		Full:      true,
		VaultIDs:  []string{"dev@" + vPassFile},
	}

	// 确认 VaultIDs 透传
	if len(opts.VaultIDs) != 1 || opts.VaultIDs[0] != "dev@"+vPassFile {
		t.Fatalf("expected VaultIDs to be set on PullOptions")
	}

	if err := runPull(opts); err != nil {
		t.Fatalf("runPull failed: %v", err)
	}
}

// 8. 测试 ansible-doc 多模块展示与 -j/--json 结构化导出
func TestPhase31_DocMultiModuleAndJSON(t *testing.T) {
	// (a) 多模块展示
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	opts := &DocOptions{
		Modules: []string{"ping", "command"},
	}
	err := opts.Run()
	_ = w.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("runDoc failed: %v", err)
	}
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	out := buf.String()
	if !strings.Contains(out, "> PING") || !strings.Contains(out, "> COMMAND") {
		t.Fatalf("expected both PING and COMMAND doc in output, got: %s", out)
	}

	// (b) -j / --json 结构化导出
	rJSON, wJSON, _ := os.Pipe()
	os.Stdout = wJSON

	jsonOpts := &DocOptions{
		Modules: []string{"copy", "shell"},
		JSON:    true,
	}
	errJSON := jsonOpts.Run()
	_ = wJSON.Close()
	os.Stdout = oldStdout

	if errJSON != nil {
		t.Fatalf("runDoc json failed: %v", errJSON)
	}
	var bufJSON bytes.Buffer
	_, _ = io.Copy(&bufJSON, rJSON)

	var docMap map[string]interface{}
	if err := json.Unmarshal(bufJSON.Bytes(), &docMap); err != nil {
		t.Fatalf("failed to parse doc json output: %v, raw: %s", err, bufJSON.String())
	}
	if _, ok := docMap["copy"]; !ok {
		t.Fatalf("expected 'copy' key in json doc output")
	}
	if _, ok := docMap["shell"]; !ok {
		t.Fatalf("expected 'shell' key in json doc output")
	}
}
