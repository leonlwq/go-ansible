package module

import (
	"fmt"
	"strings"

	"go-ansible/pkg/ssh"
)

// YumModule yum 模块
type YumModule struct{}

func (m *YumModule) Name() string { return "yum" }

// DnfModule dnf 模块 (RHEL 8/9 / Fedora / AlmaLinux)
type DnfModule struct {
	YumModule
}

func (m *DnfModule) Name() string { return "dnf" }

func (m *DnfModule) Validate(params map[string]interface{}) error {
	if packageNames(params) == "" && !GetParamBool(params, "update_cache", false) {
		return fmt.Errorf("dnf module requires name, pkg or update_cache")
	}
	return nil
}

func (m *DnfModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	name := packageNames(params)
	updateCache := GetParamBool(params, "update_cache", false)
	if name == "" {
		if updateCache {
			res, err := client.Execute("dnf makecache")
			if err != nil {
				return res, err
			}
			return &Result{Changed: true, Stdout: res.Stdout, Stderr: res.Stderr, Message: "dnf cache updated"}, nil
		}
		return &Result{Changed: false}, nil
	}

	state, err := normalizePackageState("dnf", GetParamString(params, "state", "present"))
	if err != nil {
		return nil, err
	}

	if IsCheckMode(params) {
		pkgs := strings.Fields(name)
		allInstalled := true
		anyInstalled := false
		for _, pkg := range pkgs {
			qRes, _ := client.Execute(fmt.Sprintf("rpm -q %s 2>/dev/null", shellQuote(pkg)))
			if qRes != nil && qRes.ExitCode == 0 {
				anyInstalled = true
			} else {
				allInstalled = false
			}
		}

		changed := false
		msg := "check mode: dnf package state already in desired state"
		switch state {
		case "present", "installed":
			if !allInstalled {
				changed = true
				msg = fmt.Sprintf("check mode: package(s) %s would be installed", name)
			}
		case "latest":
			if !allInstalled {
				changed = true
				msg = fmt.Sprintf("check mode: package(s) %s would be installed", name)
			} else {
				msg = fmt.Sprintf("check mode: package(s) %s already installed", name)
			}
		case "absent", "removed", "purged":
			if anyInstalled {
				changed = true
				msg = fmt.Sprintf("check mode: package(s) %s would be removed", name)
			}
		}

		return &Result{
			Changed: changed,
			Message: msg,
		}, nil
	}

	var cmd string
	if updateCache {
		client.Execute("dnf makecache -y || true")
	}

	switch state {
	case "present", "installed":
		cmd = fmt.Sprintf("dnf install -y %s", name)
	case "latest":
		cmd = fmt.Sprintf("dnf upgrade -y %s", name)
	case "absent", "removed", "purged":
		cmd = fmt.Sprintf("dnf remove -y %s", name)
	}

	result, err := client.Execute(cmd)
	if err != nil {
		return result, err
	}

	result.Changed = yumChanged(state, result.Stdout, result.Stderr)
	return result, nil
}

func (m *YumModule) Validate(params map[string]interface{}) error {
	if packageNames(params) == "" && !GetParamBool(params, "update_cache", false) {
		return fmt.Errorf("yum module requires name, pkg or update_cache")
	}
	return nil
}

func (m *YumModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	name := packageNames(params)
	updateCache := GetParamBool(params, "update_cache", false)
	if name == "" {
		if updateCache {
			res, err := client.Execute("yum makecache")
			if err != nil {
				return res, err
			}
			return &Result{Changed: true, Stdout: res.Stdout, Stderr: res.Stderr, Message: "yum cache updated"}, nil
		}
		return &Result{Changed: false}, nil
	}

	state, err := normalizePackageState("yum", GetParamString(params, "state", "present"))
	if err != nil {
		return nil, err
	}

	if IsCheckMode(params) {
		pkgs := strings.Fields(name)
		allInstalled := true
		anyInstalled := false
		for _, pkg := range pkgs {
			qRes, _ := client.Execute(fmt.Sprintf("rpm -q %s 2>/dev/null", shellQuote(pkg)))
			if qRes != nil && qRes.ExitCode == 0 {
				anyInstalled = true
			} else {
				allInstalled = false
			}
		}

		changed := false
		msg := "check mode: yum package state already in desired state"
		switch state {
		case "present", "installed":
			if !allInstalled {
				changed = true
				msg = fmt.Sprintf("check mode: package(s) %s would be installed", name)
			}
		case "latest":
			if !allInstalled {
				changed = true
				msg = fmt.Sprintf("check mode: package(s) %s would be installed", name)
			} else {
				msg = fmt.Sprintf("check mode: package(s) %s already installed", name)
			}
		case "absent", "removed", "purged":
			if anyInstalled {
				changed = true
				msg = fmt.Sprintf("check mode: package(s) %s would be removed", name)
			}
		}

		return &Result{
			Changed: changed,
			Message: msg,
		}, nil
	}

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
	if packageNames(params) == "" && !GetParamBool(params, "update_cache", false) {
		return fmt.Errorf("apt module requires name, pkg or update_cache")
	}
	return nil
}

