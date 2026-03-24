package playbook

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/module"
	"go-ansible/pkg/ssh"
	"go-ansible/pkg/variable"
)

// Executor Playbook 执行器
type Executor struct {
	inventory  *inventory.Inventory
	pool       *ssh.ConnectionPool
	registry   *module.Registry
	varManager *variable.Manager
	facts      map[string]map[string]interface{}
	extraVars  map[string]interface{}
	tags       []string
	verbose    bool
	mu         sync.RWMutex
}

// NewExecutor 创建新的执行器
func NewExecutor(inv *inventory.Inventory) *Executor {
	return &Executor{
		inventory:  inv,
		pool:       ssh.NewConnectionPool(nil),
		registry:   module.NewRegistry(),
		varManager: variable.NewManager(),
		facts:      make(map[string]map[string]interface{}),
		extraVars:  make(map[string]interface{}),
		tags:       make([]string, 0),
	}
}

// SetVerbose 设置是否输出详细日志
func (e *Executor) SetVerbose(verbose bool) {
	e.verbose = verbose
}

// log 输出日志
func (e *Executor) log(format string, args ...interface{}) {
	if e.verbose {
		fmt.Printf("[DEBUG] "+format+"\n", args...)
	}
}

// SetExtraVars 设置额外变量
func (e *Executor) SetExtraVars(vars map[string]interface{}) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for k, v := range vars {
		e.extraVars[k] = v
	}
}

// GetExtraVars 获取额外变量
func (e *Executor) GetExtraVars() map[string]interface{} {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make(map[string]interface{})
	for k, v := range e.extraVars {
		result[k] = v
	}
	return result
}

// SetTags 设置执行的 tags
func (e *Executor) SetTags(tags []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tags = tags
}

// GetTags 获取设置的 tags
func (e *Executor) GetTags() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]string, len(e.tags))
	copy(result, e.tags)
	return result
}

// Execute 执行 playbook
func (e *Executor) Execute(playbook *Playbook) (*PlaybookResult, error) {
	fmt.Println("[EXECUTOR] Execute() called")
	result := &PlaybookResult{
		Plays: make([]*PlayResult, 0),
	}

	playsCount := len(playbook.Plays)
	fmt.Printf("[EXECUTOR] Plays count: %d\n", playsCount)

	if playsCount == 0 {
		fmt.Println("[EXECUTOR] No plays to execute")
		return result, nil
	}

	for i, play := range playbook.Plays {
		fmt.Printf("[EXECUTOR] Executing play %d: %s\n", i, play.Name)
		playResult, err := e.ExecutePlay(play)
		if err != nil {
			return result, fmt.Errorf("execute play %q: %w", play.Name, err)
		}
		result.Plays = append(result.Plays, playResult)
	}

	fmt.Println("[EXECUTOR] Execute() completed")
	return result, nil
}

