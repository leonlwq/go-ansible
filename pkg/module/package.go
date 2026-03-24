package module

import (
	"fmt"

	"go-ansible/pkg/ssh"
)

// YumModule yum 模块
type YumModule struct{}

func (m *YumModule) Name() string { return "yum" }

func (m *YumModule) Validate(params map[string]interface{}) error {
	if _, ok := params["name"]; !ok {
		if _, ok := params["pkg"]; !ok {
			return fmt.Errorf("yum module requires name or pkg")
		}
	}
	return nil
}

func (m *YumModule) Execute(client *ssh.Client, params map[string]interface{}) (*Result, error) {
	name := GetParamString(params, "name", "")
	if name == "" {
		name = GetParamString(params, "pkg", "")
	}
	state := GetParamString(params, "state", "present")
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
	case "absent", "removed":
		cmd = fmt.Sprintf("yum remove -y %s", name)
	default:
		return nil, fmt.Errorf("unsupported state: %s", state)
	}

	result, err := client.Execute(cmd)
	if err != nil {
		return result, err
	}

	// 检查是否有变更
	result.Changed = result.ExitCode == 0 && !contains(result.Stdout, "Nothing to do")
	return result, nil
}

// AptModule apt 模块
type AptModule struct{}

func (m *AptModule) Name() string { return "apt" }

func (m *AptModule) Validate(params map[string]interface{}) error {
	if _, ok := params["name"]; !ok {
		if _, ok := params["pkg"]; !ok {
			return fmt.Errorf("apt module requires name or pkg")
		}
	}
	return nil
}

func (m *AptModule) Execute(client *ssh.Client, params map[string]interface{}) (*Result, error) {
	name := GetParamString(params, "name", "")
	if name == "" {
		name = GetParamString(params, "pkg", "")
	}
	state := GetParamString(params, "state", "present")
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
	default:
		return nil, fmt.Errorf("unsupported state: %s", state)
	}

	result, err := client.Execute(cmd)
	if err != nil {
		return result, err
	}

	result.Changed = result.ExitCode == 0 && !contains(result.Stdout, "is already the newest version")
	return result, nil
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
