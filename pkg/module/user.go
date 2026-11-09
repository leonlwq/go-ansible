package module

import (
	"fmt"
	"path/filepath"
	"strings"

	"go-ansible/pkg/ssh"
	"go-ansible/pkg/util"
)

// UserModule user 模块
type UserModule struct{}

func (m *UserModule) Name() string { return "user" }

func (m *UserModule) Validate(params map[string]interface{}) error {
	if !HasAnyParam(params, "name", "user") {
		return fmt.Errorf("user module requires name or user")
	}
	return nil
}

func parseGroupsParam(params map[string]interface{}) string {
	val, ok := params["groups"]
	if !ok || val == nil {
		return ""
	}
	switch v := val.(type) {
	case string:
		return strings.TrimSpace(v)
	case []string:
		return strings.Join(v, ",")
	case []interface{}:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			s := strings.TrimSpace(fmt.Sprintf("%v", item))
			if s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ",")
	default:
		s := fmt.Sprintf("%v", val)
		s = strings.TrimPrefix(s, "[")
		s = strings.TrimSuffix(s, "]")
		fields := strings.Fields(s)
		return strings.Join(fields, ",")
	}
}

func (m *UserModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	name := GetParamStringAliases(params, "", "name", "user")
	state := GetParamString(params, "state", "present")
	system := GetParamBool(params, "system", false)
	createHome := GetParamBool(params, "create_home", true)
	shell := GetParamString(params, "shell", "")
	group := GetParamString(params, "group", "")
	groups := parseGroupsParam(params)
	appends := GetParamBool(params, "append", false)
	password := GetParamString(params, "password", "")
	updatePassword := GetParamString(params, "update_password", "always")
	uid := GetParamString(params, "uid", "")
	comment := GetParamString(params, "comment", "")
	home := GetParamString(params, "home", "")
	expires := GetParamString(params, "expires", "")
	moveHome := GetParamBool(params, "move_home", false)
	remove := GetParamBool(params, "remove", false)
	var passwordLock *bool
	if rawLock, ok := params["password_lock"]; ok && rawLock != nil {
		b := util.ToBool(rawLock)
		passwordLock = &b
	}
	checkCmd := fmt.Sprintf("id %s 2>/dev/null", shellQuote(name))
	checkResult, _ := client.Execute(checkCmd)
	exists := checkResult != nil && checkResult.ExitCode == 0

	// update_password: on_create 规范
	if exists && strings.ToLower(updatePassword) == "on_create" {
		password = ""
	}

	// 幂等性检查：若已有用户追加相同组，无需二次执行 usermod
	if exists && groups != "" && appends {
		idRes, _ := client.Execute(fmt.Sprintf("id -Gn %s 2>/dev/null", shellQuote(name)))
		if idRes != nil && idRes.ExitCode == 0 {
			currentGroups := strings.Fields(idRes.Stdout)
			currMap := make(map[string]bool, len(currentGroups))
			for _, cg := range currentGroups {
				currMap[cg] = true
			}
			targetList := strings.Split(groups, ",")
			allPresent := true
			for _, tg := range targetList {
				tg = strings.TrimSpace(tg)
				if tg != "" && !currMap[tg] {
					allPresent = false
					break
				}
			}
			if allPresent {
				groups = ""
			}
		}
	}

	generateSSHKey := GetParamBool(params, "generate_ssh_key", false)
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
		MoveHome:     moveHome,
		Remove:       remove,
		PasswordLock: passwordLock,
	}, exists)
	if err != nil {
		return nil, err
	}
	if !changed {
		if state != "absent" && generateSSHKey {
			pubKey, keyFile, keyChanged, err := handleUserSSHKey(client, name, home, group, params, IsCheckMode(params))
			if err != nil {
				return nil, err
			}
			if keyChanged {
				if IsCheckMode(params) {
					return &Result{
						Changed: true,
						Message: fmt.Sprintf("check mode: ssh key for user %s would be generated", name),
						Data: map[string]interface{}{
							"ssh_key_file": keyFile,
						},
					}, nil
				}
				return &Result{
					Changed: true,
					Message: fmt.Sprintf("ssh key generated for user %s", name),
					Data: map[string]interface{}{
						"ssh_public_key": pubKey,
						"ssh_key_file":   keyFile,
					},
				}, nil
			}
			return &Result{
				Changed: false,
				Message: fmt.Sprintf("user %s already in desired state", name),
				Data: map[string]interface{}{
					"ssh_public_key": pubKey,
					"ssh_key_file":   keyFile,
				},
			}, nil
		}
		return &Result{Changed: false, Message: fmt.Sprintf("user %s already in desired state", name)}, nil
	}
	if IsCheckMode(params) {
		return &Result{
			Changed: true,
			Message: fmt.Sprintf("check mode: user %s would change", name),
		}, nil
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

	if state != "absent" && generateSSHKey {
		pubKey, keyFile, _, err := handleUserSSHKey(client, name, home, group, params, false)
		if err != nil {
			return result, err
		}
		if result.Data == nil {
			result.Data = make(map[string]interface{})
		}
		result.Data["ssh_public_key"] = pubKey
		result.Data["ssh_key_file"] = keyFile
	}

	result.Changed = true
	return result, nil
}