// ExecutePlay 执行单个 play
func (e *Executor) ExecutePlay(play *Play) (*PlayResult, error) {
	fmt.Printf("[EXECUTOR] ExecutePlay() called for: %s\n", play.Name)
	result := &PlayResult{
		Name:  play.Name,
		Hosts: make(map[string]*HostResult),
	}

	fmt.Printf("[EXECUTOR] Getting play hosts: %s\n", play.Hosts)

	// 获取目标主机
	hosts, err := e.getPlayHosts(play)
	if err != nil {
		fmt.Printf("[EXECUTOR] Error getting play hosts: %v\n", err)
		return result, err
	}

	fmt.Printf("[EXECUTOR] Found %d hosts\n", len(hosts))

	// 应用 play 级别的 become 配置到所有主机
	if play.Become {
		fmt.Printf("[EXECUTOR] Play become: true, become_user: %s, become_method: %s\n", play.BecomeUser, play.BecomeMethod)
		for _, h := range hosts {
			h.Become = true
			if play.BecomeUser != "" {
				h.BecomeUser = play.BecomeUser
			}
			if play.BecomeMethod != "" {
				h.BecomeMethod = play.BecomeMethod
			}
		}
	}

	// 应用 become_pass 从 play vars
	if becomePass, ok := play.Vars["ansible_become_pass"]; ok {
		for _, h := range hosts {
			h.BecomePass = fmt.Sprintf("%v", becomePass)
		}
	}

	for _, h := range hosts {
		fmt.Printf("[EXECUTOR]   Host: %s, User: %s, Become: %v, BecomeUser: %s\n", h.Name, h.User, h.Become, h.BecomeUser)
	}

	// 合并变量
	playVars := make(map[string]interface{})
	for k, v := range play.Vars {
		playVars[k] = v
	}
	playVars["inventory_hostname"] = ""

	// 检查是否需要收集 facts
	gatherFacts := true
	if play.GatherFacts != nil {
		gatherFacts = *play.GatherFacts
	}

	// 收集 facts
	if gatherFacts {
		fmt.Println("[EXECUTOR] Gathering facts...")
		e.gatherFacts(hosts)
		fmt.Println("[EXECUTOR] Facts gathered")
	} else {
		fmt.Println("[EXECUTOR] Skipping facts gathering")
	}

	// 执行 pre_tasks
	fmt.Printf("[EXECUTOR] Pre-tasks count: %d\n", len(play.PreTasks))
	if len(play.PreTasks) > 0 {
		for _, host := range hosts {
			hostResult := e.getHostResult(result, host.Name)
			for _, task := range play.PreTasks {
				fmt.Printf("[EXECUTOR] Executing pre-task: %s\n", task.Name)
				taskResults, _ := e.executeTask(host, task, playVars)
				hostResult.Tasks = append(hostResult.Tasks, taskResults...)
			}
		}
	}

	// 执行 tasks
	fmt.Printf("[EXECUTOR] Tasks count: %d\n", len(play.Tasks))
	if len(play.Tasks) > 0 {
		for _, host := range hosts {
			hostResult := e.getHostResult(result, host.Name)
			for _, task := range play.Tasks {
				fmt.Printf("[EXECUTOR] Executing task: %s, Module: %s, Tags: %v\n", task.Name, task.ModuleName, task.Tags)
				taskResults, _ := e.executeTask(host, task, playVars)
				for _, tr := range taskResults {
					if tr != nil {
						fmt.Printf("[EXECUTOR] Task result: Name=%s, Skipped=%v, Failed=%v, Changed=%v\n", tr.Name, tr.Skipped, tr.Failed, tr.Changed)
					}
				}
				hostResult.Tasks = append(hostResult.Tasks, taskResults...)
			}
		}
	}

	// 执行 post_tasks
	fmt.Printf("[EXECUTOR] Post-tasks count: %d\n", len(play.PostTasks))
	if len(play.PostTasks) > 0 {
		for _, host := range hosts {
			hostResult := e.getHostResult(result, host.Name)
			for _, task := range play.PostTasks {
				fmt.Printf("[EXECUTOR] Executing post-task: %s\n", task.Name)
				taskResults, _ := e.executeTask(host, task, playVars)
				hostResult.Tasks = append(hostResult.Tasks, taskResults...)
			}
		}
	}

	return result, nil
}

