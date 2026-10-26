package inventory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseINIHostWithSSHCommonArgs(t *testing.T) {
	inv, err := ParseINI([]byte(`
[web]
app01 ansible_host=10.0.0.10 ansible_ssh_common_args='-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null'
`))
	if err != nil {
		t.Fatalf("ParseINI() error = %v", err)
	}

	host := inv.AllHosts["app01"]
	if host == nil {
		t.Fatal("expected host app01 to exist")
	}
	if host.Address != "10.0.0.10" {
		t.Fatalf("expected address 10.0.0.10, got %q", host.Address)
	}
	if host.SSHCommonArgs != "-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null" {
		t.Fatalf("unexpected SSHCommonArgs: %q", host.SSHCommonArgs)
	}
}

func TestParseYAMLGroupVarsApplySSHCommonArgs(t *testing.T) {
	inv, err := ParseYAML([]byte(`
all:
  children:
    web:
      vars:
        ansible_ssh_common_args: -o StrictHostKeyChecking=no
      hosts:
        app01:
          ansible_host: 10.0.0.10
`))
	if err != nil {
		t.Fatalf("ParseYAML() error = %v", err)
	}

	inv.ApplyGroupVars()

	host := inv.AllHosts["app01"]
	if host == nil {
		t.Fatal("expected host app01 to exist")
	}
	if host.SSHCommonArgs != "-o StrictHostKeyChecking=no" {
		t.Fatalf("unexpected SSHCommonArgs: %q", host.SSHCommonArgs)
	}
}

func TestApplyConnectionVarsOverridesHostFields(t *testing.T) {
	inv := NewInventory()
	host := &Host{
		Name:          "app01",
		Address:       "10.0.0.10",
		Port:          22,
		User:          "deploy",
		SSHCommonArgs: "-o StrictHostKeyChecking=yes",
		Variables:     map[string]string{},
	}
	inv.AllHosts[host.Name] = host

	inv.ApplyConnectionVars(map[string]interface{}{
		"ansible_port":            "2222",
		"ansible_user":            "root",
		"ansible_ssh_common_args": "-o StrictHostKeyChecking=no",
	})

	if host.Port != 2222 {
		t.Fatalf("expected port 2222, got %d", host.Port)
	}
	if host.User != "root" {
		t.Fatalf("expected user root, got %q", host.User)
	}
	if host.SSHCommonArgs != "-o StrictHostKeyChecking=no" {
		t.Fatalf("unexpected SSHCommonArgs: %q", host.SSHCommonArgs)
	}
}

func TestParseFileJSONInventoryWithMetaHostVars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inventory.json")
	content := `{
  "all": {
    "children": ["web"],
    "vars": {
      "ansible_user": "deploy"
    }
  },
  "web": {
    "hosts": ["app01"],
    "vars": {
      "project_env": "test"
    }
  },
  "_meta": {
    "hostvars": {
      "app01": {
        "ansible_host": "10.0.0.10",
        "ansible_port": 2222,
        "service_name": "elite"
      }
    }
  }
}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	inv, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	host := inv.AllHosts["app01"]
	if host == nil {
		t.Fatal("expected host app01 to exist")
	}
	if host.Address != "10.0.0.10" {
		t.Fatalf("expected address 10.0.0.10, got %q", host.Address)
	}
	if host.Port != 2222 {
		t.Fatalf("expected port 2222, got %d", host.Port)
	}
	if host.User != "deploy" {
		t.Fatalf("expected user deploy from all vars, got %q", host.User)
	}
	if host.Variables["service_name"] != "elite" {
		t.Fatalf("expected service_name elite, got %q", host.Variables["service_name"])
	}
	resolved := inv.ResolveHostVars(host)
	if resolved["project_env"] != "test" {
		t.Fatalf("expected project_env test from resolved vars, got %q", resolved["project_env"])
	}
}

func TestParseFileDynamicInventoryList(t *testing.T) {
	script := writeExecutableInventoryScript(t, `#!/bin/sh
