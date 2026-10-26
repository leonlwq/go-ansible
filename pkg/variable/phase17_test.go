package variable

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseVaultIDString(t *testing.T) {
	tests := []struct {
		input       string
		expectedID  string
		expectedSrc string
	}{
		{"dev@password.txt", "dev", "password.txt"},
		{"prod@/opt/vault/client.sh", "prod", "/opt/vault/client.sh"},
		{"dev@prompt", "dev", "prompt"},
		{"my_secret.txt", "", "my_secret.txt"},
		{"prompt", "", "prompt"},
		{"  staging@secret.txt  ", "staging", "secret.txt"},
	}

	for _, tc := range tests {
		id, src := ParseVaultIDString(tc.input)
		if id != tc.expectedID || src != tc.expectedSrc {
			t.Errorf("ParseVaultIDString(%q) = (%q, %q), expected (%q, %q)", tc.input, id, src, tc.expectedID, tc.expectedSrc)
		}
	}
}

func TestLoadVaultSecret_ExternalScript(t *testing.T) {
	tempDir := t.TempDir()
	scriptPath := filepath.Join(tempDir, "vault_client.sh")

	// 创建可执行脚本，根据参数输出密码
	scriptContent := `#!/bin/sh
if [ "$1" = "--vault-id" ]; then
    echo "pass_for_$2"
else
    echo "default_script_pass"
fi
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("failed to create client script: %v", err)
	}

	// 1. 带 vaultID 的外部脚本执行
	sec1, err := LoadVaultSecret("dev", scriptPath, nil)
	if err != nil {
		t.Fatalf("LoadVaultSecret with dev failed: %v", err)
	}
	if sec1.ID != "dev" || sec1.Password != "pass_for_dev" {
		t.Fatalf("unexpected secret: id=%s, pass=%s", sec1.ID, sec1.Password)
	}

	// 2. 不带 vaultID 的外部脚本执行
	sec2, err := LoadVaultSecret("", scriptPath, nil)
	if err != nil {
		t.Fatalf("LoadVaultSecret without id failed: %v", err)
	}
	if sec2.Password != "default_script_pass" {
		t.Fatalf("unexpected secret without id: pass=%s", sec2.Password)
	}
}

func TestLoadVaultSecret_Prompt(t *testing.T) {
	promptCalls := 0
	mockPrompt := func(prompt string) (string, error) {
		promptCalls++
		if strings.Contains(prompt, "staging") {
			return "staging_secret", nil
		}
		return "fallback_secret", nil
	}

	sec, err := LoadVaultSecret("staging", "prompt", mockPrompt)
	if err != nil {
		t.Fatalf("LoadVaultSecret prompt failed: %v", err)
	}
	if sec.ID != "staging" || sec.Password != "staging_secret" {
		t.Fatalf("unexpected secret from prompt: id=%s, pass=%s", sec.ID, sec.Password)
	}
	if promptCalls != 1 {
		t.Fatalf("expected prompt to be called once, got %d", promptCalls)
	}
}

func TestMultiVaultID_EncryptDecryptWithSecrets(t *testing.T) {
	devPass := "dev_secret_password"
	prodPass := "prod_secret_password"

	devPlain := []byte("database_password_dev: 123456")
	prodPlain := []byte("database_password_prod: 987654")

	// 分别使用 dev 与 prod 加密
	devEncrypted, err := EncryptVaultWithID(devPlain, devPass, "dev")
	if err != nil {
		t.Fatalf("failed to encrypt dev: %v", err)
	}
	if !strings.Contains(string(devEncrypted), "$ANSIBLE_VAULT;1.2;AES256;dev") {
		t.Fatalf("expected dev header, got:\n%s", string(devEncrypted))
	}

	prodEncrypted, err := EncryptVaultWithID(prodPlain, prodPass, "prod")
	if err != nil {
		t.Fatalf("failed to encrypt prod: %v", err)
	}
	if !strings.Contains(string(prodEncrypted), "$ANSIBLE_VAULT;1.2;AES256;prod") {
		t.Fatalf("expected prod header, got:\n%s", string(prodEncrypted))
	}

	secrets := []*VaultSecret{
		{ID: "dev", Password: devPass},
		{ID: "prod", Password: prodPass},
	}

	// 解密 dev
	decDev, matchedDev, err := DecryptVaultWithSecrets(devEncrypted, secrets)
	if err != nil {
		t.Fatalf("decrypt dev with secrets failed: %v", err)
	}
	if string(decDev) != string(devPlain) {
		t.Fatalf("expected %q, got %q", string(devPlain), string(decDev))
	}
	if matchedDev.ID != "dev" {
		t.Fatalf("expected matched secret dev, got %s", matchedDev.ID)
	}

	// 解密 prod
	decProd, matchedProd, err := DecryptVaultWithSecrets(prodEncrypted, secrets)
	if err != nil {
		t.Fatalf("decrypt prod with secrets failed: %v", err)
	}
	if string(decProd) != string(prodPlain) {
		t.Fatalf("expected %q, got %q", string(prodPlain), string(decProd))
	}
	if matchedProd.ID != "prod" {
		t.Fatalf("expected matched secret prod, got %s", matchedProd.ID)
	}

	// 测试 Vault 1.1 无 vaultID 格式的回退解密
	v11Encrypted, err := EncryptVault(devPlain, devPass)
	if err != nil {
		t.Fatalf("failed to encrypt v11: %v", err)
	}
	decV11, matchedV11, err := DecryptVaultWithSecrets(v11Encrypted, secrets)
	if err != nil {
		t.Fatalf("decrypt v11 with secrets failed: %v", err)
	}
	if string(decV11) != string(devPlain) || matchedV11.Password != devPass {
		t.Fatalf("expected v11 decrypt success with dev secret")
	}

	// 测试全错密码
	wrongSecrets := []*VaultSecret{
		{ID: "dev", Password: "wrong"},
		{ID: "prod", Password: "wrong"},
	}
	_, _, err = DecryptVaultWithSecrets(devEncrypted, wrongSecrets)
	if err == nil {
		t.Fatalf("expected decrypt failure with wrong secrets, got nil")
	}
}

func TestManagerLoadVarsFileMultiVaultSecrets(t *testing.T) {
	tempDir := t.TempDir()

	devFile := filepath.Join(tempDir, "dev_vars.yml")
	prodFile := filepath.Join(tempDir, "prod_vars.yml")

	devContent := []byte("env: development\napi_key: secret_dev_key\n")
	prodContent := []byte("env: production\napi_key: secret_prod_key\n")

	devEnc, err := EncryptVaultWithID(devContent, "dev_pass", "dev")
	if err != nil {
		t.Fatalf("encrypt dev: %v", err)
	}
	prodEnc, err := EncryptVaultWithID(prodContent, "prod_pass", "prod")
	if err != nil {
		t.Fatalf("encrypt prod: %v", err)
	}

	if err := os.WriteFile(devFile, devEnc, 0644); err != nil {
		t.Fatalf("write dev file: %v", err)
	}
	if err := os.WriteFile(prodFile, prodEnc, 0644); err != nil {
		t.Fatalf("write prod file: %v", err)
	}

	mgr := NewManager()
	mgr.SetVaultSecrets([]*VaultSecret{
		{ID: "dev", Password: "dev_pass"},
		{ID: "prod", Password: "prod_pass"},
	})

	// 测试加载 dev 变量文件
	devVars, err := mgr.LoadVarsFile(devFile)
	if err != nil {
		t.Fatalf("LoadVarsFile devFile failed: %v", err)
	}
	if devVars["env"] != "development" || devVars["api_key"] != "secret_dev_key" {
		t.Fatalf("unexpected devVars: %#v", devVars)
	}

	// 测试加载 prod 变量文件
	prodVars, err := mgr.LoadVarsFile(prodFile)
	if err != nil {
		t.Fatalf("LoadVarsFile prodFile failed: %v", err)
	}
	if prodVars["env"] != "production" || prodVars["api_key"] != "secret_prod_key" {
		t.Fatalf("unexpected prodVars: %#v", prodVars)
	}
}

func TestManagerInlineVaultMultiSecrets(t *testing.T) {
	tempDir := t.TempDir()
	varsFile := filepath.Join(tempDir, "mixed_vars.yml")

	devEnc, _ := EncryptVaultWithID([]byte("super_dev_token"), "dev_pass", "dev")
	prodEnc, _ := EncryptVaultWithID([]byte("super_prod_token"), "prod_pass", "prod")

	devInline := FormatVaultInline(devEnc, "token_dev")
	prodInline := FormatVaultInline(prodEnc, "token_prod")

	mixedContent := fmt.Sprintf("app_name: my_app\n%s\n%s\n", devInline, prodInline)
	if err := os.WriteFile(varsFile, []byte(mixedContent), 0644); err != nil {
		t.Fatalf("write mixed file: %v", err)
	}

	mgr := NewManager()
	mgr.SetVaultSecrets([]*VaultSecret{
		{ID: "dev", Password: "dev_pass"},
		{ID: "prod", Password: "prod_pass"},
	})

	vars, err := mgr.LoadVarsFile(varsFile)
	if err != nil {
		t.Fatalf("LoadVarsFile mixed vars failed: %v", err)
	}

	if vars["token_dev"] != "super_dev_token" {
		t.Fatalf("expected token_dev 'super_dev_token', got %#v", vars["token_dev"])
	}
	if vars["token_prod"] != "super_prod_token" {
		t.Fatalf("expected token_prod 'super_prod_token', got %#v", vars["token_prod"])
	}
}
