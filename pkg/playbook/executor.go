package playbook

import (
	"bufio"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/module"
	"go-ansible/pkg/ssh"
	"go-ansible/pkg/util"
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
	throttleSems          map[string]chan struct{}
	throttleMu            sync.Mutex
	factCaching           string
	factCachingConnection string
	factCachingTimeout    int
	factCacheTimestamps   map[string]time.Time
	mu                    sync.RWMutex

	Stdin               io.Reader
	Stdout              io.Writer
	currentPlayDebugger string
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

func (e *Executor) getTaskThrottleSem(task *Task) chan struct{} {
	if task == nil || task.Throttle <= 0 {
		return nil
	}
	key := fmt.Sprintf("%p_%d", task, task.Throttle)
	e.throttleMu.Lock()
	defer e.throttleMu.Unlock()
	if e.throttleSems == nil {
		e.throttleSems = make(map[string]chan struct{})
	}
	if sem, ok := e.throttleSems[key]; ok {
		return sem
	}
	sem := make(chan struct{}, task.Throttle)
	e.throttleSems[key] = sem
	return sem
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

func (e *Executor) SetFactCaching(plugin, connection string, timeout int) {
	e.factCaching = strings.ToLower(strings.TrimSpace(plugin))
	e.factCachingConnection = strings.TrimSpace(connection)
	e.factCachingTimeout = timeout
}

func (e *Executor) FlushCache() {
	e.mu.Lock()
	e.facts = make(map[string]map[string]interface{})
	e.factCacheTimestamps = make(map[string]time.Time)
	e.mu.Unlock()

	if e.factCaching == "jsonfile" && e.factCachingConnection != "" {
		entries, err := os.ReadDir(e.factCachingConnection)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
					_ = os.Remove(filepath.Join(e.factCachingConnection, entry.Name()))
				}
			}
		}
	}
}

func (e *Executor) getCachedFacts(hostName string) (map[string]interface{}, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	switch e.factCaching {
	case "memory":
		if cached, ok := e.facts[hostName]; ok && cached != nil {
			if e.factCachingTimeout > 0 {
				if ts, ok := e.factCacheTimestamps[hostName]; ok {
					if time.Since(ts) > time.Duration(e.factCachingTimeout)*time.Second {
						return nil, false
					}
				}
			}
			return cached, true
		}
	case "jsonfile":
		if e.factCachingConnection == "" {
			return nil, false
		}
		cacheFile := filepath.Join(e.factCachingConnection, hostName+".json")
		info, err := os.Stat(cacheFile)
		if err != nil {
			return nil, false
		}
		if e.factCachingTimeout > 0 && time.Since(info.ModTime()) > time.Duration(e.factCachingTimeout)*time.Second {
			return nil, false
		}
		data, err := os.ReadFile(cacheFile)
		if err != nil {
			return nil, false
		}
		var facts map[string]interface{}
		if err := json.Unmarshal(data, &facts); err == nil && facts != nil {
			return facts, true
		}
	}
	return nil, false
}

func (e *Executor) saveCachedFacts(hostName string, facts map[string]interface{}) {
	e.mu.Lock()
	e.facts[hostName] = facts
	if e.factCacheTimestamps == nil {
		e.factCacheTimestamps = make(map[string]time.Time)
	}
	e.factCacheTimestamps[hostName] = time.Now()
	e.mu.Unlock()

	if e.factCaching == "jsonfile" && e.factCachingConnection != "" {
		_ = os.MkdirAll(e.factCachingConnection, 0755)
		cacheFile := filepath.Join(e.factCachingConnection, hostName+".json")
		data, err := json.MarshalIndent(facts, "", "  ")
		if err == nil {
			_ = os.WriteFile(cacheFile, data, 0644)
		}
	}
}

