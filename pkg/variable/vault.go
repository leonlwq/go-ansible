package variable

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

const vaultHeaderPrefix = "$ANSIBLE_VAULT;"

// EncryptVault 使用 AES256-CTR + HMAC-SHA256 加密数据，生成 Ansible Vault 1.1 格式
func EncryptVault(plaintext []byte, password string) ([]byte, error) {
	return EncryptVaultWithID(plaintext, password, "")
}

// EncryptVaultWithID 使用指定 Vault ID 加密数据，生成 Ansible Vault 1.1/1.2 格式
func EncryptVaultWithID(plaintext []byte, password, vaultID string) ([]byte, error) {
	if strings.TrimSpace(password) == "" {
		return nil, fmt.Errorf("vault password is required")
	}

	salt := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("generate random salt: %w", err)
	}

	derivedKey := pbkdf2.Key([]byte(password), salt, 10000, 80, sha256.New)
	key1 := derivedKey[:32]
	key2 := derivedKey[32:64]
	iv := derivedKey[64:80]

	block, err := aes.NewCipher(key1)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	padded := pkcs7Pad(plaintext, aes.BlockSize)
	ciphertext := make([]byte, len(padded))
	stream := cipher.NewCTR(block, iv)
	stream.XORKeyStream(ciphertext, padded)

	mac := hmac.New(sha256.New, key2)
	mac.Write(ciphertext)
	cryptedHMAC := hex.EncodeToString(mac.Sum(nil))

	saltHex := hex.EncodeToString(salt)
	cipherHex := hex.EncodeToString(ciphertext)
	inner := fmt.Sprintf("%s\n%s\n%s", saltHex, cryptedHMAC, cipherHex)

	payloadHex := hex.EncodeToString([]byte(inner))

	var wrapped strings.Builder
	header := "$ANSIBLE_VAULT;1.1;AES256"
	if vaultID != "" {
		header = fmt.Sprintf("$ANSIBLE_VAULT;1.2;AES256;%s", vaultID)
	}
	wrapped.WriteString(header)
	wrapped.WriteString("\n")

	for i := 0; i < len(payloadHex); i += 80 {
		end := i + 80
		if end > len(payloadHex) {
			end = len(payloadHex)
		}
		wrapped.WriteString(payloadHex[i:end])
		wrapped.WriteString("\n")
	}

	return []byte(wrapped.String()), nil
}

// RekeyVault 使用新密码重新加密 Vault 数据
func RekeyVault(data []byte, oldPassword, newPassword string) ([]byte, error) {
	_, _, _, vaultID, err := parseVaultEnvelope(data)
	if err != nil {
		return nil, err
	}

	plaintext, err := DecryptVault(data, oldPassword)
	if err != nil {
		return nil, fmt.Errorf("decrypt with old password: %w", err)
	}

	return EncryptVaultWithID(plaintext, newPassword, vaultID)
}

