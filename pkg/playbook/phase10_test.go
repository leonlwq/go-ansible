package playbook

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-ansible/pkg/inventory"
)

// TestTaskTimeoutFailsGracefully 测试任务级超时熔断机制 (timeout: <seconds>)
func TestTaskTimeoutFailsGracefully(t *testing.T) {
	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	e := NewExecutor(inv)
	defer e.Close()

	falseVal := false
	play := &Play{
		Name:        "Test Task Timeout",
		Hosts:       "localhost",
		GatherFacts: &falseVal,
		Tasks: []*Task{
			{
				Name:       "Sleeping task that should time out",
				ModuleName: "command",
				Params: map[string]interface{}{
					"_raw_params": "sleep 3",
				},
				Timeout: 1, // 1 秒超时
			},
		},
	}

	pb := &Playbook{Plays: []*Play{play}}
	start := time.Now()
	res, err := e.Execute(pb)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected playbook execution error due to timeout, got nil")
	}

	// 耗时应在 1~2 秒左右，不应等待 3 秒完整 sleep
	if elapsed >= 2800*time.Millisecond {
		t.Errorf("task took %v, timeout did not abort promptly", elapsed)
	}

	if len(res.Plays) != 1 {
		t.Fatalf("expected 1 play result, got %d", len(res.Plays))
	}
	hostRes := res.Plays[0].Hosts["localhost"]
	if hostRes == nil || len(hostRes.Tasks) == 0 {
		t.Fatalf("expected task result for localhost, got %#v", hostRes)
	}

	taskRes := hostRes.Tasks[0]
	if !taskRes.Failed {
		t.Errorf("expected task to be marked failed, got failed=%v", taskRes.Failed)
	}
	if taskRes.ExitCode != 124 {
		t.Errorf("expected exit code 124 for timeout, got %d", taskRes.ExitCode)
	}
	if !strings.Contains(taskRes.Message, "timed out after 1 seconds") {
		t.Errorf("expected timeout message, got %s", taskRes.Message)
	}
}

// TestTaskTimeoutWithIgnoreErrors 测试任务超时但配置了 ignore_errors 时 Play 不熔断并继续推进
func TestTaskTimeoutWithIgnoreErrors(t *testing.T) {
	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	e := NewExecutor(inv)
	defer e.Close()

	falseVal := false
	play := &Play{
		Name:        "Test Task Timeout Ignore Errors",
		Hosts:       "localhost",
		GatherFacts: &falseVal,
		Tasks: []*Task{
			{
				Name:       "Timed out task with ignore_errors",
				ModuleName: "command",
				Params: map[string]interface{}{
					"_raw_params": "sleep 3",
				},
				Timeout:     1,
				IgnoreError: true,
			},
			{
				Name:       "Subsequent task executes normally",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "subsequent_ok",
				},
			},
		},
	}

	pb := &Playbook{Plays: []*Play{play}}
	res, err := e.Execute(pb)
	if err != nil {
		t.Fatalf("expected playbook to succeed with ignore_errors, got error: %v", err)
	}

	hostRes := res.Plays[0].Hosts["localhost"]
	if hostRes == nil || len(hostRes.Tasks) != 2 {
		t.Fatalf("expected 2 tasks results, got %#v", hostRes)
	}
	if hostRes.Tasks[0].Failed || hostRes.Tasks[0].ExitCode != 124 || !strings.Contains(hostRes.Tasks[0].Message, "ignored error") {
		t.Errorf("expected first task failed=false with code 124 and ignored error message, got failed=%v, code=%d, msg=%s",
			hostRes.Tasks[0].Failed, hostRes.Tasks[0].ExitCode, hostRes.Tasks[0].Message)
	}
	if !strings.Contains(hostRes.Tasks[1].Stdout, "subsequent_ok") {
		t.Errorf("expected second task stdout to contain 'subsequent_ok', got %q", hostRes.Tasks[1].Stdout)
	}
}

