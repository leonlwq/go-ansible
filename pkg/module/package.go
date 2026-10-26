package module

import (
	"fmt"
	"strings"

	"go-ansible/pkg/ssh"
)

// YumModule yum 模块
type YumModule struct{}

func (m *YumModule) Name() string { return "yum" }

func (m *YumModule) Validate(params map[string]interface{}) error {
	if packageNames(params) == "" {
		return fmt.Errorf("yum module requires name or pkg")
	}
	return nil
}

func (m *YumModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	name := packageNames(params)
	state, err := normalizePackageState("yum", GetParamString(params, "state", "present"))
	if err != nil {
		return nil, err
	}
	updateCache := GetParamBool(params, "update_cache", false)

	var cmd string
	if updateCache {
		client.Execute("yum makecache -y || true")
	}

	switch state {
	case "present", "installed":
		cmd = fmt.Sprintf("yum install -y %s", name)
	case "latest":
		cmd = fmt.Sprintf("yum update -y %s", name)
	case "absent", "removed", "purged":
		cmd = fmt.Sprintf("yum remove -y %s", name)
	}

	result, err := client.Execute(cmd)
	if err != nil {
		return result, err
	}

	// 检查是否有变更
	result.Changed = yumChanged(state, result.Stdout, result.Stderr)
	return result, nil
}

// AptModule apt 模块
type AptModule struct{}

func (m *AptModule) Name() string { return "apt" }

func (m *AptModule) Validate(params map[string]interface{}) error {
	if packageNames(params) == "" {
		return fmt.Errorf("apt module requires name or pkg")
	}
	return nil
}

func (m *AptModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	name := packageNames(params)
	state, err := normalizePackageState("apt", GetParamString(params, "state", "present"))
	if err != nil {
		return nil, err
	}
	updateCache := GetParamBool(params, "update_cache", false)

	if updateCache {
		client.Execute("apt-get update")
	}

	var cmd string
	switch state {
	case "present", "installed":
		cmd = fmt.Sprintf("apt-get install -y %s", name)
	case "latest":
		cmd = fmt.Sprintf("apt-get install -y --only-upgrade %s", name)
	case "absent", "removed":
		cmd = fmt.Sprintf("apt-get remove -y %s", name)
	case "purged":
		cmd = fmt.Sprintf("apt-get purge -y %s", name)
	}

	result, err := client.Execute(cmd)
	if err != nil {
		return result, err
	}

	result.Changed = aptChanged(state, result.Stdout, result.Stderr)
	return result, nil
}

func yumChanged(state, stdout, stderr string) bool {
	combined := strings.TrimSpace(stdout + "\n" + stderr)
	if combined == "" {
		return true
	}
	return !hasAnySubstring(combined,
		"nothing to do",
		"no packages marked for update",
		"no packages marked for removal",
		"no packages marked for erase",
	)
}

func aptChanged(state, stdout, stderr string) bool {
	combined := strings.TrimSpace(stdout + "\n" + stderr)
	if combined == "" {
		return true
	}
	return !hasAnySubstring(combined,
		"is already the newest version",
		"is already the latest version",
		"0 upgraded, 0 newly installed, 0 to remove",
		"0 upgraded, 0 newly installed, 0 to remove and 0 not upgraded",
		"package is not installed, so not removed",
		"package is not installed, so not removed",
	)
}

func normalizePackageState(manager, state string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "", "present", "installed", "install":
		return "present", nil
	case "latest", "upgraded", "upgrade":
		return "latest", nil
	case "absent", "removed", "remove", "deleted", "uninstalled":
		return "absent", nil
	case "purged", "purge":
		if manager == "apt" {
			return "purged", nil
		}
		return "absent", nil
	default:
		return "", fmt.Errorf("unsupported state: %s", state)
	}
}

// PackageModule 通用包管理模块，自动探测目标系统的包管理器（apt/yum/dnf）并自适应分发
type PackageModule struct{}

func (m *PackageModule) Name() string { return "package" }

func (m *PackageModule) Validate(params map[string]interface{}) error {
	if packageNames(params) == "" {
		return fmt.Errorf("package module requires name or pkg")
	}
	return nil
}

func (m *PackageModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	probeCmd := "if command -v apt-get >/dev/null 2>&1; then echo 'apt'; elif command -v yum >/dev/null 2>&1 || command -v dnf >/dev/null 2>&1; then echo 'yum'; else echo 'unknown'; fi"
	res, err := client.Execute(probeCmd)
	if err != nil {
		return res, fmt.Errorf("failed to detect package manager on target host: %w", err)
	}

	pm := strings.TrimSpace(res.Stdout)
	switch pm {
	case "apt":
		return (&AptModule{}).Execute(client, params)
	case "yum":
		return (&YumModule{}).Execute(client, params)
	default:
		return nil, fmt.Errorf("could not detect a supported package manager (apt/yum/dnf) on target host")
	}
}
