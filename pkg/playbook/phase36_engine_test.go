package playbook

import (
	"testing"
)

// TestPhase36_VersionConditionTests 验证 is version 与 is version_compare 条件测试
func TestPhase36_VersionConditionTests(t *testing.T) {
	e := &Executor{}
	vars := map[string]interface{}{
		"ansible_distribution_version":       "22.04",
		"ansible_distribution_major_version": "22",
		"kernel_version":                     "5.15.0-generic",
		"app_version":                        "1.0.0-rc1",
	}

	tests := []struct {
		cond     string
		expected bool
	}{
		{"ansible_distribution_version is version('20.04', '>=')", true},
		{"ansible_distribution_version is version('24.04', '<')", true},
		{"ansible_distribution_version is not version('22.04', '!=')", true},
		{"ansible_distribution_major_version is version('22', '==')", true},
		{"ansible_distribution_major_version is version_compare('20', '>')", true},
		{"kernel_version is version('5.10', '>=')", true},
		{"app_version is version('1.0.0', '<')", true},
		{"ansible_distribution_version is version('30.00', '>=')", false},
	}

	for _, tt := range tests {
		got := e.evaluateWhen(tt.cond, vars)
		if got != tt.expected {
			t.Errorf("evaluateWhen(%q) = %v, expected %v", tt.cond, got, tt.expected)
		}
	}
}

// TestPhase36_QuoteAwareComparisonOperators 验证顶层引号感知比较操作符切分
func TestPhase36_QuoteAwareComparisonOperators(t *testing.T) {
	e := &Executor{}
	vars := map[string]interface{}{
		"status": "ready",
		"msg":    "a>b",
		"token":  "==abc==",
	}

	tests := []struct {
		cond     string
		expected bool
	}{
		{`status != "=="`, true},
		{`status == "ready"`, true},
		{`msg == "a>b"`, true},
		{`msg != "a<b"`, true},
		{`token == "==abc=="`, true},
		{`token != "=="`, true},
	}

	for _, tt := range tests {
		got := e.evaluateWhen(tt.cond, vars)
		if got != tt.expected {
			t.Errorf("evaluateWhen(%q) = %v, expected %v", tt.cond, got, tt.expected)
		}
	}
}

// TestPhase36_LookupVarsLinesSequence 验证 lookup vars, lines, sequence 插件
func TestPhase36_LookupVarsLinesSequence(t *testing.T) {
	e := &Executor{}
	vars := map[string]interface{}{
		"env_name": "prod",
		"db_prod":  "10.0.0.1",
		"nested": map[string]interface{}{
			"host": "cluster-master",
		},
	}

	// 1. lookup('vars', ...)
	tmpl1 := "{{ lookup('vars', 'db_' ~ env_name) }}"
	res1 := e.resolveString(tmpl1, vars)
	if res1 != "10.0.0.1" {
		t.Fatalf("lookup('vars') failed: res=%q", res1)
	}

	// 2. lookup('vars', ...) 嵌套点号
	tmpl2 := "{{ lookup('vars', 'nested.host') }}"
	res2 := e.resolveString(tmpl2, vars)
	if res2 != "cluster-master" {
		t.Fatalf("lookup('vars' nested) failed: res=%q", res2)
	}

	// 3. lookup('sequence', ...)
	tmpl3 := "{{ lookup('sequence', 'count=3 format=web%02d') }}"
	res3 := e.resolveString(tmpl3, vars)
	if res3 != "[web01 web02 web03]" {
		t.Fatalf("lookup('sequence') failed: res=%q", res3)
	}

	// 4. lookup('lines', ...)
	tmpl4 := "{{ lookup('lines', 'printf \"line1\\nline2\"') }}"
	res4 := e.resolveString(tmpl4, vars)
	if res4 != "[line1 line2]" {
		t.Fatalf("lookup('lines') failed: res=%q", res4)
	}
}

// TestPhase36_VersionFilter 验证 version 与 version_compare 过滤器
func TestPhase36_VersionFilter(t *testing.T) {
	e := &Executor{}
	vars := map[string]interface{}{
		"ver": "8.10",
	}

	tmpl := "{{ ver | version_compare('8.2', '>') }}"
	res := e.resolveString(tmpl, vars)
	if res != "true" {
		t.Fatalf("version_compare filter failed: res=%q", res)
	}

	tmpl2 := "{{ ver | version('9.0', '<') }}"
	res2 := e.resolveString(tmpl2, vars)
	if res2 != "true" {
		t.Fatalf("version filter failed: res=%q", res2)
	}
}
