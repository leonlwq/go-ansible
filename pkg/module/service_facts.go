package module

import (
	"fmt"
	"strconv"
	"strings"

	"go-ansible/pkg/ssh"
)

// ServiceFactsModule 实现了 ansible.builtin.service_facts 核心功能
type ServiceFactsModule struct{}

func (m *ServiceFactsModule) Name() string {
	return "service_facts"
}

func (m *ServiceFactsModule) Validate(params map[string]interface{}) error {
	return nil
}

func (m *ServiceFactsModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	script := `
if command -v systemctl >/dev/null 2>&1; then
    echo "TYPE:systemd"
    echo "---UNITS---"
    systemctl list-units --type=service --all --no-pager --plain 2>/dev/null || true
    echo "---FILES---"
    systemctl list-unit-files --type=service --no-pager --plain 2>/dev/null || true
elif command -v service >/dev/null 2>&1; then
    echo "TYPE:sysv"
    service --status-all 2>&1 || true
elif [ "$(uname -s)" = "Darwin" ] && command -v launchctl >/dev/null 2>&1; then
    echo "TYPE:launchd"
    launchctl list 2>/dev/null || true
else
    echo "TYPE:none"
fi
`
	res, err := client.Execute(script)
	if err != nil {
		return nil, fmt.Errorf("execute service_facts probe: %w", err)
	}

	stdout := ""
	if res != nil {
		stdout = res.Stdout
	}

	services := parseServiceFactsOutput(stdout)

	return &Result{
		Changed: false,
		Data: map[string]interface{}{
			"ansible_facts": map[string]interface{}{
				"services": services,
			},
		},
		Message: fmt.Sprintf("%d services discovered", len(services)),
	}, nil
}

func parseServiceFactsOutput(output string) map[string]interface{} {
	services := make(map[string]interface{})
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")

	probeType := ""
	section := ""

	fileStatuses := make(map[string]string)
	var unitLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "TYPE:") {
			probeType = strings.TrimPrefix(trimmed, "TYPE:")
			continue
		}

		switch probeType {
		case "systemd":
			if trimmed == "---UNITS---" {
				section = "units"
				continue
			} else if trimmed == "---FILES---" {
				section = "files"
				continue
			}

			if section == "files" {
				// UNIT FILE                    STATE           VENDOR PRESET
				// cron.service                 enabled         enabled
				if strings.HasPrefix(trimmed, "UNIT FILE") || strings.Contains(trimmed, "unit files listed") {
					continue
				}
				fields := strings.Fields(trimmed)
				if len(fields) >= 2 {
					unitFile := fields[0]
					state := fields[1]
					fileStatuses[unitFile] = state
					if strings.HasSuffix(unitFile, ".service") {
						fileStatuses[strings.TrimSuffix(unitFile, ".service")] = state
					}
				}
			} else if section == "units" {
				unitLines = append(unitLines, trimmed)
			}

		case "sysv":
			// [ + ]  cron
			// [ - ]  rsync
			// [ ? ]  hwclock.sh
			if strings.Contains(trimmed, "[") && strings.Contains(trimmed, "]") {
				open := strings.Index(trimmed, "[")
				close := strings.Index(trimmed, "]")
				if open != -1 && close > open {
					sym := strings.TrimSpace(trimmed[open+1 : close])
					name := strings.TrimSpace(trimmed[close+1:])
					if name != "" {
						state := "unknown"
						switch sym {
						case "+":
							state = "running"
						case "-":
							state = "stopped"
						}
						svcInfo := map[string]interface{}{
							"name":   name,
							"state":  state,
							"status": "unknown",
							"source": "sysv",
						}
						services[name] = svcInfo
					}
				}
			}

		case "launchd":
			// PID Status Label
			// 1234 0 com.apple.Finder
			// - 0 com.apple.Spotlight
			if strings.HasPrefix(trimmed, "PID") || strings.HasPrefix(trimmed, "---") {
				continue
			}
			fields := strings.Fields(trimmed)
			if len(fields) >= 3 {
				pidStr := fields[0]
				label := fields[2]
				state := "stopped"
				if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
					state = "running"
				}
				svcInfo := map[string]interface{}{
					"name":   label,
					"state":  state,
					"status": "unknown",
					"source": "launchd",
				}
				services[label] = svcInfo
			}
		}
	}

	// 2. Process systemd unit lines now that fileStatuses is completely populated
	for _, trimmed := range unitLines {
		if strings.HasPrefix(trimmed, "UNIT") || strings.HasPrefix(trimmed, "LOAD") || strings.Contains(trimmed, "loaded units listed") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 4 {
			continue
		}
		unit := fields[0]
		if !strings.HasSuffix(unit, ".service") && !strings.Contains(unit, ".") {
			continue
		}
		active := fields[2]
		sub := fields[3]

		state := "stopped"
		if active == "active" || sub == "running" {
			state = "running"
		}

		status := "unknown"
		if st, ok := fileStatuses[unit]; ok {
			status = st
		} else if st, ok := fileStatuses[strings.TrimSuffix(unit, ".service")]; ok {
			status = st
		}

		svcInfo := map[string]interface{}{
			"name":   unit,
			"state":  state,
			"status": status,
			"source": "systemd",
		}
		services[unit] = svcInfo
		if strings.HasSuffix(unit, ".service") {
			baseName := strings.TrimSuffix(unit, ".service")
			services[baseName] = map[string]interface{}{
				"name":   baseName,
				"state":  state,
				"status": status,
				"source": "systemd",
			}
		}
	}

	return services
}
