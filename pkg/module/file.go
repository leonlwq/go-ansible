package module

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"go-ansible/pkg/ssh"
)

// FileModule file 模块
type FileModule struct{}

func (m *FileModule) Name() string { return "file" }

func (m *FileModule) Validate(params map[string]interface{}) error {
	if !HasAnyParam(params, "path", "dest", "name") {
		return fmt.Errorf("file module requires path, dest or name")
	}
	return nil
}

func (m *FileModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	path := GetParamStringAliases(params, "", "path", "dest", "name")

	state := GetParamString(params, "state", "file")
	mode := GetParamMode(params, "mode", "")
	owner := GetParamString(params, "owner", "")
	group := GetParamString(params, "group", "")
	src := GetParamString(params, "src", "")
	recurse := GetParamBool(params, "recurse", false)
	backup := GetParamBool(params, "backup", false)
	force := GetParamBool(params, "force", false)
	checkMode := IsCheckMode(params)
	mtime := GetParamString(params, "modification_time", "")
	atime := GetParamString(params, "access_time", "")
	cmd, err := buildFileStateCommandWithTimes(state, path, src, backup, force, mtime, atime, checkMode)
	if err != nil {
		return nil, err
	}

	result, err := client.Execute(cmd)
	if err != nil {
		return result, err
	}
	stateChanged := fileStateChanged(state, result.Stdout)
	HandleChangedMarker(result)
	if result.Changed {
		stateChanged = true
	}
	if checkMode {
		permChanged := false
		if state != "absent" && (mode != "" || owner != "" || group != "") {
			if stateChanged {
				permChanged = true
			} else {
				permChanged, _ = CheckOrSetFilePermissions(client, path, mode, owner, group, recurse, true)
			}
		}
		result.Changed = stateChanged || permChanged
		if result.Changed {
			result.Message = "check mode: file state would change"
		} else {
			result.Message = "check mode: file already in desired state"
		}
		return result, nil
	}

	// 设置权限与属组
	permChanged, err := SetFilePermissions(client, path, mode, owner, group, recurse)
	if err != nil {
		result.Stderr = err.Error()
	}

	result.Changed = stateChanged || permChanged
	return result, nil
}

func formatTouchTimestamp(tStr string) string {
	tStr = strings.TrimSpace(tStr)
	if tStr == "" || tStr == "now" || tStr == "preserve" {
		return ""
	}
	layouts := []string{
		"200601021504.05",
		"20060102150405",
		"200601021504",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, tStr); err == nil {
			return t.Format("200601021504.05")
		}
	}
	if epoch, err := strconv.ParseInt(tStr, 10, 64); err == nil {
		t := time.Unix(epoch, 0)
		return t.Format("200601021504.05")
	}
	return tStr
}

func buildTouchCommand(quotedPath, mtime, atime string) string {
	mtime = strings.TrimSpace(mtime)
	atime = strings.TrimSpace(atime)

	if mtime == "preserve" && atime == "preserve" {
		return fmt.Sprintf("if [ ! -e %s ]; then touch %s && echo %s; else echo %s; fi", quotedPath, quotedPath, changeMarkerTrue, changeMarkerFalse)
	}

	flags := ""
	mTs := formatTouchTimestamp(mtime)
	aTs := formatTouchTimestamp(atime)

	if mTs != "" && aTs != "" && mTs == aTs {
		flags = fmt.Sprintf(" -t %s", mTs)
	} else {
		if mTs != "" {
			flags += fmt.Sprintf(" -m -t %s", mTs)
		}
		if aTs != "" {
			flags += fmt.Sprintf(" -a -t %s", aTs)
		}
	}

	return fmt.Sprintf("touch%s %s && echo %s", flags, quotedPath, changeMarkerTrue)
}

func buildFileStateCommand(state, path, src string, backup, force bool, checkMode ...bool) (string, error) {
	return buildFileStateCommandWithTimes(state, path, src, backup, force, "", "", checkMode...)
}

