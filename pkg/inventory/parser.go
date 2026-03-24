package inventory

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseFile 解析 inventory 文件，自动检测格式
func ParseFile(filepath string) (*Inventory, error) {
	data, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("read inventory file: %w", err)
	}

	var inv *Inventory

	// 检测格式：YAML 格式通常以 "---" 开头或包含冒号
	content := strings.TrimSpace(string(data))
	if strings.HasPrefix(content, "---") || isYAMLFormat(content) {
		inv, err = ParseYAML(data)
	} else {
		inv, err = ParseINI(data)
	}

	if err != nil {
		return nil, err
	}

	// 应用组变量到主机
	inv.ApplyGroupVars()

	return inv, nil
}

// isYAMLFormat 检测是否为 YAML 格式
func isYAMLFormat(content string) bool {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// YAML 格式的组定义通常是 "groupname:"
		if strings.HasSuffix(line, ":") && !strings.Contains(line, "=") {
			return true
		}
	}
	return false
}

// ParseINI 解析 INI 格式的 inventory
func ParseINI(data []byte) (*Inventory, error) {
	inv := NewInventory()
	scanner := bufio.NewScanner(strings.NewReader(string(data)))

	var currentGroup *Group
	var inVarsSection bool
	groupPattern := regexp.MustCompile(`^\[([^:]+)\]`)
	childrenPattern := regexp.MustCompile(`^\[([^:]+):children\]`)
	varsPattern := regexp.MustCompile(`^\[([^:]+):vars\]`)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// 跳过空行和注释
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		// 检查是否是分组定义
		if strings.HasPrefix(line, "[") {
			inVarsSection = false

			if matches := childrenPattern.FindStringSubmatch(line); matches != nil {
				groupName := matches[1]
				if inv.Groups[groupName] == nil {
					inv.Groups[groupName] = &Group{
						Name:      groupName,
						Hosts:     make([]*Host, 0),
						Children:  make([]*Group, 0),
						Variables: make(map[string]string),
					}
				}
				currentGroup = inv.Groups[groupName]
				currentGroup.Children = make([]*Group, 0)
				continue
			}

			if matches := varsPattern.FindStringSubmatch(line); matches != nil {
				groupName := matches[1]
				if inv.Groups[groupName] == nil {
					inv.Groups[groupName] = &Group{
						Name:      groupName,
						Hosts:     make([]*Host, 0),
						Children:  make([]*Group, 0),
						Variables: make(map[string]string),
					}
				}
				currentGroup = inv.Groups[groupName]
				inVarsSection = true
				continue
			}

			if matches := groupPattern.FindStringSubmatch(line); matches != nil {
				groupName := matches[1]
				if inv.Groups[groupName] == nil {
					inv.Groups[groupName] = &Group{
						Name:      groupName,
						Hosts:     make([]*Host, 0),
						Children:  make([]*Group, 0),
						Variables: make(map[string]string),
					}
				}
				currentGroup = inv.Groups[groupName]
				continue
			}

			return nil, fmt.Errorf("invalid group definition at line %d: %s", lineNum, line)
		}

		// 在变量部分
		if inVarsSection && currentGroup != nil {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])
				currentGroup.Variables[key] = value
			}
			continue
		}

		// 解析主机行
		if currentGroup != nil {
			// 检查是否是子组
			if childGroup, exists := inv.Groups[line]; exists && currentGroup.Children != nil {
				childGroup.Parent = currentGroup
				currentGroup.Children = append(currentGroup.Children, childGroup)
				continue
			}

			// 解析主机
			host, err := parseHostLine(line)
			if err != nil {
				return nil, fmt.Errorf("parse host at line %d: %w", lineNum, err)
			}

			if _, exists := inv.AllHosts[host.Name]; !exists {
				inv.AllHosts[host.Name] = host
			}
			currentGroup.Hosts = append(currentGroup.Hosts, inv.AllHosts[host.Name])
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan inventory: %w", err)
	}

	return inv, nil
}

