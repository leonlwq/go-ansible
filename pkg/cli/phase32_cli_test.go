package cli

import (
	"os"
	"path/filepath"
	"testing"

	"go-ansible/pkg/inventory"
)

func TestPhase32_DocLookupAndConnectionPlugins(t *testing.T) {
	// 1. 测试 lookup 插件列表与详情
	optsLookup := &DocOptions{
		Type:   "lookup",
		Module: "file",
	}
	if err := optsLookup.Run(); err != nil {
		t.Fatalf("DocOptions lookup run failed: %v", err)
	}

	// 2. 测试 lookup 插件列表与 JSON
	optsLookupJSON := &DocOptions{
		Type: "lookup",
		List: true,
		JSON: true,
	}
	if err := optsLookupJSON.Run(); err != nil {
		t.Fatalf("DocOptions lookup json run failed: %v", err)
	}

	// 3. 测试 connection 插件详情
	optsConn := &DocOptions{
		Type:   "connection",
		Module: "ssh",
	}
	if err := optsConn.Run(); err != nil {
		t.Fatalf("DocOptions connection run failed: %v", err)
	}

	// 4. 测试未知插件类型报错
	optsInvalid := &DocOptions{
		Type:   "unknown_type",
		Module: "ssh",
	}
	if err := optsInvalid.Run(); err == nil {
		t.Fatalf("expected error for unknown plugin type, got nil")
	}
}

func TestPhase32_VaultOutputShorthandFlag(t *testing.T) {
	cmd := NewVaultCmd("0.1.0")
	flag := cmd.PersistentFlags().Lookup("output")
	if flag == nil {
		t.Fatalf("expected persistent flag --output on vault cmd")
	}
	if flag.Shorthand != "o" {
		t.Fatalf("expected shorthand -o for --output flag, got %q", flag.Shorthand)
	}
}

func TestPhase32_LoadVarsDirsComplexTypesPreserved(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vars_complex_test_*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	groupVarsDir := filepath.Join(tmpDir, "group_vars")
	if err := os.MkdirAll(groupVarsDir, 0755); err != nil {
		t.Fatalf("mkdir group_vars: %v", err)
	}
	allYml := filepath.Join(groupVarsDir, "all.yml")
	content := `---
http_ports:
  - 80
  - 443
db_config:
  host: "10.0.0.1"
  port: 3306
  ssl: true
`
	if err := os.WriteFile(allYml, []byte(content), 0644); err != nil {
		t.Fatalf("write all.yml: %v", err)
	}

	inv := inventory.NewInventory()
	h := &inventory.Host{
		Name:      "web1",
		Variables: make(map[string]string),
	}
	inv.AllHosts["web1"] = h

	// 调用 loadVarsDirsToInventory 加载
	loadVarsDirsToInventory(inv, nil, tmpDir)

	// 检查 RawVariables 中结构化数据完整性
	rawVars := inv.ResolveHostVarsRaw(h)
	ports, ok := rawVars["http_ports"].([]interface{})
	if !ok {
		t.Fatalf("expected http_ports to be []interface{}, got %T: %v", rawVars["http_ports"], rawVars["http_ports"])
	}
	if len(ports) != 2 {
		t.Fatalf("expected 2 ports, got %d", len(ports))
	}

	dbConf, ok := rawVars["db_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected db_config to be map[string]interface{}, got %T: %v", rawVars["db_config"], rawVars["db_config"])
	}
	if dbConf["host"] != "10.0.0.1" {
		t.Errorf("expected host 10.0.0.1, got %v", dbConf["host"])
	}
}
