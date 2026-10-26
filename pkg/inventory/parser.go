package inventory

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go-ansible/pkg/util"
	"gopkg.in/yaml.v3"
)

// ParseSources 解析多个 inventory 源（可为多个文件路径、目录、逗号分隔路径、内联主机列表）
func ParseSources(sources ...string) (*Inventory, error) {
	if len(sources) == 0 {
		return nil, fmt.Errorf("no inventory sources specified")
	}

	result := NewInventory()
	parsedAny := false

	for _, src := range sources {
		src = strings.TrimSpace(src)
		if src == "" {
			continue
		}

		// 处理逗号结尾的内联主机（Ansible 经典语法: -i "192.168.1.10,"）
		if strings.HasSuffix(src, ",") {
			inlineInv, err := ParseInlineHosts(src)
			if err != nil {
				return nil, err
			}
			result.Merge(inlineInv)
			parsedAny = true
			continue
		}

		// 检查路径是否存在
		info, err := os.Stat(src)
		if err == nil {
			if info.IsDir() {
				dirInv, err := ParseDirectory(src)
				if err != nil {
					return nil, err
				}
				result.Merge(dirInv)
				parsedAny = true
				continue
			}

			// 单文件
			fileInv, err := parseSingleFile(src)
			if err != nil {
				return nil, err
			}
			result.Merge(fileInv)
			parsedAny = true
			continue
		}

		// 文件不存在但包含逗号，尝试按多个文件或内联主机解析
		if strings.Contains(src, ",") {
			parts := strings.Split(src, ",")
			allExist := true
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}
				if _, err := os.Stat(p); err != nil {
					allExist = false
					break
				}
			}
			if allExist {
				for _, p := range parts {
					p = strings.TrimSpace(p)
					if p == "" {
						continue
					}
					inv, err := ParseSources(p)
					if err != nil {
						return nil, err
					}
					result.Merge(inv)
					parsedAny = true
				}
				continue
			}

			// 作为内联主机解析
			inlineInv, err := ParseInlineHosts(src)
			if err != nil {
				return nil, err
			}
			result.Merge(inlineInv)
			parsedAny = true
			continue
		}

		return nil, fmt.Errorf("inventory source not found: %s", src)
	}

	if !parsedAny {
		return nil, fmt.Errorf("no valid inventory parsed from sources: %v", sources)
	}

	result.ApplyGroupVars()
	return result, nil
}

// ParseDirectory 解析目录下所有 inventory 文件并合并
func ParseDirectory(dirPath string) (*Inventory, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("read inventory directory: %w", err)
	}

	result := NewInventory()
	validExts := map[string]bool{
		".ini":  true,
		".yml":  true,
		".yaml": true,
		".json": true,
		".toml": true,
		"":      true, // 可能是动态脚本或无后缀主机文件
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		// 忽略隐藏文件、临时备份文件
		if strings.HasPrefix(name, ".") || strings.HasSuffix(name, "~") || strings.HasSuffix(name, ".bak") || strings.HasSuffix(name, ".tmp") {
			continue
		}
		if entry.IsDir() {
			continue
		}
		ext := filepath.Ext(name)
		if validExts[ext] {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	if len(names) == 0 {
		return nil, fmt.Errorf("no inventory files found in directory: %s", dirPath)
	}

	for _, name := range names {
		filePath := filepath.Join(dirPath, name)
		inv, err := ParseFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("parse file %s: %w", filePath, err)
		}
		result.Merge(inv)
	}

	result.ApplyGroupVars()
	return result, nil
}

// ParseInlineHosts 解析内联主机字符串（如 "web1,web2" 或 "192.168.1.10,"）
func ParseInlineHosts(raw string) (*Inventory, error) {
	inv := NewInventory()
	allGroup := &Group{
		Name:      "all",
		Hosts:     make([]*Host, 0),
		Children:  make([]*Group, 0),
		Variables: make(map[string]string),
	}
	ungrouped := &Group{
		Name:      "ungrouped",
		Hosts:     make([]*Host, 0),
		Children:  make([]*Group, 0),
		Variables: make(map[string]string),
	}
	inv.Groups["all"] = allGroup
	inv.Groups["ungrouped"] = ungrouped

	tokens := strings.Split(raw, ",")
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		host := &Host{
			Name:      token,
			Address:   token,
			Port:      22,
			Variables: make(map[string]string),
		}
		inv.AllHosts[token] = host
		allGroup.Hosts = append(allGroup.Hosts, host)
		ungrouped.Hosts = append(ungrouped.Hosts, host)
	}

	if len(inv.AllHosts) == 0 {
		return nil, fmt.Errorf("no valid hosts found in inline expression: %q", raw)
	}

	return inv, nil
}

// ParseFile 解析 inventory 文件或目录，自动检测格式
func ParseFile(path string) (*Inventory, error) {
	if strings.Contains(path, ",") {
		return ParseSources(path)
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return ParseDirectory(path)
	}
	return parseSingleFile(path)
}

