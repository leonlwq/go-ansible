package inventory

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// EnsureLocalhost 确保 Inventory 中具备隐式 localhost 主机定义
func (inv *Inventory) EnsureLocalhost() *Host {
	if inv.AllHosts == nil {
		inv.AllHosts = make(map[string]*Host)
	}
	if host, ok := inv.AllHosts["localhost"]; ok {
		return host
	}
	if host, ok := inv.AllHosts["127.0.0.1"]; ok {
		return host
	}
	lh := &Host{
		Name:    "localhost",
		Address: "127.0.0.1",
		Port:    22,
		Variables: map[string]string{
			"ansible_connection":         "local",
			"ansible_python_interpreter": "auto_silent",
		},
	}
	inv.AllHosts["localhost"] = lh
	return lh
}

// GetHost 获取指定名称的主机
func (inv *Inventory) GetHost(name string) (*Host, error) {
	if host, ok := inv.AllHosts[name]; ok {
		return host, nil
	}
	if name == "localhost" || name == "127.0.0.1" {
		return inv.EnsureLocalhost(), nil
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
	names := make([]string, 0, len(inv.AllHosts))
	for name := range inv.AllHosts {
		names = append(names, name)
	}
	sort.Strings(names)
	hosts := make([]*Host, 0, len(names))
	for _, name := range names {
		hosts = append(hosts, inv.AllHosts[name])
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

	names := make([]string, 0, len(hostMap))
	for name := range hostMap {
		names = append(names, name)
	}
	sort.Strings(names)
	hosts := make([]*Host, 0, len(names))
	for _, name := range names {
		hosts = append(hosts, hostMap[name])
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

// CollectGroupHosts 递归收集组内主机到调用方提供的 map。
func (inv *Inventory) CollectGroupHosts(group *Group, hostMap map[string]*Host) {
	if group == nil || hostMap == nil {
		return
	}
	inv.collectGroupHosts(group, hostMap)
}

// MatchHosts 根据主机模式从 inventory 中选择主机。
func (inv *Inventory) MatchHosts(patternExpr string) ([]*Host, error) {
	trimmed := strings.TrimSpace(patternExpr)
	if strings.Contains(trimmed, "localhost") || strings.Contains(trimmed, "127.0.0.1") {
		inv.EnsureLocalhost()
	}
	return inv.FilterHosts(inv.GetAllHosts(), patternExpr)
}

// FilterHosts 在给定主机集合上应用主机模式过滤。
func (inv *Inventory) FilterHosts(hosts []*Host, patternExpr string) ([]*Host, error) {
	patterns := splitHostPatterns(patternExpr)
	if len(patterns) == 0 {
		return nil, fmt.Errorf("hosts pattern is empty")
	}

	current := make(map[string]bool)
	hasInitialized := false

	hasPositive := false
	for _, rawPattern := range patterns {
		p := strings.TrimSpace(rawPattern)
		if p != "" && !strings.HasPrefix(p, "!") && !strings.HasPrefix(p, "&") {
			hasPositive = true
			break
		}
	}
	if !hasPositive {
		for _, host := range hosts {
			current[host.Name] = true
		}
		hasInitialized = true
	}

	for _, rawPattern := range patterns {
		pattern := strings.TrimSpace(rawPattern)
		if pattern == "" {
			continue
		}

		isExclude := strings.HasPrefix(pattern, "!")
		isIntersect := strings.HasPrefix(pattern, "&")

		subPattern := pattern
		if isExclude || isIntersect {
			subPattern = strings.TrimSpace(pattern[1:])
		}
		if subPattern == "" {
			continue
		}

		matched := inv.matchSubPattern(hosts, subPattern)

		if isExclude {
			for name := range matched {
				delete(current, name)
			}
		} else if isIntersect {
			if !hasInitialized {
				for name := range matched {
					current[name] = true
				}
				hasInitialized = true
			} else {
				intersected := make(map[string]bool)
				for name := range current {
					if matched[name] {
						intersected[name] = true
					}
				}
				current = intersected
			}
		} else {
			for name := range matched {
				current[name] = true
			}
			hasInitialized = true
		}
	}

	limited := make([]*Host, 0, len(hosts))
	for _, host := range hosts {
		if current[host.Name] {
			limited = append(limited, host)
		}
	}
	if len(limited) == 0 {
		return nil, fmt.Errorf("no matching hosts for %q", patternExpr)
	}
	return limited, nil
}

func (inv *Inventory) matchSubPattern(hosts []*Host, subPattern string) map[string]bool {
	matched := make(map[string]bool)
	subPattern = strings.TrimSpace(subPattern)
	if subPattern == "" {
		return matched
	}

	// 1. 正则表达式模式: ~regex
	if strings.HasPrefix(subPattern, "~") {
		regexStr := strings.TrimSpace(subPattern[1:])
		if re, err := regexp.Compile(regexStr); err == nil {
			for _, host := range hosts {
				if re.MatchString(host.Name) || re.MatchString(host.Address) {
					matched[host.Name] = true
				}
			}
			return matched
		}
	}

	// 2. 切片模式: group[0:2] 或 group[0]
	if sliceHosts, ok := inv.resolveGroupSlice(subPattern); ok {
		for _, h := range sliceHosts {
			matched[h.Name] = true
		}
		return matched
	}

	// 3. 通用组或主机匹配
	for _, host := range hosts {
		if inv.hostMatchesPattern(host, subPattern) {
			matched[host.Name] = true
		}
	}
	return matched
}

func (inv *Inventory) resolveGroupSlice(pattern string) ([]*Host, bool) {
	idxOpen := strings.Index(pattern, "[")
	idxClose := strings.LastIndex(pattern, "]")
	if idxOpen <= 0 || idxClose <= idxOpen {
		return nil, false
	}

	sliceExpr := pattern[idxOpen+1 : idxClose]
	groupName := pattern[:idxOpen]
	var sorted []*Host
	if groupName == "all" || groupName == "*" {
		sorted = inv.GetAllHosts()
	} else {
		group, err := inv.GetGroup(groupName)
		if err != nil {
			return nil, false
		}
		hostsMap := make(map[string]*Host)
		inv.collectGroupHosts(group, hostsMap)
		sorted = make([]*Host, 0, len(hostsMap))
		for _, h := range hostsMap {
			sorted = append(sorted, h)
		}
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].Name < sorted[j].Name
		})
	}

	n := len(sorted)
	if n == 0 {
		return nil, true
	}

	if strings.Contains(sliceExpr, ":") {
		parts := strings.SplitN(sliceExpr, ":", 2)
		start := 0
		end := n
		if parts[0] != "" {
			if s, err := strconv.Atoi(parts[0]); err == nil {
				if s < 0 {
					start = n + s
				} else {
					start = s
				}
			}
		}
		if parts[1] != "" {
			if e, err := strconv.Atoi(parts[1]); err == nil {
				if e < 0 {
					end = n + e
				} else {
					end = e
				}
			}
		}
		if start < 0 {
			start = 0
		}
		if end > n {
			end = n
		}
		if start >= end {
			return nil, true
		}
		return sorted[start:end], true
	}

	idx, err := strconv.Atoi(sliceExpr)
	if err != nil {
		return nil, false
	}
	if idx < 0 {
		idx = n + idx
	}
	if idx >= 0 && idx < n {
		return []*Host{sorted[idx]}, true
	}
	return nil, true
}

func (inv *Inventory) hostMatchesPattern(host *Host, pattern string) bool {
	if host == nil {
		return false
	}
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if pattern == "all" || pattern == "*" {
		return true
	}
	if wildcardMatch(pattern, host.Name) || wildcardMatch(pattern, host.Address) {
		return true
	}
	if group, err := inv.GetGroup(pattern); err == nil {
		hostMap := make(map[string]*Host)
		inv.collectGroupHosts(group, hostMap)
		_, ok := hostMap[host.Name]
		return ok
	}
	return false
}

func splitHostPatterns(patternExpr string) []string {
	patternExpr = strings.TrimSpace(patternExpr)
	if patternExpr == "" {
		return nil
	}

	var result []string
	var current strings.Builder
	inBracket := false

	for _, r := range patternExpr {
		switch r {
		case '[':
			inBracket = true
			current.WriteRune(r)
		case ']':
			inBracket = false
			current.WriteRune(r)
		case ',', ':':
			if inBracket {
				current.WriteRune(r)
			} else {
				item := strings.TrimSpace(current.String())
				if item != "" {
					result = append(result, item)
				}
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		item := strings.TrimSpace(current.String())
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

func wildcardMatch(pattern, value string) bool {
	if pattern == "" || value == "" {
		return false
	}
	if ok, err := filepath.Match(pattern, value); err == nil && ok {
		return true
	}
	return pattern == value
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
		inv.applyConnectionVarToHost(host, key, value, false)
	}
}

// ApplyConnectionVars 将连接相关的 extra vars 应用到所有主机。
// extra vars 在 Ansible 中优先级最高，这里按覆盖语义处理。
func (inv *Inventory) ApplyConnectionVars(vars map[string]interface{}) {
	for _, host := range inv.AllHosts {
		for key, value := range vars {
			inv.applyConnectionVarToHost(host, key, fmt.Sprintf("%v", value), true)
		}
	}
}

func (inv *Inventory) applyConnectionVarToHost(host *Host, key, value string, override bool) {
	switch key {
	case "ansible_host", "ansible_ssh_host":
		if override || host.Address == "" || host.Address == host.Name {
			host.Address = value
		}
	case "ansible_user", "ansible_ssh_user":
		if override || host.User == "" {
			host.User = value
		}
	case "ansible_port", "ansible_ssh_port":
		port := 0
		fmt.Sscanf(value, "%d", &port)
		if port > 0 && (override || host.Port == 0 || host.Port == 22) {
			host.Port = port
		}
	case "ansible_ssh_pass":
		if override || host.Password == "" {
			host.Password = value
		}
	case "ansible_private_key_file", "ansible_ssh_private_key_file":
		if override || host.PrivateKey == "" {
			host.PrivateKey = expandPath(value)
		}
	case "ansible_ssh_common_args":
		if override || host.SSHCommonArgs == "" {
			host.SSHCommonArgs = value
		}
	case "ansible_connection":
		if override || host.Connection == "" {
			host.Connection = value
		}
	case "ansible_become":
		if override || !host.Become {
			host.Become = value == "yes" || value == "true" || value == "1"
		}
	case "ansible_become_user":
		if override || host.BecomeUser == "" {
			host.BecomeUser = value
		}
	case "ansible_become_method":
		if override || host.BecomeMethod == "" {
			host.BecomeMethod = value
		}
	case "ansible_become_pass", "ansible_become_password":
		if override || host.BecomePass == "" {
			host.BecomePass = value
		}
	case "ansible_become_exe":
		if override || host.BecomeExe == "" {
			host.BecomeExe = value
		}
	case "ansible_become_flags":
		if override || host.BecomeFlags == "" {
			host.BecomeFlags = value
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

// Merge 将另一个 Inventory 的主机、组和变量合并到当前 Inventory 中
func (inv *Inventory) Merge(other *Inventory) {
	if other == nil {
		return
	}

	if inv.AllHosts == nil {
		inv.AllHosts = make(map[string]*Host)
	}
	if inv.Groups == nil {
		inv.Groups = make(map[string]*Group)
	}
	if inv.Variables == nil {
		inv.Variables = make(map[string]string)
	}

	// 1. 合并全局变量
	for k, v := range other.Variables {
		inv.Variables[k] = v
	}

	// 2. 合并所有主机 (AllHosts)
	for name, otherHost := range other.AllHosts {
		if curHost, exists := inv.AllHosts[name]; exists {
			if otherHost.Address != "" && (curHost.Address == "" || curHost.Address == curHost.Name) {
				curHost.Address = otherHost.Address
			}
			if otherHost.Port != 0 && (curHost.Port == 0 || curHost.Port == 22) {
				curHost.Port = otherHost.Port
			}
			if otherHost.User != "" && curHost.User == "" {
				curHost.User = otherHost.User
			}
			if otherHost.Password != "" && curHost.Password == "" {
				curHost.Password = otherHost.Password
			}
			if otherHost.PrivateKey != "" && curHost.PrivateKey == "" {
				curHost.PrivateKey = otherHost.PrivateKey
			}
			if otherHost.SSHCommonArgs != "" && curHost.SSHCommonArgs == "" {
				curHost.SSHCommonArgs = otherHost.SSHCommonArgs
			}
			if otherHost.Become {
				curHost.Become = true
			}
			if otherHost.BecomeUser != "" && curHost.BecomeUser == "" {
				curHost.BecomeUser = otherHost.BecomeUser
			}
			if otherHost.BecomeMethod != "" && curHost.BecomeMethod == "" {
				curHost.BecomeMethod = otherHost.BecomeMethod
			}
			if otherHost.BecomePass != "" && curHost.BecomePass == "" {
				curHost.BecomePass = otherHost.BecomePass
			}
			if curHost.Variables == nil {
				curHost.Variables = make(map[string]string)
			}
			for k, v := range otherHost.Variables {
				curHost.Variables[k] = v
			}
		} else {
			newHost := *otherHost
			newHost.Variables = make(map[string]string)
			for k, v := range otherHost.Variables {
				newHost.Variables[k] = v
			}
			inv.AllHosts[name] = &newHost
		}
	}

	// 3. 合并组 (Groups)
	for gName, otherGroup := range other.Groups {
		curGroup, exists := inv.Groups[gName]
		if !exists {
			curGroup = &Group{
				Name:      gName,
				Hosts:     make([]*Host, 0),
				Children:  make([]*Group, 0),
				Variables: make(map[string]string),
			}
			inv.Groups[gName] = curGroup
		}

		if curGroup.Variables == nil {
			curGroup.Variables = make(map[string]string)
		}
		for k, v := range otherGroup.Variables {
			curGroup.Variables[k] = v
		}

		// 合并主机列表
		hostExists := make(map[string]bool)
		for _, h := range curGroup.Hosts {
			hostExists[h.Name] = true
		}
		for _, h := range otherGroup.Hosts {
			if !hostExists[h.Name] {
				targetHost := inv.AllHosts[h.Name]
				if targetHost == nil {
					targetHost = h
					inv.AllHosts[h.Name] = targetHost
				}
				curGroup.Hosts = append(curGroup.Hosts, targetHost)
				hostExists[h.Name] = true
			}
		}

		// 合并子组列表
		childExists := make(map[string]bool)
		for _, c := range curGroup.Children {
			childExists[c.Name] = true
		}
		for _, c := range otherGroup.Children {
			if !childExists[c.Name] {
				childGroup, hasChild := inv.Groups[c.Name]
				if !hasChild {
					childGroup = &Group{
						Name:      c.Name,
						Hosts:     make([]*Host, 0),
						Children:  make([]*Group, 0),
						Variables: make(map[string]string),
					}
					inv.Groups[c.Name] = childGroup
				}
				curGroup.Children = append(curGroup.Children, childGroup)
				childExists[c.Name] = true
			}
		}
	}

	inv.ApplyGroupVars()
}
