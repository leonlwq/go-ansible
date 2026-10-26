package playbook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/module"
)

func TestInlineTernaryExpression(t *testing.T) {
	exec := &Executor{}

	vars := map[string]interface{}{
		"is_master":  true,
		"is_worker":  false,
		"env":        "prod",
		"load":       85,
		"port":       3000,
		"tier":       "b",
		"is_ha":      true,
		"is_primary": false,
		"fast":       true,
	}

	tests := []struct {
		name     string
		expr     string
		expected interface{}
	}{
		{
			name:     "boolean true branch",
			expr:     "'master' if is_master else 'worker'",
			expected: "master",
		},
		{
			name:     "boolean false branch",
			expr:     "'master' if is_worker else 'worker'",
			expected: "worker",
		},
		{
			name:     "equality condition true",
			expr:     "'prod_cluster' if env == 'prod' else 'dev_cluster'",
			expected: "prod_cluster",
		},
		{
			name:     "equality condition false",
			expr:     "'staging' if env == 'stage' else 'other'",
			expected: "other",
		},
		{
			name:     "greater than condition true",
			expr:     "'high' if load > 80 else 'normal'",
			expected: "high",
		},
		{
			name:     "greater than condition false",
			expr:     "'critical' if load > 90 else 'ok'",
			expected: "ok",
		},
		{
			name:     "is not defined when defined",
			expr:     "8080 if port is not defined else port",
			expected: 3000,
		},
		{
			name:     "is not defined when undefined",
			expr:     "8080 if missing_port is not defined else missing_port",
			expected: 8080,
		},
		{
			name:     "is defined when defined",
			expr:     "port if port is defined else 9090",
			expected: 3000,
		},
		{
			name:     "is defined when undefined",
			expr:     "missing if missing is defined else 9090",
			expected: 9090,
		},
		{
			name:     "chained ternary right-associative middle match",
			expr:     "'alpha' if tier == 'a' else 'beta' if tier == 'b' else 'gamma'",
			expected: "beta",
		},
		{
			name:     "chained ternary right-associative fallback match",
			expr:     "'alpha' if tier == 'a' else 'beta' if tier == 'x' else 'gamma'",
			expected: "gamma",
		},
		{
			name:     "parenthesized inner ternary",
			expr:     "('primary' if is_primary else 'standby') if is_ha else 'single'",
			expected: "standby",
		},
		{
			name:     "numeric ternary result",
			expr:     "100 if fast else 10",
			expected: 100,
		},
		{
			name:     "ternary with pipe filter",
			expr:     "('master' if is_master else 'worker') | upper",
			expected: "MASTER",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := exec.resolveVarExpr(tc.expr, vars)
			if res != tc.expected {
				t.Fatalf("resolveVarExpr(%q) = %v (%T), expected %v (%T)", tc.expr, res, res, tc.expected, tc.expected)
			}
		})
	}
}

func TestResolveStringWithTernary(t *testing.T) {
	exec := &Executor{}
	vars := map[string]interface{}{
		"is_k8s_master": true,
		"env":           "prod",
		"replicas":      3,
	}

	templateStr := "node_role={{ 'control-plane' if is_k8s_master else 'worker' }}, env={{ 'production' if env == 'prod' else 'staging' }}"
	rendered := exec.resolveString(templateStr, vars)
	expected := "node_role=control-plane, env=production"
	if rendered != expected {
		t.Fatalf("resolveString returned %q, expected %q", rendered, expected)
	}
}

func TestTernaryFilter(t *testing.T) {
	exec := &Executor{}

	vars := map[string]interface{}{
		"is_master": true,
		"is_worker": false,
		"empty_val": "",
		"zero_val":  0,
	}

	tests := []struct {
		name     string
		expr     string
		expected interface{}
	}{
		{
			name:     "ternary filter true condition",
			expr:     "is_master | ternary('master_node', 'worker_node')",
			expected: "master_node",
		},
		{
			name:     "ternary filter false condition",
			expr:     "is_worker | ternary('master_node', 'worker_node')",
			expected: "worker_node",
		},
		{
			name:     "ternary filter with undefined and none fallback",
			expr:     "missing_var | ternary('yes', 'no', 'unknown')",
			expected: "unknown",
		},
		{
			name:     "ternary filter truthy empty string is false",
			expr:     "empty_val | ternary('yes', 'no')",
			expected: "no",
		},
		{
			name:     "ternary filter zero is false",
			expr:     "zero_val | ternary('yes', 'no')",
			expected: "no",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := exec.resolveVarExpr(tc.expr, vars)
			if res != tc.expected {
				t.Fatalf("resolveVarExpr(%q) = %v, expected %v", tc.expr, res, tc.expected)
			}
		})
	}
}

