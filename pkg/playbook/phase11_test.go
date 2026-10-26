package playbook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/inventory"
)

// TestAsyncStatusExecutionAndPolling 测试 async: N, poll: 0 配合 async_status 与 until 轮询完成
func TestAsyncStatusExecutionAndPolling(t *testing.T) {
	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	e := NewExecutor(inv)
	defer e.Close()

	falseVal := false
	zeroPoll := 0
	play := &Play{
		Name:        "Test Async Background and Poll",
		Hosts:       "localhost",
		GatherFacts: &falseVal,
		Tasks: []*Task{
			{
				Name:       "Start background async job",
				ModuleName: "shell",
				Params: map[string]interface{}{
					"_raw_params": "sleep 1 && echo async_job_done",
				},
				Async:    10,
				Poll:     &zeroPoll,
				Register: "job_init",
			},
			{
				Name:       "Wait for async job to complete",
				ModuleName: "async_status",
				Params: map[string]interface{}{
					"jid": "{{ job_init.ansible_job_id }}",
				},
				Register: "job_poll",
				Until:    "job_poll.finished == 1",
				Retries:  10,
				Delay:    1,
			},
			{
				Name:       "Clean up async job files",
				ModuleName: "async_status",
				Params: map[string]interface{}{
					"jid":  "{{ job_init.ansible_job_id }}",
					"mode": "cleanup",
				},
				Register: "job_clean",
			},
		},
	}

	pb := &Playbook{Plays: []*Play{play}}
	res, err := e.Execute(pb)
	if err != nil {
		t.Fatalf("playbook execution failed: %v", err)
	}

	hostRes := res.Plays[0].Hosts["localhost"]
	if hostRes == nil || len(hostRes.Tasks) != 3 {
		t.Fatalf("expected 3 tasks executed, got %#v", hostRes)
	}

	task1 := hostRes.Tasks[0]
	if !task1.Changed {
		t.Errorf("task 1 expected changed=true")
	}
	jidVal := task1.Data["ansible_job_id"]
	if jidVal == nil || jidVal == "" {
		t.Fatalf("expected ansible_job_id in task 1 data, got %#v", task1.Data)
	}

	task2 := hostRes.Tasks[1]
	if task2.Failed {
		t.Errorf("task 2 polling failed: %s", task2.Message)
	}
	if !strings.Contains(task2.Stdout, "async_job_done") {
		t.Errorf("expected stdout to contain 'async_job_done', got %q", task2.Stdout)
	}

	task3 := hostRes.Tasks[2]
	if task3.Failed {
		t.Errorf("task 3 cleanup failed: %s", task3.Message)
	}
}

// TestCustomLibraryModuleExecution 测试 library/ 自定义私有模块自动装载与执行 (JSON 协议)
func TestCustomLibraryModuleExecution(t *testing.T) {
	tempDir := t.TempDir()
	libDir := filepath.Join(tempDir, "library")
	if err := os.MkdirAll(libDir, 0755); err != nil {
		t.Fatalf("mkdir library: %v", err)
	}

	// 1. 创建 library/k8s_cluster_check.sh
	scriptContent := `#!/bin/sh
cat <<EOF
{
  "changed": true,
  "msg": "cluster is healthy",
  "nodes_ready": 3,
  "version": "v1.28.2"
}
EOF
exit 0
`
	scriptPath := filepath.Join(libDir, "k8s_cluster_check.sh")
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("write custom module: %v", err)
	}

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	e := NewExecutor(inv)
	e.baseDir = tempDir
	defer e.Close()

	falseVal := false
	play := &Play{
		Name:        "Test Custom Module",
		Hosts:       "localhost",
		GatherFacts: &falseVal,
		Tasks: []*Task{
			{
				Name:       "Run custom k8s module",
				ModuleName: "k8s_cluster_check",
				Params: map[string]interface{}{
					"check_timeout": 30,
				},
				Register: "custom_res",
			},
		},
	}

	pb := &Playbook{Plays: []*Play{play}}
	res, err := e.Execute(pb)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	hostRes := res.Plays[0].Hosts["localhost"]
	if hostRes == nil || len(hostRes.Tasks) != 1 {
		t.Fatalf("expected 1 task executed, got %#v", hostRes)
	}
	taskRes := hostRes.Tasks[0]
	if !taskRes.Changed {
		t.Errorf("expected changed=true for custom module")
	}
	if taskRes.Message != "cluster is healthy" {
		t.Errorf("expected msg 'cluster is healthy', got %q", taskRes.Message)
	}
	if taskRes.Data["version"] != "v1.28.2" {
		t.Errorf("expected version v1.28.2 in data, got %#v", taskRes.Data)
	}
}