// getPlayHosts 获取 play 的目标主机
func (e *Executor) getPlayHosts(play *Play) ([]*inventory.Host, error) {
	fmt.Println("[EXECUTOR] getPlayHosts() called")

	// 先解析 hosts 字段中的变量
	hostsStr := play.Hosts
	fmt.Printf("[EXECUTOR] Original hosts string: %s\n", hostsStr)

	// 使用 extra vars 替换 {{ 变量 }}
	e.mu.RLock()
	for k, v := range e.extraVars {
		placeholder := fmt.Sprintf("{{ %s }}", k)
		if contains(hostsStr, placeholder) {
			hostsStr = replaceAll(hostsStr, placeholder, fmt.Sprintf("%v", v))
			fmt.Printf("[EXECUTOR] Replaced %s with %v\n", placeholder, v)
		}
		// 也处理不带空格的情况
		placeholder2 := fmt.Sprintf("{{%s}}", k)
		if contains(hostsStr, placeholder2) {
			hostsStr = replaceAll(hostsStr, placeholder2, fmt.Sprintf("%v", v))
			fmt.Printf("[EXECUTOR] Replaced %s with %v\n", placeholder2, v)
		}
	}
	e.mu.RUnlock()

	hostsStr = strings.TrimSpace(hostsStr)
	fmt.Printf("[EXECUTOR] Resolved hosts string: %s\n", hostsStr)

	if hostsStr == "all" || hostsStr == "*" {
		fmt.Println("[EXECUTOR] Getting all hosts")
		return e.inventory.GetAllHosts(), nil
	}

	fmt.Printf("[EXECUTOR] Looking for group/host: %s\n", hostsStr)
	fmt.Printf("[EXECUTOR] Available groups: %v\n", getMapKeys(e.inventory.Groups))
	fmt.Printf("[EXECUTOR] Available hosts: %v\n", getMapKeys2(e.inventory.AllHosts))

	group, err := e.inventory.GetGroup(hostsStr)
	if err != nil {
		// 可能是单个主机
		fmt.Printf("[EXECUTOR] Group not found, trying as host: %s\n", hostsStr)
		host, hostErr := e.inventory.GetHost(hostsStr)
		if hostErr != nil {
			fmt.Printf("[EXECUTOR] Host not found either: %v\n", hostErr)
			return nil, fmt.Errorf("no matching hosts for %q", hostsStr)
		}
		fmt.Printf("[EXECUTOR] Found host: %s\n", host.Name)
		return []*inventory.Host{host}, nil
	}

	fmt.Printf("[EXECUTOR] Found group: %s\n", group.Name)
	return e.inventory.GetGroupHosts(group.Name)
}

