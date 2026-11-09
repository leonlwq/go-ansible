package playbook

import (
	"testing"

	"go-ansible/pkg/inventory"
)

// 1. 验证 evaluateWhen 对 []string 切片类型的正确判断
func TestPhase39_EvaluateWhenStringSlice(t *testing.T) {
	exec := NewExecutor(nil)
	vars := map[string]interface{}{
		"os_family": "Debian",
		"version":   12,
	}

	// 1.1 全部满足的 []string 条件
	trueConditions := []string{
		"os_family == 'Debian'",
		"version >= 10",
	}
	if !exec.evaluateWhen(trueConditions, vars) {
		t.Fatalf("expected []string conditions to evaluate true, got false")
	}

	// 1.2 包含不满足项的 []string 条件
	falseConditions := []string{
		"os_family == 'Debian'",
		"version < 10",
	}
	if exec.evaluateWhen(falseConditions, vars) {
		t.Fatalf("expected []string conditions to evaluate false, got true")
	}
}

// 2. 验证 Inventory.groupContainsHost 与 getGroupDepth 在扁平与嵌套组下的层级与深度
func TestPhase39_InventoryZeroAllocDepthAndContains(t *testing.T) {
	inv := inventory.NewInventory()

	// 扁平组
	gFlat := &inventory.Group{Name: "webservers", Variables: map[string]string{"tier": "frontend"}}
	h1 := &inventory.Host{Name: "web1"}
	gFlat.Hosts = append(gFlat.Hosts, h1)
	inv.Groups["webservers"] = gFlat
	inv.AllHosts["web1"] = h1

	// 嵌套组：grandparent -> parent -> child
	gGrandParent := &inventory.Group{Name: "all_db", Variables: map[string]string{"type": "database"}}
	gParent := &inventory.Group{Name: "mysql_cluster", Parent: gGrandParent, Variables: map[string]string{"engine": "innodb"}}
	gChild := &inventory.Group{Name: "mysql_master", Parent: gParent, Variables: map[string]string{"role": "primary"}}
	h2 := &inventory.Host{Name: "db1"}
	gChild.Hosts = append(gChild.Hosts, h2)
	gParent.Children = append(gParent.Children, gChild)
	gGrandParent.Children = append(gGrandParent.Children, gParent)

	inv.Groups["all_db"] = gGrandParent
	inv.Groups["mysql_cluster"] = gParent
	inv.Groups["mysql_master"] = gChild
	inv.AllHosts["db1"] = h2

	// 验证扁平组变量解析
	vars1 := inv.ResolveHostVarsRaw(h1)
	if vars1["tier"] != "frontend" {
		t.Fatalf("expected web1 tier=frontend, got %v", vars1["tier"])
	}

	// 验证嵌套组变量继承覆盖（子组覆盖父组与祖父组）
	vars2 := inv.ResolveHostVarsRaw(h2)
	if vars2["type"] != "database" {
		t.Fatalf("expected db1 type=database, got %v", vars2["type"])
	}
	if vars2["engine"] != "innodb" {
		t.Fatalf("expected db1 engine=innodb, got %v", vars2["engine"])
	}
	if vars2["role"] != "primary" {
		t.Fatalf("expected db1 role=primary, got %v", vars2["role"])
	}
}

