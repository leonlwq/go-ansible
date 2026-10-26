package module

import (
	"strings"
	"testing"

	"go-ansible/pkg/ssh"
)

func TestAsyncStatusModuleValidate(t *testing.T) {
	m := &AsyncStatusModule{}
	if m.Name() != "async_status" {
		t.Fatalf("expected module name 'async_status', got %s", m.Name())
	}

	if err := m.Validate(map[string]interface{}{}); err == nil {
		t.Errorf("expected validation error when jid is missing")
	}

	if err := m.Validate(map[string]interface{}{"jid": "123.456"}); err != nil {
		t.Errorf("unexpected validation error: %v", err)
	}
}

func TestAsyncStatusModuleMissingJob(t *testing.T) {
	m := &AsyncStatusModule{}
	conn := ssh.NewLocalConnection(nil)
	_ = conn.Connect()
	defer conn.Close()

	res, err := m.Execute(conn, map[string]interface{}{
		"jid": "nonexistent_job_id_999999",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res.Failed {
		t.Errorf("expected failed=true for nonexistent job")
	}
	if !strings.Contains(res.Message, "could not find async job") {
		t.Errorf("unexpected message: %s", res.Message)
	}
}

func TestAsyncStatusModuleCleanup(t *testing.T) {
	m := &AsyncStatusModule{}
	conn := ssh.NewLocalConnection(nil)
	_ = conn.Connect()
	defer conn.Close()

	res, err := m.Execute(conn, map[string]interface{}{
		"jid":  "dummy_job_cleanup_123",
		"mode": "cleanup",
	})
	if err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}
	if res.Failed {
		t.Errorf("cleanup should not fail")
	}
	if res.Data["finished"] != 1 {
		t.Errorf("expected finished=1 in data")
	}
}
