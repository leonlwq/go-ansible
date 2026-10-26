package module

import (
	"fmt"
	"strings"
	"testing"

	"go-ansible/pkg/ssh"
)

type mockPhase15Conn struct {
	connectErr   error
	executeErr   error
	connected    bool
	bootID       string
	executedCmds []string
	rebootCalls  int
	connectCalls int
}

func (c *mockPhase15Conn) Connect() error {
	c.connectCalls++
	if c.connectErr != nil {
		return c.connectErr
	}
	c.connected = true
	return nil
}

func (c *mockPhase15Conn) Close() error {
	c.connected = false
	return nil
}

func (c *mockPhase15Conn) Execute(cmd string) (*ssh.Result, error) {
	c.executedCmds = append(c.executedCmds, cmd)
	if c.executeErr != nil {
		return nil, c.executeErr
	}
	if strings.Contains(cmd, "reboot") {
		c.rebootCalls++
		c.bootID = "new-boot-id-456"
		return &ssh.Result{ExitCode: 0, Stdout: "rebooting"}, nil
	}
	if strings.Contains(cmd, "boot_id") || strings.Contains(cmd, "uptime") {
		return &ssh.Result{ExitCode: 0, Stdout: c.bootID}, nil
	}
	if strings.Contains(cmd, "whoami") {
		return &ssh.Result{ExitCode: 0, Stdout: "root\n"}, nil
	}
	if strings.Contains(cmd, "echo ping") {
		return &ssh.Result{ExitCode: 0, Stdout: "ping\n"}, nil
	}
	return &ssh.Result{ExitCode: 0, Stdout: "ok"}, nil
}

func (c *mockPhase15Conn) ExecuteWithBecome(cmd string) (*ssh.Result, error) {
	return c.Execute(cmd)
}

func (c *mockPhase15Conn) Upload(local, remote string) error   { return nil }
func (c *mockPhase15Conn) Download(remote, local string) error { return nil }

func TestWaitForConnectionModule(t *testing.T) {
	mod := &WaitForConnectionModule{}

	t.Run("check mode", func(t *testing.T) {
		conn := &mockPhase15Conn{}
		res, err := mod.Execute(conn, map[string]interface{}{
			"_ansible_check_mode": true,
		})
		if err != nil {
			t.Fatalf("unexpected error in check mode: %v", err)
		}
		if res.Changed {
			t.Errorf("expected changed=false in check mode, got true")
		}
		if res.Data["elapsed"].(int) != 0 {
			t.Errorf("expected elapsed=0, got %v", res.Data["elapsed"])
		}
	})

	t.Run("successful connection", func(t *testing.T) {
		conn := &mockPhase15Conn{}
		res, err := mod.Execute(conn, map[string]interface{}{
			"timeout": 10,
			"sleep":   1,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Failed {
			t.Errorf("expected success, got failed: %s", res.Message)
		}
		if res.Changed {
			t.Errorf("expected changed=false, got true")
		}
	})

	t.Run("connection timeout", func(t *testing.T) {
		conn := &mockPhase15Conn{
			connectErr: fmt.Errorf("connection refused"),
		}
		res, err := mod.Execute(conn, map[string]interface{}{
			"timeout": 1,
			"sleep":   1,
		})
		if err == nil {
			t.Fatalf("expected timeout error, got nil")
		}
		if res == nil || !res.Failed {
			t.Errorf("expected result.Failed=true on timeout")
		}
	})
}

func TestRebootModule(t *testing.T) {
	mod := &RebootModule{}

	t.Run("check mode", func(t *testing.T) {
		conn := &mockPhase15Conn{}
		res, err := mod.Execute(conn, map[string]interface{}{
			"_ansible_check_mode": true,
		})
		if err != nil {
			t.Fatalf("unexpected error in check mode: %v", err)
		}
		if !res.Changed {
			t.Errorf("expected changed=true in check mode, got false")
		}
		if rebooted, ok := res.Data["rebooted"].(bool); !ok || !rebooted {
			t.Errorf("expected rebooted=true in data")
		}
	})

	t.Run("successful reboot and boot_id validation", func(t *testing.T) {
		conn := &mockPhase15Conn{
			bootID: "initial-boot-id-123",
		}
		res, err := mod.Execute(conn, map[string]interface{}{
			"reboot_timeout": 10,
			"reboot_command": "echo fake_reboot",
			"test_command":   "whoami",
			"search_regex":   "root",
		})
		if err != nil {
			t.Fatalf("unexpected error during reboot: %v", err)
		}
		if !res.Changed {
			t.Errorf("expected changed=true, got false")
		}
		if conn.rebootCalls != 1 {
			t.Errorf("expected 1 reboot command execution, got %d", conn.rebootCalls)
		}
		if conn.bootID != "new-boot-id-456" {
			t.Errorf("expected boot_id to update, got %s", conn.bootID)
		}
	})

	t.Run("search_regex mismatch causes timeout", func(t *testing.T) {
		conn := &mockPhase15Conn{
			bootID: "initial-boot-id-123",
		}
		res, err := mod.Execute(conn, map[string]interface{}{
			"reboot_timeout": 1,
			"reboot_command": "echo fake_reboot",
			"test_command":   "whoami",
			"search_regex":   "non_matching_user_xyz",
		})
		if err == nil {
			t.Fatalf("expected timeout error due to search_regex mismatch")
		}
		if res == nil || !res.Failed {
			t.Errorf("expected result.Failed=true on regex mismatch timeout")
		}
	})
}