// TestRescueFailedTaskVariables 测试 block 发生故障切入 rescue 时注入 ansible_failed_task 与 ansible_failed_result
func TestRescueFailedTaskVariables(t *testing.T) {
	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	e := NewExecutor(inv)
	defer e.Close()

	falseVal := false
	play := &Play{
		Name:        "Test Rescue Error Context",
		Hosts:       "localhost",
		GatherFacts: &falseVal,
		Tasks: []*Task{
			{
				Name: "Block with deliberate failure",
				Block: []*Task{
					{
						Name:       "Failing step 1",
						ModuleName: "command",
						Params: map[string]interface{}{
							"_raw_params": "echo 'disk full error' >&2; exit 2",
						},
					},
				},
				Rescue: []*Task{
					{
						Name:       "Rescue step captures failure info",
						ModuleName: "debug",
						Params: map[string]interface{}{
							"msg": "rescued_task={{ ansible_failed_task.name }} rc={{ ansible_failed_result.rc }}",
						},
					},
				},
			},
		},
	}

	pb := &Playbook{Plays: []*Play{play}}
	res, err := e.Execute(pb)
	if err != nil {
		t.Fatalf("playbook expected to succeed due to rescue, got error: %v", err)
	}

	hostRes := res.Plays[0].Hosts["localhost"]
	if hostRes == nil || len(hostRes.Tasks) < 2 {
		t.Fatalf("expected at least 2 tasks (failed block + rescue), got %#v", hostRes)
	}

	rescueTask := hostRes.Tasks[1]
	expected := "rescued_task=Failing step 1 rc=2"
	if !strings.Contains(rescueTask.Stdout, expected) {
		t.Errorf("rescue stdout expected to contain %q, got %q", expected, rescueTask.Stdout)
	}
}

// TestCustomLibraryModuleNonJsonScript 测试执行非 JSON 返回的原生 Shell 脚本模块
func TestCustomLibraryModuleNonJsonScript(t *testing.T) {
	tempDir := t.TempDir()
	libDir := filepath.Join(tempDir, "library")
	if err := os.MkdirAll(libDir, 0755); err != nil {
		t.Fatalf("mkdir library: %v", err)
	}

	scriptContent := `#!/bin/sh
echo "simple plain text output from custom module"
exit 0
`
	scriptPath := filepath.Join(libDir, "plain_echo.sh")
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("write custom module: %v", err)
	}

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	e := NewExecutor(inv)
	e.baseDir = tempDir
	defer e.Close()

	falseVal := false
	play := &Play{
		Name:        "Test Plain Script Custom Module",
		Hosts:       "localhost",
		GatherFacts: &falseVal,
		Tasks: []*Task{
			{
				Name:       "Run plain echo module",
				ModuleName: "plain_echo",
			},
		},
	}

	pb := &Playbook{Plays: []*Play{play}}
	res, err := e.Execute(pb)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	hostRes := res.Plays[0].Hosts["localhost"]
	if hostRes == nil || len(hostRes.Tasks) != 1 {
		t.Fatalf("expected 1 task executed, got %#v", hostRes)
	}
	if !strings.Contains(hostRes.Tasks[0].Stdout, "simple plain text output from custom module") {
		t.Errorf("unexpected stdout: %q", hostRes.Tasks[0].Stdout)
	}
}