// TestVarsFilesDynamicResolutionAndCandidates 测试 vars_files 动态变量与候选列表 fallback
func TestVarsFilesDynamicResolutionAndCandidates(t *testing.T) {
	tempDir := t.TempDir()
	varsDir := filepath.Join(tempDir, "vars")
	if err := os.MkdirAll(varsDir, 0755); err != nil {
		t.Fatalf("mkdir error: %v", err)
	}

	// 1. 创建 vars/prod.yml
	prodVarsYAML := `
app_tier: "production_tier"
db_pool_size: 50
`
	if err := os.WriteFile(filepath.Join(varsDir, "prod.yml"), []byte(prodVarsYAML), 0644); err != nil {
		t.Fatalf("write prod vars: %v", err)
	}

	// 2. 创建 vars/fallback.yml
	fallbackVarsYAML := `
backup_storage: "s3_glacier"
`
	if err := os.WriteFile(filepath.Join(varsDir, "fallback.yml"), []byte(fallbackVarsYAML), 0644); err != nil {
		t.Fatalf("write fallback vars: %v", err)
	}

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	e := NewExecutor(inv)
	e.baseDir = tempDir
	defer e.Close()

	falseVal := false
	// 验证动态 vars_files:
	// - 第 1 个条目: "vars/{{ env }}.yml"
	// - 第 2 个条目: 候选列表 "vars/missing_{{ env }}.yml,vars/fallback.yml"
	play := &Play{
		Name:        "Test Dynamic Vars Files",
		Hosts:       "localhost",
		GatherFacts: &falseVal,
		Vars: map[string]interface{}{
			"env": "prod",
		},
		VarsFiles: []string{
			"vars/{{ env }}.yml",
			"vars/missing_{{ env }}.yml,vars/fallback.yml",
		},
		Tasks: []*Task{
			{
				Name:       "Check loaded vars",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "app_tier={{ app_tier }} backup_storage={{ backup_storage }}",
				},
			},
		},
	}

	pb := &Playbook{Plays: []*Play{play}}
	res, err := e.Execute(pb)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	hostRes := res.Plays[0].Hosts["localhost"]
	if hostRes == nil || len(hostRes.Tasks) == 0 {
		t.Fatalf("expected task result for localhost")
	}
	stdout := hostRes.Tasks[0].Stdout
	expectedOut := "app_tier=production_tier backup_storage=s3_glacier"
	if !strings.Contains(stdout, expectedOut) {
		t.Errorf("expected stdout to contain %q, got %q", expectedOut, stdout)
	}

	// 验证当候选列表中所有文件均不存在时报错
	playMissing := &Play{
		Name:        "Test Missing Candidate List",
		Hosts:       "localhost",
		GatherFacts: &falseVal,
		VarsFiles: []string{
			"vars/none1.yml,vars/none2.yml",
		},
	}
	pbMissing := &Playbook{Plays: []*Play{playMissing}}
	if _, err := e.Execute(pbMissing); err == nil {
		t.Errorf("expected error when none of the candidates exist, got nil")
	}
}

// TestDebugModuleVerbosityFiltering 测试 debug 模块的 verbosity 级别抑制与输出
func TestDebugModuleVerbosityFiltering(t *testing.T) {
	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	falseVal := false
	play := &Play{
		Name:        "Test Debug Verbosity",
		Hosts:       "localhost",
		GatherFacts: &falseVal,
		Tasks: []*Task{
			{
				Name:       "Default debug always shows",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "always visible",
				},
			},
			{
				Name:       "Debug requires verbosity 2",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg":       "only with -vv",
					"verbosity": 2,
				},
			},
		},
	}

	// 1. 当 VerbosityLevel = 1 时，verbosity=2 任务应该被 Skipped
	e1 := NewExecutor(inv)
	e1.SetVerbosityLevel(1)
	res1, err := e1.Execute(&Playbook{Plays: []*Play{play}})
	e1.Close()
	if err != nil {
		t.Fatalf("e1 execute error: %v", err)
	}
	tasks1 := res1.Plays[0].Hosts["localhost"].Tasks
	if len(tasks1) != 2 {
		t.Fatalf("expected 2 tasks in e1, got %d", len(tasks1))
	}
	if tasks1[0].Skipped {
		t.Errorf("task 1 should not be skipped")
	}
	if !tasks1[1].Skipped {
		t.Errorf("task 2 should be skipped under verbosity 1, got skipped=%v", tasks1[1].Skipped)
	}

	// 2. 当 VerbosityLevel = 2 时，两个任务均正常执行，不被 Skipped
	e2 := NewExecutor(inv)
	e2.SetVerbosityLevel(2)
	res2, err := e2.Execute(&Playbook{Plays: []*Play{play}})
	e2.Close()
	if err != nil {
		t.Fatalf("e2 execute error: %v", err)
	}
	tasks2 := res2.Plays[0].Hosts["localhost"].Tasks
	if len(tasks2) != 2 {
		t.Fatalf("expected 2 tasks in e2, got %d", len(tasks2))
	}
	if tasks2[0].Skipped || tasks2[1].Skipped {
		t.Errorf("neither task should be skipped under verbosity 2: t1=%v, t2=%v", tasks2[0].Skipped, tasks2[1].Skipped)
	}
	if !strings.Contains(tasks2[1].Stdout, "only with -vv") {
		t.Errorf("task 2 stdout expected to contain 'only with -vv', got %q", tasks2[1].Stdout)
	}
}

// TestPlaybookYamlVarsFilesParsing 测试解析包含复杂与候选嵌套列表的 YAML playbook
func TestPlaybookYamlVarsFilesParsing(t *testing.T) {
	yamlContent := `
- name: Test YAML vars_files parsing
  hosts: localhost
  vars_files:
    - "vars/base.yml"
    - [ "vars/{{ env }}.yml", "vars/default.yml" ]
  tasks:
    - name: Ping
      ping:
`
	parser := NewParser("")
	pb, err := parser.Parse([]byte(yamlContent))
	if err != nil {
		t.Fatalf("parser.Parse error: %v", err)
	}
	if len(pb.Plays) != 1 {
		t.Fatalf("expected 1 play, got %d", len(pb.Plays))
	}

	play := pb.Plays[0]
	if len(play.VarsFiles) != 2 {
		t.Fatalf("expected 2 vars_files entries, got %d: %#v", len(play.VarsFiles), play.VarsFiles)
	}
	if play.VarsFiles[0] != "vars/base.yml" {
		t.Errorf("expected first entry 'vars/base.yml', got %q", play.VarsFiles[0])
	}
	expectedCandidates := fmt.Sprintf("vars/{{ env }}.yml,vars/default.yml")
	if play.VarsFiles[1] != expectedCandidates {
		t.Errorf("expected second entry %q, got %q", expectedCandidates, play.VarsFiles[1])
	}
}
