package playbook

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/module"
	"go-ansible/pkg/ssh"
)

// mockThrottleModule 模拟带休眠的任务以检测并发度
type mockThrottleModule struct {
	currentConc int64
	maxConc     int64
	mu          sync.Mutex
}

func (m *mockThrottleModule) Name() string                                 { return "mock_throttle" }
func (m *mockThrottleModule) Validate(params map[string]interface{}) error { return nil }
func (m *mockThrottleModule) Execute(client ssh.Connection, params map[string]interface{}) (*module.Result, error) {
	curr := atomic.AddInt64(&m.currentConc, 1)
	m.mu.Lock()
	if curr > m.maxConc {
		m.maxConc = curr
	}
	m.mu.Unlock()

	time.Sleep(30 * time.Millisecond)

	atomic.AddInt64(&m.currentConc, -1)
	return &module.Result{Changed: false}, nil
}

func TestTaskThrottleConcurrency(t *testing.T) {
	inv := inventory.NewInventory()
	// 创建 6 个本地执行的主机
	for i := 1; i <= 6; i++ {
		h := &inventory.Host{
			Name:      "h" + string(rune('0'+i)),
			Address:   "127.0.0.1",
			Port:      22,
			Variables: map[string]string{"ansible_connection": "local"},
		}
		inv.AllHosts[h.Name] = h
	}

	mockMod := &mockThrottleModule{}
	e := NewExecutor(inv)
	e.registry.Register(mockMod)
	e.SetForks(10) // 全局并发设为 10，但任务限制为 2

	play := &Play{
		Name:     "Test Throttle Play",
		Hosts:    "all",
		Strategy: "linear",
		Tasks: []*Task{
			{
				Name:       "Throttled Task",
				ModuleName: "mock_throttle",
				Throttle:   2,
			},
		},
	}

	pb := &Playbook{Plays: []*Play{play}}
	_, err := e.Execute(pb)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if mockMod.maxConc > 2 {
		t.Errorf("expected max concurrency <= 2, got %d", mockMod.maxConc)
	}
}

func TestPlayThrottleInheritance(t *testing.T) {
	parser := NewParser("")
	yamlContent := `
- name: Test Play Throttle
  hosts: localhost
  throttle: 3
  tasks:
    - name: Task One
      debug:
        msg: "hello"
    - name: Block Task
      block:
        - name: Inner Task
          debug:
            msg: "inner"
`
	pb, err := parser.Parse([]byte(yamlContent))
	if err != nil {
		t.Fatalf("ParseBytes error: %v", err)
	}
	if len(pb.Plays) != 1 {
		t.Fatalf("expected 1 play, got %d", len(pb.Plays))
	}
	play := pb.Plays[0]
	if play.Throttle != 3 {
		t.Errorf("expected play throttle 3, got %d", play.Throttle)
	}
	if len(play.Tasks) < 2 {
		t.Fatalf("expected at least 2 tasks")
	}
	if play.Tasks[0].Throttle != 3 {
		t.Errorf("expected Task One to inherit throttle 3, got %d", play.Tasks[0].Throttle)
	}
	if len(play.Tasks[1].Block) == 0 || play.Tasks[1].Block[0].Throttle != 3 {
		t.Errorf("expected Inner Task to inherit throttle 3, got %d", play.Tasks[1].Block[0].Throttle)
	}
}

func TestAddHostAndSubsequentPlay(t *testing.T) {
	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	e := NewExecutor(inv)
	falseVal := false

	play1 := &Play{
		Name:        "Play 1 - Add Host",
		Hosts:       "localhost",
		GatherFacts: &falseVal,
		Tasks: []*Task{
			{
				Name:       "Add dynamic k8s worker",
				ModuleName: "add_host",
				Params: map[string]interface{}{
					"name":               "k8s-node-01",
					"groups":             "k8s_workers,k8s_nodes",
					"custom_var":         "hello_cluster",
					"ansible_connection": "local",
				},
			},
		},
	}

	play2 := &Play{
		Name:        "Play 2 - Run on dynamic worker",
		Hosts:       "k8s_workers",
		GatherFacts: &falseVal,
		Tasks: []*Task{
			{
				Name:       "Check custom var",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "Variable value is {{ custom_var }}",
				},
			},
		},
	}

	pb := &Playbook{Plays: []*Play{play1, play2}}
	_, err := e.Execute(pb)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	// 验证 inventory 中已包含动态添加的主机和组
	h, err := inv.GetHost("k8s-node-01")
	if err != nil {
		t.Fatalf("host k8s-node-01 not found in inventory: %v", err)
	}
	if h.Variables["custom_var"] != "hello_cluster" {
		t.Errorf("expected custom_var 'hello_cluster', got %s", h.Variables["custom_var"])
	}

	grp, err := inv.GetGroup("k8s_workers")
	if err != nil {
		t.Fatalf("group k8s_workers not found: %v", err)
	}
	if len(grp.Hosts) != 1 || grp.Hosts[0].Name != "k8s-node-01" {
		t.Errorf("expected k8s-node-01 in group k8s_workers")
	}
}

func TestGroupByAndSubsequentPlay(t *testing.T) {
	inv := inventory.NewInventory()
	h1 := &inventory.Host{
		Name:      "master-1",
		Address:   "127.0.0.1",
		Port:      22,
		Variables: map[string]string{"node_role": "master", "ansible_connection": "local"},
	}
	h2 := &inventory.Host{
		Name:      "worker-1",
		Address:   "127.0.0.1",
		Port:      22,
		Variables: map[string]string{"node_role": "worker", "ansible_connection": "local"},
	}
	inv.AllHosts[h1.Name] = h1
	inv.AllHosts[h2.Name] = h2

	e := NewExecutor(inv)
	falseVal := false

	play1 := &Play{
		Name:        "Play 1 - Group Hosts Dynamically",
		Hosts:       "all",
		GatherFacts: &falseVal,
		Tasks: []*Task{
			{
				Name:       "Group nodes by role",
				ModuleName: "group_by",
				Params: map[string]interface{}{
					"key": "role_{{ node_role }}",
				},
			},
		},
	}

	play2 := &Play{
		Name:        "Play 2 - Run on masters only",
		Hosts:       "role_master",
		GatherFacts: &falseVal,
		Tasks: []*Task{
			{
				Name:       "Echo master task",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "running on master",
				},
			},
		},
	}

	pb := &Playbook{Plays: []*Play{play1, play2}}
	_, err := e.Execute(pb)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	masterGroup, err := inv.GetGroup("role_master")
	if err != nil {
		t.Fatalf("role_master group not found: %v", err)
	}
	if len(masterGroup.Hosts) != 1 || masterGroup.Hosts[0].Name != "master-1" {
		t.Errorf("expected only master-1 in role_master group")
	}

	workerGroup, err := inv.GetGroup("role_worker")
	if err != nil {
		t.Fatalf("role_worker group not found: %v", err)
	}
	if len(workerGroup.Hosts) != 1 || workerGroup.Hosts[0].Name != "worker-1" {
		t.Errorf("expected only worker-1 in role_worker group")
	}
}
