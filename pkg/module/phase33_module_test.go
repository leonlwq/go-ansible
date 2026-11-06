package module

import (
	"strings"
	"testing"
)

type mockPhase33Client struct {
	commands []string
	files    map[string]string
	exitCode int
}

func (m *mockPhase33Client) Connect() error {
	return nil
}

func (m *mockPhase33Client) Execute(cmd string) (*Result, error) {
	m.commands = append(m.commands, cmd)
	if strings.HasPrefix(cmd, "which systemctl") {
		return &Result{ExitCode: 0, Stdout: "/bin/systemctl"}, nil
	}
	if strings.Contains(cmd, "is-enabled") {
		return &Result{ExitCode: 0, Stdout: "disabled"}, nil
	}
	if strings.Contains(cmd, "is-active") {
		return &Result{ExitCode: 0, Stdout: "inactive"}, nil
	}
	if strings.HasPrefix(cmd, "test -e") {
		return &Result{ExitCode: 0}, nil
	}
	if strings.HasPrefix(cmd, "stat -c") {
		return &Result{ExitCode: 0, Stdout: "644:root:root"}, nil
	}
	return &Result{ExitCode: m.exitCode, Changed: true}, nil
}

func (m *mockPhase33Client) ExecuteWithBecome(cmd string) (*Result, error) {
	return m.Execute(cmd)
}

func (m *mockPhase33Client) Upload(local, remote string) error {
	m.commands = append(m.commands, "UPLOAD "+local+" -> "+remote)
	return nil
}

func (m *mockPhase33Client) Download(remote, local string) error {
	m.commands = append(m.commands, "DOWNLOAD "+remote+" -> "+local)
	return nil
}

func (m *mockPhase33Client) Close() error {
	return nil
}

// TestPhase33_ServiceMaskedState 测试 service 与 systemd 模块支持 state: masked
func TestPhase33_ServiceMaskedState(t *testing.T) {
	client := &mockPhase33Client{}
	mod := &ServiceModule{}

	params := map[string]interface{}{
		"name":  "telnet.socket",
		"state": "masked",
	}

	res, err := mod.Execute(client, params)
	if err != nil {
		t.Fatalf("ServiceModule.Execute failed: %v", err)
	}
	if res == nil {
		t.Fatalf("expected non-nil result")
	}

	foundMask := false
	for _, cmd := range client.commands {
		if strings.Contains(cmd, "systemctl mask 'telnet.socket'") {
			foundMask = true
			break
		}
	}
	if !foundMask {
		t.Fatalf("expected systemctl mask command, executed: %v", client.commands)
	}
}

// TestPhase33_SystemdMaskedParam 测试 systemd 模块支持 state: masked 与 masked: true/false
func TestPhase33_SystemdMaskedParam(t *testing.T) {
	client := &mockPhase33Client{}
	mod := &SystemdModule{}

	params := map[string]interface{}{
		"name":  "rsh.socket",
		"state": "masked",
	}

	res, err := mod.Execute(client, params)
	if err != nil {
		t.Fatalf("SystemdModule.Execute failed: %v", err)
	}
	if res == nil {
		t.Fatalf("expected non-nil result")
	}

	foundMask := false
	for _, cmd := range client.commands {
		if strings.Contains(cmd, "systemctl mask 'rsh.socket'") {
			foundMask = true
			break
		}
	}
	if !foundMask {
		t.Fatalf("expected systemctl mask command, executed: %v", client.commands)
	}
}

// TestPhase33_UnarchivePermissions 测试 unarchive 模块解压后应用 owner/group/mode
func TestPhase33_UnarchivePermissions(t *testing.T) {
	client := &mockPhase33Client{}
	mod := &UnarchiveModule{}

	params := map[string]interface{}{
		"src":        "/remote/app.tar.gz",
		"dest":       "/opt/app with space",
		"remote_src": true,
		"owner":      "deployer",
		"group":      "webgroup",
		"mode":       "0755",
	}

	res, err := mod.Execute(client, params)
	if err != nil {
		t.Fatalf("UnarchiveModule.Execute failed: %v", err)
	}
	if res == nil {
		t.Fatalf("expected non-nil result")
	}

	foundChmod := false
	foundChown := false
	for _, cmd := range client.commands {
		if strings.Contains(cmd, "chmod -R") && strings.Contains(cmd, "'/opt/app with space'") {
			foundChmod = true
		}
		if strings.Contains(cmd, "chown -R") && strings.Contains(cmd, "deployer:webgroup") {
			foundChown = true
		}
	}
	if !foundChmod || !foundChown {
		t.Fatalf("expected chmod -R and chown -R after unarchive, executed: %v", client.commands)
	}
}

// TestPhase33_CopyRemoteSrcQuoting 测试 copy 模块 remote_src 对含空格路径的安全转义
func TestPhase33_CopyRemoteSrcQuoting(t *testing.T) {
	client := &mockPhase33Client{}
	mod := &CopyModule{}

	params := map[string]interface{}{
		"src":        "/remote/source path/file.conf",
		"dest":       "/remote/target dir/file.conf",
		"remote_src": true,
	}

	res, err := mod.Execute(client, params)
	if err != nil {
		t.Fatalf("CopyModule.Execute failed: %v", err)
	}
	if res == nil {
		t.Fatalf("expected non-nil result")
	}

	foundCp := false
	for _, cmd := range client.commands {
		if strings.Contains(cmd, "cp -p '/remote/source path/file.conf' '/remote/target dir/file.conf'") {
			foundCp = true
			break
		}
	}
	if !foundCp {
		t.Fatalf("expected properly shell-quoted cp -p command, executed: %v", client.commands)
	}
}

// TestPhase33_TemplateToNiceYAML 测试 to_nice_yaml 过滤器缩进控制
func TestPhase33_TemplateToNiceYAML(t *testing.T) {
	data := map[string]interface{}{
		"name": "ansible",
		"roles": []string{
			"common",
			"web",
		},
	}

	out, err := TemplateToNiceYAML(data, 4)
	if err != nil {
		t.Fatalf("TemplateToNiceYAML failed: %v", err)
	}
	if !strings.Contains(out, "name: ansible") {
		t.Errorf("expected 'name: ansible' in output, got: %s", out)
	}
	if !strings.Contains(out, "    - common") && !strings.Contains(out, "roles:") {
		t.Errorf("expected indented roles list, got: %s", out)
	}
}
