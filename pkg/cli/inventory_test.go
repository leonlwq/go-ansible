package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	invpkg "go-ansible/pkg/inventory"
	"go-ansible/pkg/playbook"
)

func TestBuildInventoryListDataIncludesMetaAndAllGroup(t *testing.T) {
	inv, err := invpkg.ParseYAML([]byte(`
all:
  children:
    web:
      vars:
        project_env: test
      hosts:
        app01:
          ansible_host: 10.0.0.10
`))
	if err != nil {
		t.Fatalf("ParseYAML() error = %v", err)
	}
	inv.ApplyGroupVars()

	data := buildInventoryListData(inv)

	allGroup, ok := data["all"].(map[string]interface{})
	if !ok {
		t.Fatal("expected all group in inventory list data")
	}
	children, ok := allGroup["children"].([]string)
	if !ok || len(children) != 1 || children[0] != "web" {
		t.Fatalf("expected all.children to contain web, got %#v", allGroup["children"])
	}

	meta, ok := data["_meta"].(map[string]interface{})
	if !ok {
		t.Fatal("expected _meta section")
	}
	hostvars, ok := meta["hostvars"].(map[string]interface{})
	if !ok {
		t.Fatal("expected _meta.hostvars")
	}
	app01, ok := hostvars["app01"].(map[string]interface{})
	if !ok {
		t.Fatal("expected hostvars for app01")
	}
	if app01["ansible_host"] != "10.0.0.10" {
		t.Fatalf("expected ansible_host 10.0.0.10, got %#v", app01["ansible_host"])
	}
	if app01["project_env"] != "test" {
		t.Fatalf("expected project_env test, got %#v", app01["project_env"])
	}
}

func TestBuildInventoryListDataSynthesizesAllGroupForINIInventory(t *testing.T) {
	inv, err := invpkg.ParseINI([]byte(`
[web]
app01 ansible_host=10.0.0.20
`))
	if err != nil {
		t.Fatalf("ParseINI() error = %v", err)
	}
	inv.ApplyGroupVars()

	data := buildInventoryListData(inv)

	allGroup, ok := data["all"].(map[string]interface{})
	if !ok {
		t.Fatal("expected synthesized all group")
	}
	children, ok := allGroup["children"].([]string)
	if !ok || len(children) != 1 || children[0] != "web" {
		t.Fatalf("expected synthesized all.children to contain web, got %#v", allGroup["children"])
	}

	ungrouped, ok := data["ungrouped"].(map[string]interface{})
	if !ok {
		t.Fatal("expected ungrouped group")
	}
	hosts, ok := ungrouped["hosts"].([]string)
	if !ok {
		t.Fatalf("expected ungrouped hosts list, got %#v", ungrouped["hosts"])
	}
	if len(hosts) != 0 {
		t.Fatalf("expected no ungrouped hosts, got %#v", hosts)
	}
}

func TestBuildInventoryHostDataIncludesConnectionAndResolvedVars(t *testing.T) {
	inv, err := invpkg.ParseYAML([]byte(`
all:
  vars:
    ansible_user: deploy
  children:
    web:
      vars:
        project_env: prod
      hosts:
        app01:
          ansible_host: 10.0.0.30
          ansible_port: 2222
          ansible_ssh_common_args: -o StrictHostKeyChecking=no
          role: api
`))
	if err != nil {
		t.Fatalf("ParseYAML() error = %v", err)
	}
	inv.ApplyGroupVars()

	host := inv.AllHosts["app01"]
	if host == nil {
		t.Fatal("expected host app01")
	}

	data := buildInventoryHostData(inv, host)
	if data["ansible_host"] != "10.0.0.30" {
		t.Fatalf("expected ansible_host 10.0.0.30, got %#v", data["ansible_host"])
	}
	if data["ansible_port"] != 2222 {
		t.Fatalf("expected ansible_port 2222, got %#v", data["ansible_port"])
	}
	if data["ansible_user"] != "deploy" {
		t.Fatalf("expected ansible_user deploy, got %#v", data["ansible_user"])
	}
	if data["ansible_ssh_common_args"] != "-o StrictHostKeyChecking=no" {
		t.Fatalf("unexpected ansible_ssh_common_args: %#v", data["ansible_ssh_common_args"])
	}
	if data["project_env"] != "prod" {
		t.Fatalf("expected project_env prod, got %#v", data["project_env"])
	}
	if data["role"] != "api" {
		t.Fatalf("expected role api, got %#v", data["role"])
	}
}

func TestPrintPlaybookHosts(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	oldStdout := os.Stdout
	os.Stdout = writer

	printPlaybookHosts([]playbook.ListedPlayHosts{
		{Name: "deploy", Hosts: "web,!web2", Items: []string{"web1"}},
	})

	writer.Close()
	os.Stdout = oldStdout
	outputBytes, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	output := string(outputBytes)
	if !strings.Contains(output, "playbook: deploy") || !strings.Contains(output, "pattern: web,!web2") || !strings.Contains(output, "hosts (1):") || !strings.Contains(output, "web1") {
		t.Fatalf("unexpected list-hosts output: %q", output)
	}
}

func TestPrintInventoryGraph(t *testing.T) {
	inv := invpkg.NewInventory()
	inv.Groups["web"] = &invpkg.Group{
		Name:  "web",
		Hosts: []*invpkg.Host{{Name: "web1"}},
	}
	inv.AllHosts["web1"] = &invpkg.Host{Name: "web1"}

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	oldStdout := os.Stdout
	os.Stdout = writer

	printInventoryGraph(inv)

	writer.Close()
	os.Stdout = oldStdout
	outputBytes, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	output := string(outputBytes)
	if !strings.Contains(output, "@all:") || !strings.Contains(output, "@web:") || !strings.Contains(output, "web1") {
		t.Fatalf("unexpected graph output: %q", output)
	}
}

func TestWriteYAML(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	oldStdout := os.Stdout
	os.Stdout = writer

	writeYAML(map[string]interface{}{"env": "prod", "port": 8080})

	writer.Close()
	os.Stdout = oldStdout
	outputBytes, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	output := string(outputBytes)
	if !strings.Contains(output, "env: prod") || !strings.Contains(output, "port: 8080") {
		t.Fatalf("unexpected yaml output: %q", output)
	}
}

func TestPrintInventoryGraphWithVars(t *testing.T) {
	inv := invpkg.NewInventory()
	inv.Groups["db"] = &invpkg.Group{
		Name:      "db",
		Variables: map[string]string{"cluster": "pg"},
		Hosts: []*invpkg.Host{
			{Name: "db1", Variables: map[string]string{"role": "master"}},
		},
	}
	inv.AllHosts["db1"] = inv.Groups["db"].Hosts[0]

	var buf bytes.Buffer
	printInventoryGraphTo(&buf, inv, true)
	out := buf.String()
	if !strings.Contains(out, "@db: {cluster=pg}") {
		t.Fatalf("expected group vars in graph: %s", out)
	}
	if !strings.Contains(out, "db1 {cluster=pg, role=master}") {
		t.Fatalf("expected host resolved vars in graph: %s", out)
	}
}

func TestInventoryOutputToFile(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "out.json")
	invFile := filepath.Join(dir, "hosts.ini")
	_ = os.WriteFile(invFile, []byte("[web]\nweb1\n"), 0644)

	opts := &InventoryOptions{
		InventoryPath: invFile,
		List:          true,
		Output:        outFile,
	}
	if err := opts.Run(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"web1"`) {
		t.Fatalf("expected output file to contain host web1: %s", string(data))
	}
}