func (e *Executor) getHostFacts(hostName string) map[string]interface{} {
	e.mu.RLock()
	facts, ok := e.facts[hostName]
	e.mu.RUnlock()
	if ok && facts != nil {
		return facts
	}
	if cached, found := e.getCachedFacts(hostName); found {
		e.mu.Lock()
		if e.facts[hostName] == nil {
			e.facts[hostName] = make(map[string]interface{})
		}
		for k, v := range cached {
			e.facts[hostName][k] = v
		}
		facts = e.facts[hostName]
		e.mu.Unlock()
		return facts
	}
	return nil
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
	if e.inventory != nil {
		e.inventory.ApplyConnectionVars(vars)
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
	defer func() {
		e.currentResult = nil
		e.endedHosts = nil
		e.failedHosts = nil
		e.endPlay = false
		e.currentPlayDebugger = ""
	}()
	var playErr error

	hosts, err := e.getPlayHosts(play)
	if err != nil {
		return result, err
	}
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

	// 应用 play 级别的 become 配置到所有主机
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

	roleTasks, roleHandlers, roleVars, err := e.loadPlayRoles(play)
	if err != nil {
		return result, err
	}
	for k, v := range roleVars {
		if _, exists := playVars[k]; !exists {
			playVars[k] = v
		}
	}

	if play.RemoteUser != "" {
		for _, h := range hosts {
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
	mergePlayTags(play.Tags, play.PreTasks)
	mergePlayTags(play.Tags, effectiveRoleTasks(roleTasks))
	mergePlayTags(play.Tags, play.Tasks)
	mergePlayTags(play.Tags, play.PostTasks)

	taskRunOnceExecuted := make(map[string]bool)
	hostBatches := e.buildHostBatches(hosts, play.Serial)

	// 未显式声明 gather_facts 时，默认不收集。
	gatherFacts := e.effectiveGatherFacts(play)

	// 收集 facts
	if gatherFacts {
		fmt.Printf("Gathering facts for %d host(s)...\n", len(hosts))
		e.gatherFacts(hosts)
	}

	effectiveTasks := make([]*Task, 0, len(roleTasks)+len(play.Tasks))
	effectiveTasks = append(effectiveTasks, roleTasks...)
	effectiveTasks = append(effectiveTasks, play.Tasks...)

	for batchIndex, batchHosts := range hostBatches {
		e.log("Executing host batch %d/%d", batchIndex+1, len(hostBatches))

		failedBeforeBatch := e.failedTaskCount(result)
		if err := e.executeTaskList(hosts, batchHosts, play.PreTasks, playVars, handlers, taskRunOnceExecuted, play.Environment, result, play.AnyErrorsFatal, play.Strategy); err != nil {
			playErr = err
			break
		}
		if e.shouldEndPlay() {
			break
		}
		if play.AnyErrorsFatal && e.failedTaskCount(result) > failedBeforeBatch {
			playErr = fmt.Errorf("any_errors_fatal triggered")
			break
		}
		failedBeforeBatch = e.failedTaskCount(result)
		if err := e.executeTaskList(hosts, batchHosts, effectiveTasks, playVars, handlers, taskRunOnceExecuted, play.Environment, result, play.AnyErrorsFatal, play.Strategy); err != nil {
			playErr = err
			break
		}
		if e.shouldEndPlay() {
			break
		}
		if play.AnyErrorsFatal && e.failedTaskCount(result) > failedBeforeBatch {
			playErr = fmt.Errorf("any_errors_fatal triggered")
			break
		}
		failedBeforeBatch = e.failedTaskCount(result)
		if err := e.executeTaskList(hosts, batchHosts, play.PostTasks, playVars, handlers, taskRunOnceExecuted, play.Environment, result, play.AnyErrorsFatal, play.Strategy); err != nil {
			playErr = err
			break
		}
		if e.shouldEndPlay() {
			break
		}
		if play.AnyErrorsFatal && e.failedTaskCount(result) > failedBeforeBatch {
			playErr = fmt.Errorf("any_errors_fatal triggered")
			break
		}

		if play.MaxFailPct > 0 && e.exceededMaxFailPercentage(result, len(hosts), play.MaxFailPct) {
			playErr = fmt.Errorf("max_fail_percentage exceeded")
			break
		}
	}

	if len(e.pendingHandlers) > 0 && (playErr == nil || play.ForceHandlers || e.forceHandlers) {
		if err := e.flushHandlers(result); err != nil {
			playErr = err
		}
	}

	return result, playErr
}

func (e *Executor) playHasFailures(result *PlayResult) bool {
	if result == nil {
		return false
	}
	for _, hostResult := range result.Hosts {
		for _, taskResult := range hostResult.Tasks {
			if taskResult.Failed {
				return true
			}
		}
	}
	return false
}

func (e *Executor) failedTaskCount(result *PlayResult) int {
	if result == nil {
		return 0
	}
	count := 0
	for _, hostResult := range result.Hosts {
		for _, taskResult := range hostResult.Tasks {
			if taskResult.Failed {
				count++
			}
		}
	}
	return count
}

func (e *Executor) flushHandlers(result *PlayResult) error {
	if result == nil || len(e.pendingHandlers) == 0 {
		return nil
	}

	executedHandlers := make(map[string]bool)
	pending := e.pendingHandlers
	e.pendingHandlers = make([]pendingHandler, 0)

	for _, ph := range pending {
		handlerKey := fmt.Sprintf("%s-%s", ph.handler.Name, ph.host.Name)
		if executedHandlers[handlerKey] {
			continue
		}
		executedHandlers[handlerKey] = true

		hostResult := e.getHostResult(result, ph.host.Name)
		e.log("Executing handler: %s on host: %s", ph.handler.Name, ph.host.Name)

		if ph.handler.Loop != nil {
			items := e.evaluateLoop(ph.handler.Loop, ph.vars)
			for index, item := range items {
				resolvedItem := e.resolveValue(item, ph.vars)

				loopVars := make(map[string]interface{})
				for k, v := range ph.vars {
					loopVars[k] = v
				}
				loopVars["item"] = resolvedItem
				ansibleLoop := map[string]interface{}{
					"index":     index + 1,
					"index0":    index,
					"first":     index == 0,
					"last":      index == len(items)-1,
					"length":    len(items),
					"revindex":  len(items) - index,
					"revindex0": len(items) - index - 1,
				}
				loopVars["ansible_loop"] = ansibleLoop

				params := e.resolveVariables(ph.handler.Params, loopVars)
				e.log("Handler params with item: %+v", params)

				handlerHost := e.handlerExecutionHost(ph.host, ph.handler)
				e.applyBecomeParams(params, handlerHost)

				params = e.withEnvironmentParams(params, ph.env, ph.handler.ModuleName)
				modResult, err := e.executeHandlerModule(&handlerHost, ph.handler.ModuleName, params, ph.handler.Name, loopVars)

				taskResult := &TaskResult{
					Name:  ph.handler.Name,
					Host:  ph.host.Name,
					Item:  resolvedItem,
					Start: time.Now(),
				}

				if err != nil {
					taskResult.Failed = true
					taskResult.Message = err.Error()
				}

				if modResult != nil {
					taskResult.Changed = modResult.Changed
					taskResult.Stdout = modResult.Stdout
					taskResult.Stderr = modResult.Stderr
					taskResult.ExitCode = modResult.ExitCode
					taskResult.Diff = modResult.Diff
					taskResult.Data = modResult.Data
					if modResult.Message != "" {
						taskResult.Message = modResult.Message
					}
				}

				taskResult.End = time.Now()
				hostResult.Tasks = append(hostResult.Tasks, taskResult)
			}
			continue
		}

		handler := &Task{
			Name:       ph.handler.Name,
			ModuleName: ph.handler.ModuleName,
			Params:     ph.handler.Params,
		}

		params := e.resolveVariables(handler.Params, ph.vars)
		e.log("Handler params: %+v", params)

		handlerHost := e.handlerExecutionHost(ph.host, ph.handler)
		e.applyBecomeParams(params, handlerHost)

		params = e.withEnvironmentParams(params, ph.env, handler.ModuleName)
		modResult, err := e.executeHandlerModule(&handlerHost, handler.ModuleName, params, handler.Name, ph.vars)

		taskResult := &TaskResult{
			Name:  handler.Name,
			Host:  ph.host.Name,
			Start: time.Now(),
		}

		if err != nil {
			taskResult.Failed = true
			taskResult.Message = err.Error()
		}

		if modResult != nil {
			taskResult.Changed = modResult.Changed
			taskResult.Stdout = modResult.Stdout
			taskResult.Stderr = modResult.Stderr
			taskResult.ExitCode = modResult.ExitCode
			taskResult.Diff = modResult.Diff
			taskResult.Data = modResult.Data
			if modResult.Message != "" {
				taskResult.Message = modResult.Message
			}
		}

		taskResult.End = time.Now()
		hostResult.Tasks = append(hostResult.Tasks, taskResult)
	}

	return nil
}

// getPlayHosts 获取 play 的目标主机
func (e *Executor) getPlayHosts(play *Play) ([]*inventory.Host, error) {
	// 先解析 hosts 字段中的变量
	hostsStr := play.Hosts

	// 使用 extra vars 替换 {{ 变量 }}
	e.mu.RLock()
	for k, v := range e.extraVars {
		placeholder := fmt.Sprintf("{{ %s }}", k)
		if contains(hostsStr, placeholder) {
			hostsStr = replaceAll(hostsStr, placeholder, fmt.Sprintf("%v", v))
		}
		// 也处理不带空格的情况
		placeholder2 := fmt.Sprintf("{{%s}}", k)
		if contains(hostsStr, placeholder2) {
			hostsStr = replaceAll(hostsStr, placeholder2, fmt.Sprintf("%v", v))
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

// getMapKeys 获取 map 的键列表
func (e *Executor) executeTaskForHost(hosts []*inventory.Host, host *inventory.Host, task *Task, vars map[string]interface{}, handlers []*Handler, runOnce map[string]bool) ([]*TaskResult, error) {
	if !task.RunOnce {
		return e.executeTask(host, task, vars, handlers)
	}

	key := e.taskExecutionKey(task)
	e.mu.Lock()
	if runOnce[key] {
		e.mu.Unlock()
		return []*TaskResult{{
			Name:    e.resolveString(task.Name, vars),
			Host:    host.Name,
			Skipped: true,
			Message: "skipped due to run_once",
			Start:   time.Now(),
			End:     time.Now(),
		}}, nil
	}

	if len(hosts) > 0 && hosts[0].Name != host.Name {
		e.mu.Unlock()
		return []*TaskResult{{
			Name:    e.resolveString(task.Name, vars),
			Host:    host.Name,
			Skipped: true,
			Message: "skipped due to run_once",
			Start:   time.Now(),
			End:     time.Now(),
		}}, nil
	}

	runOnce[key] = true
	e.mu.Unlock()
	return e.executeTask(host, task, vars, handlers)
}

func (e *Executor) executeTaskList(allHosts []*inventory.Host, batchHosts []*inventory.Host, tasks []*Task, playVars map[string]interface{}, handlers []*Handler, runOnce map[string]bool, playEnv map[string]string, result *PlayResult, anyErrorsFatal bool, strategy string) error {
	if len(tasks) == 0 {
		return nil
	}

	forks := e.GetForks()
	if forks <= 0 {
		forks = 5
	}

	if strategy == "free" {
		return e.executeTasksFreeStrategy(allHosts, batchHosts, tasks, playVars, handlers, runOnce, playEnv, result, anyErrorsFatal, forks)
	}

	for _, task := range tasks {
		if e.shouldEndPlay() {
			return nil
		}

		// Filter active hosts in the current batch for this task
		var activeHosts []*inventory.Host
		for _, host := range batchHosts {
			if !e.isHostEnded(host.Name) && !e.isHostFailed(host.Name) {
				activeHosts = append(activeHosts, host)
			}
		}
		if len(activeHosts) == 0 {
			break
		}

		if e.stepMode {
			taskName := task.Name
			if taskName == "" {
				taskName = task.ModuleName
			}
			promptFn := e.stepPromptFunc
			if promptFn == nil {
				promptFn = defaultStepPrompt
			}
			ans, err := promptFn(taskName)
			if err != nil {
				ans = "n"
			}
			ans = strings.ToLower(strings.TrimSpace(ans))
			if ans == "c" || ans == "continue" {
				e.stepMode = false
			} else if ans != "y" && ans != "yes" {
				for _, h := range activeHosts {
					hr := e.getHostResult(result, h.Name)
					hr.Tasks = append(hr.Tasks, &TaskResult{
						Name:    taskName,
						Host:    h.Name,
						Skipped: true,
						Message: "skipped due to --step",
						Start:   time.Now(),
						End:     time.Now(),
					})
				}
				continue
			}
		}

		failedBeforeTask := e.failedTaskCount(result)
		if err := e.executeTaskForBatchHosts(allHosts, activeHosts, task, playVars, handlers, runOnce, playEnv, result, forks); err != nil {
			return err
		}

		if e.shouldEndPlay() {
			return nil
		}

		if anyErrorsFatal && e.failedTaskCount(result) > failedBeforeTask {
			return fmt.Errorf("any_errors_fatal triggered")
		}
	}

	return nil
}

func (e *Executor) executeTaskForBatchHosts(allHosts []*inventory.Host, activeHosts []*inventory.Host, task *Task, playVars map[string]interface{}, handlers []*Handler, runOnce map[string]bool, playEnv map[string]string, result *PlayResult, forks int) error {
	effectiveForks := forks
	if task.Throttle > 0 && task.Throttle < effectiveForks {
		effectiveForks = task.Throttle
	}

	if task.ModuleName == "meta" || len(activeHosts) == 1 || effectiveForks == 1 {
		for _, host := range activeHosts {
			if e.shouldEndPlay() || e.isHostEnded(host.Name) || e.isHostFailed(host.Name) {
				continue
			}
			hostResult := e.getHostResult(result, host.Name)
			before := len(hostResult.Tasks)
			hostVars := e.taskVarsForHost(host, playVars)
			if err := e.executeTaskEntryForHost(allHosts, host, task, hostVars, handlers, runOnce, playEnv, hostResult); err != nil {
				return err
			}
			if hostResultHasFailure(hostResult, before) {
				e.markHostFailed(host.Name)
			}
		}
		return nil
	}

	sem := make(chan struct{}, effectiveForks)
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex

	for _, host := range activeHosts {
		if e.shouldEndPlay() || e.isHostEnded(host.Name) || e.isHostFailed(host.Name) {
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(h *inventory.Host) {
			defer wg.Done()
			defer func() { <-sem }()

			if e.shouldEndPlay() || e.isHostEnded(h.Name) || e.isHostFailed(h.Name) {
				return
			}

			hostResult := e.getHostResult(result, h.Name)
			before := len(hostResult.Tasks)
			hostVars := e.taskVarsForHost(h, playVars)

			if err := e.executeTaskEntryForHost(allHosts, h, task, hostVars, handlers, runOnce, playEnv, hostResult); err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
				return
			}
			if hostResultHasFailure(hostResult, before) {
				e.markHostFailed(h.Name)
			}
		}(host)
	}

	wg.Wait()
	return firstErr
}

func (e *Executor) executeTasksFreeStrategy(allHosts []*inventory.Host, batchHosts []*inventory.Host, tasks []*Task, playVars map[string]interface{}, handlers []*Handler, runOnce map[string]bool, playEnv map[string]string, result *PlayResult, anyErrorsFatal bool, forks int) error {
	if len(batchHosts) == 1 || forks == 1 {
		for _, host := range batchHosts {
			if e.shouldEndPlay() || e.isHostEnded(host.Name) || e.isHostFailed(host.Name) {
				continue
			}
			hostResult := e.getHostResult(result, host.Name)
			for _, task := range tasks {
				if e.shouldEndPlay() || e.isHostEnded(host.Name) || e.isHostFailed(host.Name) {
					break
				}
				before := len(hostResult.Tasks)
				hostVars := e.taskVarsForHost(host, playVars)
				if err := e.executeTaskEntryForHost(allHosts, host, task, hostVars, handlers, runOnce, playEnv, hostResult); err != nil {
					return err
				}
				if hostResultHasFailure(hostResult, before) {
					e.markHostFailed(host.Name)
					break
				}
			}
		}
		return nil
	}

	sem := make(chan struct{}, forks)
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex

	for _, host := range batchHosts {
		if e.shouldEndPlay() || e.isHostEnded(host.Name) || e.isHostFailed(host.Name) {
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(h *inventory.Host) {
			defer wg.Done()
			defer func() { <-sem }()

			hostResult := e.getHostResult(result, h.Name)
			for _, task := range tasks {
				if e.shouldEndPlay() || e.isHostEnded(h.Name) || e.isHostFailed(h.Name) {
					break
				}
				execSingleTask := func() error {
					if tSem := e.getTaskThrottleSem(task); tSem != nil {
						tSem <- struct{}{}
						defer func() { <-tSem }()
					}
					before := len(hostResult.Tasks)
					hostVars := e.taskVarsForHost(h, playVars)
					if err := e.executeTaskEntryForHost(allHosts, h, task, hostVars, handlers, runOnce, playEnv, hostResult); err != nil {
						return err
					}
					if hostResultHasFailure(hostResult, before) {
						e.markHostFailed(h.Name)
					}
					return nil
				}
				if err := execSingleTask(); err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errMu.Unlock()
					return
				}
				if e.isHostFailed(h.Name) {
					break
				}
			}
		}(host)
	}

	wg.Wait()
	return firstErr
}

func (e *Executor) shouldEndPlay() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.endPlay
}

func (e *Executor) markEndPlay() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.endPlay = true
}

func (e *Executor) isHostEnded(hostName string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.endedHosts != nil && e.endedHosts[hostName]
}

func (e *Executor) markHostEnded(hostName string) {
	if hostName == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.endedHosts == nil {
		e.endedHosts = make(map[string]bool)
	}
	e.endedHosts[hostName] = true
}

func (e *Executor) isHostFailed(hostName string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.failedHosts != nil && e.failedHosts[hostName]
}

func (e *Executor) markHostFailed(hostName string) {
	if hostName == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.failedHosts == nil {
		e.failedHosts = make(map[string]bool)
	}
	e.failedHosts[hostName] = true
}

func (e *Executor) clearHostFailed(hostName string) {
	if hostName == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.failedHosts != nil {
		delete(e.failedHosts, hostName)
	}
}

func (e *Executor) executeTaskEntryForHost(allHosts []*inventory.Host, host *inventory.Host, task *Task, vars map[string]interface{}, handlers []*Handler, runOnce map[string]bool, playEnv map[string]string, hostResult *HostResult) error {
	if hostResult == nil || task == nil {
		return nil
	}

	if !e.shouldRunTaskByStartAtTask(host, task, vars) {
		hostResult.Tasks = append(hostResult.Tasks, &TaskResult{
			Name:    e.resolveString(task.Name, vars),
			Host:    host.Name,
			Skipped: true,
			Message: "skipped before start_at_task",
			Start:   time.Now(),
			End:     time.Now(),
		})
		return nil
	}

	switch {
	case isIncludeRoleTask(task):
		return e.executeIncludedRoleForHost(allHosts, host, task, vars, handlers, runOnce, playEnv, hostResult)
	case isIncludeTask(task):
		return e.executeIncludedTasksForHost(allHosts, host, task, vars, handlers, runOnce, playEnv, hostResult)
	case isBlockTask(task):
		return e.executeBlockTaskForHost(allHosts, host, task, vars, handlers, runOnce, playEnv, hostResult)
	default:
		fmt.Printf("Running task [%s] on [%s]\n", e.resolveString(task.Name, vars), host.Name)
		taskResults, err := e.executeTaskForHostWithEnv(allHosts, host, task, vars, handlers, runOnce, playEnv)
		if len(taskResults) > 0 {
			hostResult.Tasks = append(hostResult.Tasks, taskResults...)
		}
		if err != nil {
			if task.IgnoreError || (task.IgnoreUnreachable && isUnreachableError(err)) {
				return nil
			}
			return err
		}
		return nil
	}
}

func isIncludeRoleTask(task *Task) bool {
	if task == nil {
		return false
	}
	switch task.ModuleName {
	case "include_role", "import_role":
		return true
	default:
		return false
	}
}

func (e *Executor) executeIncludedRoleForHost(allHosts []*inventory.Host, host *inventory.Host, task *Task, vars map[string]interface{}, handlers []*Handler, runOnce map[string]bool, playEnv map[string]string, hostResult *HostResult) error {
	if hostResult == nil || task == nil {
		return nil
	}

	if task.When != nil && !e.evaluateWhen(task.When, vars) {
		hostResult.Tasks = append(hostResult.Tasks, &TaskResult{
			Name:    e.resolveString(task.Name, vars),
			Host:    host.Name,
			Skipped: true,
			Message: "skipped due to when condition",
			Start:   time.Now(),
			End:     time.Now(),
		})
		return nil
	}

	roleName := GetString(task.Params, "name", "")
	if roleName == "" {
		roleName = GetString(task.Params, "_raw_params", "")
	}
	roleName = strings.TrimSpace(e.resolveString(roleName, vars))
	if roleName == "" {
		return fmt.Errorf("%s requires role name", task.ModuleName)
	}

	rolePath, err := e.resolveRolePath(roleName)
	if err != nil {
		return err
	}
	parser := NewParser(e.baseDir)
	role, err := parser.ParseRole(rolePath)
	if err != nil {
		return fmt.Errorf("parse included role %q: %w", roleName, err)
	}

	childVars := make(map[string]interface{})
	for k, v := range role.Defaults {
		childVars[k] = v
	}
	for k, v := range vars {
		childVars[k] = v
	}
	for k, v := range role.Vars {
		childVars[k] = v
	}
	if task.Vars != nil {
		for k, v := range task.Vars {
			childVars[k] = e.resolveValue(v, vars)
		}
	}

	if len(role.Handlers) > 0 {
		handlers = append(handlers, role.Handlers...)
	}

	roleTasks := cloneTasks(role.Tasks)
	applyParentTaskAttributes(task, roleTasks)

	for _, rt := range roleTasks {
		taskVars := e.taskVarsForHost(host, childVars)
		if err := e.executeTaskEntryForHost(allHosts, host, rt, taskVars, handlers, runOnce, playEnv, hostResult); err != nil {
			return err
		}
	}

	return nil
}

func isIncludeTask(task *Task) bool {
	if task == nil {
		return false
	}
	switch task.ModuleName {
	case "include_tasks", "import_tasks":
		return true
	default:
		return false
	}
}

func isBlockTask(task *Task) bool {
	if task == nil {
		return false
	}
	return len(task.Block) > 0 || len(task.Rescue) > 0 || len(task.Always) > 0
}

func (e *Executor) shouldRunTaskByStartAtTask(host *inventory.Host, task *Task, vars map[string]interface{}) bool {
	taskName := strings.TrimSpace(e.resolveString(task.Name, vars))
	containerTask := isBlockTask(task) || isIncludeTask(task) || isIncludeRoleTask(task)

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.startAtTask == "" {
		return true
	}

	hostName := ""
	if host != nil {
		hostName = host.Name
	}
	if e.startedTasks[hostName] {
		return true
	}

	if taskName == e.startAtTask {
		e.startedTasks[hostName] = true
		return true
	}

	return containerTask
}

func (e *Executor) executeIncludedTasksForHost(allHosts []*inventory.Host, host *inventory.Host, task *Task, vars map[string]interface{}, handlers []*Handler, runOnce map[string]bool, playEnv map[string]string, hostResult *HostResult) error {
	if hostResult == nil {
		return nil
	}

	if !e.shouldRunTaskByTags(task.Tags) {
		hostResult.Tasks = append(hostResult.Tasks, &TaskResult{
			Name:    e.resolveString(task.Name, vars),
			Host:    host.Name,
			Skipped: true,
			Message: "skipped due to tags filter",
			Start:   time.Now(),
			End:     time.Now(),
		})
		return nil
	}

	if task.When != nil && !e.evaluateWhen(task.When, vars) {
		hostResult.Tasks = append(hostResult.Tasks, &TaskResult{
			Name:    e.resolveString(task.Name, vars),
			Host:    host.Name,
			Skipped: true,
			Message: "skipped due to when condition",
			Start:   time.Now(),
			End:     time.Now(),
		})
		return nil
	}

	includedTasks, err := e.loadIncludedTasks(task, vars)
	if err != nil {
		return err
	}

	for _, includedTask := range includedTasks {
		childVars := e.taskVarsForHost(host, vars)
		if err := e.executeTaskEntryForHost(allHosts, host, includedTask, childVars, handlers, runOnce, playEnv, hostResult); err != nil {
			return err
		}
	}

	return nil
}

func (e *Executor) executeBlockTaskForHost(allHosts []*inventory.Host, host *inventory.Host, task *Task, vars map[string]interface{}, handlers []*Handler, runOnce map[string]bool, playEnv map[string]string, hostResult *HostResult) error {
	if hostResult == nil {
		return nil
	}

	blockTasks := cloneTasks(task.Block)
	applyParentTaskAttributes(task, blockTasks)

	beforeBlock := len(hostResult.Tasks)
	for _, blockTask := range blockTasks {
		childVars := e.taskVarsForHost(host, vars)
		if err := e.executeTaskEntryForHost(allHosts, host, blockTask, childVars, handlers, runOnce, playEnv, hostResult); err != nil {
			return err
		}
		if hostResultHasFailure(hostResult, beforeBlock) {
			break
		}
	}

	blockFailed := hostResultHasNewFailure(hostResult, beforeBlock)
	if blockFailed && len(task.Rescue) > 0 {
		rescueTasks := cloneTasks(task.Rescue)
		applyParentTaskAttributes(task, rescueTasks)
		beforeRescue := len(hostResult.Tasks)
		if host != nil {
			e.clearHostFailed(host.Name)
		}

		// 提取被救援的失败任务及详细结果
		var failedTaskName string
		failedResult := map[string]interface{}{
			"failed": true,
		}
		for i := beforeBlock; i < len(hostResult.Tasks); i++ {
			tr := hostResult.Tasks[i]
			if tr.Failed {
				failedTaskName = tr.Name
				failedResult["msg"] = tr.Message
				failedResult["stdout"] = tr.Stdout
				failedResult["stderr"] = tr.Stderr
				failedResult["rc"] = tr.ExitCode
				failedResult["changed"] = tr.Changed
				break
			}
		}

		rescueVars := make(map[string]interface{})
		for k, v := range vars {
			rescueVars[k] = v
		}
		failedTaskMap := map[string]interface{}{
			"name": failedTaskName,
		}
		rescueVars["ansible_failed_task"] = failedTaskMap
		rescueVars["ansible_failed_result"] = failedResult

		if host != nil {
			e.mu.Lock()
			if e.facts[host.Name] == nil {
				e.facts[host.Name] = make(map[string]interface{})
			}
			e.facts[host.Name]["ansible_failed_task"] = failedTaskMap
			e.facts[host.Name]["ansible_failed_result"] = failedResult
			e.mu.Unlock()
		}

		for _, rescueTask := range rescueTasks {
			childVars := e.taskVarsForHost(host, rescueVars)
			if err := e.executeTaskEntryForHost(allHosts, host, rescueTask, childVars, handlers, runOnce, playEnv, hostResult); err != nil {
				return err
			}
		}
		if host != nil && !hostResultHasNewFailure(hostResult, beforeRescue) {
			e.clearHostFailed(host.Name)
		}
	}

	if len(task.Always) > 0 {
		alwaysTasks := cloneTasks(task.Always)
		applyParentTaskAttributes(task, alwaysTasks)
		for _, alwaysTask := range alwaysTasks {
			childVars := e.taskVarsForHost(host, vars)
			if err := e.executeTaskEntryForHost(allHosts, host, alwaysTask, childVars, handlers, runOnce, playEnv, hostResult); err != nil {
				return err
			}
		}
	}

	return nil
}

func (e *Executor) loadIncludedTasks(task *Task, vars map[string]interface{}) ([]*Task, error) {
	includePath := e.resolveIncludePath(task, vars)
	if includePath == "" {
		return nil, fmt.Errorf("%s requires file path", task.ModuleName)
	}

	parser := NewParser(e.baseDir)
	includedTasks, err := parser.ResolveInclude(includePath)
	if err != nil {
		return nil, fmt.Errorf("resolve %s %q: %w", task.ModuleName, includePath, err)
	}

	applyParentTaskAttributes(task, includedTasks)
	return includedTasks, nil
}

func (e *Executor) resolveIncludePath(task *Task, vars map[string]interface{}) string {
	if task == nil {
		return ""
	}

	if file := GetString(task.Params, "file", ""); file != "" {
		return e.resolveString(file, vars)
	}
	if raw := GetString(task.Params, "_raw_params", ""); raw != "" {
		return e.resolveString(raw, vars)
	}
	return ""
}

func applyParentTaskAttributes(parent *Task, tasks []*Task) {
	if parent == nil {
		return
	}

	for _, task := range tasks {
		task.Tags = mergeTags(parent.Tags, task.Tags)
		task.Vars = mergeTaskVars(parent.Vars, task.Vars)
		task.When = combineWhen(parent.When, task.When)
		if parent.RunOnce {
			task.RunOnce = true
		}
		if parent.Become && !task.Become {
			task.Become = true
		}
		if parent.BecomeUser != "" && task.BecomeUser == "" {
			task.BecomeUser = parent.BecomeUser
		}
		if parent.IgnoreUnreachable && !task.IgnoreUnreachable {
			task.IgnoreUnreachable = true
		}
	}
}

func cloneTasks(tasks []*Task) []*Task {
	if len(tasks) == 0 {
		return nil
	}

	cloned := make([]*Task, 0, len(tasks))
	for _, task := range tasks {
		if task == nil {
			continue
		}
		copied := *task
		cloned = append(cloned, &copied)
	}
	return cloned
}

func hostResultHasFailure(hostResult *HostResult, start int) bool {
	if hostResult == nil {
		return false
	}
	if start < 0 {
		start = 0
	}
	for i := start; i < len(hostResult.Tasks); i++ {
		if hostResult.Tasks[i].Failed {
			return true
		}
	}
	return false
}

func hostResultHasNewFailure(hostResult *HostResult, start int) bool {
	return hostResultHasFailure(hostResult, start)
}

func mergeTaskVars(parentVars, childVars map[string]interface{}) map[string]interface{} {
	if len(parentVars) == 0 && len(childVars) == 0 {
		return nil
	}

	merged := make(map[string]interface{})
	for key, value := range parentVars {
		merged[key] = value
	}
	for key, value := range childVars {
		merged[key] = value
	}
	return merged
}

func combineWhen(parentWhen, childWhen interface{}) interface{} {
	if parentWhen == nil {
		return childWhen
	}
	if childWhen == nil {
		return parentWhen
	}

	result := make([]interface{}, 0, 2)
	if parentList, ok := parentWhen.([]interface{}); ok {
		result = append(result, parentList...)
	} else {
		result = append(result, parentWhen)
	}
	if childList, ok := childWhen.([]interface{}); ok {
		result = append(result, childList...)
	} else {
		result = append(result, childWhen)
	}
	return result
}

func (e *Executor) executeTaskForHostWithEnv(hosts []*inventory.Host, host *inventory.Host, task *Task, vars map[string]interface{}, handlers []*Handler, runOnce map[string]bool, playEnv map[string]string) ([]*TaskResult, error) {
	if task.Timeout > 0 {
		type execRes struct {
			results []*TaskResult
			err     error
		}
		ch := make(chan execRes, 1)
		go func() {
			res, err := e.executeTaskForHostWithEnvInternal(hosts, host, task, vars, handlers, runOnce, playEnv)
			ch <- execRes{results: res, err: err}
		}()
		select {
		case out := <-ch:
			return out.results, out.err
		case <-time.After(time.Duration(task.Timeout) * time.Second):
			timeoutRes := &TaskResult{
				Name:     e.resolveString(task.Name, vars),
				Host:     host.Name,
				Failed:   !task.IgnoreError,
				Message:  fmt.Sprintf("task timed out after %d seconds", task.Timeout),
				Stderr:   fmt.Sprintf("task timed out after %d seconds", task.Timeout),
				ExitCode: 124,
				Start:    time.Now(),
				End:      time.Now(),
			}
			if task.IgnoreError {
				timeoutRes.Message = fmt.Sprintf("ignored error: task timed out after %d seconds", task.Timeout)
				return []*TaskResult{timeoutRes}, nil
			}
			return []*TaskResult{timeoutRes}, fmt.Errorf("task timed out after %d seconds", task.Timeout)
		}
	}
	return e.executeTaskForHostWithEnvInternal(hosts, host, task, vars, handlers, runOnce, playEnv)
}

func (e *Executor) executeTaskForHostWithEnvInternal(hosts []*inventory.Host, host *inventory.Host, task *Task, vars map[string]interface{}, handlers []*Handler, runOnce map[string]bool, playEnv map[string]string) ([]*TaskResult, error) {
	if !task.RunOnce {
		return e.executeTaskWithEnv(host, task, vars, handlers, playEnv)
	}

	key := e.taskExecutionKey(task)
	e.mu.Lock()
	if runOnce[key] {
		e.mu.Unlock()
		return []*TaskResult{{
			Name:    e.resolveString(task.Name, vars),
			Host:    host.Name,
			Skipped: true,
			Message: "skipped due to run_once",
			Start:   time.Now(),
			End:     time.Now(),
		}}, nil
	}

	if len(hosts) > 0 && hosts[0].Name != host.Name {
		e.mu.Unlock()
		return []*TaskResult{{
			Name:    e.resolveString(task.Name, vars),
			Host:    host.Name,
			Skipped: true,
			Message: "skipped due to run_once",
			Start:   time.Now(),
			End:     time.Now(),
		}}, nil
	}

	runOnce[key] = true
	e.mu.Unlock()
	return e.executeTaskWithEnv(host, task, vars, handlers, playEnv)
}

func (e *Executor) taskExecutionKey(task *Task) string {
	return fmt.Sprintf("%s|%s|%v", task.Name, task.ModuleName, task.Params)
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
			if _, exists := playVars[k]; !exists {
				playVars[k] = v
			}
		}
	}
	return nil
}

func (e *Executor) loadPlayRoles(play *Play) ([]*Task, []*Handler, map[string]interface{}, error) {
	if len(play.Roles) == 0 {
		return nil, nil, nil, nil
	}

	parser := NewParser(e.baseDir)
	tasks := make([]*Task, 0)
	handlers := make([]*Handler, 0)
	roleVars := make(map[string]interface{})
	executedRoles := make(map[string]bool)

	var loadRole func(rawRole interface{}, callStack []string) error
	loadRole = func(rawRole interface{}, callStack []string) error {
		roleName, inlineVars := parseRoleSpec(rawRole)
		if roleName == "" {
			return fmt.Errorf("invalid role specification: %v", rawRole)
		}

		// 循环依赖检测
		for _, prev := range callStack {
			if prev == roleName {
				return fmt.Errorf("circular role dependency detected: %s -> %s", strings.Join(callStack, " -> "), roleName)
			}
		}

		// 同一 Play 避免重复执行同名依赖角色
		if executedRoles[roleName] {
			return nil
		}

		rolePath, err := e.resolveRolePath(roleName)
		if err != nil {
			return err
		}

		role, err := parser.ParseRole(rolePath)
		if err != nil {
			return fmt.Errorf("parse role %q: %w", roleName, err)
		}

		newStack := append(append([]string{}, callStack...), roleName)

		// 1. 递归优先加载前置依赖角色 (dependencies)
		for _, dep := range role.Dependencies {
			if err := loadRole(dep, newStack); err != nil {
				return err
			}
		}

		// 2. 加载 Defaults, Vars 和 InlineVars
		for k, v := range role.Defaults {
			if _, exists := roleVars[k]; !exists {
				roleVars[k] = v
			}
		}
		for k, v := range role.Vars {
			roleVars[k] = v
		}
		for k, v := range inlineVars {
			roleVars[k] = v
		}

		// 3. 追加任务和 Handlers
		tasks = append(tasks, role.Tasks...)
		handlers = append(handlers, role.Handlers...)

		executedRoles[roleName] = true
		return nil
	}

	for _, rawRole := range play.Roles {
		if err := loadRole(rawRole, nil); err != nil {
			return nil, nil, nil, err
		}
	}

	return tasks, handlers, roleVars, nil
}

func (e *Executor) resolveRolePath(roleName string) (string, error) {
	if roleName == "" {
		return "", fmt.Errorf("role name is required")
	}
	if filepath.IsAbs(roleName) {
		return roleName, nil
	}

	candidates := make([]string, 0, len(e.rolePaths)+1)
	for _, rolePath := range e.rolePaths {
		candidate := rolePath
		if !filepath.IsAbs(candidate) && e.baseDir != "" {
			candidate = filepath.Join(e.baseDir, candidate)
		}
		candidates = append(candidates, filepath.Join(candidate, roleName))
	}
	if e.baseDir != "" {
		candidates = append(candidates, filepath.Join(e.baseDir, "roles", roleName))
	}

	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
	}

	if len(candidates) > 0 {
		return candidates[0], nil
	}
	return roleName, nil
}

func parseRoleSpec(rawRole interface{}) (string, map[string]interface{}) {
	switch v := rawRole.(type) {
	case string:
		return v, nil
	case map[string]interface{}:
		roleName := ""
		if role, ok := v["role"]; ok {
			roleName = fmt.Sprintf("%v", role)
		} else if name, ok := v["name"]; ok {
			roleName = fmt.Sprintf("%v", name)
		}

		inlineVars := make(map[string]interface{})
		if nestedVars, ok := v["vars"].(map[string]interface{}); ok {
			for k, val := range nestedVars {
				inlineVars[k] = val
			}
		}
		for k, val := range v {
			switch k {
			case "role", "name", "vars", "when", "tags":
				continue
			default:
				inlineVars[k] = val
			}
		}
		return roleName, inlineVars
	default:
		return "", nil
	}
}

func (e *Executor) taskVarsForHost(host *inventory.Host, baseVars map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})

	if host != nil && e.inventory != nil {
		for key, value := range e.inventory.ResolveHostVars(host) {
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
	result["play_hosts"] = e.getActivePlayHosts()
	result["hostvars"] = e.buildHostVarsSnapshot(host, result)

	return result
}

func (e *Executor) buildGroupsMap() map[string][]string {
	groups := make(map[string][]string)
	if e.inventory == nil {
		groups["all"] = []string{}
		groups["ungrouped"] = []string{}
		return groups
	}

	allHosts := e.inventory.GetAllHosts()
	allNames := make([]string, 0, len(allHosts))
	for _, h := range allHosts {
		allNames = append(allNames, h.Name)
	}
	sort.Strings(allNames)
	groups["all"] = allNames

	grouped := make(map[string]bool)
	for gName, g := range e.inventory.Groups {
		if gName == "all" {
			continue
		}
		hMap := make(map[string]*inventory.Host)
		e.inventory.CollectGroupHosts(g, hMap)
		names := make([]string, 0, len(hMap))
		for name := range hMap {
			names = append(names, name)
			grouped[name] = true
		}
		sort.Strings(names)
		groups[gName] = names
	}

	ungrouped := make([]string, 0)
	for _, name := range allNames {
		if !grouped[name] {
			ungrouped = append(ungrouped, name)
		}
	}
	sort.Strings(ungrouped)
	groups["ungrouped"] = ungrouped

	return groups
}

func (e *Executor) buildGroupNamesForHost(host *inventory.Host) []string {
	if host == nil || e.inventory == nil {
		return []string{}
	}
	names := make([]string, 0)
	for gName, g := range e.inventory.Groups {
		if gName == "all" {
			continue
		}
		hMap := make(map[string]*inventory.Host)
		e.inventory.CollectGroupHosts(g, hMap)
		if _, ok := hMap[host.Name]; ok {
			names = append(names, gName)
		}
	}
	sort.Strings(names)
	return names
}

func (e *Executor) buildHostVarsSnapshot(currentHost *inventory.Host, currentVars map[string]interface{}) map[string]map[string]interface{} {
	hostvars := make(map[string]map[string]interface{})
	if e.inventory == nil {
		return hostvars
	}

	for _, h := range e.inventory.GetAllHosts() {
		hVars := make(map[string]interface{})
		for k, v := range e.inventory.ResolveHostVars(h) {
			hVars[k] = v
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

		hostvars[h.Name] = hVars
	}

	if currentHost != nil {
		if cur, ok := hostvars[currentHost.Name]; ok {
			for k, v := range currentVars {
				if k == "hostvars" {
					continue
				}
				cur[k] = v
			}
		}
	}

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

func shortHostname(name string) string {
	if idx := strings.Index(name, "."); idx > 0 {
		return name[:idx]
	}
	return name
}

// executeTask 执行单个任务，返回结果列表（循环任务会返回多个结果）
func (e *Executor) executeTask(host *inventory.Host, task *Task, vars map[string]interface{}, handlers []*Handler) ([]*TaskResult, error) {
	return e.executeTaskWithEnv(host, task, vars, handlers, nil)
}

func (e *Executor) executeTaskWithEnv(host *inventory.Host, task *Task, vars map[string]interface{}, handlers []*Handler, playEnv map[string]string) ([]*TaskResult, error) {
	// 先合并 play/inventory vars
	allVars := make(map[string]interface{})
	for k, v := range vars {
		allVars[k] = v
	}

	// task vars 可以引用当前上下文，但 extra vars 仍然保持最高优先级
	if len(task.Vars) > 0 {
		resolvedTaskVars := e.resolveVariables(task.Vars, allVars)
		for k, v := range resolvedTaskVars {
			allVars[k] = v
		}
	}

	e.mu.RLock()
	for k, v := range e.extraVars {
		allVars[k] = v
	}
	e.mu.RUnlock()
	combinedEnv := make(map[string]string)
	for k, v := range playEnv {
		combinedEnv[k] = v
	}
	if task != nil && len(task.Environment) > 0 {
		for k, v := range task.Environment {
			combinedEnv[k] = v
		}
	}
	resolvedEnv := e.resolveEnvironment(combinedEnv, allVars)

	e.log("Executing task: %s on host: %s", task.Name, host.Name)
	e.log("Task tags: %v, Filter tags: %v", task.Tags, e.tags)

	execHost := *host
	if task.DelegateTo != "" {
		delegateHost, localDelegate, err := e.resolveDelegateHost(task.DelegateTo, allVars)
		if err != nil {
			result := &TaskResult{
				Name:    e.resolveString(task.Name, allVars),
				Host:    host.Name,
				Failed:  true,
				Message: err.Error(),
				Start:   time.Now(),
				End:     time.Now(),
			}
			return []*TaskResult{result}, nil
		}
		if !localDelegate {
			execHost = *delegateHost
		}
	}
	if task.Become {
		execHost.Become = true
	}
	if task.BecomeUser != "" {
		execHost.Become = true
		execHost.BecomeUser = task.BecomeUser
	}
	if task.BecomeMethod != "" {
		execHost.Become = true
		execHost.BecomeMethod = task.BecomeMethod
	}
	if task.BecomePass != "" {
		execHost.Become = true
		execHost.BecomePass = task.BecomePass
	}

	result := &TaskResult{
		Name:    e.resolveString(task.Name, allVars),
		Host:    execHost.Name,
		Start:   time.Now(),
		Changed: false,
		Failed:  false,
	}

	// 检查 tags 过滤 - 如果指定了 tags，只有匹配的任务才执行
	if !e.shouldRunTaskByTags(task.Tags) {
		result.Skipped = true
		result.Message = "skipped due to tags filter"
		result.End = time.Now()
		e.log("Task skipped: tags not matched")
		return []*TaskResult{result}, nil
	}

	e.log("Task passed tags filter, executing...")

	// 检查 when 条件（无 loop 时在此处判定；有 loop 时在每个 item 执行时单独判定）
	if task.When != nil && task.Loop == nil {
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

		loopVarName := task.LoopControl.LoopVar
		if loopVarName == "" {
			loopVarName = "item"
		}

		for index, item := range items {
			// 先解析 item 中的变量
			resolvedItem := e.resolveValue(item, allVars)

			loopVars := make(map[string]interface{})
			for k, v := range allVars {
				loopVars[k] = v
			}
			loopVars[loopVarName] = resolvedItem
			if loopVarName != "item" {
				loopVars["item"] = resolvedItem
			}
			if task.LoopControl.IndexVar != "" {
				loopVars[task.LoopControl.IndexVar] = index
			}
			ansibleLoop := map[string]interface{}{
				"index":     index + 1,
				"index0":    index,
				"first":     index == 0,
				"last":      index == len(items)-1,
				"length":    len(items),
				"revindex":  len(items) - index,
				"revindex0": len(items) - index - 1,
			}
			loopVars["ansible_loop"] = ansibleLoop

			// 创建单个 item 的结果
			displayItem := resolvedItem
			if task.LoopControl.Label != "" {
				displayItem = e.resolveString(task.LoopControl.Label, loopVars)
			}
			itemResult := &TaskResult{
				Name:    e.resolveString(task.Name, loopVars),
				Host:    host.Name,
				Start:   time.Now(),
				Changed: false,
				Failed:  false,
				Item:    displayItem,
			}

			if task.When != nil {
				if !e.evaluateWhen(task.When, loopVars) {
					itemResult.Skipped = true
					itemResult.Message = "skipped due to when condition"
					itemResult.End = time.Now()
					results = append(results, itemResult)
					continue
				}
			}

			// 执行任务
			for {
				params := e.resolveVariables(task.Params, loopVars)
				moduleName := task.ModuleName
				localAction := task.LocalAction != "" || isLocalDelegate(task.DelegateTo, loopVars)
				if task.LocalAction != "" {
					moduleName = task.ModuleName
				}

				e.applyBecomeParams(params, execHost)
				params = e.withEnvironmentParams(params, resolvedEnv, moduleName)

				e.log("Resolved params: %+v", params)
				modResult, err := e.executeTaskModule(&execHost, moduleName, params, task, loopVars, localAction)
				e.applyExecutionOutcome(itemResult, task, modResult, err)

				if modResult != nil {
					e.log("Module result: Changed=%v, Stdout=%s", modResult.Changed, modResult.Stdout)
					itemResult.Changed = modResult.Changed
					itemResult.Stdout = modResult.Stdout
					itemResult.Stderr = modResult.Stderr
					itemResult.ExitCode = modResult.ExitCode
					itemResult.Diff = modResult.Diff
					itemResult.Data = modResult.Data
				}
				e.applyTaskResultConditions(itemResult, task, loopVars)

				if e.shouldTriggerDebugger(task, itemResult) {
					action := e.runDebugger(&execHost, task, itemResult, loopVars)
					if action == DebuggerRedo {
						itemResult.Failed = false
						itemResult.Changed = false
						itemResult.Message = ""
						itemResult.Stdout = ""
						itemResult.Stderr = ""
						itemResult.ExitCode = 0
						continue
					} else if action == DebuggerQuit {
						itemResult.End = time.Now()
						results = append(results, itemResult)
						e.storeRegisteredResult(host.Name, task, results)
						return results, fmt.Errorf("debugger requested quit")
					}
				}
				break
			}

			// notify handlers if changed
			if task.Notify != nil && itemResult.Changed {
				e.notifyHandlers(task.Notify, &execHost, loopVars, resolvedEnv, handlers)
			}

			itemResult.End = time.Now()
			results = append(results, itemResult)
		}

		e.storeRegisteredResult(host.Name, task, results)
		return results, nil
	}

	// 普通任务执行
	for {
		// 解析变量
		params := e.resolveVariables(task.Params, allVars)
		localAction := task.LocalAction != "" || isLocalDelegate(task.DelegateTo, allVars)

		e.applyBecomeParams(params, execHost)
		params = e.withEnvironmentParams(params, resolvedEnv, task.ModuleName)

		e.log("Resolved params: %+v", params)
		modResult, err := e.executeTaskModule(&execHost, task.ModuleName, params, task, allVars, localAction)
		e.applyExecutionOutcome(result, task, modResult, err)

		if modResult != nil {
			e.log("Module result: Changed=%v", modResult.Changed)
			result.Changed = modResult.Changed
			result.Stdout = modResult.Stdout
			result.Stderr = modResult.Stderr
			result.ExitCode = modResult.ExitCode
			result.Diff = modResult.Diff
			result.Data = modResult.Data
			if modResult.Message != "" {
				result.Message = modResult.Message
			}
		}
		e.applyTaskResultConditions(result, task, allVars)

		if e.shouldTriggerDebugger(task, result) {
			action := e.runDebugger(&execHost, task, result, allVars)
			if action == DebuggerRedo {
				result.Failed = false
				result.Changed = false
				result.Message = ""
				result.Stdout = ""
				result.Stderr = ""
				result.ExitCode = 0
				continue
			} else if action == DebuggerQuit {
				result.End = time.Now()
				return []*TaskResult{result}, fmt.Errorf("debugger requested quit")
			}
		}
		break
	}

	// register 变量
	if task.Register != "" {
		e.storeRegisteredResult(host.Name, task, []*TaskResult{result})
	}

	// notify handlers
	if task.Notify != nil && result.Changed {
		e.notifyHandlers(task.Notify, &execHost, allVars, resolvedEnv, handlers)
	}

	result.End = time.Now()
	return []*TaskResult{result}, nil
}

func (e *Executor) executeTaskModule(host *inventory.Host, moduleName string, params map[string]interface{}, task *Task, vars map[string]interface{}, localAction bool) (*module.Result, error) {
	if task != nil && task.Async > 0 {
		return e.executeAsyncTask(host, moduleName, params, task, vars, localAction)
	}
	params = e.withCheckModeParams(params, moduleName)
	params = e.withDiffModeParams(params, moduleName)

	modResult, handled, err := e.executeBuiltinModule(host, moduleName, params, task, vars)
	if handled {
		return modResult, err
	}
	return e.executeWithRetry(host, moduleName, params, task, vars, localAction)
}

func (e *Executor) executeHandlerModule(host *inventory.Host, moduleName string, params map[string]interface{}, taskName string, vars map[string]interface{}) (*module.Result, error) {
	modResult, handled, err := e.executeBuiltinModule(host, moduleName, params, &Task{
		Name:       taskName,
		ModuleName: moduleName,
		Params:     params,
	}, vars)
	if handled {
		return modResult, err
	}
	return e.executeRemoteModule(host, moduleName, params)
}

func (e *Executor) handlerExecutionHost(host *inventory.Host, handler *Handler) inventory.Host {
	handlerHost := *host
	if handler == nil {
		return handlerHost
	}
	if handler.Become {
		handlerHost.Become = true
	}
	if handler.BecomeUser != "" {
		handlerHost.Become = true
		handlerHost.BecomeUser = handler.BecomeUser
	}
	if handler.BecomeMethod != "" {
		handlerHost.Become = true
		handlerHost.BecomeMethod = handler.BecomeMethod
	}
	if handler.BecomePass != "" {
		handlerHost.Become = true
		handlerHost.BecomePass = handler.BecomePass
	}
	return handlerHost
}

func (e *Executor) applyBecomeParams(params map[string]interface{}, host inventory.Host) {
	if !host.Become {
		return
	}

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

func (e *Executor) executeBuiltinModule(host *inventory.Host, moduleName string, params map[string]interface{}, task *Task, vars map[string]interface{}) (*module.Result, bool, error) {
	switch moduleName {
	case "debug":
		return e.executeDebugModule(params, task, vars), true, nil
	case "fail":
		return e.executeFailModule(params), true, nil
	case "assert":
		return e.executeAssertModule(params, vars), true, nil
	case "set_fact":
		return e.executeSetFactModule(host, params), true, nil
	case "include_vars":
		return e.executeIncludeVarsModule(host, params)
	case "meta":
		return e.executeMetaModule(host, params, vars), true, nil
	case "add_host":
		return e.executeAddHostModule(params, vars), true, nil
	case "group_by":
		return e.executeGroupByModule(host, params, vars), true, nil
	case "reboot":
		res, err := e.executeReboot(host, params)
		return res, true, err
	case "wait_for_connection":
		res, err := e.executeWaitForConnection(host, params)
		return res, true, err
	default:
		return nil, false, nil
	}
}

func (e *Executor) executeAsyncTask(host *inventory.Host, moduleName string, params map[string]interface{}, task *Task, vars map[string]interface{}, localAction bool) (*module.Result, error) {
	if moduleName != "shell" && moduleName != "command" {
		return nil, fmt.Errorf("async is only supported for shell and command modules")
	}

	poll := 0
	if task.Poll != nil {
		poll = *task.Poll
	}

	if poll == 0 {
		return e.startDetachedTask(host, moduleName, params, localAction)
	}

	type asyncResult struct {
		result *module.Result
		err    error
	}

	done := make(chan asyncResult, 1)
	go func() {
		var result *module.Result
		var err error
		if localAction {
			result, err = e.executeLocalModule(moduleName, params)
		} else {
			result, err = e.executeRemoteModule(host, moduleName, params)
		}
		done <- asyncResult{result: result, err: err}
	}()

	timeout := time.After(time.Duration(task.Async) * time.Second)
	interval := e.pollInterval
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case res := <-done:
			return res.result, res.err
		case <-timeout:
			return &module.Result{
				Changed: true,
				Failed:  true,
				Message: fmt.Sprintf("async task timed out after %d seconds", task.Async),
			}, fmt.Errorf("async task timed out after %d seconds", task.Async)
		case <-ticker.C:
			e.log("Polling async task %s on host %s", task.Name, host.Name)
		}
	}
}

func (e *Executor) startDetachedTask(host *inventory.Host, moduleName string, params map[string]interface{}, localAction bool) (*module.Result, error) {
	raw := GetString(params, "_raw_params", GetString(params, "cmd", ""))
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("%s module requires _raw_params or cmd", moduleName)
	}

	command := raw
	if moduleName == "command" {
		command = "exec " + raw
	}

	params, env := splitEnvironmentParams(params)
	if len(env) > 0 {
		params = applyEnvironmentToModuleParams(moduleName, params, env)
		raw = GetString(params, "_raw_params", GetString(params, "cmd", ""))
		if strings.TrimSpace(raw) == "" {
			return nil, fmt.Errorf("%s module requires _raw_params or cmd", moduleName)
		}
		command = raw
		if moduleName == "command" {
			command = "exec " + raw
		}
	}

	targetHost := host
	if localAction {
		targetHost = &inventory.Host{
			Name:       "localhost",
			Address:    "localhost",
			Connection: "local",
		}
	}

	client := e.getOrCreateClient(targetHost)
	if client == nil {
		return nil, fmt.Errorf("failed to create connection client")
	}

	jid := fmt.Sprintf("%d.%06d", time.Now().Unix(), time.Now().Nanosecond()%1000000)
	escapedCommand := strings.ReplaceAll(command, `'`, `'\''`)

	detachedScript := fmt.Sprintf(`
ASYNC_DIR="$HOME/.ansible_async"
if [ ! -d "$ASYNC_DIR" ]; then
    mkdir -p "$ASYNC_DIR" 2>/dev/null || ASYNC_DIR="/tmp/.ansible_async"
    mkdir -p "$ASYNC_DIR" 2>/dev/null
fi
STATUS_FILE="$ASYNC_DIR/%s"
OUT_FILE="$ASYNC_DIR/%s.out"
ERR_FILE="$ASYNC_DIR/%s.err"

echo '{"started":1,"finished":0,"ansible_job_id":"%s","results_file":"'"$STATUS_FILE"'"}' > "$STATUS_FILE"

(
    /bin/sh -c '%s' > "$OUT_FILE" 2> "$ERR_FILE"
    RC=$?
    echo '{"started":1,"finished":1,"ansible_job_id":"%s","results_file":"'"$STATUS_FILE"'","rc":'$RC'}' > "$STATUS_FILE"
) >/dev/null 2>&1 &
PID=$!
echo '{"started":1,"finished":0,"ansible_job_id":"%s","results_file":"'"$STATUS_FILE"'","pid":'$PID'}' > "$STATUS_FILE"
echo $PID
`, jid, jid, jid, jid, escapedCommand, jid, jid)

	res, err := client.Execute(detachedScript)
	if err != nil {
		return res, err
	}

	pid := strings.TrimSpace(res.Stdout)
	return &module.Result{
		Changed: true,
		Message: fmt.Sprintf("started async job jid=%s (pid=%s)", jid, pid),
		Stdout:  pid,
		Data: map[string]interface{}{
			"ansible_job_id": jid,
			"results_file":   fmt.Sprintf("~/.ansible_async/%s", jid),
			"started":        1,
			"finished":       0,
		},
	}, nil
}

func (e *Executor) executeDebugModule(params map[string]interface{}, task *Task, vars map[string]interface{}) *module.Result {
	if vVal, ok := params["verbosity"]; ok {
		requiredVerbosity := 0
		switch v := vVal.(type) {
		case int:
			requiredVerbosity = v
		case float64:
			requiredVerbosity = int(v)
		case string:
			if iv, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				requiredVerbosity = iv
			}
		}
		if e.verbosityLevel < requiredVerbosity {
			return &module.Result{
				Changed: false,
				Message: fmt.Sprintf("debug task skipped: verbosity %d required, current is %d", requiredVerbosity, e.verbosityLevel),
				Stdout:  "",
				Data: map[string]interface{}{
					"skipped":        true,
					"skipped_reason": fmt.Sprintf("Verbosity threshold not met: %d > %d", requiredVerbosity, e.verbosityLevel),
				},
			}
		}
	}

	if msg, ok := params["msg"]; ok {
		return &module.Result{
			Changed: false,
			Message: fmt.Sprintf("%v", msg),
			Stdout:  fmt.Sprintf("%v", msg),
		}
	}

	rawVarName := ""
	if task != nil {
		rawVarName = GetString(task.Params, "var", "")
	}
	if rawVarName == "" {
		rawVarName = GetString(params, "var", "")
	}
	if rawVarName == "" {
		return &module.Result{
			Changed: false,
			Message: "debug module requires msg or var",
		}
	}

	value, ok := e.lookupVarExpr(rawVarName, vars)
	if !ok {
		return &module.Result{
			Changed: false,
			Message: fmt.Sprintf("%s: VARIABLE IS NOT DEFINED", rawVarName),
			Stdout:  fmt.Sprintf("%s: VARIABLE IS NOT DEFINED", rawVarName),
		}
	}

	output := fmt.Sprintf("%s: %v", rawVarName, value)
	return &module.Result{
		Changed: false,
		Message: output,
		Stdout:  output,
	}
}

