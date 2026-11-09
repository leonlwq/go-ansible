package module

import (
	"fmt"
	"strings"

	"go-ansible/pkg/ssh"
)

// ServiceModule service 模块
type ServiceModule struct{}

func (m *ServiceModule) Name() string { return "service" }

func (m *ServiceModule) Validate(params map[string]interface{}) error {
	if !HasAnyParam(params, "name", "service") {
		return fmt.Errorf("service module requires name or service")
	}
	return nil
}

func (m *ServiceModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	name := GetParamStringAliases(params, "", "name", "service")
	state := GetParamString(params, "state", "")
	enabled, hasEnabled := GetOptionalBoolParam(params, "enabled")
	masked, hasMasked := GetOptionalBoolParam(params, "masked")
	if state == "masked" {
		masked = true
		hasMasked = true
		state = ""
	}
	daemonReload := GetParamBool(params, "daemon_reload", false)

	// 检查是否使用 systemctl
	checkSystemd, _ := client.Execute("which systemctl 2>/dev/null")
	useSystemd := checkSystemd.ExitCode == 0

	if daemonReload && useSystemd && !IsCheckMode(params) {
		_, _ = client.Execute("systemctl daemon-reload")
	}

	if IsCheckMode(params) {
		statusCmd := buildServiceStatusCommand(useSystemd, name)
		statusRes, _ := client.Execute(statusCmd)
		isActive := statusRes != nil && statusRes.ExitCode == 0

		stateChanged := false
		switch state {
		case "started":
			if !isActive {
				stateChanged = true
			}
		case "stopped":
			if isActive {
				stateChanged = true
			}
		case "restarted":
			stateChanged = true
		case "reloaded":
			if isActive {
				stateChanged = true
			}
		}

		enabledChanged := false
		if hasEnabled {
			if useSystemd {
				eRes, _ := client.Execute(fmt.Sprintf("systemctl is-enabled %s 2>/dev/null", shellQuote(name)))
				isCurrentlyEnabled := eRes != nil && strings.TrimSpace(eRes.Stdout) == "enabled"
				if isCurrentlyEnabled != enabled {
					enabledChanged = true
				}
			} else {
				enabledChanged = true
			}
		}

		maskedChanged := false
		if hasMasked {
			if useSystemd {
				mRes, _ := client.Execute(fmt.Sprintf("systemctl is-enabled %s 2>/dev/null", shellQuote(name)))
				isCurrentlyMasked := mRes != nil && strings.TrimSpace(mRes.Stdout) == "masked"
				if isCurrentlyMasked != masked {
					maskedChanged = true
				}
			} else {
				maskedChanged = true
			}
		}

		return &Result{
			Changed: stateChanged || enabledChanged || maskedChanged,
			Message: "check mode: service state checked",
		}, nil
	}

	result := &Result{}

	// 支持 masked 控制
	if hasMasked && useSystemd {
		cmd := buildSystemdMaskedCommandWithCmd("systemctl", name, masked)
		stepResult, err := client.Execute(cmd)
		if err != nil {
			return stepResult, err
		}
		changed := true
		if cleaned, markerChanged, hasMarker := parseChangedMarker(stepResult.Stdout); hasMarker {
			stepResult.Stdout = cleaned
			changed = markerChanged
		}
		if cleaned, _, hasMarker := parseChangedMarker(stepResult.Stderr); hasMarker {
			stepResult.Stderr = cleaned
		}
		result = MergeModuleResults(result, stepResult, changed)
	}

	// Ansible 官方规范：当 enabled 与 state 同时存在时，必须先评估/设置 enabled，再执行 state 变更
	if hasEnabled {
		cmd := buildServiceEnabledCommand(useSystemd, name, enabled)
		stepResult, err := client.Execute(cmd)
		if err != nil {
			return stepResult, err
		}
		changed := true
		if cleaned, markerChanged, hasMarker := parseChangedMarker(stepResult.Stdout); hasMarker {
			stepResult.Stdout = cleaned
			changed = markerChanged
		}
		if cleaned, _, hasMarker := parseChangedMarker(stepResult.Stderr); hasMarker {
			stepResult.Stderr = cleaned
		}
		result = MergeModuleResults(result, stepResult, changed)
	}

	if state != "" {
		cmd, expectedChange, err := buildServiceStateCommand(useSystemd, name, state)
		if err != nil {
			return nil, err
		}
		stepResult, err := client.Execute(cmd)
		if err != nil {
			return stepResult, err
		}
		changed := expectedChange
		if cleaned, markerChanged, hasMarker := parseChangedMarker(stepResult.Stdout); hasMarker {
			stepResult.Stdout = cleaned
			changed = markerChanged
		}
		if cleaned, _, hasMarker := parseChangedMarker(stepResult.Stderr); hasMarker {
			stepResult.Stderr = cleaned
		}
		result = MergeModuleResults(result, stepResult, changed)
	}

	if state == "" && !hasEnabled && !hasMasked {
		cmd := buildServiceStatusCommand(useSystemd, name)
		return client.Execute(cmd)
	}

	return result, nil
}

