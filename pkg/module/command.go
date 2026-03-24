package module

import (
	"fmt"

	"go-ansible/pkg/ssh"
)

// CommandModule command 模块
type CommandModule struct{}

func (m *CommandModule) Name() string { return "command" }

func (m *CommandModule) Validate(params map[string]interface{}) error {
	if _, ok := params["_raw_params"]; !ok {
		if _, ok := params["cmd"]; !ok {
			return fmt.Errorf("command module requires _raw_params or cmd")
		}
	}
	return nil
}

func (m *CommandModule) Execute(client *ssh.Client, params map[string]interface{}) (*Result, error) {
	cmd := GetParamString(params, "_raw_params", "")
	if cmd == "" {
		cmd = GetParamString(params, "cmd", "")
	}

	creates := GetParamString(params, "creates", "")
	removes := GetParamString(params, "removes", "")

	// 检查 creates 参数
	if creates != "" {
		result, err := client.Execute(fmt.Sprintf("test -e %s", creates))
		if err == nil && result.ExitCode == 0 {
			result.Changed = false
			result.Message = fmt.Sprintf("skipped, since %s exists", creates)
			return result, nil
		}
	}

	// 检查 removes 参数
	if removes != "" {
		result, err := client.Execute(fmt.Sprintf("test -e %s", removes))
		if err != nil || result.ExitCode != 0 {
			result.Changed = false
			result.Message = fmt.Sprintf("skipped, since %s does not exist", removes)
			return result, nil
		}
	}

	return client.Execute(cmd)
}

// ShellModule shell 模块
type ShellModule struct{}

func (m *ShellModule) Name() string { return "shell" }

func (m *ShellModule) Validate(params map[string]interface{}) error {
	if _, ok := params["_raw_params"]; !ok {
		if _, ok := params["cmd"]; !ok {
			return fmt.Errorf("shell module requires _raw_params or cmd")
		}
	}
	return nil
}

func (m *ShellModule) Execute(client *ssh.Client, params map[string]interface{}) (*Result, error) {
	cmd := GetParamString(params, "_raw_params", "")
	if cmd == "" {
		cmd = GetParamString(params, "cmd", "")
	}

	// 使用 bash 执行
	cmd = fmt.Sprintf("/bin/bash -c %q", cmd)

	creates := GetParamString(params, "creates", "")
	removes := GetParamString(params, "removes", "")

	if creates != "" {
		result, err := client.Execute(fmt.Sprintf("test -e %s", creates))
		if err == nil && result.ExitCode == 0 {
			result.Changed = false
			result.Message = fmt.Sprintf("skipped, since %s exists", creates)
			return result, nil
		}
	}

	if removes != "" {
		result, err := client.Execute(fmt.Sprintf("test -e %s", removes))
		if err != nil || result.ExitCode != 0 {
			result.Changed = false
			result.Message = fmt.Sprintf("skipped, since %s does not exist", removes)
			return result, nil
		}
	}

	return client.Execute(cmd)
}

// PingModule ping 模块
type PingModule struct{}

func (m *PingModule) Name() string { return "ping" }

func (m *PingModule) Validate(params map[string]interface{}) error {
	return nil
}

func (m *PingModule) Execute(client *ssh.Client, params map[string]interface{}) (*Result, error) {
	result, err := client.Execute("echo pong")
	if err != nil {
		return result, err
	}
	result.Changed = false
	result.Message = "pong"
	return result, nil
}

// SetupModule setup 模块（收集主机信息）
type SetupModule struct{}

func (m *SetupModule) Name() string { return "setup" }

func (m *SetupModule) Validate(params map[string]interface{}) error {
	return nil
}

func (m *SetupModule) Execute(client *ssh.Client, params map[string]interface{}) (*Result, error) {
	filter := GetParamString(params, "filter", "")

	script := `
		echo "gathered_facts:"
		echo "  ansible_hostname: $(hostname)"
		echo "  ansible_fqdn: $(hostname -f 2>/dev/null || hostname)"
		echo "  ansible_os_family: $(uname -s)"
		echo "  ansible_architecture: $(uname -m)"
		echo "  ansible_kernel: $(uname -r)"
		echo "  ansible_distribution: $(cat /etc/os-release 2>/dev/null | grep ^NAME= | cut -d= -f2 | tr -d '\"') || $(sw_vers -productName 2>/dev/null)"
		echo "  ansible_distribution_version: $(cat /etc/os-release 2>/dev/null | grep ^VERSION_ID= | cut -d= -f2 | tr -d '\"') || $(sw_vers -productVersion 2>/dev/null)"
		echo "  ansible_python_version: $(python3 --version 2>/dev/null || python --version 2>/dev/null || echo 'Not installed')"
		echo "  ansible_processor_count: $(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo '1')"
		echo "  ansible_memtotal_mb: $(free -m 2>/dev/null | awk '/^Mem:/{print $2}' || echo 'N/A')"
		echo "  ansible_date_time: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
	`

	if filter != "" {
		script = fmt.Sprintf(`
			echo "gathered_facts:"
			echo "  ansible_%s: $(grep -E "^(%s)=" /etc/os-release 2>/dev/null | head -1 | cut -d= -f2 | tr -d '\"')"
		`, filter, filter)
	}

	return client.Execute(script)
}
