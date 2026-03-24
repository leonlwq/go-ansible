package inventory

import (
	"fmt"
	"os"
	"path/filepath"
)

// GetHost 获取指定名称的主机
func (inv *Inventory) GetHost(name string) (*Host, error) {
	if host, ok := inv.AllHosts[name]; ok {
		return host, nil
	}
	return nil, fmt.Errorf("host not found: %s", name)
}

// GetGroup 获取指定名称的组
func (inv *Inventory) GetGroup(name string) (*Group, error) {
	if group, ok := inv.Groups[name]; ok {
		return group, nil
	}
	return nil, fmt.Errorf("group not found: %s", name)
}

// GetAllHosts 获取所有主机列表
func (inv *Inventory) GetAllHosts() []*Host {
	hosts := make([]*Host, 0, len(inv.AllHosts))
	for _, host := range inv.AllHosts {
		hosts = append(hosts, host)
	}
	return hosts
}

// GetGroupHosts 获取指定组及其子组的所有主机（递归）
func (inv *Inventory) GetGroupHosts(groupName string) ([]*Host, error) {
	group, err := inv.GetGroup(groupName)
	if err != nil {
		return nil, err
	}

	hostMap := make(map[string]*Host)
	inv.collectGroupHosts(group, hostMap)

	hosts := make([]*Host, 0, len(hostMap))
	for _, host := range hostMap {
		hosts = append(hosts, host)
	}
	return hosts, nil
}

// collectGroupHosts 递归收集组内的主机
func (inv *Inventory) collectGroupHosts(group *Group, hostMap map[string]*Host) {
	for _, host := range group.Hosts {
		if _, exists := hostMap[host.Name]; !exists {
			hostMap[host.Name] = host
		}
	}
	for _, child := range group.Children {
		inv.collectGroupHosts(child, hostMap)
	}
}

// ResolveHostVars 解析主机的完整变量（包括组变量和全局变量）
func (inv *Inventory) ResolveHostVars(host *Host) map[string]string {
	vars := make(map[string]string)

	// 全局变量
	for k, v := range inv.Variables {
		vars[k] = v
	}

	// 查找主机所属的组，并合并组变量
	for _, group := range inv.Groups {
		for _, h := range group.Hosts {
			if h.Name == host.Name {
				// 合并组变量
				for k, v := range group.Variables {
					if _, exists := vars[k]; !exists {
						vars[k] = v
					}
				}
				break
			}
		}
	}

	// 主机变量（最高优先级）
	for k, v := range host.Variables {
		vars[k] = v
	}

	return vars
}

// ApplyGroupVars 将组变量应用到组内的主机
// 用于处理在 [group:vars] 中定义的 SSH 连接参数
func (inv *Inventory) ApplyGroupVars() {
	// 首先处理 [all:vars]，将全局变量应用到所有主机
	if allGroup, ok := inv.Groups["all"]; ok && len(allGroup.Variables) > 0 {
		// 将 all 组的变量复制到全局变量
		for k, v := range allGroup.Variables {
			inv.Variables[k] = v
		}

		// 应用到所有主机
		for _, host := range inv.AllHosts {
			inv.applyVarsToHost(host, allGroup.Variables)
		}
	}

	// 然后处理其他组的变量
	for groupName, group := range inv.Groups {
		if groupName == "all" {
			continue
		}
		if len(group.Variables) == 0 {
			continue
		}

		for _, host := range group.Hosts {
			inv.applyVarsToHost(host, group.Variables)
		}
	}
}

// applyVarsToHost 将变量应用到单个主机
func (inv *Inventory) applyVarsToHost(host *Host, vars map[string]string) {
	for key, value := range vars {
		switch key {
		case "ansible_host", "ansible_ssh_host":
			if host.Address == host.Name {
				host.Address = value
			}
		case "ansible_user", "ansible_ssh_user":
			if host.User == "" {
				host.User = value
			}
		case "ansible_port", "ansible_ssh_port":
			if host.Port == 22 {
				port := 0
				fmt.Sscanf(value, "%d", &port)
				if port > 0 {
					host.Port = port
				}
			}
		case "ansible_ssh_pass":
			if host.Password == "" {
				host.Password = value
			}
		case "ansible_private_key_file", "ansible_ssh_private_key_file":
			if host.PrivateKey == "" {
				host.PrivateKey = expandPath(value)
			}
		case "ansible_become":
			if !host.Become {
				host.Become = value == "yes" || value == "true" || value == "1"
			}
		case "ansible_become_user":
			if host.BecomeUser == "" {
				host.BecomeUser = value
			}
		case "ansible_become_method":
			if host.BecomeMethod == "" {
				host.BecomeMethod = value
			}
		case "ansible_become_pass", "ansible_become_password":
			if host.BecomePass == "" {
				host.BecomePass = value
			}
		case "ansible_become_exe":
			if host.BecomeExe == "" {
				host.BecomeExe = value
			}
		case "ansible_become_flags":
			if host.BecomeFlags == "" {
				host.BecomeFlags = value
			}
		}
	}
}

// expandPath 展开路径中的 ~ 和环境变量
func expandPath(path string) string {
	if path == "" {
		return path
	}

	// 展开 ~ 为 home 目录
	if path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		if len(path) > 1 {
			return filepath.Join(home, path[2:])
		}
		return home
	}

	// 展开环境变量
	if path[0] == '$' {
		return os.ExpandEnv(path)
	}

	return path
}
