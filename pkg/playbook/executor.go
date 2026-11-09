package playbook

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/module"
	"go-ansible/pkg/ssh"
	"go-ansible/pkg/variable"
)

// pendingHandler 待执行的 handler
type pendingHandler struct {
	handler *Handler
	host    *inventory.Host
	vars    map[string]interface{}
	env     map[string]string
}

// Executor Playbook 执行器
type Executor struct {
	inventory             *inventory.Inventory
	inventoryPath         string
	inventoryLoader       func(string) (*inventory.Inventory, error)
	pool                  *ssh.ConnectionPool
	sshConfig             *ssh.ClientConfig
	registry              *module.Registry
	varManager            *variable.Manager
	facts                 map[string]map[string]interface{}
	extraVars             map[string]interface{}
	tags                  []string
	skipTags              []string
	limitPatterns         []string
	startAtTask           string
	startedTasks          map[string]bool
	checkMode             bool
	diffMode              bool
	verbose               bool
	verbosityLevel        int
	stepMode              bool
	forceHandlers         bool
	stepPromptFunc        func(taskName string) (string, error)
	baseDir               string
	rolePaths             []string
	gathering             string
	pollInterval          time.Duration
	currentResult         *PlayResult
	endedHosts            map[string]bool
	failedHosts           map[string]bool
	endPlay               bool
	forks                 int
	pendingHandlers       []pendingHandler
	activePlayHosts       []string
	activeBatchHosts      []string
	currentPlayName       string
	throttleSems          map[string]chan struct{}
	throttleMu            sync.RWMutex
	factCaching           string
	factCachingConnection string
	factCachingTimeout    int
	factCacheTimestamps   map[string]time.Time
	ctx                   context.Context
	cancelFunc            context.CancelFunc
	mu                    sync.RWMutex

	Stdin                io.Reader
	Stdout               io.Writer
	debuggerMu           sync.Mutex
	flushMu              sync.Mutex
	currentPlayDebugger  string
	currentPlayHandlers  []*Handler
	currentPlayCheckMode *bool
	currentPlayDiff      *bool
	currentPlayDefaults      map[string]interface{}
	currentPlayForceHandlers bool

	cachedGroupsMap        map[string][]string
	cachedGroupNames       map[string][]string
	groupsDirty            bool
	groupsMu               sync.RWMutex
	baseHostVarsCache      map[string]map[string]interface{}
	cachedHostVarsSnapshot map[string]map[string]interface{}
	hostVarsMu             sync.RWMutex

	parsedRoles    sync.Map // map[string]*Role，缓存已解析的角色 AST，避免海量主机重复磁盘读写与反序列化
	parsedIncludes sync.Map // map[string][]*Task，缓存已解析的包含任务 AST
}

// NewExecutor 创建新的执行器
func NewExecutor(inv *inventory.Inventory) *Executor {
	return &Executor{
		inventory:           inv,
		inventoryLoader:     inventory.ParseFile,
		pool:                ssh.NewConnectionPool(nil),
		registry:            module.NewRegistry(),
		varManager:          variable.NewManager(),
		facts:               make(map[string]map[string]interface{}),
		extraVars:           make(map[string]interface{}),
		tags:                make([]string, 0),
		skipTags:            make([]string, 0),
		startedTasks:        make(map[string]bool),
		pollInterval:        15 * time.Second,
		forks:               5,
		pendingHandlers:     make([]pendingHandler, 0),
		throttleSems:        make(map[string]chan struct{}),
		factCacheTimestamps: make(map[string]time.Time),
		groupsDirty:         true,
		baseHostVarsCache:   make(map[string]map[string]interface{}),
	}
}

func (e *Executor) getStdin() io.Reader {
	if e.Stdin != nil {
		return e.Stdin
	}
	return os.Stdin
}

func (e *Executor) getStdout() io.Writer {
	if e.Stdout != nil {
		return e.Stdout
	}
	return os.Stdout
}

func (e *Executor) SetForks(forks int) {
	if forks <= 0 {
		return
	}
	e.forks = forks
}

func (e *Executor) GetForks() int {
	if e.forks <= 0 {
		return 5
	}
	return e.forks
}

func (e *Executor) SetSSHConfig(cfg *ssh.ClientConfig) {
	e.sshConfig = cfg
	e.pool = ssh.NewConnectionPool(cfg)
}

func (e *Executor) SetRolePaths(paths []string) {
	e.rolePaths = append([]string{}, paths...)
}

func (e *Executor) SetGathering(mode string) {
	e.gathering = strings.ToLower(strings.TrimSpace(mode))
}

func (e *Executor) SetPollInterval(seconds int) {
	if seconds <= 0 {
		return
	}
	e.pollInterval = time.Duration(seconds) * time.Second
}

func (e *Executor) SetVaultPassword(password string) {
	e.varManager.SetVaultPassword(password)
}