func (e *Executor) executeFailModule(params map[string]interface{}) *module.Result {
	message := GetString(params, "msg", "")
	if message == "" {
		message = GetString(params, "message", "Failed as requested from task")
	}
	return &module.Result{
		Changed:  false,
		Failed:   true,
		Message:  message,
		Stderr:   message,
		ExitCode: 1,
	}
}

func (e *Executor) executeAssertModule(params map[string]interface{}, vars map[string]interface{}) *module.Result {
	expressions := assertExpressions(params["that"])
	if len(expressions) == 0 {
		return &module.Result{
			Changed:  false,
			Failed:   true,
			Message:  "assert requires that",
			ExitCode: 1,
		}
	}

	failed := make([]string, 0)
	for _, expr := range expressions {
		if !e.evaluateWhen(expr, vars) {
			failed = append(failed, expr)
		}
	}

	quiet := false
	if q, ok := params["quiet"].(bool); ok {
		quiet = q
	}

	if len(failed) > 0 {
		rawMsg := GetString(params, "fail_msg", "")
		if rawMsg == "" {
			rawMsg = GetString(params, "msg", "")
		}
		if rawMsg == "" {
			rawMsg = fmt.Sprintf("assertion failed: %s", strings.Join(failed, ", "))
		}
		message := e.resolveString(rawMsg, vars)
		return &module.Result{
			Changed:  false,
			Failed:   true,
			Message:  message,
			Stderr:   message,
			ExitCode: 1,
			Data: map[string]interface{}{
				"assertion":    failed[0],
				"evaluated_to": false,
			},
		}
	}

	rawSuccess := GetString(params, "success_msg", "All assertions passed")
	message := e.resolveString(rawSuccess, vars)
	stdout := message
	if quiet {
		stdout = ""
	}

	return &module.Result{
		Changed: false,
		Message: message,
		Stdout:  stdout,
	}
}

