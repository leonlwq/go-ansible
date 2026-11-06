package playbook

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/module"
	"go-ansible/pkg/ssh"
	"go-ansible/pkg/util"
)

func (e *Executor) flushHandlers(result *PlayResult) error {
	if result == nil {
		return nil
	}

	e.flushMu.Lock()
	defer e.flushMu.Unlock()

	executedHandlers := make(map[string]bool)
	var handlerErr error

	for {
		e.mu.Lock()
		if len(e.pendingHandlers) == 0 {
			e.mu.Unlock()
			break
		}
		pending := e.pendingHandlers
		e.pendingHandlers = make([]pendingHandler, 0)
		e.mu.Unlock()

		// 按照 Play 声明的 handler 顺序对待触发的 handlers 进行排序（Ansible 官方规范）
		handlerOrder := make(map[string]int)
		for idx, h := range e.currentPlayHandlers {
			if _, exists := handlerOrder[h.Name]; !exists {
				handlerOrder[h.Name] = idx
			}
		}
		sort.SliceStable(pending, func(i, j int) bool {
			ordI := 999999
			if idx, ok := handlerOrder[pending[i].handler.Name]; ok {
				ordI = idx
			}
			ordJ := 999999
			if idx, ok := handlerOrder[pending[j].handler.Name]; ok {
				ordJ = idx
			}
			return ordI < ordJ
		})

		for _, ph := range pending {
			// 如果宿主已失败且未声明 force_handlers，则跳过执行
			if e.isHostFailed(ph.host.Name) {
				force := false
				e.mu.RLock()
				if e.currentPlayForceHandlers || e.forceHandlers {
					force = true
				}
				e.mu.RUnlock()
				if !force {
					continue
				}
			}

			handlerKey := fmt.Sprintf("%s-%s", ph.handler.Name, ph.host.Name)
			if executedHandlers[handlerKey] {
				continue
			}
			executedHandlers[handlerKey] = true

			// 获取该主机最新变量
			vars := e.taskVarsForHost(ph.host, ph.vars)

			hostResult := e.getHostResult(result, ph.host.Name)
			e.log("Executing handler: %s on host: %s", ph.handler.Name, ph.host.Name)

			if ph.handler.When != nil && !e.evaluateWhen(ph.handler.When, vars) {
				hostResult.AppendTask(&TaskResult{
					Name:    ph.handler.Name,
					Host:    ph.host.Name,
					Skipped: true,
					Message: "skipped due to when condition",
					Start:   time.Now(),
					End:     time.Now(),
				})
				continue
			}

			if ph.handler.Loop != nil {
				items := e.evaluateLoop(ph.handler.Loop, vars)
				for index, item := range items {
					resolvedItem := e.resolveValue(item, ph.vars)

					loopVars := make(map[string]interface{})
					for k, v := range ph.vars {
						loopVars[k] = v
					}
					loopVars["item"] = resolvedItem
					loopVars["ansible_loop"] = buildAnsibleLoopContext(index, items)

					params := e.resolveVariables(ph.handler.Params, loopVars)
					if ph.handler.NoLog {
						e.log("Handler params with item: [VALUE_SPECIFIED_IN_NO_LOG_TASK]")
					} else {
						e.log("Handler params with item: %+v", params)
					}

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

					e.maskNoLog(taskResult, ph.handler.NoLog)
					taskResult.End = time.Now()
					hostResult.AppendTask(taskResult)

					if ph.handler.Notify != nil && taskResult.Changed {
						e.notifyHandlers(ph.handler.Notify, ph.host, loopVars, ph.env, e.currentPlayHandlers)
					}
				}
				continue
			}

			handler := &Task{
				Name:       ph.handler.Name,
				ModuleName: ph.handler.ModuleName,
				Params:     ph.handler.Params,
				NoLog:      ph.handler.NoLog,
			}

			params := e.resolveVariables(handler.Params, ph.vars)
			if ph.handler.NoLog {
				e.log("Handler params: [VALUE_SPECIFIED_IN_NO_LOG_TASK]")
			} else {
				e.log("Handler params: %+v", params)
			}

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

			e.maskNoLog(taskResult, ph.handler.NoLog)
			taskResult.End = time.Now()
			hostResult.AppendTask(taskResult)

			if taskResult.Failed {
				e.markHostFailed(ph.host.Name)
				if handlerErr == nil {
					handlerErr = fmt.Errorf("handler %s failed on host %s: %s", ph.handler.Name, ph.host.Name, taskResult.Message)
				}
			}

			if ph.handler.Notify != nil && taskResult.Changed {
				e.notifyHandlers(ph.handler.Notify, ph.host, ph.vars, ph.env, e.currentPlayHandlers)
			}
		}
	}

	return handlerErr
}

