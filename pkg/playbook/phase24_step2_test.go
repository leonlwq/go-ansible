package playbook

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/module"
	"go-ansible/pkg/ssh"
	"go-ansible/pkg/util"
)

// TestPhase24_ExpandPathIntegration 测试全局统一路径展开 util.ExpandPath
func TestPhase24_ExpandPathIntegration(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("unable to get home dir")
	}

	// 1. Tilde expansion
	p1 := util.ExpandPath("~/custom/key.pem")
	if p1 != filepath.Join(home, "custom/key.pem") {
		t.Errorf("expected %q, got %q", filepath.Join(home, "custom/key.pem"), p1)
	}

	// 2. Env expansion
	os.Setenv("TEST_P24_ENV", "/opt/ansible")
	defer os.Unsetenv("TEST_P24_ENV")
	p2 := util.ExpandPath("$TEST_P24_ENV/hosts")
	if p2 != "/opt/ansible/hosts" {
		t.Errorf("expected /opt/ansible/hosts, got %q", p2)
	}
}

// TestPhase24_InventoryUnifiedConnectionMapping 测试 INI 与 YAML 清单解析对连接变量的统一映射
func TestPhase24_InventoryUnifiedConnectionMapping(t *testing.T) {
	// 1. INI host line with connection vars
	iniContent := `
[servers]
db1 ansible_host=10.0.0.2 ansible_user=dbadmin ansible_port=2222 ansible_connection=ssh ansible_become=true ansible_become_user=postgres custom_flag=yes
`
	inv1, err := inventory.ParseINI([]byte(iniContent))
	if err != nil {
		t.Fatalf("ParseINI failed: %v", err)
	}
	h1 := inv1.AllHosts["db1"]
	if h1 == nil {
		t.Fatalf("host db1 not found")
	}
	if h1.Address != "10.0.0.2" || h1.User != "dbadmin" || h1.Port != 2222 || h1.Connection != "ssh" || !h1.Become || h1.BecomeUser != "postgres" {
		t.Errorf("unexpected host fields from INI: %+v", h1)
	}
	if h1.Variables["custom_flag"] != "yes" {
		t.Errorf("expected custom_flag=yes, got %q", h1.Variables["custom_flag"])
	}

	// 2. YAML hosts with connection vars
	yamlContent := `
all:
  hosts:
    db2:
      ansible_host: 10.0.0.3
      ansible_user: dbadmin2
      ansible_port: 2223
      ansible_connection: docker
      ansible_become: yes
      ansible_become_user: root
      custom_flag: "true"
`
	inv2, err := inventory.ParseYAML([]byte(yamlContent))
	if err != nil {
		t.Fatalf("ParseYAML failed: %v", err)
	}
	h2 := inv2.AllHosts["db2"]
	if h2 == nil {
		t.Fatalf("host db2 not found")
	}
	if h2.Address != "10.0.0.3" || h2.User != "dbadmin2" || h2.Port != 2223 || h2.Connection != "docker" || !h2.Become || h2.BecomeUser != "root" {
		t.Errorf("unexpected host fields from YAML: %+v", h2)
	}
	if h2.Variables["custom_flag"] != "true" {
		t.Errorf("expected custom_flag=true, got %q", h2.Variables["custom_flag"])
	}
}

// TestPhase24_DockerConnectionConcurrency 测试 DockerConnection 在并发场景下的线程安全性
func TestPhase24_DockerConnectionConcurrency(t *testing.T) {
	conn := &ssh.DockerConnection{}
	h := &inventory.Host{
		Name:        "c1",
		Address:     "test-container",
		User:        "app",
		Become:      false,
		BecomeUser:  "root",
	}
	conn.SetHost(h)

	var wg sync.WaitGroup
	// 并发读写 host 和 container 属性
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			hCopy := *h
			if idx%2 == 0 {
				hCopy.Become = true
				hCopy.User = fmt.Sprintf("user-%d", idx)
			}
			conn.SetHost(&hCopy)
		}(i)

		go func() {
			defer wg.Done()
			_ = conn.Host()
		}()
	}
	wg.Wait()
}

// TestPhase24_HostVarsSnapshotConcurrentReaders 测试 buildHostVarsSnapshot 多 reader 并发零排队
func TestPhase24_HostVarsSnapshotConcurrentReaders(t *testing.T) {
	inv := inventory.NewInventory()
	for i := 0; i < 50; i++ {
		h := &inventory.Host{
			Name: fmt.Sprintf("node-%02d", i),
			Variables: map[string]string{
				"env": "prod",
			},
		}
		inv.AllHosts[h.Name] = h
	}
	exec := NewExecutor(inv)
	curHost := inv.AllHosts["node-00"]
	curVars := map[string]interface{}{"task_var": "val1"}

	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				snap := exec.buildHostVarsSnapshot(curHost, curVars)
				if len(snap) != 50 {
					t.Errorf("expected 50 hosts in snapshot, got %d", len(snap))
				}
			}
		}()
	}
	wg.Wait()
}

// TestPhase24_MergeModuleResultsAndGetOptionalBoolParam 测试提取后的通用模块辅助函数
func TestPhase24_MergeModuleResultsAndGetOptionalBoolParam(t *testing.T) {
	// 1. GetOptionalBoolParam
	params := map[string]interface{}{
		"b_true":   true,
		"b_false":  false,
		"s_yes":    "yes",
		"s_no":     "no",
		"s_one":    "1",
		"s_zero":   "0",
		"i_one":    1,
		"i_zero":   0,
		"invalid":  "maybe",
	}

	val, ok := module.GetOptionalBoolParam(params, "b_true")
	if !ok || !val {
		t.Errorf("b_true failed: %v, %v", val, ok)
	}
	val, ok = module.GetOptionalBoolParam(params, "s_yes")
	if !ok || !val {
		t.Errorf("s_yes failed: %v, %v", val, ok)
	}
	val, ok = module.GetOptionalBoolParam(params, "s_no")
	if !ok || val {
		t.Errorf("s_no failed: %v, %v", val, ok)
	}
	_, ok = module.GetOptionalBoolParam(params, "non_existent")
	if ok {
		t.Errorf("expected ok=false for non_existent")
	}

	// 2. MergeModuleResults
	base := &module.Result{Stdout: "out1", ExitCode: 0, Changed: false}
	next := &module.Result{Stdout: "out2", ExitCode: 0, Changed: true}
	merged := module.MergeModuleResults(base, next, false)
	if !merged.Changed {
		t.Errorf("expected merged.Changed to be true")
	}
	if merged.Stdout != "out1\nout2" {
		t.Errorf("expected 'out1\\nout2', got %q", merged.Stdout)
	}
}