// parseHostLine 解析主机行，支持格式：
// hostname
// hostname:port
// hostname ansible_host=192.168.1.1 ansible_user=root ansible_port=22
func parseHostLine(line string) (*Host, error) {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty host line")
	}

	host := &Host{
		Name:      parts[0],
		Port:      22, // 默认端口
		Variables: make(map[string]string),
	}

	// 如果是 hostname:port 格式
	if strings.Contains(host.Name, ":") {
		hp := strings.Split(host.Name, ":")
		host.Name = hp[0]
		port, err := strconv.Atoi(hp[1])
		if err != nil {
			return nil, fmt.Errorf("invalid port: %s", hp[1])
		}
		host.Port = port
	}

	// 解析其他变量
	for _, part := range parts[1:] {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 {
			key := kv[0]
			value := kv[1]
			switch key {
			case "ansible_host", "ansible_ssh_host":
				host.Address = value
			case "ansible_user", "ansible_ssh_user":
				host.User = value
			case "ansible_port", "ansible_ssh_port":
				port, err := strconv.Atoi(value)
				if err != nil {
					return nil, fmt.Errorf("invalid port: %s", value)
				}
				host.Port = port
			case "ansible_ssh_pass":
				host.Password = value
			case "ansible_private_key_file", "ansible_ssh_private_key_file":
				host.PrivateKey = expandHome(value)
			case "ansible_become":
				host.Become = value == "yes" || value == "true" || value == "1"
			case "ansible_become_user":
				host.BecomeUser = value
			case "ansible_become_method":
				host.BecomeMethod = value
			case "ansible_become_pass", "ansible_become_password":
				host.BecomePass = value
			case "ansible_become_exe":
				host.BecomeExe = value
			case "ansible_become_flags":
				host.BecomeFlags = value
			default:
				host.Variables[key] = value
			}
		}
	}

	if host.Address == "" {
		host.Address = host.Name
	}

	return host, nil
}

// ParseYAML 解析 YAML 格式的 inventory
func ParseYAML(data []byte) (*Inventory, error) {
	inv := NewInventory()

	// 先尝试解析简单格式：all: children: vars:
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	// 解析 all 组
	if all, ok := raw["all"].(map[string]interface{}); ok {
		parseYAMLGroup(inv, "all", all, nil)
	} else {
		// 尝试直接解析组
		for name, value := range raw {
			if groupData, ok := value.(map[string]interface{}); ok {
				parseYAMLGroup(inv, name, groupData, nil)
			}
		}
	}

	return inv, nil
}

// parseYAMLGroup 递归解析 YAML 组
func parseYAMLGroup(inv *Inventory, name string, data map[string]interface{}, parent *Group) {
	if inv.Groups[name] == nil {
		inv.Groups[name] = &Group{
			Name:      name,
			Hosts:     make([]*Host, 0),
			Children:  make([]*Group, 0),
			Variables: make(map[string]string),
			Parent:    parent,
		}
	}
	group := inv.Groups[name]

	// 解析变量
	if vars, ok := data["vars"].(map[string]interface{}); ok {
		for k, v := range vars {
			group.Variables[k] = fmt.Sprintf("%v", v)
		}
	}

	// 解析 hosts
	if hosts, ok := data["hosts"].(map[string]interface{}); ok {
		for hostName, hostData := range hosts {
			host := &Host{
				Name:      hostName,
				Address:   hostName,
				Port:      22,
				Variables: make(map[string]string),
			}

			if hostVars, ok := hostData.(map[string]interface{}); ok {
				for k, v := range hostVars {
					val := fmt.Sprintf("%v", v)
					switch k {
					case "ansible_host", "ansible_ssh_host":
						host.Address = val
					case "ansible_user", "ansible_ssh_user":
						host.User = val
					case "ansible_port", "ansible_ssh_port":
						port, _ := strconv.Atoi(val)
						if port > 0 {
							host.Port = port
						}
					case "ansible_ssh_pass":
						host.Password = val
					case "ansible_private_key_file", "ansible_ssh_private_key_file":
						host.PrivateKey = expandHome(val)
					case "ansible_become":
						host.Become = val == "yes" || val == "true" || val == "1"
					case "ansible_become_user":
						host.BecomeUser = val
					case "ansible_become_method":
						host.BecomeMethod = val
					case "ansible_become_pass", "ansible_become_password":
						host.BecomePass = val
					case "ansible_become_exe":
						host.BecomeExe = val
					case "ansible_become_flags":
						host.BecomeFlags = val
					default:
						host.Variables[k] = val
					}
				}
			}

			if _, exists := inv.AllHosts[hostName]; !exists {
				inv.AllHosts[hostName] = host
			}
			group.Hosts = append(group.Hosts, inv.AllHosts[hostName])
		}
	}

	// 解析 children
	if children, ok := data["children"].(map[string]interface{}); ok {
		for childName, childData := range children {
			if childGroupData, ok := childData.(map[string]interface{}); ok {
				parseYAMLGroup(inv, childName, childGroupData, group)
				if childGroup := inv.Groups[childName]; childGroup != nil {
					group.Children = append(group.Children, childGroup)
				}
			}
		}
	}
}

// expandHome 展开路径中的 ~ 为用户主目录（兼容 Ansible 的路径格式）
func expandHome(path string) string {
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
	if strings.Contains(path, "$") {
		return os.ExpandEnv(path)
	}

	return path
}