func parseSingleFile(filepath string) (*Inventory, error) {
	if info, err := os.Stat(filepath); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
		inv, err := ParseDynamicFile(filepath)
		if err == nil {
			inv.ApplyGroupVars()
			return inv, nil
		}
	}

	data, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("read inventory file: %w", err)
	}

	var inv *Inventory

	// 检测格式：YAML 格式通常以 "---" 开头或包含冒号
	content := strings.TrimSpace(string(data))
	if strings.HasPrefix(content, "{") {
		inv, err = ParseJSON(data)
	} else if strings.HasPrefix(content, "---") || isYAMLFormat(content) {
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

// ParseDynamicFile 解析可执行 inventory 脚本
func ParseDynamicFile(path string) (*Inventory, error) {
	listData, err := exec.Command(path, "--list").Output()
	if err != nil {
		return nil, fmt.Errorf("run dynamic inventory --list: %w", err)
	}

	return parseJSONInventory(listData, func(hostName string) (map[string]interface{}, error) {
		hostData, hostErr := exec.Command(path, "--host", hostName).Output()
		if hostErr != nil {
			return nil, hostErr
		}

		var vars map[string]interface{}
		if err := json.Unmarshal(hostData, &vars); err != nil {
			return nil, fmt.Errorf("parse dynamic inventory host %s: %w", hostName, err)
		}
		return vars, nil
	})
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
	parts, err := util.SplitArgs(line)
	if err != nil {
		return nil, fmt.Errorf("split host line: %w", err)
	}
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
			case "ansible_ssh_common_args":
				host.SSHCommonArgs = value
			case "ansible_connection":
				host.Connection = value
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

// ParseJSON 解析 JSON 格式的 inventory
func ParseJSON(data []byte) (*Inventory, error) {
	return parseJSONInventory(data, nil)
}

func parseJSONInventory(data []byte, hostVarProvider func(string) (map[string]interface{}, error)) (*Inventory, error) {
	raw := make(map[string]json.RawMessage)
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse json inventory: %w", err)
	}

	inv := NewInventory()
	hostVars := make(map[string]map[string]interface{})
	if metaRaw, ok := raw["_meta"]; ok {
		var meta struct {
			HostVars map[string]map[string]interface{} `json:"hostvars"`
		}
		if err := json.Unmarshal(metaRaw, &meta); err != nil {
			return nil, fmt.Errorf("parse json inventory _meta: %w", err)
		}
		hostVars = meta.HostVars
	}

	groupChildren := make(map[string][]string)
	for groupName, groupRaw := range raw {
		if groupName == "_meta" {
			continue
		}

		group := ensureInventoryGroup(inv, groupName)
		var parsed struct {
			Hosts    []string               `json:"hosts"`
			Children []string               `json:"children"`
			Vars     map[string]interface{} `json:"vars"`
		}
		if err := json.Unmarshal(groupRaw, &parsed); err != nil {
			return nil, fmt.Errorf("parse json inventory group %s: %w", groupName, err)
		}

		for key, value := range parsed.Vars {
			group.Variables[key] = fmt.Sprintf("%v", value)
		}
		for _, hostName := range parsed.Hosts {
			host := ensureInventoryHost(inv, hostName)
			group.Hosts = appendUniqueHost(group.Hosts, host)
		}
		groupChildren[groupName] = parsed.Children
	}

	for groupName, children := range groupChildren {
		group := ensureInventoryGroup(inv, groupName)
		for _, childName := range children {
			child := ensureInventoryGroup(inv, childName)
			child.Parent = group
			group.Children = appendUniqueGroup(group.Children, child)
		}
	}

	for hostName, vars := range hostVars {
		host := ensureInventoryHost(inv, hostName)
		applyJSONHostVars(inv, host, vars)
	}

	if hostVarProvider != nil {
		for hostName, host := range inv.AllHosts {
			if len(host.Variables) > 0 || host.Address != host.Name || host.User != "" || host.Port != 22 || host.PrivateKey != "" || host.Password != "" {
				continue
			}
			vars, err := hostVarProvider(hostName)
			if err != nil || len(vars) == 0 {
				continue
			}
			applyJSONHostVars(inv, host, vars)
		}
	}

	return inv, nil
}

func ensureInventoryGroup(inv *Inventory, name string) *Group {
	if inv.Groups[name] == nil {
		inv.Groups[name] = &Group{
			Name:      name,
			Hosts:     make([]*Host, 0),
			Children:  make([]*Group, 0),
			Variables: make(map[string]string),
		}
	}
	return inv.Groups[name]
}

func ensureInventoryHost(inv *Inventory, name string) *Host {
	if inv.AllHosts[name] == nil {
		inv.AllHosts[name] = &Host{
			Name:      name,
			Address:   name,
			Port:      22,
			Variables: make(map[string]string),
		}
	}
	return inv.AllHosts[name]
}

func appendUniqueHost(hosts []*Host, host *Host) []*Host {
	for _, existing := range hosts {
		if existing.Name == host.Name {
			return hosts
		}
	}
	return append(hosts, host)
}

func appendUniqueGroup(groups []*Group, group *Group) []*Group {
	for _, existing := range groups {
		if existing.Name == group.Name {
			return groups
		}
	}
	return append(groups, group)
}

func applyJSONHostVars(inv *Inventory, host *Host, vars map[string]interface{}) {
	for key, value := range vars {
		stringValue := fmt.Sprintf("%v", value)
		switch key {
		case "ansible_host", "ansible_ssh_host",
			"ansible_user", "ansible_ssh_user",
			"ansible_port", "ansible_ssh_port",
			"ansible_ssh_pass",
			"ansible_private_key_file", "ansible_ssh_private_key_file",
			"ansible_ssh_common_args",
			"ansible_connection",
			"ansible_become", "ansible_become_user", "ansible_become_method",
			"ansible_become_pass", "ansible_become_password",
			"ansible_become_exe", "ansible_become_flags":
			inv.applyConnectionVarToHost(host, key, stringValue, true)
		default:
			host.Variables[key] = stringValue
		}
	}
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
					case "ansible_ssh_common_args":
						host.SSHCommonArgs = val
					case "ansible_connection":
						host.Connection = val
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
