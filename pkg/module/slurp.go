package module

import (
	"fmt"
	"strings"

	"go-ansible/pkg/ssh"
)

// SlurpModule slurp 模块，读取远程文件并以 base64 内容返回。
type SlurpModule struct{}

func (m *SlurpModule) Name() string { return "slurp" }

func (m *SlurpModule) Validate(params map[string]interface{}) error {
	if strings.TrimSpace(GetParamString(params, "src", "")) == "" {
		return fmt.Errorf("slurp module requires src")
	}
	return nil
}

func (m *SlurpModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	src := strings.TrimSpace(GetParamString(params, "src", ""))
	command := buildSlurpCommand(src)

	result, err := client.Execute(command)
	if result == nil {
		return result, err
	}
	if err != nil {
		return result, err
	}
	if result.IsFailed() {
		result.Changed = false
		return result, nil
	}

	content := strings.TrimSpace(result.Stdout)
	result.Changed = false
	result.Stdout = content
	result.Data = map[string]interface{}{
		"content":  content,
		"encoding": "base64",
		"source":   src,
	}
	if result.Message == "" {
		result.Message = fmt.Sprintf("slurped %s", src)
	}
	return result, nil
}

func buildSlurpCommand(src string) string {
	quotedSrc := shellQuote(src)
	return fmt.Sprintf("if [ -f %s ]; then if command -v base64 >/dev/null 2>&1; then base64 %s | tr -d '\\n'; else openssl base64 -A -in %s; fi; else echo 'file not found: %s' >&2; exit 1; fi", quotedSrc, quotedSrc, quotedSrc, src)
}