// FormatVaultInline 将 Vault 数据格式化为 YAML 行内 !vault 格式
func FormatVaultInline(vaultData []byte, varName string) string {
	lines := strings.Split(strings.TrimSpace(string(vaultData)), "\n")
	var sb strings.Builder

	indent := "          "
	if varName != "" {
		sb.WriteString(fmt.Sprintf("%s: !vault |\n", varName))
	} else {
		sb.WriteString("!vault |\n")
	}

	for _, line := range lines {
		sb.WriteString(indent)
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	return sb.String()
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - (len(data) % blockSize)
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(data, padtext...)
}

// VaultSecret 代表一个已解析的 Vault 密码凭据
type VaultSecret struct {
	ID       string // 标识标签，如 "dev", "prod", 或空/default
	Password string // 解密密码
}

// ParseVaultIDString 解析形如 "dev@pass.txt" 或 "prompt" 或 "secret.txt" 的配置串
// 返回 id 与 source
func ParseVaultIDString(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if idx := strings.Index(raw, "@"); idx != -1 {
		id := strings.TrimSpace(raw[:idx])
		source := strings.TrimSpace(raw[idx+1:])
		return id, source
	}
	return "", raw
}

// LoadVaultSecret 加载单个 VaultSecret，支持三种来源：
// 1. "prompt"：交互式提示输入密码 (若 promptFn != nil)
// 2. 外部客户端脚本：若 source 文件具有可执行权限，执行脚本并读取 stdout（若 id 非空传入 --vault-id <id>）
// 3. 普通文本文件：读取文件内容并去除首尾空白
func LoadVaultSecret(id, source string, promptFn func(prompt string) (string, error)) (*VaultSecret, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, fmt.Errorf("empty vault source")
	}

	if source == "prompt" {
		if promptFn == nil {
			return nil, fmt.Errorf("interactive prompt requested but prompt function is not provided")
		}
		promptMsg := "Vault password: "
		if id != "" {
			promptMsg = fmt.Sprintf("Vault password (%s): ", id)
		}
		pass, err := promptFn(promptMsg)
		if err != nil {
			return nil, fmt.Errorf("prompt for vault password (%s): %w", id, err)
		}
		return &VaultSecret{ID: id, Password: pass}, nil
	}

	expandedPath := source
	if strings.HasPrefix(expandedPath, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			expandedPath = filepath.Join(home, expandedPath[2:])
		}
	}
	expandedPath = os.ExpandEnv(expandedPath)

	info, err := os.Stat(expandedPath)
	if err != nil {
		return nil, fmt.Errorf("stat vault secret source %q: %w", source, err)
	}

	// 检查是否具有可执行权限 (Unix executable)
	if !info.IsDir() && (info.Mode()&0111 != 0) {
		var cmd *exec.Cmd
		if id != "" {
			cmd = exec.Command(expandedPath, "--vault-id", id)
		} else {
			cmd = exec.Command(expandedPath)
		}
		output, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("execute vault secret script %q: %w", expandedPath, err)
		}
		pass := strings.TrimRight(string(output), "\r\n")
		return &VaultSecret{ID: id, Password: pass}, nil
	}

	data, err := os.ReadFile(expandedPath)
	if err != nil {
		return nil, fmt.Errorf("read vault password file %q: %w", expandedPath, err)
	}
	pass := strings.TrimRight(string(data), "\r\n")
	return &VaultSecret{ID: id, Password: pass}, nil
}

func IsVaultData(data []byte) bool {
	return bytes.HasPrefix(bytes.TrimSpace(data), []byte(vaultHeaderPrefix))
}

// DecryptVault 使用单一密码解密数据
func DecryptVault(data []byte, password string) ([]byte, error) {
	if strings.TrimSpace(password) == "" {
		return nil, fmt.Errorf("vault password is required")
	}

	res, _, err := DecryptVaultWithSecrets(data, []*VaultSecret{{Password: password}})
	return res, err
}

// DecryptVaultWithSecrets 使用一组 VaultSecret 解密数据，并返回解密明文及成功匹配的 secret
func DecryptVaultWithSecrets(data []byte, secrets []*VaultSecret) ([]byte, *VaultSecret, error) {
	if len(secrets) == 0 {
		return nil, nil, fmt.Errorf("no vault secrets provided")
	}

	payload, version, cipherName, vaultID, err := parseVaultEnvelope(data)
	if err != nil {
		return nil, nil, err
	}
	if cipherName != "AES256" {
		return nil, nil, fmt.Errorf("unsupported vault cipher: %s", cipherName)
	}
	if version != "1.1" && version != "1.2" {
		return nil, nil, fmt.Errorf("unsupported vault version: %s", version)
	}

	ciphertext, salt, cryptedHMAC, err := parseVaultPayload(payload)
	if err != nil {
		return nil, nil, err
	}

	orderedSecrets := make([]*VaultSecret, 0, len(secrets))
	if vaultID != "" {
		for _, s := range secrets {
			if s != nil && s.ID == vaultID {
				orderedSecrets = append(orderedSecrets, s)
			}
		}
		for _, s := range secrets {
			if s != nil && s.ID != vaultID {
				orderedSecrets = append(orderedSecrets, s)
			}
		}
	} else {
		for _, s := range secrets {
			if s != nil {
				orderedSecrets = append(orderedSecrets, s)
			}
		}
	}

	if len(orderedSecrets) == 0 {
		return nil, nil, fmt.Errorf("no matching vault secrets available for vault-id %q", vaultID)
	}

	var lastErr error
	for _, sec := range orderedSecrets {
		if strings.TrimSpace(sec.Password) == "" {
			continue
		}
		plaintext, err := decryptPayloadWithPassword(ciphertext, salt, cryptedHMAC, sec.Password)
		if err == nil {
			return plaintext, sec, nil
		}
		lastErr = err
	}

	if lastErr != nil {
		return nil, nil, fmt.Errorf("vault decryption failed: %w", lastErr)
	}
	return nil, nil, fmt.Errorf("vault decryption failed: none of the available secrets matched")
}