func handleUserSSHKey(client ssh.Connection, name, home, group string, params map[string]interface{}, checkMode bool) (string, string, bool, error) {
	keyType := strings.ToLower(strings.TrimSpace(GetParamString(params, "ssh_key_type", "rsa")))
	if keyType == "" {
		keyType = "rsa"
	}
	keyBits := GetParamInt(params, "ssh_key_bits", 2048)
	keyComment := GetParamString(params, "ssh_key_comment", fmt.Sprintf("ansible-generated on %s", name))
	keyPassphrase := GetParamString(params, "ssh_key_passphrase", "")
	keyFile := GetParamString(params, "ssh_key_file", "")

	userHome := home
	if userHome == "" {
		res, _ := client.Execute(fmt.Sprintf("getent passwd %s 2>/dev/null", shellQuote(name)))
		if res != nil && res.ExitCode == 0 && res.Stdout != "" {
			parts := strings.Split(strings.TrimSpace(res.Stdout), ":")
			if len(parts) >= 6 && parts[5] != "" {
				userHome = parts[5]
			}
		}
	}
	if userHome == "" {
		userHome = "/home/" + name
	}

	if keyFile == "" {
		keyFile = fmt.Sprintf("%s/.ssh/id_%s", userHome, keyType)
	} else if !strings.HasPrefix(keyFile, "/") {
		keyFile = fmt.Sprintf("%s/%s", userHome, keyFile)
	}

	pubKeyFile := keyFile + ".pub"

	checkKeyCmd := fmt.Sprintf("test -f %s", shellQuote(keyFile))
	checkRes, _ := client.Execute(checkKeyCmd)
	keyExists := checkRes != nil && checkRes.ExitCode == 0

	if keyExists {
		catRes, _ := client.Execute(fmt.Sprintf("cat %s 2>/dev/null", shellQuote(pubKeyFile)))
		pubKey := ""
		if catRes != nil && catRes.ExitCode == 0 {
			pubKey = strings.TrimSpace(catRes.Stdout)
		}
		return pubKey, keyFile, false, nil
	}

	if checkMode {
		return "", keyFile, true, nil
	}

	sshDir := filepath.Dir(keyFile)
	mkdirCmd := fmt.Sprintf("mkdir -p %s && chmod 700 %s", shellQuote(sshDir), shellQuote(sshDir))
	if group != "" {
		mkdirCmd += fmt.Sprintf(" && chown %s:%s %s", shellQuote(name), shellQuote(group), shellQuote(sshDir))
	} else {
		mkdirCmd += fmt.Sprintf(" && chown %s %s", shellQuote(name), shellQuote(sshDir))
	}
	_, _ = client.Execute(mkdirCmd)

	keygenArgs := []string{
		"ssh-keygen",
		"-q",
		"-t", keyType,
		"-N", shellQuote(keyPassphrase),
		"-C", shellQuote(keyComment),
		"-f", shellQuote(keyFile),
	}
	if keyType == "rsa" || keyType == "ecdsa" {
		keygenArgs = append(keygenArgs, "-b", fmt.Sprintf("%d", keyBits))
	}
	keygenCmd := strings.Join(keygenArgs, " ")

	kgRes, err := client.Execute(keygenCmd)
	if err != nil || (kgRes != nil && kgRes.ExitCode != 0) {
		errMsg := ""
		if kgRes != nil {
			errMsg = kgRes.Stderr
		}
		return "", keyFile, false, fmt.Errorf("failed to generate ssh key: %s (err: %v)", errMsg, err)
	}

	permCmd := fmt.Sprintf("chmod 600 %s && chmod 644 %s", shellQuote(keyFile), shellQuote(pubKeyFile))
	if group != "" {
		permCmd += fmt.Sprintf(" && chown %s:%s %s %s", shellQuote(name), shellQuote(group), shellQuote(keyFile), shellQuote(pubKeyFile))
	} else {
		permCmd += fmt.Sprintf(" && chown %s %s %s", shellQuote(name), shellQuote(keyFile), shellQuote(pubKeyFile))
	}
	_, _ = client.Execute(permCmd)

	catRes, _ := client.Execute(fmt.Sprintf("cat %s 2>/dev/null", shellQuote(pubKeyFile)))
	pubKey := ""
	if catRes != nil && catRes.ExitCode == 0 {
		pubKey = strings.TrimSpace(catRes.Stdout)
	}

	return pubKey, keyFile, true, nil
}

