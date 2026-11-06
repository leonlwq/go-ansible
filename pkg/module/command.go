package module

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"go-ansible/pkg/ssh"
)

// CommandModule command 模块
type CommandModule struct{}

func (m *CommandModule) Name() string { return "command" }

func (m *CommandModule) Validate(params map[string]interface{}) error {
	if _, ok := params["_raw_params"]; !ok {
		if _, ok := params["cmd"]; !ok {
			if _, ok := params["argv"]; !ok {
				return fmt.Errorf("command module requires _raw_params, cmd, or argv")
			}
		}
	}
	return nil
}

func buildArgvCommand(v interface{}) string {
	switch args := v.(type) {
	case []interface{}:
		if len(args) == 0 {
			return ""
		}
		parts := make([]string, 0, len(args))
		for _, arg := range args {
			parts = append(parts, shellQuote(fmt.Sprintf("%v", arg)))
		}
		return strings.Join(parts, " ")
	case []string:
		if len(args) == 0 {
			return ""
		}
		parts := make([]string, 0, len(args))
		for _, arg := range args {
			parts = append(parts, shellQuote(arg))
		}
		return strings.Join(parts, " ")
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
}

func (m *CommandModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	var cmd string
	if argvRaw, ok := params["argv"]; ok && argvRaw != nil {
		cmd = buildArgvCommand(argvRaw)
	}
	if cmd == "" {
		cmd = GetParamString(params, "_raw_params", "")
	}
	if cmd == "" {
		cmd = GetParamString(params, "cmd", "")
	}

	chdir := GetParamString(params, "chdir", "")
	creates := GetParamString(params, "creates", "")
	removes := GetParamString(params, "removes", "")

	// 检查 creates 参数
	if creates != "" {
		checkPath := creates
		if chdir != "" && !filepath.IsAbs(creates) {
			checkPath = filepath.Join(chdir, creates)
		}
		result, err := client.Execute(fmt.Sprintf("test -e %s", shellQuote(checkPath)))
		if err == nil && result.ExitCode == 0 {
			result.Changed = false
			result.Message = fmt.Sprintf("skipped, since %s exists", creates)
			return result, nil
		}
	}

	// 检查 removes 参数
	if removes != "" {
		checkPath := removes
		if chdir != "" && !filepath.IsAbs(removes) {
			checkPath = filepath.Join(chdir, removes)
		}
		result, err := client.Execute(fmt.Sprintf("test -e %s", shellQuote(checkPath)))
		if err != nil || result.ExitCode != 0 {
			result.Changed = false
			result.Message = fmt.Sprintf("skipped, since %s does not exist", removes)
			return result, nil
		}
	}

	if chdir != "" {
		cmd = fmt.Sprintf("cd %s && %s", shellQuote(chdir), cmd)
	}

	return client.Execute(cmd)
}

// ShellModule shell 模块
type ShellModule struct{}

func (m *ShellModule) Name() string { return "shell" }

func (m *ShellModule) Validate(params map[string]interface{}) error {
	if _, ok := params["_raw_params"]; !ok {
		if _, ok := params["cmd"]; !ok {
			return fmt.Errorf("shell module requires _raw_params or cmd")
		}
	}
	return nil
}

func (m *ShellModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	cmd := GetParamString(params, "_raw_params", "")
	if cmd == "" {
		cmd = GetParamString(params, "cmd", "")
	}

	chdir := GetParamString(params, "chdir", "")
	executable := GetParamString(params, "executable", "")
	if executable == "" {
		executable = "/bin/sh"
	}

	creates := GetParamString(params, "creates", "")
	removes := GetParamString(params, "removes", "")

	if creates != "" {
		checkPath := creates
		if chdir != "" && !filepath.IsAbs(creates) {
			checkPath = filepath.Join(chdir, creates)
		}
		result, err := client.Execute(fmt.Sprintf("test -e %s", shellQuote(checkPath)))
		if err == nil && result.ExitCode == 0 {
			result.Changed = false
			result.Message = fmt.Sprintf("skipped, since %s exists", creates)
			return result, nil
		}
	}

	if removes != "" {
		checkPath := removes
		if chdir != "" && !filepath.IsAbs(removes) {
			checkPath = filepath.Join(chdir, removes)
		}
		result, err := client.Execute(fmt.Sprintf("test -e %s", shellQuote(checkPath)))
		if err != nil || result.ExitCode != 0 {
			result.Changed = false
			result.Message = fmt.Sprintf("skipped, since %s does not exist", removes)
			return result, nil
		}
	}

	targetCmd := cmd
	if chdir != "" {
		targetCmd = fmt.Sprintf("cd %s && %s", shellQuote(chdir), cmd)
	}

	fullCmd := fmt.Sprintf("%s -c %q", executable, targetCmd)
	return client.Execute(fullCmd)
}

// PingModule ping 模块
type PingModule struct{}

func (m *PingModule) Name() string { return "ping" }

func (m *PingModule) Validate(params map[string]interface{}) error {
	return nil
}

func (m *PingModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	result, err := client.Execute("echo pong")
	if err != nil {
		return result, err
	}
	result.Changed = false
	result.Message = "pong"
	return result, nil
}

// SetupModule setup 模块（收集主机信息）
type SetupModule struct{}

func (m *SetupModule) Name() string { return "setup" }

func (m *SetupModule) Validate(params map[string]interface{}) error {
	return nil
}

func shouldGatherSubset(subsets []string, name string) bool {
	if len(subsets) == 0 {
		return true
	}
	isMin := false
	hasAll := false
	for _, s := range subsets {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "min" {
			isMin = true
		}
		if s == "all" {
			hasAll = true
		}
	}
	defaultEnabled := true
	if isMin && !hasAll {
		defaultEnabled = false
	}
	for _, s := range subsets {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "!all" {
			defaultEnabled = false
		}
	}
	enabled := defaultEnabled
	for _, s := range subsets {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == name {
			enabled = true
		} else if s == "!"+name {
			enabled = false
		}
	}
	return enabled
}

func (m *SetupModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	filter := GetParamString(params, "filter", "")

	if timeoutSec := GetParamInt(params, "gather_timeout", 0); timeoutSec > 0 {
		if timeoutSetter, ok := client.(interface{ SetTimeout(time.Duration) }); ok {
			origTimeout := time.Duration(0)
			if timeoutGetter, ok := client.(interface{ GetTimeout() time.Duration }); ok {
				origTimeout = timeoutGetter.GetTimeout()
			}
			timeoutSetter.SetTimeout(time.Duration(timeoutSec) * time.Second)
			if origTimeout > 0 {
				defer timeoutSetter.SetTimeout(origTimeout)
			}
		}
	}

	var subsets []string
	switch v := params["gather_subset"].(type) {
	case []string:
		subsets = v
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				subsets = append(subsets, s)
			}
		}
	case string:
		for _, part := range strings.Split(v, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				subsets = append(subsets, trimmed)
			}
		}
	}

	includeHW := shouldGatherSubset(subsets, "hardware")
	includeNet := shouldGatherSubset(subsets, "network")
	includeEnv := shouldGatherSubset(subsets, "env")

	var script strings.Builder
	script.WriteString(`
		echo "gathered_facts:"
		echo "  ansible_hostname: $(hostname)"
		echo "  ansible_fqdn: $(hostname -f 2>/dev/null || hostname)"
		echo "  ansible_architecture: $(uname -m)"
		echo "  ansible_kernel: $(uname -r)"
		echo "  ansible_os_family: $(if [ -f /etc/os-release ]; then . /etc/os-release; case \"$ID_LIKE $ID\" in *rhel*|*centos*|*fedora*|*rocky*|*alma*) echo 'RedHat' ;; *debian*|*ubuntu*) echo 'Debian' ;; *alpine*) echo 'Alpine' ;; *arch*) echo 'Archlinux' ;; *suse*) echo 'Suse' ;; *) echo \"$ID\" ;; esac; elif [ \"$(uname -s)\" = 'Darwin' ]; then echo 'Darwin'; else uname -s; fi)"
		DIST_NAME=""
		DIST_VER=""
		if [ -f /etc/os-release ]; then
			. /etc/os-release
			case "$NAME" in
				*Ubuntu*) DIST_NAME="Ubuntu" ;;
				*Debian*) DIST_NAME="Debian" ;;
				*CentOS*) DIST_NAME="CentOS" ;;
				*Red\ Hat*|*RHEL*) DIST_NAME="RedHat" ;;
				*Rocky*) DIST_NAME="Rocky" ;;
				*AlmaLinux*) DIST_NAME="AlmaLinux" ;;
				*Alpine*) DIST_NAME="Alpine" ;;
				*Fedora*) DIST_NAME="Fedora" ;;
				*Amazon*) DIST_NAME="Amazon" ;;
				*Arch*) DIST_NAME="Archlinux" ;;
				*) DIST_NAME="${NAME:-$ID}" ;;
			esac
			DIST_VER="$VERSION_ID"
		elif [ "$(uname -s)" = "Darwin" ]; then
			DIST_NAME="MacOSX"
			DIST_VER="$(sw_vers -productVersion 2>/dev/null)"
		else
			DIST_NAME="$(uname -s)"
			DIST_VER="$(uname -r)"
		fi
		echo "  ansible_distribution: \"$DIST_NAME\""
		echo "  ansible_distribution_version: \"$DIST_VER\""
		echo "  ansible_python_version: $(python3 --version 2>/dev/null || python --version 2>/dev/null || echo 'Not installed')"
		echo "  ansible_user_id: \"$(whoami 2>/dev/null || echo $USER)\""
		echo "  ansible_user_dir: \"$HOME\""
`)
	if includeHW {
		script.WriteString(`
		echo "  ansible_processor_count: $(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo '1')"
		echo "  ansible_memtotal_mb: $(free -m 2>/dev/null | awk '/^Mem:/{print $2}' || echo 'N/A')"
`)
	}
	if includeNet {
		script.WriteString(`
		DEF_IP=$(ip route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}' || true)
		DEF_INT=$(ip route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="dev") print $(i+1)}' || true)
		DEF_GW=$(ip route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="via") print $(i+1)}' || true)
		if [ -z "$DEF_IP" ] && [ "$(uname -s)" = "Darwin" ]; then
			DEF_INT=$(route -n get default 2>/dev/null | awk '/interface:/{print $2}')
			DEF_GW=$(route -n get default 2>/dev/null | awk '/gateway:/{print $2}')
			if [ -n "$DEF_INT" ]; then
				DEF_IP=$(ifconfig "$DEF_INT" 2>/dev/null | awk '/inet /{print $2}')
			fi
		fi
		if [ -z "$DEF_IP" ]; then
			DEF_IP=$(hostname -I 2>/dev/null | awk '{print $1}' || ifconfig 2>/dev/null | awk '/inet /{print $2}' | grep -v '127.0.0.1' | head -n1 || echo '127.0.0.1')
		fi
		echo "  ansible_default_ipv4:"
		echo "    address: \"$DEF_IP\""
		echo "    interface: \"$DEF_INT\""
		echo "    gateway: \"$DEF_GW\""
		echo "  ansible_all_ipv4_addresses:"
		for ip in $(hostname -I 2>/dev/null || ifconfig 2>/dev/null | awk '/inet /{print $2}' | grep -v '127.0.0.1' || echo "$DEF_IP"); do
			echo "    - \"$ip\""
		done
`)
	}
	if includeEnv {
		script.WriteString(`
		echo "  ansible_env:"
		echo "    HOME: \"$HOME\""
		echo "    USER: \"$(whoami 2>/dev/null || echo $USER)\""
		echo "    PATH: \"$PATH\""
		echo "    SHELL: \"$SHELL\""
		env 2>/dev/null | while IFS='=' read -r k v; do
			case "$k" in
				HOME|USER|PATH|SHELL|"") continue ;;
			esac
			if echo "$k" | grep -q '^[A-Za-z_][A-Za-z0-9_]*$'; then
				clean_v=$(printf '%s' "$v" | sed 's/\\/\\\\/g; s/"/\\"/g' | tr '\r\n' '  ')
				printf '    %s: "%s"\n' "$k" "$clean_v"
			fi
		done
`)
	}
	script.WriteString(`
		echo "  ansible_date_time:"
		echo "    epoch: \"$(date +%s)\""
		echo "    iso8601: \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\""
		echo "    date: \"$(date +%Y-%m-%d)\""
		echo "    time: \"$(date +%H:%M:%S)\""
		echo "    year: \"$(date +%Y)\""
		echo "    month: \"$(date +%m)\""
		echo "    day: \"$(date +%d)\""
	`)

	res, err := client.Execute(script.String())
	if err != nil || res == nil || filter == "" || filter == "*" {
		return res, err
	}

	// Filter gathered facts by key pattern without wiping out valid facts
	lines := strings.Split(res.Stdout, "\n")
	var filteredLines []string
	filteredLines = append(filteredLines, "gathered_facts:")

	inFacts := false
	currentMatch := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "gathered_facts:" {
			inFacts = true
			continue
		}
		if !inFacts {
			continue
		}

		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") {
			colonIdx := strings.Index(line, ":")
			if colonIdx > 2 {
				key := strings.TrimSpace(line[2:colonIdx])
				matched, matchErr := filepath.Match(filter, key)
				if !matched && matchErr == nil && !strings.Contains(filter, "*") {
					matched, _ = filepath.Match("*"+filter+"*", key)
				}
				if matched {
					currentMatch = true
					filteredLines = append(filteredLines, line)
				} else {
					currentMatch = false
				}
			}
		} else if strings.HasPrefix(line, "    ") {
			if currentMatch {
				filteredLines = append(filteredLines, line)
			}
		}
	}

	res.Stdout = strings.Join(filteredLines, "\n") + "\n"
	return res, nil
}
