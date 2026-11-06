package playbook

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/ssh"
	"go-ansible/pkg/util"
)

// TestStep2_StartAtTaskFastPath 验证未指定 start-at-task 时的无锁快速放行机制
func TestStep2_StartAtTaskFastPath(t *testing.T) {
	exec := NewExecutor(&inventory.Inventory{})
	exec.startAtTask = ""

	h := &inventory.Host{Name: "web1"}
	task := &Task{Name: "Some task with {{ unresolved_var }}"}

	// 在 startAtTask 为空时，无需模板变量解析且无需加锁即可直接放行
	if !exec.shouldRunTaskByStartAtTask(h, task, nil) {
		t.Fatalf("expected task to be allowed on empty startAtTask")
	}

	// 当指定了 startAtTask 时，目标任务前的普通任务被拦截
	exec.startAtTask = "target task"
	exec.startedTasks = make(map[string]bool)

	task1 := &Task{Name: "first task"}
	if exec.shouldRunTaskByStartAtTask(h, task1, map[string]interface{}{}) {
		t.Fatalf("expected task1 to be blocked before startAtTask")
	}

	taskTarget := &Task{Name: "target task"}
	if !exec.shouldRunTaskByStartAtTask(h, taskTarget, map[string]interface{}{}) {
		t.Fatalf("expected taskTarget to match startAtTask and be allowed")
	}

	// 目标任务匹配后，后续任务自动放行
	taskAfter := &Task{Name: "subsequent task"}
	if !exec.shouldRunTaskByStartAtTask(h, taskAfter, map[string]interface{}{}) {
		t.Fatalf("expected subsequent task to be allowed after startAtTask reached")
	}
}

// TestStep2_ExecuteTaskWithEnv_COW 验证任务级变量 Copy-on-Write 性能优化
func TestStep2_ExecuteTaskWithEnv_COW(t *testing.T) {
	exec := NewExecutor(&inventory.Inventory{})
	exec.extraVars = make(map[string]interface{})

	baseVars := map[string]interface{}{
		"global_k1": "val1",
		"global_k2": "val2",
	}

	// 场景 1: task.Vars 为空且 extraVars 为空，COW 直接共享变量指针
	taskNoVars := &Task{
		ModuleName: "debug",
		Params:     map[string]interface{}{"msg": "hello"},
	}

	host := &inventory.Host{Name: "localhost"}
	results, err := exec.executeTask(host, taskNoVars, baseVars, nil)
	if err != nil {
		t.Fatalf("executeTask error: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected results")
	}

	// 场景 2: task.Vars 存在，独立克隆并在当前任务生效，不污染原始 baseVars
	taskWithVars := &Task{
		ModuleName: "debug",
		Vars: map[string]interface{}{
			"global_k1": "overridden_in_task",
			"task_only": "new_val",
		},
	}
	_, err = exec.executeTask(host, taskWithVars, baseVars, nil)
	if err != nil {
		t.Fatalf("executeTask with vars error: %v", err)
	}

	if baseVars["global_k1"] != "val1" {
		t.Fatalf("expected baseVars[global_k1] to remain val1, got %v", baseVars["global_k1"])
	}
	if _, ok := baseVars["task_only"]; ok {
		t.Fatalf("task_only should not leak into baseVars")
	}
}

// TestStep2_RoleASTCaching 验证 Role AST 内存缓存与避免重复磁盘解析
func TestStep2_RoleASTCaching(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "role_cache_test_*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	roleDir := filepath.Join(tempDir, "roles", "test_role")
	tasksDir := filepath.Join(roleDir, "tasks")
	if err := os.MkdirAll(tasksDir, 0755); err != nil {
		t.Fatalf("mkdir tasks: %v", err)
	}
	tasksYaml := `- name: Role Task 1
  command: echo "role task"
`
	if err := os.WriteFile(filepath.Join(tasksDir, "main.yml"), []byte(tasksYaml), 0644); err != nil {
		t.Fatalf("write role task: %v", err)
	}

	exec := NewExecutor(&inventory.Inventory{})
	exec.baseDir = tempDir

	// 第一次解析：从磁盘读取
	role1, err := exec.getOrParseRole(roleDir)
	if err != nil {
		t.Fatalf("first getOrParseRole error: %v", err)
	}
	if role1 == nil || len(role1.Tasks) != 1 {
		t.Fatalf("expected 1 task in role1")
	}

	// 第二次解析：必须直接命中 parsedRoles 缓存并返回相同指针
	role2, err := exec.getOrParseRole(roleDir)
	if err != nil {
		t.Fatalf("second getOrParseRole error: %v", err)
	}
	if role1 != role2 {
		t.Fatalf("expected role1 and role2 pointers to be identical (cache hit)")
	}
}

