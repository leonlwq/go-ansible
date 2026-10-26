package module

import (
	"fmt"
	"strconv"
	"strings"

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

func (m *FileModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	path := GetParamString(params, "path", "")
	if path == "" {
		path = GetParamString(params, "dest", "")
	}

	state := GetParamString(params, "state", "file")
	mode := GetParamMode(params, "mode", "")
	owner := GetParamString(params, "owner", "")
	group := GetParamString(params, "group", "")
	src := GetParamString(params, "src", "")
	recurse := GetParamBool(params, "recurse", false)
	backup := GetParamBool(params, "backup", false)
	force := GetParamBool(params, "force", false)
	checkMode := GetParamBool(params, "__check_mode", false)
	cmd, err := buildFileStateCommand(state, path, src, backup, force, checkMode)
	if err != nil {
		return nil, err
	}

	result, err := client.Execute(cmd)
	if err != nil {
		return result, err
	}
	result.Stdout, _, _ = parseChangedMarker(result.Stdout)
	result.Stderr, _, _ = parseChangedMarker(result.Stderr)
	if checkMode {
		result.Changed = fileStateChanged(state, result.Stdout) || mode != "" || owner != "" || group != ""
		if result.Changed {
			result.Message = "check mode: file state would change"
		} else {
			result.Message = "check mode: file already in desired state"
		}
		return result, nil
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

	result.Changed = fileStateChanged(state, result.Stdout) || mode != "" || owner != "" || group != ""
	return result, nil
}

func buildFileStateCommand(state, path, src string, backup, force bool, checkMode ...bool) (string, error) {
	quotedPath := shellQuote(path)
	quotedSrc := shellQuote(src)
	backupCmd := ""
	if backup {
		backupCmd = fmt.Sprintf("cp -a %s %s.bak && ", quotedPath, quotedPath)
	}
	check := len(checkMode) > 0 && checkMode[0]

	switch state {
	case "directory":
		if check {
			return fmt.Sprintf("if [ -d %s ]; then echo %s; else echo %s; fi", quotedPath, changeMarkerFalse, changeMarkerTrue), nil
		}
		return fmt.Sprintf("if [ -d %s ]; then echo %s; else mkdir -p %s && echo %s; fi", quotedPath, changeMarkerFalse, quotedPath, changeMarkerTrue), nil
	case "touch":
		if check {
			return fmt.Sprintf("if [ -e %s ]; then echo %s; else echo %s; fi", quotedPath, changeMarkerFalse, changeMarkerTrue), nil
		}
		return fmt.Sprintf("touch %s && echo %s", quotedPath, changeMarkerTrue), nil
	case "absent":
		if check {
			return fmt.Sprintf("if [ -e %s ] || [ -L %s ]; then echo %s; else echo %s; fi", quotedPath, quotedPath, changeMarkerTrue, changeMarkerFalse), nil
		}
		return fmt.Sprintf("if [ -e %s ] || [ -L %s ]; then %srm -rf %s && echo %s; else echo %s; fi", quotedPath, quotedPath, backupCmd, quotedPath, changeMarkerTrue, changeMarkerFalse), nil
	case "link":
		if src == "" {
			return "", fmt.Errorf("src is required for state=link")
		}
		if check {
			return fmt.Sprintf("if [ -L %s ] && [ \"$(readlink %s)\" = %s ]; then echo %s; elif [ -e %s ] || [ -L %s ]; then echo destination exists: %s >&2; exit 1; else echo %s; fi", quotedPath, quotedPath, quotedSrc, changeMarkerFalse, quotedPath, quotedPath, quotedPath, changeMarkerTrue), nil
		}
		if force {
			return fmt.Sprintf("if [ -L %s ] && [ \"$(readlink %s)\" = %s ]; then echo %s; else if [ -e %s ] || [ -L %s ]; then %srm -rf %s; fi && ln -snf %s %s && echo %s; fi", quotedPath, quotedPath, quotedSrc, changeMarkerFalse, quotedPath, quotedPath, backupCmd, quotedPath, quotedSrc, quotedPath, changeMarkerTrue), nil
		}
		return fmt.Sprintf("if [ -L %s ] && [ \"$(readlink %s)\" = %s ]; then echo %s; elif [ -e %s ] || [ -L %s ]; then echo destination exists: %s >&2; exit 1; else ln -snf %s %s && echo %s; fi", quotedPath, quotedPath, quotedSrc, changeMarkerFalse, quotedPath, quotedPath, quotedPath, quotedSrc, quotedPath, changeMarkerTrue), nil
	case "hard":
		if src == "" {
			return "", fmt.Errorf("src is required for state=hard")
		}
		if check {
			return fmt.Sprintf("if [ -e %s ] && [ \"$(stat -c %%i %s 2>/dev/null || stat -f %%i %s 2>/dev/null)\" = \"$(stat -c %%i %s 2>/dev/null || stat -f %%i %s 2>/dev/null)\" ]; then echo %s; elif [ -e %s ] || [ -L %s ]; then echo destination exists: %s >&2; exit 1; else echo %s; fi", quotedPath, quotedPath, quotedPath, quotedSrc, quotedSrc, changeMarkerFalse, quotedPath, quotedPath, quotedPath, changeMarkerTrue), nil
		}
		if force {
			return fmt.Sprintf("if [ -e %s ] && [ \"$(stat -c %%i %s 2>/dev/null || stat -f %%i %s 2>/dev/null)\" = \"$(stat -c %%i %s 2>/dev/null || stat -f %%i %s 2>/dev/null)\" ]; then echo %s; else if [ -e %s ] || [ -L %s ]; then %srm -rf %s; fi && ln -f %s %s && echo %s; fi", quotedPath, quotedPath, quotedPath, quotedSrc, quotedSrc, changeMarkerFalse, quotedPath, quotedPath, backupCmd, quotedPath, quotedSrc, quotedPath, changeMarkerTrue), nil
		}
		return fmt.Sprintf("if [ -e %s ] && [ \"$(stat -c %%i %s 2>/dev/null || stat -f %%i %s 2>/dev/null)\" = \"$(stat -c %%i %s 2>/dev/null || stat -f %%i %s 2>/dev/null)\" ]; then echo %s; elif [ -e %s ] || [ -L %s ]; then echo destination exists: %s >&2; exit 1; else ln -f %s %s && echo %s; fi", quotedPath, quotedPath, quotedPath, quotedSrc, quotedSrc, changeMarkerFalse, quotedPath, quotedPath, quotedPath, quotedSrc, quotedPath, changeMarkerTrue), nil
	case "file":
		return fmt.Sprintf("if [ -e %s ]; then echo %s; else echo file missing: %s >&2; exit 1; fi", quotedPath, changeMarkerFalse, quotedPath), nil
	default:
		return "", fmt.Errorf("unsupported state: %s", state)
	}
}

func fileStateChanged(state, stdout string) bool {
	if state == "touch" {
		return true
	}
	return strings.Contains(stdout, changeMarkerTrue)
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

func (m *StatModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
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

	result, err := client.Execute(script)
	if result != nil {
		result.Changed = false
		result.Data = map[string]interface{}{
			"stat": parseStatOutput(result.Stdout, path),
		}
	}
	return result, err
}

func parseStatOutput(output, path string) map[string]interface{} {
	stat := map[string]interface{}{
		"exists": false,
		"path":   path,
	}

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "stat:" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "exists", "isdir", "isfile":
			stat[key] = value == "true"
		case "size":
			if size, err := strconv.ParseInt(value, 10, 64); err == nil {
				stat[key] = size
			}
		default:
			if value != "" {
				stat[key] = value
			}
		}
	}

	if exists, _ := stat["exists"].(bool); !exists {
		stat["isdir"] = false
		stat["isfile"] = false
	}
	return stat
}