func (e *Executor) SetVaultSecrets(secrets []*variable.VaultSecret) {
	e.varManager.SetVaultSecrets(secrets)
}

func (e *Executor) AddVaultSecret(secret *variable.VaultSecret) {
	e.varManager.AddVaultSecret(secret)
}

func (e *Executor) SetInventoryPath(path string) {
	e.inventoryPath = strings.TrimSpace(path)
}

func (e *Executor) SetInventoryLoader(loader func(string) (*inventory.Inventory, error)) {
	if loader == nil {
		e.inventoryLoader = inventory.ParseFile
		return
	}
	e.inventoryLoader = loader
}

// SetVerbose 设置是否输出详细日志
func (e *Executor) SetVerbose(verbose bool) {
	e.verbose = verbose
	if verbose && e.verbosityLevel == 0 {
		e.verbosityLevel = 1
	}
}

func (e *Executor) SetVerbosityLevel(level int) {
	e.verbosityLevel = level
	e.verbose = level > 0
}

func (e *Executor) GetVerbosityLevel() int {
	return e.verbosityLevel
}

func (e *Executor) SetStepMode(step bool) {
	e.stepMode = step
}

func (e *Executor) SetForceHandlers(force bool) {
	e.forceHandlers = force
}

func (e *Executor) SetStepPromptFunc(fn func(taskName string) (string, error)) {
	e.stepPromptFunc = fn
}

func defaultStepPrompt(taskName string) (string, error) {
	fmt.Printf("Perform task: %q (N)o/(y)es/(c)ontinue: ", taskName)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "n", err
	}
	return strings.TrimSpace(line), nil
}

// SetCheckMode 设置 dry-run 模式
func (e *Executor) SetCheckMode(check bool) {
	e.checkMode = check
}

// SetDiffMode 设置是否输出 diff。
func (e *Executor) SetDiffMode(diff bool) {
	e.diffMode = diff
}

// SetContext 设置执行器上下文，用于优雅取消与超时控制
func (e *Executor) SetContext(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ctx = ctx
}

func (e *Executor) getContext() context.Context {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.ctx != nil {
		return e.ctx
	}
	return context.Background()
}

// sleep 优雅等待指定时长，若上下文已取消则立即中断并返回 false
func (e *Executor) sleep(d time.Duration) bool {
	if d <= 0 {
		return true
	}
	ctx := e.getContext()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
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
	for k, v := range vars {
		e.extraVars[k] = v
	}
	if e.inventory != nil {
		e.inventory.ApplyConnectionVars(vars)
	}
	e.mu.Unlock()
	e.invalidateHostVarsCache()
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
	e.tags = normalizeTagList(tags)
}

// GetTags 获取设置的 tags
func (e *Executor) GetTags() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]string, len(e.tags))
	copy(result, e.tags)
	return result
}

// SetSkipTags 设置跳过的 tags
func (e *Executor) SetSkipTags(tags []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.skipTags = normalizeTagList(tags)
}

// GetSkipTags 获取设置的 skip tags
func (e *Executor) GetSkipTags() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]string, len(e.skipTags))
	copy(result, e.skipTags)
	return result
}

// SetLimit 设置 play 执行的主机限制模式。
func (e *Executor) SetLimit(limit string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	trimmed := strings.TrimSpace(limit)
	if trimmed == "" {
		e.limitPatterns = nil
		return
	}
	e.limitPatterns = []string{trimmed}
}

// SetStartAtTask 设置从指定任务名开始执行。
func (e *Executor) SetStartAtTask(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.startAtTask = strings.TrimSpace(name)
	e.startedTasks = make(map[string]bool)
}

// Execute 执行 playbook
func (e *Executor) Execute(playbook *Playbook) (*PlaybookResult, error) {
	result := &PlaybookResult{
		Plays: make([]*PlayResult, 0),
		Start: time.Now(),
	}
	if playbook.Path != "" {
		e.baseDir = playbook.BaseDir()
	}

	if len(playbook.Plays) == 0 {
		result.End = time.Now()
		return result, nil
	}

	for _, play := range playbook.Plays {
		playResult, err := e.ExecutePlay(play)
		if playResult != nil {
			result.Plays = append(result.Plays, playResult)
		}
		if err != nil {
			result.End = time.Now()
			return result, fmt.Errorf("execute play %q: %w", play.Name, err)
		}
	}

	result.End = time.Now()
	return result, nil
}

