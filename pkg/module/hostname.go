package module

import (
	"fmt"
	"strings"

	"go-ansible/pkg/ssh"
)

// HostnameModule hostname 模块
type HostnameModule struct{}

func (m *HostnameModule) Name() string { return "hostname" }

func (m *HostnameModule) Validate(params map[string]interface{}) error {
	name := strings.TrimSpace(GetParamString(params, "name", ""))
	if name == "" {
		return fmt.Errorf("hostname module requires name")
	}
	return nil
}

func (m *HostnameModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	targetName := strings.TrimSpace(GetParamString(params, "name", ""))
	use := strings.ToLower(strings.TrimSpace(GetParamString(params, "use", "")))

	checkMode := IsCheckMode(params)
	diffMode := IsDiffMode(params)

	// 1. 查询当前系统主机名
	currentHostname := ""
	if res, err := client.Execute("hostname 2>/dev/null"); err == nil && res != nil && res.ExitCode == 0 {
		currentHostname = strings.TrimSpace(res.Stdout)
	}
	if currentHostname == "" {
		currentHostname = strings.TrimSpace(readRemoteFileContent(client, "/etc/hostname"))
	}

	if currentHostname == targetName {
		return &Result{
			Changed:  false,
			ExitCode: 0,
			Message:  fmt.Sprintf("hostname is already %s", targetName),
			Data: map[string]interface{}{
				"name": targetName,
			},
		}, nil
	}

	diff := ""
	if diffMode {
		diff = buildTextDiff("/etc/hostname", currentHostname+"\n", targetName+"\n")
	}

	if checkMode {
		return &Result{
			Changed:  true,
			Diff:     diff,
			ExitCode: 0,
			Message:  fmt.Sprintf("check mode: hostname would be changed from %s to %s", currentHostname, targetName),
			Data: map[string]interface{}{
				"name":     targetName,
				"old_name": currentHostname,
			},
		}, nil
	}

	// 2. 检查是否使用 systemd (hostnamectl)
	hasHostnamectl := false
	if use != "generic" {
		if res, err := client.Execute("command -v hostnamectl 2>/dev/null"); err == nil && res != nil && res.ExitCode == 0 {
			hasHostnamectl = true
		}
	}

	if hasHostnamectl && use != "generic" {
		cmd := fmt.Sprintf("hostnamectl set-hostname %s", shellQuote(targetName))
		if res, err := client.Execute(cmd); err != nil || (res != nil && res.ExitCode != 0) {
			// 若 hostnamectl 失败，回退至普通方式
			hasHostnamectl = false
		}
	}

	if !hasHostnamectl {
		// 执行 hostname 命令行更新当前内核主机名
		_, _ = client.Execute(fmt.Sprintf("hostname %s 2>/dev/null || true", shellQuote(targetName)))
	}

	// 3. 持久化至 /etc/hostname
	writeCmd := buildContentWriteCommand("/etc/hostname", targetName+"\n")
	if writeCmd != "" {
		if writeRes, err := client.Execute(writeCmd); err != nil || (writeRes != nil && writeRes.ExitCode != 0) {
			errMsg := ""
			if writeRes != nil {
				errMsg = writeRes.Stderr
			}
			return &Result{
				Failed:   true,
				ExitCode: 1,
				Message:  fmt.Sprintf("failed to update /etc/hostname: %s", errMsg),
			}, fmt.Errorf("write /etc/hostname: %v %s", err, errMsg)
		}
	}

	return &Result{
		Changed:  true,
		Diff:     diff,
		ExitCode: 0,
		Message:  fmt.Sprintf("hostname changed from %s to %s", currentHostname, targetName),
		Data: map[string]interface{}{
			"name":     targetName,
			"old_name": currentHostname,
		},
	}, nil
}
