package variable

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEvalParsesNumbers(t *testing.T) {
	value, err := Eval("42", nil)
	if err != nil {
		t.Fatalf("Eval() error = %v", err)
	}
	if value != 42 {
		t.Fatalf("expected int 42, got %#v", value)
	}

	value, err = Eval("3.14", nil)
	if err != nil {
		t.Fatalf("Eval() error = %v", err)
	}
	if value != 3.14 {
		t.Fatalf("expected float64 3.14, got %#v", value)
	}
}

func TestDecryptVaultAES256(t *testing.T) {
	vaultData := []byte(`$ANSIBLE_VAULT;1.1;AES256
65613534373065303762363261323634316534656431623062323163616334326632623136313831
3439313832326431386466653438383465313866373266630a626165356364336536396463363933
66363864396630363265306536656135653536323465643832383464633635333862336161646162
3063336562633630300a313861616534323337643539363765656463626136353164336439353963
65376461633630356162313432633131346661643331626235393465343862303065
`)

	plaintext, err := DecryptVault(vaultData, "secret123")
	if err != nil {
		t.Fatalf("DecryptVault() error = %v", err)
	}
	if string(plaintext) != "foo: bar\nport: 8080\n" {
		t.Fatalf("unexpected plaintext: %q", string(plaintext))
	}
}

func TestManagerLoadVarsFileDecryptsVault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vault.yml")
	content := `$ANSIBLE_VAULT;1.1;AES256
65613534373065303762363261323634316534656431623062323163616334326632623136313831
3439313832326431386466653438383465313866373266630a626165356364336536396463363933
66363864396630363265306536656135653536323465643832383464633635333862336161646162
3063336562633630300a313861616534323337643539363765656463626136353164336439353963
65376461633630356162313432633131346661643331626235393465343862303065
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	manager := NewManager()
	manager.SetVaultPassword("secret123")

	vars, err := manager.LoadVarsFile(path)
	if err != nil {
		t.Fatalf("LoadVarsFile() error = %v", err)
	}
	if vars["foo"] != "bar" {
		t.Fatalf("expected foo=bar, got %#v", vars["foo"])
	}
	if vars["port"] != 8080 {
		t.Fatalf("expected port=8080, got %#v", vars["port"])
	}
}

func TestManagerLoadVarsFileDecryptsInlineVaultValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inline-vault.yml")
	content := `db_password: !vault |
  $ANSIBLE_VAULT;1.1;AES256
  65613534373065303762363261323634316534656431623062323163616334326632623136313831
  3439313832326431386466653438383465313866373266630a626165356364336536396463363933
  66363864396630363265306536656135653536323465643832383464633635333862336161646162
  3063336562633630300a313861616534323337643539363765656463626136353164336439353963
  65376461633630356162313432633131346661643331626235393465343862303065
nested:
  tokens:
    - plain
    - !vault |
        $ANSIBLE_VAULT;1.1;AES256
        65613534373065303762363261323634316534656431623062323163616334326632623136313831
        3439313832326431386466653438383465313866373266630a626165356364336536396463363933
        66363864396630363265306536656135653536323465643832383464633635333862336161646162
        3063336562633630300a313861616534323337643539363765656463626136353164336439353963
        65376461633630356162313432633131346661643331626235393465343862303065
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	manager := NewManager()
	manager.SetVaultPassword("secret123")

	vars, err := manager.LoadVarsFile(path)
	if err != nil {
		t.Fatalf("LoadVarsFile(inline vault) error = %v", err)
	}
	if vars["db_password"] != "foo: bar\nport: 8080" {
		t.Fatalf("unexpected db_password: %#v", vars["db_password"])
	}

	nested, ok := vars["nested"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected nested map, got %#v", vars["nested"])
	}
	tokens, ok := nested["tokens"].([]interface{})
	if !ok || len(tokens) != 2 {
		t.Fatalf("expected nested tokens list, got %#v", nested["tokens"])
	}
	if tokens[1] != "foo: bar\nport: 8080" {
		t.Fatalf("unexpected inline vault token: %#v", tokens[1])
	}
}

