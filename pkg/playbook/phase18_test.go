package playbook

import (
	"testing"

	"go-ansible/pkg/inventory"
)

func TestParsePlaybookTemplateSyntax(t *testing.T) {
	pb, err := NewParser("").ParseFile("../../playbook.yml.template")
	if err != nil {
		t.Fatalf("ParseFile(playbook.yml.template) failed: %v", err)
	}
	if len(pb.Plays) == 0 {
		t.Fatalf("expected at least 1 play")
	}
	play := pb.Plays[0]
	if play.Name != "Production Nginx Deployment and System Configuration" {
		t.Errorf("unexpected play name: %s", play.Name)
	}
	if len(play.Tasks) < 5 {
		t.Errorf("expected at least 5 tasks, got %d", len(play.Tasks))
	}
	if len(play.Handlers) < 2 {
		t.Errorf("expected at least 2 handlers, got %d", len(play.Handlers))
	}
}

func TestConditionStatusTests(t *testing.T) {
	inv := inventory.NewInventory()
	executor := NewExecutor(inv)

	vars := map[string]interface{}{
		"success_res": map[string]interface{}{
			"rc":     0,
			"failed": false,
		},
		"fail_res": map[string]interface{}{
			"rc":     1,
			"failed": true,
		},
		"skipped_res": map[string]interface{}{
			"skipped": true,
		},
		"unreach_res": map[string]interface{}{
			"unreachable": true,
		},
	}

	if !executor.evaluateWhen("success_res is succeeded", vars) {
		t.Errorf("expected 'success_res is succeeded' to be true")
	}
	if executor.evaluateWhen("fail_res is succeeded", vars) {
		t.Errorf("expected 'fail_res is succeeded' to be false")
	}
	if !executor.evaluateWhen("fail_res is failed", vars) {
		t.Errorf("expected 'fail_res is failed' to be true")
	}
	if !executor.evaluateWhen("skipped_res is skipped", vars) {
		t.Errorf("expected 'skipped_res is skipped' to be true")
	}
	if !executor.evaluateWhen("unreach_res is unreachable", vars) {
		t.Errorf("expected 'unreach_res is unreachable' to be true")
	}
	if !executor.evaluateWhen("fail_res is not succeeded", vars) {
		t.Errorf("expected 'fail_res is not succeeded' to be true")
	}
}

func TestFactsDateTimeNestedResolution(t *testing.T) {
	output := `
gathered_facts:
  ansible_hostname: web01
  ansible_os_family: RedHat
  ansible_date_time:
    epoch: "1729900000"
    iso8601: "2026-10-26T11:00:00Z"
    date: "2026-10-26"
`
	parsed := parseFacts(output)
	if parsed == nil {
		t.Fatalf("parseFacts returned nil")
	}
	if parsed["ansible_os_family"] != "RedHat" {
		t.Errorf("expected ansible_os_family=RedHat, got %v", parsed["ansible_os_family"])
	}

	dateTime, ok := parsed["ansible_date_time"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected ansible_date_time to be map, got %T: %#v", parsed["ansible_date_time"], parsed["ansible_date_time"])
	}
	if dateTime["epoch"] != "1729900000" {
		t.Errorf("expected epoch=1729900000, got %v", dateTime["epoch"])
	}

	inv := inventory.NewInventory()
	executor := NewExecutor(inv)
	resolvedEpoch := executor.resolveString("/etc/nginx.conf.bak.{{ ansible_date_time.epoch }}", parsed)
	if resolvedEpoch != "/etc/nginx.conf.bak.1729900000" {
		t.Errorf("expected resolved string with epoch, got: %s", resolvedEpoch)
	}
}

func TestDict2ItemsInPlaybook(t *testing.T) {
	inv := inventory.NewInventory()
	executor := NewExecutor(inv)

	vars := map[string]interface{}{
		"my_map": map[string]interface{}{
			"k1": "v1",
			"k2": "v2",
		},
	}

	resolved := executor.resolveVarExpr("my_map | dict2items", vars)
	items, ok := resolved.([]map[string]interface{})
	if !ok {
		t.Fatalf("expected []map[string]interface{}, got %T", resolved)
	}
	if len(items) != 2 {
		t.Errorf("expected 2 items, got %d", len(items))
	}
}

func TestTaskLevelEnvironment(t *testing.T) {
	inv := inventory.NewInventory()
	host := &inventory.Host{Name: "localhost", Address: "127.0.0.1"}
	inv.AllHosts[host.Name] = host

	executor := NewExecutor(inv)
	play := &Play{
		Hosts: "localhost",
		Environment: map[string]string{
			"GLOBAL_VAR": "play_val",
			"OVERRIDE":   "from_play",
		},
		Tasks: []*Task{
			{
				Name:       "test env",
				ModuleName: "debug",
				Environment: map[string]string{
					"OVERRIDE": "from_task",
					"TASK_VAR": "task_val",
				},
				Params: map[string]interface{}{
					"msg": "ok",
				},
			},
		},
	}

	res, err := executor.ExecutePlay(play)
	if err != nil {
		t.Fatalf("ExecutePlay failed: %v", err)
	}
	if len(res.Hosts["localhost"].Tasks) != 1 {
		t.Fatalf("expected 1 task executed")
	}
}