if [ "$1" = "--list" ]; then
  cat <<'EOF'
{
  "web": {
    "hosts": ["app01"]
  },
  "_meta": {
    "hostvars": {
      "app01": {
        "ansible_host": "10.0.0.21",
        "ansible_user": "root",
        "role": "api"
      }
    }
  }
}
EOF
  exit 0
fi
exit 1
`)

	inv, err := ParseFile(script)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	host := inv.AllHosts["app01"]
	if host == nil {
		t.Fatal("expected host app01 to exist")
	}
	if host.Address != "10.0.0.21" {
		t.Fatalf("expected address 10.0.0.21, got %q", host.Address)
	}
	if host.User != "root" {
		t.Fatalf("expected user root, got %q", host.User)
	}
	if host.Variables["role"] != "api" {
		t.Fatalf("expected role api, got %q", host.Variables["role"])
	}
}

func TestParseFileDynamicInventoryHostFallback(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "dynamic.log")
	script := writeExecutableInventoryScript(t, `#!/bin/sh
if [ "$1" = "--list" ]; then
  cat <<'EOF'
{
  "web": {
    "hosts": ["app01", "app02"]
  },
  "_meta": {
    "hostvars": {
      "app01": {
        "ansible_host": "10.0.0.31"
      }
    }
  }
}
EOF
  exit 0
fi

if [ "$1" = "--host" ]; then
  echo "$2" >> "`+logPath+`"
  if [ "$2" = "app02" ]; then
    cat <<'EOF'
{
  "ansible_host": "10.0.0.32",
  "ansible_port": 2222,
  "ansible_ssh_common_args": "-o StrictHostKeyChecking=no",
  "env": "prod"
}
EOF
    exit 0
  fi
fi

