package adhoc

import (
	"strings"
	"testing"

	"go-ansible/pkg/inventory"
)

func TestGetTargetHostsSupportsPatternAndLimit(t *testing.T) {
	inv := inventory.NewInventory()
	web1 := &inventory.Host{Name: "web1", Address: "10.0.0.1"}
	web2 := &inventory.Host{Name: "web2", Address: "10.0.0.2"}
	db1 := &inventory.Host{Name: "db1", Address: "10.0.0.3"}
	for _, host := range []*inventory.Host{web1, web2, db1} {
		inv.AllHosts[host.Name] = host
	}
	inv.Groups["web"] = &inventory.Group{Name: "web", Hosts: []*inventory.Host{web1, web2}}

	executor := NewAdhoc(inv, 2)
	executor.SetLimit("!web2")

	hosts, err := executor.getTargetHosts("web:db1")
	if err != nil {
		t.Fatalf("getTargetHosts() error = %v", err)
	}
	if len(hosts) != 2 || hosts[0].Name != "db1" || hosts[1].Name != "web1" {
		t.Fatalf("unexpected hosts: %#v", hosts)
	}
}

func TestListHostsReturnsNames(t *testing.T) {
	inv := inventory.NewInventory()
	web1 := &inventory.Host{Name: "web1", Address: "10.0.0.1"}
	web2 := &inventory.Host{Name: "web2", Address: "10.0.0.2"}
	inv.AllHosts[web1.Name] = web1
	inv.AllHosts[web2.Name] = web2

	executor := NewAdhoc(inv, 2)
	hosts, err := executor.ListHosts("web*")
	if err != nil {
		t.Fatalf("ListHosts() error = %v", err)
	}
	if len(hosts) != 2 || hosts[0] != "web1" || hosts[1] != "web2" {
		t.Fatalf("unexpected host names: %#v", hosts)
	}
}

func TestResolveParamsForHostMergesInventoryAndExtraVars(t *testing.T) {
	inv := inventory.NewInventory()
	host := &inventory.Host{
		Name:      "web1",
		Address:   "10.0.0.10",
		User:      "deploy",
		Port:      2222,
		Variables: map[string]string{"service_name": "api"},
	}
	inv.AllHosts[host.Name] = host
	inv.Variables["region"] = "cn"

	executor := NewAdhoc(inv, 2)
	executor.SetExtraVars(map[string]interface{}{
		"service_name": "frontend",
		"env":          "prod",
	})

	params := map[string]interface{}{
		"_raw_params": "echo {{ inventory_hostname }} {{ service_name }} {{ region }} {{ env }}",
		"cmd":         "{{ service_name }}",
		"port":        "{{ ansible_port }}",
		"nested": map[string]interface{}{
			"user": "{{ ansible_user }}",
		},
	}

	resolved := executor.resolveParamsForHost(host, "command", params)

	if resolved["_raw_params"] != "echo web1 frontend cn prod" {
		t.Fatalf("unexpected _raw_params: %#v", resolved["_raw_params"])
	}
	if resolved["cmd"] != "frontend" {
		t.Fatalf("unexpected cmd: %#v", resolved["cmd"])
	}
	if resolved["port"] != 2222 {
		t.Fatalf("expected typed port 2222, got %#v", resolved["port"])
	}
	nested, ok := resolved["nested"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected nested map, got %#v", resolved["nested"])
	}
	if nested["user"] != "deploy" {
		t.Fatalf("unexpected nested.user: %#v", nested["user"])
	}
}

func TestResolveParamsForHostAppliesCheckAndDiffFlags(t *testing.T) {
	inv := inventory.NewInventory()
	host := &inventory.Host{Name: "web1", Variables: map[string]string{}}
	inv.AllHosts[host.Name] = host

	executor := NewAdhoc(inv, 2)
	executor.SetCheckMode(true)
	executor.SetDiffMode(true)

	resolved := executor.resolveParamsForHost(host, "lineinfile", map[string]interface{}{
		"path": "/tmp/app.conf",
		"line": "mode=prod",
	})

	if resolved["__check_mode"] != true {
		t.Fatalf("expected __check_mode=true, got %#v", resolved["__check_mode"])
	}
	if resolved["__diff"] != true {
		t.Fatalf("expected __diff=true, got %#v", resolved["__diff"])
	}

	resolved = executor.resolveParamsForHost(host, "shell", map[string]interface{}{
		"_raw_params": "echo hi",
	})
	if _, ok := resolved["__check_mode"]; ok {
		t.Fatalf("did not expect __check_mode for shell: %#v", resolved)
	}
	if _, ok := resolved["__diff"]; ok {
		t.Fatalf("did not expect __diff for shell: %#v", resolved)
	}
}

func TestSetExtraVarsAppliesConnectionOverrides(t *testing.T) {
	inv := inventory.NewInventory()
	host := &inventory.Host{
		Name:      "web1",
		Address:   "10.0.0.10",
		User:      "root",
		Variables: map[string]string{},
	}
	inv.AllHosts[host.Name] = host

	executor := NewAdhoc(inv, 2)
	executor.SetExtraVars(map[string]interface{}{
		"ansible_user": "deploy",
		"ansible_port": 2200,
	})

	if host.User != "deploy" {
		t.Fatalf("expected ansible_user override, got %q", host.User)
	}
	if host.Port != 2200 {
		t.Fatalf("expected ansible_port override, got %d", host.Port)
	}
}

func TestFormatResultIncludesMessageDiffAndData(t *testing.T) {
	result := &Result{
		Hosts: map[string]*HostResult{
			"web2": {
				Changed: true,
				Stdout:  "updated",
			},
			"web1": {
				Changed: false,
				Message: "HTTP 200",
				Diff:    "--- before\n+++ after\n-old\n+new",
				Data: map[string]interface{}{
					"status": 200,
					"body":   "ok",
				},
			},
		},
	}

	output := result.FormatResult()
	if !strings.Contains(output, "web1 | SUCCESS") {
		t.Fatalf("expected web1 status in output: %q", output)
	}
	if !strings.Contains(output, "msg: HTTP 200") {
		t.Fatalf("expected message in output: %q", output)
	}
	if !strings.Contains(output, "diff:") || !strings.Contains(output, "+new") {
		t.Fatalf("expected diff in output: %q", output)
	}
	if !strings.Contains(output, `"status": 200`) || !strings.Contains(output, `"body": "ok"`) {
		t.Fatalf("expected data JSON in output: %q", output)
	}
	if strings.Index(output, "web1 | SUCCESS") > strings.Index(output, "web2 | CHANGED") {
		t.Fatalf("expected hosts sorted in output: %q", output)
	}
}
