package module

import (
	"fmt"
	"strings"

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

func (m *UserModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
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
	home := GetParamString(params, "home", "")
	expires := GetParamString(params, "expires", "")
	moveHome := GetParamBool(params, "move_home", false)
	remove := GetParamBool(params, "remove", false)
	checkCmd := fmt.Sprintf("id %s 2>/dev/null", shellQuote(name))
	checkResult, _ := client.Execute(checkCmd)
	exists := checkResult != nil && checkResult.ExitCode == 0

	cmd, passwordCmd, changed, err := buildUserCommands(userCommandSpec{
		Name:       name,
		State:      state,
		System:     system,
		CreateHome: createHome,
		Shell:      shell,
		Group:      group,
		Groups:     groups,
		Append:     appends,
		Password:   password,
		UID:        uid,
		Comment:    comment,
		Home:       home,
		Expires:    expires,
		MoveHome:   moveHome,
		Remove:     remove,
	}, exists)
	if err != nil {
		return nil, err
	}
	if !changed {
		return &Result{Changed: false, Message: fmt.Sprintf("user %s already in desired state", name)}, nil
	}

	var result *Result
	if cmd != "" {
		result, err = client.Execute(cmd)
		if err != nil {
			return result, err
		}
	} else {
		result = &Result{}
	}

	if passwordCmd != "" {
		passwdResult, passwdErr := client.Execute(passwordCmd)
		if passwdErr != nil {
			return passwdResult, passwdErr
		}
		result.Stdout = strings.TrimSpace(strings.TrimSpace(result.Stdout) + "\n" + strings.TrimSpace(passwdResult.Stdout))
		result.Stderr = strings.TrimSpace(strings.TrimSpace(result.Stderr) + "\n" + strings.TrimSpace(passwdResult.Stderr))
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

func (m *GroupModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	name := GetParamString(params, "name", "")
	state := GetParamString(params, "state", "present")
	system := GetParamBool(params, "system", false)
	gid := GetParamString(params, "gid", "")
	checkCmd := fmt.Sprintf("getent group %s", shellQuote(name))
	checkResult, _ := client.Execute(checkCmd)
	exists := checkResult != nil && checkResult.ExitCode == 0

	cmd, changed, err := buildGroupCommand(groupCommandSpec{
		Name:   name,
		State:  state,
		System: system,
		GID:    gid,
	}, exists)
	if err != nil {
		return nil, err
	}
	if !changed {
		return &Result{Changed: false, Message: fmt.Sprintf("group %s already in desired state", name)}, nil
	}

	result, err := client.Execute(cmd)
	if err != nil {
		return result, err
	}
	result.Changed = true
	return result, nil
}

type userCommandSpec struct {
	Name       string
	State      string
	System     bool
	CreateHome bool
	Shell      string
	Group      string
	Groups     string
	Append     bool
	Password   string
	UID        string
	Comment    string
	Home       string
	Expires    string
	MoveHome   bool
	Remove     bool
}

func buildUserCommands(spec userCommandSpec, exists bool) (string, string, bool, error) {
	quotedName := shellQuote(spec.Name)

	switch spec.State {
	case "present":
		if exists {
			args := make([]string, 0)
			if spec.Shell != "" {
				args = append(args, "-s "+shellQuote(spec.Shell))
			}
			if spec.Group != "" {
				args = append(args, "-g "+shellQuote(spec.Group))
			}
			if spec.Groups != "" {
				if spec.Append {
					args = append(args, "-aG "+shellQuote(spec.Groups))
				} else {
					args = append(args, "-G "+shellQuote(spec.Groups))
				}
			}
			if spec.UID != "" {
				args = append(args, "-u "+shellQuote(spec.UID))
			}
			if spec.Comment != "" {
				args = append(args, "-c "+shellQuote(spec.Comment))
			}
			if spec.Home != "" {
				args = append(args, "-d "+shellQuote(spec.Home))
				if spec.MoveHome {
					args = append(args, "-m")
				}
			}
			if spec.Expires != "" {
				args = append(args, "-e "+shellQuote(spec.Expires))
			}
			var cmd string
			if len(args) > 0 {
				cmd = "usermod " + strings.Join(args, " ") + " " + quotedName
			}
			passwordCmd := ""
			if spec.Password != "" {
				passwordCmd = "usermod -p " + shellQuote(spec.Password) + " " + quotedName
			}
			return cmd, passwordCmd, cmd != "" || passwordCmd != "", nil
		}

		args := make([]string, 0)
		if spec.System {
			args = append(args, "-r")
		}
		if !spec.CreateHome {
			args = append(args, "-M")
		} else {
			args = append(args, "-m")
		}
		if spec.Shell != "" {
			args = append(args, "-s "+shellQuote(spec.Shell))
		}
		if spec.Group != "" {
			args = append(args, "-g "+shellQuote(spec.Group))
		}
		if spec.Groups != "" {
			args = append(args, "-G "+shellQuote(spec.Groups))
		}
		if spec.UID != "" {
			args = append(args, "-u "+shellQuote(spec.UID))
		}
		if spec.Comment != "" {
			args = append(args, "-c "+shellQuote(spec.Comment))
		}
		if spec.Home != "" {
			args = append(args, "-d "+shellQuote(spec.Home))
		}
		if spec.Expires != "" {
			args = append(args, "-e "+shellQuote(spec.Expires))
		}
		if spec.Password != "" {
			args = append(args, "-p "+shellQuote(spec.Password))
		}
		return "useradd " + strings.Join(args, " ") + " " + quotedName, "", true, nil
	case "absent":
		if !exists {
			return "", "", false, nil
		}
		cmd := "userdel"
		if spec.Remove {
			cmd += " -r"
		}
		cmd += " " + quotedName
		return cmd, "", true, nil
	case "locked":
		if !exists {
			return "", "", false, fmt.Errorf("user %s not found", spec.Name)
		}
		return "usermod -L " + quotedName, "", true, nil
	case "unlocked":
		if !exists {
			return "", "", false, fmt.Errorf("user %s not found", spec.Name)
		}
		return "usermod -U " + quotedName, "", true, nil
	default:
		return "", "", false, fmt.Errorf("unsupported state: %s", spec.State)
	}
}

type groupCommandSpec struct {
	Name   string
	State  string
	System bool
	GID    string
}

func buildGroupCommand(spec groupCommandSpec, exists bool) (string, bool, error) {
	quotedName := shellQuote(spec.Name)

	switch spec.State {
	case "present":
		if exists {
			if spec.GID == "" {
				return "", false, nil
			}
			return "groupmod -g " + shellQuote(spec.GID) + " " + quotedName, true, nil
		}
		args := make([]string, 0)
		if spec.System {
			args = append(args, "-r")
		}
		if spec.GID != "" {
			args = append(args, "-g "+shellQuote(spec.GID))
		}
		return "groupadd " + strings.Join(args, " ") + " " + quotedName, true, nil
	case "absent":
		if !exists {
			return "", false, nil
		}
		return "groupdel " + quotedName, true, nil
	default:
		return "", false, fmt.Errorf("unsupported state: %s", spec.State)
	}
}