func assertExpressions(value interface{}) []string {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		return []string{v}
	case []string:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if strings.TrimSpace(item) != "" {
				result = append(result, item)
			}
		}
		return result
	case []interface{}:
		result := make([]string, 0, len(v))
		for _, item := range v {
			expr := strings.TrimSpace(fmt.Sprintf("%v", item))
			if expr != "" {
				result = append(result, expr)
			}
		}
		return result
	default:
		expr := strings.TrimSpace(fmt.Sprintf("%v", v))
		if expr == "" {
			return nil
		}
		return []string{expr}
	}
}

func (e *Executor) executeSetFactModule(host *inventory.Host, params map[string]interface{}) *module.Result {
	if host == nil {
		return &module.Result{
			Changed: false,
			Message: "set_fact requires host context",
			Failed:  true,
		}
	}

	e.mu.Lock()
	if e.facts[host.Name] == nil {
		e.facts[host.Name] = make(map[string]interface{})
	}
	for key, value := range params {
		e.facts[host.Name][key] = value
	}
	e.mu.Unlock()

	return &module.Result{
		Changed: false,
		Message: "facts updated",
	}
}

func (e *Executor) executeAddHostModule(params map[string]interface{}, vars map[string]interface{}) *module.Result {
	rawName := GetString(params, "name", GetString(params, "hostname", ""))
	hostName := strings.TrimSpace(e.resolveString(rawName, vars))
	if hostName == "" {
		return &module.Result{
			Failed:   true,
			Message:  "add_host requires name or hostname",
			ExitCode: 1,
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.inventory == nil {
		e.inventory = inventory.NewInventory()
	}
	if e.inventory.AllHosts == nil {
		e.inventory.AllHosts = make(map[string]*inventory.Host)
	}
	if e.inventory.Groups == nil {
		e.inventory.Groups = make(map[string]*inventory.Group)
	}

	host, exists := e.inventory.AllHosts[hostName]
	if !exists {
		host = &inventory.Host{
			Name:      hostName,
			Address:   hostName,
			Port:      22,
			Variables: make(map[string]string),
		}
		e.inventory.AllHosts[hostName] = host
	}

	if host.Variables == nil {
		host.Variables = make(map[string]string)
	}

	var groupNames []string
	if gVal, ok := params["groups"]; ok {
		groupNames = append(groupNames, parseGroupList(gVal, e, vars)...)
	}
	if gVal, ok := params["group"]; ok {
		groupNames = append(groupNames, parseGroupList(gVal, e, vars)...)
	}

	if e.facts == nil {
		e.facts = make(map[string]map[string]interface{})
	}
	if e.facts[hostName] == nil {
		e.facts[hostName] = make(map[string]interface{})
	}

	for k, v := range params {
		if k == "name" || k == "hostname" || k == "groups" || k == "group" {
			continue
		}
		valStr := e.resolveString(fmt.Sprintf("%v", v), vars)
		host.Variables[k] = valStr
		e.facts[hostName][k] = v

		switch k {
		case "ansible_host":
			host.Address = valStr
		case "ansible_port":
			if p, err := strconv.Atoi(valStr); err == nil {
				host.Port = p
			}
		case "ansible_user":
			host.User = valStr
		case "ansible_ssh_pass", "ansible_password":
			host.Password = valStr
		case "ansible_ssh_private_key_file":
			host.PrivateKey = valStr
		case "ansible_connection":
			host.Variables["ansible_connection"] = valStr
		}
	}

	for _, gName := range groupNames {
		gName = strings.TrimSpace(gName)
		if gName == "" {
			continue
		}
		grp, gExists := e.inventory.Groups[gName]
		if !gExists {
			grp = &inventory.Group{
				Name:      gName,
				Hosts:     make([]*inventory.Host, 0),
				Children:  make([]*inventory.Group, 0),
				Variables: make(map[string]string),
			}
			e.inventory.Groups[gName] = grp
		}
		found := false
		for _, h := range grp.Hosts {
			if h.Name == hostName {
				found = true
				break
			}
		}
		if !found {
			grp.Hosts = append(grp.Hosts, host)
		}
	}

	return &module.Result{
		Changed: true,
		Message: fmt.Sprintf("added host %s to inventory", hostName),
	}
}

func (e *Executor) executeGroupByModule(host *inventory.Host, params map[string]interface{}, vars map[string]interface{}) *module.Result {
	if host == nil {
		return &module.Result{
			Failed:   true,
			Message:  "group_by requires host context",
			ExitCode: 1,
		}
	}

	rawKey := GetString(params, "key", "")
	groupName := strings.TrimSpace(e.resolveString(rawKey, vars))
	if groupName == "" {
		return &module.Result{
			Failed:   true,
			Message:  "group_by requires key",
			ExitCode: 1,
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.inventory == nil {
		e.inventory = inventory.NewInventory()
	}
	if e.inventory.Groups == nil {
		e.inventory.Groups = make(map[string]*inventory.Group)
	}

	grp, exists := e.inventory.Groups[groupName]
	if !exists {
		grp = &inventory.Group{
			Name:      groupName,
			Hosts:     make([]*inventory.Host, 0),
			Children:  make([]*inventory.Group, 0),
			Variables: make(map[string]string),
		}
		e.inventory.Groups[groupName] = grp
	}

	found := false
	for _, h := range grp.Hosts {
		if h.Name == host.Name {
			found = true
			break
		}
	}
	if !found {
		grp.Hosts = append(grp.Hosts, host)
	}

	if parentsVal, ok := params["parents"]; ok {
		parentNames := parseGroupList(parentsVal, e, vars)
		for _, pName := range parentNames {
			pName = strings.TrimSpace(pName)
			if pName == "" {
				continue
			}
			pGrp, pExists := e.inventory.Groups[pName]
			if !pExists {
				pGrp = &inventory.Group{
					Name:      pName,
					Hosts:     make([]*inventory.Host, 0),
					Children:  make([]*inventory.Group, 0),
					Variables: make(map[string]string),
				}
				e.inventory.Groups[pName] = pGrp
			}
			childFound := false
			for _, child := range pGrp.Children {
				if child.Name == grp.Name {
					childFound = true
					break
				}
			}
			if !childFound {
				pGrp.Children = append(pGrp.Children, grp)
			}
		}
	}

	return &module.Result{
		Changed: true,
		Message: fmt.Sprintf("host %s added to group %s", host.Name, groupName),
	}
}

func (e *Executor) executeReboot(host *inventory.Host, params map[string]interface{}) (*module.Result, error) {
	checkMode := false
	if cm, ok := params["_ansible_check_mode"].(bool); ok && cm {
		checkMode = true
	}
	if checkMode {
		return &module.Result{
			Changed: true,
			Message: "Reboot would be initiated",
			Data: map[string]interface{}{
				"rebooted": true,
				"elapsed":  0,
			},
		}, nil
	}

	rebootTimeout := GetInt(params, "reboot_timeout", 600)
	postRebootDelay := GetInt(params, "post_reboot_delay", 0)
	preRebootDelay := GetInt(params, "pre_reboot_delay", 0)
	testCommand := GetString(params, "test_command", "whoami")
	searchRegex := GetString(params, "search_regex", "")
	bootTimeCommand := GetString(params, "boot_time_command", "cat /proc/sys/kernel/random/boot_id 2>/dev/null || uptime -s 2>/dev/null || who -b 2>/dev/null")
	rebootCommand := GetString(params, "reboot_command", "")

	startTime := time.Now()

	// 1. 获取重启前 Boot ID
	var previousBootID string
	client := e.getOrCreateClient(host)
	if client != nil {
		res, err := client.Execute(bootTimeCommand)
		if err == nil && res != nil && res.ExitCode == 0 {
			previousBootID = strings.TrimSpace(res.Stdout)
		}
	}

	// 2. Pre-reboot delay
	if preRebootDelay > 0 {
		time.Sleep(time.Duration(preRebootDelay) * time.Second)
	}

	// 3. 下发 reboot 命令
	cmd := rebootCommand
	if cmd == "" {
		cmd = "/bin/sh -c 'sleep 1; reboot' >/dev/null 2>&1 &"
	}
	if client != nil {
		if host.Become {
			_, _ = client.ExecuteWithBecome(cmd)
		} else {
			_, _ = client.Execute(cmd)
		}
	}

	// 4. 关闭现有连接
	if client != nil {
		_ = client.Close()
	}

	// 5. 轮询等待恢复上线
	deadline := startTime.Add(time.Duration(rebootTimeout) * time.Second)
	time.Sleep(100 * time.Millisecond)

	var rebootedSuccessfully bool
	for {
		if time.Now().After(deadline) {
			break
		}

		conn := client
		if conn == nil {
			conn = e.getOrCreateClient(host)
		}
		if conn != nil {
			if err := conn.Connect(); err == nil {
				res, err := conn.Execute(testCommand)
				if err == nil && res != nil && res.ExitCode == 0 {
					passed := true
					if searchRegex != "" {
						matched, _ := regexp.MatchString(searchRegex, res.Stdout)
						if !matched {
							passed = false
						}
					}
					if passed && previousBootID != "" {
						bRes, bErr := conn.Execute(bootTimeCommand)
						if bErr == nil && bRes != nil && bRes.ExitCode == 0 {
							if strings.TrimSpace(bRes.Stdout) == previousBootID {
								passed = false
							}
						}
					}
					if passed {
						rebootedSuccessfully = true
						break
					}
				}
			}
		}

		time.Sleep(100 * time.Millisecond)
	}

	elapsed := int(time.Since(startTime).Seconds())
	if !rebootedSuccessfully {
		return &module.Result{
			Failed:  true,
			Message: fmt.Sprintf("Timed out waiting for host %s to reboot after %d seconds", host.Name, rebootTimeout),
			Data: map[string]interface{}{
				"rebooted": false,
				"elapsed":  elapsed,
			},
		}, fmt.Errorf("timed out waiting for host %s to reboot after %d seconds", host.Name, rebootTimeout)
	}

	// 6. 清理 Facts 缓存，让下次获取 Facts 时重新探测
	e.mu.Lock()
	if e.facts != nil {
		delete(e.facts, host.Name)
	}
	e.mu.Unlock()

	// 7. Post-reboot delay
	if postRebootDelay > 0 {
		time.Sleep(time.Duration(postRebootDelay) * time.Second)
	}

	return &module.Result{
		Changed: true,
		Message: "Reboot succeeded",
		Data: map[string]interface{}{
			"rebooted": true,
			"elapsed":  elapsed,
		},
	}, nil
}

func (e *Executor) executeWaitForConnection(host *inventory.Host, params map[string]interface{}) (*module.Result, error) {
	checkMode := false
	if cm, ok := params["_ansible_check_mode"].(bool); ok && cm {
		checkMode = true
	}
	if checkMode {
		return &module.Result{
			Changed: false,
			Message: "Connection check mode ok",
			Data: map[string]interface{}{
				"elapsed": 0,
			},
		}, nil
	}

	delay := GetInt(params, "delay", 0)
	sleepSec := GetInt(params, "sleep", 1)
	timeout := GetInt(params, "timeout", 600)
	testCommand := GetString(params, "test_command", "echo ping")

	if sleepSec <= 0 {
		sleepSec = 1
	}

	if delay > 0 {
		time.Sleep(time.Duration(delay) * time.Second)
	}

	startTime := time.Now()
	deadline := startTime.Add(time.Duration(timeout) * time.Second)

	for {
		client := e.getOrCreateClient(host)
		if client != nil {
			if err := client.Connect(); err == nil {
				res, execErr := client.Execute(testCommand)
				if execErr == nil && res != nil && res.ExitCode == 0 {
					elapsed := int(time.Since(startTime).Seconds())
					return &module.Result{
						Changed: false,
						Message: "Connection established successfully",
						Data: map[string]interface{}{
							"elapsed": elapsed,
						},
					}, nil
				}
			}
		}

		if time.Now().After(deadline) {
			elapsed := int(time.Since(startTime).Seconds())
			return &module.Result{
				Failed:  true,
				Message: fmt.Sprintf("Timed out waiting for connection to %s after %d seconds", host.Name, timeout),
				Data: map[string]interface{}{
					"elapsed": elapsed,
				},
			}, fmt.Errorf("timed out waiting for connection to %s after %d seconds", host.Name, timeout)
		}

		time.Sleep(time.Duration(sleepSec) * time.Second)
	}
}

func parseGroupList(val interface{}, e *Executor, vars map[string]interface{}) []string {
	var result []string
	switch v := val.(type) {
	case string:
		parts := strings.Split(v, ",")
		for _, p := range parts {
			trimmed := strings.TrimSpace(e.resolveString(p, vars))
			if trimmed != "" {
				result = append(result, trimmed)
			}
		}
	case []interface{}:
		for _, item := range v {
			trimmed := strings.TrimSpace(e.resolveString(fmt.Sprintf("%v", item), vars))
			if trimmed != "" {
				result = append(result, trimmed)
			}
		}
	case []string:
		for _, item := range v {
			trimmed := strings.TrimSpace(e.resolveString(item, vars))
			if trimmed != "" {
				result = append(result, trimmed)
			}
		}
	}
	return result
}

func (e *Executor) executeMetaModule(host *inventory.Host, params map[string]interface{}, vars map[string]interface{}) *module.Result {
	action := GetString(params, "_raw_params", "")
	if action == "" {
		action = GetString(params, "action", "")
	}
	action = strings.TrimSpace(action)

	switch action {
	case "flush_handlers":
		if e.currentResult == nil {
			return &module.Result{
				Changed:  false,
				Failed:   true,
				Message:  "flush_handlers requires active play result",
				ExitCode: 1,
			}
		}
		before := len(e.pendingHandlers)
		if err := e.flushHandlers(e.currentResult); err != nil {
			return &module.Result{
				Changed:  false,
				Failed:   true,
				Message:  err.Error(),
				ExitCode: 1,
			}
		}
		return &module.Result{
			Changed: before > 0,
			Message: "handlers flushed",
		}
	case "clear_facts":
		if host != nil {
			e.mu.Lock()
			delete(e.facts, host.Name)
			e.mu.Unlock()
		}
		return &module.Result{
			Changed: false,
			Message: "facts cleared",
		}
	case "clear_host_errors":
		if host == nil {
			return &module.Result{
				Changed: false,
				Message: "clear_host_errors requires host context",
				Failed:  true,
			}
		}
		e.clearHostFailed(host.Name)
		return &module.Result{
			Changed: false,
			Message: "host errors cleared",
		}
	case "reset_connection":
		if host == nil {
			return &module.Result{
				Changed: false,
				Message: "reset_connection requires host context",
				Failed:  true,
			}
		}
		if err := e.pool.Remove(host.Name); err != nil {
			return &module.Result{
				Changed:  false,
				Failed:   true,
				Message:  err.Error(),
				ExitCode: 1,
			}
		}
		return &module.Result{
			Changed: false,
			Message: "connection reset",
		}
	case "refresh_inventory":
		if strings.TrimSpace(e.inventoryPath) == "" {
			return &module.Result{
				Changed: false,
				Message: "refresh_inventory requires configured inventory path",
				Failed:  true,
			}
		}
		loader := e.inventoryLoader
		if loader == nil {
			loader = inventory.ParseFile
		}
		reloaded, err := loader(e.inventoryPath)
		if err != nil {
			return &module.Result{
				Changed:  false,
				Failed:   true,
				Message:  fmt.Sprintf("refresh inventory: %v", err),
				ExitCode: 1,
			}
		}
		if reloaded == nil {
			return &module.Result{
				Changed: false,
				Failed:  true,
				Message: "refresh inventory: loader returned nil inventory",
			}
		}
		e.mu.RLock()
		extraVars := make(map[string]interface{}, len(e.extraVars))
		for key, value := range e.extraVars {
			extraVars[key] = value
		}
		e.mu.RUnlock()
		reloaded.ApplyConnectionVars(extraVars)
		if e.pool != nil {
			_ = e.pool.Close()
		}
		e.inventory = reloaded
		e.pool = ssh.NewConnectionPool(e.sshConfig)
		return &module.Result{
			Changed: false,
			Message: "inventory refreshed",
			Stdout:  e.inventoryPath,
		}
	case "end_host":
		if host != nil {
			e.markHostEnded(host.Name)
		}
		return &module.Result{
			Changed: false,
			Message: "ending host",
		}
	case "end_play":
		e.markEndPlay()
		return &module.Result{
			Changed: false,
			Message: "ending play",
		}
	case "noop":
		return &module.Result{
			Changed: false,
			Message: "noop",
		}
	default:
		_ = vars
		return &module.Result{
			Changed:  false,
			Failed:   true,
			Message:  fmt.Sprintf("unsupported meta action: %s", action),
			ExitCode: 1,
		}
	}
}

func (e *Executor) executeIncludeVarsModule(host *inventory.Host, params map[string]interface{}) (*module.Result, bool, error) {
	if host == nil {
		return &module.Result{
			Changed: false,
			Message: "include_vars requires host context",
			Failed:  true,
		}, true, nil
	}

	path := GetString(params, "file", "")
	if path == "" {
		path = GetString(params, "_raw_params", "")
	}
	dir := GetString(params, "dir", "")
	if path == "" && dir == "" {
		return &module.Result{
			Changed: false,
			Message: "include_vars requires file or dir",
			Failed:  true,
		}, true, nil
	}

	name := GetString(params, "name", "")
	ignoreUnknown := toBool(params["ignore_unknown_extensions"])
	loaded := make(map[string]interface{})
	files := []string{}

	if path != "" {
		resolvedPath := e.resolvePath(path)
		vars, err := e.varManager.LoadVarsFile(resolvedPath)
		if err != nil {
			return nil, true, err
		}
		mergeMap(loaded, vars)
		files = append(files, resolvedPath)
	}

	if dir != "" {
		resolvedDir := e.resolvePath(dir)
		dirFiles, err := includeVarsDirFiles(resolvedDir, ignoreUnknown)
		if err != nil {
			return nil, true, err
		}
		for _, file := range dirFiles {
			vars, err := e.varManager.LoadVarsFile(file)
			if err != nil {
				return nil, true, err
			}
			mergeMap(loaded, vars)
			files = append(files, file)
		}
	}

	e.mu.Lock()
	if e.facts[host.Name] == nil {
		e.facts[host.Name] = make(map[string]interface{})
	}
	if name != "" {
		e.facts[host.Name][name] = loaded
	} else {
		for key, value := range loaded {
			e.facts[host.Name][key] = value
		}
	}
	e.mu.Unlock()

	return &module.Result{
		Changed: false,
		Message: fmt.Sprintf("included %d vars file(s)", len(files)),
		Stdout:  strings.Join(files, "\n"),
	}, true, nil
}

func (e *Executor) resolvePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) || e.baseDir == "" {
		return path
	}
	return filepath.Join(e.baseDir, path)
}

func includeVarsDirFiles(dir string, ignoreUnknown bool) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		switch ext {
		case ".yml", ".yaml", ".json":
			files = append(files, filepath.Join(dir, entry.Name()))
		default:
			if !ignoreUnknown {
				return nil, fmt.Errorf("unsupported vars file extension: %s", entry.Name())
			}
		}
	}
	sort.Strings(files)
	return files, nil
}

func mergeMap(dst, src map[string]interface{}) {
	for key, value := range src {
		dst[key] = value
	}
}

func toBool(value interface{}) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		default:
			return false
		}
	case int:
		return v != 0
	default:
		return false
	}
}

func (e *Executor) storeRegisteredResult(hostName string, task *Task, results []*TaskResult) {
	if task == nil || task.Register == "" || hostName == "" || len(results) == 0 {
		return
	}

	registered := map[string]interface{}{
		"changed": false,
		"failed":  false,
		"results": make([]map[string]interface{}, 0, len(results)),
	}

	last := results[len(results)-1]
	registered["stdout"] = last.Stdout
	registered["stderr"] = last.Stderr
	registered["rc"] = last.ExitCode
	registered["msg"] = last.Message
	registered["diff"] = last.Diff
	registered["skipped"] = last.Skipped
	mergeMap(registered, last.Data)

	items := registered["results"].([]map[string]interface{})
	for _, item := range results {
		entry := map[string]interface{}{
			"changed": item.Changed,
			"failed":  item.Failed,
			"skipped": item.Skipped,
			"stdout":  item.Stdout,
			"stderr":  item.Stderr,
			"rc":      item.ExitCode,
			"msg":     item.Message,
			"diff":    item.Diff,
			"item":    item.Item,
		}
		mergeMap(entry, item.Data)
		if item.Changed {
			registered["changed"] = true
		}
		if item.Failed {
			registered["failed"] = true
		}
		items = append(items, entry)
	}
	registered["results"] = items

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.facts[hostName] == nil {
		e.facts[hostName] = make(map[string]interface{})
	}
	e.facts[hostName][task.Register] = registered
}

func effectiveRoleTasks(tasks []*Task) []*Task {
	return tasks
}

