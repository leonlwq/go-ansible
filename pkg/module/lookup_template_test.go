package module

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLookupInJinjaTemplate(t *testing.T) {
	// 1. Setup environment variable and temp file
	os.Setenv("TEST_GO_ANSIBLE_VAR", "hello_antigravity")
	defer os.Unsetenv("TEST_GO_ANSIBLE_VAR")

	tmpDir, err := os.MkdirTemp("", "lookup_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	secretFile := filepath.Join(tmpDir, "secret.key")
	if err := os.WriteFile(secretFile, []byte("super-secret-token-123\n"), 0600); err != nil {
		t.Fatalf("failed to write secret file: %v", err)
	}

	// 2. Test lookup('env', ...)
	tplEnv := "greeting: {{ lookup('env', 'TEST_GO_ANSIBLE_VAR') }}"
	rendered, err := RenderTemplateString(tplEnv, nil)
	if err != nil {
		t.Fatalf("render template failed: %v", err)
	}
	if strings.TrimSpace(rendered) != "greeting: hello_antigravity" {
		t.Fatalf("unexpected rendered env: %s", rendered)
	}

	// 3. Test lookup('env', ...) with filter chain
	tplEnvFilter := "greeting: {{ lookup('env', 'TEST_GO_ANSIBLE_VAR') | upper }}"
	rendered, err = RenderTemplateString(tplEnvFilter, nil)
	if err != nil {
		t.Fatalf("render template failed: %v", err)
	}
	if strings.TrimSpace(rendered) != "greeting: HELLO_ANTIGRAVITY" {
		t.Fatalf("unexpected rendered filtered env: %s", rendered)
	}

	// 4. Test lookup('file', ...)
	tplFile := "key: {{ lookup('file', '" + secretFile + "') }}"
	rendered, err = RenderTemplateString(tplFile, nil)
	if err != nil {
		t.Fatalf("render template failed: %v", err)
	}
	if strings.TrimSpace(rendered) != "key: super-secret-token-123" {
		t.Fatalf("unexpected rendered file: %s", rendered)
	}

	// 5. Test lookup('file', ...) piped with b64encode
	tplFileFilter := "b64: {{ lookup('file', '" + secretFile + "') | b64encode }}"
	rendered, err = RenderTemplateString(tplFileFilter, nil)
	if err != nil {
		t.Fatalf("render template failed: %v", err)
	}
	if !strings.Contains(rendered, "c3VwZXItc2VjcmV0LXRva2VuLTEyMw==") {
		t.Fatalf("unexpected base64 encoded lookup: %s", rendered)
	}

	// 6. Test lookup('pipe', ...)
	tplPipe := "pipe_res: {{ lookup('pipe', 'printf piped_value') }}"
	rendered, err = RenderTemplateString(tplPipe, nil)
	if err != nil {
		t.Fatalf("render template failed: %v", err)
	}
	if strings.TrimSpace(rendered) != "pipe_res: piped_value" {
		t.Fatalf("unexpected pipe output: %s", rendered)
	}

	// 7. Test ExecuteLookup with BaseDir
	relFile := filepath.Join(tmpDir, "rel_config.ini")
	_ = os.WriteFile(relFile, []byte("port=9090\n"), 0644)
	val, err := ExecuteLookup(tmpDir, "file", "rel_config.ini")
	if err != nil {
		t.Fatalf("ExecuteLookup relative file failed: %v", err)
	}
	if fmt.Sprintf("%v", val) != "port=9090" {
		t.Fatalf("expected port=9090, got: %v", val)
	}

	// 8. Test Regex Cache
	pattern := `([a-z]+)-(\d+)`
	res1 := TemplateRegexReplace(pattern, `$1:$2`, "item-12345")
	if res1 != "item:12345" {
		t.Fatalf("unexpected regex replace: %s", res1)
	}
	// Verify it loaded into regexCache
	if _, ok := regexCache.Load(pattern); !ok {
		t.Fatalf("expected pattern %q to be cached in regexCache", pattern)
	}
}
