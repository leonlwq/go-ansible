package module

import (
	"strings"
	"testing"

	"go-ansible/pkg/ssh"
)

func TestRegistryIncludesWaitForModule(t *testing.T) {
	registry := NewRegistry()
	mod, err := registry.Get("wait_for")
	if err != nil {
		t.Fatalf("Get(wait_for) error = %v", err)
	}
	if mod.Name() != "wait_for" {
		t.Fatalf("expected wait_for module, got %q", mod.Name())
	}
}

func TestWaitForValidate(t *testing.T) {
	mod := &WaitForModule{}

	if err := mod.Validate(map[string]interface{}{}); err == nil {
		t.Fatal("expected error when port and path are missing")
	}

	if err := mod.Validate(map[string]interface{}{"port": 8080}); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	if err := mod.Validate(map[string]interface{}{"path": "/tmp/app.log"}); err != nil {
		t.Fatalf("Validate(path) error = %v", err)
	}
}

func TestGetParamModeFormatsNumericModesAsOctal(t *testing.T) {
	params := map[string]interface{}{
		"mode": 420,
	}

	if got := GetParamMode(params, "mode", ""); got != "644" {
		t.Fatalf("expected mode 644, got %q", got)
	}

	params["mode"] = "0755"
	if got := GetParamMode(params, "mode", ""); got != "0755" {
		t.Fatalf("expected mode 0755, got %q", got)
	}
}

func TestWaitForScriptGenerationAndSyntax(t *testing.T) {
	spec := waitForSpec{
		Host:    "127.0.0.1",
		Port:    "57716",
		Delay:   0,
		Timeout: 1,
		State:   "started",
	}

	script, successMsg, timeoutMsg, err := buildWaitForScript(spec)
	if err != nil {
		t.Fatalf("buildWaitForScript error = %v", err)
	}

	// 验证不再包含引起 syntax error 的内嵌单引号断裂
	if strings.Contains(script, "unexpected end of file") {
		t.Fatalf("script contains unexpected text: %s", script)
	}

	if !strings.Contains(script, "base64") {
		t.Fatalf("expected base64 pipe in script, got: %s", script)
	}

	if successMsg != "wait_for condition satisfied on 127.0.0.1:57716" {
		t.Errorf("unexpected successMsg: %s", successMsg)
	}

	if timeoutMsg != "timed out waiting for 127.0.0.1:57716" {
		t.Errorf("unexpected timeoutMsg: %s", timeoutMsg)
	}
}

type mockWaitForConn struct {
	exitCode int
	stdout   string
	stderr   string
	execErr  error
}

func (m *mockWaitForConn) Connect() error                      { return nil }
func (m *mockWaitForConn) Close() error                        { return nil }
func (m *mockWaitForConn) Upload(local, remote string) error   { return nil }
func (m *mockWaitForConn) Download(remote, local string) error { return nil }
func (m *mockWaitForConn) ExecuteWithBecome(cmd string) (*ssh.Result, error) {
	return m.Execute(cmd)
}
func (m *mockWaitForConn) Execute(cmd string) (*ssh.Result, error) {
	if m.execErr != nil {
		return nil, m.execErr
	}
	return &ssh.Result{
		ExitCode: m.exitCode,
		Stdout:   m.stdout,
		Stderr:   m.stderr,
	}, nil
}

func TestWaitForModuleExecute(t *testing.T) {
	mod := &WaitForModule{}

	t.Run("port condition satisfied", func(t *testing.T) {
		conn := &mockWaitForConn{
			exitCode: 0,
			stdout:   "wait_for condition satisfied on 127.0.0.1:57716\n",
		}
		res, err := mod.Execute(conn, map[string]interface{}{
			"port": 57716,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Failed {
			t.Errorf("expected Failed=false")
		}
		if res.Message != "wait_for condition satisfied on 127.0.0.1:57716" {
			t.Errorf("expected success message, got: %s", res.Message)
		}
	})

	t.Run("port condition timeout", func(t *testing.T) {
		conn := &mockWaitForConn{
			exitCode: 1,
			stdout:   "timed out waiting for 127.0.0.1:57716\n",
		}
		res, err := mod.Execute(conn, map[string]interface{}{
			"port": 57716,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Failed {
			t.Errorf("expected Failed=true")
		}
		if res.Message != "timed out waiting for 127.0.0.1:57716" {
			t.Errorf("expected timeout message, got: %s", res.Message)
		}
	})

	t.Run("remote bash execution error preserved in stderr", func(t *testing.T) {
		conn := &mockWaitForConn{
			exitCode: 2,
			stderr:   "bash: syntax error near unexpected token\n",
		}
		res, err := mod.Execute(conn, map[string]interface{}{
			"port": 57716,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Failed {
			t.Errorf("expected Failed=true")
		}
		if res.Message != "bash: syntax error near unexpected token" {
			t.Errorf("expected stderr preserved in message, got: %s", res.Message)
		}
	})
}
