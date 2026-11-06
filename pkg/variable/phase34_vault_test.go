package variable

import (
	"strings"
	"testing"
)

// 9. 测试 Ansible Vault 处理 Windows CRLF (\r\n) 换行符的稳健解密
func TestPhase34_Vault_WindowsCRLF(t *testing.T) {
	plaintext := "database_password: super_secret_production_pass"
	password := "VaultPassword123!"

	// 1. 标准加密
	encrypted, err := EncryptVault([]byte(plaintext), password)
	if err != nil {
		t.Fatalf("failed to encrypt vault: %v", err)
	}

	// 2. 模拟 Windows 环境签出时被转换为 \r\n 换行符
	windowsVault := strings.ReplaceAll(string(encrypted), "\n", "\r\n")

	// 3. 解密 Windows CRLF Vault 数据
	decrypted, err := DecryptVault([]byte(windowsVault), password)
	if err != nil {
		t.Fatalf("failed to decrypt vault containing CRLF line endings: %v", err)
	}

	if string(decrypted) != plaintext {
		t.Fatalf("expected plaintext %q, got %q", plaintext, string(decrypted))
	}

	// 4. 测试 parseVaultEnvelope 对 CRLF 的容错
	payload, version, cipher, vaultID, err := parseVaultEnvelope([]byte(windowsVault))
	if err != nil {
		t.Fatalf("failed to parse envelope: %v", err)
	}
	if version != "1.1" {
		t.Fatalf("expected version 1.1, got %q", version)
	}
	if cipher != "AES256" {
		t.Fatalf("expected cipher AES256, got %q", cipher)
	}
	_ = payload
	_ = vaultID
}
