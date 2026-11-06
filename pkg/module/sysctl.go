package module

import (
	"fmt"
	"strings"

	"go-ansible/pkg/ssh"
)

// SysctlModule sysctl 模块
type SysctlModule struct{}

func (m *SysctlModule) Name() string { return "sysctl" }

func (m *SysctlModule) Validate(params map[string]interface{}) error {
	name := strings.TrimSpace(getSysctlName(params))
	if name == "" {
		return fmt.Errorf("sysctl module requires name (or key)")
	}

	state := strings.ToLower(strings.TrimSpace(GetParamString(params, "state", "present")))
	if state != "present" && state != "absent" {
		return fmt.Errorf("sysctl state must be present or absent, got %s", state)
	}

	if state == "present" {
		if _, ok := getSysctlValue(params); !ok {
			return fmt.Errorf("sysctl module requires value when state=present")
		}
	}

	return nil
}

func getSysctlName(params map[string]interface{}) string {
	if n := GetParamString(params, "name", ""); n != "" {
		return n
	}
	return GetParamString(params, "key", "")
}

func getSysctlValue(params map[string]interface{}) (string, bool) {
	if val, ok := params["value"]; ok && val != nil {
		return fmt.Sprintf("%v", val), true
	}
	if val, ok := params["val"]; ok && val != nil {
		return fmt.Sprintf("%v", val), true
	}
	return "", false
}