// ExecutePlay 执行单个 play
func (e *Executor) ExecutePlay(play *Play) (*PlayResult, error) {
	result := &PlayResult{
		Name:  play.Name,
		Hosts: make(map[string]*HostResult),
	}
	e.currentResult = result
	e.endedHosts = make(map[string]bool)
	e.failedHosts = make(map[string]bool)
	e.endPlay = false
	e.currentPlayDebugger = play.Debugger
	e.currentPlayDefaults = nil
	e.mu.Lock()
	e.currentPlayName = play.Name
	e.currentPlayCheckMode = play.CheckMode
	e.currentPlayDiff = play.Diff
	e.currentPlayForceHandlers = play.ForceHandlers || e.forceHandlers
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.currentResult = nil
		e.endedHosts = nil
		e.failedHosts = nil
		e.endPlay = false
		e.currentPlayName = ""
		e.activeBatchHosts = nil
		e.currentPlayDebugger = ""
		e.currentPlayHandlers = nil
		e.currentPlayCheckMode = nil
		e.currentPlayDiff = nil
		e.currentPlayForceHandlers = false
		e.mu.Unlock()
	}()
	var playErr error

	hosts, err := e.getPlayHosts(play)
	if err != nil {
		return result, err
	}
	originalBecome := make(map[string]bool)
	originalBecomeUser := make(map[string]string)
	originalBecomeMethod := make(map[string]string)
	originalBecomePass := make(map[string]string)
	for _, h := range hosts {
		originalBecome[h.Name] = h.Become
		originalBecomeUser[h.Name] = h.BecomeUser
		originalBecomeMethod[h.Name] = h.BecomeMethod
		originalBecomePass[h.Name] = h.BecomePass
	}
	defer func() {
		for _, h := range hosts {
			h.Become = originalBecome[h.Name]
			h.BecomeUser = originalBecomeUser[h.Name]
			h.BecomeMethod = originalBecomeMethod[h.Name]
			h.BecomePass = originalBecomePass[h.Name]
		}
	}()

	playHostNames := make([]string, len(hosts))
	for i, h := range hosts {
		playHostNames[i] = h.Name
		if _, ok := result.Hosts[h.Name]; !ok {
			result.Hosts[h.Name] = &HostResult{
				Tasks: make([]*TaskResult, 0),
			}
		}
	}
	e.mu.Lock()
	e.activePlayHosts = playHostNames
	e.mu.Unlock()

	// 应用 play 级别的 become 配置到当前 play
	if play.Become {
		for _, h := range hosts {
			h.Become = true
			if play.BecomeUser != "" {
				h.BecomeUser = play.BecomeUser
			}
			if play.BecomeMethod != "" {
				h.BecomeMethod = play.BecomeMethod
			}
			if play.BecomePass != "" {
				h.BecomePass = play.BecomePass
			}
		}
	}

	// 合并变量
	playVars := make(map[string]interface{})
	for k, v := range play.Vars {
		playVars[k] = v
	}
	playVars["inventory_hostname"] = ""
	if err := e.loadPlayVarsFiles(play, playVars); err != nil {
		return result, err
	}

	if err := e.promptPlayVars(play, playVars); err != nil {
		return result, err
	}

	roleTasks, roleHandlers, roleVars, roleDefaults, err := e.loadPlayRoles(play)
	if err != nil {
		return result, err
	}
	e.mu.Lock()
	e.currentPlayDefaults = roleDefaults
	e.mu.Unlock()

	for k, v := range roleVars {
		if _, exists := playVars[k]; !exists {
			playVars[k] = v
		}
	}

	if play.RemoteUser != "" {
		for _, h := range hosts {
			// 如果主机显式指定了 ansible_user / ansible_ssh_user，其优先级高于 Play 的 remote_user
			if h.Variables != nil && (h.Variables["ansible_user"] != "" || h.Variables["ansible_ssh_user"] != "") {
				continue
			}
			hostVars := e.taskVarsForHost(h, playVars)
			if resolved := strings.TrimSpace(e.resolveString(play.RemoteUser, hostVars)); resolved != "" {
				h.User = resolved
			}
		}
	}

	// 应用 become_pass 从 play vars
	if becomePass, ok := play.Vars["ansible_become_pass"]; ok {
		for _, h := range hosts {
			h.BecomePass = fmt.Sprintf("%v", becomePass)
		}
	}

	handlers := make([]*Handler, 0, len(roleHandlers)+len(play.Handlers))
	handlers = append(handlers, roleHandlers...)
	handlers = append(handlers, play.Handlers...)
	e.currentPlayHandlers = handlers
	mergePlayTags(play.Tags, play.PreTasks)
	mergePlayTags(play.Tags, roleTasks)
	mergePlayTags(play.Tags, play.Tasks)
	mergePlayTags(play.Tags, play.PostTasks)

	taskRunOnceExecuted := make(map[string]bool)
	hostBatches := e.buildHostBatches(hosts, play.Serial)

	// 未显式声明 gather_facts 时，默认不收集。
	gatherFacts := e.effectiveGatherFacts(play)

	// 收集 facts
	if gatherFacts {
		fmt.Printf("Gathering facts for %d host(s)...\n", len(hosts))
		e.gatherFactsForPlay(play, hosts)
	}

	effectiveTasks := make([]*Task, 0, len(roleTasks)+len(play.Tasks))
	effectiveTasks = append(effectiveTasks, roleTasks...)
	effectiveTasks = append(effectiveTasks, play.Tasks...)

	for batchIndex, batchHosts := range hostBatches {
		e.log("Executing host batch %d/%d", batchIndex+1, len(hostBatches))
		batchNames := make([]string, len(batchHosts))
		for i, bh := range batchHosts {
			batchNames[i] = bh.Name
		}
		e.mu.Lock()
		e.activeBatchHosts = batchNames
		e.mu.Unlock()

		failedBeforeBatch := e.failedTaskCount(result)
		if err := e.executeTaskList(hosts, batchHosts, play.PreTasks, playVars, handlers, taskRunOnceExecuted, play.Environment, result, play.AnyErrorsFatal, play.Strategy); err != nil {
			playErr = err
			break
		}
		if play.AnyErrorsFatal && (e.failedTaskCount(result) > failedBeforeBatch || (e.shouldEndPlay() && e.failedTaskCount(result) > 0)) {
			playErr = fmt.Errorf("any_errors_fatal triggered")
			break
		}
		if e.shouldEndPlay() {
			break
		}
		if e.hasPendingHandlers() {
			if err := e.flushHandlers(result); err != nil {
				playErr = err
				break
			}
		}

		failedBeforeBatch = e.failedTaskCount(result)
		if err := e.executeTaskList(hosts, batchHosts, effectiveTasks, playVars, handlers, taskRunOnceExecuted, play.Environment, result, play.AnyErrorsFatal, play.Strategy); err != nil {
			playErr = err
			break
		}
		if play.AnyErrorsFatal && (e.failedTaskCount(result) > failedBeforeBatch || (e.shouldEndPlay() && e.failedTaskCount(result) > 0)) {
			playErr = fmt.Errorf("any_errors_fatal triggered")
			break
		}
		if e.shouldEndPlay() {
			break
		}
		if e.hasPendingHandlers() {
			if err := e.flushHandlers(result); err != nil {
				playErr = err
				break
			}
		}

		failedBeforeBatch = e.failedTaskCount(result)
		if err := e.executeTaskList(hosts, batchHosts, play.PostTasks, playVars, handlers, taskRunOnceExecuted, play.Environment, result, play.AnyErrorsFatal, play.Strategy); err != nil {
			playErr = err
			break
		}
		if play.AnyErrorsFatal && (e.failedTaskCount(result) > failedBeforeBatch || (e.shouldEndPlay() && e.failedTaskCount(result) > 0)) {
			playErr = fmt.Errorf("any_errors_fatal triggered")
			break
		}
		if e.shouldEndPlay() {
			break
		}
		if e.hasPendingHandlers() {
			if err := e.flushHandlers(result); err != nil {
				playErr = err
				break
			}
		}

		if play.MaxFailPct > 0 && e.exceededMaxFailPercentage(result, len(hosts), play.MaxFailPct) {
			playErr = fmt.Errorf("max_fail_percentage exceeded")
			break
		}
	}

	if e.hasPendingHandlers() && (playErr == nil || play.ForceHandlers || e.forceHandlers) {
		if err := e.flushHandlers(result); err != nil {
			playErr = err
		}
	}

	return result, playErr
}