func handlerMatchesName(handler *Handler, name string) bool {
	if handler == nil {
		return false
	}
	if handler.Name == name || handler.Listen == name {
		return true
	}
	for _, l := range handler.Listens {
		if l == name {
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

	// 查找匹配的 handler (支持 Name 精确匹配与 Listen 广播一对多匹配)
	queuedThisTime := make(map[string]bool)
	for _, name := range handlerNames {
		for _, handler := range handlers {
			if handlerMatchesName(handler, name) {
				queueKey := fmt.Sprintf("%s-%s", handler.Name, host.Name)
				if queuedThisTime[queueKey] {
					continue
				}
				queuedThisTime[queueKey] = true

				e.mu.Lock()
				alreadyQueued := false
				for _, ph := range e.pendingHandlers {
					if ph.handler != nil && ph.handler.Name == handler.Name && ph.host != nil && ph.host.Name == host.Name {
						alreadyQueued = true
						break
					}
				}
				if !alreadyQueued {
					cleanVars := vars
					if _, hasItem := vars["item"]; hasItem || vars["ansible_loop_var"] != nil {
						cleanVars = make(map[string]interface{}, len(vars))
						loopVarName, _ := vars["ansible_loop_var"].(string)
						for k, v := range vars {
							if k == "item" || k == "ansible_loop" || k == "ansible_loop_var" || (loopVarName != "" && k == loopVarName) {
								continue
							}
							cleanVars[k] = v
						}
					}
					e.pendingHandlers = append(e.pendingHandlers, pendingHandler{
						handler: handler,
						host:    host,
						vars:    cleanVars,
						env:     env,
					})
				}
				e.mu.Unlock()
			}
		}
	}
}

// getHostResult 获取主机结果

// getMapKeys 获取 map 的键列表
func (e *Executor) executeTaskForHost(hosts []*inventory.Host, host *inventory.Host, task *Task, vars map[string]interface{}, handlers []*Handler, runOnce map[string]bool) ([]*TaskResult, error) {
	return e.executeTaskForHostWithEnv(hosts, host, task, vars, handlers, runOnce, nil)
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
		hostResult.AppendTask(&TaskResult{
			Name:    e.resolveString(task.Name, vars),
			Host:    host.Name,
			Skipped: true,
			Message: "skipped before start_at_task",
			Start:   time.Now(),
			End:     time.Now(),
		})
		return nil
	}

	// 动态包含任务循环处理（include_tasks / include_role 官方规范支持 loop / with_items 迭代）
	if (isIncludeRoleTask(task) || isIncludeTask(task)) && task.Loop != nil {
		items := e.evaluateLoop(task.Loop, vars)
		loopVar := "item"
		if task.LoopControl.LoopVar != "" {
			loopVar = task.LoopControl.LoopVar
		}
		for idx, item := range items {
			loopVars := make(map[string]interface{}, len(vars)+4)
			for k, v := range vars {
				loopVars[k] = v
			}
			loopVars[loopVar] = item
			if task.LoopControl.IndexVar != "" {
				loopVars[task.LoopControl.IndexVar] = idx
			}
			loopVars["ansible_loop"] = buildAnsibleLoopContext(idx, items)
			var err error
			if isIncludeRoleTask(task) {
				err = e.executeIncludedRoleForHost(allHosts, host, task, loopVars, handlers, runOnce, playEnv, hostResult)
			} else {
				err = e.executeIncludedTasksForHost(allHosts, host, task, loopVars, handlers, runOnce, playEnv, hostResult)
			}
			if err != nil {
				if task.IgnoreError || (task.IgnoreUnreachable && isUnreachableError(err)) {
					if task.LoopControl.Pause > 0 && idx < len(items)-1 {
						if !e.sleep(time.Duration(task.LoopControl.Pause * float64(time.Second))) {
							break
						}
					}
					continue
				}
				return err
			}
			if task.LoopControl.Pause > 0 && idx < len(items)-1 {
				if !e.sleep(time.Duration(task.LoopControl.Pause * float64(time.Second))) {
					break
				}
			}
		}
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
			hostResult.AppendTasks(taskResults...)
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

func isBlockTask(task *Task) bool {
	if task == nil {
		return false
	}
	return len(task.Block) > 0 || len(task.Rescue) > 0 || len(task.Always) > 0
}

func (e *Executor) shouldRunTaskByStartAtTask(host *inventory.Host, task *Task, vars map[string]interface{}) bool {
	// Fast path: 未指定 --start-at-task 时直接放行，完全免除锁争用与模板渲染开销
	if e.startAtTask == "" {
		return true
	}

	hostName := ""
	if host != nil {
		hostName = host.Name
	}

	e.mu.RLock()
	started := e.startedTasks[hostName]
	e.mu.RUnlock()
	if started {
		return true
	}

	taskName := strings.TrimSpace(e.resolveString(task.Name, vars))
	containerTask := isBlockTask(task) || isIncludeTask(task) || isIncludeRoleTask(task)

	if taskName == e.startAtTask {
		e.mu.Lock()
		e.startedTasks[hostName] = true
		e.mu.Unlock()
		return true
	}

	return containerTask
}

func (e *Executor) executeBlockTaskForHost(allHosts []*inventory.Host, host *inventory.Host, task *Task, vars map[string]interface{}, handlers []*Handler, runOnce map[string]bool, playEnv map[string]string, hostResult *HostResult) error {
	if hostResult == nil {
		return nil
	}

	blockTasks := cloneTasks(task.Block)
	applyParentTaskAttributes(task, blockTasks)

	beforeBlock := hostResult.TaskCount()
	var blockErr error
	for _, blockTask := range blockTasks {
		childVars := e.taskVarsForHost(host, vars)
		if err := e.executeTaskEntryForHost(allHosts, host, blockTask, childVars, handlers, runOnce, playEnv, hostResult); err != nil {
			blockErr = err
			break
		}
		if hostResultHasFailure(hostResult, beforeBlock) {
			break
		}
	}

	blockFailed := blockErr != nil || hostResultHasNewFailure(hostResult, beforeBlock)
	if blockFailed && len(task.Rescue) > 0 {
		rescueTasks := cloneTasks(task.Rescue)
		applyParentTaskAttributes(task, rescueTasks)
		beforeRescue := hostResult.TaskCount()
		if host != nil {
			e.clearHostFailed(host.Name)
		}

		// 提取被救援的失败任务及详细结果
		var failedTaskName string
		failedResult := map[string]interface{}{
			"failed": true,
		}
		tasksSnapshot := hostResult.GetTasks()
		for i := beforeBlock; i < len(tasksSnapshot); i++ {
			tr := tasksSnapshot[i]
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
		if failedTaskName == "" && blockErr != nil {
			failedResult["msg"] = blockErr.Error()
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

		var rescueErr error
		for _, rescueTask := range rescueTasks {
			childVars := e.taskVarsForHost(host, rescueVars)
			if err := e.executeTaskEntryForHost(allHosts, host, rescueTask, childVars, handlers, runOnce, playEnv, hostResult); err != nil {
				rescueErr = err
				break
			}
			if hostResultHasNewFailure(hostResult, beforeRescue) {
				break
			}
		}
		if rescueErr == nil && !hostResultHasNewFailure(hostResult, beforeRescue) {
			if host != nil {
				e.clearHostFailed(host.Name)
			}
			hostResult.MarkRescued(beforeBlock, beforeRescue)
			blockFailed = false
			blockErr = nil
		} else {
			if rescueErr != nil && blockErr == nil {
				blockErr = rescueErr
			}
		}
	}

	var alwaysErr error
	if len(task.Always) > 0 {
		alwaysTasks := cloneTasks(task.Always)
		applyParentTaskAttributes(task, alwaysTasks)
		for _, alwaysTask := range alwaysTasks {
			childVars := e.taskVarsForHost(host, vars)
			if err := e.executeTaskEntryForHost(allHosts, host, alwaysTask, childVars, handlers, runOnce, playEnv, hostResult); err != nil {
				if alwaysErr == nil {
					alwaysErr = err
				}
			}
		}
	}

	if alwaysErr != nil {
		return alwaysErr
	}
	if blockFailed && blockErr != nil {
		return blockErr
	}

	return nil
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
		if parent.Become != nil && task.Become == nil {
			task.Become = parent.Become
		}
		if parent.BecomeUser != "" && task.BecomeUser == "" {
			task.BecomeUser = parent.BecomeUser
		}
		if parent.IgnoreUnreachable && !task.IgnoreUnreachable {
			task.IgnoreUnreachable = true
		}
		if parent.CheckMode != nil && task.CheckMode == nil {
			task.CheckMode = parent.CheckMode
		}
		if parent.Diff != nil && task.Diff == nil {
			task.Diff = parent.Diff
		}
		if len(parent.Environment) > 0 {
			if task.Environment == nil {
				task.Environment = make(map[string]string, len(parent.Environment))
			}
			for k, v := range parent.Environment {
				if _, ok := task.Environment[k]; !ok {
					task.Environment[k] = v
				}
			}
		}
		if parent.NoLog && !task.NoLog {
			task.NoLog = true
		}
		if parent.Throttle > 0 && task.Throttle == 0 {
			task.Throttle = parent.Throttle
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
		if len(task.Tags) > 0 {
			copied.Tags = append([]string(nil), task.Tags...)
		}
		if len(task.Environment) > 0 {
			copied.Environment = make(map[string]string, len(task.Environment))
			for k, v := range task.Environment {
				copied.Environment[k] = v
			}
		}
		if len(task.Vars) > 0 {
			copied.Vars = make(map[string]interface{}, len(task.Vars))
			for k, v := range task.Vars {
				copied.Vars[k] = v
			}
		}
		cloned = append(cloned, &copied)
	}
	return cloned
}

func hostResultHasFailure(hostResult *HostResult, start int) bool {
	if hostResult == nil {
		return false
	}
	return hostResult.HasFailure(start)
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
		hostCopy := *host
		go func() {
			res, err := e.executeTaskForHostWithEnvInternal(hosts, &hostCopy, task, vars, handlers, runOnce, playEnv)
			ch <- execRes{results: res, err: err}
		}()
		timer := time.NewTimer(time.Duration(task.Timeout) * time.Second)
		defer timer.Stop()
		select {
		case out := <-ch:
			return out.results, out.err
		case <-timer.C:
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
	res, err := e.executeTaskWithEnv(host, task, vars, handlers, playEnv)
	if task.RunOnce && len(hosts) > 1 {
		for _, otherHost := range hosts {
			if otherHost == nil || otherHost.Name == host.Name {
				continue
			}
			if task.Register != "" && len(res) > 0 {
				e.storeRegisteredResult(otherHost.Name, task, res)
			}
			if task.ModuleName == "set_fact" {
				resolvedFacts := e.resolveVariables(task.Params, vars)
				e.mergeAnsibleFacts(otherHost.Name, resolvedFacts)
			}
		}
	}
	return res, err
}

func (e *Executor) taskExecutionKey(task *Task) string {
	return fmt.Sprintf("%s|%s|%v", task.Name, task.ModuleName, task.Params)
}

// executeTask 执行单个任务，返回结果列表（循环任务会返回多个结果）
func (e *Executor) executeTask(host *inventory.Host, task *Task, vars map[string]interface{}, handlers []*Handler) ([]*TaskResult, error) {
	return e.executeTaskWithEnv(host, task, vars, handlers, nil)
}

func (e *Executor) executeTaskWithEnv(host *inventory.Host, task *Task, vars map[string]interface{}, handlers []*Handler, playEnv map[string]string) ([]*TaskResult, error) {
	// Copy-on-Write (COW): 当任务无专属 task.Vars 且无 extraVars 时，直接复用上游变量字典，彻底消除每任务全量深拷贝
	allVars := vars
	if task != nil && len(task.Vars) > 0 {
		e.mu.RLock()
		hasExtraVars := len(e.extraVars) > 0
		extraCount := len(task.Vars)
		if hasExtraVars {
			extraCount += len(e.extraVars)
		}
		e.mu.RUnlock()

		allVars = make(map[string]interface{}, len(vars)+extraCount)
		for k, v := range vars {
			allVars[k] = v
		}

		resolvedTaskVars := e.resolveVariables(task.Vars, allVars)
		for k, v := range resolvedTaskVars {
			allVars[k] = v
		}

		if hasExtraVars {
			e.mu.RLock()
			for k, v := range e.extraVars {
				allVars[k] = v
			}
			e.mu.RUnlock()
		}
	}
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
	var delegateHost *inventory.Host
	if task.DelegateTo != "" {
		var err error
		delegateHost, _, err = e.resolveDelegateHost(task.DelegateTo, allVars)
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
		if delegateHost != nil {
			execHost = *delegateHost
			execHost.Variables = make(map[string]string, len(delegateHost.Variables))
			for k, v := range delegateHost.Variables {
				execHost.Variables[k] = v
			}
			// 继承目标委派主机在 hostvars 中的连接变量
			if hostvarsRaw, ok := allVars["hostvars"]; ok {
				switch hv := hostvarsRaw.(type) {
				case map[string]map[string]interface{}:
					if dVars, ok := hv[delegateHost.Name]; ok {
						for k, v := range dVars {
							if strings.HasPrefix(k, "ansible_") {
								inventory.ApplyHostVariable(&execHost, k, fmt.Sprintf("%v", v))
							}
						}
					}
				case map[string]interface{}:
					if dVarsRaw, ok := hv[delegateHost.Name]; ok {
						if dVars, ok := dVarsRaw.(map[string]interface{}); ok {
							for k, v := range dVars {
								if strings.HasPrefix(k, "ansible_") {
									inventory.ApplyHostVariable(&execHost, k, fmt.Sprintf("%v", v))
								}
							}
						}
					}
				}
			}
			// 继承当前任务显式声明的 ansible_* 连接覆盖变量
			if task != nil && len(task.Vars) > 0 {
				for k, v := range task.Vars {
					if strings.HasPrefix(k, "ansible_") {
						inventory.ApplyHostVariable(&execHost, k, fmt.Sprintf("%v", v))
					}
				}
			}
		}
	}

	factTargetHost := host.Name
	if task.DelegateTo != "" && task.DelegateFacts && delegateHost != nil {
		factTargetHost = delegateHost.Name
	}
	if task.Become != nil {
		execHost.Become = *task.Become
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
		if task.NoLog {
			e.log("Loop items: [VALUE_SPECIFIED_IN_NO_LOG_TASK]")
		} else {
			e.log("Loop items: %v", items)
		}
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
			loopVars["ansible_loop"] = buildAnsibleLoopContext(index, items)

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
					e.applyNoLogMask(itemResult, task)
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

				if task.NoLog {
					e.log("Resolved params: [VALUE_SPECIFIED_IN_NO_LOG_TASK]")
				} else {
					e.log("Resolved params: %+v", params)
				}
				modResult, err := e.executeTaskModule(&execHost, moduleName, params, task, loopVars, localAction)
				e.applyExecutionOutcome(itemResult, task, modResult, err)

				if modResult != nil {
					if task.NoLog {
						e.log("Module result: Changed=%v, Stdout=[VALUE_SPECIFIED_IN_NO_LOG_TASK]", modResult.Changed)
					} else {
						e.log("Module result: Changed=%v, Stdout=%s", modResult.Changed, modResult.Stdout)
					}
					itemResult.Changed = modResult.Changed
					itemResult.Stdout = modResult.Stdout
					itemResult.Stderr = modResult.Stderr
					itemResult.ExitCode = modResult.ExitCode
					itemResult.Diff = modResult.Diff
					itemResult.Data = modResult.Data
					if modResult.Data != nil {
						if facts, ok := modResult.Data["ansible_facts"].(map[string]interface{}); ok {
							e.mergeAnsibleFacts(factTargetHost, facts)
						}
					}
				}
				e.applyTaskResultConditions(itemResult, task, loopVars)
				e.applyNoLogMask(itemResult, task)

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

			if task.LoopControl.Pause > 0 && index < len(items)-1 {
				if !e.sleep(time.Duration(task.LoopControl.Pause * float64(time.Second))) {
					break
				}
			}
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

		if task.NoLog {
			e.log("Resolved params: [VALUE_SPECIFIED_IN_NO_LOG_TASK]")
		} else {
			e.log("Resolved params: %+v", params)
		}
		modResult, err := e.executeTaskModule(&execHost, task.ModuleName, params, task, allVars, localAction)
		e.applyExecutionOutcome(result, task, modResult, err)

		if modResult != nil {
			if task.NoLog {
				e.log("Module result: Changed=%v", modResult.Changed)
			} else {
				e.log("Module result: Changed=%v", modResult.Changed)
			}
			result.Changed = modResult.Changed
			result.Stdout = modResult.Stdout
			result.Stderr = modResult.Stderr
			result.ExitCode = modResult.ExitCode
			result.Diff = modResult.Diff
			result.Data = modResult.Data
			if modResult.Message != "" {
				result.Message = modResult.Message
			}
			if modResult.Data != nil {
				if facts, ok := modResult.Data["ansible_facts"].(map[string]interface{}); ok {
					e.mergeAnsibleFacts(factTargetHost, facts)
				}
			}
		}
		e.applyTaskResultConditions(result, task, allVars)
		e.applyNoLogMask(result, task)

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

	if moduleName == "template" {
		if _, exists := params["vars"]; !exists {
			params["vars"] = vars
		}
	}
	if (moduleName == "template" || moduleName == "copy") && e.baseDir != "" {
		if src, ok := params["src"].(string); ok && src != "" && !filepath.IsAbs(src) {
			if !module.GetParamBool(params, "remote_src", false) {
				candidate := filepath.Join(e.baseDir, src)
				if _, err := os.Stat(candidate); err == nil {
					params["src"] = candidate
				} else if moduleName == "template" {
					tmplCandidate := filepath.Join(e.baseDir, "templates", src)
					if _, err := os.Stat(tmplCandidate); err == nil {
						params["src"] = tmplCandidate
					}
				} else if moduleName == "copy" {
					fileCandidate := filepath.Join(e.baseDir, "files", src)
					if _, err := os.Stat(fileCandidate); err == nil {
						params["src"] = fileCandidate
					}
				}
			}
		}
	}

	if params == nil {
		params = make(map[string]interface{})
	}
	if host != nil {
		if _, exists := params["inventory_hostname"]; !exists {
			params["inventory_hostname"] = host.Name
		}
	}

	checkMode := e.isTaskCheckMode(task)
	diffMode := e.isTaskDiffMode(task)
	params = e.withCheckModeParams(params, moduleName, checkMode)
	params = e.withDiffModeParams(params, moduleName, diffMode)

	modResult, handled, err := e.executeBuiltinModule(host, moduleName, params, task, vars)
	if handled {
		return modResult, err
	}
	return e.executeWithRetry(host, moduleName, params, task, vars, localAction)
}

func (e *Executor) executeHandlerModule(host *inventory.Host, moduleName string, params map[string]interface{}, taskName string, vars map[string]interface{}) (*module.Result, error) {
	if moduleName == "template" {
		if _, exists := params["vars"]; !exists {
			params["vars"] = vars
		}
	}
	if (moduleName == "template" || moduleName == "copy") && e.baseDir != "" {
		if src, ok := params["src"].(string); ok && src != "" && !filepath.IsAbs(src) {
			if !module.GetParamBool(params, "remote_src", false) {
				candidate := filepath.Join(e.baseDir, src)
				if _, err := os.Stat(candidate); err == nil {
					params["src"] = candidate
				}
			}
		}
	}
	params = e.withCheckModeParams(params, moduleName)
	params = e.withDiffModeParams(params, moduleName)

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
	if handler.Become != nil {
		handlerHost.Become = *handler.Become
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
		factHost := host
		if task != nil && task.DelegateTo != "" {
			if task.DelegateFacts {
				delHost := strings.TrimSpace(e.resolveString(task.DelegateTo, vars))
				if isLocalDelegate(delHost, vars) {
					delHost = "localhost"
				}
				factHost = &inventory.Host{Name: delHost}
			} else if origHostName, ok := vars["inventory_hostname"].(string); ok && origHostName != "" {
				factHost = &inventory.Host{Name: origHostName}
			}
		}
		return e.executeSetFactModule(factHost, params), true, nil
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

	timer := time.NewTimer(time.Duration(task.Async) * time.Second)
	defer timer.Stop()
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
		case <-timer.C:
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

func (e *Executor) executeReboot(host *inventory.Host, params map[string]interface{}) (*module.Result, error) {
	client := e.getOrCreateClient(host)
	params = e.withCheckModeParams(params, "reboot")
	mod := &module.RebootModule{}
	res, err := mod.Execute(client, params)
	if err == nil && res != nil && !res.Failed {
		e.mu.Lock()
		if e.facts != nil {
			delete(e.facts, host.Name)
		}
		e.mu.Unlock()
	}
	return res, err
}

func (e *Executor) executeWaitForConnection(host *inventory.Host, params map[string]interface{}) (*module.Result, error) {
	client := e.getOrCreateClient(host)
	params = e.withCheckModeParams(params, "wait_for_connection")
	mod := &module.WaitForConnectionModule{}
	return mod.Execute(client, params)
}

func (e *Executor) resolvePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) || e.baseDir == "" {
		return path
	}
	return filepath.Join(e.baseDir, path)
}

func splitLines(s string) []string {
	return util.SplitLines(s)
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
	registered["stdout_lines"] = splitLines(last.Stdout)
	registered["stderr_lines"] = splitLines(last.Stderr)
	registered["rc"] = last.ExitCode
	registered["msg"] = last.Message
	registered["diff"] = last.Diff
	registered["skipped"] = last.Skipped
	mergeMap(registered, last.Data)

	items := registered["results"].([]map[string]interface{})
	for _, item := range results {
		entry := map[string]interface{}{
			"changed":      item.Changed,
			"failed":       item.Failed,
			"skipped":      item.Skipped,
			"stdout":       item.Stdout,
			"stderr":       item.Stderr,
			"stdout_lines": splitLines(item.Stdout),
			"stderr_lines": splitLines(item.Stderr),
			"rc":           item.ExitCode,
			"msg":          item.Message,
			"diff":         item.Diff,
			"item":         item.Item,
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
	if e.facts[hostName] == nil {
		e.facts[hostName] = make(map[string]interface{})
	}
	e.facts[hostName][task.Register] = registered
	e.mu.Unlock()
	e.invalidateHostVarsCache(hostName)
}

func (e *Executor) mergeAnsibleFacts(hostName string, facts map[string]interface{}) {
	e.mu.Lock()

	if e.facts == nil {
		e.facts = make(map[string]map[string]interface{})
	}
	if e.facts[hostName] == nil {
		e.facts[hostName] = make(map[string]interface{})
	}

	for k, v := range facts {
		e.facts[hostName][k] = v
	}

	var afMap map[string]interface{}
	if existing, ok := e.facts[hostName]["ansible_facts"].(map[string]interface{}); ok && existing != nil {
		afMap = existing
	} else {
		afMap = make(map[string]interface{})
		e.facts[hostName]["ansible_facts"] = afMap
	}

	for k, v := range facts {
		afMap[k] = v
	}
	e.mu.Unlock()
	e.invalidateHostVarsCache(hostName)
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
	} else if task.Until != nil {
		// Ansible 规范：当声明 until 但未指定 retries 时，默认为 3 次重试（共 4 次尝试）
		attempts = 4
	}
	delay := task.Delay

	var lastResult *module.Result
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if e.isTaskCheckMode(task) && !supportsCheckMode(moduleName) {
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
		} else if e.evaluateUntil(task.Until, task.Register, lastResult, lastErr, vars, attempt) {
			if lastResult != nil {
				if lastResult.Data == nil {
					lastResult.Data = make(map[string]interface{})
				}
				lastResult.Data["attempts"] = attempt
			}
			return lastResult, lastErr
		}

		if attempt < attempts && delay > 0 {
			if !e.sleep(time.Duration(delay) * time.Second) {
				break
			}
		}
	}

	if task.Until != nil {
		if lastResult == nil {
			lastResult = &module.Result{}
		}
		if lastResult.Data == nil {
			lastResult.Data = make(map[string]interface{})
		}
		lastResult.Data["attempts"] = attempts
		lastResult.Failed = true
		if lastResult.ExitCode == 0 {
			lastResult.ExitCode = 1
		}
		lastResult.Message = "all retries exhausted; until condition not met"
		return lastResult, fmt.Errorf("all retries exhausted; until condition not met")
	}

	return lastResult, lastErr
}

func (e *Executor) isTaskCheckMode(task *Task) bool {
	if task != nil && task.CheckMode != nil {
		return *task.CheckMode
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.currentPlayCheckMode != nil {
		return *e.currentPlayCheckMode
	}
	return e.checkMode
}

func (e *Executor) isTaskDiffMode(task *Task) bool {
	if task != nil && task.Diff != nil {
		return *task.Diff
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.currentPlayDiff != nil {
		return *e.currentPlayDiff
	}
	return e.diffMode
}

func (e *Executor) withCheckModeParams(params map[string]interface{}, moduleName string, isCheckMode ...bool) map[string]interface{} {
	check := e.checkMode
	if len(isCheckMode) > 0 {
		check = isCheckMode[0]
	}
	if !check || !supportsCheckMode(moduleName) {
		return params
	}
	cloned := make(map[string]interface{}, len(params)+3)
	for key, value := range params {
		cloned[key] = value
	}
	cloned["__check_mode"] = true
	cloned["_ansible_check_mode"] = true
	cloned["check_mode"] = true
	return cloned
}

func (e *Executor) withDiffModeParams(params map[string]interface{}, moduleName string, isDiffMode ...bool) map[string]interface{} {
	diff := e.diffMode
	if len(isDiffMode) > 0 {
		diff = isDiffMode[0]
	}
	if !diff || !supportsDiffMode(moduleName) {
		return params
	}
	cloned := make(map[string]interface{}, len(params)+2)
	for key, value := range params {
		cloned[key] = value
	}
	cloned["__diff"] = true
	cloned["_ansible_diff"] = true
	return cloned
}

func supportsCheckMode(moduleName string) bool {
	switch moduleName {
	case "debug", "assert", "fail", "set_fact", "meta", "pause":
		return true
	default:
		return module.SupportsCheckMode(moduleName)
	}
}

func supportsDiffMode(moduleName string) bool {
	return module.SupportsDiffMode(moduleName)
}

func (e *Executor) executeRemoteModule(host *inventory.Host, moduleName string, params map[string]interface{}) (*module.Result, error) {
	if params == nil {
		params = make(map[string]interface{})
	}
	if host != nil {
		if _, exists := params["inventory_hostname"]; !exists {
			params["inventory_hostname"] = host.Name
		}
	}
	params, env := splitEnvironmentParams(params)

	mod, err := e.registry.Get(moduleName)
	if err != nil {
		if customPath, found := e.findCustomModule(moduleName); found {
			return e.executeCustomModule(host, customPath, params, false, env)
		}
		return nil, fmt.Errorf("module not found: %s", moduleName)
	}

	client, err := e.getOrCreateClientWithErr(host)
	if err != nil {
		return nil, err
	}
	if len(env) > 0 {
		client = ssh.NewEnvConnection(client, env)
	}

	return mod.Execute(client, params)
}

func (e *Executor) executeLocalModule(moduleName string, params map[string]interface{}) (*module.Result, error) {
	if params == nil {
		params = make(map[string]interface{})
	}
	localHost := &inventory.Host{
		Name:       "localhost",
		Address:    "localhost",
		Connection: "local",
	}
	if e.inventory != nil {
		if h, err := e.inventory.GetHost("localhost"); err == nil && h != nil {
			localHost = h
		}
	}
	if _, exists := params["inventory_hostname"]; !exists {
		params["inventory_hostname"] = localHost.Name
	}
	if _, err := e.registry.Get(moduleName); err != nil {
		if customPath, found := e.findCustomModule(moduleName); found {
			_, env := splitEnvironmentParams(params)
			return e.executeCustomModule(localHost, customPath, params, true, env)
		}
	}
	return e.executeRemoteModule(localHost, moduleName, params)
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

func (e *Executor) executeCustomModule(host *inventory.Host, customPath string, params map[string]interface{}, localAction bool, envOpt ...map[string]string) (*module.Result, error) {
	var env map[string]string
	if len(envOpt) > 0 && len(envOpt[0]) > 0 {
		env = envOpt[0]
	}

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
		if len(env) > 0 {
			cmd.Env = mergeLocalEnv(env)
		}

		output, err := cmd.CombinedOutput()
		return parseCustomModuleOutput(output, err)
	}

	// Remote execution
	client := e.getOrCreateClient(host)
	if client == nil {
		return nil, fmt.Errorf("failed to create client for custom module")
	}
	if len(env) > 0 {
		client = ssh.NewEnvConnection(client, env)
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

func (e *Executor) evaluateUntil(until interface{}, registerName string, result *module.Result, execErr error, vars map[string]interface{}, currentAttempt ...int) bool {
	attempt := 1
	if len(currentAttempt) > 0 && currentAttempt[0] > 0 {
		attempt = currentAttempt[0]
	}

	evalVars := make(map[string]interface{}, len(vars)+2)
	for k, v := range vars {
		evalVars[k] = v
	}

	status := map[string]interface{}{
		"changed":      result != nil && result.Changed,
		"stdout":       "",
		"stderr":       "",
		"stdout_lines": []string{},
		"stderr_lines": []string{},
		"rc":           0,
		"failed":       execErr != nil,
		"attempts":     attempt,
	}
	if result != nil {
		status["stdout"] = result.Stdout
		status["stderr"] = result.Stderr
		status["stdout_lines"] = splitLines(result.Stdout)
		status["stderr_lines"] = splitLines(result.Stderr)
		status["rc"] = result.ExitCode
		status["msg"] = result.Message
		mergeMap(status, result.Data)
		status["attempts"] = attempt
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
		"changed":      result.Changed,
		"failed":       result.Failed,
		"unreachable":  result.Unreachable,
		"stdout":       result.Stdout,
		"stderr":       result.Stderr,
		"stdout_lines": splitLines(result.Stdout),
		"stderr_lines": splitLines(result.Stderr),
		"rc":           result.ExitCode,
		"msg":          result.Message,
		"skipped":      result.Skipped,
	}
	mergeMap(status, result.Data)
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
		if result.Failed && task.IgnoreError {
			result.Failed = false
			result.Ignored = true
			if !strings.HasPrefix(result.Message, "ignored:") {
				if result.Message == "" {
					result.Message = "ignored error: failed_when condition matched"
				} else {
					result.Message = fmt.Sprintf("ignored: %s", result.Message)
				}
			}
		}
	}
}

func (e *Executor) maskNoLog(result *TaskResult, noLog bool) {
	if result == nil || !noLog {
		return
	}
	result.NoLog = true
	result.Stdout = "[VALUE_SPECIFIED_IN_NO_LOG_TASK]"
	result.Stderr = ""
	result.Diff = ""
	result.Data = map[string]interface{}{
		"censored": "the output has been hidden due to the fact that 'no_log: true' was specified for this result",
	}
	if result.Message != "" {
		result.Message = "[VALUE_SPECIFIED_IN_NO_LOG_TASK]"
	}
	if result.Item != nil {
		result.Item = "[VALUE_SPECIFIED_IN_NO_LOG_TASK]"
	}
}

func (e *Executor) applyNoLogMask(result *TaskResult, task *Task) {
	if task == nil {
		return
	}
	e.maskNoLog(result, task.NoLog)
}

func (e *Executor) resolveDelegateHost(delegateTo string, vars map[string]interface{}) (*inventory.Host, bool, error) {
	resolved := strings.TrimSpace(e.resolveString(delegateTo, vars))
	if resolved == "" {
		return nil, false, fmt.Errorf("delegate_to resolved to empty host")
	}
	if isLocalDelegate(resolved, nil) {
		localHost := &inventory.Host{
			Name:       "localhost",
			Address:    "localhost",
			Connection: "local",
			Variables:  make(map[string]string),
		}
		if e.inventory != nil {
			if h, err := e.inventory.GetHost("localhost"); err == nil && h != nil {
				localHost = h
			}
		}
		return localHost, true, nil
	}

	if e.inventory != nil {
		host, err := e.inventory.GetHost(resolved)
		if err == nil && host != nil {
			return host, false, nil
		}
	}

	// 动态临时主机：支持未在清单预定义的目标 IP 或主机名
	dHost := &inventory.Host{
		Name:       resolved,
		Address:    resolved,
		Port:       22,
		Connection: "ssh",
		Variables:  make(map[string]string),
	}
	return dHost, false, nil
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
	if len(env) == 0 {
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
	return true
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

	envPrefix := util.FormatEnvPrefix(env)
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

func mergeLocalEnv(env map[string]string) []string {
	merged := make([]string, 0, len(env)+len(os.Environ()))
	merged = append(merged, os.Environ()...)
	for key, value := range env {
		merged = append(merged, key+"="+value)
	}
	return merged
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

func buildAnsibleLoopContext(index int, items []interface{}) map[string]interface{} {
	length := len(items)
	loop := map[string]interface{}{
		"index":     index + 1,
		"index0":    index,
		"first":     index == 0,
		"last":      index == length-1,
		"length":    length,
		"revindex":  length - index,
		"revindex0": length - index - 1,
	}
	if index > 0 && index-1 < length {
		loop["previtem"] = items[index-1]
	}
	if index >= 0 && index+1 < length {
		loop["nextitem"] = items[index+1]
	}
	return loop
}