func (m *AptModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	name := packageNames(params)
	updateCache := GetParamBool(params, "update_cache", false)
	if name == "" {
		if updateCache {
			res, err := client.Execute("apt-get update")
			if err != nil {
				return res, err
			}
			return &Result{Changed: true, Stdout: res.Stdout, Stderr: res.Stderr, Message: "apt cache updated"}, nil
		}
		return &Result{Changed: false}, nil
	}

	state, err := normalizePackageState("apt", GetParamString(params, "state", "present"))
	if err != nil {
		return nil, err
	}

	if IsCheckMode(params) {
		pkgs := strings.Fields(name)
		allInstalled := true
		anyInstalled := false
		for _, pkg := range pkgs {
			qRes, _ := client.Execute(fmt.Sprintf("dpkg -s %s 2>/dev/null | grep -q '^Status: install ok installed'", shellQuote(pkg)))
			if qRes != nil && qRes.ExitCode == 0 {
				anyInstalled = true
			} else {
				allInstalled = false
			}
		}

		changed := false
		msg := "check mode: apt package state already in desired state"
		switch state {
		case "present", "installed":
			if !allInstalled {
				changed = true
				msg = fmt.Sprintf("check mode: package(s) %s would be installed", name)
			}
		case "latest":
			if !allInstalled {
				changed = true
				msg = fmt.Sprintf("check mode: package(s) %s would be installed", name)
			} else {
				msg = fmt.Sprintf("check mode: package(s) %s already installed", name)
			}
		case "absent", "removed", "purged":
			if anyInstalled {
				changed = true
				msg = fmt.Sprintf("check mode: package(s) %s would be removed", name)
			}
		}

		return &Result{
			Changed: changed,
			Message: msg,
		}, nil
	}

	if updateCache {
		client.Execute("apt-get update")
	}

	var cmd string
	switch state {
	case "present", "installed":
		cmd = fmt.Sprintf("apt-get install -y %s", name)
	case "latest":
		cmd = fmt.Sprintf("apt-get install -y %s", name)
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

// PackageModule 通用包管理模块，自动探测目标系统的包管理器（apt/dnf/yum/apk）并自适应分发
type PackageModule struct{}

func (m *PackageModule) Name() string { return "package" }

func (m *PackageModule) Validate(params map[string]interface{}) error {
	if packageNames(params) == "" && !GetParamBool(params, "update_cache", false) {
		return fmt.Errorf("package module requires name, pkg or update_cache")
	}
	return nil
}

func (m *PackageModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	probeCmd := "if command -v apt-get >/dev/null 2>&1; then echo 'apt'; elif command -v dnf >/dev/null 2>&1; then echo 'dnf'; elif command -v yum >/dev/null 2>&1; then echo 'yum'; elif command -v apk >/dev/null 2>&1; then echo 'apk'; else echo 'unknown'; fi"
	res, err := client.Execute(probeCmd)
	if err != nil {
		return res, fmt.Errorf("failed to detect package manager on target host: %w", err)
	}

	pm := strings.TrimSpace(res.Stdout)
	switch pm {
	case "apt":
		return (&AptModule{}).Execute(client, params)
	case "dnf":
		return (&DnfModule{}).Execute(client, params)
	case "yum":
		return (&YumModule{}).Execute(client, params)
	case "apk":
		return executeApk(client, params)
	default:
		return nil, fmt.Errorf("could not detect a supported package manager (apt/dnf/yum/apk) on target host")
	}
}

func executeApk(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	name := packageNames(params)
	state := strings.ToLower(strings.TrimSpace(GetParamString(params, "state", "present")))
	updateCache := GetParamBool(params, "update_cache", false)

	if updateCache {
		_, _ = client.Execute("apk update")
	}
	if name == "" {
		return &Result{Changed: updateCache, Message: "apk cache updated"}, nil
	}
	if IsCheckMode(params) {
		return &Result{Changed: true, Message: "check mode: apk package state would change"}, nil
	}
	var cmd string
	switch state {
	case "", "present", "installed", "latest":
		cmd = fmt.Sprintf("apk add %s", name)
	case "absent", "removed":
		cmd = fmt.Sprintf("apk del %s", name)
	default:
		return nil, fmt.Errorf("apk unsupported state: %s", state)
	}
	res, err := client.Execute(cmd)
	if err != nil {
		return res, err
	}
	res.Changed = !strings.Contains(res.Stdout, "OK: ") || strings.Contains(res.Stdout, "Installing ") || strings.Contains(res.Stdout, "Purging ")
	return res, nil
}