// getMapKeys 获取 map 的键列表
func getMapKeys(m map[string]*inventory.Group) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// getMapKeys2 获取 map 的键列表
func getMapKeys2(m map[string]*inventory.Host) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// executeTask 执行单个任务，返回结果列表（循环任务会返回多个结果）
func (e *Executor) executeTask(host *inventory.Host, task *Task, vars map[string]interface{}) ([]*TaskResult, error) {
	// 合并 extra vars 到 vars
	allVars := make(map[string]interface{})
	for k, v := range vars {
		allVars[k] = v
	}
	e.mu.RLock()
	for k, v := range e.extraVars {
		allVars[k] = v
	}
	e.mu.RUnlock()

	e.log("Executing task: %s on host: %s", task.Name, host.Name)
	e.log("Task tags: %v, Filter tags: %v", task.Tags, e.tags)

	result := &TaskResult{
		Name:    e.resolveString(task.Name, allVars),
		Host:    host.Name,
		Start:   time.Now(),
		Changed: false,
		Failed:  false,
	}

	// 检查 tags 过滤 - 如果指定了 tags，只有匹配的任务才执行
	if len(e.tags) > 0 {
		if len(task.Tags) == 0 || !e.matchTags(task.Tags) {
			result.Skipped = true
			result.Message = "skipped due to tags filter"
			result.End = time.Now()
			e.log("Task skipped: tags not matched")
			return []*TaskResult{result}, nil
		}
	}

	e.log("Task passed tags filter, executing...")

	// 检查 when 条件
	if task.When != nil {
		if !e.evaluateWhen(task.When, allVars) {
			result.Skipped = true
			result.Message = "skipped due to when condition"
			result.End = time.Now()
			return []*TaskResult{result}, nil
		}
	}

	// 检查 loop
	if task.Loop != nil {
		items := e.evaluateLoop(task.Loop, allVars)
		e.log("Loop items: %v", items)
		results := make([]*TaskResult, 0, len(items))

		for _, item := range items {
			// 先解析 item 中的变量
			resolvedItem := e.resolveValue(item, allVars)

			loopVars := make(map[string]interface{})
			for k, v := range allVars {
				loopVars[k] = v
			}
			loopVars["item"] = resolvedItem

			// 创建单个 item 的结果
			itemResult := &TaskResult{
				Name:    e.resolveString(task.Name, loopVars),
				Host:    host.Name,
				Start:   time.Now(),
				Changed: false,
				Failed:  false,
				Item:    resolvedItem,
			}

			// 执行任务
			mod, err := e.registry.Get(task.ModuleName)
			if err != nil {
				itemResult.Failed = true
				itemResult.Message = err.Error()
				itemResult.End = time.Now()
				results = append(results, itemResult)
				continue
			}

			// 替换变量
			params := e.resolveVariables(task.Params, loopVars)

			// 将主机的 become 设置添加到模块参数中
			if host.Become {
				params["become"] = true
				if host.BecomeUser != "" {
					params["become_user"] = host.BecomeUser
				}
				if host.BecomeMethod != "" {
					params["become_method"] = host.BecomeMethod
				}
				if host.BecomePass != "" {
					params["become_pass"] = host.BecomePass
				}
			}

			e.log("Resolved params: %+v", params)

			fmt.Printf("[EXECUTOR] Executing module: %s on host: %s (Become: %v)\n", task.ModuleName, host.Name, host.Become)
			client := e.getOrCreateClient(host)
			if client == nil {
				itemResult.Failed = true
				itemResult.Message = "failed to create SSH client"
				itemResult.End = time.Now()
				e.log("Failed to create SSH client")
				results = append(results, itemResult)
				continue
			}

			e.log("Executing module: %s", task.ModuleName)
			modResult, err := mod.Execute(client, params)
			if err != nil {
				e.log("Module execution error: %v", err)
				if !task.IgnoreError {
					itemResult.Failed = true
					itemResult.Message = err.Error()
				} else {
					itemResult.Message = fmt.Sprintf("ignored error: %v", err)
				}
			}

			if modResult != nil {
				e.log("Module result: Changed=%v, Stdout=%s", modResult.Changed, modResult.Stdout)
				itemResult.Changed = modResult.Changed
				itemResult.Stdout = modResult.Stdout
				itemResult.Stderr = modResult.Stderr
				itemResult.ExitCode = modResult.ExitCode
			}

			itemResult.End = time.Now()
			results = append(results, itemResult)
		}

		return results, nil
	}

	// 普通任务执行
	e.log("Getting module: %s", task.ModuleName)
	mod, err := e.registry.Get(task.ModuleName)
	if err != nil {
		result.Failed = true
		result.Message = fmt.Sprintf("module not found: %s", task.ModuleName)
		result.End = time.Now()
		return []*TaskResult{result}, nil
	}

	// 解析变量
	params := e.resolveVariables(task.Params, allVars)

	// 将主机的 become 设置添加到模块参数中
	if host.Become {
		params["become"] = true
		if host.BecomeUser != "" {
			params["become_user"] = host.BecomeUser
		}
		if host.BecomeMethod != "" {
			params["become_method"] = host.BecomeMethod
		}
		if host.BecomePass != "" {
			params["become_pass"] = host.BecomePass
		}
	}

	e.log("Resolved params: %+v", params)

	fmt.Printf("[EXECUTOR] Executing module: %s on host: %s (Become: %v)\n", task.ModuleName, host.Name, host.Become)
	client := e.getOrCreateClient(host)
	if client == nil {
		result.Failed = true
		result.Message = "failed to create SSH client"
		result.End = time.Now()
		e.log("Failed to create SSH client")
		return []*TaskResult{result}, nil
	}

	e.log("Executing module: %s", task.ModuleName)
	modResult, err := mod.Execute(client, params)
	if err != nil {
		e.log("Module execution error: %v", err)
		if !task.IgnoreError {
			result.Failed = true
			result.Message = err.Error()
		} else {
			result.Message = fmt.Sprintf("ignored error: %v", err)
		}
	}

	if modResult != nil {
		e.log("Module result: Changed=%v", modResult.Changed)
		result.Changed = modResult.Changed
		result.Stdout = modResult.Stdout
		result.Stderr = modResult.Stderr
		result.ExitCode = modResult.ExitCode
		if modResult.Message != "" {
			result.Message = modResult.Message
		}
	}

	// register 变量
	if task.Register != "" {
		e.mu.Lock()
		if e.facts[host.Name] == nil {
			e.facts[host.Name] = make(map[string]interface{})
		}
		e.facts[host.Name][task.Register] = map[string]interface{}{
			"changed": result.Changed,
			"stdout":  result.Stdout,
			"stderr":  result.Stderr,
			"rc":      result.ExitCode,
			"msg":     result.Message,
		}
		e.mu.Unlock()
	}

	// notify handlers
	if task.Notify != nil {
		e.notifyHandlers(task.Notify)
	}

	result.End = time.Now()
	return []*TaskResult{result}, nil
}

