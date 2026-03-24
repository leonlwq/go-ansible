package module

import (
	"fmt"

	"go-ansible/pkg/ssh"
)

// FileModule file 模块
type FileModule struct{}

func (m *FileModule) Name() string { return "file" }

func (m *FileModule) Validate(params map[string]interface{}) error {
	if _, ok := params["path"]; !ok {
		if _, ok := params["dest"]; !ok {
			return fmt.Errorf("file module requires path or dest")
		}
	}
	return nil
}

func (m *FileModule) Execute(client *ssh.Client, params map[string]interface{}) (*Result, error) {
	path := GetParamString(params, "path", "")
	if path == "" {
		path = GetParamString(params, "dest", "")
	}

	state := GetParamString(params, "state", "file")
	mode := GetParamString(params, "mode", "")
	owner := GetParamString(params, "owner", "")
	group := GetParamString(params, "group", "")
	src := GetParamString(params, "src", "")
	recurse := GetParamBool(params, "recurse", false)

	var cmd string

	switch state {
	case "directory":
		cmd = fmt.Sprintf("mkdir -p %s", path)
	case "touch":
		cmd = fmt.Sprintf("touch %s", path)
	case "absent":
		cmd = fmt.Sprintf("rm -rf %s", path)
	case "link":
		if src == "" {
			return nil, fmt.Errorf("src is required for state=link")
		}
		cmd = fmt.Sprintf("ln -snf %s %s", src, path)
	case "hard":
		if src == "" {
			return nil, fmt.Errorf("src is required for state=hard")
		}
		cmd = fmt.Sprintf("ln -f %s %s", src, path)
	case "file":
		cmd = fmt.Sprintf("test -e %s", path)
	default:
		return nil, fmt.Errorf("unsupported state: %s", state)
	}

	result, err := client.Execute(cmd)
	if err != nil {
		return result, err
	}

	// 设置权限
	if mode != "" {
		chmodCmd := fmt.Sprintf("chmod %s %s", mode, path)
		if recurse {
			chmodCmd = fmt.Sprintf("chmod -R %s %s", mode, path)
		}
		if _, err := client.Execute(chmodCmd); err != nil {
			result.Stderr = fmt.Sprintf("failed to set mode: %v", err)
		}
	}

	// 设置所有者
	if owner != "" {
		chownCmd := fmt.Sprintf("chown %s %s", owner, path)
		if recurse {
			chownCmd = fmt.Sprintf("chown -R %s:%s %s", owner, group, path)
		} else if group != "" {
			chownCmd = fmt.Sprintf("chown %s:%s %s", owner, group, path)
		}
		if _, err := client.Execute(chownCmd); err != nil {
			result.Stderr = fmt.Sprintf("failed to set owner: %v", err)
		}
	} else if group != "" {
		chgrpCmd := fmt.Sprintf("chgrp %s %s", group, path)
		if recurse {
			chgrpCmd = fmt.Sprintf("chgrp -R %s %s", group, path)
		}
		if _, err := client.Execute(chgrpCmd); err != nil {
			result.Stderr = fmt.Sprintf("failed to set group: %v", err)
		}
	}

	result.Changed = state != "file" || mode != "" || owner != "" || group != ""
	return result, nil
}

// StatModule stat 模块
type StatModule struct{}

func (m *StatModule) Name() string { return "stat" }

func (m *StatModule) Validate(params map[string]interface{}) error {
	if _, ok := params["path"]; !ok {
		return fmt.Errorf("stat module requires path")
	}
	return nil
}

func (m *StatModule) Execute(client *ssh.Client, params map[string]interface{}) (*Result, error) {
	path := GetParamString(params, "path", "")
	getChecksum := GetParamBool(params, "get_checksum", false)
	getMd5 := GetParamBool(params, "get_md5", false)

	script := fmt.Sprintf(`
		if [ -e %q ]; then
			echo "exists: true"
			echo "stat:"
			if [ -f %q ]; then
				echo "  isdir: false"
				echo "  isfile: true"
				echo "  size: $(stat -c%%s %q 2>/dev/null || stat -f%%z %q 2>/dev/null)"
			elif [ -d %q ]; then
				echo "  isdir: true"
				echo "  isfile: false"
			fi
			echo "  mode: $(stat -c%%a %q 2>/dev/null || stat -f%%Lp %q 2>/dev/null)"
		else
			echo "exists: false"
		fi
	`, path, path, path, path, path, path, path)

	if getChecksum {
		script += fmt.Sprintf("if [ -f %q ]; then echo \"  checksum: $(sha256sum %q 2>/dev/null | cut -d' ' -f1 || shasum -a 256 %q 2>/dev/null | cut -d' ' -f1)\"; fi\n", path, path, path)
	}

	if getMd5 {
		script += fmt.Sprintf("if [ -f %q ]; then echo \"  md5: $(md5sum %q 2>/dev/null | cut -d' ' -f1 || md5 -q %q 2>/dev/null)\"; fi\n", path, path, path)
	}

	return client.Execute(script)
}