func TestDecryptVaultRequiresPassword(t *testing.T) {
	_, err := DecryptVault([]byte(`$ANSIBLE_VAULT;1.1;AES256
00
`), "")
	if err == nil {
		t.Fatal("expected empty password to fail")
	}
}

func TestExtractTargetNameFromVarsPath(t *testing.T) {
	tests := []struct {
		baseDir  string
		filePath string
		expected string
		ok       bool
	}{
		{"/playbook/group_vars", "/playbook/group_vars/all.yml", "all", true},
		{"/playbook/group_vars", "/playbook/group_vars/all.yaml", "all", true},
		{"/playbook/group_vars", "/playbook/group_vars/all.json", "all", true},
		{"/playbook/group_vars", "/playbook/group_vars/webservers/main.yml", "webservers", true},
		{"/playbook/group_vars", "/playbook/group_vars/webservers/db.json", "webservers", true},
		{"/playbook/group_vars", "/playbook/group_vars/webservers/nested/vars.yml", "webservers", true},
		{"/playbook/host_vars", "/playbook/host_vars/db01.yml", "db01", true},
		{"/playbook/host_vars", "/playbook/host_vars/db01/mysql.yml", "db01", true},
		{"/playbook/host_vars", "/other/path/db01.yml", "", false},
	}

	for _, tt := range tests {
		got, ok := ExtractTargetNameFromVarsPath(tt.baseDir, tt.filePath)
		if ok != tt.ok || got != tt.expected {
			t.Errorf("ExtractTargetNameFromVarsPath(%q, %q) = (%q, %v), want (%q, %v)",
				tt.baseDir, tt.filePath, got, ok, tt.expected, tt.ok)
		}
	}
}

func TestLoadGroupAndHostVarsSubdirectories(t *testing.T) {
	tmpDir := t.TempDir()
	groupVarsDir := filepath.Join(tmpDir, "group_vars")
	hostVarsDir := filepath.Join(tmpDir, "host_vars")

	// 1. group_vars/webservers/main.yml + group_vars/webservers/extra.yml
	if err := os.MkdirAll(filepath.Join(groupVarsDir, "webservers"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(groupVarsDir, "webservers", "main.yml"), []byte("http_port: 80\napp_name: demo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(groupVarsDir, "webservers", "extra.yml"), []byte("max_clients: 200\napp_name: override_demo\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. host_vars/db01/service.yml
	if err := os.MkdirAll(filepath.Join(hostVarsDir, "db01"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostVarsDir, "db01", "service.yml"), []byte("db_port: 5432\n"), 0644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager()
	if err := mgr.LoadGroupVarsDir(groupVarsDir); err != nil {
		t.Fatalf("LoadGroupVarsDir failed: %v", err)
	}
	if err := mgr.LoadHostVarsDir(hostVarsDir); err != nil {
		t.Fatalf("LoadHostVarsDir failed: %v", err)
	}

	// Verify webservers group vars
	port, ok := mgr.GetGroupVar("webservers", "http_port")
	if !ok || port != 80 {
		t.Fatalf("expected http_port 80, got %v", port)
	}
	clients, ok := mgr.GetGroupVar("webservers", "max_clients")
	if !ok || clients != 200 {
		t.Fatalf("expected max_clients 200, got %v", clients)
	}
	appName, ok := mgr.GetGroupVar("webservers", "app_name")
	if !ok || appName != "demo" {
		t.Fatalf("expected app_name demo (from main.yml after extra.yml), got %v", appName)
	}

	// Verify db01 host vars
	dbPort, ok := mgr.GetHostVar("db01", "db_port")
	if !ok || dbPort != 5432 {
		t.Fatalf("expected db_port 5432, got %v", dbPort)
	}
}
