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

func (m *ServiceModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	name := GetParamString(params, "name", "")
	state := GetParamString(params, "state", "")
	enabled, hasEnabled := getOptionalBoolParam(params, "enabled")

	// 检查是否使用 systemctl
	checkSystemd, _ := client.Execute("which systemctl 2>/dev/null")
	useSystemd := checkSystemd.ExitCode == 0
	result := &Result{}
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
		result = mergeModuleResults(result, stepResult, changed)
	}

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
		result = mergeModuleResults(result, stepResult, changed)
	}

	if state == "" && !hasEnabled {
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

func mergeModuleResults(base, next *Result, changed bool) *Result {
	if base == nil {
		base = &Result{}
	}
	if next == nil {
		base.Changed = base.Changed || changed
		return base
	}
	if next.Stdout != "" {
		if base.Stdout != "" {
			base.Stdout += "\n"
		}
		base.Stdout += next.Stdout
	}
	if next.Stderr != "" {
		if base.Stderr != "" {
			base.Stderr += "\n"
		}
		base.Stderr += next.Stderr
	}
	if next.Message != "" {
		if base.Message != "" {
			base.Message += "\n"
		}
		base.Message += next.Message
	}
	base.ExitCode = next.ExitCode
	base.Failed = base.Failed || next.Failed
	base.Changed = base.Changed || changed || next.Changed
	return base
}

func getOptionalBoolParam(params map[string]interface{}, key string) (bool, bool) {
	value, ok := params[key]
	if !ok {
		return false, false
	}
	switch v := value.(type) {
	case bool:
		return v, true
	case string:
		switch v {
		case "yes", "true", "1", "on":
			return true, true
		case "no", "false", "0", "off":
			return false, true
		}
	}
	return GetParamBool(params, key, false), true
}