func (e *Executor) failedTaskCount(result *PlayResult) int {
	if result == nil {
		return 0
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	count := 0
	for _, hostResult := range result.Hosts {
		for _, taskResult := range hostResult.GetTasks() {
			if taskResult.Failed && !taskResult.Rescued {
				count++
			}
		}
	}
	return count
}

func (e *Executor) hasPendingHandlers() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.pendingHandlers) > 0
}

// getPlayHosts 获取 play 的目标主机
func (e *Executor) getPlayHosts(play *Play) ([]*inventory.Host, error) {
	// 先解析 hosts 字段中的变量
	hostsStr := play.Hosts

	// 使用 extra vars 替换 {{ 变量 }}
	e.mu.RLock()
	for k, v := range e.extraVars {
		placeholder := fmt.Sprintf("{{ %s }}", k)
		if strings.Contains(hostsStr, placeholder) {
			hostsStr = strings.ReplaceAll(hostsStr, placeholder, fmt.Sprintf("%v", v))
		}
		// 也处理不带空格的情况
		placeholder2 := fmt.Sprintf("{{%s}}", k)
		if strings.Contains(hostsStr, placeholder2) {
			hostsStr = strings.ReplaceAll(hostsStr, placeholder2, fmt.Sprintf("%v", v))
		}
	}
	e.mu.RUnlock()

	hostsStr = strings.TrimSpace(hostsStr)

	hosts, err := e.inventory.MatchHosts(hostsStr)
	if err != nil {
		return nil, err
	}
	return e.applyHostLimit(hosts)
}

