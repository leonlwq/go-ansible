package playbook

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/ssh"
)

// 1. 测试 HostResult 并发安全：AppendTask, TaskCount, MarkRescued, HasFailure, GetTasks
func TestPhase30_HostResultMarkRescuedConcurrency(t *testing.T) {
	hr := &HostResult{}
	var wg sync.WaitGroup

	const goroutines = 20
	const tasksPerGoroutine = 50

	// 并发追加任务
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < tasksPerGoroutine; i++ {
				failed := (i % 5 == 0)
				tr := &TaskResult{
					Name:   fmt.Sprintf("task_%d_%d", gid, i),
					Host:   "node1",
					Failed: failed,
				}
				hr.AppendTask(tr)
				_ = hr.TaskCount()
				_ = hr.HasFailure(0)
			}
		}(g)
	}

	// 并发读取与标记救援
	for g := 0; g < 5; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				snapshot := hr.GetTasks()
				if len(snapshot) > 10 {
					hr.MarkRescued(len(snapshot) / 2)
				}
				time.Sleep(100 * time.Microsecond)
			}
		}()
	}

	wg.Wait()

	totalExpected := goroutines * tasksPerGoroutine
	if hr.TaskCount() != totalExpected {
		t.Fatalf("expected TaskCount %d, got %d", totalExpected, hr.TaskCount())
	}

	finalTasks := hr.GetTasks()
	if len(finalTasks) != totalExpected {
		t.Fatalf("expected %d tasks in snapshot, got %d", totalExpected, len(finalTasks))
	}

	// 执行全量救援标记并验证 HasFailure 返回 false
	hr.MarkRescued(0)
	if hr.HasFailure(0) {
		t.Errorf("expected HasFailure == false after full MarkRescued(0)")
	}
}

// 2. 测试 executeAddHostModule 锁外模板解析与动态清单注入
func TestPhase30_ExecuteAddHostLockScope(t *testing.T) {
	inv := inventory.NewInventory()
	exec := NewExecutor(inv)

	vars := map[string]interface{}{
		"dynamic_ip":   "192.168.10.50",
		"dynamic_user": "appuser",
	}

	params := map[string]interface{}{
		"name":         "node_dynamic",
		"ansible_host": "{{ dynamic_ip }}",
		"ansible_user": "{{ dynamic_user }}",
		"groups":       "workers,staging",
	}

	res := exec.executeAddHostModule(params, vars)
	if !res.Changed {
		t.Errorf("expected Changed == true when adding new host")
	}

	h := inv.AllHosts["node_dynamic"]
	if h == nil {
		t.Fatalf("expected node_dynamic to be present in inventory")
	}
	if h.Address != "192.168.10.50" {
		t.Errorf("expected host address '192.168.10.50', got %q", h.Address)
	}
	if h.User != "appuser" {
		t.Errorf("expected host user 'appuser', got %q", h.User)
	}

	// 验证组归属
	workersGroup := inv.Groups["workers"]
	var foundWorker bool
	if workersGroup != nil {
		for _, wh := range workersGroup.Hosts {
			if wh.Name == "node_dynamic" {
				foundWorker = true
				break
			}
		}
	}
	if !foundWorker {
		t.Errorf("expected node_dynamic in workers group")
	}
}

// 3. 测试任务超时机制 (NewTimer 资源清理)
func TestPhase30_TaskTimeout(t *testing.T) {
	inv := inventory.NewInventory()
	h := &inventory.Host{Name: "localhost", Connection: "local"}
	inv.AddHost(h)

	exec := NewExecutor(inv)
	exec.registry.Register(&phase30CustomModule{
		name: "hanging_module",
		onExecute: func(client ssh.Connection, params map[string]interface{}) {
			time.Sleep(300 * time.Millisecond)
		},
	})

	play := &Play{
		Name:  "timeout test",
		Hosts: "localhost",
		Tasks: []*Task{
			{
				Name:        "timeout task",
				ModuleName:  "hanging_module",
				Timeout:     1, // 1 秒超时 (任务 300ms 完成，应正常成功且 timer 及时停止)
				IgnoreError: false,
			},
		},
	}

	res, err := exec.ExecutePlay(play)
	if err != nil {
		t.Fatalf("ExecutePlay failed: %v", err)
	}
	if res == nil {
		t.Fatalf("expected non-nil result")
	}
	hostRes := res.Hosts["localhost"]
	if hostRes == nil || hostRes.TaskCount() != 1 {
		t.Fatalf("expected 1 task executed")
	}
	if hostRes.GetTasks()[0].Failed {
		t.Errorf("expected task to succeed within timeout")
	}
}

