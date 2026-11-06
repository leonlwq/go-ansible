package module

import (
	"strings"
	"testing"

	"go-ansible/pkg/ssh"
)

type mockPipConn struct {
	executedCmds []string
	customOutput map[string]*ssh.Result
}

func newMockPipConn() *mockPipConn {
	return &mockPipConn{
		executedCmds: make([]string, 0),
		customOutput: make(map[string]*ssh.Result),
	}
}

func (c *mockPipConn) Connect() error { return nil }
func (c *mockPipConn) Close() error   { return nil }
func (c *mockPipConn) Execute(cmd string) (*ssh.Result, error) {
	c.executedCmds = append(c.executedCmds, cmd)
	for pattern, res := range c.customOutput {
		if strings.Contains(cmd, pattern) {
			return res, nil
		}
	}
	return &ssh.Result{ExitCode: 0, Stdout: "success"}, nil
}
func (c *mockPipConn) ExecuteWithBecome(cmd string) (*ssh.Result, error) { return c.Execute(cmd) }
func (c *mockPipConn) Upload(local, remote string) error                 { return nil }
func (c *mockPipConn) Download(remote, local string) error               { return nil }
func (c *mockPipConn) User() string                                      { return "root" }

func TestPipModule_Validate(t *testing.T) {
	mod := &PipModule{}
	if err := mod.Validate(map[string]interface{}{}); err == nil {
		t.Fatal("expected error without name or requirements")
	}
	if err := mod.Validate(map[string]interface{}{"name": "flask"}); err != nil {
		t.Fatalf("unexpected error with name: %v", err)
	}
	if err := mod.Validate(map[string]interface{}{"requirements": "/app/req.txt"}); err != nil {
		t.Fatalf("unexpected error with requirements: %v", err)
	}
}

func TestPipModule_InstallPresentAndIdempotent(t *testing.T) {
	mod := &PipModule{}

	// 1. Initial install -> Changed = true
	conn1 := newMockPipConn()
	conn1.customOutput["pip3 install 'flask'"] = &ssh.Result{
		ExitCode: 0,
		Stdout:   "Collecting flask\nInstalling collected packages: flask\nSuccessfully installed flask-2.2.2\n",
	}
	res1, err := mod.Execute(conn1, map[string]interface{}{
		"name": "flask",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res1.Changed {
		t.Errorf("expected Changed=true on initial install")
	}

	// 2. Already satisfied -> Changed = false
	conn2 := newMockPipConn()
	conn2.customOutput["pip3 install 'flask'"] = &ssh.Result{
		ExitCode: 0,
		Stdout:   "Requirement already satisfied: flask in /usr/lib/python3/dist-packages (2.2.2)\n",
	}
	res2, err := mod.Execute(conn2, map[string]interface{}{
		"name": "flask",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res2.Changed {
		t.Errorf("expected Changed=false when package is already satisfied")
	}
}

func TestPipModule_CheckMode(t *testing.T) {
	mod := &PipModule{}

	// When package is already installed and matches version
	conn1 := newMockPipConn()
	conn1.customOutput["pip3 show 'requests'"] = &ssh.Result{
		ExitCode: 0,
		Stdout:   "Name: requests\nVersion: 2.28.1\nSummary: Python HTTP for Humans.\n",
	}
	res1, err := mod.Execute(conn1, map[string]interface{}{
		"name":                "requests",
		"version":             "2.28.1",
		"_ansible_check_mode": true,
	})
	if err != nil {
		t.Fatalf("check mode error: %v", err)
	}
	if res1.Changed {
		t.Errorf("expected Changed=false when version matches")
	}

	// When package version differs
	conn2 := newMockPipConn()
	conn2.customOutput["pip3 show 'requests'"] = &ssh.Result{
		ExitCode: 0,
		Stdout:   "Name: requests\nVersion: 2.25.0\nSummary: Python HTTP for Humans.\n",
	}
	res2, err := mod.Execute(conn2, map[string]interface{}{
		"name":                "requests",
		"version":             "2.28.1",
		"_ansible_check_mode": true,
	})
	if err != nil {
		t.Fatalf("check mode error: %v", err)
	}
	if !res2.Changed {
		t.Errorf("expected Changed=true when version differs")
	}
}

func TestPipModule_UninstallAbsent(t *testing.T) {
	mod := &PipModule{}

	// 1. Package uninstalled -> Changed = true
	conn1 := newMockPipConn()
	conn1.customOutput["pip3 uninstall -y 'pytest'"] = &ssh.Result{
		ExitCode: 0,
		Stdout:   "Successfully uninstalled pytest-7.1.2\n",
	}
	res1, err := mod.Execute(conn1, map[string]interface{}{
		"name":  "pytest",
		"state": "absent",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res1.Changed {
		t.Errorf("expected Changed=true when package uninstalled")
	}

	// 2. Package already absent -> Changed = false
	conn2 := newMockPipConn()
	conn2.customOutput["pip3 uninstall -y 'pytest'"] = &ssh.Result{
		ExitCode: 0,
		Stdout:   "WARNING: Skipping pytest as it is not installed.\n",
	}
	res2, err := mod.Execute(conn2, map[string]interface{}{
		"name":  "pytest",
		"state": "absent",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res2.Changed {
		t.Errorf("expected Changed=false when package was not installed")
	}
}

func TestPipModule_Virtualenv(t *testing.T) {
	mod := &PipModule{}
	conn := newMockPipConn()
	// venv check fails -> triggers creation
	conn.customOutput["test -f '/opt/venv/bin/python'"] = &ssh.Result{ExitCode: 1}
	conn.customOutput["python3 -m venv '/opt/venv'"] = &ssh.Result{ExitCode: 0, Stdout: "created"}
	conn.customOutput["'/opt/venv/bin/pip' install 'django'"] = &ssh.Result{
		ExitCode: 0,
		Stdout:   "Successfully installed django-4.1\n",
	}

	res, err := mod.Execute(conn, map[string]interface{}{
		"name":       "django",
		"virtualenv": "/opt/venv",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Changed {
		t.Errorf("expected Changed=true when virtualenv was created and package installed")
	}

	// Verify virtualenv creation command was executed
	createdVenv := false
	usedVenvPip := false
	for _, cmd := range conn.executedCmds {
		if strings.Contains(cmd, "python3 -m venv '/opt/venv'") {
			createdVenv = true
		}
		if strings.Contains(cmd, "'/opt/venv/bin/pip' install 'django'") {
			usedVenvPip = true
		}
	}
	if !createdVenv || !usedVenvPip {
		t.Fatalf("expected venv created and venv pip used, executed cmds: %v", conn.executedCmds)
	}
}