func (m *SysctlModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	name := strings.TrimSpace(getSysctlName(params))
	state := strings.ToLower(strings.TrimSpace(GetParamString(params, "state", "present")))
	sysctlFile := GetParamString(params, "sysctl_file", "/etc/sysctl.conf")
	reload := GetParamBool(params, "reload", true)
	sysctlSet := GetParamBool(params, "sysctl_set", false)
	ignoreErrors := GetParamBool(params, "ignoreerrors", false)

	checkMode := IsCheckMode(params)
	diffMode := IsDiffMode(params)

	targetVal, _ := getSysctlValue(params)
	targetVal = strings.TrimSpace(targetVal)

	// 1. 查询运行时值
	currentRuntime := ""
	queryCmd := fmt.Sprintf("sysctl -n %s 2>/dev/null", shellQuote(name))
	if res, err := client.Execute(queryCmd); err == nil && res != nil && res.ExitCode == 0 {
		currentRuntime = strings.TrimSpace(res.Stdout)
	}

	// 2. 读取配置文件现有内容
	beforeContent := readRemoteFileContent(client, sysctlFile)
	afterContent := beforeContent

	fileChanged := false
	lines := strings.Split(beforeContent, "\n")
	foundLineIdx := -1
	var existingVal string

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		eqIdx := strings.IndexAny(trimmed, "=:")
		if eqIdx != -1 {
			k := strings.TrimSpace(trimmed[:eqIdx])
			v := strings.TrimSpace(trimmed[eqIdx+1:])
			if k == name {
				foundLineIdx = i
				existingVal = v
				break
			}
		}
	}

	if state == "present" {
		newLine := fmt.Sprintf("%s = %s", name, targetVal)
		if foundLineIdx != -1 {
			if normalizeSysctlValue(existingVal) != normalizeSysctlValue(targetVal) {
				lines[foundLineIdx] = newLine
				afterContent = strings.Join(lines, "\n")
				fileChanged = true
			}
		} else {
			// 文件中不存在，追加
			if len(lines) == 1 && lines[0] == "" {
				afterContent = newLine + "\n"
			} else {
				trimmedBefore := strings.TrimRight(beforeContent, "\n")
				afterContent = trimmedBefore + "\n" + newLine + "\n"
			}
			fileChanged = true
		}
	} else if state == "absent" {
		var newLines []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
				newLines = append(newLines, line)
				continue
			}
			eqIdx := strings.IndexAny(trimmed, "=:")
			if eqIdx != -1 {
				k := strings.TrimSpace(trimmed[:eqIdx])
				if k == name {
					fileChanged = true
					continue
				}
			}
			newLines = append(newLines, line)
		}
		if fileChanged {
			afterContent = strings.Join(newLines, "\n")
		}
	}

	// 3. 判断运行时是否需要同步变更
	runtimeChanged := false
	if state == "present" {
		if normalizeSysctlValue(currentRuntime) != normalizeSysctlValue(targetVal) {
			runtimeChanged = true
		}
	}

	changed := fileChanged || runtimeChanged

	diff := ""
	if diffMode && fileChanged {
		diff = buildTextDiff(sysctlFile, beforeContent, afterContent)
	}

	if checkMode {
		return &Result{
			Changed:  changed,
			Diff:     diff,
			ExitCode: 0,
			Message:  fmt.Sprintf("check mode: sysctl %s (file_changed=%v, runtime_changed=%v)", name, fileChanged, runtimeChanged),
			Data: map[string]interface{}{
				"name":          name,
				"value":         targetVal,
				"sysctl_file":   sysctlFile,
				"runtime_value": currentRuntime,
			},
		}, nil
	}

	if !changed {
		return &Result{
			Changed:  false,
			ExitCode: 0,
			Message:  fmt.Sprintf("sysctl %s already satisfied", name),
			Data: map[string]interface{}{
				"name":          name,
				"value":         targetVal,
				"sysctl_file":   sysctlFile,
				"runtime_value": currentRuntime,
			},
		}, nil
	}

	// 4. sysctl_set 预检：在写入 sysctlFile 之前先调用 sysctl -w 校验与应用，防止非法配置破坏文件
	sysctlPreSetDone := false
	if state == "present" && sysctlSet {
		setCmd := fmt.Sprintf("sysctl -w %s=%s", name, shellQuote(targetVal))
		if ignoreErrors {
			setCmd += " 2>/dev/null || true"
		}
		setRes, err := client.Execute(setCmd)
		if err != nil || (setRes != nil && setRes.ExitCode != 0 && !ignoreErrors) {
			errMsg := ""
			if setRes != nil {
				errMsg = setRes.Stderr
			}
			return &Result{
				Failed:   true,
				ExitCode: 1,
				Message:  fmt.Sprintf("failed to pre-set sysctl runtime: %s", errMsg),
			}, fmt.Errorf("pre-set sysctl runtime: %v %s", err, errMsg)
		}
		sysctlPreSetDone = true
		runtimeChanged = false
	}

	// 5. 写入文件变更
	if fileChanged {
		writeCmd := buildContentWriteCommand(sysctlFile, afterContent)
		if writeCmd != "" {
			if writeRes, err := client.Execute(writeCmd); err != nil || (writeRes != nil && writeRes.ExitCode != 0) {
				errMsg := ""
				if writeRes != nil {
					errMsg = writeRes.Stderr
				}
				return &Result{
					Failed:   true,
					ExitCode: 1,
					Message:  fmt.Sprintf("failed to update sysctl file %s: %s", sysctlFile, errMsg),
				}, fmt.Errorf("write %s: %v %s", sysctlFile, err, errMsg)
			}
		}
	}

	// 6. 应用/重载内核运行时参数
	if (fileChanged || state == "present") && reload {
		reloadCmd := fmt.Sprintf("sysctl -p %s", shellQuote(sysctlFile))
		if ignoreErrors {
			reloadCmd += " 2>/dev/null || true"
		}
		reloadRes, err := client.Execute(reloadCmd)
		if err != nil || (reloadRes != nil && reloadRes.ExitCode != 0 && !ignoreErrors) {
			errMsg := ""
			if reloadRes != nil {
				errMsg = reloadRes.Stderr
			}
			return &Result{
				Failed:   true,
				ExitCode: 1,
				Message:  fmt.Sprintf("failed to reload sysctl: %s", errMsg),
			}, fmt.Errorf("reload sysctl: %v %s", err, errMsg)
		}
	}

	if state == "present" {
		if (!sysctlPreSetDone && sysctlSet) || runtimeChanged {
			setCmd := fmt.Sprintf("sysctl -w %s=%s", name, shellQuote(targetVal))
			if ignoreErrors {
				setCmd += " 2>/dev/null || true"
			}
			setRes, err := client.Execute(setCmd)
			if err != nil || (setRes != nil && setRes.ExitCode != 0 && !ignoreErrors) {
				errMsg := ""
				if setRes != nil {
					errMsg = setRes.Stderr
				}
				return &Result{
					Failed:   true,
					ExitCode: 1,
					Message:  fmt.Sprintf("failed to set sysctl runtime: %s", errMsg),
				}, fmt.Errorf("set sysctl runtime: %v %s", err, errMsg)
			}
		}
	}

	// 重新获取最新运行时值
	newRuntime := currentRuntime
	if res, err := client.Execute(queryCmd); err == nil && res != nil && res.ExitCode == 0 {
		newRuntime = strings.TrimSpace(res.Stdout)
	}

	return &Result{
		Changed:  true,
		Diff:     diff,
		ExitCode: 0,
		Message:  fmt.Sprintf("sysctl %s updated", name),
		Data: map[string]interface{}{
			"name":          name,
			"value":         targetVal,
			"sysctl_file":   sysctlFile,
			"runtime_value": newRuntime,
		},
	}, nil
}

func normalizeSysctlValue(v string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(v)), " ")
}
