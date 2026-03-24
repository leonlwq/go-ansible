package module

import (
	"fmt"

	"go-ansible/pkg/ssh"
)

// UserModule user 模块
type UserModule struct{}

func (m *UserModule) Name() string { return "user" }

func (m *UserModule) Validate(params map[string]interface{}) error {
	if _, ok := params["name"]; !ok {
		return fmt.Errorf("user module requires name")
	}
	return nil
}

func (m *UserModule) Execute(client *ssh.Client, params map[string]interface{}) (*Result, error) {
	name := GetParamString(params, "name", "")
	state := GetParamString(params, "state", "present")
	system := GetParamBool(params, "system", false)
	createHome := GetParamBool(params, "create_home", true)
	shell := GetParamString(params, "shell", "")
	group := GetParamString(params, "group", "")
	groups := GetParamString(params, "groups", "")
	appends := GetParamBool(params, "append", false)
	password := GetParamString(params, "password", "")
	uid := GetParamString(params, "uid", "")
	comment := GetParamString(params, "comment", "")
	remove := GetParamBool(params, "remove", false)

	var cmd string

	switch state {
	case "present":
		// 检查用户是否存在
		checkCmd := fmt.Sprintf("id %s 2>/dev/null", name)
		checkResult, _ := client.Execute(checkCmd)

		if checkResult.ExitCode == 0 {
			// 用户存在，修改
			cmd = fmt.Sprintf("usermod")
			if shell != "" {
				cmd += fmt.Sprintf(" -s %s", shell)
			}
			if group != "" {
				cmd += fmt.Sprintf(" -g %s", group)
			}
			if groups != "" {
				if appends {
					cmd += fmt.Sprintf(" -aG %s", groups)
				} else {
					cmd += fmt.Sprintf(" -G %s", groups)
				}
			}
			if comment != "" {
				cmd += fmt.Sprintf(" -c %q", comment)
			}
			cmd += fmt.Sprintf(" %s", name)
		} else {
			// 用户不存在，创建
			cmd = "useradd"
			if system {
				cmd += " -r"
			}
			if !createHome {
				cmd += " -M"
			}
			if shell != "" {
				cmd += fmt.Sprintf(" -s %s", shell)
			}
			if group != "" {
				cmd += fmt.Sprintf(" -g %s", group)
			}
			if groups != "" {
				cmd += fmt.Sprintf(" -G %s", groups)
			}
			if uid != "" {
				cmd += fmt.Sprintf(" -u %s", uid)
			}
			if comment != "" {
				cmd += fmt.Sprintf(" -c %q", comment)
			}
			cmd += fmt.Sprintf(" %s", name)
		}

	case "absent":
		cmd = "userdel"
		if remove {
			cmd += " -r"
		}
		cmd += fmt.Sprintf(" %s", name)

	default:
		return nil, fmt.Errorf("unsupported state: %s", state)
	}

	result, err := client.Execute(cmd)
	if err != nil {
		return result, err
	}

	// 设置密码
	if password != "" && state == "present" {
		passwdCmd := fmt.Sprintf("echo '%s:%s' | chpasswd", name, password)
		client.Execute(passwdCmd)
	}

	result.Changed = true
	return result, nil
}

// GroupModule group 模块
type GroupModule struct{}

func (m *GroupModule) Name() string { return "group" }

func (m *GroupModule) Validate(params map[string]interface{}) error {
	if _, ok := params["name"]; !ok {
		return fmt.Errorf("group module requires name")
	}
	return nil
}

func (m *GroupModule) Execute(client *ssh.Client, params map[string]interface{}) (*Result, error) {
	name := GetParamString(params, "name", "")
	state := GetParamString(params, "state", "present")
	system := GetParamBool(params, "system", false)
	gid := GetParamString(params, "gid", "")

	var cmd string

	switch state {
	case "present":
		checkCmd := fmt.Sprintf("getent group %s", name)
		checkResult, _ := client.Execute(checkCmd)

		if checkResult.ExitCode == 0 {
			// 组存在
			if gid != "" {
				cmd = fmt.Sprintf("groupmod -g %s %s", gid, name)
			}
		} else {
			// 组不存在，创建
			cmd = "groupadd"
			if system {
				cmd += " -r"
			}
			if gid != "" {
				cmd += fmt.Sprintf(" -g %s", gid)
			}
			cmd += fmt.Sprintf(" %s", name)
		}

	case "absent":
		cmd = fmt.Sprintf("groupdel %s", name)

	default:
		return nil, fmt.Errorf("unsupported state: %s", state)
	}

	if cmd == "" {
		return &Result{Changed: false, Message: "group already exists"}, nil
	}

	result, err := client.Execute(cmd)
	if err != nil {
		return result, err
	}
	result.Changed = true
	return result, nil
}