func TestDefaultFilterWithBooleanOption(t *testing.T) {
	exec := &Executor{}

	vars := map[string]interface{}{
		"empty_str":   "",
		"false_bool":  false,
		"zero_int":    0,
		"valid_str":   "hello",
		"valid_int":   42,
		"empty_slice": []interface{}{},
		"empty_map":   map[string]interface{}{},
	}

	tests := []struct {
		name     string
		expr     string
		expected interface{}
	}{
		// 默认 boolean=false: 只要变量已定义，空字符串/false/0 均保留
		{
			name:     "default without boolean on empty str preserves empty str",
			expr:     "empty_str | default('fallback')",
			expected: "",
		},
		{
			name:     "default without boolean on false preserves false",
			expr:     "false_bool | default('fallback')",
			expected: false,
		},
		{
			name:     "default without boolean on zero preserves zero",
			expr:     "zero_int | default(100)",
			expected: 0,
		},
		{
			name:     "default without boolean on undefined triggers fallback",
			expr:     "undefined_val | default('fallback')",
			expected: "fallback",
		},
		// 显式 boolean=true: 空字符串/false/0/空列表/空字典均触发回退
		{
			name:     "default boolean=true on empty str triggers fallback",
			expr:     "empty_str | default('fallback', boolean=true)",
			expected: "fallback",
		},
		{
			name:     "default boolean=true on false triggers fallback",
			expr:     "false_bool | default('fallback', boolean=true)",
			expected: "fallback",
		},
		{
			name:     "default boolean=true on zero triggers fallback",
			expr:     "zero_int | default(100, boolean=true)",
			expected: 100,
		},
		{
			name:     "default boolean=true on empty slice triggers fallback",
			expr:     "empty_slice | default('fallback', boolean=true)",
			expected: "fallback",
		},
		{
			name:     "default boolean=true on empty map triggers fallback",
			expr:     "empty_map | default('fallback', boolean=true)",
			expected: "fallback",
		},
		{
			name:     "default boolean=true on valid str preserves value",
			expr:     "valid_str | default('fallback', boolean=true)",
			expected: "hello",
		},
		{
			name:     "default boolean=true on valid int preserves value",
			expr:     "valid_int | default(100, boolean=true)",
			expected: 42,
		},
		{
			name:     "default true positional arg triggers fallback on empty str",
			expr:     "empty_str | default('fallback', true)",
			expected: "fallback",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := exec.resolveVarExpr(tc.expr, vars)
			if res != tc.expected {
				t.Fatalf("resolveVarExpr(%q) = %v, expected %v", tc.expr, res, tc.expected)
			}
		})
	}
}

func TestDefaultChainedWithJSONAndLength(t *testing.T) {
	exec := &Executor{}

	vars := map[string]interface{}{
		"existing_list": []interface{}{"alpha", "beta"},
		"existing_dict": map[string]interface{}{"k1": "v1"},
	}

	// 1. undefined | default({}) | to_json -> "{}"
	resJSON := exec.resolveVarExpr("missing_dict | default({}) | to_json", vars)
	if resJSON != "{}" {
		t.Fatalf("expected \"{}\", got %q", resJSON)
	}

	// 2. undefined | default([]) | length -> 0
	resLen := exec.resolveVarExpr("missing_list | default([]) | length", vars)
	if resLen != 0 {
		t.Fatalf("expected 0, got %v", resLen)
	}

	// 3. existing_list | default([]) | length -> 2
	resExistingLen := exec.resolveVarExpr("existing_list | default([]) | length", vars)
	if resExistingLen != 2 {
		t.Fatalf("expected 2, got %v", resExistingLen)
	}

	// 4. resolveValue preserves typed map from default({})
	valMap := exec.resolveValue("{{ missing_dict | default({}) }}", vars)
	if m, ok := valMap.(map[string]interface{}); !ok || len(m) != 0 {
		t.Fatalf("expected empty map[string]interface{}, got %v (%T)", valMap, valMap)
	}

	// 5. resolveValue preserves typed slice from default([])
	valSlice := exec.resolveValue("{{ missing_list | default([]) }}", vars)
	if s, ok := valSlice.([]interface{}); !ok || len(s) != 0 {
		t.Fatalf("expected empty []interface{}, got %v (%T)", valSlice, valSlice)
	}
}