func (e *Executor) applyHostLimit(hosts []*inventory.Host) ([]*inventory.Host, error) {
	e.mu.RLock()
	patterns := append([]string{}, e.limitPatterns...)
	e.mu.RUnlock()

	if len(patterns) == 0 {
		return hosts, nil
	}
	limited, err := e.inventory.FilterHosts(hosts, strings.Join(patterns, ","))
	if err != nil {
		return nil, fmt.Errorf("no hosts matched limit %q", strings.Join(patterns, ","))
	}
	return limited, nil
}

func (e *Executor) loadPlayVarsFiles(play *Play, playVars map[string]interface{}) error {
	for _, rawEntry := range play.VarsFiles {
		resolvedEntry := strings.TrimSpace(e.resolveString(rawEntry, playVars))
		if resolvedEntry == "" {
			continue
		}

		candidates := strings.Split(resolvedEntry, ",")
		var loaded map[string]interface{}
		var lastErr error
		found := false

		for _, candidate := range candidates {
			candidate = strings.TrimSpace(candidate)
			if candidate == "" {
				continue
			}
			path := candidate
			if !filepath.IsAbs(path) && e.baseDir != "" {
				path = filepath.Join(e.baseDir, path)
			}
			if _, err := os.Stat(path); err != nil {
				lastErr = err
				continue
			}
			data, err := e.varManager.LoadVarsFile(path)
			if err != nil {
				return fmt.Errorf("load vars file %q: %w", candidate, err)
			}
			loaded = data
			found = true
			break
		}

		if !found {
			if len(candidates) == 1 {
				return fmt.Errorf("load vars file %q: %w", rawEntry, lastErr)
			}
			return fmt.Errorf("could not find any vars file in candidate list [%s]: %w", rawEntry, lastErr)
		}

		for k, v := range loaded {
			playVars[k] = v
		}
	}
	return nil
}

func (e *Executor) taskVarsForHost(host *inventory.Host, baseVars map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})

	// 1. Role Defaults (官方规范最低优先级底座变量，可被 Inventory 主机/组变量覆盖)
	e.mu.RLock()
	for key, value := range e.currentPlayDefaults {
		result[key] = value
	}
	e.mu.RUnlock()

	if host != nil && e.inventory != nil {
		for key, value := range e.inventory.ResolveHostVarsRaw(host) {
			result[key] = value
		}
		result["inventory_hostname"] = host.Name
		result["inventory_hostname_short"] = shortHostname(host.Name)
		result["ansible_host"] = host.Address
		result["ansible_port"] = host.Port
		if host.User != "" {
			result["ansible_user"] = host.User
		}
	}

	for key, value := range baseVars {
		result[key] = value
	}

	if host != nil {
		facts := e.getHostFacts(host.Name)
		for key, value := range facts {
			result[key] = value
		}
		result["inventory_hostname"] = host.Name
		result["inventory_hostname_short"] = shortHostname(host.Name)
	}

	result["groups"] = e.buildGroupsMap()
	if host != nil {
		result["group_names"] = e.buildGroupNamesForHost(host)
	} else {
		result["group_names"] = []string{}
	}
	allPlayHosts := e.getActivePlayHosts()
	activeUnfailedPlayHosts := e.filterActiveUnfailedHosts(allPlayHosts)
	batchHosts := e.getActiveBatchHosts()
	if len(batchHosts) == 0 {
		batchHosts = allPlayHosts
	}
	activeBatchHosts := e.filterActiveUnfailedHosts(batchHosts)

	result["play_hosts"] = activeUnfailedPlayHosts
	result["ansible_play_hosts_all"] = allPlayHosts
	result["ansible_play_hosts"] = activeUnfailedPlayHosts
	result["ansible_play_batch"] = activeBatchHosts

	e.mu.RLock()
	playName := e.currentPlayName
	verbosity := e.verbosityLevel
	e.mu.RUnlock()
	result["ansible_play_name"] = playName
	result["ansible_check_mode"] = e.isTaskCheckMode(nil)
	result["ansible_diff_mode"] = e.isTaskDiffMode(nil)
	result["ansible_forks"] = e.GetForks()
	result["ansible_verbosity"] = verbosity
	result["ansible_version"] = map[string]interface{}{
		"full":     "2.16.0",
		"major":    2,
		"minor":    16,
		"revision": 0,
		"string":   "2.16.0",
	}

	result["hostvars"] = e.buildHostVarsSnapshot(host, result)

	// 注入核心魔术变量
	if e.baseDir != "" {
		result["playbook_dir"] = e.baseDir
	} else if wd, err := os.Getwd(); err == nil {
		result["playbook_dir"] = wd
	}
	invFile := ""
	invDir := ""
	if e.inventory != nil && e.inventory.FilePath != "" {
		invFile = e.inventory.FilePath
		invDir = filepath.Dir(invFile)
	} else if pbDir, ok := result["playbook_dir"].(string); ok && pbDir != "" {
		invDir = pbDir
	}
	result["inventory_dir"] = invDir
	result["inventory_file"] = invFile
	result["omit"] = "__omit_place_holder__"

	return result
}

