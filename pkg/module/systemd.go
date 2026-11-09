package module

import (
	"fmt"
	"strings"

	"go-ansible/pkg/ssh"
)

type SystemdModule struct{}

func (m *SystemdModule) Name() string { return "systemd" }

func (m *SystemdModule) Validate(params map[string]interface{}) error {
	name := GetParamString(params, "name", "")
	state := GetParamString(params, "state", "")
	_, hasEnabled := GetOptionalBoolParam(params, "enabled")
	_, hasMasked := GetOptionalBoolParam(params, "masked")
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
	enabled, hasEnabled := GetOptionalBoolParam(params, "enabled")
	masked, hasMasked := GetOptionalBoolParam(params, "masked")
	if state == "masked" {
		masked = true
		hasMasked = true
		state = ""
	}
	daemonReload := GetParamBool(params, "daemon_reload", false)
	daemonReexec := GetParamBool(params, "daemon_reexec", false)

	scope := strings.ToLower(strings.TrimSpace(GetParamString(params, "scope", "system")))
	systemctlCmd := "systemctl"
	switch scope {
	case "user":
		systemctlCmd = "systemctl --user"
	case "global":
		systemctlCmd = "systemctl --global"
	}

	if IsCheckMode(params) {
		stateChanged := false
		if state != "" {
			activeRes, _ := client.Execute(fmt.Sprintf("%s is-active %s 2>/dev/null", systemctlCmd, shellQuote(name)))
			isActive := activeRes != nil && strings.TrimSpace(activeRes.Stdout) == "active"
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
		}

		enabledChanged := false
		if hasEnabled {
			enRes, _ := client.Execute(fmt.Sprintf("%s is-enabled %s 2>/dev/null", systemctlCmd, shellQuote(name)))
			isCurrentlyEnabled := enRes != nil && strings.TrimSpace(enRes.Stdout) == "enabled"
			if isCurrentlyEnabled != enabled {
				enabledChanged = true
			}
		}

		maskedChanged := false
		if hasMasked {
			mRes, _ := client.Execute(fmt.Sprintf("%s is-enabled %s 2>/dev/null", systemctlCmd, shellQuote(name)))
			isCurrentlyMasked := mRes != nil && strings.TrimSpace(mRes.Stdout) == "masked"
			if isCurrentlyMasked != masked {
				maskedChanged = true
			}
		}

		changed := daemonReload || daemonReexec || stateChanged || enabledChanged || maskedChanged
		return &Result{
			Changed: changed,
			Message: "check mode: systemd state checked",
		}, nil
	}

	result := &Result{}

	if daemonReload {
		stepResult, stepErr := client.Execute(fmt.Sprintf("%s daemon-reload", systemctlCmd))
		if stepErr != nil {
			return stepResult, stepErr
		}
		result = MergeModuleResults(result, stepResult, true)
	}

	if daemonReexec {
		stepResult, stepErr := client.Execute(fmt.Sprintf("%s daemon-reexec", systemctlCmd))
		if stepErr != nil {
			return stepResult, stepErr
		}
		result = MergeModuleResults(result, stepResult, true)
	}

	if hasMasked {
		cmd := buildSystemdMaskedCommandWithCmd(systemctlCmd, name, masked)
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
		result = MergeModuleResults(result, stepResult, changed)
	}

	if hasEnabled {
		cmd := buildServiceEnabledCommand(true, name, enabled)
		if systemctlCmd != "systemctl" {
			cmd = strings.ReplaceAll(cmd, "systemctl ", systemctlCmd+" ")
		}
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
		result = MergeModuleResults(result, stepResult, changed)
	}

	if state != "" {
		cmd, expectedChange, cmdErr := buildServiceStateCommand(true, name, state)
		if cmdErr != nil {
			return nil, cmdErr
		}
		if systemctlCmd != "systemctl" {
			cmd = strings.ReplaceAll(cmd, "systemctl ", systemctlCmd+" ")
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
		result = MergeModuleResults(result, stepResult, changed)
	}

	if !daemonReload && !daemonReexec && !hasMasked && !hasEnabled && state == "" {
		cmd := buildServiceStatusCommand(true, name)
		if systemctlCmd != "systemctl" {
			cmd = strings.ReplaceAll(cmd, "systemctl ", systemctlCmd+" ")
		}
		return client.Execute(cmd)
	}

	return result, nil
}

func buildSystemdMaskedCommand(name string, masked bool) string {
	return buildSystemdMaskedCommandWithCmd("systemctl", name, masked)
}

func buildSystemdMaskedCommandWithCmd(systemctlCmd, name string, masked bool) string {
	quotedName := shellQuote(name)
	if masked {
		return fmt.Sprintf("if %s is-enabled %s 2>/dev/null | grep -qx masked; then echo %s; else %s mask %s && echo %s; fi", systemctlCmd, quotedName, changeMarkerFalse, systemctlCmd, quotedName, changeMarkerTrue)
	}
	return fmt.Sprintf("if ! %s is-enabled %s 2>/dev/null | grep -qx masked; then echo %s; else %s unmask %s && echo %s; fi", systemctlCmd, quotedName, changeMarkerFalse, systemctlCmd, quotedName, changeMarkerTrue)
}
