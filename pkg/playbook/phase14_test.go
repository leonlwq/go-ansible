package playbook

import (
	"strings"
	"testing"

	"go-ansible/pkg/module"
)

func TestPhase14ModuleRegistration(t *testing.T) {
	registry := module.NewRegistry()

	modules := []string{"sysctl", "hostname", "mount"}
	for _, name := range modules {
		mod, err := registry.Get(name)
		if err != nil || mod == nil {
			t.Fatalf("expected module %s to be registered in NewRegistry()", name)
		}
		if mod.Name() != name {
			t.Fatalf("expected module.Name() == %s, got %s", name, mod.Name())
		}
	}
}

func TestPhase14ModuleDoc(t *testing.T) {
	modules := []string{"sysctl", "hostname", "mount"}
	for _, name := range modules {
		doc, ok := module.GetModuleDoc(name)
		if !ok || doc == nil {
			t.Fatalf("expected doc for module %s", name)
		}
		if doc.Name != name {
			t.Fatalf("doc.Name = %s, expected %s", doc.Name, name)
		}
		if doc.ShortDesc == "" {
			t.Fatalf("doc.ShortDesc should not be empty for %s", name)
		}
		if len(doc.Options) == 0 {
			t.Fatalf("doc.Options should not be empty for %s", name)
		}
		if doc.Examples == "" {
			t.Fatalf("doc.Examples should not be empty for %s", name)
		}
	}
}

func TestPlaybookTaskResolutionPhase14(t *testing.T) {
	exec := &Executor{}
	vars := map[string]interface{}{
		"node_role": "master",
		"node_id":   "01",
		"ip_fwd":    1,
		"data_dev":  "/dev/vdb1",
	}

	// 1. Resolve sysctl params
	sysctlParams := map[string]interface{}{
		"name":   "net.ipv4.ip_forward",
		"value":  "{{ ip_fwd }}",
		"state":  "present",
		"reload": false,
	}
	resolvedSysctl := exec.resolveVariables(sysctlParams, vars)
	if resolvedSysctl["value"] != 1 && resolvedSysctl["value"] != "1" {
		t.Fatalf("unexpected resolved sysctl value: %v", resolvedSysctl["value"])
	}

	// 2. Resolve hostname params
	hostnameParams := map[string]interface{}{
		"name": "k8s-{{ node_role }}-{{ node_id }}",
	}
	resolvedHostname := exec.resolveVariables(hostnameParams, vars)
	if resolvedHostname["name"] != "k8s-master-01" {
		t.Fatalf("unexpected resolved hostname: %v", resolvedHostname["name"])
	}

	// 3. Resolve mount params
	mountParams := map[string]interface{}{
		"path":   "/var/lib/k8s/{{ node_role }}",
		"src":    "{{ data_dev }}",
		"fstype": "ext4",
		"state":  "present",
	}
	resolvedMount := exec.resolveVariables(mountParams, vars)
	if resolvedMount["path"] != "/var/lib/k8s/master" {
		t.Fatalf("unexpected resolved mount path: %v", resolvedMount["path"])
	}
	if resolvedMount["src"] != "/dev/vdb1" {
		t.Fatalf("unexpected resolved mount src: %v", resolvedMount["src"])
	}
}

func TestPlaybookParserPhase14Modules(t *testing.T) {
	playbookYAML := `
- name: Phase 14 Node Initialization Play
  hosts: all
  tasks:
    - name: Enable IP Forwarding and Swap tuning
      sysctl:
        name: net.ipv4.ip_forward
        value: 1
        state: present
        reload: no

    - name: Configure node hostname
      hostname:
        name: k8s-master-01

    - name: Configure data mount in fstab
      mount:
        path: /data
        src: /dev/vdb1
        fstype: ext4
        opts: defaults,noatime
        state: mounted
`

	parser := NewParser("")
	pb, err := parser.Parse([]byte(playbookYAML))
	if err != nil {
		t.Fatalf("parser.Parse() error = %v", err)
	}
	if len(pb.Plays) != 1 {
		t.Fatalf("expected 1 play, got %d", len(pb.Plays))
	}

	tasks := pb.Plays[0].Tasks
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}

	// Task 1: sysctl
	if tasks[0].ModuleName != "sysctl" {
		t.Fatalf("expected task 1 module == 'sysctl', got %s", tasks[0].ModuleName)
	}
	if tasks[0].Params["name"] != "net.ipv4.ip_forward" {
		t.Fatalf("expected task 1 name param, got %v", tasks[0].Params["name"])
	}

	// Task 2: hostname
	if tasks[1].ModuleName != "hostname" {
		t.Fatalf("expected task 2 module == 'hostname', got %s", tasks[1].ModuleName)
	}
	if tasks[1].Params["name"] != "k8s-master-01" {
		t.Fatalf("expected task 2 name param, got %v", tasks[1].Params["name"])
	}

	// Task 3: mount
	if tasks[2].ModuleName != "mount" {
		t.Fatalf("expected task 3 module == 'mount', got %s", tasks[2].ModuleName)
	}
	if tasks[2].Params["path"] != "/data" {
		t.Fatalf("expected task 3 path param, got %v", tasks[2].Params["path"])
	}
	if tasks[2].Params["state"] != "mounted" {
		t.Fatalf("expected task 3 state param, got %v", tasks[2].Params["state"])
	}
	_ = strings.TrimSpace("")
}