// getOrCreateClient 获取或创建 SSH 客户端
func (e *Executor) getOrCreateClient(host *inventory.Host) *ssh.Client {
	client, err := e.pool.Get(host)
	if err != nil {
		return nil
	}
	return client
}

// gatherFacts 收集主机信息
func (e *Executor) gatherFacts(hosts []*inventory.Host) {
	mod, err := e.registry.Get("setup")
	if err != nil {
		return
	}

	for _, host := range hosts {
		client := e.getOrCreateClient(host)
		if client == nil {
			continue
		}

		result, err := mod.Execute(client, nil)
		if err == nil && result != nil {
			e.mu.Lock()
			e.facts[host.Name] = e.parseFacts(result.Stdout)
			e.mu.Unlock()
		}
	}
}

// parseFacts 解析 facts 输出
func parseFacts(output string) map[string]interface{} {
	facts := make(map[string]interface{})
	// 简单解析 YAML 格式的 facts
	lines := splitLines(output)
	inFacts := false
	for _, line := range lines {
		if line == "gathered_facts:" {
			inFacts = true
			continue
		}
		if inFacts && len(line) > 2 && line[0:2] == "  " {
			// 解析 key: value
			for i := 2; i < len(line); i++ {
				if line[i] == ':' {
					key := line[2:i]
					value := ""
					if i+2 < len(line) {
						value = line[i+2:]
					}
					facts[key] = value
					break
				}
			}
		}
	}
	return facts
}

func (e *Executor) parseFacts(output string) map[string]interface{} {
	return parseFacts(output)
}