// GroupModule group 模块
type GroupModule struct{}

func (m *GroupModule) Name() string { return "group" }

func (m *GroupModule) Validate(params map[string]interface{}) error {
	if !HasAnyParam(params, "name", "group") {
		return fmt.Errorf("group module requires name or group")
	}
	return nil
}

func (m *GroupModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	name := GetParamStringAliases(params, "", "name", "group")
	state := GetParamString(params, "state", "present")
	system := GetParamBool(params, "system", false)
	gid := GetParamString(params, "gid", "")
	checkCmd := fmt.Sprintf("getent group %s", shellQuote(name))
	checkResult, _ := client.Execute(checkCmd)
	exists := checkResult != nil && checkResult.ExitCode == 0
	currentGID := ""
	if exists {
		parts := strings.Split(strings.TrimSpace(checkResult.Stdout), ":")
		if len(parts) >= 3 {
			currentGID = strings.TrimSpace(parts[2])
		}
	}

	cmd, changed, err := buildGroupCommand(groupCommandSpec{
		Name:       name,
		State:      state,
		System:     system,
		GID:        gid,
		CurrentGID: currentGID,
	}, exists)
	if err != nil {
		return nil, err
	}
	if !changed {
		return &Result{Changed: false, Message: fmt.Sprintf("group %s already in desired state", name)}, nil
	}
	if IsCheckMode(params) {
		return &Result{
			Changed: true,
			Message: fmt.Sprintf("check mode: group %s would change", name),
		}, nil
	}

	result, err := client.Execute(cmd)
	if err != nil {
		return result, err
	}
	result.Changed = true
	return result, nil
}

type userCommandSpec struct {
	Name         string
	State        string
	System       bool
	CreateHome   bool
	Shell        string
	Group        string
	Groups       string
	Append       bool
	Password     string
	UID          string
	Comment      string
	Home         string
	Expires      string
	MoveHome     bool
	Remove       bool
	PasswordLock *bool
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
			if spec.PasswordLock != nil {
				lockCmd := "usermod -U " + quotedName
				if *spec.PasswordLock {
					lockCmd = "usermod -L " + quotedName
				}
				if cmd != "" {
					cmd += " && " + lockCmd
				} else {
					cmd = lockCmd
				}
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
		cmd := "useradd " + strings.Join(args, " ") + " " + quotedName
		if spec.PasswordLock != nil && *spec.PasswordLock {
			cmd += " && usermod -L " + quotedName
		}
		return cmd, "", true, nil
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
	Name       string
	State      string
	System     bool
	GID        string
	CurrentGID string
}

func buildGroupCommand(spec groupCommandSpec, exists bool) (string, bool, error) {
	quotedName := shellQuote(spec.Name)

	switch spec.State {
	case "present":
		if exists {
			if spec.GID == "" || spec.GID == spec.CurrentGID {
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
