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
