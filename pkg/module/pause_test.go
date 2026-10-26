package module

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestPauseModuleValidateAndName(t *testing.T) {
	m := &PauseModule{}
	if m.Name() != "pause" {
		t.Fatalf("expected module name 'pause', got %s", m.Name())
	}
	if err := m.Validate(nil); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestPauseModuleSeconds(t *testing.T) {
	out := &bytes.Buffer{}
	m := &PauseModule{Stdout: out}

	start := time.Now()
	res, err := m.Execute(nil, map[string]interface{}{
		"seconds": 0.05,
		"prompt":  "Short sleep test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Changed {
		t.Errorf("pause should never report changed")
	}
	if time.Since(start) < 40*time.Millisecond {
		t.Errorf("expected sleep for at least 40ms")
	}
	if !strings.Contains(out.String(), "Short sleep test") {
		t.Errorf("expected output to contain prompt, got: %s", out.String())
	}
}

func TestPauseModuleUserInput(t *testing.T) {
	in := bytes.NewBufferString("deploy-confirm\n")
	out := &bytes.Buffer{}
	m := &PauseModule{
		Stdin:  in,
		Stdout: out,
	}

	res, err := m.Execute(nil, map[string]interface{}{
		"prompt": "Enter deployment target:",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Stdout != "deploy-confirm" {
		t.Errorf("expected stdout 'deploy-confirm', got: %s", res.Stdout)
	}
	if userInput, ok := res.Data["user_input"].(string); !ok || userInput != "deploy-confirm" {
		t.Errorf("expected user_input data 'deploy-confirm', got: %v", res.Data["user_input"])
	}
	if !strings.Contains(out.String(), "Enter deployment target:") {
		t.Errorf("expected output to contain prompt, got: %s", out.String())
	}
}

func TestPauseModuleCheckMode(t *testing.T) {
	out := &bytes.Buffer{}
	m := &PauseModule{
		Stdout: out,
	}

	start := time.Now()
	res, err := m.Execute(nil, map[string]interface{}{
		"seconds":    100, // 超长休眠
		"check_mode": true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Errorf("check mode should skip pause immediately")
	}
	if res.Changed {
		t.Errorf("expected changed=false in check mode")
	}
	if res.Message != "Paused (check mode skipped)" {
		t.Errorf("unexpected check mode message: %s", res.Message)
	}
}
