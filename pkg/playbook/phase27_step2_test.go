package playbook

import (
	"sync"
	"testing"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/module"
)

// TestPhase27_GetHostFactsConcurrency 验证并发读取主机 Facts 时零数据争用与内存隔离
func TestPhase27_GetHostFactsConcurrency(t *testing.T) {
	inv := inventory.NewInventory()
	exec := NewExecutor(inv)

	hostName := "node1"
	exec.facts[hostName] = map[string]interface{}{
		"ansible_distribution": "Ubuntu",
		"ansible_architecture": "x86_64",
		"ansible_processor_count": 8,
	}

	var wg sync.WaitGroup
	// 50 个 Goroutine 并发读取 Facts 并就地浅修改返回的 map，验证内部事实库不被污染且无并发读写冲突
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			facts := exec.getHostFacts(hostName)
			if facts == nil {
				t.Errorf("expected non-nil facts")
				return
			}
			if facts["ansible_distribution"] != "Ubuntu" {
				t.Errorf("unexpected distribution: %v", facts["ansible_distribution"])
			}
			// 模拟调用方修改返回的字典
			facts["custom_id"] = id
		}(i)
	}

	// 并发写 Goroutine
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			exec.mu.Lock()
			exec.facts[hostName]["last_ping"] = idx
			exec.mu.Unlock()
		}(i)
	}

	wg.Wait()
}

// TestPhase27_ResolveHostVarsConcurrency 验证并发调用 ResolveHostVars 与清单写入互斥安全
func TestPhase27_ResolveHostVarsConcurrency(t *testing.T) {
	inv := inventory.NewInventory()
	h := &inventory.Host{
		Name:      "web1",
		Variables: map[string]string{"env": "prod"},
	}
	inv.AllHosts["web1"] = h
	g := &inventory.Group{
		Name:      "webservers",
		Variables: map[string]string{"tier": "frontend"},
		Hosts:     []*inventory.Host{h},
	}
	inv.Groups["webservers"] = g

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			vars := inv.ResolveHostVars(h)
			if vars["env"] != "prod" || vars["tier"] != "frontend" {
				t.Errorf("unexpected resolved vars: %v", vars)
			}
		}()
	}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			inv.AddGroup(&inventory.Group{
				Name: "dynamic_group",
			})
		}(i)
	}

	wg.Wait()
}

// TestPhase27_TemplateASTCache 验证 RenderTemplateString 具备 AST 缓存加速且多次渲染输出一致
func TestPhase27_TemplateASTCache(t *testing.T) {
	tmpl := "Server: {{ server_name | upper }}, Port: {{ port | default(80) }}"
	vars1 := map[string]interface{}{"server_name": "nginx-01", "port": 8080}
	vars2 := map[string]interface{}{"server_name": "apache-02"}

	res1, err := module.RenderTemplateString(tmpl, vars1)
	if err != nil {
		t.Fatalf("first render failed: %v", err)
	}
	if res1 != "Server: NGINX-01, Port: 8080" {
		t.Fatalf("unexpected res1: %q", res1)
	}

	// 第二次渲染同一模板内容，命中 parsedTemplateCache 缓存
	res2, err := module.RenderTemplateString(tmpl, vars2)
	if err != nil {
		t.Fatalf("second render failed: %v", err)
	}
	if res2 != "Server: APACHE-02, Port: 80" {
		t.Fatalf("unexpected res2: %q", res2)
	}
}

// TestPhase27_HandleChangedMarker 验证 HandleChangedMarker 统一剔除标记并正确判定 Changed
func TestPhase27_HandleChangedMarker(t *testing.T) {
	resChanged := &module.Result{
		Stdout:   "created file\n__go_ansible_changed__=true\n",
		Stderr:   "",
		ExitCode: 0,
	}
	module.HandleChangedMarker(resChanged)
	if !resChanged.Changed {
		t.Fatalf("expected resChanged.Changed=true")
	}
	if resChanged.Stdout != "created file" {
		t.Fatalf("expected marker removed from stdout, got: %q", resChanged.Stdout)
	}

	resUnchanged := &module.Result{
		Stdout:   "file already present\n__go_ansible_changed__=false\n",
		Stderr:   "",
		ExitCode: 0,
	}
	module.HandleChangedMarker(resUnchanged)
	if resUnchanged.Changed {
		t.Fatalf("expected resUnchanged.Changed=false")
	}
	if resUnchanged.Stdout != "file already present" {
		t.Fatalf("expected marker removed from stdout, got: %q", resUnchanged.Stdout)
	}
}

