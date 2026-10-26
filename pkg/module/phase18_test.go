package module

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/ssh"
)

type mockPhase18Conn struct {
	executedCmds []string
	uploaded     map[string]string
	downloaded   map[string]string
	customOutput map[string]*ssh.Result
}

func newMockPhase18Conn() *mockPhase18Conn {
	return &mockPhase18Conn{
		executedCmds: make([]string, 0),
		uploaded:     make(map[string]string),
		downloaded:   make(map[string]string),
		customOutput: make(map[string]*ssh.Result),
	}
}

func (c *mockPhase18Conn) Connect() error { return nil }
func (c *mockPhase18Conn) Close() error   { return nil }

func (c *mockPhase18Conn) Execute(cmd string) (*ssh.Result, error) {
	c.executedCmds = append(c.executedCmds, cmd)
	for pattern, res := range c.customOutput {
		if strings.Contains(cmd, pattern) {
			return res, nil
		}
	}
	if strings.Contains(cmd, "apt-get >/dev/null") {
		return &ssh.Result{ExitCode: 0, Stdout: "apt\n"}, nil
	}
	if strings.Contains(cmd, "yum >/dev/null") {
		return &ssh.Result{ExitCode: 1, Stderr: "no yum"}, nil
	}
	return &ssh.Result{ExitCode: 0, Stdout: "success"}, nil
}

func (c *mockPhase18Conn) ExecuteWithBecome(cmd string) (*ssh.Result, error) {
	return c.Execute(cmd)
}

func (c *mockPhase18Conn) Upload(localPath, remotePath string) error {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	c.uploaded[remotePath] = string(data)
	return nil
}

func (c *mockPhase18Conn) Download(remotePath, localPath string) error {
	c.downloaded[remotePath] = localPath
	return nil
}

func (c *mockPhase18Conn) User() string { return "testuser" }

func TestCommandModule_Argv(t *testing.T) {
	cmdMod := &CommandModule{}
	conn := newMockPhase18Conn()
	conn.customOutput["'echo' 'hello world' '$var'"] = &ssh.Result{ExitCode: 0, Changed: true, Stdout: "hello world $var"}

	params := map[string]interface{}{
		"argv": []interface{}{"echo", "hello world", "$var"},
	}
	if err := cmdMod.Validate(params); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}

	res, err := cmdMod.Execute(conn, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !res.Changed {
		t.Errorf("expected Changed to be true")
	}

	found := false
	for _, cmd := range conn.executedCmds {
		if strings.Contains(cmd, "'echo' 'hello world' '$var'") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected command with shellQuote argv, got cmds: %v", conn.executedCmds)
	}
}

func TestPackageModule_AptDispatch(t *testing.T) {
	pkgMod := &PackageModule{}
	conn := newMockPhase18Conn()
	conn.customOutput["dpkg-query -W"] = &ssh.Result{ExitCode: 1, Stderr: "not installed"}
	conn.customOutput["apt-get install"] = &ssh.Result{ExitCode: 0, Stdout: "Setting up nginx (1.18.0) ...\n1 newly installed"}

	params := map[string]interface{}{
		"name":  "nginx",
		"state": "present",
	}
	if err := pkgMod.Validate(params); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}

	res, err := pkgMod.Execute(conn, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !res.Changed {
		t.Errorf("expected Changed to be true")
	}

	aptFound := false
	for _, cmd := range conn.executedCmds {
		if strings.Contains(cmd, "apt-get install") {
			aptFound = true
			break
		}
	}
	if !aptFound {
		t.Fatalf("expected apt-get install command, got cmds: %v", conn.executedCmds)
	}
}

func TestTemplateDict2Items(t *testing.T) {
	input := map[string]interface{}{
		"alpha": "val1",
		"beta":  123,
	}
	items, err := TemplateDict2Items(input)
	if err != nil {
		t.Fatalf("TemplateDict2Items failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	foundAlpha := false
	for _, it := range items {
		if it["key"] == "alpha" && it["value"] == "val1" {
			foundAlpha = true
		}
	}
	if !foundAlpha {
		t.Fatalf("expected alpha item, got: %v", items)
	}
}

func TestCopyModule_RemoteSrc_And_Validate(t *testing.T) {
	copyMod := &CopyModule{}
	conn := newMockPhase18Conn()

	conn.customOutput["test -e /etc/nginx/nginx.conf"] = &ssh.Result{ExitCode: 0}
	conn.customOutput["nginx.conf.bak'"] = &ssh.Result{ExitCode: 1, Stderr: "no such file"}
	conn.customOutput["nginx -t"] = &ssh.Result{ExitCode: 0, Stdout: "syntax ok"}

	params := map[string]interface{}{
		"src":        "/etc/nginx/nginx.conf",
		"dest":       "/etc/nginx/nginx.conf.bak",
		"remote_src": true,
		"validate":   "nginx -t -c %s",
	}

	res, err := copyMod.Execute(conn, params)
	if err != nil {
		t.Fatalf("Execute remote_src copy failed: %v", err)
	}
	if !res.Changed {
		t.Errorf("expected Changed=true")
	}

	valFound := false
	for _, cmd := range conn.executedCmds {
		if strings.Contains(cmd, "nginx -t -c /tmp/go-ansible-cptmp-") {
			valFound = true
			break
		}
	}
	if !valFound {
		t.Fatalf("expected validation command executed on tmp file, got cmds: %v", conn.executedCmds)
	}
}

func TestTemplateModule_Validate(t *testing.T) {
	tmplMod := &TemplateModule{}
	conn := newMockPhase18Conn()
	conn.customOutput["nginx -t"] = &ssh.Result{ExitCode: 0, Stdout: "syntax ok"}

	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "test.j2")
	if err := os.WriteFile(srcFile, []byte("server { listen {{ port }}; }"), 0644); err != nil {
		t.Fatal(err)
	}

	params := map[string]interface{}{
		"src":      srcFile,
		"dest":     "/etc/nginx/conf.d/test.conf",
		"validate": "nginx -t -c %s",
		"vars": map[string]interface{}{
			"port": 8080,
		},
	}

	res, err := tmplMod.Execute(conn, params)
	if err != nil {
		t.Fatalf("Template Execute failed: %v", err)
	}
	if !res.Changed {
		t.Errorf("expected Changed=true")
	}

	valFound := false
	for _, cmd := range conn.executedCmds {
		if strings.Contains(cmd, "nginx -t -c /tmp/go-ansible-tmpl-") {
			valFound = true
			break
		}
	}
	if !valFound {
		t.Fatalf("expected template validation executed on tmp file, got cmds: %v", conn.executedCmds)
	}
}