func buildServiceStateCommand(useSystemd bool, name, state string) (string, bool, error) {
	quotedName := shellQuote(name)

	switch state {
	case "started", "running":
		if useSystemd {
			return fmt.Sprintf("if systemctl is-active --quiet %s; then echo %s; else systemctl start %s && echo %s; fi", quotedName, changeMarkerFalse, quotedName, changeMarkerTrue), false, nil
		}
		return fmt.Sprintf("if service %s status >/dev/null 2>&1; then echo %s; else service %s start && echo %s; fi", quotedName, changeMarkerFalse, quotedName, changeMarkerTrue), false, nil
	case "stopped":
		if useSystemd {
			return fmt.Sprintf("if ! systemctl is-active --quiet %s; then echo %s; else systemctl stop %s && echo %s; fi", quotedName, changeMarkerFalse, quotedName, changeMarkerTrue), false, nil
		}
		return fmt.Sprintf("if ! service %s status >/dev/null 2>&1; then echo %s; else service %s stop && echo %s; fi", quotedName, changeMarkerFalse, quotedName, changeMarkerTrue), false, nil
	case "restarted":
		if useSystemd {
			return fmt.Sprintf("systemctl restart %s", quotedName), true, nil
		}
		return fmt.Sprintf("service %s restart", quotedName), true, nil
	case "reloaded":
		if useSystemd {
			return fmt.Sprintf("systemctl reload %s", quotedName), true, nil
		}
		return fmt.Sprintf("service %s reload", quotedName), true, nil
	default:
		return "", false, fmt.Errorf("unsupported state: %s", state)
	}
}

func buildServiceEnabledCommand(useSystemd bool, name string, enabled bool) string {
	quotedName := shellQuote(name)

	if useSystemd {
		if enabled {
			return fmt.Sprintf("if systemctl is-enabled %s >/dev/null 2>&1; then echo %s; else systemctl enable %s && echo %s; fi", quotedName, changeMarkerFalse, quotedName, changeMarkerTrue)
		}
		return fmt.Sprintf("if ! systemctl is-enabled %s >/dev/null 2>&1; then echo %s; else systemctl disable %s && echo %s; fi", quotedName, changeMarkerFalse, quotedName, changeMarkerTrue)
	}

	if enabled {
		return fmt.Sprintf(`if command -v chkconfig >/dev/null 2>&1; then
  if chkconfig --list %s 2>/dev/null | grep -Eq '(^|[[:space:]])(2|3|4|5):on([[:space:]]|$)'; then
    echo %s
  else
    chkconfig %s on && echo %s
  fi
elif command -v update-rc.d >/dev/null 2>&1; then
  if ls /etc/rc[2345].d/S*%s >/dev/null 2>&1; then
    echo %s
  else
    update-rc.d %s enable && echo %s
  fi
else
  service %s status >/dev/null 2>&1 && echo %s
fi`, quotedName, changeMarkerFalse, quotedName, changeMarkerTrue, quotedName, changeMarkerFalse, quotedName, changeMarkerTrue, quotedName, changeMarkerFalse)
	}
	return fmt.Sprintf(`if command -v chkconfig >/dev/null 2>&1; then
  if ! chkconfig --list %s 2>/dev/null | grep -Eq '(^|[[:space:]])(2|3|4|5):on([[:space:]]|$)'; then
    echo %s
  else
    chkconfig %s off && echo %s
  fi
elif command -v update-rc.d >/dev/null 2>&1; then
  if ! ls /etc/rc[2345].d/S*%s >/dev/null 2>&1; then
    echo %s
  else
    update-rc.d %s disable && echo %s
  fi
else
  echo %s
fi`, quotedName, changeMarkerFalse, quotedName, changeMarkerTrue, quotedName, changeMarkerFalse, quotedName, changeMarkerTrue, changeMarkerFalse)
}

func buildServiceStatusCommand(useSystemd bool, name string) string {
	quotedName := shellQuote(name)
	if useSystemd {
		return fmt.Sprintf("systemctl is-active %s", quotedName)
	}
	return fmt.Sprintf("service %s status", quotedName)
}
