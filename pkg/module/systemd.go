package module

import (
	"fmt"

	"go-ansible/pkg/ssh"
)

type SystemdModule struct{}

func (m *SystemdModule) Name() string { return "systemd" }

func (m *SystemdModule) Validate(params map[string]interface{}) error {
	name := GetParamString(params, "name", "")
	state := GetParamString(params, "state", "")
	_, hasEnabled := getOptionalBoolParam(params, "enabled")
	_, hasMasked := getOptionalBoolParam(params, "masked")
	daemonReload := GetParamBool(params, "daemon_reload", false)
	daemonReexec := GetParamBool(params, "daemon_reexec", false)

	if state != "" || hasEnabled || hasMasked {
		if name == "" {
			return fmt.Errorf("systemd module requires name when state, enabled, or masked is set")
		}
	}

	if name == "" && !daemonReload && !daemonReexec {
		return fmt.Errorf("systemd module requires name or daemon_reload/daemon_reexec")
	}

	return nil
}

func (m *SystemdModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	checkSystemd, err := client.Execute("which systemctl 2>/dev/null")
	if err != nil {
		return checkSystemd, err
	}
	if checkSystemd.ExitCode != 0 {
		return &Result{
			Failed:   true,
			ExitCode: checkSystemd.ExitCode,
			Message:  "systemctl is required for systemd module",
			Stderr:   checkSystemd.Stderr,
		}, fmt.Errorf("systemctl is required for systemd module")
	}

	name := GetParamString(params, "name", "")
	state := GetParamString(params, "state", "")
	enabled, hasEnabled := getOptionalBoolParam(params, "enabled")
	masked, hasMasked := getOptionalBoolParam(params, "masked")
	daemonReload := GetParamBool(params, "daemon_reload", false)
	daemonReexec := GetParamBool(params, "daemon_reexec", false)

	result := &Result{}

	if daemonReload {
		stepResult, stepErr := client.Execute("systemctl daemon-reload")
		if stepErr != nil {
			return stepResult, stepErr
		}
		result = mergeModuleResults(result, stepResult, true)
	}

	if daemonReexec {
		stepResult, stepErr := client.Execute("systemctl daemon-reexec")
		if stepErr != nil {
			return stepResult, stepErr
		}
		result = mergeModuleResults(result, stepResult, true)
	}

	if hasMasked {
		cmd := buildSystemdMaskedCommand(name, masked)
		stepResult, stepErr := client.Execute(cmd)
		if stepErr != nil {
			return stepResult, stepErr
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

	if hasEnabled {
		cmd := buildServiceEnabledCommand(true, name, enabled)
		stepResult, stepErr := client.Execute(cmd)
		if stepErr != nil {
			return stepResult, stepErr
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

	if state != "" {
		cmd, expectedChange, cmdErr := buildServiceStateCommand(true, name, state)
		if cmdErr != nil {
			return nil, cmdErr
		}
		stepResult, stepErr := client.Execute(cmd)
		if stepErr != nil {
			return stepResult, stepErr
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

	if !daemonReload && !daemonReexec && !hasMasked && !hasEnabled && state == "" {
		return client.Execute(buildServiceStatusCommand(true, name))
	}

	return result, nil
}

func buildSystemdMaskedCommand(name string, masked bool) string {
	quotedName := shellQuote(name)
	if masked {
		return fmt.Sprintf("if systemctl is-enabled %s 2>/dev/null | grep -qx masked; then echo %s; else systemctl mask %s && echo %s; fi", quotedName, changeMarkerFalse, quotedName, changeMarkerTrue)
	}
	return fmt.Sprintf("if ! systemctl is-enabled %s 2>/dev/null | grep -qx masked; then echo %s; else systemctl unmask %s && echo %s; fi", quotedName, changeMarkerFalse, quotedName, changeMarkerTrue)
}