func (e *Executor) invalidateHostVarsCache(hostNames ...string) {
	e.hostVarsMu.Lock()
	defer e.hostVarsMu.Unlock()
	if e.baseHostVarsCache == nil {
		e.baseHostVarsCache = make(map[string]map[string]interface{})
	}
	if len(hostNames) == 0 {
		e.cachedHostVarsSnapshot = nil
		e.baseHostVarsCache = make(map[string]map[string]interface{})
		return
	}
	for _, h := range hostNames {
		delete(e.baseHostVarsCache, h)
		if e.cachedHostVarsSnapshot != nil {
			delete(e.cachedHostVarsSnapshot, h)
		}
	}
}

func (e *Executor) InvalidateHostVarsCache(hostNames ...string) {
	e.invalidateHostVarsCache(hostNames...)
}

func (e *Executor) invalidateGroupsCache() {
	e.groupsMu.Lock()
	e.groupsDirty = true
	e.cachedGroupsMap = nil
	e.cachedGroupNames = nil
	e.groupsMu.Unlock()
}

func (e *Executor) InvalidateGroupsCache() {
	e.invalidateGroupsCache()
}

func (e *Executor) ensureGroupsCache() {
	e.groupsMu.Lock()
	defer e.groupsMu.Unlock()
	if !e.groupsDirty && e.cachedGroupsMap != nil {
		return
	}

	groups := make(map[string][]string)
	groupNames := make(map[string][]string)

	if e.inventory == nil {
		groups["all"] = []string{}
		groups["ungrouped"] = []string{}
		e.cachedGroupsMap = groups
		e.cachedGroupNames = groupNames
		e.groupsDirty = false
		return
	}

	allHosts := e.inventory.GetAllHosts()
	allNames := make([]string, 0, len(allHosts))
	for _, h := range allHosts {
		allNames = append(allNames, h.Name)
		groupNames[h.Name] = make([]string, 0)
	}
	sort.Strings(allNames)
	groups["all"] = allNames

	grouped := make(map[string]bool)
	groupsSnapshot := e.inventory.GetGroupsSnapshot()
	for gName, g := range groupsSnapshot {
		if gName == "all" {
			continue
		}
		hMap := make(map[string]*inventory.Host)
		e.inventory.CollectGroupHosts(g, hMap)
		names := make([]string, 0, len(hMap))
		for name := range hMap {
			names = append(names, name)
			grouped[name] = true
			groupNames[name] = append(groupNames[name], gName)
		}
		sort.Strings(names)
		groups[gName] = names
	}

	for hName, gList := range groupNames {
		sort.Strings(gList)
		groupNames[hName] = gList
	}

	ungrouped := make([]string, 0)
	for _, name := range allNames {
		if !grouped[name] {
			ungrouped = append(ungrouped, name)
		}
	}
	sort.Strings(ungrouped)
	groups["ungrouped"] = ungrouped

	e.cachedGroupsMap = groups
	e.cachedGroupNames = groupNames
	e.groupsDirty = false
}

func (e *Executor) buildGroupsMap() map[string][]string {
	e.groupsMu.RLock()
	if !e.groupsDirty && e.cachedGroupsMap != nil {
		res := e.cachedGroupsMap
		e.groupsMu.RUnlock()
		return res
	}
	e.groupsMu.RUnlock()

	e.ensureGroupsCache()

	e.groupsMu.RLock()
	defer e.groupsMu.RUnlock()
	return e.cachedGroupsMap
}

func (e *Executor) buildGroupNamesForHost(host *inventory.Host) []string {
	if host == nil {
		return []string{}
	}
	e.groupsMu.RLock()
	if !e.groupsDirty && e.cachedGroupNames != nil {
		res := e.cachedGroupNames[host.Name]
		e.groupsMu.RUnlock()
		if res == nil {
			return []string{}
		}
		return res
	}
	e.groupsMu.RUnlock()

	e.ensureGroupsCache()

	e.groupsMu.RLock()
	defer e.groupsMu.RUnlock()
	res := e.cachedGroupNames[host.Name]
	if res == nil {
		return []string{}
	}
	return res
}

func (e *Executor) getBaseHostVarsLocked(h *inventory.Host) map[string]interface{} {
	if h == nil {
		return make(map[string]interface{})
	}
	if e.baseHostVarsCache != nil {
		if cached, ok := e.baseHostVarsCache[h.Name]; ok {
			return cached
		}
	} else {
		e.baseHostVarsCache = make(map[string]map[string]interface{})
	}

	hVars := make(map[string]interface{})
	if e.inventory != nil {
		for k, v := range e.inventory.ResolveHostVarsRaw(h) {
			hVars[k] = v
		}
	}
	hVars["inventory_hostname"] = h.Name
	hVars["inventory_hostname_short"] = shortHostname(h.Name)
	hVars["ansible_host"] = h.Address
	hVars["ansible_port"] = h.Port
	if h.User != "" {
		hVars["ansible_user"] = h.User
	}

	facts := e.getHostFacts(h.Name)
	for k, v := range facts {
		hVars[k] = v
	}

	e.baseHostVarsCache[h.Name] = hVars
	return hVars
}