func mergePlayTags(playTags []string, tasks []*Task) {
	if len(playTags) == 0 {
		return
	}
	for _, task := range tasks {
		task.Tags = mergeTags(playTags, task.Tags)
	}
}

func mergeTags(left, right []string) []string {
	seen := make(map[string]struct{}, len(left)+len(right))
	result := make([]string, 0, len(left)+len(right))
	for _, tag := range left {
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		result = append(result, tag)
	}
	for _, tag := range right {
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		result = append(result, tag)
	}
	return result
}

func (e *Executor) executeWithRetry(host *inventory.Host, moduleName string, params map[string]interface{}, task *Task, vars map[string]interface{}, localAction bool) (*module.Result, error) {
	attempts := 1
	if task.Retries > 0 {
		attempts += task.Retries
	}

	var lastResult *module.Result
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if e.checkMode && !supportsCheckMode(moduleName) {
			return &module.Result{
				Changed: false,
				Message: "check mode: execution skipped",
			}, nil
		}

		if localAction {
			lastResult, lastErr = e.executeLocalModule(moduleName, params)
		} else {
			lastResult, lastErr = e.executeRemoteModule(host, moduleName, params)
		}

		if task.Until == nil {
			if lastErr == nil {
				return lastResult, nil
			}
		} else if e.evaluateUntil(task.Until, task.Register, lastResult, lastErr, vars) {
			return lastResult, lastErr
		}

		if attempt < attempts && task.Delay > 0 {
			time.Sleep(time.Duration(task.Delay) * time.Second)
		}
	}

	return lastResult, lastErr
}

func (e *Executor) withCheckModeParams(params map[string]interface{}, moduleName string) map[string]interface{} {
	if !e.checkMode || !supportsCheckMode(moduleName) {
		return params
	}
	cloned := make(map[string]interface{}, len(params)+1)
	for key, value := range params {
		cloned[key] = value
	}
	cloned["__check_mode"] = true
	return cloned
}

func (e *Executor) withDiffModeParams(params map[string]interface{}, moduleName string) map[string]interface{} {
	if !e.diffMode || !supportsDiffMode(moduleName) {
		return params
	}
	cloned := make(map[string]interface{}, len(params)+1)
	for key, value := range params {
		cloned[key] = value
	}
	cloned["__diff"] = true
	return cloned
}

func supportsCheckMode(moduleName string) bool {
	switch moduleName {
	case "copy", "template", "file", "lineinfile", "replace":
		return true
	default:
		return false
	}
}

func supportsDiffMode(moduleName string) bool {
	switch moduleName {
	case "copy", "template", "lineinfile", "replace":
		return true
	default:
		return false
	}
}

func (e *Executor) executeRemoteModule(host *inventory.Host, moduleName string, params map[string]interface{}) (*module.Result, error) {
	params, env := splitEnvironmentParams(params)
	if len(env) > 0 {
		params = applyEnvironmentToModuleParams(moduleName, params, env)
	}

	mod, err := e.registry.Get(moduleName)
	if err != nil {
		if customPath, found := e.findCustomModule(moduleName); found {
			return e.executeCustomModule(host, customPath, params, false)
		}
		return nil, fmt.Errorf("module not found: %s", moduleName)
	}

	client, err := e.getOrCreateClientWithErr(host)
	if err != nil {
		return nil, err
	}

	return mod.Execute(client, params)
}

func (e *Executor) executeLocalModule(moduleName string, params map[string]interface{}) (*module.Result, error) {
	params, env := splitEnvironmentParams(params)
	switch moduleName {
	case "shell":
		cmd := exec.Command("/bin/sh", "-c", GetString(params, "_raw_params", GetString(params, "cmd", "")))
		cmd.Env = mergeLocalEnv(env)
		output, err := cmd.CombinedOutput()
		return localResult(output, err), localError(err)
	case "command":
		raw := GetString(params, "_raw_params", GetString(params, "cmd", ""))
		parts := strings.Fields(raw)
		if len(parts) == 0 {
			return nil, fmt.Errorf("command module requires _raw_params or cmd")
		}
		cmd := exec.Command(parts[0], parts[1:]...)
		cmd.Env = mergeLocalEnv(env)
		output, err := cmd.CombinedOutput()
		return localResult(output, err), localError(err)
	default:
		if _, err := e.registry.Get(moduleName); err != nil {
			if customPath, found := e.findCustomModule(moduleName); found {
				return e.executeCustomModule(&inventory.Host{
					Name:       "localhost",
					Address:    "localhost",
					Connection: "local",
				}, customPath, params, true)
			}
		}
		localHost := &inventory.Host{
			Name:       "localhost",
			Address:    "localhost",
			Connection: "local",
		}
		return e.executeRemoteModule(localHost, moduleName, params)
	}
}

func (e *Executor) findCustomModule(moduleName string) (string, bool) {
	candidates := make([]string, 0)
	if e.baseDir != "" {
		candidates = append(candidates,
			filepath.Join(e.baseDir, "library", moduleName),
			filepath.Join(e.baseDir, "library", moduleName+".sh"),
			filepath.Join(e.baseDir, "library", moduleName+".py"),
			filepath.Join(e.baseDir, "plugins", "modules", moduleName),
			filepath.Join(e.baseDir, "plugins", "modules", moduleName+".sh"),
			filepath.Join(e.baseDir, "plugins", "modules", moduleName+".py"),
		)
	}
	for _, rp := range e.rolePaths {
		if rp != "" {
			candidates = append(candidates,
				filepath.Join(rp, "library", moduleName),
				filepath.Join(rp, "library", moduleName+".sh"),
				filepath.Join(rp, "library", moduleName+".py"),
			)
		}
	}
	for _, path := range candidates {
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return path, true
		}
	}
	return "", false
}

func (e *Executor) executeCustomModule(host *inventory.Host, customPath string, params map[string]interface{}, localAction bool) (*module.Result, error) {
	paramJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal custom module params: %w", err)
	}

	isLocal := localAction || host == nil || host.Connection == "local" || host.Name == "localhost"

	if isLocal {
		tmpFile, err := os.CreateTemp("", "ansible_args_*.json")
		if err != nil {
			return nil, fmt.Errorf("create args temp file: %w", err)
		}
		defer os.Remove(tmpFile.Name())
		if _, err := tmpFile.Write(paramJSON); err != nil {
			return nil, fmt.Errorf("write args temp file: %w", err)
		}
		tmpFile.Close()

		var cmd *exec.Cmd
		if strings.HasSuffix(customPath, ".py") {
			cmd = exec.Command("python3", customPath, tmpFile.Name())
		} else if strings.HasSuffix(customPath, ".sh") {
			cmd = exec.Command("/bin/sh", customPath, tmpFile.Name())
		} else {
			cmd = exec.Command(customPath, tmpFile.Name())
		}

		output, err := cmd.CombinedOutput()
		return parseCustomModuleOutput(output, err)
	}

	// Remote execution
	client := e.getOrCreateClient(host)
	if client == nil {
		return nil, fmt.Errorf("failed to create client for custom module")
	}

	randSuffix := fmt.Sprintf("%d_%d", time.Now().Unix(), time.Now().Nanosecond()%10000)
	remoteScript := fmt.Sprintf("/tmp/.ans_mod_%s_%s", filepath.Base(customPath), randSuffix)
	remoteArgs := fmt.Sprintf("/tmp/.ans_args_%s_%s.json", filepath.Base(customPath), randSuffix)

	if err := client.Upload(customPath, remoteScript); err != nil {
		return nil, fmt.Errorf("upload custom module %s: %w", customPath, err)
	}
	defer client.Execute(fmt.Sprintf("rm -f '%s' '%s'", remoteScript, remoteArgs))

	localTmp, err := os.CreateTemp("", "ansible_args_*.json")
	if err != nil {
		return nil, err
	}
	defer os.Remove(localTmp.Name())
	localTmp.Write(paramJSON)
	localTmp.Close()

	if err := client.Upload(localTmp.Name(), remoteArgs); err != nil {
		return nil, fmt.Errorf("upload custom module args: %w", err)
	}

	execCmd := fmt.Sprintf("chmod +x '%s' && '%s' '%s'", remoteScript, remoteScript, remoteArgs)
	if strings.HasSuffix(customPath, ".py") {
		execCmd = fmt.Sprintf("python3 '%s' '%s'", remoteScript, remoteArgs)
	} else if strings.HasSuffix(customPath, ".sh") {
		execCmd = fmt.Sprintf("/bin/sh '%s' '%s'", remoteScript, remoteArgs)
	}

	res, err := client.Execute(execCmd)
	if res == nil {
		return nil, err
	}
	return parseCustomModuleOutput([]byte(res.Stdout+"\n"+res.Stderr), err)
}

func parseCustomModuleOutput(output []byte, execErr error) (*module.Result, error) {
	raw := strings.TrimSpace(string(output))
	res := &module.Result{
		Stdout:  raw,
		Changed: false,
	}

	startIdx := strings.Index(raw, "{")
	endIdx := strings.LastIndex(raw, "}")
	if startIdx >= 0 && endIdx > startIdx {
		jsonSub := raw[startIdx : endIdx+1]
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonSub), &data); err == nil {
			res.Data = data
			if c, ok := data["changed"].(bool); ok {
				res.Changed = c
			} else {
				res.Changed = execErr == nil
			}
			if f, ok := data["failed"].(bool); ok {
				res.Failed = f
			} else if execErr != nil {
				res.Failed = true
			}
			if msg, ok := data["msg"].(string); ok {
				res.Message = msg
			} else if msg, ok := data["message"].(string); ok {
				res.Message = msg
			}
			if stdout, ok := data["stdout"].(string); ok {
				res.Stdout = stdout
			}
			if stderr, ok := data["stderr"].(string); ok {
				res.Stderr = stderr
			}
			if rc, ok := data["rc"].(float64); ok {
				res.ExitCode = int(rc)
			}
			return res, nil
		}
	}

	if execErr != nil {
		res.Failed = true
		res.Stderr = raw
		res.Message = execErr.Error()
		return res, execErr
	}
	res.Changed = true
	res.Message = raw
	return res, nil
}

func localResult(output []byte, err error) *module.Result {
	result := &module.Result{
		Stdout:  strings.TrimSpace(string(output)),
		Changed: err == nil,
	}
	if err != nil {
		result.Stderr = strings.TrimSpace(string(output))
	}
	return result
}

func localError(err error) error {
	if err == nil {
		return nil
	}
	return err
}

func (e *Executor) evaluateUntil(until interface{}, registerName string, result *module.Result, execErr error, vars map[string]interface{}) bool {
	evalVars := make(map[string]interface{}, len(vars)+2)
	for k, v := range vars {
		evalVars[k] = v
	}

	status := map[string]interface{}{
		"changed": result != nil && result.Changed,
		"stdout":  "",
		"stderr":  "",
		"rc":      0,
		"failed":  execErr != nil,
	}
	if result != nil {
		status["stdout"] = result.Stdout
		status["stderr"] = result.Stderr
		status["rc"] = result.ExitCode
		status["msg"] = result.Message
		mergeMap(status, result.Data)
	}
	evalVars["result"] = status
	if registerName != "" {
		evalVars[registerName] = status
	}

	return e.evaluateWhen(until, evalVars)
}

func isUnreachableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	// 如果是命令在目标节点正常执行后返回的非零退出码（如 local/docker 执行退出），绝非网络连接不可达
	if strings.Contains(msg, "command execution failed with exit code") ||
		strings.Contains(msg, "docker exec failed with exit code") {
		return false
	}

	return strings.Contains(msg, "unreachable") ||
		strings.Contains(msg, "failed to connect to") ||
		strings.Contains(msg, "failed to create ssh client") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "connection timed out") ||
		strings.Contains(msg, "timed out waiting for connection") ||
		strings.Contains(msg, "dial tcp") ||
		strings.Contains(msg, "no route to host") ||
		strings.Contains(msg, "host is down") ||
		strings.Contains(msg, "network is unreachable") ||
		strings.Contains(msg, "handshake failed") ||
		strings.Contains(msg, "connection closed") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connect to ")
}

func (e *Executor) applyExecutionOutcome(result *TaskResult, task *Task, modResult *module.Result, err error) {
	unreachable := isUnreachableError(err)

	if unreachable {
		result.Unreachable = true
		if task.IgnoreUnreachable {
			result.Failed = false
			result.Ignored = true
			if err != nil {
				result.Message = fmt.Sprintf("Host unreachable (ignored): %v", err)
			} else {
				result.Message = "Host unreachable (ignored)"
			}
			return
		}
	}

	if err != nil {
		e.log("Module execution error: %v", err)
		if !task.IgnoreError {
			result.Failed = true
			result.Message = err.Error()
		} else {
			result.Ignored = true
			result.Message = fmt.Sprintf("ignored error: %v", err)
		}
	}
	if modResult != nil && modResult.IsFailed() {
		if !task.IgnoreError {
			result.Failed = true
			if modResult.Message != "" {
				result.Message = modResult.Message
			} else if result.Message == "" {
				if modResult.Stderr != "" {
					result.Message = modResult.Stderr
				} else {
					result.Message = fmt.Sprintf("command failed with exit code %d", modResult.ExitCode)
				}
			}
		} else {
			result.Ignored = true
			if modResult.Message != "" {
				result.Message = fmt.Sprintf("ignored: %s", modResult.Message)
			} else if result.Message == "" {
				result.Message = fmt.Sprintf("ignored error: exit code %d", modResult.ExitCode)
			}
		}
	}
	if modResult != nil && modResult.Data != nil {
		if skipped, ok := modResult.Data["skipped"].(bool); ok && skipped {
			result.Skipped = true
		}
	}
	if modResult != nil && modResult.Message != "" && result.Message == "" {
		result.Message = modResult.Message
	}
}

func (e *Executor) applyTaskResultConditions(result *TaskResult, task *Task, vars map[string]interface{}) {
	if result == nil || task == nil {
		return
	}

	conditionVars := make(map[string]interface{}, len(vars)+2)
	for k, v := range vars {
		conditionVars[k] = v
	}
	status := map[string]interface{}{
		"changed":     result.Changed,
		"failed":      result.Failed,
		"unreachable": result.Unreachable,
		"stdout":      result.Stdout,
		"stderr":      result.Stderr,
		"rc":          result.ExitCode,
		"msg":         result.Message,
		"skipped":     result.Skipped,
	}
	conditionVars["result"] = status
	if task.Register != "" {
		conditionVars[task.Register] = status
	}

	if task.ChangedWhen != nil {
		result.Changed = e.evaluateWhen(task.ChangedWhen, conditionVars)
		status["changed"] = result.Changed
	}
	if task.FailedWhen != nil {
		result.Failed = e.evaluateWhen(task.FailedWhen, conditionVars)
		status["failed"] = result.Failed
		if result.Failed && result.Message == "" {
			result.Message = "failed_when condition matched"
		}
	}
}

func (e *Executor) resolveDelegateHost(delegateTo string, vars map[string]interface{}) (*inventory.Host, bool, error) {
	resolved := strings.TrimSpace(e.resolveString(delegateTo, vars))
	if resolved == "" {
		return nil, false, fmt.Errorf("delegate_to resolved to empty host")
	}
	if isLocalDelegate(resolved, nil) {
		return nil, true, nil
	}

	host, err := e.inventory.GetHost(resolved)
	if err == nil {
		return host, false, nil
	}
	return nil, false, fmt.Errorf("delegate_to host not found: %s", resolved)
}

func (e *Executor) resolveEnvironment(env map[string]string, vars map[string]interface{}) map[string]string {
	if len(env) == 0 {
		return nil
	}

	resolved := make(map[string]string, len(env))
	for k, v := range env {
		resolved[k] = e.resolveString(v, vars)
	}
	return resolved
}

func (e *Executor) withEnvironmentParams(params map[string]interface{}, env map[string]string, moduleName string) map[string]interface{} {
	if len(env) == 0 || !supportsEnvironment(moduleName) {
		return params
	}

	cloned := make(map[string]interface{}, len(params)+1)
	for k, v := range params {
		cloned[k] = v
	}
	cloned["__environment"] = env
	return cloned
}

func supportsEnvironment(moduleName string) bool {
	switch moduleName {
	case "shell", "command":
		return true
	default:
		return false
	}
}

func splitEnvironmentParams(params map[string]interface{}) (map[string]interface{}, map[string]string) {
	if params == nil {
		return nil, nil
	}

	cloned := make(map[string]interface{}, len(params))
	var env map[string]string
	for k, v := range params {
		if k == "__environment" {
			switch typed := v.(type) {
			case map[string]string:
				env = typed
			case map[string]interface{}:
				env = make(map[string]string, len(typed))
				for ek, ev := range typed {
					env[ek] = fmt.Sprintf("%v", ev)
				}
			}
			continue
		}
		cloned[k] = v
	}
	return cloned, env
}

func applyEnvironmentToModuleParams(moduleName string, params map[string]interface{}, env map[string]string) map[string]interface{} {
	if len(env) == 0 || !supportsEnvironment(moduleName) {
		return params
	}

	envPrefix := buildEnvPrefix(env)
	cloned := make(map[string]interface{}, len(params))
	for k, v := range params {
		cloned[k] = v
	}

	raw := GetString(cloned, "_raw_params", "")
	if raw == "" {
		raw = GetString(cloned, "cmd", "")
	}
	if raw == "" {
		return cloned
	}

	prefixed := envPrefix + raw
	if _, ok := cloned["_raw_params"]; ok {
		cloned["_raw_params"] = prefixed
	} else {
		cloned["cmd"] = prefixed
	}
	return cloned
}

func buildEnvPrefix(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}

	parts := make([]string, 0, len(env))
	for key, value := range env {
		escaped := strings.ReplaceAll(value, `'`, `'"'"'`)
		parts = append(parts, fmt.Sprintf("%s='%s'", key, escaped))
	}
	return "env " + strings.Join(parts, " ") + " "
}

func mergeLocalEnv(env map[string]string) []string {
	merged := make([]string, 0, len(env)+len(os.Environ()))
	merged = append(merged, os.Environ()...)
	for key, value := range env {
		merged = append(merged, key+"="+value)
	}
	return merged
}

func (e *Executor) buildHostBatches(hosts []*inventory.Host, serial interface{}) [][]*inventory.Host {
	batchSize := len(hosts)
	if size := resolveSerialBatchSize(serial, len(hosts)); size > 0 {
		batchSize = size
	}
	if batchSize <= 0 || batchSize >= len(hosts) {
		return [][]*inventory.Host{hosts}
	}

	batches := make([][]*inventory.Host, 0, (len(hosts)+batchSize-1)/batchSize)
	for i := 0; i < len(hosts); i += batchSize {
		end := i + batchSize
		if end > len(hosts) {
			end = len(hosts)
		}
		batches = append(batches, hosts[i:end])
	}
	return batches
}

func resolveSerialBatchSize(serial interface{}, total int) int {
	if total <= 0 {
		return 0
	}

	switch v := serial.(type) {
	case nil:
		return total
	case int:
		if v > 0 && v < total {
			return v
		}
	case float64:
		size := int(v)
		if size > 0 && size < total {
			return size
		}
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return total
		}
		if strings.HasSuffix(s, "%") {
			pct := parsePositiveInt(strings.TrimSuffix(s, "%"))
			if pct <= 0 {
				return total
			}
			size := (total*pct + 99) / 100
			if size < 1 {
				size = 1
			}
			if size > total {
				size = total
			}
			return size
		}
		size := parsePositiveInt(s)
		if size > 0 && size < total {
			return size
		}
	}

	return total
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
	if maxFailPct <= 0 || totalHosts <= 0 {
		return false
	}

	failedHosts := 0
	for _, hostResult := range result.Hosts {
		for _, taskResult := range hostResult.Tasks {
			if taskResult.Failed {
				failedHosts++
				break
			}
		}
	}

	return failedHosts*100 > maxFailPct*totalHosts
}

