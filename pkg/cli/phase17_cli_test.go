package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/variable"
)

func TestVaultCLI_MultiVaultIDAndScript_EndToEnd(t *testing.T) {
	tempDir := t.TempDir()

	// 1. 创建普通密码文件 dev_pass.txt
	devPassFile := filepath.Join(tempDir, "dev_pass.txt")
	if err := os.WriteFile(devPassFile, []byte("dev_secret_123\n"), 0600); err != nil {
		t.Fatalf("write dev pass: %v", err)
	}

	// 2. 创建外部凭据脚本 prod_script.sh
	prodScript := filepath.Join(tempDir, "prod_script.sh")
	scriptContent := `#!/bin/sh
if [ "$1" = "--vault-id" ] && [ "$2" = "prod" ]; then
    echo "prod_secret_456"
else
    echo "unknown_secret"
fi
`
	if err := os.WriteFile(prodScript, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("write prod script: %v", err)
	}

	// 3. 创建测试文件
	devFile := filepath.Join(tempDir, "dev_config.yml")
	prodFile := filepath.Join(tempDir, "prod_config.yml")

	if err := os.WriteFile(devFile, []byte("env: dev_environment\n"), 0644); err != nil {
		t.Fatalf("write dev file: %v", err)
	}
	if err := os.WriteFile(prodFile, []byte("env: prod_environment\n"), 0644); err != nil {
		t.Fatalf("write prod file: %v", err)
	}

	// 4. 执行 ansible-vault encrypt dev_config.yml --vault-id dev@dev_pass.txt
	encryptDevCmd := NewVaultCmd("test")
	encryptDevCmd.SetArgs([]string{
		"encrypt", devFile,
		"--vault-id", fmt.Sprintf("dev@%s", devPassFile),
	})
	if err := encryptDevCmd.Execute(); err != nil {
		t.Fatalf("encrypt dev file failed: %v", err)
	}

	// 5. 执行 ansible-vault encrypt prod_config.yml --vault-id prod@prod_script.sh
	encryptProdCmd := NewVaultCmd("test")
	encryptProdCmd.SetArgs([]string{
		"encrypt", prodFile,
		"--vault-id", fmt.Sprintf("prod@%s", prodScript),
	})
	if err := encryptProdCmd.Execute(); err != nil {
		t.Fatalf("encrypt prod file failed: %v", err)
	}

	// 验证文件头
	devData, _ := os.ReadFile(devFile)
	if !strings.Contains(string(devData), "$ANSIBLE_VAULT;1.2;AES256;dev") {
		t.Fatalf("expected dev vault header in %s", devFile)
	}
	prodData, _ := os.ReadFile(prodFile)
	if !strings.Contains(string(prodData), "$ANSIBLE_VAULT;1.2;AES256;prod") {
		t.Fatalf("expected prod vault header in %s", prodFile)
	}

	// 6. 统一指定两个 vault-id 执行 decrypt，同时解密两份文件
	decryptCmd := NewVaultCmd("test")
	decryptCmd.SetArgs([]string{
		"decrypt", devFile, prodFile,
		"--vault-id", fmt.Sprintf("dev@%s", devPassFile),
		"--vault-id", fmt.Sprintf("prod@%s", prodScript),
	})
	if err := decryptCmd.Execute(); err != nil {
		t.Fatalf("decrypt files failed: %v", err)
	}

	devDecrypted, _ := os.ReadFile(devFile)
	if string(devDecrypted) != "env: dev_environment\n" {
		t.Fatalf("unexpected dev decrypted content: %s", string(devDecrypted))
	}
	prodDecrypted, _ := os.ReadFile(prodFile)
	if string(prodDecrypted) != "env: prod_environment\n" {
		t.Fatalf("unexpected prod decrypted content: %s", string(prodDecrypted))
	}
}

func TestPlaybook_MultiVaultIDAndScript_EndToEnd(t *testing.T) {
	tempDir := t.TempDir()

	// 创建密码文件与外部客户端脚本
	devPassFile := filepath.Join(tempDir, "dev.pass")
	_ = os.WriteFile(devPassFile, []byte("dev_pass\n"), 0600)

	prodScript := filepath.Join(tempDir, "vault_keyring.sh")
	scriptContent := `#!/bin/sh
echo "prod_pass"
`
	_ = os.WriteFile(prodScript, []byte(scriptContent), 0755)

	// 创建两个加密变量文件
	devVarsFile := filepath.Join(tempDir, "dev_vars.yml")
	prodVarsFile := filepath.Join(tempDir, "prod_vars.yml")

	devEnc, _ := variable.EncryptVaultWithID([]byte("cluster_name: dev_k8s\n"), "dev_pass", "dev")
	prodEnc, _ := variable.EncryptVaultWithID([]byte("db_endpoint: prod_db.internal\n"), "prod_pass", "prod")

	_ = os.WriteFile(devVarsFile, devEnc, 0644)
	_ = os.WriteFile(prodVarsFile, prodEnc, 0644)

	// 创建 Playbook
	pbFile := filepath.Join(tempDir, "playbook.yml")
	pbContent := fmt.Sprintf(`
- name: test multi vault id
  hosts: localhost
  vars_files:
    - %s
    - %s
  tasks:
    - name: assert vars
      assert:
        that:
          - cluster_name == 'dev_k8s'
          - db_endpoint == 'prod_db.internal'
`, devVarsFile, prodVarsFile)
	_ = os.WriteFile(pbFile, []byte(pbContent), 0644)

	// 创建 inventory
	invFile := filepath.Join(tempDir, "hosts.ini")
	_ = os.WriteFile(invFile, []byte("localhost ansible_connection=local\n"), 0644)

	// 执行 ansible-playbook
	pbCmd := NewPlaybookCmd("test")
	var stdout bytes.Buffer
	pbCmd.SetOut(&stdout)
	pbCmd.SetArgs([]string{
		pbFile,
		"-i", invFile,
		"--vault-id", fmt.Sprintf("dev@%s", devPassFile),
		"--vault-id", fmt.Sprintf("prod@%s", prodScript),
	})

	if err := pbCmd.Execute(); err != nil {
		t.Fatalf("ansible-playbook with multi vault-id failed: %v", err)
	}
}