// splitLines 分割行
func splitLines(s string) []string {
	lines := make([]string, 0)
	current := ""
	for _, c := range s {
		if c == '\n' {
			lines = append(lines, current)
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

// evaluateWhen 评估 when 条件
func (e *Executor) evaluateWhen(when interface{}, vars map[string]interface{}) bool {
	switch v := when.(type) {
	case bool:
		return v
	case string:
		// 简单条件评估
		if v == "true" || v == "yes" {
			return true
		}
		if v == "false" || v == "no" {
			return false
		}
		// 检查变量
		if val, ok := vars[v]; ok {
			if b, ok := val.(bool); ok {
				return b
			}
			return val != nil
		}
		return true
	case []interface{}:
		// 多个条件，需要全部满足
		for _, cond := range v {
			if !e.evaluateWhen(cond, vars) {
				return false
			}
		}
		return true
	default:
		return true
	}
}

// evaluateLoop 评估 loop
func (e *Executor) evaluateLoop(loop interface{}, vars map[string]interface{}) []interface{} {
	switch v := loop.(type) {
	case []interface{}:
		return v
	case string:
		// 检查是否是变量
		if val, ok := vars[v]; ok {
			if items, ok := val.([]interface{}); ok {
				return items
			}
		}
		return []interface{}{v}
	default:
		return []interface{}{v}
	}
}

// resolveVariables 解析变量
func (e *Executor) resolveVariables(params map[string]interface{}, vars map[string]interface{}) map[string]interface{} {
	resolved := make(map[string]interface{})
	for k, v := range params {
		resolved[k] = e.resolveValue(v, vars)
	}
	return resolved
}

// resolveValue 解析单个值
func (e *Executor) resolveValue(v interface{}, vars map[string]interface{}) interface{} {
	switch val := v.(type) {
	case string:
		return e.resolveString(val, vars)
	case map[string]interface{}:
		return e.resolveVariables(val, vars)
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, item := range val {
			result[i] = e.resolveValue(item, vars)
		}
		return result
	default:
		return v
	}
}

// resolveString 解析字符串中的变量
func (e *Executor) resolveString(s string, vars map[string]interface{}) string {
	result := s

	// 循环替换直到没有变量（支持嵌套变量）
	maxIterations := 10
	for i := 0; i < maxIterations; i++ {
		oldResult := result

		// 匹配 {{ xxx }} 或 {{xxx}}
		for {
			start := index(result, "{{")
			if start == -1 {
				break
			}
			end := index(result[start:], "}}")
			if end == -1 {
				break
			}
			end += start

			// 提取变量名
			varExpr := result[start+2 : end]
			varExpr = strings.TrimSpace(varExpr)

			// 解析变量值
			varValue := e.resolveVarExpr(varExpr, vars)

			// 替换
			result = result[:start] + fmt.Sprintf("%v", varValue) + result[end+2:]
		}

		if result == oldResult {
			break
		}
	}

	return result
}

// resolveVarExpr 解析变量表达式，支持嵌套属性如 item.src
func (e *Executor) resolveVarExpr(expr string, vars map[string]interface{}) interface{} {
	// 处理点号分隔的属性访问
	parts := strings.Split(expr, ".")
	var current interface{} = vars

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		switch v := current.(type) {
		case map[string]interface{}:
			if val, ok := v[part]; ok {
				current = val
			} else {
				return "{{ " + expr + " }}"
			}
		case map[string]string:
			if val, ok := v[part]; ok {
				current = val
			} else {
				return "{{ " + expr + " }}"
			}
		default:
			return "{{ " + expr + " }}"
		}
	}

	return current
}

// index 返回子字符串在字符串中的位置，-1 表示未找到
func index(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// matchTags 检查任务标签是否匹配
func (e *Executor) matchTags(taskTags []string) bool {
	if len(e.tags) == 0 {
		return true
	}
	for _, tag := range e.tags {
		for _, taskTag := range taskTags {
			if tag == taskTag || tag == "all" || tag == "always" || taskTag == "always" {
				return true
			}
		}
	}
	return false
}

func replaceAll(s, old, new string) string {
	result := ""
	i := 0
	for i < len(s) {
		if i+len(old) <= len(s) && s[i:i+len(old)] == old {
			result += new
			i += len(old)
		} else {
			result += string(s[i])
			i++
		}
	}
	return result
}

// contains 检查字符串是否包含子串
func contains(s, substr string) bool {
	if len(substr) > len(s) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// notifyHandlers 通知 handlers
func (e *Executor) notifyHandlers(notify interface{}) {
	// 实现 handler 通知逻辑
}

// getHostResult 获取主机结果
func (e *Executor) getHostResult(playResult *PlayResult, hostName string) *HostResult {
	if _, ok := playResult.Hosts[hostName]; !ok {
		playResult.Hosts[hostName] = &HostResult{
			Tasks: make([]*TaskResult, 0),
		}
	}
	return playResult.Hosts[hostName]
}

// Close 关闭执行器
func (e *Executor) Close() error {
	return e.pool.Close()
}

// PlaybookResult Playbook 执行结果
type PlaybookResult struct {
	Plays []*PlayResult
}

// PlayResult Play 执行结果
type PlayResult struct {
	Name  string
	Hosts map[string]*HostResult
}

// HostResult 主机执行结果
type HostResult struct {
	Tasks []*TaskResult
}

// TaskResult 任务执行结果
type TaskResult struct {
	Name     string
	Host     string
	Changed  bool
	Failed   bool
	Skipped  bool
	Stdout   string
	Stderr   string
	ExitCode int
	Message  string
	Item     interface{} // 循环时的 item 值
	Start    time.Time
	End      time.Time
}
