package module

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/ssh"
)

func TestPhase30_CopyPermissionsOnContentMatch(t *testing.T) {
	tempDir := t.TempDir()
	srcFile := filepath.Join(tempDir, "source.txt")
	destFile := filepath.Join(tempDir, "dest.txt")

	content := []byte("hello idempotent world")
	if err := os.WriteFile(srcFile, content, 0644); err != nil {
		t.Fatalf("failed to write src: %v", err)
	}
	// Destination initially has identical content and 0644 permission
	if err := os.WriteFile(destFile, content, 0644); err != nil {
		t.Fatalf("failed to write dest: %v", err)
	}

	conn := ssh.NewLocalConnection(&inventory.Host{Name: "localhost"})
	mod := &CopyModule{}

	// Request mode 0600 on destFile
	params := map[string]interface{}{
		"src":  srcFile,
		"dest": destFile,
		"mode": "0600",
	}

	res, err := mod.Execute(conn, params)
	if err != nil {
		t.Fatalf("copy execute error: %v", err)
	}
	if !res.Changed {
		t.Errorf("expected Changed == true when file permissions change even if content matches")
	}

	fi, err := os.Stat(destFile)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("expected destFile mode 0600, got %o", fi.Mode().Perm())
	}

	// Run again with mode 0600: content and mode now both match, should report Changed == false
	res2, err := mod.Execute(conn, params)
	if err != nil {
		t.Fatalf("second copy execute error: %v", err)
	}
	if res2.Changed {
		t.Errorf("expected Changed == false on second run when content and permissions already match")
	}
}

func TestPhase30_TemplatePermissionsOnContentMatch(t *testing.T) {
	tempDir := t.TempDir()
	srcFile := filepath.Join(tempDir, "template.j2")
	destFile := filepath.Join(tempDir, "rendered.txt")

	content := []byte("server_port: {{ port }}")
	if err := os.WriteFile(srcFile, content, 0644); err != nil {
		t.Fatalf("failed to write src: %v", err)
	}
	// Initial rendered destination with 0644
	if err := os.WriteFile(destFile, []byte("server_port: 8080"), 0644); err != nil {
		t.Fatalf("failed to write dest: %v", err)
	}

	conn := ssh.NewLocalConnection(&inventory.Host{Name: "localhost"})
	mod := &TemplateModule{}

	params := map[string]interface{}{
		"src":  srcFile,
		"dest": destFile,
		"mode": "0600",
		"port": 8080,
	}

	res, err := mod.Execute(conn, params)
	if err != nil {
		t.Fatalf("template execute error: %v", err)
	}
	if !res.Changed {
		t.Errorf("expected Changed == true when mode changed even if rendered template matched")
	}

	fi, err := os.Stat(destFile)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("expected rendered.txt mode 0600, got %o", fi.Mode().Perm())
	}
}

func TestPhase30_MandatoryFilter(t *testing.T) {
	// Nil value should return error
	_, err := TemplateMandatory(nil)
	if err == nil {
		t.Errorf("expected error for mandatory filter on nil value")
	}

	// Empty string should return error
	_, err = TemplateMandatory("")
	if err == nil {
		t.Errorf("expected error for mandatory filter on empty string")
	}

	// Custom message error
	_, err = TemplateMandatory(nil, "Custom error: variable is required!")
	if err == nil || err.Error() != "Custom error: variable is required!" {
		t.Errorf("expected custom error message, got: %v", err)
	}

	// Valid value should pass through
	val, err := TemplateMandatory("valid-secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "valid-secret" {
		t.Errorf("expected 'valid-secret', got %v", val)
	}

	// ApplyFilter testing
	_, _, err = ApplyFilter("mandatory", nil)
	if err == nil {
		t.Errorf("expected ApplyFilter mandatory on nil to fail")
	}
	resVal, ok, err := ApplyFilter("mandatory", "hello")
	if err != nil || !ok || resVal != "hello" {
		t.Errorf("expected ApplyFilter mandatory on 'hello' to succeed, got %v, %v, %v", resVal, ok, err)
	}
}

func TestPhase30_RegexReplaceBackreferences(t *testing.T) {
	// Python \1 backreference
	res1 := TemplateRegexReplace(`version-([0-9.]+)-release`, `v\1`, "version-1.2.3-release")
	if res1 != "v1.2.3" {
		t.Errorf("expected 'v1.2.3', got %q", res1)
	}

	// Python \g<1> backreference
	res2 := TemplateRegexReplace(`item_([0-9]+)_done`, `id: \g<1>`, "item_456_done")
	if res2 != "id: 456" {
		t.Errorf("expected 'id: 456', got %q", res2)
	}

	// Native Go $1 backreference still works
	res3 := TemplateRegexReplace(`([0-9]+)`, `num: $1`, "foo 123 bar")
	if res3 != "foo num: 123 bar" {
		t.Errorf("expected 'foo num: 123 bar', got %q", res3)
	}

	// Via ApplyFilter
	res4, ok, err := ApplyFilter(`regex_replace('^node-(\d+)', 'host-\1')`, "node-42")
	if err != nil || !ok || res4 != "host-42" {
		t.Errorf("expected ApplyFilter regex_replace 'host-42', got %v, %v, %v", res4, ok, err)
	}
}

func TestPhase30_SplitFilterWhitespace(t *testing.T) {
	// ApplyFilter split without arguments splits by whitespace
	res, ok, err := ApplyFilter("split", "alpha   bravo\tcharlie\n\ndelta")
	if err != nil || !ok {
		t.Fatalf("split filter failed: %v, ok: %v", err, ok)
	}
	expected := []string{"alpha", "bravo", "charlie", "delta"}
	if !reflect.DeepEqual(res, expected) {
		t.Errorf("expected %v, got %v", expected, res)
	}

	// ApplyFilter split with explicit delimiter
	res2, ok2, err := ApplyFilter("split(':')", "a:b:c")
	if err != nil || !ok2 {
		t.Fatalf("split filter with delim failed: %v, ok: %v", err, ok2)
	}
	expected2 := []string{"a", "b", "c"}
	if !reflect.DeepEqual(res2, expected2) {
		t.Errorf("expected %v, got %v", expected2, res2)
	}

	// templateFuncMap split
	splitFn, exists := templateFuncMap()["split"].(func(...interface{}) []string)
	if !exists {
		t.Fatalf("split function not found in templateFuncMap")
	}
	gotList := splitFn("foo   bar\t baz")
	expectedList := []string{"foo", "bar", "baz"}
	if !reflect.DeepEqual(gotList, expectedList) {
		t.Errorf("expected %v, got %v", expectedList, gotList)
	}
}