func TestWhenConditionsWithFiltersAndTernary(t *testing.T) {
	exec := &Executor{}

	vars := map[string]interface{}{
		"is_master": true,
		"is_worker": false,
		"items":     []interface{}{"item1", "item2"},
		"empty_arr": []interface{}{},
		"str_true":  "true",
		"str_false": "false",
		"raw_num":   10,
	}

	tests := []struct {
		name     string
		cond     string
		expected bool
	}{
		{
			name:     "ternary in when comparison true",
			cond:     "('master' if is_master else 'worker') == 'master'",
			expected: true,
		},
		{
			name:     "ternary in when comparison false",
			cond:     "('master' if is_worker else 'worker') == 'master'",
			expected: false,
		},
		{
			name:     "pipe length greater than 0 true",
			cond:     "items | length > 0",
			expected: true,
		},
		{
			name:     "pipe length greater than 0 false",
			cond:     "empty_arr | length > 0",
			expected: false,
		},
		{
			name:     "pipe bool filter true",
			cond:     "str_true | bool",
			expected: true,
		},
		{
			name:     "pipe bool filter false",
			cond:     "str_false | bool",
			expected: false,
		},
		{
			name:     "parenthesized combined condition",
			cond:     "(items | length > 0) and ('master' if is_master else 'worker') == 'master'",
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := exec.evaluateWhen(tc.cond, vars)
			if res != tc.expected {
				t.Fatalf("evaluateWhen(%q) = %v, expected %v", tc.cond, res, tc.expected)
			}
		})
	}
}

func TestLoopWithTernaryAndDefault(t *testing.T) {
	exec := &Executor{}

	vars := map[string]interface{}{
		"master_nodes": []interface{}{"k8s-m1", "k8s-m2"},
		"worker_nodes": []interface{}{"k8s-w1", "k8s-w2", "k8s-w3"},
		"is_master":    true,
	}

	// 1. Loop with inline ternary
	loopTernary := exec.evaluateLoop("{{ master_nodes if is_master else worker_nodes }}", vars)
	if len(loopTernary) != 2 || loopTernary[0] != "k8s-m1" {
		t.Fatalf("evaluateLoop ternary failed: %v", loopTernary)
	}

	// 2. Loop with default filter on missing variable
	loopDefault := exec.evaluateLoop("{{ missing_nodes | default(['default-node']) }}", vars)
	if len(loopDefault) != 1 || loopDefault[0] != "default-node" {
		t.Fatalf("evaluateLoop default failed: %v", loopDefault)
	}
}

func TestJinjaTemplateTernaryAndComparisons(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "jinja_phase13_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	templateContent := `
Role: {{ 'control-plane' if is_master else 'worker' }}
Env: {{ 'production' if env == 'prod' else 'development' }}
TernaryFilter: {{ is_master | ternary('active', 'passive') }}
DefaultBool: {{ empty_var | default('fallback_val', boolean=true) }}
{% if env == 'prod' %}
Running in PRODUCTION cluster
{% else %}
Running in DEV cluster
{% endif %}
`
	srcPath := filepath.Join(tempDir, "test.j2")
	if err := os.WriteFile(srcPath, []byte(templateContent), 0644); err != nil {
		t.Fatalf("failed to write test.j2: %v", err)
	}

	destPath := filepath.Join(tempDir, "rendered.txt")

	// Use TemplateModule directly
	exec := &Executor{baseDir: tempDir}
	vars := map[string]interface{}{
		"is_master": true,
		"env":       "prod",
		"empty_var": "",
	}

	// Test template rendering logic via resolveString or direct template render
	renderedString := exec.resolveString("{{ 'control-plane' if is_master else 'worker' }}", vars)
	if renderedString != "control-plane" {
		t.Fatalf("rendered string expected 'control-plane', got %q", renderedString)
	}

	// Also verify .j2 module file execution
	contentBytes, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("failed to read srcPath: %v", err)
	}

	// Render using module.RenderTemplate
	rendered, err := exec.renderTemplateString(string(contentBytes), vars)
	if err != nil {
		t.Fatalf("renderTemplateString failed: %v", err)
	}

	if !strings.Contains(rendered, "Role: control-plane") {
		t.Errorf("missing 'Role: control-plane', output:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Env: production") {
		t.Errorf("missing 'Env: production', output:\n%s", rendered)
	}
	if !strings.Contains(rendered, "TernaryFilter: active") {
		t.Errorf("missing 'TernaryFilter: active', output:\n%s", rendered)
	}
	if !strings.Contains(rendered, "DefaultBool: fallback_val") {
		t.Errorf("missing 'DefaultBool: fallback_val', output:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Running in PRODUCTION cluster") {
		t.Errorf("missing 'Running in PRODUCTION cluster', output:\n%s", rendered)
	}
	_ = destPath
}

// helper for template rendering test in executor
func (e *Executor) renderTemplateString(tmplContent string, vars map[string]interface{}) (string, error) {
	return module.RenderTemplateString(tmplContent, vars)
}
