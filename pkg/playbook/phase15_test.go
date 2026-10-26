package playbook

import (
	"fmt"
	"strings"
	"testing"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/module"
	"go-ansible/pkg/ssh"
)

type mockPhase15PlaybookConn struct {
	failCount    int
	callCount    int
	bootID       string
	executedCmds []string
}

func (m *mockPhase15PlaybookConn) Connect() error {
	m.callCount++
	if m.callCount <= m.failCount {
		return fmt.Errorf("dial tcp 192.168.1.100:22: connect: connection refused")
	}
	return nil
}

func (m *mockPhase15PlaybookConn) Close() error {
	return nil
}

func (m *mockPhase15PlaybookConn) Execute(cmd string) (*ssh.Result, error) {
	m.executedCmds = append(m.executedCmds, cmd)
	m.callCount++
	if m.callCount <= m.failCount {
		return nil, fmt.Errorf("dial tcp 192.168.1.100:22: connect: connection refused")
	}
	if strings.Contains(cmd, "reboot") {
		m.bootID = "boot-new-999"
		return &ssh.Result{ExitCode: 0, Stdout: "rebooting"}, nil
	}
	if strings.Contains(cmd, "boot_id") || strings.Contains(cmd, "uptime") {
		return &ssh.Result{ExitCode: 0, Stdout: m.bootID}, nil
	}
	if strings.Contains(cmd, "whoami") {
		return &ssh.Result{ExitCode: 0, Stdout: "root\n"}, nil
	}
	return &ssh.Result{ExitCode: 0, Stdout: "ok"}, nil
}

func (m *mockPhase15PlaybookConn) ExecuteWithBecome(cmd string) (*ssh.Result, error) {
	return m.Execute(cmd)
}

func (m *mockPhase15PlaybookConn) Upload(local, remote string) error   { return nil }
func (m *mockPhase15PlaybookConn) Download(remote, local string) error { return nil }

func TestPhase15ModuleRegistrationAndDoc(t *testing.T) {
	registry := module.NewRegistry()

	modules := []string{"reboot", "wait_for_connection"}
	for _, name := range modules {
		mod, err := registry.Get(name)
		if err != nil || mod == nil {
			t.Fatalf("expected module %s to be registered in NewRegistry()", name)
		}
		if mod.Name() != name {
			t.Fatalf("expected module.Name() == %s, got %s", name, mod.Name())
		}

		doc, ok := module.GetModuleDoc(name)
		if !ok || doc == nil {
			t.Fatalf("expected doc for module %s", name)
		}
		if doc.Name != name {
			t.Fatalf("doc.Name = %s, expected %s", doc.Name, name)
		}
		if doc.ShortDesc == "" || doc.Description == "" {
			t.Fatalf("descriptions should not be empty for %s", name)
		}
		if len(doc.Options) == 0 {
			t.Fatalf("options should not be empty for %s", name)
		}
		if doc.Examples == "" {
			t.Fatalf("examples should not be empty for %s", name)
		}
	}
}

func TestPlaybookTaskIgnoreUnreachable(t *testing.T) {
	t.Run("ignore_unreachable=true keeps host alive for later tasks", func(t *testing.T) {
		inv := inventory.NewInventory()
		host := &inventory.Host{Name: "web1", Address: "192.168.1.100", Port: 22}
		inv.AllHosts[host.Name] = host

		executor := NewExecutor(inv)
		mockConn := &mockPhase15PlaybookConn{
			failCount: 1, // first command fails with unreachable error, subsequent commands succeed
		}
		executor.pool.Store(host.Name, mockConn)

		play := &Play{
			Hosts: "web1",
			Tasks: []*Task{
				{
					Name:              "unreachable task with ignore_unreachable",
					ModuleName:        "command",
					Params:            map[string]interface{}{"_raw_params": "uptime"},
					IgnoreUnreachable: true,
					Register:          "probe_result",
				},
				{
					Name:       "recovery task after network comes back",
					ModuleName: "debug",
					Params: map[string]interface{}{
						"msg": "web1 recovered",
					},
				},
			},
		}

		result, err := executor.ExecutePlay(play)
		if err != nil {
			t.Fatalf("ExecutePlay error = %v", err)
		}

		hostResult := result.Hosts["web1"]
		if hostResult == nil || len(hostResult.Tasks) != 2 {
			t.Fatalf("expected 2 tasks executed on web1, got %v", hostResult)
		}

		task1 := hostResult.Tasks[0]
		if !task1.Unreachable {
			t.Errorf("task1 expected Unreachable=true")
		}
		if !task1.Ignored {
			t.Errorf("task1 expected Ignored=true")
		}
		if task1.Failed {
			t.Errorf("task1 expected Failed=false because ignore_unreachable=true")
		}

		task2 := hostResult.Tasks[1]
		if task2.Failed || task2.Skipped {
			t.Errorf("task2 expected to execute ok, got failed=%v, skipped=%v", task2.Failed, task2.Skipped)
		}
	})

	t.Run("ignore_unreachable=false marks host failed and skips later tasks", func(t *testing.T) {
		inv := inventory.NewInventory()
		host := &inventory.Host{Name: "web2", Address: "192.168.1.101", Port: 22}
		inv.AllHosts[host.Name] = host

		executor := NewExecutor(inv)
		mockConn := &mockPhase15PlaybookConn{
			failCount: 99, // always fails with unreachable error
		}
		executor.pool.Store(host.Name, mockConn)

		play := &Play{
			Hosts: "web2",
			Tasks: []*Task{
				{
					Name:              "unreachable task without ignore",
					ModuleName:        "command",
					Params:            map[string]interface{}{"_raw_params": "uptime"},
					IgnoreUnreachable: false,
				},
				{
					Name:       "should be skipped",
					ModuleName: "debug",
					Params: map[string]interface{}{
						"msg": "should not run",
					},
				},
			},
		}

		result, err := executor.ExecutePlay(play)
		if err != nil {
			t.Fatalf("ExecutePlay error = %v", err)
		}

		hostResult := result.Hosts["web2"]
		if hostResult == nil || len(hostResult.Tasks) != 1 {
			t.Fatalf("expected exactly 1 task on web2, got %v", hostResult)
		}

		task1 := hostResult.Tasks[0]
		if !task1.Unreachable {
			t.Errorf("expected Unreachable=true")
		}
		if !task1.Failed {
			t.Errorf("expected Failed=true")
		}
	})
}

