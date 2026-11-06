package module

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/ssh"
)

func TestKnownHostsModule(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "known_hosts_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	khFile := filepath.Join(tmpDir, "known_hosts")
	client := &ssh.LocalConnection{}
	mod := &KnownHostsModule{}

	testHost := "test.example.com"
	testKey1 := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJ1"
	testKey2 := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJ2"
	rsaKey := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC12345"

	// 1. Validation test
	if err := mod.Validate(map[string]interface{}{}); err == nil {
		t.Fatalf("expected error on missing name")
	}
	if err := mod.Validate(map[string]interface{}{"name": testHost, "state": "present"}); err == nil {
		t.Fatalf("expected error on missing key when state=present")
	}

	// 2. Add entry (present)
	res, err := mod.Execute(client, map[string]interface{}{
		"name":  testHost,
		"key":   testKey1,
		"path":  khFile,
		"state": "present",
		"diff":  true,
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected changed=true on initial add")
	}
	if !strings.Contains(res.Diff, testHost) {
		t.Fatalf("expected diff to contain testHost, got: %s", res.Diff)
	}

	content, err := os.ReadFile(khFile)
	if err != nil {
		t.Fatalf("failed to read known_hosts: %v", err)
	}
	if !strings.Contains(string(content), testHost) || !strings.Contains(string(content), testKey1) {
		t.Fatalf("unexpected file content: %s", string(content))
	}

	// 3. Idempotent check (same parameters)
	res, err = mod.Execute(client, map[string]interface{}{
		"name":  testHost,
		"key":   testKey1,
		"path":  khFile,
		"state": "present",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if res.Changed {
		t.Fatalf("expected changed=false on idempotent run")
	}

	// 4. Update key for existing host and keyType (key rotation)
	res, err = mod.Execute(client, map[string]interface{}{
		"name":  testHost,
		"key":   testKey2,
		"path":  khFile,
		"state": "present",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected changed=true when updating key")
	}

	content, _ = os.ReadFile(khFile)
	if strings.Contains(string(content), testKey1) {
		t.Fatalf("expected old key to be replaced, but found in file: %s", string(content))
	}
	if !strings.Contains(string(content), testKey2) {
		t.Fatalf("expected new key in file: %s", string(content))
	}

	// 5. Add second key of different keyType (ssh-rsa) for same host
	res, err = mod.Execute(client, map[string]interface{}{
		"name":  testHost,
		"key":   rsaKey,
		"path":  khFile,
		"state": "present",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected changed=true when adding new keyType")
	}

	content, _ = os.ReadFile(khFile)
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines in known_hosts, got %d: %s", len(lines), string(content))
	}

	// 6. Check mode test (should report changed without modifying file)
	res, err = mod.Execute(client, map[string]interface{}{
		"name":         "check.example.com",
		"key":          testKey1,
		"path":         khFile,
		"state":        "present",
		"__check_mode": true,
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected changed=true in check mode")
	}
	content, _ = os.ReadFile(khFile)
	if strings.Contains(string(content), "check.example.com") {
		t.Fatalf("file should not have been modified in check mode")
	}

	// 7. Remove specific key (absent with key)
	res, err = mod.Execute(client, map[string]interface{}{
		"name":  testHost,
		"key":   rsaKey,
		"path":  khFile,
		"state": "absent",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected changed=true on removing key")
	}

	content, _ = os.ReadFile(khFile)
	if strings.Contains(string(content), "ssh-rsa") {
		t.Fatalf("expected rsa key to be removed: %s", string(content))
	}
	if !strings.Contains(string(content), testKey2) {
		t.Fatalf("expected ed25519 key to remain: %s", string(content))
	}

	// 8. Remove all keys for host (absent without key)
	res, err = mod.Execute(client, map[string]interface{}{
		"name":  testHost,
		"path":  khFile,
		"state": "absent",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected changed=true on removing all host keys")
	}

	content, _ = os.ReadFile(khFile)
	if strings.TrimSpace(string(content)) != "" {
		t.Fatalf("expected file to be empty, got: %s", string(content))
	}

	// 9. Absent on non-existent host is idempotent
	res, err = mod.Execute(client, map[string]interface{}{
		"name":  testHost,
		"path":  khFile,
		"state": "absent",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if res.Changed {
		t.Fatalf("expected changed=false on removing non-existent host")
	}
}

func TestKnownHostsHashing(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "known_hosts_hash_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	khFile := filepath.Join(tmpDir, "known_hosts")
	client := &ssh.LocalConnection{}
	mod := &KnownHostsModule{}

	testHost := "secure.internal.net"
	testKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJ9 my-comment"

	// 1. Add hashed entry
	res, err := mod.Execute(client, map[string]interface{}{
		"name":      testHost,
		"key":       testKey,
		"path":      khFile,
		"hash_host": true,
		"state":     "present",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected changed=true on hashed add")
	}

	content, err := os.ReadFile(khFile)
	if err != nil {
		t.Fatalf("read file failed: %v", err)
	}
	line := strings.TrimSpace(string(content))
	if !strings.HasPrefix(line, "|1|") {
		t.Fatalf("expected hashed line starting with |1|, got: %s", line)
	}
	if strings.Contains(line, testHost) {
		t.Fatalf("hashed line must not reveal raw hostname: %s", line)
	}

	// 2. Idempotent check with hash_host
	res, err = mod.Execute(client, map[string]interface{}{
		"name":      testHost,
		"key":       testKey,
		"path":      khFile,
		"hash_host": true,
		"state":     "present",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if res.Changed {
		t.Fatalf("expected changed=false on idempotent hashed run")
	}

	// 3. Remove hashed entry by host name
	res, err = mod.Execute(client, map[string]interface{}{
		"name":  testHost,
		"path":  khFile,
		"state": "absent",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected changed=true on removing hashed entry")
	}

	content, _ = os.ReadFile(khFile)
	if strings.TrimSpace(string(content)) != "" {
		t.Fatalf("expected empty file after absent, got: %s", string(content))
	}
}
