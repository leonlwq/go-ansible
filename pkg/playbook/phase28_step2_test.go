package playbook

import (
	"sync"
	"testing"
	"time"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/ssh"
)

// 1. 测试 multi-play playbook 中 currentPlayDefaults 在 play 间的隔离性
func TestPhase28_CurrentPlayDefaultsIsolationAcrossPlays(t *testing.T) {
	inv := inventory.NewInventory()
	inv.AllHosts["localhost"] = &inventory.Host{Name: "localhost", Connection: "local"}
	executor := NewExecutor(inv)

	// 构造双 play 的 playbook：Play 1 具备 defaults，Play 2 无 defaults
	play1 := &Play{
		Name:  "Play 1 with role defaults",
		Hosts: "localhost",
		Tasks: []*Task{
			{
				Name:       "Check p1 default",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "in play 1",
				},
			},
		},
	}

	play2 := &Play{
		Name:  "Play 2 without role defaults",
		Hosts: "localhost",
		Tasks: []*Task{
			{
				Name:       "Check p2 vars",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "in play 2",
				},
			},
		},
	}

	// 模拟 Play 1 执行前注入的 role defaults
	executor.currentPlayDefaults = map[string]interface{}{
		"role_default_var": "val1",
	}

	// 执行 Play 2
	_, err := executor.ExecutePlay(play2)
	if err != nil {
		t.Fatalf("execute play2 failed: %v", err)
	}

	// 验证 Play 2 执行时 currentPlayDefaults 已被清空隔离
	if executor.currentPlayDefaults != nil {
		t.Errorf("expected currentPlayDefaults to be nil in Play 2, got: %#v", executor.currentPlayDefaults)
	}

	// 执行包含两个 play 的完成 playbook
	pb := &Playbook{
		Plays: []*Play{play1, play2},
	}
	res, err := executor.Execute(pb)
	if err != nil {
		t.Fatalf("execute playbook failed: %v", err)
	}
	if len(res.Plays) != 2 {
		t.Fatalf("expected 2 plays, got %d", len(res.Plays))
	}
}

// 2. 测试 SSHClient SetTimeout 与 GetTimeout 线程安全
func TestPhase28_SSHClientTimeoutAccess(t *testing.T) {
	h := &inventory.Host{Name: "test-host", Address: "127.0.0.1", Port: 22}
	client, err := ssh.NewClient(h, nil)
	if err != nil {
		t.Fatalf("failed to create ssh client: %v", err)
	}

	if client.GetTimeout() != 30*time.Second {
		t.Errorf("expected default timeout 30s, got: %v", client.GetTimeout())
	}

	client.SetTimeout(10 * time.Second)
	if client.GetTimeout() != 10*time.Second {
		t.Errorf("expected updated timeout 10s, got: %v", client.GetTimeout())
	}

	// 并发读写测试超时字段
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			client.SetTimeout(time.Duration(idx+1) * time.Second)
		}(i)
		go func() {
			defer wg.Done()
			_ = client.GetTimeout()
		}()
	}
	wg.Wait()
}

// 3. 测试 ResolveHostVars 高并发读与组继承保护，验证无 Data Race 与 Panic
func TestPhase28_ResolveHostVarsConcurrentReadWriteSafety(t *testing.T) {
	inv := inventory.NewInventory()

	prodGroup := &inventory.Group{
		Name:      "prod",
		Hosts:     make([]*inventory.Host, 0),
		Children:  make([]*inventory.Group, 0),
		Variables: map[string]string{"env": "prod", "datacenter": "dc1"},
	}
	webGroup := &inventory.Group{
		Name:      "web",
		Hosts:     make([]*inventory.Host, 0),
		Children:  make([]*inventory.Group, 0),
		Variables: map[string]string{"tier": "frontend", "datacenter": "dc2"},
		Parent:    prodGroup,
	}
	prodGroup.Children = append(prodGroup.Children, webGroup)

	host := &inventory.Host{
		Name:      "web-app-01",
		Address:   "10.0.1.1",
		Variables: map[string]string{"node_id": "01"},
	}
	webGroup.Hosts = append(webGroup.Hosts, host)
	inv.Groups["prod"] = prodGroup
	inv.Groups["web"] = webGroup
	inv.AllHosts["web-app-01"] = host

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			vars := inv.ResolveHostVars(host)
			if vars["env"] != "prod" {
				t.Errorf("expected env=prod, got: %v", vars["env"])
			}
			if vars["datacenter"] != "dc2" { // 子组 web 覆盖父组 prod
				t.Errorf("expected datacenter=dc2, got: %v", vars["datacenter"])
			}
			if vars["node_id"] != "01" {
				t.Errorf("expected node_id=01, got: %v", vars["node_id"])
			}
		}(i)
	}
	wg.Wait()
}
