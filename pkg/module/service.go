package module

import (
	"fmt"

	"go-ansible/pkg/ssh"
)

// ServiceModule service 模块
type ServiceModule struct{}

func (m *ServiceModule) Name() string { return "service" }

func (m *ServiceModule) Validate(params map[string]interface{}) error {
	if _, ok := params["name"]; !ok {
		return fmt.Errorf("service module requires name")
	}
	return nil
}

func (m *ServiceModule) Execute(client *ssh.Client, params map[string]interface{}) (*Result, error) {
	name := GetParamString(params, "name", "")
	state := GetParamString(params, "state", "")
	enabled := GetParamString(params, "enabled", "")

	var cmd string

	// 检查是否使用 systemctl
	checkSystemd, _ := client.Execute("which systemctl 2>/dev/null")
	useSystemd := checkSystemd.ExitCode == 0

	switch state {
	case "started", "running":
		if useSystemd {
			cmd = fmt.Sprintf("systemctl start %s", name)
		} else {
			cmd = fmt.Sprintf("service %s start", name)
		}
	case "stopped":
		if useSystemd {
			cmd = fmt.Sprintf("systemctl stop %s", name)
		} else {
			cmd = fmt.Sprintf("service %s stop", name)
		}
	case "restarted":
		if useSystemd {
			cmd = fmt.Sprintf("systemctl restart %s", name)
		} else {
			cmd = fmt.Sprintf("service %s restart", name)
		}
	case "reloaded":
		if useSystemd {
			cmd = fmt.Sprintf("systemctl reload %s", name)
		} else {
			cmd = fmt.Sprintf("service %s reload", name)
		}
	}

	if cmd != "" {
		result, err := client.Execute(cmd)
		if err != nil {
			return result, err
		}
		result.Changed = true
	}

	// 设置开机启动
	if enabled != "" {
		if useSystemd {
			if enabled == "yes" || enabled == "true" {
				cmd = fmt.Sprintf("systemctl enable %s", name)
			} else {
				cmd = fmt.Sprintf("systemctl disable %s", name)
			}
		} else {
			if enabled == "yes" || enabled == "true" {
				cmd = fmt.Sprintf("chkconfig %s on || update-rc.d %s enable", name, name)
			} else {
				cmd = fmt.Sprintf("chkconfig %s off || update-rc.d %s disable", name, name)
			}
		}
		result, err := client.Execute(cmd)
		if err != nil {
			return result, err
		}
		result.Changed = true
		return result, nil
	}

	// 检查服务状态
	if useSystemd {
		cmd = fmt.Sprintf("systemctl is-active %s", name)
	} else {
		cmd = fmt.Sprintf("service %s status", name)
	}
	return client.Execute(cmd)
}