// 4. 测试 Debugger 互斥锁 debuggerMu 防止多主机并发冲突
func TestPhase30_DebuggerMutexSerialization(t *testing.T) {
	exec := NewExecutor(inventory.NewInventory())

	// 模拟 Stdin 提供两个 "continue\n" 输入
	inputBuf := bytes.NewBufferString("c\nc\n")
	outBuf := &bytes.Buffer{}
	exec.Stdin = inputBuf
	exec.Stdout = outBuf

	h1 := &inventory.Host{Name: "host1"}
	h2 := &inventory.Host{Name: "host2"}
	task := &Task{Name: "failing_task"}
	tr := &TaskResult{Name: "failing_task", Failed: true, Message: "error"}

	var wg sync.WaitGroup
	wg.Add(2)

	var act1, act2 DebuggerAction
	go func() {
		defer wg.Done()
		act1 = exec.runDebugger(h1, task, tr, nil)
	}()
	go func() {
		defer wg.Done()
		act2 = exec.runDebugger(h2, task, tr, nil)
	}()

	wg.Wait()

	if act1 != DebuggerContinue || act2 != DebuggerContinue {
		t.Errorf("expected both debugger actions to be DebuggerContinue, got %v and %v", act1, act2)
	}
}

// 5. 测试细粒度主机快照失效 (invalidateHostVarsCache) 避免全量缓存抖动
func TestPhase30_FineGrainedHostVarsCacheInvalidation(t *testing.T) {
	inv := inventory.NewInventory()
	h1 := &inventory.Host{Name: "srv1", Address: "10.0.0.1"}
	h2 := &inventory.Host{Name: "srv2", Address: "10.0.0.2"}
	h3 := &inventory.Host{Name: "srv3", Address: "10.0.0.3"}
	inv.AddHost(h1)
	inv.AddHost(h2)
	inv.AddHost(h3)

	exec := NewExecutor(inv)

	// 1. 构建初始快照
	snapshot1 := exec.buildHostVarsSnapshot(h1, nil)
	if len(snapshot1) < 3 {
		t.Fatalf("expected snapshot with at least 3 hosts, got %d", len(snapshot1))
	}

	exec.hostVarsMu.RLock()
	cachedSnapshot := exec.cachedHostVarsSnapshot
	exec.hostVarsMu.RUnlock()

	if cachedSnapshot == nil || len(cachedSnapshot) != 3 {
		t.Fatalf("expected cachedHostVarsSnapshot to contain 3 hosts")
	}

	// 2. 仅失效单个主机 srv2
	exec.invalidateHostVarsCache("srv2")

	exec.hostVarsMu.RLock()
	if exec.cachedHostVarsSnapshot == nil {
		t.Fatalf("cachedHostVarsSnapshot should NOT be completely wiped on single host invalidation")
	}
	if _, ok := exec.cachedHostVarsSnapshot["srv2"]; ok {
		t.Errorf("srv2 should have been deleted from cached snapshot")
	}
	if _, ok := exec.cachedHostVarsSnapshot["srv1"]; !ok {
		t.Errorf("srv1 should still be preserved in cached snapshot")
	}
	if _, ok := exec.cachedHostVarsSnapshot["srv3"]; !ok {
		t.Errorf("srv3 should still be preserved in cached snapshot")
	}
	exec.hostVarsMu.RUnlock()

	// 3. 再次获取快照，验证 srv2 被精准补全，全量重构未发生
	snapshot2 := exec.buildHostVarsSnapshot(h1, nil)
	if len(snapshot2) < 3 {
		t.Fatalf("expected snapshot2 with at least 3 hosts, got %d", len(snapshot2))
	}

	exec.hostVarsMu.RLock()
	if len(exec.cachedHostVarsSnapshot) != 3 {
		t.Errorf("expected cachedHostVarsSnapshot to have 3 hosts after backfill")
	}
	exec.hostVarsMu.RUnlock()

	// 4. 全量失效 (无参数)
	exec.invalidateHostVarsCache()
	exec.hostVarsMu.RLock()
	if exec.cachedHostVarsSnapshot != nil {
		t.Errorf("expected cachedHostVarsSnapshot == nil after full invalidateHostVarsCache()")
	}
	exec.hostVarsMu.RUnlock()
}
