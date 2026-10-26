package variable

import (
	"bytes"
	"strings"
	"testing"
)

func TestVaultEncryptDecryptRoundTrip(t *testing.T) {
	password := "my-secret-vault-password"
	plaintext := []byte("secret_api_key: 1234567890abcdef\ndatabase_url: postgres://user:pass@localhost:5432/db")

	encrypted, err := EncryptVault(plaintext, password)
	if err != nil {
		t.Fatalf("EncryptVault() error = %v", err)
	}

	if !IsVaultData(encrypted) {
		t.Errorf("expected IsVaultData to be true")
	}

	if !strings.HasPrefix(string(encrypted), "$ANSIBLE_VAULT;1.1;AES256\n") {
		t.Errorf("expected header $ANSIBLE_VAULT;1.1;AES256, got: %s", string(encrypted[:30]))
	}

	decrypted, err := DecryptVault(encrypted, password)
	if err != nil {
		t.Fatalf("DecryptVault() error = %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("expected decrypted text %q, got %q", string(plaintext), string(decrypted))
	}

	// Test wrong password
	_, err = DecryptVault(encrypted, "wrong-password")
	if err == nil {
		t.Errorf("expected DecryptVault to fail with wrong password")
	}
}

func TestVaultEncryptWithID(t *testing.T) {
	password := "test-pass"
	plaintext := []byte("top_secret_data")
	vaultID := "prod_vault"

	encrypted, err := EncryptVaultWithID(plaintext, password, vaultID)
	if err != nil {
		t.Fatalf("EncryptVaultWithID() error = %v", err)
	}

	if !strings.HasPrefix(string(encrypted), "$ANSIBLE_VAULT;1.2;AES256;prod_vault\n") {
		t.Errorf("expected 1.2 header with vault ID, got: %s", string(encrypted[:40]))
	}

	decrypted, err := DecryptVault(encrypted, password)
	if err != nil {
		t.Fatalf("DecryptVault() error = %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Errorf("expected %q, got %q", string(plaintext), string(decrypted))
	}
}

func TestVaultRekey(t *testing.T) {
	oldPass := "old-password"
	newPass := "new-password"
	plaintext := []byte("sensitive_token: tok_987654321")

	encrypted, err := EncryptVault(plaintext, oldPass)
	if err != nil {
		t.Fatalf("EncryptVault error = %v", err)
	}

	rekeyed, err := RekeyVault(encrypted, oldPass, newPass)
	if err != nil {
		t.Fatalf("RekeyVault error = %v", err)
	}

	// Old pass must fail
	if _, err := DecryptVault(rekeyed, oldPass); err == nil {
		t.Errorf("expected decryption with old password to fail")
	}

	// New pass must succeed
	decrypted, err := DecryptVault(rekeyed, newPass)
	if err != nil {
		t.Fatalf("DecryptVault with new password error = %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Errorf("expected %q, got %q", string(plaintext), string(decrypted))
	}
}

func TestFormatVaultInline(t *testing.T) {
	password := "inline-pass"
	plaintext := []byte("super-secret")

	encrypted, err := EncryptVault(plaintext, password)
	if err != nil {
		t.Fatalf("EncryptVault error = %v", err)
	}

	formatted := FormatVaultInline(encrypted, "api_token")
	if !strings.HasPrefix(formatted, "api_token: !vault |\n") {
		t.Errorf("expected formatted to start with 'api_token: !vault |', got: %s", formatted)
	}

	if !strings.Contains(formatted, "          $ANSIBLE_VAULT;1.1;AES256") {
		t.Errorf("expected indented vault header, got: %s", formatted)
	}
}
