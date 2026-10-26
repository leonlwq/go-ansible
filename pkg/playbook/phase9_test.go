package playbook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/inventory"
)

func TestRoleDependenciesExecution(t *testing.T) {
	tempDir := t.TempDir()
	rolesDir := filepath.Join(tempDir, "roles")

	// 1. 创建 common 角色
	commonTasksDir := filepath.Join(rolesDir, "common", "tasks")
	if err := os.MkdirAll(commonTasksDir, 0755); err != nil {
		t.Fatalf("mkdir error: %v", err)
	}
	commonTaskYAML := `
- name: Common task executed
  debug:
    msg: "common_executed"
`
	if err := os.WriteFile(filepath.Join(commonTasksDir, "main.yml"), []byte(commonTaskYAML), 0644); err != nil {
		t.Fatalf("write common tasks: %v", err)
	}

	// 2. 创建 webapp 角色，其 meta/main.yml 声明依赖 common
	webappTasksDir := filepath.Join(rolesDir, "webapp", "tasks")
	webappMetaDir := filepath.Join(rolesDir, "webapp", "meta")
	if err := os.MkdirAll(webappTasksDir, 0755); err != nil {
		t.Fatalf("mkdir error: %v", err)
	}
	if err := os.MkdirAll(webappMetaDir, 0755); err != nil {
		t.Fatalf("mkdir error: %v", err)
	}

	webappMetaYAML := `
dependencies:
  - role: common
`
	if err := os.WriteFile(filepath.Join(webappMetaDir, "main.yml"), []byte(webappMetaYAML), 0644); err != nil {
		t.Fatalf("write webapp meta: %v", err)
	}

	webappTaskYAML := `
- name: Webapp task executed
  debug:
    msg: "webapp_executed"
`
	if err := os.WriteFile(filepath.Join(webappTasksDir, "main.yml"), []byte(webappTaskYAML), 0644); err != nil {
		t.Fatalf("write webapp tasks: %v", err)
	}

	// 3. 构建执行器并运行只引用了 webapp 角色的 Play
	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	e := NewExecutor(inv)
	e.baseDir = tempDir
	e.SetRolePaths([]string{rolesDir})

	falseVal := false
	play := &Play{
		Name:        "Test Role Dependencies",
		Hosts:       "localhost",
		GatherFacts: &falseVal,
		Roles: []interface{}{
			"webapp",
		},
	}

	pb := &Playbook{Plays: []*Play{play}}
	result, err := e.Execute(pb)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	// 验证：依赖的 common 角色任务先被执行，随后 webapp 角色任务被执行
	if len(result.Plays) != 1 {
		t.Fatalf("expected 1 play result, got %d", len(result.Plays))
	}
	hostRes := result.Plays[0].Hosts["localhost"]
	if hostRes == nil || len(hostRes.Tasks) < 2 {
		t.Fatalf("expected at least 2 tasks (common + webapp), got %#v", hostRes)
	}
	if !strings.Contains(hostRes.Tasks[0].Stdout, "common_executed") {
		t.Errorf("expected first task to be common, got %s", hostRes.Tasks[0].Stdout)
	}
	if !strings.Contains(hostRes.Tasks[1].Stdout, "webapp_executed") {
		t.Errorf("expected second task to be webapp, got %s", hostRes.Tasks[1].Stdout)
	}
}