func mergeCurrentHostVars(hostvars map[string]map[string]interface{}, currentHost *inventory.Host, currentVars map[string]interface{}) {
	if currentHost == nil || len(currentVars) == 0 {
		return
	}
	base := hostvars[currentHost.Name]
	cur := make(map[string]interface{}, len(base)+len(currentVars))
	for k, v := range base {
		cur[k] = v
	}
	for k, v := range currentVars {
		if k == "hostvars" {
			continue
		}
		cur[k] = v
	}
	hostvars[currentHost.Name] = cur
}

func (e *Executor) buildHostVarsSnapshot(currentHost *inventory.Host, currentVars map[string]interface{}) map[string]map[string]interface{} {
	if e.inventory == nil {
		return make(map[string]map[string]interface{})
	}

	hostCount := e.inventory.HostCount()

	// 快速无冲突并发读路径：若快照已构建且主机数一致，使用 RLock 共享读取（完全杜绝 GetAllHosts 全量排序与切片分配）
	e.hostVarsMu.RLock()
	if e.cachedHostVarsSnapshot != nil && len(e.cachedHostVarsSnapshot) == hostCount {
		hostvars := make(map[string]map[string]interface{}, len(e.cachedHostVarsSnapshot)+1)
		for k, v := range e.cachedHostVarsSnapshot {
			hostvars[k] = v
		}
		e.hostVarsMu.RUnlock()

		mergeCurrentHostVars(hostvars, currentHost, currentVars)
		return hostvars
	}
	e.hostVarsMu.RUnlock()

	allHosts := e.inventory.GetAllHosts()

	// 慢路径：排他锁重新构建快照
	e.hostVarsMu.Lock()
	if e.cachedHostVarsSnapshot == nil {
		snapshot := make(map[string]map[string]interface{}, len(allHosts))
		for _, h := range allHosts {
			snapshot[h.Name] = e.getBaseHostVarsLocked(h)
		}
		e.cachedHostVarsSnapshot = snapshot
	} else if len(e.cachedHostVarsSnapshot) != len(allHosts) {
		for _, h := range allHosts {
			if _, ok := e.cachedHostVarsSnapshot[h.Name]; !ok {
				e.cachedHostVarsSnapshot[h.Name] = e.getBaseHostVarsLocked(h)
			}
		}
	}

	hostvars := make(map[string]map[string]interface{}, len(e.cachedHostVarsSnapshot)+1)
	for k, v := range e.cachedHostVarsSnapshot {
		hostvars[k] = v
	}
	e.hostVarsMu.Unlock()

	mergeCurrentHostVars(hostvars, currentHost, currentVars)

	return hostvars
}

func (e *Executor) getActivePlayHosts() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.activePlayHosts) == 0 {
		return []string{}
	}
	res := make([]string, len(e.activePlayHosts))
	copy(res, e.activePlayHosts)
	return res
}

func (e *Executor) getActiveBatchHosts() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.activeBatchHosts) == 0 {
		return []string{}
	}
	res := make([]string, len(e.activeBatchHosts))
	copy(res, e.activeBatchHosts)
	return res
}

func (e *Executor) filterActiveUnfailedHosts(hosts []string) []string {
	if len(hosts) == 0 {
		return []string{}
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	res := make([]string, 0, len(hosts))
	for _, h := range hosts {
		if (e.failedHosts != nil && e.failedHosts[h]) || (e.endedHosts != nil && e.endedHosts[h]) {
			continue
		}
		res = append(res, h)
	}
	return res
}

func shortHostname(name string) string {
	if idx := strings.Index(name, "."); idx > 0 {
		return name[:idx]
	}
	return name
}
func effectiveGatherFacts(play *Play) bool {
	if play == nil {
		return false
	}
	if play.GatherFacts == nil {
		return false
	}
	return *play.GatherFacts
}

func (e *Executor) effectiveGatherFacts(play *Play) bool {
	if play == nil {
		return false
	}
	if play.GatherFacts != nil {
		return *play.GatherFacts
	}
	switch e.gathering {
	case "implicit", "smart":
		return true
	default:
		return false
	}
}

func parsePositiveInt(value string) int {
	var parsed int
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &parsed); err != nil {
		return 0
	}
	if parsed < 0 {
		return 0
	}
	return parsed
}

