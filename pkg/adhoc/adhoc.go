package adhoc

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/module"
	"go-ansible/pkg/ssh"
	"go-ansible/pkg/util"
)

// Adhoc Ad-hoc 命令执行器
type Adhoc struct {
	inventory *inventory.Inventory
	pool      *ssh.ConnectionPool
	registry  *module.Registry
	forks     int
	limit     string
	extraVars map[string]interface{}
	checkMode bool
	diffMode  bool
	async     int
	poll      int
}

// NewAdhoc 创建新的 Ad-hoc 执行器
func NewAdhoc(inv *inventory.Inventory, forks int) *Adhoc {
	return NewAdhocWithConfig(inv, forks, nil)
}

func NewAdhocWithConfig(inv *inventory.Inventory, forks int, sshConfig *ssh.ClientConfig) *Adhoc {
	if forks <= 0 {
		forks = 5
	}
	return &Adhoc{
		inventory: inv,
		pool:      ssh.NewConnectionPool(sshConfig),
		registry:  module.NewRegistry(),
		forks:     forks,
		extraVars: make(map[string]interface{}),
	}
}

func (a *Adhoc) SetAsync(async, poll int) {
	a.async = async
	a.poll = poll
}

func (a *Adhoc) SetLimit(limit string) {
	a.limit = limit
}

func (a *Adhoc) SetExtraVars(vars map[string]interface{}) {
	for k, v := range vars {
		a.extraVars[k] = v
	}
	if a.inventory != nil {
		a.inventory.ApplyConnectionVars(vars)
	}
}

func (a *Adhoc) SetCheckMode(check bool) {
	a.checkMode = check
}

func (a *Adhoc) SetDiffMode(diff bool) {
	a.diffMode = diff
}

func (a *Adhoc) ListHosts(target string) ([]string, error) {
	hosts, err := a.getTargetHosts(target)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(hosts))
	for _, host := range hosts {
		result = append(result, host.Name)
	}
	return result, nil
}

// Execute 执行 Ad-hoc 命令
func (a *Adhoc) Execute(target, moduleName string, params map[string]interface{}) (*Result, error) {
	// 获取目标主机
	hosts, err := a.getTargetHosts(target)
	if err != nil {
		return nil, err
	}

	// 获取模块
	mod, err := a.registry.Get(moduleName)
	if err != nil {
		return nil, fmt.Errorf("module not found: %s", moduleName)
	}

	// 验证参数
	if err := mod.Validate(params); err != nil {
		return nil, fmt.Errorf("validate params: %w", err)
	}

	// 并发执行
	result := &Result{
		Hosts: make(map[string]*HostResult),
	}

	sem := make(chan struct{}, a.forks)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, host := range hosts {
		wg.Add(1)
		sem <- struct{}{}

		go func(h *inventory.Host) {
			defer wg.Done()
			defer func() { <-sem }()

			start := time.Now()
			client, err := a.pool.Get(h)
			if err != nil {
				mu.Lock()
				result.Hosts[h.Name] = &HostResult{
					Failed:  true,
					Message: err.Error(),
					Start:   start,
					End:     time.Now(),
				}
				mu.Unlock()
				return
			}

			resolvedParams := a.resolveParamsForHost(h, moduleName, params)
			var modResult *module.Result
			if a.async > 0 {
				if a.poll == 0 {
					jobID := fmt.Sprintf("%d.%d", time.Now().UnixNano(), os.Getpid())
					go func() {
						_, _ = mod.Execute(client, resolvedParams)
					}()
					modResult = &module.Result{
						Changed: true,
						Data: map[string]interface{}{
							"ansible_job_id": jobID,
							"started":        1,
							"finished":       0,
							"results_file":   fmt.Sprintf("/tmp/.ansible_async/%s", jobID),
						},
						Message: fmt.Sprintf("background job started: %s", jobID),
					}
					err = nil
				} else {
					type asyncRes struct {
						res *module.Result
						err error
					}
					done := make(chan asyncRes, 1)
					go func() {
						r, e := mod.Execute(client, resolvedParams)
						done <- asyncRes{res: r, err: e}
					}()
					timer := time.NewTimer(time.Duration(a.async) * time.Second)
					defer timer.Stop()
					interval := time.Duration(a.poll) * time.Second
					if interval <= 0 {
						interval = time.Second
					}
					ticker := time.NewTicker(interval)
					defer ticker.Stop()

				pollLoop:
					for {
						select {
						case ar := <-done:
							modResult = ar.res
							err = ar.err
							break pollLoop
						case <-timer.C:
							err = fmt.Errorf("async task timed out after %d seconds", a.async)
							modResult = &module.Result{
								Failed:  true,
								Changed: true,
								Message: err.Error(),
							}
							break pollLoop
						case <-ticker.C:
						}
					}
				}
			} else {
				modResult, err = mod.Execute(client, resolvedParams)
			}
			hostResult := &HostResult{
				Start: start,
			}

			if err != nil {
				hostResult.Failed = true
				hostResult.Message = err.Error()
			}

			if modResult != nil {
				hostResult.Changed = modResult.Changed
				hostResult.Stdout = modResult.Stdout
				hostResult.Stderr = modResult.Stderr
				hostResult.ExitCode = modResult.ExitCode
				hostResult.Diff = modResult.Diff
				hostResult.Data = modResult.Data
				if modResult.Message != "" {
					hostResult.Message = modResult.Message
				}
			}
			hostResult.End = time.Now()

			mu.Lock()
			result.Hosts[h.Name] = hostResult
			mu.Unlock()
		}(host)
	}

	wg.Wait()
	return result, nil
}