func isLocalDelegate(delegateTo string, vars map[string]interface{}) bool {
	if delegateTo == "" {
		return false
	}
	resolved := strings.TrimSpace(delegateTo)
	if vars != nil {
		resolved = strings.TrimSpace((&Executor{}).resolveString(delegateTo, vars))
	}
	switch resolved {
	case "localhost", "127.0.0.1", "::1", "local":
		return true
	default:
		return false
	}
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

// gatherFacts 收集主机信息
func (e *Executor) gatherFacts(hosts []*inventory.Host) {
	mod, err := e.registry.Get("setup")
	if err != nil {
		return
	}

	forks := e.GetForks()
	if forks <= 0 {
		forks = 5
	}
	sem := make(chan struct{}, forks)
	var wg sync.WaitGroup

	for _, host := range hosts {
		wg.Add(1)
		sem <- struct{}{}
		go func(h *inventory.Host) {
			defer wg.Done()
			defer func() { <-sem }()

			if cached, found := e.getCachedFacts(h.Name); found {
				e.mu.Lock()
				e.facts[h.Name] = cached
				e.mu.Unlock()
				return
			}

			client := e.getOrCreateClient(h)
			if client == nil {
				return
			}

			result, err := mod.Execute(client, nil)
			if err == nil && result != nil {
				parsed := e.parseFacts(result.Stdout)
				e.saveCachedFacts(h.Name, parsed)
			}
		}(host)
	}
	wg.Wait()
}

// parseFacts 解析 facts 输出
func parseFacts(output string) map[string]interface{} {
	trimmed := strings.TrimSpace(output)
	if idx := strings.Index(trimmed, "gathered_facts:"); idx >= 0 {
		var wrapper struct {
			GatheredFacts map[string]interface{} `yaml:"gathered_facts"`
		}
		if err := yaml.Unmarshal([]byte(trimmed[idx:]), &wrapper); err == nil && wrapper.GatheredFacts != nil {
			return wrapper.GatheredFacts
		}
	}

	facts := make(map[string]interface{})
	// 简单解析 YAML 格式的 facts (回退处理)
	lines := splitLines(output)
	inFacts := false
	var currentParentMap map[string]interface{}
	for _, line := range lines {
		if line == "gathered_facts:" {
			inFacts = true
			continue
		}
		if inFacts {
			if len(line) > 4 && line[0:4] == "    " && currentParentMap != nil {
				subLine := strings.TrimSpace(line[4:])
				if colon := strings.Index(subLine, ":"); colon != -1 {
					subKey := strings.TrimSpace(subLine[:colon])
					subVal := strings.Trim(strings.TrimSpace(subLine[colon+1:]), "\"'")
					currentParentMap[subKey] = subVal
				}
				continue
			}
			if len(line) > 2 && line[0:2] == "  " {
				currentParentMap = nil
				colon := strings.Index(line[2:], ":")
				if colon != -1 {
					key := strings.TrimSpace(line[2 : 2+colon])
					val := ""
					if 2+colon+1 < len(line) {
						val = strings.Trim(strings.TrimSpace(line[2+colon+1:]), "\"'")
					}
					if val == "" {
						subMap := make(map[string]interface{})
						facts[key] = subMap
						currentParentMap = subMap
					} else {
						facts[key] = val
					}
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
		v = strings.TrimSpace(v)
		for strings.HasPrefix(v, "(") && strings.HasSuffix(v, ")") && isMatchedParentheses(v[1:len(v)-1]) {
			v = strings.TrimSpace(v[1 : len(v)-1])
		}
		if parts, ok := splitConditionExpression(v, " or "); ok {
			for _, part := range parts {
				if e.evaluateWhen(part, vars) {
					return true
				}
			}
			return false
		}
		if parts, ok := splitConditionExpression(v, " and "); ok {
			for _, part := range parts {
				if !e.evaluateWhen(part, vars) {
					return false
				}
			}
			return true
		}
		if comparison, ok := e.evaluateComparisonCondition(v, vars); ok {
			return comparison
		}
		if strings.HasPrefix(v, "not ") {
			return !e.evaluateWhen(strings.TrimSpace(strings.TrimPrefix(v, "not ")), vars)
		}
		// 简单条件评估
		if v == "true" || v == "yes" {
			return true
		}
		if v == "false" || v == "no" {
			return false
		}
		// 检查管道过滤器，如 when: val | bool 或 when: list | length > 0
		if strings.Contains(v, "|") {
			if val, ok := e.resolveFilteredExpr(v, vars); ok {
				return truthyValue(val)
			}
		}
		// 检查变量
		if val, ok := e.lookupVarExpr(v, vars); ok {
			return truthyValue(val)
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

func (e *Executor) evaluateComparisonCondition(expr string, vars map[string]interface{}) (bool, bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return false, false
	}

	if strings.HasSuffix(expr, " is not defined") {
		name := strings.TrimSpace(strings.TrimSuffix(expr, " is not defined"))
		_, ok := e.lookupVarExpr(name, vars)
		return !ok, true
	}
	if strings.HasSuffix(expr, " is defined") {
		name := strings.TrimSpace(strings.TrimSuffix(expr, " is defined"))
		_, ok := e.lookupVarExpr(name, vars)
		return ok, true
	}

	// 支持 Ansible 核心状态测试: is succeeded / is failed / is unreachable / is skipped
	for _, suffix := range []string{" is succeeded", " is success", " is successful"} {
		if strings.HasSuffix(expr, suffix) {
			name := strings.TrimSpace(strings.TrimSuffix(expr, suffix))
			val, ok := e.lookupVarExpr(name, vars)
			return ok && isTaskResultSucceeded(val), true
		}
	}
	for _, suffix := range []string{" is not succeeded", " is not success", " is not successful"} {
		if strings.HasSuffix(expr, suffix) {
			name := strings.TrimSpace(strings.TrimSuffix(expr, suffix))
			val, ok := e.lookupVarExpr(name, vars)
			return !ok || !isTaskResultSucceeded(val), true
		}
	}
	for _, suffix := range []string{" is failed", " is failure"} {
		if strings.HasSuffix(expr, suffix) {
			name := strings.TrimSpace(strings.TrimSuffix(expr, suffix))
			val, ok := e.lookupVarExpr(name, vars)
			return ok && isTaskResultFailed(val), true
		}
	}
	for _, suffix := range []string{" is not failed", " is not failure"} {
		if strings.HasSuffix(expr, suffix) {
			name := strings.TrimSpace(strings.TrimSuffix(expr, suffix))
			val, ok := e.lookupVarExpr(name, vars)
			return !ok || !isTaskResultFailed(val), true
		}
	}
	if strings.HasSuffix(expr, " is unreachable") {
		name := strings.TrimSpace(strings.TrimSuffix(expr, " is unreachable"))
		val, ok := e.lookupVarExpr(name, vars)
		return ok && isTaskResultUnreachable(val), true
	}
	if strings.HasSuffix(expr, " is skipped") {
		name := strings.TrimSpace(strings.TrimSuffix(expr, " is skipped"))
		val, ok := e.lookupVarExpr(name, vars)
		return ok && isTaskResultSkipped(val), true
	}
	if idx := strings.Index(expr, " not in "); idx >= 0 {
		left, leftOK := e.resolveConditionOperand(strings.TrimSpace(expr[:idx]), vars)
		right, rightOK := e.resolveConditionOperand(strings.TrimSpace(expr[idx+len(" not in "):]), vars)
		if !leftOK || !rightOK {
			return false, false
		}
		return !conditionContains(right, left), true
	}
	if idx := strings.Index(expr, " in "); idx >= 0 {
		left, leftOK := e.resolveConditionOperand(strings.TrimSpace(expr[:idx]), vars)
		right, rightOK := e.resolveConditionOperand(strings.TrimSpace(expr[idx+len(" in "):]), vars)
		if !leftOK || !rightOK {
			return false, false
		}
		return conditionContains(right, left), true
	}

	for _, op := range []string{"==", "!=", ">=", "<=", ">", "<"} {
		if idx := strings.Index(expr, op); idx >= 0 {
			left, leftOK := e.resolveConditionOperand(strings.TrimSpace(expr[:idx]), vars)
			right, rightOK := e.resolveConditionOperand(strings.TrimSpace(expr[idx+len(op):]), vars)
			if !leftOK || !rightOK {
				return false, false
			}
			return compareConditionValues(left, right, op), true
		}
	}

	return false, false
}

func (e *Executor) resolveConditionOperand(operand string, vars map[string]interface{}) (interface{}, bool) {
	operand = strings.TrimSpace(operand)
	if operand == "" {
		return "", true
	}
	for strings.HasPrefix(operand, "(") && strings.HasSuffix(operand, ")") && isMatchedParentheses(operand[1:len(operand)-1]) {
		operand = strings.TrimSpace(operand[1 : len(operand)-1])
	}
	if quoted, ok := parseQuotedConditionOperand(operand); ok {
		return quoted, true
	}

	switch strings.ToLower(operand) {
	case "true", "yes":
		return true, true
	case "false", "no":
		return false, true
	case "nil", "null":
		return nil, true
	}

	if i, err := strconv.Atoi(operand); err == nil {
		return i, true
	}
	if f, err := strconv.ParseFloat(operand, 64); err == nil {
		return f, true
	}
	// 支持三元表达式作为操作数，如 ('master' if is_master else 'worker') == 'master'
	if val, ok := e.resolveTernaryExpr(operand, vars); ok {
		return val, true
	}
	// 支持管道过滤器，如 (list | length) > 0
	if strings.Contains(operand, "|") {
		if val, ok := e.resolveFilteredExpr(operand, vars); ok {
			return val, true
		}
	}
	if val, ok := e.lookupVarExpr(operand, vars); ok {
		return val, true
	}

	return operand, true
}

func parseQuotedConditionOperand(operand string) (string, bool) {
	if len(operand) < 2 {
		return "", false
	}
	quote := operand[0]
	if quote != '\'' && quote != '"' {
		return "", false
	}
	if operand[len(operand)-1] != quote {
		return "", false
	}
	for i := 1; i < len(operand)-1; i++ {
		if operand[i] == '\\' {
			i++
			continue
		}
		if operand[i] == quote {
			return "", false
		}
	}
	return operand[1 : len(operand)-1], true
}

func compareConditionValues(left, right interface{}, op string) bool {
	if lf, ok := toFloat64(left); ok {
		if rf, ok := toFloat64(right); ok {
			switch op {
			case "==":
				return lf == rf
			case "!=":
				return lf != rf
			case ">":
				return lf > rf
			case "<":
				return lf < rf
			case ">=":
				return lf >= rf
			case "<=":
				return lf <= rf
			}
		}
	}

	ls := fmt.Sprintf("%v", left)
	rs := fmt.Sprintf("%v", right)
	switch op {
	case "==":
		return ls == rs
	case "!=":
		return ls != rs
	case ">":
		return ls > rs
	case "<":
		return ls < rs
	case ">=":
		return ls >= rs
	case "<=":
		return ls <= rs
	default:
		return false
	}
}

func conditionContains(container, item interface{}) bool {
	switch v := container.(type) {
	case []interface{}:
		for _, entry := range v {
			if compareConditionValues(entry, item, "==") {
				return true
			}
		}
		return false
	case []string:
		for _, entry := range v {
			if compareConditionValues(entry, item, "==") {
				return true
			}
		}
		return false
	case string:
		return strings.Contains(v, fmt.Sprintf("%v", item))
	default:
		return strings.Contains(fmt.Sprintf("%v", container), fmt.Sprintf("%v", item))
	}
}

func isTaskResultSucceeded(val interface{}) bool {
	if val == nil {
		return false
	}
	switch m := val.(type) {
	case map[string]interface{}:
		if failed, ok := m["failed"]; ok && extractBool(failed) {
			return false
		}
		if unreach, ok := m["unreachable"]; ok && extractBool(unreach) {
			return false
		}
		if rc, ok := m["rc"]; ok {
			if rci, ok := toFloat64(rc); ok && int(rci) != 0 {
				return false
			}
		}
		return true
	case *TaskResult:
		return !m.Failed && !m.Unreachable && (m.ExitCode == 0 || m.Skipped)
	case *module.Result:
		return !m.IsFailed()
	case bool:
		return m
	default:
		return true
	}
}

func isTaskResultFailed(val interface{}) bool {
	if val == nil {
		return false
	}
	switch m := val.(type) {
	case map[string]interface{}:
		if failed, ok := m["failed"]; ok && extractBool(failed) {
			return true
		}
		if rc, ok := m["rc"]; ok {
			if rci, ok := toFloat64(rc); ok && int(rci) != 0 {
				return true
			}
		}
		return false
	case *TaskResult:
		return m.Failed || m.ExitCode != 0
	case *module.Result:
		return m.IsFailed()
	case bool:
		return !m
	default:
		return false
	}
}

func isTaskResultUnreachable(val interface{}) bool {
	if val == nil {
		return false
	}
	switch m := val.(type) {
	case map[string]interface{}:
		if unreach, ok := m["unreachable"]; ok {
			return extractBool(unreach)
		}
		return false
	case *TaskResult:
		return m.Unreachable
	default:
		return false
	}
}

func isTaskResultSkipped(val interface{}) bool {
	if val == nil {
		return false
	}
	switch m := val.(type) {
	case map[string]interface{}:
		if skipped, ok := m["skipped"]; ok {
			return extractBool(skipped)
		}
		return false
	case *TaskResult:
		return m.Skipped
	default:
		return false
	}
}

func splitConditionExpression(expr, sep string) ([]string, bool) {
	var (
		parts   []string
		current strings.Builder
		quote   byte
		depth   int
		found   bool
	)

	for i := 0; i < len(expr); i++ {
		ch := expr[i]
		switch {
		case quote != 0:
			current.WriteByte(ch)
			if ch == quote {
				quote = 0
			}
		case ch == '\'' || ch == '"':
			quote = ch
			current.WriteByte(ch)
		case ch == '(':
			depth++
			current.WriteByte(ch)
		case ch == ')':
			if depth > 0 {
				depth--
			}
			current.WriteByte(ch)
		case depth == 0 && strings.HasPrefix(expr[i:], sep):
			parts = append(parts, strings.TrimSpace(current.String()))
			current.Reset()
			found = true
			i += len(sep) - 1
		default:
			current.WriteByte(ch)
		}
	}

	if !found {
		return nil, false
	}
	parts = append(parts, strings.TrimSpace(current.String()))
	return parts, true
}

func toFloat64(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

// evaluateLoop 评估 loop
func (e *Executor) evaluateLoop(loop interface{}, vars map[string]interface{}) []interface{} {
	value := e.resolveLoopValue(loop, vars)

	switch v := value.(type) {
	case nil:
		return nil
	case []interface{}:
		return v
	case []string:
		result := make([]interface{}, 0, len(v))
		for _, item := range v {
			result = append(result, item)
		}
		return result
	case map[string]interface{}:
		if pattern, ok := fileglobPattern(v); ok {
			return expandFileglob(pattern, e.baseDir)
		}
		if sequence, ok := sequenceSpec(v); ok {
			return expandSequence(sequence)
		}
		if together, ok := togetherSpec(v); ok {
			return e.expandTogether(together, vars)
		}
		if nested, ok := nestedSpec(v); ok {
			return e.expandNested(nested, vars)
		}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		result := make([]interface{}, 0, len(keys))
		for _, key := range keys {
			result = append(result, map[string]interface{}{
				"key":   key,
				"value": v[key],
			})
		}
		return result
	case map[string]string:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		result := make([]interface{}, 0, len(keys))
		for _, key := range keys {
			result = append(result, map[string]interface{}{
				"key":   key,
				"value": v[key],
			})
		}
		return result
	case map[interface{}]interface{}:
		keys := make([]string, 0, len(v))
		values := make(map[string]interface{}, len(v))
		for key, val := range v {
			keyStr := fmt.Sprintf("%v", key)
			keys = append(keys, keyStr)
			values[keyStr] = val
		}
		sort.Strings(keys)
		result := make([]interface{}, 0, len(keys))
		for _, key := range keys {
			result = append(result, map[string]interface{}{
				"key":   key,
				"value": values[key],
			})
		}
		return result
	default:
		return []interface{}{v}
	}
}

func (e *Executor) resolveLoopValue(loop interface{}, vars map[string]interface{}) interface{} {
	switch v := loop.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if strings.HasPrefix(trimmed, "{{") && strings.HasSuffix(trimmed, "}}") {
			expr := strings.TrimSpace(trimmed[2 : len(trimmed)-2])
			res := e.resolveVarExpr(expr, vars)
			if resStr, isStr := res.(string); !isStr || (resStr != "{{ "+expr+" }}" && resStr != trimmed) {
				return res
			}
			if resolved, ok := e.lookupVarExpr(expr, vars); ok {
				return resolved
			}
		}
		if val, ok := vars[v]; ok {
			return val
		}
		return v
	default:
		return loop
	}
}

func fileglobPattern(value map[string]interface{}) (string, bool) {
	if len(value) != 1 {
		return "", false
	}
	pattern, ok := value["__with_fileglob__"]
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%v", pattern), true
}

func sequenceSpec(value map[string]interface{}) (string, bool) {
	if len(value) != 1 {
		return "", false
	}
	spec, ok := value["__with_sequence__"]
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%v", spec), true
}

func togetherSpec(value map[string]interface{}) (interface{}, bool) {
	if len(value) != 1 {
		return nil, false
	}
	spec, ok := value["__with_together__"]
	return spec, ok
}

func nestedSpec(value map[string]interface{}) (interface{}, bool) {
	if len(value) != 1 {
		return nil, false
	}
	spec, ok := value["__with_nested__"]
	return spec, ok
}

func expandFileglob(pattern string, baseDir string) []interface{} {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil
	}

	resolvedPattern := pattern
	if !filepath.IsAbs(resolvedPattern) && baseDir != "" {
		resolvedPattern = filepath.Join(baseDir, resolvedPattern)
	}

	matches, err := filepath.Glob(resolvedPattern)
	if err != nil {
		return []interface{}{}
	}
	sort.Strings(matches)

	result := make([]interface{}, 0, len(matches))
	for _, match := range matches {
		result = append(result, match)
	}
	return result
}

func expandSequence(spec string) []interface{} {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil
	}

	args, err := util.SplitArgs(spec)
	if err != nil {
		return nil
	}

	start := 0
	end := 0
	stride := 1
	format := "%d"
	startSet := false
	endSet := false

	for _, arg := range args {
		key, value, ok := strings.Cut(arg, "=")
		if !ok {
			continue
		}

		switch strings.ToLower(strings.TrimSpace(key)) {
		case "start":
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil
			}
			start = parsed
			startSet = true
		case "end":
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil
			}
			end = parsed
			endSet = true
		case "stride":
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || parsed == 0 {
				return nil
			}
			stride = parsed
		case "format":
			if strings.TrimSpace(value) != "" {
				format = value
			}
		}
	}

	if !startSet || !endSet {
		return nil
	}

	if start < end && stride < 0 {
		stride = -stride
	}
	if start > end && stride > 0 {
		stride = -stride
	}

	result := make([]interface{}, 0)
	for i := start; ; i += stride {
		if stride > 0 && i > end {
			break
		}
		if stride < 0 && i < end {
			break
		}
		result = append(result, fmt.Sprintf(format, i))
	}
	return result
}

func (e *Executor) expandTogether(spec interface{}, vars map[string]interface{}) []interface{} {
	lists := e.loopListSet(spec, vars)
	if len(lists) == 0 {
		return nil
	}

	maxLen := 0
	for _, list := range lists {
		if len(list) > maxLen {
			maxLen = len(list)
		}
	}

	result := make([]interface{}, 0, maxLen)
	for i := 0; i < maxLen; i++ {
		row := make([]interface{}, 0, len(lists))
		for _, list := range lists {
			if i < len(list) {
				row = append(row, list[i])
			} else {
				row = append(row, nil)
			}
		}
		result = append(result, row)
	}
	return result
}

func (e *Executor) expandNested(spec interface{}, vars map[string]interface{}) []interface{} {
	lists := e.loopListSet(spec, vars)
	if len(lists) == 0 {
		return nil
	}

	result := []interface{}{[]interface{}{}}
	for _, list := range lists {
		next := make([]interface{}, 0, len(result)*len(list))
		for _, prefix := range result {
			prefixList, _ := prefix.([]interface{})
			for _, item := range list {
				combined := make([]interface{}, 0, len(prefixList)+1)
				combined = append(combined, prefixList...)
				combined = append(combined, item)
				next = append(next, combined)
			}
		}
		result = next
	}
	return result
}

func (e *Executor) loopListSet(spec interface{}, vars map[string]interface{}) [][]interface{} {
	items, ok := spec.([]interface{})
	if !ok {
		return nil
	}

	lists := make([][]interface{}, 0, len(items))
	for _, item := range items {
		value := e.resolveLoopListItem(item, vars)
		switch typed := value.(type) {
		case []interface{}:
			lists = append(lists, typed)
		case []string:
			list := make([]interface{}, 0, len(typed))
			for _, entry := range typed {
				list = append(list, entry)
			}
			lists = append(lists, list)
		default:
			lists = append(lists, []interface{}{typed})
		}
	}
	return lists
}

func (e *Executor) resolveLoopListItem(item interface{}, vars map[string]interface{}) interface{} {
	switch typed := item.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if strings.HasPrefix(trimmed, "{{") && strings.HasSuffix(trimmed, "}}") {
			expr := strings.TrimSpace(trimmed[2 : len(trimmed)-2])
			if resolved, ok := e.lookupVarExpr(expr, vars); ok {
				return resolved
			}
		}
	}
	return e.resolveLoopValue(item, vars)
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
		trimmed := strings.TrimSpace(val)
		if strings.HasPrefix(trimmed, "{{") && strings.HasSuffix(trimmed, "}}") && !strings.Contains(trimmed[2:len(trimmed)-2], "{{") {
			expr := strings.TrimSpace(trimmed[2 : len(trimmed)-2])
			res := e.resolveVarExpr(expr, vars)
			if resStr, isStr := res.(string); !isStr || (resStr != "{{ "+expr+" }}" && resStr != trimmed) {
				return res
			}
		}
		return e.resolveString(val, vars)
	case map[string]interface{}:
		return e.resolveVariables(val, vars)
	case rawTask:
		return e.resolveVariables(map[string]interface{}(val), vars)
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
		searchFrom := 0

		// 匹配 {{ xxx }} 或 {{xxx}}
		for {
			start := index(result[searchFrom:], "{{")
			if start == -1 {
				break
			}
			start += searchFrom
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
			replacement := fmt.Sprintf("%v", varValue)
			placeholder := result[start : end+2]

			// 未解析出的占位符保持原样，并继续向后扫描，避免卡在同一处死循环。
			if replacement == placeholder || replacement == "{{ "+varExpr+" }}" {
				searchFrom = end + 2
				continue
			}

			// 替换
			result = result[:start] + replacement + result[end+2:]
			searchFrom = start + len(replacement)
		}

		if result == oldResult {
			break
		}
	}

	return result
}

