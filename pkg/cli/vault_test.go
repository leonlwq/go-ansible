package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/variable"
)

func TestVaultCLI_EncryptAndDecryptFile(t *testing.T) {
	tempDir := t.TempDir()
	dataFile := filepath.Join(tempDir, "secrets.yml")
	passFile := filepath.Join(tempDir, "vault_pass.txt")

	plainContent := "api_secret: s3cr3t_v4lu3\ndb_pass: supersecret"
	password := "p@ssw0rd123"

	if err := os.WriteFile(dataFile, []byte(plainContent), 0644); err != nil {
		t.Fatalf("WriteFile(dataFile) error: %v", err)
	}
	if err := os.WriteFile(passFile, []byte(password), 0600); err != nil {
		t.Fatalf("WriteFile(passFile) error: %v", err)
	}

	// 1. Test Encrypt
	cmd := NewVaultCmd("0.1.0")
	cmd.SetArgs([]string{"encrypt", "--vault-password-file", passFile, dataFile})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("encrypt execute error: %v", err)
	}

	encryptedData, err := os.ReadFile(dataFile)
	if err != nil {
		t.Fatalf("ReadFile(dataFile) error: %v", err)
	}
	if !variable.IsVaultData(encryptedData) {
		t.Errorf("expected file to be encrypted with vault header")
	}

	// 2. Test Decrypt
	cmd = NewVaultCmd("0.1.0")
	cmd.SetArgs([]string{"decrypt", "--vault-password-file", passFile, dataFile})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("decrypt execute error: %v", err)
	}

	decryptedData, err := os.ReadFile(dataFile)
	if err != nil {
		t.Fatalf("ReadFile(dataFile) error: %v", err)
	}
	if string(decryptedData) != plainContent {
		t.Errorf("expected decrypted data %q, got %q", plainContent, string(decryptedData))
	}
}

func TestVaultCLI_EncryptWithOutput(t *testing.T) {
	tempDir := t.TempDir()
	srcFile := filepath.Join(tempDir, "plain.yml")
	outFile := filepath.Join(tempDir, "encrypted.yml")
	passFile := filepath.Join(tempDir, "pass.txt")

	content := "token: abc123xyz"
	_ = os.WriteFile(srcFile, []byte(content), 0644)
	_ = os.WriteFile(passFile, []byte("vaultpass"), 0600)

	cmd := NewVaultCmd("0.1.0")
	cmd.SetArgs([]string{"encrypt", "--vault-password-file", passFile, "--output", outFile, srcFile})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("encrypt with output error: %v", err)
	}

	// Original must still be plaintext
	srcData, _ := os.ReadFile(srcFile)
	if string(srcData) != content {
		t.Errorf("expected src file to remain plaintext")
	}

	// Out file must be encrypted
	outData, _ := os.ReadFile(outFile)
	if !variable.IsVaultData(outData) {
		t.Errorf("expected output file to be encrypted")
	}
}

func TestVaultCLI_View(t *testing.T) {
	tempDir := t.TempDir()
	dataFile := filepath.Join(tempDir, "data.yml")
	passFile := filepath.Join(tempDir, "pass.txt")

	content := "app_env: production"
	password := "viewpass"
	_ = os.WriteFile(passFile, []byte(password), 0600)

	encrypted, err := variable.EncryptVault([]byte(content), password)
	if err != nil {
		t.Fatalf("EncryptVault error: %v", err)
	}
	_ = os.WriteFile(dataFile, encrypted, 0644)

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := NewVaultCmd("0.1.0")
	cmd.SetArgs([]string{"view", "--vault-password-file", passFile, dataFile})
	execErr := cmd.Execute()

	_ = w.Close()
	os.Stdout = oldStdout

	if execErr != nil {
		t.Fatalf("view execute error: %v", execErr)
	}

	outBytes, _ := io.ReadAll(r)
	if strings.TrimSpace(string(outBytes)) != content {
		t.Errorf("expected view output %q, got %q", content, string(outBytes))
	}
}

func TestVaultCLI_Rekey(t *testing.T) {
	tempDir := t.TempDir()
	dataFile := filepath.Join(tempDir, "secret.yml")
	oldPassFile := filepath.Join(tempDir, "old_pass.txt")
	newPassFile := filepath.Join(tempDir, "new_pass.txt")

	content := "password: confidential"
	_ = os.WriteFile(oldPassFile, []byte("old123"), 0600)
	_ = os.WriteFile(newPassFile, []byte("new456"), 0600)

	encrypted, _ := variable.EncryptVault([]byte(content), "old123")
	_ = os.WriteFile(dataFile, encrypted, 0644)

	cmd := NewVaultCmd("0.1.0")
	cmd.SetArgs([]string{"rekey", "--vault-password-file", oldPassFile, "--new-vault-password-file", newPassFile, dataFile})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rekey execute error: %v", err)
	}

	rekeyedData, _ := os.ReadFile(dataFile)
	// Must decrypt with new password
	decrypted, err := variable.DecryptVault(rekeyedData, "new456")
	if err != nil {
		t.Fatalf("failed to decrypt rekeyed data with new password: %v", err)
	}
	if string(decrypted) != content {
		t.Errorf("expected %q, got %q", content, string(decrypted))
	}
}

func TestVaultCLI_EncryptString(t *testing.T) {
	tempDir := t.TempDir()
	passFile := filepath.Join(tempDir, "pass.txt")
	_ = os.WriteFile(passFile, []byte("stringpass"), 0600)

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := NewVaultCmd("0.1.0")
	cmd.SetArgs([]string{"encrypt_string", "--vault-password-file", passFile, "--name", "db_password", "my_secret"})
	execErr := cmd.Execute()

	_ = w.Close()
	os.Stdout = oldStdout

	if execErr != nil {
		t.Fatalf("encrypt_string execute error: %v", execErr)
	}

	outBytes, _ := io.ReadAll(r)
	outStr := string(outBytes)

	if !strings.HasPrefix(outStr, "db_password: !vault |\n") {
		t.Errorf("expected output to start with 'db_password: !vault |', got %s", outStr)
	}
	if !strings.Contains(outStr, "$ANSIBLE_VAULT;1.1;AES256") {
		t.Errorf("expected output to contain vault header, got %s", outStr)
	}
}

func TestVaultViewIntegration(t *testing.T) {
	tempDir := t.TempDir()
	dataFile := filepath.Join(tempDir, "data.yml")
	passFile := filepath.Join(tempDir, "pass.txt")
	_ = os.WriteFile(passFile, []byte("mypass"), 0600)

	encrypted, _ := variable.EncryptVault([]byte("nested_val: 42"), "mypass")
	_ = os.WriteFile(dataFile, encrypted, 0644)

	vaultCmd := NewVaultCmd("0.1.0")

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	vaultCmd.SetArgs([]string{"view", "--vault-password-file", passFile, dataFile})
	execErr := vaultCmd.Execute()

	_ = w.Close()
	os.Stdout = oldStdout

	if execErr != nil {
		t.Fatalf("ansible-vault view error: %v", execErr)
	}

	out, _ := io.ReadAll(r)
	if strings.TrimSpace(string(out)) != "nested_val: 42" {
		t.Errorf("expected 'nested_val: 42', got %q", string(out))
	}
}