func TestPlaybookRebootAndWaitForConnection(t *testing.T) {
	inv := inventory.NewInventory()
	host := &inventory.Host{Name: "web3", Address: "127.0.0.1", Port: 22}
	inv.AllHosts[host.Name] = host

	executor := NewExecutor(inv)
	mockConn := &mockPhase15PlaybookConn{
		bootID: "boot-old-111",
	}
	executor.pool.Store(host.Name, mockConn)

	t.Run("reboot in check mode", func(t *testing.T) {
		res, err := executor.executeReboot(host, map[string]interface{}{
			"_ansible_check_mode": true,
		})
		if err != nil {
			t.Fatalf("executeReboot error = %v", err)
		}
		if !res.Changed {
			t.Errorf("expected changed=true in check mode")
		}
		if rebooted, ok := res.Data["rebooted"].(bool); !ok || !rebooted {
			t.Errorf("expected rebooted=true in data")
		}
	})

	t.Run("reboot execution", func(t *testing.T) {
		res, err := executor.executeReboot(host, map[string]interface{}{
			"reboot_timeout": 5,
			"reboot_command": "echo fake_reboot",
			"test_command":   "whoami",
			"search_regex":   "root",
		})
		if err != nil {
			t.Fatalf("executeReboot error = %v", err)
		}
		if !res.Changed {
			t.Errorf("expected changed=true")
		}
		if rebooted, ok := res.Data["rebooted"].(bool); !ok || !rebooted {
			t.Errorf("expected rebooted=true in data")
		}
	})

	t.Run("wait_for_connection in check mode", func(t *testing.T) {
		res, err := executor.executeWaitForConnection(host, map[string]interface{}{
			"_ansible_check_mode": true,
		})
		if err != nil {
			t.Fatalf("executeWaitForConnection error = %v", err)
		}
		if res.Changed {
			t.Errorf("expected changed=false")
		}
		if res.Data["elapsed"].(int) != 0 {
			t.Errorf("expected elapsed=0 in check mode")
		}
	})

	t.Run("wait_for_connection execution", func(t *testing.T) {
		res, err := executor.executeWaitForConnection(host, map[string]interface{}{
			"timeout": 5,
			"sleep":   1,
		})
		if err != nil {
			t.Fatalf("executeWaitForConnection error = %v", err)
		}
		if res.Changed {
			t.Errorf("expected changed=false")
		}
	})
}

type mockFailingTaskConn struct {
	exitCode int
	stdout   string
	stderr   string
}

func (m *mockFailingTaskConn) Connect() error                      { return nil }
func (m *mockFailingTaskConn) Close() error                        { return nil }
func (m *mockFailingTaskConn) Upload(local, remote string) error   { return nil }
func (m *mockFailingTaskConn) Download(remote, local string) error { return nil }
func (m *mockFailingTaskConn) ExecuteWithBecome(cmd string) (*ssh.Result, error) {
	return m.Execute(cmd)
}
func (m *mockFailingTaskConn) Execute(cmd string) (*ssh.Result, error) {
	return &ssh.Result{
		ExitCode: m.exitCode,
		Stdout:   m.stdout,
		Stderr:   m.stderr,
	}, nil
}

func TestTaskModuleFailureIsNotUnreachable(t *testing.T) {
	inv := inventory.NewInventory()
	host := &inventory.Host{Name: "app-node", Address: "10.0.0.1", Port: 22}
	inv.AllHosts[host.Name] = host

	executor := NewExecutor(inv)
	mockConn := &mockFailingTaskConn{
		exitCode: 1,
		stdout:   "timed out waiting for 127.0.0.1:57716\n",
	}
	executor.pool.Store(host.Name, mockConn)

	// 任务执行失败（例如 wait_for 超时或命令输出包含 connection refused / timed out）
	play := &Play{
		Hosts: "app-node",
		Tasks: []*Task{
			{
				Name:       "wait for port timeout check",
				ModuleName: "wait_for",
				Params: map[string]interface{}{
					"port":    57716,
					"timeout": 1,
				},
			},
			{
				Name:       "subsequent task that should NOT run because previous task failed",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "after wait_for",
				},
			},
		},
	}

	result, err := executor.ExecutePlay(play)
	if err != nil {
		t.Fatalf("ExecutePlay error = %v", err)
	}

	hostResult := result.Hosts["app-node"]
	if hostResult == nil || len(hostResult.Tasks) != 1 {
		t.Fatalf("expected 1 task executed on app-node, got %v", hostResult)
	}

	task1 := hostResult.Tasks[0]
	// 关键断言：主机应为可达（Unreachable=false），但任务属于业务失败（Failed=true）
	if task1.Unreachable {
		t.Errorf("task1 should NOT be Unreachable when SSH connection is alive")
	}
	if !task1.Failed {
		t.Errorf("task1 should be Failed=true")
	}
}