func (e *Executor) exceededMaxFailPercentage(result *PlayResult, totalHosts int, maxFailPct int) bool {
	if maxFailPct <= 0 || totalHosts <= 0 || result == nil {
		return false
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	failedHosts := 0
	for _, hostResult := range result.Hosts {
		if hostResult == nil {
			continue
		}
		for _, taskResult := range hostResult.GetTasks() {
			if (taskResult.Failed && !taskResult.Rescued) || (taskResult.Unreachable && !taskResult.Ignored) {
				failedHosts++
				break
			}
		}
	}

	return failedHosts*100 > maxFailPct*totalHosts
}

func GetString(params map[string]interface{}, key, fallback string) string {
	if val, ok := params[key]; ok {
		return fmt.Sprintf("%v", val)
	}
	return fallback
}

// GetInt 获取整数参数
func GetInt(params map[string]interface{}, key string, fallback int) int {
	if val, ok := params[key]; ok {
		switch v := val.(type) {
		case int:
			return v
		case int64:
			return int(v)
		case float64:
			return int(v)
		case string:
			if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				return i
			}
		}
	}
	return fallback
}

// getOrCreateClientWithErr 获取或创建客户端连接并返回错误详情
func (e *Executor) getOrCreateClientWithErr(host *inventory.Host) (ssh.Connection, error) {
	if e.pool == nil {
		return nil, fmt.Errorf("connection pool is not initialized")
	}
	client, err := e.pool.Get(host)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", host.Name, err)
	}
	return client, nil
}

// getOrCreateClient 获取或创建客户端连接
func (e *Executor) getOrCreateClient(host *inventory.Host) ssh.Connection {
	client, _ := e.getOrCreateClientWithErr(host)
	return client
}

func (e *Executor) getHostResult(playResult *PlayResult, hostName string) *HostResult {
	if playResult == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
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
	Start time.Time
	End   time.Time
}

// PlayResult Play 执行结果
type PlayResult struct {
	Name  string
	Hosts map[string]*HostResult
}

// HostResult 主机执行结果
type HostResult struct {
	mu    sync.RWMutex
	Tasks []*TaskResult
}

// AppendTask 线程安全追加单个任务执行结果
func (hr *HostResult) AppendTask(tr *TaskResult) {
	if hr == nil || tr == nil {
		return
	}
	hr.mu.Lock()
	hr.Tasks = append(hr.Tasks, tr)
	hr.mu.Unlock()
}

// AppendTasks 线程安全追加多个任务执行结果
func (hr *HostResult) AppendTasks(trs ...*TaskResult) {
	if hr == nil || len(trs) == 0 {
		return
	}
	hr.mu.Lock()
	hr.Tasks = append(hr.Tasks, trs...)
	hr.mu.Unlock()
}

// GetTasks 线程安全获取任务执行结果快照
func (hr *HostResult) GetTasks() []*TaskResult {
	if hr == nil {
		return nil
	}
	hr.mu.RLock()
	defer hr.mu.RUnlock()
	res := make([]*TaskResult, len(hr.Tasks))
	copy(res, hr.Tasks)
	return res
}

// TaskCount 线程安全获取已执行任务数量
func (hr *HostResult) TaskCount() int {
	if hr == nil {
		return 0
	}
	hr.mu.RLock()
	defer hr.mu.RUnlock()
	return len(hr.Tasks)
}

// MarkRescued 线程安全标记指定索引范围内的失败任务为已救援 (Rescued = true)
func (hr *HostResult) MarkRescued(startIdx int, endIdx ...int) {
	if hr == nil {
		return
	}
	hr.mu.Lock()
	defer hr.mu.Unlock()
	if startIdx < 0 {
		startIdx = 0
	}
	limit := len(hr.Tasks)
	if len(endIdx) > 0 && endIdx[0] >= 0 && endIdx[0] < limit {
		limit = endIdx[0]
	}
	for i := startIdx; i < limit; i++ {
		if hr.Tasks[i].Failed {
			hr.Tasks[i].Rescued = true
		}
	}
}

// HasFailure 线程安全检查指定起始索引之后是否有未被救援的失败任务
func (hr *HostResult) HasFailure(start int) bool {
	if hr == nil {
		return false
	}
	hr.mu.RLock()
	defer hr.mu.RUnlock()
	if start < 0 {
		start = 0
	}
	for i := start; i < len(hr.Tasks); i++ {
		if hr.Tasks[i].Failed && !hr.Tasks[i].Rescued {
			return true
		}
	}
	return false
}

// TaskResult 任务执行结果
type TaskResult struct {
	Name        string
	Host        string
	Changed     bool
	Failed      bool
	Skipped     bool
	Unreachable bool
	Ignored     bool
	Rescued     bool
	NoLog       bool
	Stdout      string
	Stderr      string
	ExitCode    int
	Message     string
	Diff        string
	Data        map[string]interface{}
	Item        interface{} // 循环时的 item 值
	Start       time.Time
	End         time.Time
}
