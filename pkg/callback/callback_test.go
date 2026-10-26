package callback

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"go-ansible/pkg/playbook"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{name: ""},
		{name: "default"},
		{name: "json"},
		{name: "yaml", wantErr: true},
	}

	for _, tt := range tests {
		renderer, err := New(tt.name)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("New(%q) expected error", tt.name)
			}
			continue
		}
		if err != nil {
			t.Fatalf("New(%q) error = %v", tt.name, err)
		}
		if renderer == nil {
			t.Fatalf("New(%q) returned nil renderer", tt.name)
		}
	}
}

func TestDefaultRendererRender(t *testing.T) {
	result := &playbook.PlaybookResult{
		Plays: []*playbook.PlayResult{
			{
				Name: "deploy",
				Hosts: map[string]*playbook.HostResult{
					"web2": {
						Tasks: []*playbook.TaskResult{
							{Name: "skip task", Skipped: true},
						},
					},
					"web1": {
						Tasks: []*playbook.TaskResult{
							{Name: "changed task", Changed: true, Stdout: "done", Diff: "--- before\n+++ after\n-old\n+new"},
							{Name: "failed task", Failed: true, Message: "boom", Stderr: "trace", Item: map[string]interface{}{"cmd": "restart"}},
						},
					},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := (&DefaultRenderer{}).Render(&buf, result, "restart,check"); err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "PLAY [deploy]") {
		t.Fatalf("expected play header, got %q", output)
	}
	if !strings.Contains(output, "TASK [changed task]") {
		t.Fatalf("expected task output, got %q", output)
	}
	if !strings.Contains(output, "changed: [web1]") {
		t.Fatalf("expected changed output, got %q", output)
	}
	if !strings.Contains(output, "fatal: [web1]: FAILED!") {
		t.Fatalf("expected failed output, got %q", output)
	}
	if !strings.Contains(output, "skipping: [web2]") {
		t.Fatalf("expected skipped output, got %q", output)
	}
	if !strings.Contains(output, "PLAY RECAP") {
		t.Fatalf("expected recap, got %q", output)
	}
	if !strings.Contains(output, "Tags: restart,check") {
		t.Fatalf("expected tags, got %q", output)
	}
	if !strings.Contains(output, "diff:") || !strings.Contains(output, "-old") || !strings.Contains(output, "+new") {
		t.Fatalf("expected diff output, got %q", output)
	}

	web1Index := strings.Index(output, "web1")
	web2Index := strings.Index(output, "web2")
	if web1Index == -1 || web2Index == -1 || web1Index > web2Index {
		t.Fatalf("expected sorted hosts in output, got %q", output)
	}
}

func TestJSONRendererRender(t *testing.T) {
	result := &playbook.PlaybookResult{
		Plays: []*playbook.PlayResult{
			{
				Name: "deploy",
				Hosts: map[string]*playbook.HostResult{
					"web1": {
						Tasks: []*playbook.TaskResult{
							{Name: "ok task", Diff: "--- before\n+++ after\n-old\n+new", Data: map[string]interface{}{"content": "aGVsbG8=", "encoding": "base64"}},
						},
					},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := (&JSONRenderer{}).Render(&buf, result, "restart, check"); err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, `"name": "deploy"`) {
		t.Fatalf("expected play name in JSON, got %q", output)
	}
	if !strings.Contains(output, `"tags": [`) {
		t.Fatalf("expected tags in JSON, got %q", output)
	}
	if !strings.Contains(output, `"restart"`) || !strings.Contains(output, `"check"`) {
		t.Fatalf("expected split tags in JSON, got %q", output)
	}
	if !strings.Contains(output, `"diff"`) || !strings.Contains(output, `-old`) {
		t.Fatalf("expected diff in JSON, got %q", output)
	}
	if !strings.Contains(output, `"data"`) || !strings.Contains(output, `"content": "aGVsbG8="`) {
		t.Fatalf("expected module data in JSON, got %q", output)
	}
}

func TestHasFailures(t *testing.T) {
	if HasFailures(nil) {
		t.Fatal("expected nil result to have no failures")
	}

	okResult := &playbook.PlaybookResult{
		Plays: []*playbook.PlayResult{
			{Hosts: map[string]*playbook.HostResult{"web1": {Tasks: []*playbook.TaskResult{{Name: "ok"}}}}},
		},
	}
	if HasFailures(okResult) {
		t.Fatal("expected no failures")
	}

	failResult := &playbook.PlaybookResult{
		Plays: []*playbook.PlayResult{
			{Hosts: map[string]*playbook.HostResult{"web1": {Tasks: []*playbook.TaskResult{{Name: "bad", Failed: true}}}}},
		},
	}
	if !HasFailures(failResult) {
		t.Fatal("expected failure to be detected")
	}
}

func TestProfileTasksAndTimer(t *testing.T) {
	now := time.Now()
	result := &playbook.PlaybookResult{
		Start: now,
		End:   now.Add(3 * time.Second),
		Plays: []*playbook.PlayResult{
			{
				Name: "timing play",
				Hosts: map[string]*playbook.HostResult{
					"web1": {
						Tasks: []*playbook.TaskResult{
							{Name: "quick task", Start: now, End: now.Add(100 * time.Millisecond)},
							{Name: "slow task", Start: now, End: now.Add(2500 * time.Millisecond)},
						},
					},
				},
			},
		},
	}

	renderer, err := NewWithCallbacks("default", []string{"profile_tasks", "timer"})
	if err != nil {
		t.Fatalf("NewWithCallbacks error: %v", err)
	}

	var buf bytes.Buffer
	if err := renderer.Render(&buf, result, ""); err != nil {
		t.Fatalf("Render error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "slow task") || !strings.Contains(output, "quick task") {
		t.Fatalf("expected profile_tasks output with task names, got: %s", output)
	}
	if !strings.Contains(output, "Playbook run took") {
		t.Fatalf("expected timer output, got: %s", output)
	}
}

func TestCallbackUnreachableAndIgnoredRecap(t *testing.T) {
	result := &playbook.PlaybookResult{
		Plays: []*playbook.PlayResult{
			{
				Name: "cluster resilience play",
				Hosts: map[string]*playbook.HostResult{
					"web-unreachable": {
						Tasks: []*playbook.TaskResult{
							{Name: "task1", Unreachable: true, Failed: true},
						},
					},
					"web-ignored": {
						Tasks: []*playbook.TaskResult{
							{Name: "task2", Unreachable: true, Ignored: true, Failed: false},
							{Name: "task3", Changed: true},
						},
					},
				},
			},
		},
	}

	renderer, err := New("default")
	if err != nil {
		t.Fatalf("New callback error = %v", err)
	}

	var buf bytes.Buffer
	if err := renderer.Render(&buf, result, ""); err != nil {
		t.Fatalf("Render error = %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "fatal: [web-unreachable]: UNREACHABLE!") {
		t.Errorf("expected UNREACHABLE message for web-unreachable, got: %s", out)
	}
	if !strings.Contains(out, "ignoring unreachable: [web-ignored]") {
		t.Errorf("expected ignoring unreachable message for web-ignored, got: %s", out)
	}
	if !strings.Contains(out, "unreachable=1") || !strings.Contains(out, "ignored=0") {
		t.Errorf("expected recap unreachable=1 and ignored=0, got: %s", out)
	}
	if !strings.Contains(out, "unreachable=1") || !strings.Contains(out, "ignored=1") {
		t.Errorf("expected recap unreachable=1 and ignored=1, got: %s", out)
	}
}
