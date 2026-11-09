package module

import (
	"strings"
	"testing"
)

// TestPhase36_URIModuleAuthAndBodyFormat 验证 uri 模块 Basic 认证、证书与 body_format: json 自动请求头
func TestPhase36_URIModuleAuthAndBodyFormat(t *testing.T) {
	params := map[string]interface{}{
		"url":              "https://api.example.com/v1/users",
		"method":           "POST",
		"url_username":     "admin",
		"url_password":     "secret123",
		"force_basic_auth": true,
		"body_format":      "json",
		"body": map[string]interface{}{
			"role": "ops",
		},
		"client_cert": "/etc/ssl/cert.pem",
		"client_key":  "/etc/ssl/key.pem",
	}

	spec, err := newURISpec(params)
	if err != nil {
		t.Fatalf("newURISpec failed: %v", err)
	}

	if spec.URLUsername != "admin" || spec.URLPassword != "secret123" || !spec.ForceBasicAuth {
		t.Fatalf("unexpected auth in spec: %#v", spec)
	}
	if spec.Headers["Content-Type"] != "application/json" {
		t.Fatalf("expected auto-injected Content-Type header, got: %#v", spec.Headers)
	}
	if !strings.Contains(spec.Body, `"role":"ops"`) {
		t.Fatalf("expected json serialized body, got: %s", spec.Body)
	}

	cmd := buildURICommand(spec)
	if !strings.Contains(cmd, "--basic") || !strings.Contains(cmd, "-u 'admin:secret123'") {
		t.Fatalf("expected curl command with basic auth, got:\n%s", cmd)
	}
	if !strings.Contains(cmd, "--cert '/etc/ssl/cert.pem'") || !strings.Contains(cmd, "--key '/etc/ssl/key.pem'") {
		t.Fatalf("expected curl command with mTLS cert/key, got:\n%s", cmd)
	}
}

// TestPhase36_ParseHeadersParam 验证 ParseHeadersParam 支持 map 与行切分文本
func TestPhase36_ParseHeadersParam(t *testing.T) {
	// 1. map[string]interface{}
	h1 := ParseHeadersParam(map[string]interface{}{
		"Authorization": "Bearer token123",
		"X-RateLimit":   100,
	})
	if h1["Authorization"] != "Bearer token123" || h1["X-RateLimit"] != "100" {
		t.Fatalf("unexpected map[string]interface headers: %#v", h1)
	}

	// 2. string 多行切分
	rawStr := "Authorization: Basic admin\nContent-Type: application/xml\n"
	h2 := ParseHeadersParam(rawStr)
	if h2["Authorization"] != "Basic admin" || h2["Content-Type"] != "application/xml" {
		t.Fatalf("unexpected string headers: %#v", h2)
	}
}

// TestPhase36_CronEnvVariables 验证 cron 模块 env: yes 环境变量创建、修改与删除
func TestPhase36_CronEnvVariables(t *testing.T) {
	mod := &CronModule{}

	// 1. 验证 env 参数检验
	if err := mod.Validate(map[string]interface{}{"env": true}); err == nil {
		t.Fatal("expected error when name is missing for env")
	}
	if err := mod.Validate(map[string]interface{}{"env": true, "name": "PATH"}); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	// 2. 初始添加环境变量 PATH
	initial := "# Existing job\n0 * * * * /bin/run.sh\n"
	out1, changed1 := updateCrontabContent(initial, "PATH", "", "PATH=/usr/local/bin:/usr/bin", "present", false)
	if !changed1 || !strings.Contains(out1, "PATH=/usr/local/bin:/usr/bin") || !strings.Contains(out1, "# Ansible: PATH") {
		t.Fatalf("expected added PATH env: changed=%v, out:\n%s", changed1, out1)
	}

	// 3. 幂等测试：再次以相同内容添加，changed 必须为 false
	out2, changed2 := updateCrontabContent(out1, "PATH", "", "PATH=/usr/local/bin:/usr/bin", "present", false)
	if changed2 || out2 != out1 {
		t.Fatalf("expected idempotent cron env: changed=%v, out:\n%s", changed2, out2)
	}

	// 4. 修改环境变量值
	out3, changed3 := updateCrontabContent(out2, "PATH", "", "PATH=/usr/local/bin:/bin", "present", false)
	if !changed3 || !strings.Contains(out3, "PATH=/usr/local/bin:/bin") || strings.Contains(out3, "/usr/bin") {
		t.Fatalf("expected updated PATH env: changed=%v, out:\n%s", changed3, out3)
	}

	// 5. 删除环境变量 (state: absent)
	out4, changed4 := updateCrontabContent(out3, "PATH", "", "", "absent", false)
	if !changed4 || strings.Contains(out4, "PATH=") || strings.Contains(out4, "Ansible: PATH") {
		t.Fatalf("expected removed PATH env: changed=%v, out:\n%s", changed4, out4)
	}
	if !strings.Contains(out4, "/bin/run.sh") {
		t.Fatalf("expected existing job to be preserved: out:\n%s", out4)
	}
}

