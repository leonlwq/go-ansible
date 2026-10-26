package module

import (
	"encoding/base64"
	"fmt"
	"strings"

	"go-ansible/pkg/ssh"
)

// WaitForModule wait_for 模块
type WaitForModule struct{}

func (m *WaitForModule) Name() string { return "wait_for" }

func (m *WaitForModule) Validate(params map[string]interface{}) error {
	if _, ok := params["port"]; !ok {
		if _, ok := params["path"]; !ok {
			return fmt.Errorf("wait_for module requires port or path")
		}
	}
	return nil
}

func (m *WaitForModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	host := GetParamString(params, "host", "127.0.0.1")
	port := GetParamString(params, "port", "")
	path := GetParamString(params, "path", "")
	searchRegex := GetParamString(params, "search_regex", "")

	delay := GetParamInt(params, "delay", 0)
	if delay < 0 {
		delay = 0
	}
	timeout := GetParamInt(params, "timeout", 300)
	if timeout <= 0 {
		timeout = 300
	}
	state := GetParamString(params, "state", "started")
	script, successMessage, timeoutMessage, err := buildWaitForScript(waitForSpec{
		Host:        host,
		Port:        port,
		Path:        path,
		SearchRegex: searchRegex,
		Delay:       delay,
		Timeout:     timeout,
		State:       state,
	})
	if err != nil {
		return nil, err
	}

	result, err := client.Execute(script)
	if result == nil {
		return nil, err
	}

	result.Changed = false
	if result.ExitCode == 0 {
		if result.Message == "" {
			result.Message = successMessage
		}
		return result, nil
	}

	result.Failed = true
	if strings.Contains(result.Stdout, timeoutMessage) {
		result.Message = timeoutMessage
		return result, nil
	}

	if strings.TrimSpace(result.Stderr) != "" {
		result.Message = strings.TrimSpace(result.Stderr)
	} else if result.Message == "" {
		result.Message = timeoutMessage
	}

	if err != nil {
		return result, err
	}
	return result, nil
}

type waitForSpec struct {
	Host        string
	Port        string
	Path        string
	SearchRegex string
	Delay       int
	Timeout     int
	State       string
}

func buildWaitForScript(spec waitForSpec) (string, string, string, error) {
	condition, successMessage, timeoutMessage, err := buildWaitForCondition(spec)
	if err != nil {
		return "", "", "", err
	}

	rawScript := fmt.Sprintf(`delay=%d
timeout=%d
deadline=$((SECONDS + timeout))

if [ "$delay" -gt 0 ]; then
  sleep "$delay"
fi

while [ "$SECONDS" -le "$deadline" ]; do
  if ( %s ) >/dev/null 2>&1; then
    echo %s
    exit 0
  fi
  sleep 1
done

echo %s
exit 1
`, spec.Delay, spec.Timeout, condition, shellQuote(successMessage), shellQuote(timeoutMessage))

	encoded := base64.StdEncoding.EncodeToString([]byte(rawScript))
	// 使用 base64 管道传递给 bash，彻底杜绝换行、单双引号嵌套与多层 SSH/sudo shell 词法解析截断
	script := fmt.Sprintf("/bin/bash -c 'echo %s | { if command -v base64 >/dev/null 2>&1; then base64 -d; else openssl base64 -A -d; fi; } | /bin/bash'", encoded)

	return script, successMessage, timeoutMessage, nil
}

func buildWaitForCondition(spec waitForSpec) (string, string, string, error) {
	if spec.Port != "" {
		switch spec.State {
		case "", "started", "present":
			return fmt.Sprintf("echo > /dev/tcp/%s/%s", spec.Host, spec.Port),
				fmt.Sprintf("wait_for condition satisfied on %s:%s", spec.Host, spec.Port),
				fmt.Sprintf("timed out waiting for %s:%s", spec.Host, spec.Port), nil
		case "stopped", "absent":
			return fmt.Sprintf("! echo > /dev/tcp/%s/%s", spec.Host, spec.Port),
				fmt.Sprintf("wait_for condition satisfied on %s:%s", spec.Host, spec.Port),
				fmt.Sprintf("timed out waiting for %s:%s", spec.Host, spec.Port), nil
		default:
			return "", "", "", fmt.Errorf("wait_for unsupported state: %s", spec.State)
		}
	}

	if spec.Path == "" {
		return "", "", "", fmt.Errorf("wait_for module requires port or path")
	}

	quotedPath := shellQuote(spec.Path)
	fileCondition := fmt.Sprintf("[ -e %s ]", quotedPath)
	if spec.SearchRegex != "" {
		fileCondition = fmt.Sprintf("%s && grep -qE %s %s", fileCondition, shellQuote(spec.SearchRegex), quotedPath)
	}

	successMessage := fmt.Sprintf("wait_for condition satisfied on %s", spec.Path)
	timeoutMessage := fmt.Sprintf("timed out waiting for %s", spec.Path)
	if spec.SearchRegex != "" {
		successMessage = fmt.Sprintf("wait_for condition satisfied on %s matching %s", spec.Path, spec.SearchRegex)
		timeoutMessage = fmt.Sprintf("timed out waiting for %s matching %s", spec.Path, spec.SearchRegex)
	}

	switch spec.State {
	case "", "started", "present":
		return fileCondition, successMessage, timeoutMessage, nil
	case "stopped", "absent":
		if spec.SearchRegex != "" {
			return fmt.Sprintf("! grep -qE %s %s", shellQuote(spec.SearchRegex), quotedPath), successMessage, timeoutMessage, nil
		}
		return fmt.Sprintf("! [ -e %s ]", quotedPath), successMessage, timeoutMessage, nil
	default:
		return "", "", "", fmt.Errorf("wait_for unsupported state: %s", spec.State)
	}
}
