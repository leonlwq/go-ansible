package adhoc

import (
	"fmt"
	"sync"
	"time"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/module"
	"go-ansible/pkg/ssh"
)

// Adhoc Ad-hoc 命令执行器
type Adhoc struct {
	inventory *inventory.Inventory
	pool      *ssh.ConnectionPool
	registry  *module.Registry
	forks     int
}

// NewAdhoc 创建新的 Ad-hoc 执行器
func NewAdhoc(inv *inventory.Inventory, forks int) *Adhoc {
	if forks <= 0 {
		forks = 5
	}
	return &Adhoc{
		inventory: inv,
		pool:      ssh.NewConnectionPool(nil),
		registry:  module.NewRegistry(),
		forks:     forks,
	}
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

			client, err := a.pool.Get(h)
			if err != nil {
				mu.Lock()
				result.Hosts[h.Name] = &HostResult{
					Failed:  true,
					Message: err.Error(),
				}
				mu.Unlock()
				return
			}

			modResult, err := mod.Execute(client, params)
			hostResult := &HostResult{}

			if err != nil {
				hostResult.Failed = true
				hostResult.Message = err.Error()
			}

			if modResult != nil {
				hostResult.Changed = modResult.Changed
				hostResult.Stdout = modResult.Stdout
				hostResult.Stderr = modResult.Stderr
				hostResult.ExitCode = modResult.ExitCode
				if modResult.Message != "" {
					hostResult.Message = modResult.Message
				}
			}

			mu.Lock()
			result.Hosts[h.Name] = hostResult
			mu.Unlock()
		}(host)
	}

	wg.Wait()
	return result, nil
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
	if target == "all" || target == "*" {
		return a.inventory.GetAllHosts(), nil
	}

	// 尝试作为组获取
	if hosts, err := a.inventory.GetGroupHosts(target); err == nil {
		return hosts, nil
	}

	// 尝试作为主机获取
	if host, err := a.inventory.GetHost(target); err == nil {
		return []*inventory.Host{host}, nil
	}

	return nil, fmt.Errorf("no matching hosts for %q", target)
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
	Start    time.Time
	End      time.Time
}

// FormatResult 格式化输出结果
func (r *Result) FormatResult() string {
	output := ""
	for hostName, hostResult := range r.Hosts {
		output += fmt.Sprintf("%s | ", hostName)

		if hostResult.Failed {
			output += "FAILED"
			if hostResult.Message != "" {
				output += fmt.Sprintf(" | %s", hostResult.Message)
			}
		} else if hostResult.Changed {
			output += "CHANGED"
		} else {
			output += "SUCCESS"
		}

		if hostResult.Stdout != "" {
			output += fmt.Sprintf("\n%s", hostResult.Stdout)
		}

		if hostResult.Stderr != "" {
			output += fmt.Sprintf("\nSTDERR: %s", hostResult.Stderr)
		}

		output += "\n"
	}
	return output
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