func buildFileStateCommandWithTimes(state, path, src string, backup, force bool, mtime, atime string, checkMode ...bool) (string, error) {
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
			return fmt.Sprintf("echo %s", changeMarkerTrue), nil
		}
		return buildTouchCommand(quotedPath, mtime, atime), nil
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
		if (mtime != "" || atime != "") && !check {
			return fmt.Sprintf("if [ -e %s ]; then %s; else echo file missing: %s >&2; exit 1; fi", quotedPath, buildTouchCommand(quotedPath, mtime, atime), quotedPath), nil
		}
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
	if !HasAnyParam(params, "path", "dest", "name") {
		return fmt.Errorf("stat module requires path, dest or name")
	}
	return nil
}

func (m *StatModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	path := GetParamStringAliases(params, "", "path", "dest", "name")
	getChecksum := GetParamBool(params, "get_checksum", true)
	getMd5 := GetParamBool(params, "get_md5", false)
	checksumAlgo := strings.ToLower(strings.TrimSpace(GetParamString(params, "checksum_algorithm", GetParamString(params, "checksum_algo", "sha1"))))
	if checksumAlgo == "" {
		checksumAlgo = "sha1"
	}

	quotedPath := shellQuote(path)
	script := fmt.Sprintf(`
		P=%s
		if [ -e "$P" ] || [ -L "$P" ]; then
			echo "exists: true"
			echo "stat:"
			if [ -L "$P" ]; then
				echo "  islnk: true"
				echo "  lnk_target: $(readlink "$P" 2>/dev/null)"
			else
				echo "  islnk: false"
			fi
			if [ -f "$P" ]; then
				echo "  isdir: false"
				echo "  isfile: true"
				echo "  size: $(stat -c%%s "$P" 2>/dev/null || stat -f%%z "$P" 2>/dev/null)"
			elif [ -d "$P" ]; then
				echo "  isdir: true"
				echo "  isfile: false"
			fi
			echo "  mode: $(stat -c%%a "$P" 2>/dev/null || stat -f%%Lp "$P" 2>/dev/null)"
			echo "  atime: $(stat -c%%X "$P" 2>/dev/null || stat -f%%a "$P" 2>/dev/null)"
			echo "  mtime: $(stat -c%%Y "$P" 2>/dev/null || stat -f%%m "$P" 2>/dev/null)"
			echo "  ctime: $(stat -c%%Z "$P" 2>/dev/null || stat -f%%c "$P" 2>/dev/null)"
			echo "  uid: $(stat -c%%u "$P" 2>/dev/null || stat -f%%u "$P" 2>/dev/null)"
			echo "  gid: $(stat -c%%g "$P" 2>/dev/null || stat -f%%g "$P" 2>/dev/null)"
			echo "  pw_name: $(stat -c%%U "$P" 2>/dev/null || stat -f%%Su "$P" 2>/dev/null)"
			echo "  gr_name: $(stat -c%%G "$P" 2>/dev/null || stat -f%%Sg "$P" 2>/dev/null)"
			if [ -r "$P" ]; then echo "  readable: true"; else echo "  readable: false"; fi
			if [ -w "$P" ]; then echo "  writable: true"; else echo "  writable: false"; fi
			if [ -x "$P" ]; then echo "  executable: true"; else echo "  executable: false"; fi
		else
			echo "exists: false"
		fi
	`, quotedPath)

	if getChecksum {
		script += fmt.Sprintf("if [ -f %s ]; then echo \"  checksum: $(%s)\"; fi\n", quotedPath, checksumCommandForAlgo(quotedPath, checksumAlgo))
	}

	if getMd5 {
		script += fmt.Sprintf("if [ -f %s ]; then echo \"  md5: $(%s)\"; fi\n", quotedPath, checksumCommandForAlgo(quotedPath, "md5"))
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
		case "exists", "isdir", "isfile", "islnk", "readable", "executable":
			stat[key] = value == "true"
		case "writable", "writeable":
			b := value == "true"
			stat["writable"] = b
			stat["writeable"] = b
		case "size":
			if size, err := strconv.ParseInt(value, 10, 64); err == nil {
				stat[key] = size
			}
		case "uid", "gid":
			if idVal, err := strconv.Atoi(value); err == nil {
				stat[key] = idVal
			}
		case "mtime", "atime", "ctime":
			if tVal, err := strconv.ParseFloat(value, 64); err == nil {
				stat[key] = tVal
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
		stat["islnk"] = false
	}
	return stat
}
