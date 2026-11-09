package playbook

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"go-ansible/pkg/inventory"
)

func (e *Executor) getTaskThrottleSem(task *Task) chan struct{} {
	if task == nil || task.Throttle <= 0 {
		return nil
	}
	key := fmt.Sprintf("%p_%d", task, task.Throttle)
	e.throttleMu.RLock()
	if e.throttleSems != nil {
		if sem, ok := e.throttleSems[key]; ok {
			e.throttleMu.RUnlock()
			return sem
		}
	}
	e.throttleMu.RUnlock()

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

// promptStep 在单步调试模式下询问用户是否执行该任务 (y: 执行, n: 跳过, c: 继续后续全部执行)
func (e *Executor) promptStep(taskName string) bool {
	if !e.stepMode {
		return true
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
		return true
	}
	return ans == "y" || ans == "yes"
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
			if !e.promptStep(taskName) {
				for _, h := range activeHosts {
					hr := e.getHostResult(result, h.Name)
					hr.AppendTask(&TaskResult{
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
			before := hostResult.TaskCount()
			hostVars := e.taskVarsForHost(host, playVars)
			if err := e.executeTaskEntryForHost(allHosts, host, task, hostVars, handlers, runOnce, playEnv, hostResult); err != nil {
				e.markHostFailed(host.Name)
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
			before := hostResult.TaskCount()
			hostVars := e.taskVarsForHost(h, playVars)

			if err := e.executeTaskEntryForHost(allHosts, h, task, hostVars, handlers, runOnce, playEnv, hostResult); err != nil {
				e.markHostFailed(h.Name)
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
				taskName := task.Name
				if taskName == "" {
					taskName = task.ModuleName
				}
				if e.stepMode && !e.promptStep(taskName) {
					hostResult.AppendTask(&TaskResult{
						Name:    taskName,
						Host:    host.Name,
						Skipped: true,
						Message: "skipped due to --step",
						Start:   time.Now(),
						End:     time.Now(),
					})
					continue
				}
				before := hostResult.TaskCount()
				hostVars := e.taskVarsForHost(host, playVars)
				if err := e.executeTaskEntryForHost(allHosts, host, task, hostVars, handlers, runOnce, playEnv, hostResult); err != nil {
					e.markHostFailed(host.Name)
					return err
				}
				if hostResultHasFailure(hostResult, before) {
					e.markHostFailed(host.Name)
					if anyErrorsFatal {
						e.markEndPlay()
					}
					break
				}
			}
		}
		if anyErrorsFatal && (e.shouldEndPlay() || e.failedTaskCount(result) > 0) {
			return fmt.Errorf("any_errors_fatal triggered")
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
				taskName := task.Name
				if taskName == "" {
					taskName = task.ModuleName
				}
				if e.stepMode && !e.promptStep(taskName) {
					hostResult.AppendTask(&TaskResult{
						Name:    taskName,
						Host:    h.Name,
						Skipped: true,
						Message: "skipped due to --step",
						Start:   time.Now(),
						End:     time.Now(),
					})
					continue
				}
				execSingleTask := func() error {
					if tSem := e.getTaskThrottleSem(task); tSem != nil {
						tSem <- struct{}{}
						defer func() { <-tSem }()
					}
					before := hostResult.TaskCount()
					hostVars := e.taskVarsForHost(h, playVars)
					if err := e.executeTaskEntryForHost(allHosts, h, task, hostVars, handlers, runOnce, playEnv, hostResult); err != nil {
						e.markHostFailed(h.Name)
						return err
					}
					if hostResultHasFailure(hostResult, before) {
						e.markHostFailed(h.Name)
						if anyErrorsFatal {
							e.markEndPlay()
						}
					}
					return nil
				}
				if err := execSingleTask(); err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errMu.Unlock()
					if anyErrorsFatal {
						e.markEndPlay()
					}
					return
				}
				if e.isHostFailed(h.Name) {
					break
				}
			}
		}(host)
	}

	wg.Wait()
	if anyErrorsFatal && (e.shouldEndPlay() || e.failedTaskCount(result) > 0) {
		if firstErr == nil {
			firstErr = fmt.Errorf("any_errors_fatal triggered")
		}
	}
	return firstErr
}

func (e *Executor) buildHostBatches(hosts []*inventory.Host, serial interface{}) [][]*inventory.Host {
	total := len(hosts)
	if total == 0 {
		return nil
	}

	var serialList []interface{}
	switch s := serial.(type) {
	case []interface{}:
		serialList = s
	case []string:
		for _, v := range s {
			serialList = append(serialList, v)
		}
	case []int:
		for _, v := range s {
			serialList = append(serialList, v)
		}
	}

	if len(serialList) > 0 {
		batches := make([][]*inventory.Host, 0)
		start := 0
		stepIdx := 0
		for start < total {
			var currentSerial interface{}
			if stepIdx < len(serialList) {
				currentSerial = serialList[stepIdx]
			} else {
				currentSerial = serialList[len(serialList)-1]
			}
			stepIdx++

			size := resolveSerialBatchSize(currentSerial, total)
			if size <= 0 {
				size = total
			}
			end := start + size
			if end > total {
				end = total
			}
			batches = append(batches, hosts[start:end])
			start = end
		}
		return batches
	}

	batchSize := total
	if size := resolveSerialBatchSize(serial, total); size > 0 {
		batchSize = size
	}
	if batchSize <= 0 || batchSize >= total {
		return [][]*inventory.Host{hosts}
	}

	batches := make([][]*inventory.Host, 0, (total+batchSize-1)/batchSize)
	for i := 0; i < total; i += batchSize {
		end := i + batchSize
		if end > total {
			end = total
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
