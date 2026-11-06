package adhoc

import (
	"testing"
)

// 16. 测试 Ad-hoc 字符串模板短路与加速
func TestPhase31_AdhocResolveShortcut(t *testing.T) {
	vars := map[string]interface{}{
		"pkg_name": "nginx",
		"version":  "1.24",
		"cluster":  "production",
	}

	// (a) 不含 {{ 占位符：直接短路返回原字符串
	plain := "systemctl restart docker"
	resolvedPlain := resolveAdhocString(plain, vars)
	if resolvedPlain != plain {
		t.Fatalf("expected plain string %q, got %q", plain, resolvedPlain)
	}

	// (b) 简单单变量提取快速路径
	singleVar := "{{ pkg_name }}"
	resolvedSingle := resolveAdhocString(singleVar, vars)
	if resolvedSingle != "nginx" {
		t.Fatalf("expected 'nginx', got %v", resolvedSingle)
	}

	// (c) 复合模板替换
	multiVar := "install {{ pkg_name }}-{{ version }} in {{ cluster }}"
	resolvedMulti := resolveAdhocString(multiVar, vars)
	expectedMulti := "install nginx-1.24 in production"
	if resolvedMulti != expectedMulti {
		t.Fatalf("expected %q, got %q", expectedMulti, resolvedMulti)
	}
}