// resolveVarExpr 解析变量表达式，支持嵌套属性如 item.src
func (e *Executor) resolveVarExpr(expr string, vars map[string]interface{}) interface{} {
	if value, ok := e.resolveLookupExpr(expr, vars); ok {
		return value
	}
	if value, ok := e.resolveTernaryExpr(expr, vars); ok {
		return value
	}
	if filtered, ok := e.resolveFilteredExpr(expr, vars); ok {
		return filtered
	}
	if value, ok := e.lookupVarExpr(expr, vars); ok {
		return value
	}
	return "{{ " + expr + " }}"
}

func (e *Executor) resolveFilteredExpr(expr string, vars map[string]interface{}) (interface{}, bool) {
	parts := splitFilterSegments(expr)
	if len(parts) <= 1 {
		return nil, false
	}

	baseExpr := strings.TrimSpace(parts[0])
	for strings.HasPrefix(baseExpr, "(") && strings.HasSuffix(baseExpr, ")") && isMatchedParentheses(baseExpr[1:len(baseExpr)-1]) {
		baseExpr = strings.TrimSpace(baseExpr[1 : len(baseExpr)-1])
	}

	var baseValue interface{}
	if val, ok := e.resolveTernaryExpr(baseExpr, vars); ok {
		baseValue = val
	} else if val, ok := e.lookupVarExpr(baseExpr, vars); ok {
		baseValue = val
	} else if quoted, ok := parseQuotedConditionOperand(baseExpr); ok {
		baseValue = quoted
	} else if lit := parseFilterLiteral(baseExpr); lit != baseExpr {
		baseValue = lit
	} else {
		baseValue = nil
	}

	current := baseValue
	for _, segment := range parts[1:] {
		next, applied, err := e.applyTemplateFilter(strings.TrimSpace(segment), current)
		if err != nil || !applied {
			return nil, false
		}
		current = next
	}
	return current, true
}

func splitFilterSegments(expr string) []string {
	segments := make([]string, 0)
	var current strings.Builder
	var quote rune
	depth := 0

	for _, ch := range expr {
		switch {
		case quote != 0:
			current.WriteRune(ch)
			if ch == quote {
				quote = 0
			}
		case ch == '\'' || ch == '"':
			quote = ch
			current.WriteRune(ch)
		case ch == '(' || ch == '[' || ch == '{':
			depth++
			current.WriteRune(ch)
		case ch == ')' || ch == ']' || ch == '}':
			if depth > 0 {
				depth--
			}
			current.WriteRune(ch)
		case ch == '|' && depth == 0:
			segments = append(segments, strings.TrimSpace(current.String()))
			current.Reset()
		default:
			current.WriteRune(ch)
		}
	}

	segments = append(segments, strings.TrimSpace(current.String()))
	return segments
}

func (e *Executor) applyTemplateFilter(segment string, value interface{}) (interface{}, bool, error) {
	name := strings.TrimSpace(segment)
	args := []string{}
	if open := strings.Index(name, "("); open != -1 && strings.HasSuffix(name, ")") {
		argString := name[open+1 : len(name)-1]
		name = strings.TrimSpace(name[:open])
		args = splitFilterArgs(argString)
	}

	switch name {
	case "default":
		if len(args) == 0 {
			return value, false, fmt.Errorf("default filter requires one argument")
		}
		var filterArgs []interface{}
		if len(args) > 1 {
			arg1 := strings.TrimSpace(args[1])
			if strings.HasPrefix(strings.ToLower(arg1), "boolean=") {
				boolVal := strings.TrimSpace(arg1[len("boolean="):])
				filterArgs = []interface{}{parseFilterLiteral(args[0]), parseFilterLiteral(boolVal), value}
			} else {
				filterArgs = []interface{}{parseFilterLiteral(args[0]), parseFilterLiteral(args[1]), value}
			}
		} else {
			filterArgs = []interface{}{parseFilterLiteral(args[0]), value}
		}
		return module.TemplateDefaultValue(filterArgs...), true, nil
	case "ternary":
		if len(args) < 2 {
			return value, false, fmt.Errorf("ternary filter requires at least two arguments")
		}
		tArgs := make([]interface{}, 0, len(args)+1)
		for _, a := range args {
			tArgs = append(tArgs, parseFilterLiteral(a))
		}
		tArgs = append(tArgs, value)
		return module.TemplateTernary(tArgs...), true, nil
	case "dict2items":
		res, err := module.TemplateDict2Items(value)
		if err != nil {
			return value, false, err
		}
		return res, true, nil
	case "quote":
		return module.TemplateQuoteValue(value), true, nil
	case "upper":
		return strings.ToUpper(fmt.Sprintf("%v", value)), true, nil
	case "lower":
		return strings.ToLower(fmt.Sprintf("%v", value)), true, nil
	case "trim":
		return strings.TrimSpace(fmt.Sprintf("%v", value)), true, nil
	case "replace":
		if len(args) < 2 {
			return value, false, fmt.Errorf("replace filter requires two arguments")
		}
		return strings.ReplaceAll(fmt.Sprintf("%v", value), fmt.Sprintf("%v", parseFilterLiteral(args[0])), fmt.Sprintf("%v", parseFilterLiteral(args[1]))), true, nil
	case "join":
		if len(args) == 0 {
			return value, false, fmt.Errorf("join filter requires one argument")
		}
		return strings.Join(module.TemplateNormalizeList(value), fmt.Sprintf("%v", parseFilterLiteral(args[0]))), true, nil
	case "split":
		sep := " "
		if len(args) > 0 {
			sep = fmt.Sprintf("%v", parseFilterLiteral(args[0]))
		}
		return strings.Split(fmt.Sprintf("%v", value), sep), true, nil
	case "length":
		return templateLength(value), true, nil
	case "first":
		item, ok := templateIndex(value, 0)
		return item, ok, nil
	case "last":
		length := templateLength(value)
		if length <= 0 {
			return nil, false, nil
		}
		item, ok := templateIndex(value, length-1)
		return item, ok, nil
	case "to_json":
		data, err := json.Marshal(value)
		if err != nil {
			return value, false, err
		}
		return string(data), true, nil
	case "from_json":
		var decoded interface{}
		if err := json.Unmarshal([]byte(fmt.Sprintf("%v", value)), &decoded); err != nil {
			return value, false, err
		}
		return decoded, true, nil
	case "basename":
		return filepath.Base(fmt.Sprintf("%v", value)), true, nil
	case "dirname":
		return filepath.Dir(fmt.Sprintf("%v", value)), true, nil
	case "b64encode":
		return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%v", value))), true, nil
	case "b64decode":
		data, err := base64.StdEncoding.DecodeString(fmt.Sprintf("%v", value))
		if err != nil {
			return value, false, err
		}
		return string(data), true, nil
	case "combine":
		parsedArgs := make([]interface{}, 0, len(args)+1)
		parsedArgs = append(parsedArgs, value)
		for _, a := range args {
			parsedArgs = append(parsedArgs, parseFilterLiteral(a))
		}
		res, err := module.TemplateCombine(parsedArgs...)
		return res, err == nil, err
	case "regex_replace":
		if len(args) < 2 {
			return value, false, fmt.Errorf("regex_replace requires 2 arguments")
		}
		return module.TemplateRegexReplace(parseFilterLiteral(args[0]), parseFilterLiteral(args[1]), value), true, nil
	case "regex_search":
		if len(args) < 1 {
			return "", false, fmt.Errorf("regex_search requires 1 argument")
		}
		return module.TemplateRegexSearch(parseFilterLiteral(args[0]), value), true, nil
	case "regex_findall":
		if len(args) < 1 {
			return []string{}, false, fmt.Errorf("regex_findall requires 1 argument")
		}
		return module.TemplateRegexFindAll(parseFilterLiteral(args[0]), value), true, nil
	case "selectattr":
		parsedArgs := make([]interface{}, 0, len(args)+1)
		for _, a := range args {
			parsedArgs = append(parsedArgs, parseFilterLiteral(a))
		}
		parsedArgs = append(parsedArgs, value)
		if len(parsedArgs) == 1 {
			return []interface{}{}, false, fmt.Errorf("selectattr requires at least attribute name")
		}
		return module.TemplateSelectAttr(parsedArgs[0], parsedArgs[1:]...), true, nil
	case "rejectattr":
		parsedArgs := make([]interface{}, 0, len(args)+1)
		for _, a := range args {
			parsedArgs = append(parsedArgs, parseFilterLiteral(a))
		}
		parsedArgs = append(parsedArgs, value)
		if len(parsedArgs) == 1 {
			return []interface{}{}, false, fmt.Errorf("rejectattr requires at least attribute name")
		}
		return module.TemplateRejectAttr(parsedArgs[0], parsedArgs[1:]...), true, nil
	case "map":
		parsedArgs := make([]interface{}, 0, len(args)+1)
		for _, a := range args {
			parsedArgs = append(parsedArgs, parseFilterLiteral(a))
		}
		parsedArgs = append(parsedArgs, value)
		if len(parsedArgs) == 1 {
			return []interface{}{}, false, fmt.Errorf("map requires at least 1 argument")
		}
		return module.TemplateMap(parsedArgs[0], parsedArgs[1:]...), true, nil
	case "ipaddr":
		if len(args) == 0 {
			return module.TemplateIpAddr(value), true, nil
		}
		return module.TemplateIpAddr(parseFilterLiteral(args[0]), value), true, nil
	case "flatten":
		return module.TemplateFlatten(value), true, nil
	case "unique":
		return module.TemplateUnique(value), true, nil
	case "difference":
		if len(args) == 0 {
			return value, false, fmt.Errorf("difference requires 1 argument")
		}
		return module.TemplateDifference(parseFilterLiteral(args[0]), value), true, nil
	case "intersect":
		if len(args) == 0 {
			return value, false, fmt.Errorf("intersect requires 1 argument")
		}
		return module.TemplateIntersect(parseFilterLiteral(args[0]), value), true, nil
	case "union":
		if len(args) == 0 {
			return value, false, fmt.Errorf("union requires 1 argument")
		}
		return module.TemplateUnion(parseFilterLiteral(args[0]), value), true, nil
	case "min":
		return module.TemplateMin(value), true, nil
	case "max":
		return module.TemplateMax(value), true, nil
	case "sum":
		return module.TemplateSum(value), true, nil
	case "bool":
		return module.TemplateBool(value), true, nil
	case "int":
		if len(args) > 0 {
			return module.TemplateInt(parseFilterLiteral(args[0]), value), true, nil
		}
		return module.TemplateInt(value), true, nil
	case "float":
		if len(args) > 0 {
			return module.TemplateFloat(parseFilterLiteral(args[0]), value), true, nil
		}
		return module.TemplateFloat(value), true, nil
	case "abs":
		return module.TemplateAbs(value), true, nil
	case "hash":
		if len(args) == 0 {
			return module.TemplateHash("sha256", value), true, nil
		}
		return module.TemplateHash(parseFilterLiteral(args[0]), value), true, nil
	default:
		return nil, false, nil
	}
}

func (e *Executor) resolveLookupExpr(expr string, vars map[string]interface{}) (interface{}, bool) {
	expr = strings.TrimSpace(expr)
	if !strings.HasPrefix(expr, "lookup(") || !strings.HasSuffix(expr, ")") {
		return nil, false
	}

	args := splitFilterArgs(expr[len("lookup(") : len(expr)-1])
	if len(args) < 2 {
		return nil, false
	}

	name := fmt.Sprintf("%v", parseFilterLiteral(args[0]))
	key := fmt.Sprintf("%v", parseFilterLiteral(args[1]))
	if strings.Contains(key, "{{") {
		key = e.resolveString(key, vars)
	}

	switch strings.ToLower(strings.TrimSpace(name)) {
	case "env":
		return os.Getenv(key), true
	case "file":
		path := key
		if !filepath.IsAbs(path) && e.baseDir != "" {
			path = filepath.Join(e.baseDir, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", true
		}
		return strings.TrimRight(string(data), "\r\n"), true
	case "pipe":
		output, err := exec.Command("/bin/sh", "-c", key).Output()
		if err != nil {
			return "", true
		}
		return strings.TrimRight(string(output), "\r\n"), true
	case "template":
		path := key
		if !filepath.IsAbs(path) && e.baseDir != "" {
			path = filepath.Join(e.baseDir, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", true
		}
		rendered := e.resolveString(string(data), vars)
		return strings.TrimRight(rendered, "\r\n"), true
	case "password":
		parts := strings.Fields(key)
		if len(parts) == 0 {
			return "", true
		}
		path := parts[0]
		if !filepath.IsAbs(path) && e.baseDir != "" {
			path = filepath.Join(e.baseDir, path)
		}
		if data, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(data))) > 0 {
			return strings.TrimRight(string(data), "\r\n"), true
		}
		length := 16
		chars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
		for _, param := range parts[1:] {
			if strings.HasPrefix(param, "length=") {
				if l, err := strconv.Atoi(param[len("length="):]); err == nil && l > 0 {
					length = l
				}
			} else if strings.HasPrefix(param, "chars=") {
				charsVal := param[len("chars="):]
				customChars := ""
				for _, cName := range strings.Split(charsVal, ",") {
					switch strings.TrimSpace(cName) {
					case "ascii_letters":
						customChars += "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
					case "ascii_lowercase":
						customChars += "abcdefghijklmnopqrstuvwxyz"
					case "ascii_uppercase":
						customChars += "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
					case "digits":
						customChars += "0123456789"
					case "punctuation":
						customChars += "!@#$%^&*()-_=+"
					default:
						customChars += cName
					}
				}
				if len(customChars) > 0 {
					chars = customChars
				}
			}
		}
		genPass := generateRandomString(length, chars)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err == nil {
			_ = os.WriteFile(path, []byte(genPass+"\n"), 0600)
		}
		return genPass, true
	case "first_found":
		candidates := strings.Split(key, ",")
		for _, c := range candidates {
			candidate := strings.TrimSpace(c)
			if candidate == "" {
				continue
			}
			fullPath := candidate
			if !filepath.IsAbs(fullPath) && e.baseDir != "" {
				fullPath = filepath.Join(e.baseDir, fullPath)
			}
			if _, err := os.Stat(fullPath); err == nil {
				return fullPath, true
			}
		}
		return "", true
	case "url":
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(key)
		if err != nil {
			return "", true
		}
		defer resp.Body.Close()
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", true
		}
		return strings.TrimRight(string(bodyBytes), "\r\n"), true
	default:
		return nil, false
	}
}

func generateRandomString(length int, chars string) string {
	if length <= 0 || len(chars) == 0 {
		return ""
	}
	buf := make([]byte, length)
	_, _ = rand.Read(buf)
	res := make([]byte, length)
	for i := 0; i < length; i++ {
		res[i] = chars[int(buf[i])%len(chars)]
	}
	return string(res)
}

func templateLength(value interface{}) int {
	switch v := value.(type) {
	case nil:
		return 0
	case string:
		return len(v)
	case []interface{}:
		return len(v)
	case []string:
		return len(v)
	case map[string]interface{}:
		return len(v)
	case map[string]string:
		return len(v)
	default:
		return len(module.TemplateNormalizeList(value))
	}
}

func templateIndex(value interface{}, index int) (interface{}, bool) {
	if index < 0 {
		return nil, false
	}
	switch v := value.(type) {
	case string:
		runes := []rune(v)
		if index >= len(runes) {
			return nil, false
		}
		return string(runes[index]), true
	case []interface{}:
		if index >= len(v) {
			return nil, false
		}
		return v[index], true
	case []string:
		if index >= len(v) {
			return nil, false
		}
		return v[index], true
	default:
		items := module.TemplateNormalizeList(value)
		if index >= len(items) {
			return nil, false
		}
		return items[index], true
	}
}

func splitFilterArgs(argString string) []string {
	args := make([]string, 0)
	var current strings.Builder
	var quote rune
	depth := 0

	for _, ch := range argString {
		switch {
		case quote != 0:
			current.WriteRune(ch)
			if ch == quote {
				quote = 0
			}
		case ch == '\'' || ch == '"':
			quote = ch
			current.WriteRune(ch)
		case ch == '(' || ch == '[' || ch == '{':
			depth++
			current.WriteRune(ch)
		case ch == ')' || ch == ']' || ch == '}':
			if depth > 0 {
				depth--
			}
			current.WriteRune(ch)
		case ch == ',' && depth == 0:
			args = append(args, strings.TrimSpace(current.String()))
			current.Reset()
		default:
			current.WriteRune(ch)
		}
	}

	if strings.TrimSpace(current.String()) != "" {
		args = append(args, strings.TrimSpace(current.String()))
	}
	return args
}

func parseFilterLiteral(raw string) interface{} {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 {
		if (raw[0] == '\'' && raw[len(raw)-1] == '\'') || (raw[0] == '"' && raw[len(raw)-1] == '"') {
			return raw[1 : len(raw)-1]
		}
	}
	switch strings.ToLower(raw) {
	case "true", "yes":
		return true
	case "false", "no":
		return false
	case "none", "null":
		return nil
	}
	if strings.HasPrefix(raw, "{") && strings.HasSuffix(raw, "}") {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &m); err == nil {
			return m
		}
		if err := json.Unmarshal([]byte(convertSingleQuotesToJSON(raw)), &m); err == nil {
			return m
		}
	}
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		var list []interface{}
		if err := json.Unmarshal([]byte(raw), &list); err == nil {
			return list
		}
		if err := json.Unmarshal([]byte(convertSingleQuotesToJSON(raw)), &list); err == nil {
			return list
		}
	}
	if i, err := strconv.Atoi(raw); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil && strings.Contains(raw, ".") {
		return f
	}
	return raw
}

func convertSingleQuotesToJSON(raw string) string {
	var buf strings.Builder
	inSingle := false
	inDouble := false
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		switch {
		case inDouble:
			buf.WriteByte(ch)
			if ch == '\\' && i+1 < len(raw) {
				i++
				buf.WriteByte(raw[i])
			} else if ch == '"' {
				inDouble = false
			}
		case inSingle:
			if ch == '\\' && i+1 < len(raw) {
				i++
				buf.WriteByte(raw[i])
			} else if ch == '\'' {
				buf.WriteByte('"')
				inSingle = false
			} else if ch == '"' {
				buf.WriteString("\\\"")
			} else {
				buf.WriteByte(ch)
			}
		default:
			if ch == '\'' {
				buf.WriteByte('"')
				inSingle = true
			} else if ch == '"' {
				buf.WriteByte('"')
				inDouble = true
			} else {
				buf.WriteByte(ch)
			}
		}
	}
	return buf.String()
}

func (e *Executor) parseVarAccessPath(expr string, vars map[string]interface{}) []string {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil
	}

	parts := make([]string, 0)
	var current strings.Builder
	inBracket := false
	var quoteChar rune = 0
	wasQuoted := false

	runes := []rune(expr)
	n := len(runes)

	for i := 0; i < n; i++ {
		ch := runes[i]
		if quoteChar != 0 {
			if ch == quoteChar {
				quoteChar = 0
			} else {
				current.WriteRune(ch)
			}
			continue
		}

		switch ch {
		case '\'', '"':
			quoteChar = ch
			wasQuoted = true
		case '[':
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
			inBracket = true
			wasQuoted = false
		case ']':
			inBracket = false
			segment := strings.TrimSpace(current.String())
			current.Reset()
			if segment != "" {
				if !wasQuoted && vars != nil {
					if _, err := strconv.Atoi(segment); err != nil {
						if val, ok := e.lookupVarExpr(segment, vars); ok && val != nil {
							segment = fmt.Sprintf("%v", val)
						}
					}
				}
				parts = append(parts, segment)
			}
			wasQuoted = false
		case '.':
			if !inBracket {
				if current.Len() > 0 {
					parts = append(parts, current.String())
					current.Reset()
				}
			} else {
				current.WriteRune(ch)
			}
		default:
			current.WriteRune(ch)
		}
	}

	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts
}

func (e *Executor) lookupVarExpr(expr string, vars map[string]interface{}) (interface{}, bool) {
	if vars == nil {
		return nil, false
	}
	if val, ok := vars[expr]; ok {
		return val, true
	}

	parts := e.parseVarAccessPath(expr, vars)
	if len(parts) == 0 {
		return nil, false
	}

	var current interface{} = vars
	for _, part := range parts {
		if part == "" {
			continue
		}

		matched := false
		switch v := current.(type) {
		case map[string]interface{}:
			if val, ok := v[part]; ok {
				current = val
				matched = true
			}
		case map[string]map[string]interface{}:
			if val, ok := v[part]; ok {
				current = val
				matched = true
			}
		case map[string]string:
			if val, ok := v[part]; ok {
				current = val
				matched = true
			}
		case map[string][]string:
			if val, ok := v[part]; ok {
				current = val
				matched = true
			}
		case rawTask:
			if val, ok := v[part]; ok {
				current = val
				matched = true
			}
		case []interface{}:
			idx, err := strconv.Atoi(part)
			if err == nil && idx >= 0 && idx < len(v) {
				current = v[idx]
				matched = true
			}
		case []string:
			idx, err := strconv.Atoi(part)
			if err == nil && idx >= 0 && idx < len(v) {
				current = v[idx]
				matched = true
			}
		}

		if matched {
			continue
		}

		rv := reflect.ValueOf(current)
		if !rv.IsValid() {
			return nil, false
		}
		if rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
			rv = rv.Elem()
			if !rv.IsValid() {
				return nil, false
			}
		}

		switch rv.Kind() {
		case reflect.Map:
			mapKey := reflect.ValueOf(part)
			val := rv.MapIndex(mapKey)
			if !val.IsValid() {
				return nil, false
			}
			current = val.Interface()
		case reflect.Slice, reflect.Array:
			idx, err := strconv.Atoi(part)
			if err != nil || idx < 0 || idx >= rv.Len() {
				return nil, false
			}
			current = rv.Index(idx).Interface()
		default:
			return nil, false
		}
	}

	return current, true
}