exit 1
`)

	inv, err := ParseFile(script)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	host := inv.AllHosts["app02"]
	if host == nil {
		t.Fatal("expected host app02 to exist")
	}
	if host.Address != "10.0.0.32" {
		t.Fatalf("expected address 10.0.0.32, got %q", host.Address)
	}
	if host.Port != 2222 {
		t.Fatalf("expected port 2222, got %d", host.Port)
	}
	if host.SSHCommonArgs != "-o StrictHostKeyChecking=no" {
		t.Fatalf("unexpected SSHCommonArgs: %q", host.SSHCommonArgs)
	}
	if host.Variables["env"] != "prod" {
		t.Fatalf("expected env prod, got %q", host.Variables["env"])
	}

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	calls := strings.Fields(string(logData))
	if len(calls) != 1 || calls[0] != "app02" {
		t.Fatalf("expected fallback to request only app02, got %v", calls)
	}
}

func TestInventoryMatchHostsSupportsPatternsAndExclusions(t *testing.T) {
	inv := NewInventory()
	web1 := &Host{Name: "web1", Address: "10.0.0.1"}
	web2 := &Host{Name: "web2", Address: "10.0.0.2"}
	db1 := &Host{Name: "db1", Address: "10.0.0.3"}
	for _, host := range []*Host{web1, web2, db1} {
		inv.AllHosts[host.Name] = host
	}
	inv.Groups["web"] = &Group{Name: "web", Hosts: []*Host{web1, web2}}

	hosts, err := inv.MatchHosts("web,!web2")
	if err != nil {
		t.Fatalf("MatchHosts() error = %v", err)
	}
	if len(hosts) != 1 || hosts[0].Name != "web1" {
		t.Fatalf("expected only web1, got %#v", hosts)
	}

	hosts, err = inv.FilterHosts(inv.GetAllHosts(), "10.0.0.*, !10.0.0.2")
	if err != nil {
		t.Fatalf("FilterHosts() error = %v", err)
	}
	if len(hosts) != 2 || hosts[0].Name != "db1" || hosts[1].Name != "web1" {
		t.Fatalf("unexpected filtered hosts: %#v", hosts)
	}
}

func TestAdvancedHostPatternMatching(t *testing.T) {
	inv := NewInventory()
	w1 := &Host{Name: "web01.bj", Address: "10.0.1.1"}
	w2 := &Host{Name: "web02.sh", Address: "10.0.1.2"}
	w3 := &Host{Name: "web03.bj", Address: "10.0.1.3"}
	db1 := &Host{Name: "db01.bj", Address: "10.0.2.1"}

	for _, h := range []*Host{w1, w2, w3, db1} {
		inv.AllHosts[h.Name] = h
	}
	inv.Groups["web"] = &Group{Name: "web", Hosts: []*Host{w1, w2, w3}}
	inv.Groups["staging"] = &Group{Name: "staging", Hosts: []*Host{w1, w2, db1}}
	inv.Groups["beijing"] = &Group{Name: "beijing", Hosts: []*Host{w1, w3, db1}}

	// 1. 交集匹配: web 组与 beijing 组的交集应该是 web01.bj 和 web03.bj
	hosts, err := inv.MatchHosts("web:&beijing")
	if err != nil {
		t.Fatalf("MatchHosts(web:&beijing) error: %v", err)
	}
	if len(hosts) != 2 || hosts[0].Name != "web01.bj" || hosts[1].Name != "web03.bj" {
		t.Fatalf("expected web01.bj and web03.bj, got %#v", hosts)
	}

	// 2. 正则表达式匹配: ~.*\.sh 应该只匹配 web02.sh
	hosts, err = inv.MatchHosts("~.*\\.sh")
	if err != nil {
		t.Fatalf("MatchHosts(~.*\\.sh) error: %v", err)
	}
	if len(hosts) != 1 || hosts[0].Name != "web02.sh" {
		t.Fatalf("expected web02.sh, got %#v", hosts)
	}

	// 3. 切片匹配: web[0:2] 应该匹配按名称排序的前两台主机: web01.bj 和 web02.sh
	hosts, err = inv.MatchHosts("web[0:2]")
	if err != nil {
		t.Fatalf("MatchHosts(web[0:2]) error: %v", err)
	}
	if len(hosts) != 2 || hosts[0].Name != "web01.bj" || hosts[1].Name != "web02.sh" {
		t.Fatalf("expected web01.bj, web02.sh, got %#v", hosts)
	}

	// 4. 切片单元素: web[-1] 应该匹配最后一台主机 web03.bj
	hosts, err = inv.MatchHosts("web[-1]")
	if err != nil {
		t.Fatalf("MatchHosts(web[-1]) error: %v", err)
	}
	if len(hosts) != 1 || hosts[0].Name != "web03.bj" {
		t.Fatalf("expected web03.bj, got %#v", hosts)
	}

	// 5. 组合表达式: (交集 + 排除): staging:&beijing:!db01.bj 应该只剩下 web01.bj
	hosts, err = inv.MatchHosts("staging:&beijing:!db01.bj")
	if err != nil {
		t.Fatalf("MatchHosts(complex) error: %v", err)
	}
	// 6. 全局切片: all[0:2] 应该匹配按名称排序的前两台主机: db01.bj, web01.bj
	hosts, err = inv.MatchHosts("all[0:2]")
	if err != nil {
		t.Fatalf("MatchHosts(all[0:2]) error: %v", err)
	}
	if len(hosts) != 2 || hosts[0].Name != "db01.bj" || hosts[1].Name != "web01.bj" {
		t.Fatalf("expected db01.bj, web01.bj, got %#v", hosts)
	}
}

func writeExecutableInventoryScript(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "inventory.sh")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func TestParseSourcesMultiFileAndMerge(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "hosts1.ini")
	f2 := filepath.Join(dir, "hosts2.yml")

	if err := os.WriteFile(f1, []byte("[web]\nweb1 ansible_host=10.0.0.1\n[all:vars]\nglobal_var=from_ini\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("all:\n  children:\n    db:\n      hosts:\n        db1:\n          ansible_host: 10.0.0.2\n  vars:\n    db_var: from_yaml\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	inv, err := ParseSources(f1, f2)
	if err != nil {
		t.Fatalf("ParseSources error: %v", err)
	}

	if len(inv.AllHosts) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(inv.AllHosts))
	}
	if inv.AllHosts["web1"] == nil || inv.AllHosts["db1"] == nil {
		t.Fatalf("missing hosts: %#v", inv.AllHosts)
	}
	if inv.Variables["global_var"] != "from_ini" || inv.Variables["db_var"] != "from_yaml" {
		t.Fatalf("variables mismatch: %#v", inv.Variables)
	}
}

func TestParseSourcesDirectory(t *testing.T) {
	dir := t.TempDir()
	invDir := filepath.Join(dir, "inventories")
	if err := os.MkdirAll(invDir, 0o755); err != nil {
		t.Fatal(err)
	}

	f1 := filepath.Join(invDir, "01-web.ini")
	f2 := filepath.Join(invDir, "02-db.ini")
	if err := os.WriteFile(f1, []byte("[web]\nweb1\nweb2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("[db]\ndb1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	inv, err := ParseSources(invDir)
	if err != nil {
		t.Fatalf("ParseSources(dir) error: %v", err)
	}
	if len(inv.AllHosts) != 3 {
		t.Fatalf("expected 3 hosts, got %d", len(inv.AllHosts))
	}
	if inv.Groups["web"] == nil || len(inv.Groups["web"].Hosts) != 2 {
		t.Fatalf("expected 2 web hosts, got %#v", inv.Groups["web"])
	}
	if inv.Groups["db"] == nil || len(inv.Groups["db"].Hosts) != 1 {
		t.Fatalf("expected 1 db host, got %#v", inv.Groups["db"])
	}
}

func TestParseSourcesInlineHosts(t *testing.T) {
	inv, err := ParseSources("192.168.1.10, 192.168.1.20,")
	if err != nil {
		t.Fatalf("ParseSources inline error: %v", err)
	}
	if len(inv.AllHosts) != 2 {
		t.Fatalf("expected 2 inline hosts, got %d", len(inv.AllHosts))
	}
	h1 := inv.AllHosts["192.168.1.10"]
	if h1 == nil || h1.Address != "192.168.1.10" {
		t.Fatalf("expected host 192.168.1.10, got %#v", h1)
	}
}

func TestParseAnsibleConnection(t *testing.T) {
	// Test INI
	iniData := []byte(`