func TestRoleCircularDependencyDetection(t *testing.T) {
	tempDir := t.TempDir()
	rolesDir := filepath.Join(tempDir, "roles")

	// roleA -> roleB
	os.MkdirAll(filepath.Join(rolesDir, "roleA", "meta"), 0755)
	os.MkdirAll(filepath.Join(rolesDir, "roleA", "tasks"), 0755)
	os.WriteFile(filepath.Join(rolesDir, "roleA", "tasks", "main.yml"), []byte("- debug: msg=A\n"), 0644)
	os.WriteFile(filepath.Join(rolesDir, "roleA", "meta", "main.yml"), []byte("dependencies:\n  - roleB\n"), 0644)

	// roleB -> roleA
	os.MkdirAll(filepath.Join(rolesDir, "roleB", "meta"), 0755)
	os.MkdirAll(filepath.Join(rolesDir, "roleB", "tasks"), 0755)
	os.WriteFile(filepath.Join(rolesDir, "roleB", "tasks", "main.yml"), []byte("- debug: msg=B\n"), 0644)
	os.WriteFile(filepath.Join(rolesDir, "roleB", "meta", "main.yml"), []byte("dependencies:\n  - roleA\n"), 0644)

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	e := NewExecutor(inv)
	e.baseDir = tempDir
	e.SetRolePaths([]string{rolesDir})

	play := &Play{
		Hosts: "localhost",
		Roles: []interface{}{"roleA"},
	}

	_, err := e.Execute(&Playbook{Plays: []*Play{play}})
	if err == nil {
		t.Fatalf("expected circular dependency error, got nil")
	}
	if !strings.Contains(err.Error(), "circular role dependency detected") {
		t.Errorf("expected error message to mention circular role dependency, got: %v", err)
	}
}

func TestPasswordLookup(t *testing.T) {
	tempDir := t.TempDir()
	passFile := filepath.Join(tempDir, "vault_pwd.txt")

	e := NewExecutor(inventory.NewInventory())
	e.baseDir = tempDir

	// 1. 首次调用自动生成 24 位密码并写入文件
	res1 := e.resolveString("pass={{ lookup('password', 'vault_pwd.txt length=24') }}", nil)
	pwd1 := strings.TrimPrefix(res1, "pass=")
	if len(pwd1) != 24 {
		t.Errorf("expected 24 chars password, got len %d (%s)", len(pwd1), pwd1)
	}
	if _, err := os.Stat(passFile); err != nil {
		t.Errorf("expected password file to be created: %v", err)
	}

	// 2. 第二次调用相同路径，应读取已有密码
	res2 := e.resolveString("pass={{ lookup('password', 'vault_pwd.txt') }}", nil)
	pwd2 := strings.TrimPrefix(res2, "pass=")
	if pwd2 != pwd1 {
		t.Errorf("expected second call to return same password %s, got %s", pwd1, pwd2)
	}
}

func TestFirstFoundLookup(t *testing.T) {
	tempDir := t.TempDir()
	e := NewExecutor(inventory.NewInventory())
	e.baseDir = tempDir

	targetFile := filepath.Join(tempDir, "config_v2.json")
	if err := os.WriteFile(targetFile, []byte("{}"), 0644); err != nil {
		t.Fatalf("write file error: %v", err)
	}

	expr := "found={{ lookup('first_found', 'non_existent.json,config_v2.json,default.json') }}"
	got := e.resolveString(expr, nil)
	if !strings.HasSuffix(got, "config_v2.json") {
		t.Errorf("expected lookup to find config_v2.json, got: %s", got)
	}
}

func TestAssertModuleQuietAndFailMsg(t *testing.T) {
	e := NewExecutor(inventory.NewInventory())
	vars := map[string]interface{}{
		"k8s_version": "v1.28.0",
		"min_ram_gb":  16,
	}

	// 测试 1: 成功且 quiet=true
	resSuccess := e.executeAssertModule(map[string]interface{}{
		"that": []interface{}{
			"min_ram_gb >= 8",
			"k8s_version == 'v1.28.0'",
		},
		"quiet": true,
	}, vars)
	if resSuccess.Failed {
		t.Errorf("expected assert success, got failed: %s", resSuccess.Message)
	}
	if resSuccess.Stdout != "" {
		t.Errorf("expected empty stdout with quiet=true, got: %s", resSuccess.Stdout)
	}

	// 测试 2: 失败且带变量渲染的 fail_msg
	resFail := e.executeAssertModule(map[string]interface{}{
		"that": []interface{}{
			"min_ram_gb >= 32",
		},
		"fail_msg": "Cluster node requires at least 32GB RAM, current is {{ min_ram_gb }}GB",
	}, vars)
	if !resFail.Failed {
		t.Errorf("expected assert failure")
	}
	if !strings.Contains(resFail.Message, "current is 16GB") {
		t.Errorf("expected fail_msg to be templated with variable, got: %s", resFail.Message)
	}
}
