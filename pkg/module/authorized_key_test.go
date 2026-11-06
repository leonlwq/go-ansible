package module

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/ssh"
)

func TestAuthorizedKeyValidation(t *testing.T) {
	mod := &AuthorizedKeyModule{}

	if err := mod.Validate(map[string]interface{}{}); err == nil {
		t.Fatal("expected error on empty params")
	}

	if err := mod.Validate(map[string]interface{}{"user": "root"}); err == nil {
		t.Fatal("expected error when key is missing")
	}

	if err := mod.Validate(map[string]interface{}{"user": "root", "key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI... test@host"}); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestAuthorizedKeyPresentAndIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	authKeysPath := filepath.Join(tmpDir, "authorized_keys")

	client := ssh.NewLocalConnection(nil)
	mod := &AuthorizedKeyModule{}

	key1 := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGo4wR6K8t34uBvF9xL7M1Q5zS8vY2pW3eR6tY7uI9oP deploy@company.com"

	// 1. 首次添加：应成功并返回 changed = true
	res, err := mod.Execute(client, map[string]interface{}{
		"user":       "deploy",
		"key":        key1,
		"state":      "present",
		"path":       authKeysPath,
		"manage_dir": true,
	})
	if err != nil {
		t.Fatalf("Execute(present) failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected changed=true on initial key addition, got changed=false")
	}

	// 验证文件内容
	data, err := os.ReadFile(authKeysPath)
	if err != nil {
		t.Fatalf("read authorized_keys: %v", err)
	}
	if !strings.Contains(string(data), key1) {
		t.Fatalf("expected key in file, got: %q", string(data))
	}

	// 2. 幂等性：再次执行相同参数，应返回 changed = false
	res2, err := mod.Execute(client, map[string]interface{}{
		"user":       "deploy",
		"key":        key1,
		"state":      "present",
		"path":       authKeysPath,
		"manage_dir": true,
	})
	if err != nil {
		t.Fatalf("Execute(idempotency) failed: %v", err)
	}
	if res2.Changed {
		t.Fatalf("expected changed=false on second identical run, got changed=true")
	}

	// 3. 添加第二个 key：应追加到文件并返回 changed = true
	key2 := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC3r... admin@jumpbox"
	res3, err := mod.Execute(client, map[string]interface{}{
		"user":  "deploy",
		"key":   key2,
		"state": "present",
		"path":  authKeysPath,
	})
	if err != nil {
		t.Fatalf("Execute(second key) failed: %v", err)
	}
	if !res3.Changed {
		t.Fatalf("expected changed=true when adding second key")
	}

	data3, _ := os.ReadFile(authKeysPath)
	if !strings.Contains(string(data3), key1) || !strings.Contains(string(data3), key2) {
		t.Fatalf("expected both key1 and key2 in file, got: %q", string(data3))
	}
}

func TestAuthorizedKeyAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	authKeysPath := filepath.Join(tmpDir, "authorized_keys")

	key1 := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGo4wR6K8t34uBvF9xL7M1Q5zS8vY2pW3eR6tY7uI9oP deploy@company.com"
	key2 := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC3r... admin@jumpbox"
	initialContent := key1 + "\n" + key2 + "\n"
	if err := os.WriteFile(authKeysPath, []byte(initialContent), 0600); err != nil {
		t.Fatal(err)
	}

	client := ssh.NewLocalConnection(nil)
	mod := &AuthorizedKeyModule{}

	// 1. 删除 key1
	res, err := mod.Execute(client, map[string]interface{}{
		"user":  "deploy",
		"key":   key1,
		"state": "absent",
		"path":  authKeysPath,
	})
	if err != nil {
		t.Fatalf("Execute(absent) failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected changed=true when removing key1")
	}

	data, _ := os.ReadFile(authKeysPath)
	if strings.Contains(string(data), key1) {
		t.Fatalf("key1 was not removed from file: %q", string(data))
	}
	if !strings.Contains(string(data), key2) {
		t.Fatalf("key2 was mistakenly removed: %q", string(data))
	}

	// 2. 幂等性：再次删除已不存在的 key1，应返回 changed = false
	res2, err := mod.Execute(client, map[string]interface{}{
		"user":  "deploy",
		"key":   key1,
		"state": "absent",
		"path":  authKeysPath,
	})
	if err != nil {
		t.Fatalf("Execute(absent second time) failed: %v", err)
	}
	if res2.Changed {
		t.Fatalf("expected changed=false on removing absent key, got changed=true")
	}
}

func TestAuthorizedKeyExclusive(t *testing.T) {
	tmpDir := t.TempDir()
	authKeysPath := filepath.Join(tmpDir, "authorized_keys")

	keyOld1 := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOld1... old1@company.com"
	keyOld2 := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOld2... old2@company.com"
	initialContent := keyOld1 + "\n" + keyOld2 + "\n"
	if err := os.WriteFile(authKeysPath, []byte(initialContent), 0600); err != nil {
		t.Fatal(err)
	}

	client := ssh.NewLocalConnection(nil)
	mod := &AuthorizedKeyModule{}

	newExclusiveKey := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCExclusiveOnly... only@company.com"

	// exclusive=true: 替换为只保留 newExclusiveKey
	res, err := mod.Execute(client, map[string]interface{}{
		"user":      "deploy",
		"key":       newExclusiveKey,
		"state":     "present",
		"exclusive": true,
		"path":      authKeysPath,
	})
	if err != nil {
		t.Fatalf("Execute(exclusive) failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected changed=true on exclusive overwrite")
	}

	data, _ := os.ReadFile(authKeysPath)
	if strings.Contains(string(data), "old1") || strings.Contains(string(data), "old2") {
		t.Fatalf("expected old keys removed, got: %q", string(data))
	}
	if !strings.Contains(string(data), newExclusiveKey) {
		t.Fatalf("expected new key present, got: %q", string(data))
	}
}

func TestAuthorizedKeyCheckAndDiffMode(t *testing.T) {
	tmpDir := t.TempDir()
	authKeysPath := filepath.Join(tmpDir, "authorized_keys")

	client := ssh.NewLocalConnection(nil)
	mod := &AuthorizedKeyModule{}

	key1 := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGo4wR6K8t34uBvF9xL7M1Q5zS8vY2pW3eR6tY7uI9oP deploy@company.com"

	// Check Mode: 不落盘，返回 changed = true 与 diff
	res, err := mod.Execute(client, map[string]interface{}{
		"user":                "deploy",
		"key":                 key1,
		"state":               "present",
		"path":                authKeysPath,
		"_ansible_check_mode": true,
		"_ansible_diff":       true,
	})
	if err != nil {
		t.Fatalf("Execute(check_mode) failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected changed=true in check mode")
	}
	if res.Diff == "" || !strings.Contains(res.Diff, "+"+key1) {
		t.Fatalf("expected diff output in diff mode, got: %q", res.Diff)
	}

	// 确认文件尚未被实际创建
	if _, err := os.Stat(authKeysPath); !os.IsNotExist(err) {
		t.Fatalf("expected authorized_keys file not to be created in check mode")
	}
}