func truthyValue(value interface{}) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		switch strings.TrimSpace(strings.ToLower(v)) {
		case "", "false", "no", "0":
			return false
		default:
			return true
		}
	case int:
		return v != 0
	case int8:
		return v != 0
	case int16:
		return v != 0
	case int32:
		return v != 0
	case int64:
		return v != 0
	case uint:
		return v != 0
	case uint8:
		return v != 0
	case uint16:
		return v != 0
	case uint32:
		return v != 0
	case uint64:
		return v != 0
	case float32:
		return v != 0
	case float64:
		return v != 0
	case []interface{}:
		return len(v) > 0
	case map[string]interface{}:
		return len(v) > 0
	case map[string]string:
		return len(v) > 0
	default:
		return true
	}
}

func (e *Executor) resolveTernaryExpr(expr string, vars map[string]interface{}) (interface{}, bool) {
	expr = strings.TrimSpace(expr)
	for strings.HasPrefix(expr, "(") && strings.HasSuffix(expr, ")") && isMatchedParentheses(expr[1:len(expr)-1]) {
		expr = strings.TrimSpace(expr[1 : len(expr)-1])
	}

	truePart, condPart, falsePart, ok := splitTernary(expr)
	if !ok {
		return nil, false
	}

	condResult := e.evaluateWhen(condPart, vars)
	if condResult {
		return e.evaluateTernaryOperand(truePart, vars), true
	}
	return e.evaluateTernaryOperand(falsePart, vars), true
}

func (e *Executor) evaluateTernaryOperand(operand string, vars map[string]interface{}) interface{} {
	operand = strings.TrimSpace(operand)
	if operand == "" {
		return ""
	}
	for strings.HasPrefix(operand, "(") && strings.HasSuffix(operand, ")") && isMatchedParentheses(operand[1:len(operand)-1]) {
		operand = strings.TrimSpace(operand[1 : len(operand)-1])
	}

	// 嵌套三元（例如右结合 'b' if c2 else 'c'）
	if val, ok := e.resolveTernaryExpr(operand, vars); ok {
		return val
	}
	// 管道过滤器，例如 'foo' | upper 或 val | default('fallback')
	if strings.Contains(operand, "|") {
		if val, ok := e.resolveFilteredExpr(operand, vars); ok {
			return val
		}
	}
	// lookup 函数
	if val, ok := e.resolveLookupExpr(operand, vars); ok {
		return val
	}
	// 引号字符串字面量
	if quoted, ok := parseQuotedConditionOperand(operand); ok {
		return quoted
	}
	switch strings.ToLower(operand) {
	case "true", "yes":
		return true
	case "false", "no":
		return false
	case "none", "null", "nil":
		return nil
	}
	if strings.HasPrefix(operand, "{") && strings.HasSuffix(operand, "}") {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(operand), &m); err == nil {
			return m
		}
	}
	if strings.HasPrefix(operand, "[") && strings.HasSuffix(operand, "]") {
		var list []interface{}
		if err := json.Unmarshal([]byte(operand), &list); err == nil {
			return list
		}
	}
	if i, err := strconv.Atoi(operand); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(operand, 64); err == nil && strings.Contains(operand, ".") {
		return f
	}
	// 变量查找
	if val, ok := e.lookupVarExpr(operand, vars); ok {
		return val
	}
	return operand
}

func isMatchedParentheses(s string) bool {
	depth := 0
	var quote rune
	for _, ch := range s {
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		if ch == '(' {
			depth++
		} else if ch == ')' {
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

func splitTernary(expr string) (string, string, string, bool) {
	runes := []rune(expr)
	n := len(runes)
	if n < 7 {
		return "", "", "", false
	}

	var quote rune
	parenDepth := 0
	bracketDepth := 0
	braceDepth := 0

	ifIdx := -1

	for i := 0; i < n; i++ {
		ch := runes[i]
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		if ch == '(' {
			parenDepth++
			continue
		} else if ch == ')' {
			if parenDepth > 0 {
				parenDepth--
			}
			continue
		} else if ch == '[' {
			bracketDepth++
			continue
		} else if ch == ']' {
			if bracketDepth > 0 {
				bracketDepth--
			}
			continue
		} else if ch == '{' {
			braceDepth++
			continue
		} else if ch == '}' {
			if braceDepth > 0 {
				braceDepth--
			}
			continue
		}

		if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 {
			// 匹配关键词 "if"
			if ifIdx == -1 && i > 0 && i+2 <= n && string(runes[i:i+2]) == "if" {
				prevIsSep := unicode.IsSpace(runes[i-1]) || runes[i-1] == ')' || runes[i-1] == ']'
				nextIsSep := i+2 == n || unicode.IsSpace(runes[i+2]) || runes[i+2] == '('
				if prevIsSep && nextIsSep {
					ifIdx = i
					i++
					continue
				}
			}

			// 匹配关键词 "else"
			if ifIdx != -1 && i > ifIdx+2 && i+4 <= n && string(runes[i:i+4]) == "else" {
				prevIsSep := unicode.IsSpace(runes[i-1]) || runes[i-1] == ')' || runes[i-1] == ']'
				nextIsSep := i+4 == n || unicode.IsSpace(runes[i+4]) || runes[i+4] == '('
				if prevIsSep && nextIsSep {
					truePart := strings.TrimSpace(string(runes[:ifIdx]))
					condPart := strings.TrimSpace(string(runes[ifIdx+2 : i]))
					falsePart := strings.TrimSpace(string(runes[i+4:]))
					if truePart != "" && condPart != "" && falsePart != "" {
						return truePart, condPart, falsePart, true
					}
				}
			}
		}
	}

	return "", "", "", false
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
	if len(e.GetTags()) == 0 {
		return true
	}
	return e.shouldRunTaskByTags(taskTags)
}

func (e *Executor) shouldRunTaskByTags(taskTags []string) bool {
	selectedTags := e.GetTags()
	skipTags := e.GetSkipTags()
	taskTags = normalizeTagList(taskTags)

	if shouldSkipByTags(skipTags, taskTags) {
		return false
	}

	if len(selectedTags) == 0 {
		return !containsTag(taskTags, "never")
	}

	if containsTag(selectedTags, "all") {
		if containsTag(taskTags, "never") && !containsTag(selectedTags, "never") {
			return false
		}
		return true
	}

	if containsTag(taskTags, "always") {
		return true
	}

	if len(taskTags) == 0 {
		return false
	}

	if containsTag(taskTags, "never") && !containsTag(selectedTags, "never") {
		return matchAnyTag(selectedTags, filterOutTag(taskTags, "never"))
	}

	return matchAnyTag(selectedTags, taskTags)
}

func shouldSkipByTags(skipTags, taskTags []string) bool {
	if len(skipTags) == 0 {
		return false
	}
	if containsTag(skipTags, "all") {
		return true
	}
	return matchAnyTag(skipTags, taskTags)
}

func matchAnyTag(selectedTags, taskTags []string) bool {
	if len(selectedTags) == 0 || len(taskTags) == 0 {
		return false
	}
	for _, tag := range selectedTags {
		if tag == "all" {
			return true
		}
		for _, taskTag := range taskTags {
			if tag == taskTag {
				return true
			}
		}
	}
	return false
}

func containsTag(tags []string, target string) bool {
	for _, tag := range tags {
		if tag == target {
			return true
		}
	}
	return false
}

func filterOutTag(tags []string, excluded string) []string {
	if len(tags) == 0 {
		return nil
	}
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		if tag == excluded {
			continue
		}
		result = append(result, tag)
	}
	return result
}

func normalizeTagList(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		result = append(result, tag)
	}
	return result
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
func (e *Executor) notifyHandlers(notify interface{}, host *inventory.Host, vars map[string]interface{}, env map[string]string, handlers []*Handler) {
	if handlers == nil || len(handlers) == 0 {
		return
	}

	// 获取 handler 名称
	var handlerNames []string
	switch n := notify.(type) {
	case string:
		handlerNames = []string{n}
	case []string:
		handlerNames = n
	case []interface{}:
		for _, h := range n {
			handlerNames = append(handlerNames, fmt.Sprintf("%v", h))
		}
	default:
		e.log("Unknown notify type: %T", notify)
		return
	}

	// 查找匹配的 handler
	for _, name := range handlerNames {
		for _, handler := range handlers {
			if handler.Name == name || handler.Listen == name {
				e.mu.Lock()
				e.pendingHandlers = append(e.pendingHandlers, pendingHandler{
					handler: handler,
					host:    host,
					vars:    vars,
					env:     env,
				})
				e.mu.Unlock()
				break
			}
		}
	}
}

// getHostResult 获取主机结果
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
	Tasks []*TaskResult
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

// =============================================================================
// Phase 16: vars_prompt & debugger
// =============================================================================

type DebuggerAction int

const (
	DebuggerContinue DebuggerAction = iota
	DebuggerRedo
	DebuggerQuit
)

func (e *Executor) promptPlayVars(play *Play, playVars map[string]interface{}) error {
	if play == nil || len(play.VarsPrompt) == 0 {
		return nil
	}

	w := e.getStdout()
	r := bufio.NewReader(e.getStdin())

	for _, item := range play.VarsPrompt {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}

		// 检查 extra_vars 优先级覆盖：命令行 -e 已传入时跳过 prompt
		e.mu.RLock()
		_, inExtra := e.extraVars[name]
		e.mu.RUnlock()
		if inExtra {
			continue
		}

		promptText := item.Prompt
		if promptText == "" {
			promptText = name
		}

		defaultStr := ""
		if item.Default != nil {
			defaultStr = fmt.Sprintf("%v", item.Default)
		}

		displayPrompt := promptText
		if defaultStr != "" {
			displayPrompt = fmt.Sprintf("%s [%s]: ", promptText, defaultStr)
		} else {
			displayPrompt = fmt.Sprintf("%s: ", promptText)
		}

		var finalVal string
		maxAttempts := 3
		if !item.Confirm {
			maxAttempts = 1
		}

		for attempt := 0; attempt < maxAttempts; attempt++ {
			val, err := e.readPromptInput(r, w, displayPrompt, item.Private)
			if err != nil {
				return fmt.Errorf("read prompt for '%s': %w", name, err)
			}
			if val == "" && defaultStr != "" {
				val = defaultStr
			}

			if item.Confirm {
				confirmPrompt := fmt.Sprintf("confirm %s", displayPrompt)
				confirmVal, err := e.readPromptInput(r, w, confirmPrompt, item.Private)
				if err != nil {
					return fmt.Errorf("read confirm prompt for '%s': %w", name, err)
				}
				if confirmVal == "" && defaultStr != "" {
					confirmVal = defaultStr
				}
				if val != confirmVal {
					fmt.Fprintln(w, "Values do not match. Please try again.")
					if attempt == maxAttempts-1 {
						return fmt.Errorf("confirmation failed for '%s': values did not match", name)
					}
					continue
				}
			}

			finalVal = val
			break
		}

		if item.Encrypt != "" {
			finalVal = encryptPromptValue(finalVal, item.Encrypt)
		}

		playVars[name] = finalVal
		if play.Vars == nil {
			play.Vars = make(map[string]interface{})
		}
		play.Vars[name] = finalVal
	}
	return nil
}

func (e *Executor) readPromptInput(r *bufio.Reader, w io.Writer, prompt string, private bool) (string, error) {
	fmt.Fprint(w, prompt)
	// 如果是 private 模式且是真正控制台标准输入 (无注入测试 Reader)
	if private && e.Stdin == nil && term.IsTerminal(int(os.Stdin.Fd())) {
		bytePass, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(w)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(bytePass), "\r\n"), nil
	}

	line, err := r.ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func encryptPromptValue(val, enc string) string {
	enc = strings.ToLower(strings.TrimSpace(enc))
	switch enc {
	case "sha256", "sha256_crypt":
		hash := sha256.Sum256([]byte(val))
		return hex.EncodeToString(hash[:])
	case "sha512", "sha512_crypt":
		hash := sha512.Sum512([]byte(val))
		return hex.EncodeToString(hash[:])
	case "md5", "md5_crypt":
		hash := md5.Sum([]byte(val))
		return hex.EncodeToString(hash[:])
	default:
		return val
	}
}

func (e *Executor) shouldTriggerDebugger(task *Task, result *TaskResult) bool {
	if result == nil {
		return false
	}
	mode := ""
	if task != nil && task.Debugger != "" {
		mode = strings.ToLower(strings.TrimSpace(task.Debugger))
	} else if e.currentPlayDebugger != "" {
		mode = strings.ToLower(strings.TrimSpace(e.currentPlayDebugger))
	} else {
		envDebug := strings.ToLower(strings.TrimSpace(os.Getenv("ANSIBLE_ENABLE_TASK_DEBUGGER")))
		if envDebug == "true" || envDebug == "1" || envDebug == "yes" {
			mode = "on_failed"
		}
	}

	if mode == "" || mode == "never" || mode == "false" || mode == "no" {
		return false
	}

	switch mode {
	case "always":
		return true
	case "on_failed":
		return result.Failed
	case "on_unreachable":
		return result.Unreachable
	case "on_skipped":
		return result.Skipped
	default:
		return false
	}
}

func (e *Executor) runDebugger(host *inventory.Host, task *Task, result *TaskResult, allVars map[string]interface{}) DebuggerAction {
	w := e.getStdout()
	r := bufio.NewReader(e.getStdin())

	hostName := "localhost"
	if host != nil {
		hostName = host.Name
	}
	taskName := ""
	if task != nil {
		taskName = task.Name
	}

	fmt.Fprintf(w, "\n[Debugger] Host: %s, Task: %s\n", hostName, taskName)
	if result != nil && result.Failed {
		fmt.Fprintf(w, "[Debugger] Task failed: %s\n", result.Message)
	}

	for {
		fmt.Fprintf(w, "[%s] TASK: %s (debug)> ", hostName, taskName)
		line, err := r.ReadString('\n')
		if err != nil && len(line) == 0 {
			return DebuggerContinue
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, " ", 2)
		cmd := strings.ToLower(parts[0])
		var arg string
		if len(parts) > 1 {
			arg = strings.TrimSpace(parts[1])
		}

		switch cmd {
		case "c", "continue":
			return DebuggerContinue
		case "r", "redo":
			fmt.Fprintln(w, "Re-running task...")
			return DebuggerRedo
		case "q", "quit":
			fmt.Fprintln(w, "Quitting playbook...")
			return DebuggerQuit
		case "h", "help":
			fmt.Fprintln(w, "Commands:")
			fmt.Fprintln(w, "  p/print <expr>       Print result or variable (e.g. result, result.rc, task.args, task_vars)")
			fmt.Fprintln(w, "  u/update_task <k=v>  Update task arguments (e.g. cmd=\"echo 123\", task.args['msg']='hi')")
			fmt.Fprintln(w, "  r/redo               Re-run current task")
			fmt.Fprintln(w, "  c/continue           Continue playbook execution")
			fmt.Fprintln(w, "  q/quit               Abort playbook execution")
		case "p", "print":
			if arg == "" {
				fmt.Fprintln(w, "Usage: p <expr>")
				continue
			}
			e.printDebuggerExpr(w, arg, task, result, allVars)
		case "u", "update_task":
			if arg == "" {
				fmt.Fprintln(w, "Usage: u <param_name=value>")
				continue
			}
			key, val, err := parseUpdateTaskInput(arg)
			if err != nil {
				fmt.Fprintf(w, "Error parsing update: %v\n", err)
				continue
			}
			if task.Params == nil {
				task.Params = make(map[string]interface{})
			}
			task.Params[key] = val
			if key == "cmd" || key == "_raw_params" {
				task.Params["cmd"] = val
				task.Params["_raw_params"] = val
			}
			fmt.Fprintf(w, "Task args updated: %s = %v\n", key, val)
		default:
			fmt.Fprintf(w, "Unknown command '%s'. Type 'help' for available commands.\n", cmd)
		}
	}
}

func parseUpdateTaskInput(input string) (string, string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", fmt.Errorf("empty input")
	}

	idx := strings.Index(input, "=")
	if idx == -1 {
		return "", "", fmt.Errorf("missing '=' in expression")
	}

	left := strings.TrimSpace(input[:idx])
	right := strings.TrimSpace(input[idx+1:])

	// 去除右侧外层单/双引号
	if (strings.HasPrefix(right, "\"") && strings.HasSuffix(right, "\"")) ||
		(strings.HasPrefix(right, "'") && strings.HasSuffix(right, "'")) {
		if len(right) >= 2 {
			right = right[1 : len(right)-1]
		}
	}

	// 处理左侧 key，如 task.args['cmd'] 或 args['cmd'] 或 task.args["cmd"]
	if start := strings.Index(left, "["); start != -1 {
		if end := strings.Index(left, "]"); end > start {
			key := left[start+1 : end]
			key = strings.Trim(key, "\"' ")
			return key, right, nil
		}
	}

	// 处理左侧带点语法，如 task.args.cmd 或 args.cmd
	if strings.Contains(left, ".") {
		parts := strings.Split(left, ".")
		key := strings.TrimSpace(parts[len(parts)-1])
		return key, right, nil
	}

	return left, right, nil
}

func (e *Executor) printDebuggerExpr(w io.Writer, expr string, task *Task, result *TaskResult, allVars map[string]interface{}) {
	expr = strings.TrimSpace(expr)
	switch expr {
	case "result", "r":
		m := map[string]interface{}{
			"failed":      false,
			"changed":     false,
			"skipped":     false,
			"unreachable": false,
			"rc":          0,
			"stdout":      "",
			"stderr":      "",
			"msg":         "",
		}
		if result != nil {
			m["failed"] = result.Failed
			m["changed"] = result.Changed
			m["skipped"] = result.Skipped
			m["unreachable"] = result.Unreachable
			m["rc"] = result.ExitCode
			m["stdout"] = result.Stdout
			m["stderr"] = result.Stderr
			m["msg"] = result.Message
			if len(result.Data) > 0 {
				m["data"] = result.Data
			}
		}
		b, _ := json.MarshalIndent(m, "", "  ")
		fmt.Fprintln(w, string(b))
		return
	case "result.rc", "result.exit_code", "rc":
		if result != nil {
			fmt.Fprintln(w, result.ExitCode)
		} else {
			fmt.Fprintln(w, 0)
		}
		return
	case "result.stdout", "stdout":
		if result != nil {
			fmt.Fprintln(w, result.Stdout)
		}
		return
	case "result.stderr", "stderr":
		if result != nil {
			fmt.Fprintln(w, result.Stderr)
		}
		return
	case "result.msg", "result.message", "msg":
		if result != nil {
			fmt.Fprintln(w, result.Message)
		}
		return
	case "result.failed", "failed":
		if result != nil {
			fmt.Fprintln(w, result.Failed)
		} else {
			fmt.Fprintln(w, false)
		}
		return
	case "result.changed", "changed":
		if result != nil {
			fmt.Fprintln(w, result.Changed)
		} else {
			fmt.Fprintln(w, false)
		}
		return
	case "result.skipped", "skipped":
		if result != nil {
			fmt.Fprintln(w, result.Skipped)
		} else {
			fmt.Fprintln(w, false)
		}
		return
	case "task.args", "args", "task.params":
		if task != nil && task.Params != nil {
			b, _ := json.MarshalIndent(task.Params, "", "  ")
			fmt.Fprintln(w, string(b))
		} else {
			fmt.Fprintln(w, "{}")
		}
		return
	case "task_vars", "vars":
		b, _ := json.MarshalIndent(allVars, "", "  ")
		fmt.Fprintln(w, string(b))
		return
	}

	if strings.HasPrefix(expr, "task.args.") || strings.HasPrefix(expr, "args.") {
		parts := strings.SplitN(expr, ".", 2)
		if task != nil && task.Params != nil {
			if v, ok := task.Params[parts[1]]; ok {
				fmt.Fprintln(w, v)
				return
			}
		}
	}

	if v, ok := allVars[expr]; ok {
		switch tv := v.(type) {
		case string:
			fmt.Fprintln(w, tv)
		default:
			b, err := json.Marshal(v)
			if err == nil {
				fmt.Fprintln(w, string(b))
			} else {
				fmt.Fprintf(w, "%v\n", v)
			}
		}
		return
	}

	if strings.HasPrefix(expr, "result.data.") {
		subKey := strings.TrimPrefix(expr, "result.data.")
		if result != nil && result.Data != nil {
			if v, ok := result.Data[subKey]; ok {
				fmt.Fprintln(w, v)
				return
			}
		}
	}

	fmt.Fprintln(w, "<undefined>")
}