func decryptPayloadWithPassword(ciphertext, salt, cryptedHMAC []byte, password string) ([]byte, error) {
	derivedKey := pbkdf2.Key([]byte(password), salt, 10000, 80, sha256.New)
	key1 := derivedKey[:32]
	key2 := derivedKey[32:64]
	iv := derivedKey[64:80]

	mac := hmac.New(sha256.New, key2)
	mac.Write(ciphertext)
	if !hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), cryptedHMAC) {
		return nil, fmt.Errorf("vault hmac verification failed")
	}

	block, err := aes.NewCipher(key1)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	plaintext := make([]byte, len(ciphertext))
	stream := cipher.NewCTR(block, iv)
	stream.XORKeyStream(plaintext, ciphertext)

	return pkcs7Unpad(plaintext, aes.BlockSize)
}

func parseVaultEnvelope(data []byte) ([]byte, string, string, string, error) {
	cleanData := bytes.ReplaceAll(data, []byte("\r"), nil)
	lines := bytes.Split(bytes.TrimSpace(cleanData), []byte("\n"))
	if len(lines) < 2 {
		return nil, "", "", "", fmt.Errorf("invalid vault envelope")
	}

	header := strings.Split(strings.TrimSpace(string(lines[0])), ";")
	if len(header) < 3 || header[0] != "$ANSIBLE_VAULT" {
		return nil, "", "", "", fmt.Errorf("invalid vault header")
	}

	vaultID := ""
	if len(header) >= 4 {
		vaultID = strings.TrimSpace(header[3])
	}

	payload := bytes.Join(lines[1:], nil)
	return payload, strings.TrimSpace(header[1]), strings.TrimSpace(header[2]), vaultID, nil
}

func parseVaultPayload(payload []byte) ([]byte, []byte, []byte, error) {
	cleanPayload := bytes.ReplaceAll(payload, []byte("\r"), nil)
	cleanPayload = bytes.ReplaceAll(cleanPayload, []byte("\n"), nil)
	decoded := make([]byte, hex.DecodedLen(len(cleanPayload)))
	n, err := hex.Decode(decoded, bytes.TrimSpace(cleanPayload))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("decode vault payload: %w", err)
	}
	parts := bytes.Split(decoded[:n], []byte("\n"))
	if len(parts) != 3 {
		return nil, nil, nil, fmt.Errorf("invalid vault payload format")
	}
	for i := range parts {
		parts[i] = bytes.TrimSpace(parts[i])
	}

	salt := make([]byte, hex.DecodedLen(len(parts[0])))
	saltLen, err := hex.Decode(salt, parts[0])
	if err != nil {
		return nil, nil, nil, fmt.Errorf("decode vault salt: %w", err)
	}

	ciphertext := make([]byte, hex.DecodedLen(len(parts[2])))
	cipherLen, err := hex.Decode(ciphertext, parts[2])
	if err != nil {
		return nil, nil, nil, fmt.Errorf("decode vault ciphertext: %w", err)
	}

	return ciphertext[:cipherLen], salt[:saltLen], parts[1], nil
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, fmt.Errorf("invalid padded plaintext length")
	}
	padding := int(data[len(data)-1])
	if padding == 0 || padding > blockSize || padding > len(data) {
		return nil, fmt.Errorf("invalid padding")
	}
	for _, b := range data[len(data)-padding:] {
		if int(b) != padding {
			return nil, fmt.Errorf("invalid padding")
		}
	}
	return data[:len(data)-padding], nil
}