func (a *Adhoc) resolveParamsForHost(host *inventory.Host, moduleName string, params map[string]interface{}) map[string]interface{} {
	vars := make(map[string]interface{})

	if a.inventory != nil {
		for key, value := range a.inventory.ResolveHostVars(host) {
			vars[key] = value
		}
	}

	if host != nil {
		if host.Name != "" {
			vars["inventory_hostname"] = host.Name
		}
		if host.Address != "" {
			vars["ansible_host"] = host.Address
		}
		if host.User != "" {
			vars["ansible_user"] = host.User
		}
		if host.Port > 0 {
			vars["ansible_port"] = host.Port
		}
	}

	for key, value := range a.extraVars {
		vars[key] = value
	}

	resolved := resolveAdhocValueMap(params, vars)
	if a.checkMode && supportsAdhocCheckMode(moduleName) {
		resolved["__check_mode"] = true
		resolved["_ansible_check_mode"] = true
		resolved["check_mode"] = true
	}
	if a.diffMode && supportsAdhocDiffMode(moduleName) {
		resolved["__diff"] = true
		resolved["_ansible_diff"] = true
	}
	return resolved
}

func resolveAdhocValueMap(params map[string]interface{}, vars map[string]interface{}) map[string]interface{} {
	resolved := make(map[string]interface{}, len(params))
	for key, value := range params {
		resolved[key] = resolveAdhocValue(value, vars)
	}
	return resolved
}

func resolveAdhocValue(value interface{}, vars map[string]interface{}) interface{} {
	switch v := value.(type) {
	case string:
		return resolveAdhocString(v, vars)
	case map[string]interface{}:
		return resolveAdhocValueMap(v, vars)
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, item := range v {
			result[i] = resolveAdhocValue(item, vars)
		}
		return result
	default:
		return value
	}
}

func resolveAdhocString(s string, vars map[string]interface{}) interface{} {
	if !strings.Contains(s, "{{") {
		return s
	}

	trimmed := strings.TrimSpace(s)
	if name, ok := extractSimpleTemplate(trimmed); ok {
		if value, exists := vars[name]; exists {
			return value
		}
	}

	keys := sortedAdhocVarKeys(vars)
	result := s
	for i := 0; i < 10; i++ {
		previous := result
		for _, key := range keys {
			placeholder := "{{ " + key + " }}"
			result = strings.ReplaceAll(result, placeholder, fmt.Sprintf("%v", vars[key]))
			placeholder = "{{" + key + "}}"
			result = strings.ReplaceAll(result, placeholder, fmt.Sprintf("%v", vars[key]))
			if !strings.Contains(result, "{{") {
				return result
			}
		}
		if result == previous || !strings.Contains(result, "{{") {
			break
		}
	}
	return result
}

func extractSimpleTemplate(value string) (string, bool) {
	if !strings.HasPrefix(value, "{{") || !strings.HasSuffix(value, "}}") {
		return "", false
	}
	name := strings.TrimSpace(value[2 : len(value)-2])
	if name == "" || strings.ContainsAny(name, " |.") {
		return "", false
	}
	return name, true
}

func sortedAdhocVarKeys(vars map[string]interface{}) []string {
	keys := make([]string, 0, len(vars))
	for key := range vars {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) == len(keys[j]) {
			return keys[i] < keys[j]
		}
		return len(keys[i]) > len(keys[j])
	})
	return keys
}