[containers]
c1 ansible_host=my_container ansible_connection=docker
local1 ansible_connection=local
win1 ansible_host=win.example.com ansible_connection=winrm
`)
	inv, err := ParseINI(iniData)
	if err != nil {
		t.Fatalf("ParseINI() error = %v", err)
	}
	if inv.AllHosts["c1"].Connection != "docker" {
		t.Errorf("expected c1 connection to be docker, got %s", inv.AllHosts["c1"].Connection)
	}
	if inv.AllHosts["local1"].Connection != "local" {
		t.Errorf("expected local1 connection to be local, got %s", inv.AllHosts["local1"].Connection)
	}
	if inv.AllHosts["win1"].Connection != "winrm" {
		t.Errorf("expected win1 connection to be winrm, got %s", inv.AllHosts["win1"].Connection)
	}

	// Test YAML
	yamlData := []byte(`
all:
  hosts:
    c2:
      ansible_connection: docker
      ansible_host: nginx_test
`)
	inv2, err := ParseYAML(yamlData)
	if err != nil {
		t.Fatalf("ParseYAML() error = %v", err)
	}
	if inv2.AllHosts["c2"].Connection != "docker" {
		t.Errorf("expected c2 connection to be docker, got %s", inv2.AllHosts["c2"].Connection)
	}
}
