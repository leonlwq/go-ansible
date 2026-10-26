package module

import (
	"strings"
	"testing"
)

func TestURIValidate(t *testing.T) {
	mod := &URIModule{}
	if err := mod.Validate(map[string]interface{}{}); err == nil {
		t.Fatal("expected missing url validation error")
	}
	if err := mod.Validate(map[string]interface{}{
		"url":         "https://example.com/health",
		"status_code": []interface{}{200, "201"},
	}); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if err := mod.Validate(map[string]interface{}{
		"url":         "https://example.com/health",
		"status_code": "ok",
	}); err == nil {
		t.Fatal("expected invalid status_code validation error")
	}
}

func TestNewURISpec(t *testing.T) {
	spec, err := newURISpec(map[string]interface{}{
		"url":            "https://example.com/api",
		"body":           `{"name":"api"}`,
		"headers":        map[string]interface{}{"Content-Type": "application/json"},
		"status_code":    "200,201",
		"return_content": true,
		"validate_certs": "no",
	})
	if err != nil {
		t.Fatalf("newURISpec() error = %v", err)
	}
	if spec.Method != "POST" {
		t.Fatalf("expected implicit POST when body is set, got %s", spec.Method)
	}
	if len(spec.StatusCodes) != 2 || spec.StatusCodes[0] != 200 || spec.StatusCodes[1] != 201 {
		t.Fatalf("unexpected status codes: %#v", spec.StatusCodes)
	}
	if spec.Headers["Content-Type"] != "application/json" {
		t.Fatalf("unexpected headers: %#v", spec.Headers)
	}
	if !spec.ReturnContent || spec.ValidateCerts {
		t.Fatalf("unexpected bool options: return_content=%v validate_certs=%v", spec.ReturnContent, spec.ValidateCerts)
	}
}

func TestBuildURICommand(t *testing.T) {
	cmd := buildURICommand(uriSpec{
		URL:           "https://example.com/api",
		Method:        "POST",
		Headers:       map[string]string{"Content-Type": "application/json"},
		Body:          `{"name":"api"}`,
		StatusCodes:   []int{200, 201},
		Timeout:       10,
		ValidateCerts: false,
	})
	if !strings.Contains(cmd, "curl -sS -L -X 'POST'") {
		t.Fatalf("expected curl command, got %q", cmd)
	}
	if !strings.Contains(cmd, "-H 'Content-Type: application/json'") {
		t.Fatalf("expected header, got %q", cmd)
	}
	if !strings.Contains(cmd, "--data-binary @$body_file") {
		t.Fatalf("expected request body file, got %q", cmd)
	}
	if !strings.Contains(cmd, "-k") {
		t.Fatalf("expected insecure TLS flag, got %q", cmd)
	}
	if !strings.Contains(cmd, "__go_ansible_status__=%{http_code}") {
		t.Fatalf("expected status marker, got %q", cmd)
	}
}

func TestSplitURIOutput(t *testing.T) {
	body, status, ok := splitURIOutput("hello\n__go_ansible_status__=201")
	if !ok || body != "hello" || status != 201 {
		t.Fatalf("unexpected split result: body=%q status=%d ok=%v", body, status, ok)
	}

	if _, _, ok := splitURIOutput("hello"); ok {
		t.Fatal("expected missing status marker to fail")
	}
}

func TestRegistryIncludesURI(t *testing.T) {
	registry := NewRegistry()
	mod, err := registry.Get("uri")
	if err != nil {
		t.Fatalf("registry.Get(uri) error = %v", err)
	}
	if mod.Name() != "uri" {
		t.Fatalf("unexpected module name: %s", mod.Name())
	}
}