func supportsAdhocCheckMode(moduleName string) bool {
	return module.SupportsCheckMode(moduleName)
}

func supportsAdhocDiffMode(moduleName string) bool {
	return module.SupportsDiffMode(moduleName)
}

// ExecuteShell 执行 shell 命令（快捷方式）
func (a *Adhoc) ExecuteShell(target, cmd string) (*Result, error) {
	params := map[string]interface{}{
		"_raw_params": cmd,
	}
	return a.Execute(target, "shell", params)
}

// ExecuteCommand 执行 command 命令（快捷方式）
func (a *Adhoc) ExecuteCommand(target, cmd string) (*Result, error) {
	params := map[string]interface{}{
		"_raw_params": cmd,
	}
	return a.Execute(target, "command", params)
}

// Ping 测试连接
func (a *Adhoc) Ping(target string) (*Result, error) {
	return a.Execute(target, "ping", nil)
}

// GatherFacts 收集主机信息
func (a *Adhoc) GatherFacts(target string) (*Result, error) {
	return a.Execute(target, "setup", nil)
}

// getTargetHosts 获取目标主机列表
func (a *Adhoc) getTargetHosts(target string) ([]*inventory.Host, error) {
	hosts, err := a.inventory.MatchHosts(target)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.limit) != "" {
		return a.inventory.FilterHosts(hosts, a.limit)
	}
	return hosts, nil
}

// Close 关闭执行器
func (a *Adhoc) Close() error {
	return a.pool.Close()
}

// Result Ad-hoc 执行结果
type Result struct {
	Hosts map[string]*HostResult
}

// HostResult 单个主机的执行结果
type HostResult struct {
	Changed  bool
	Failed   bool
	Stdout   string
	Stderr   string
	ExitCode int
	Message  string
	Diff     string
	Data     map[string]interface{}
	Start    time.Time
	End      time.Time
}

// FormatResult 格式化输出结果
func (r *Result) FormatResult() string {
	var builder strings.Builder
	for _, hostName := range sortedAdhocHostNames(r.Hosts) {
		hostResult := r.Hosts[hostName]
		builder.WriteString(fmt.Sprintf("%s | ", hostName))

		if hostResult.Failed {
			builder.WriteString("FAILED")
			if hostResult.Message != "" {
				builder.WriteString(fmt.Sprintf(" | %s", hostResult.Message))
			}
		} else if hostResult.Changed {
			builder.WriteString("CHANGED")
		} else {
			builder.WriteString("SUCCESS")
		}
		builder.WriteString("\n")

		if hostResult.Message != "" && !hostResult.Failed {
			builder.WriteString(fmt.Sprintf("  msg: %s\n", hostResult.Message))
		}
		if hostResult.Stdout != "" {
			builder.WriteString(fmt.Sprintf("  stdout: %s\n", util.IndentBlock(hostResult.Stdout, "    ")))
		}
		if hostResult.Stderr != "" {
			builder.WriteString(fmt.Sprintf("  stderr: %s\n", util.IndentBlock(hostResult.Stderr, "    ")))
		}
		if hostResult.Diff != "" {
			builder.WriteString("  diff:\n")
			builder.WriteString(util.IndentBlock(hostResult.Diff, "    "))
			builder.WriteString("\n")
		}
		if len(hostResult.Data) > 0 {
			builder.WriteString("  data:\n")
			builder.WriteString(util.IndentBlock(formatAdhocData(hostResult.Data), "    "))
			builder.WriteString("\n")
		}
	}
	return strings.TrimRight(builder.String(), "\n")
}

// GetChanged 获取变更的主机列表
func (r *Result) GetChanged() []string {
	changed := make([]string, 0)
	for hostName, hostResult := range r.Hosts {
		if hostResult.Changed {
			changed = append(changed, hostName)
		}
	}
	return changed
}

// GetFailed 获取失败的主机列表
func (r *Result) GetFailed() []string {
	failed := make([]string, 0)
	for hostName, hostResult := range r.Hosts {
		if hostResult.Failed {
			failed = append(failed, hostName)
		}
	}
	return failed
}

// IsAllSuccess 检查是否全部成功
func (r *Result) IsAllSuccess() bool {
	for _, hostResult := range r.Hosts {
		if hostResult.Failed {
			return false
		}
	}
	return true
}

func sortedAdhocHostNames(hosts map[string]*HostResult) []string {
	names := make([]string, 0, len(hosts))
	for name := range hosts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func formatAdhocData(data map[string]interface{}) string {
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", data)
	}
	return string(encoded)
}
