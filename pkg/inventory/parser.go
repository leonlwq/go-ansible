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
			if result.FilePath == "" {
				result.FilePath = src
			}
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
		hostExpr, port, err := parseHostAndPort(token)
		if err != nil {
			return nil, fmt.Errorf("parse inline host %q: %w", token, err)
		}
		names := expandHostRangePattern(hostExpr)
		for _, name := range names {
			host := &Host{
				Name:      name,
				Address:   name,
				Port:      port,
				Variables: make(map[string]string),
			}
			inv.AllHosts[name] = host
			allGroup.Hosts = append(allGroup.Hosts, host)
			ungrouped.Hosts = append(ungrouped.Hosts, host)
		}
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

	inv.FilePath = filepath

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

	inv, err := parseJSONInventory(listData, func(hostName string) (map[string]interface{}, error) {
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
	if err == nil && inv != nil {
		inv.FilePath = path
	}
	return inv, err
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

var (
	iniGroupPattern    = regexp.MustCompile(`^\[([^:]+)\]`)
	iniChildrenPattern = regexp.MustCompile(`^\[([^:]+):children\]`)
	iniVarsPattern     = regexp.MustCompile(`^\[([^:]+):vars\]`)
)

// ParseINI 解析 INI 格式的 inventory
func ParseINI(data []byte) (*Inventory, error) {
	inv := NewInventory()
	scanner := bufio.NewScanner(strings.NewReader(string(data)))

	var currentGroup *Group
	var inVarsSection bool
	var inChildrenSection bool

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
			inChildrenSection = false

			if matches := iniChildrenPattern.FindStringSubmatch(line); matches != nil {
				groupName := matches[1]
				currentGroup = ensureInventoryGroup(inv, groupName)
				if currentGroup.Children == nil {
					currentGroup.Children = make([]*Group, 0)
				}
				inChildrenSection = true
				continue
			}

			if matches := iniVarsPattern.FindStringSubmatch(line); matches != nil {
				groupName := matches[1]
				currentGroup = ensureInventoryGroup(inv, groupName)
				inVarsSection = true
				continue
			}

			if matches := iniGroupPattern.FindStringSubmatch(line); matches != nil {
				groupName := matches[1]
				currentGroup = ensureInventoryGroup(inv, groupName)
				continue
			}

			// 如果当前处于某组内且为 IPv6 主机语法（如 [2001:db8::1]:2222），放行至下方主机解析
			if currentGroup != nil && (strings.Contains(line, "]:") || strings.Contains(line, "::")) {
				// 作为主机行继续处理
			} else {
				return nil, fmt.Errorf("invalid group definition at line %d: %s", lineNum, line)
			}
		}

		// 在子组定义部分
		if inChildrenSection && currentGroup != nil {
			childName := strings.TrimSpace(line)
			if childName != "" {
				childGroup := ensureInventoryGroup(inv, childName)
				childGroup.Parent = currentGroup
				currentGroup.Children = appendUniqueGroup(currentGroup.Children, childGroup)
			}
			continue
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
		targetGroup := currentGroup
		if targetGroup == nil {
			targetGroup = ensureInventoryGroup(inv, "ungrouped")
		}

		// 解析主机（支持 [01:10] 等范围展开）
		hosts, err := parseHostLines(line)
		if err != nil {
			return nil, fmt.Errorf("parse host at line %d: %w", lineNum, err)
		}

		allGroup := ensureInventoryGroup(inv, "all")
		for _, host := range hosts {
			if _, exists := inv.AllHosts[host.Name]; !exists {
				inv.AllHosts[host.Name] = host
			}
			targetGroup.Hosts = append(targetGroup.Hosts, inv.AllHosts[host.Name])
			allGroup.Hosts = append(allGroup.Hosts, inv.AllHosts[host.Name])
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan inventory: %w", err)
	}

	return inv, nil
}

// parseHostLine 解析单主机行（向后兼容，返回展开后的首个主机）
func parseHostLine(line string) (*Host, error) {
	hosts, err := parseHostLines(line)
	if err != nil {
		return nil, err
	}
	if len(hosts) == 0 {
		return nil, fmt.Errorf("empty host line")
	}
	return hosts[0], nil
}

// parseHostLines 解析主机行，支持格式：
// hostname
// hostname:port
// hostname[01:10] ansible_host=192.168.1.1 ansible_user=root ansible_port=22
func parseHostLines(line string) ([]*Host, error) {
	parts, err := util.SplitArgs(line)
	if err != nil {
		return nil, fmt.Errorf("split host line: %w", err)
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty host line")
	}

	hostExpr, port, err := parseHostAndPort(parts[0])
	if err != nil {
		return nil, err
	}

	// 预解析变量
	vars := make(map[string]string)
	for _, part := range parts[1:] {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 {
			vars[kv[0]] = kv[1]
		}
	}

	expandedNames := expandHostRangePattern(hostExpr)
	hosts := make([]*Host, 0, len(expandedNames))
	for _, name := range expandedNames {
		h := &Host{
			Name:      name,
			Port:      port,
			Variables: make(map[string]string, len(vars)),
		}
		for k, v := range vars {
			applyHostVariable(h, k, v)
		}
		if h.Address == "" {
			h.Address = h.Name
		}
		hosts = append(hosts, h)
	}

	return hosts, nil
}

// parseHostAndPort 解析主机与端口，感知 [start:end] 范围与 IPv6 [::1] 括号语法
func parseHostAndPort(raw string) (string, int, error) {
	raw = strings.TrimSpace(raw)
	lastColon := strings.LastIndex(raw, ":")
	lastBracket := strings.LastIndex(raw, "]")

	port := 22
	hostExpr := raw

	if lastColon != -1 && lastColon > lastBracket {
		// 若包含多个冒号且没有结尾括号，则属于裸写 IPv6 地址，无端口
		if strings.Count(raw, ":") >= 2 && lastBracket == -1 {
			return raw, 22, nil
		}
		portStr := raw[lastColon+1:]
		p, err := strconv.Atoi(portStr)
		if err != nil {
			return "", 0, fmt.Errorf("invalid port: %s", portStr)
		}
		port = p
		hostExpr = raw[:lastColon]
	}

	// 若主机名为括号包裹的 IPv6 地址，去除外层括号
	if strings.HasPrefix(hostExpr, "[") && strings.HasSuffix(hostExpr, "]") && strings.Count(hostExpr, ":") >= 2 {
		hostExpr = hostExpr[1 : len(hostExpr)-1]
	}

	return hostExpr, port, nil
}

// expandHostRangePattern 展开 Ansible 主机范围表达式，如 "web[01:10].example.com", "db[1:10:2]", "node[a:c]"
func expandHostRangePattern(pattern string) []string {
	startBracket := strings.Index(pattern, "[")
	if startBracket == -1 {
		return []string{pattern}
	}
	endBracket := strings.Index(pattern[startBracket:], "]")
	if endBracket == -1 {
		return []string{pattern}
	}
	endBracket += startBracket

	rangeContent := pattern[startBracket+1 : endBracket]
	parts := strings.Split(rangeContent, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return []string{pattern}
	}

	startStr := strings.TrimSpace(parts[0])
	endStr := strings.TrimSpace(parts[1])
	if startStr == "" || endStr == "" {
		return []string{pattern}
	}
	step := 1
	if len(parts) == 3 {
		s, err := strconv.Atoi(strings.TrimSpace(parts[2]))
		if err != nil || s <= 0 {
			return []string{pattern}
		}
		step = s
	}

	prefix := pattern[:startBracket]
	suffix := pattern[endBracket+1:]

	// 1. 数字范围
	startNum, err1 := strconv.Atoi(startStr)
	endNum, err2 := strconv.Atoi(endStr)
	if err1 == nil && err2 == nil {
		hasLeadingZero := (len(startStr) > 1 && startStr[0] == '0') || (len(endStr) > 1 && endStr[0] == '0')
		width := len(startStr)
		if len(endStr) > width {
			width = len(endStr)
		}

		var items []string
		if startNum <= endNum {
			for i := startNum; i <= endNum; i += step {
				var val string
				if hasLeadingZero {
					val = fmt.Sprintf("%0*d", width, i)
				} else {
					val = strconv.Itoa(i)
				}
				items = append(items, prefix+val+suffix)
			}
		} else {
			for i := startNum; i >= endNum; i -= step {
				var val string
				if hasLeadingZero {
					val = fmt.Sprintf("%0*d", width, i)
				} else {
					val = strconv.Itoa(i)
				}
				items = append(items, prefix+val+suffix)
			}
		}

		var result []string
		for _, item := range items {
			result = append(result, expandHostRangePattern(item)...)
		}
		return result
	}

	// 2. 字母范围
	if len(startStr) == 1 && len(endStr) == 1 {
		bStart := startStr[0]
		bEnd := endStr[0]
		isLower := bStart >= 'a' && bStart <= 'z' && bEnd >= 'a' && bEnd <= 'z'
		isUpper := bStart >= 'A' && bStart <= 'Z' && bEnd >= 'A' && bEnd <= 'Z'
		if isLower || isUpper {
			var items []string
			if bStart <= bEnd {
				for b := bStart; b <= bEnd; b += byte(step) {
					items = append(items, prefix+string(b)+suffix)
					if int(b)+step > int(bEnd) {
						break
					}
				}
			} else {
				for b := bStart; b >= bEnd; b -= byte(step) {
					items = append(items, prefix+string(b)+suffix)
					if int(b)-step < int(bEnd) {
						break
					}
				}
			}

			var result []string
			for _, item := range items {
				result = append(result, expandHostRangePattern(item)...)
			}
			return result
		}
	}

	return []string{pattern}
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
		applyHostVariable(host, key, fmt.Sprintf("%v", value))
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
					applyHostVariable(host, k, fmt.Sprintf("%v", v))
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
	return util.ExpandPath(path)
}