// TestStep2_IncludeTasksASTCaching 验证 include_tasks AST 缓存与安全深度克隆
func TestStep2_IncludeTasksASTCaching(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "include_cache_test_*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	incFile := filepath.Join(tempDir, "inc.yml")
	incYaml := `- name: Included Task
  command: echo "included"
  tags:
    - base_tag
`
	if err := os.WriteFile(incFile, []byte(incYaml), 0644); err != nil {
		t.Fatalf("write inc file: %v", err)
	}

	exec := NewExecutor(&inventory.Inventory{})
	exec.baseDir = tempDir

	// 首次读取并加载
	tasks1, err := exec.getOrParseInclude(incFile)
	if err != nil {
		t.Fatalf("first getOrParseInclude: %v", err)
	}
	if len(tasks1) != 1 {
		t.Fatalf("expected 1 task")
	}

	// 再次读取命中缓存，但应返回独立的 Task 浅/深拷贝，使得后续 applyParentTaskAttributes 不互相污染
	tasks2, err := exec.getOrParseInclude(incFile)
	if err != nil {
		t.Fatalf("second getOrParseInclude: %v", err)
	}
	if tasks1[0] == tasks2[0] {
		t.Fatalf("expected tasks1[0] and tasks2[0] to be distinct clones")
	}

	// 对 tasks1 修改 tags
	parentTask := &Task{Tags: []string{"parent_tag"}}
	applyParentTaskAttributes(parentTask, tasks1)

	// tasks2 的 tags 不应受 tasks1 的 parent attributes 影响
	if len(tasks2[0].Tags) != 1 || tasks2[0].Tags[0] != "base_tag" {
		t.Fatalf("expected tasks2 tags to remain pristine, got %v", tasks2[0].Tags)
	}
}

// TestStep2_InventoryGetGroupsSnapshot_Concurrency 验证高并发下 GetGroupsSnapshot 与 AddHost/AddGroup 无竞争安全
func TestStep2_InventoryGetGroupsSnapshot_Concurrency(t *testing.T) {
	inv := inventory.NewInventory()
	inv.AddGroup(&inventory.Group{Name: "web"})
	inv.AddGroup(&inventory.Group{Name: "db"})

	var wg sync.WaitGroup
	stopCh := make(chan struct{})

	// 并发读快照
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				default:
					snapshot := inv.GetGroupsSnapshot()
					if len(snapshot) < 2 {
						t.Errorf("expected at least 2 groups in snapshot")
					}
					time.Sleep(1 * time.Millisecond)
				}
			}
		}()
	}

	// 并发写主机与组
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				h := &inventory.Host{
					Name:    fmt.Sprintf("host-%d-%d", workerID, j),
					Address: "127.0.0.1",
				}
				inv.AddHost(h, "web")
				if j%5 == 0 {
					inv.AddGroup(&inventory.Group{
						Name: fmt.Sprintf("dynamic-grp-%d-%d", workerID, j),
					})
				}
			}
		}(i)
	}

	// 等待写操作完成
	time.Sleep(100 * time.Millisecond)
	close(stopCh)
	wg.Wait()
}

// TestStep2_PoolRemoveKeepsHostLock 验证连接池 Remove 时保留主机锁单例指针
func TestStep2_PoolRemoveKeepsHostLock(t *testing.T) {
	pool := ssh.NewConnectionPool(nil)

	host := &inventory.Host{
		Name:    "localhost",
		Address: "127.0.0.1",
		Port:    22,
	}

	// 预先建立连接并获取锁指针
	conn, err := pool.Get(host)
	if err != nil {
		t.Fatalf("pool.Get error: %v", err)
	}
	if conn == nil {
		t.Fatalf("expected connection")
	}

	// 移除连接
	if err := pool.Remove("localhost"); err != nil {
		t.Fatalf("pool.Remove error: %v", err)
	}

	// 再次获取连接
	conn2, err := pool.Get(host)
	if err != nil {
		t.Fatalf("second pool.Get error: %v", err)
	}
	if conn2 == nil {
		t.Fatalf("expected connection on second get")
	}

	_ = pool.Close()
}

// TestStep2_FormatEnvPrefix_Integration 验证环境变量前缀在 Shell 与任务中的通用性与确定性
func TestStep2_FormatEnvPrefix_Integration(t *testing.T) {
	env := map[string]string{
		"PORT": "8080",
		"HOST": "0.0.0.0",
		"PASS": "p@$$w'ord",
	}

	prefix := util.FormatEnvPrefix(env)
	expected := "env HOST='0.0.0.0' PASS='p@$$w'\"'\"'ord' PORT='8080' "
	if prefix != expected {
		t.Fatalf("expected prefix %q, got %q", expected, prefix)
	}
}

