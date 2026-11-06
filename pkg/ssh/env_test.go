package ssh

import (
	"strings"
	"testing"
)

type mockConnection struct {
	lastCmd       string
	lastBecomeCmd string
}

func (m *mockConnection) Connect() error                               { return nil }
func (m *mockConnection) Close() error                                 { return nil }
func (m *mockConnection) Upload(localPath, remotePath string) error   { return nil }
func (m *mockConnection) Download(remotePath, localPath string) error { return nil }

func (m *mockConnection) Execute(cmd string) (*Result, error) {
	m.lastCmd = cmd
	return &Result{Stdout: "mock output"}, nil
}

func (m *mockConnection) ExecuteWithBecome(cmd string) (*Result, error) {
	m.lastBecomeCmd = cmd
	return &Result{Stdout: "mock become output"}, nil
}

func TestEnvConnection(t *testing.T) {
	mock := &mockConnection{}
	env := map[string]string{
		"HTTP_PROXY": "http://proxy.example.com:8080",
		"TEST_VAR":   "hello 'world'",
	}

	conn := NewEnvConnection(mock, env)
	res, err := conn.Execute("echo 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Stdout != "mock output" {
		t.Fatalf("expected 'mock output', got %s", res.Stdout)
	}

	expectedPrefix1 := "HTTP_PROXY='http://proxy.example.com:8080'"
	expectedPrefix2 := "TEST_VAR='hello '\"'\"'world'\"'\"''"
	if !strings.Contains(mock.lastCmd, expectedPrefix1) {
		t.Errorf("expected lastCmd to contain %s, got %s", expectedPrefix1, mock.lastCmd)
	}
	if !strings.Contains(mock.lastCmd, expectedPrefix2) {
		t.Errorf("expected lastCmd to contain %s, got %s", expectedPrefix2, mock.lastCmd)
	}
	if !strings.HasSuffix(mock.lastCmd, "echo 1") {
		t.Errorf("expected lastCmd to end with 'echo 1', got %s", mock.lastCmd)
	}

	// Test ExecuteWithBecome
	_, err = conn.ExecuteWithBecome("whoami")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(mock.lastBecomeCmd, "whoami") {
		t.Errorf("expected lastBecomeCmd to end with 'whoami', got %s", mock.lastBecomeCmd)
	}

	// Test nested NewEnvConnection merge
	moreEnv := map[string]string{
		"ANOTHER_VAR": "value2",
	}
	nestedConn := NewEnvConnection(conn, moreEnv)
	_, _ = nestedConn.Execute("ls")
	if !strings.Contains(mock.lastCmd, "ANOTHER_VAR='value2'") || !strings.Contains(mock.lastCmd, expectedPrefix1) {
		t.Errorf("expected merged env in lastCmd, got %s", mock.lastCmd)
	}
}

