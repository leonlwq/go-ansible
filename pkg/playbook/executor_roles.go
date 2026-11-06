package playbook

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-ansible/pkg/inventory"
)

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
		hostResult.AppendTask(&TaskResult{
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
	role, err := e.getOrParseRole(rolePath)
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

	// 递归解析并执行前置依赖角色 (dependencies)
	for _, dep := range role.Dependencies {
		depName, depVars, depWhen, depTags := parseRoleSpec(dep)
		depTask := &Task{
			ModuleName: "include_role",
			Params: map[string]interface{}{
				"name": depName,
			},
			Vars: depVars,
			When: combineWhen(task.When, depWhen),
		}
		if len(depTags) > 0 {
			depTask.Tags = append(task.Tags, depTags...)
		} else {
			depTask.Tags = task.Tags
		}
		if err := e.executeIncludedRoleForHost(allHosts, host, depTask, childVars, handlers, runOnce, playEnv, hostResult); err != nil {
			return err
		}
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

func (e *Executor) executeIncludedTasksForHost(allHosts []*inventory.Host, host *inventory.Host, task *Task, vars map[string]interface{}, handlers []*Handler, runOnce map[string]bool, playEnv map[string]string, hostResult *HostResult) error {
	if hostResult == nil {
		return nil
	}

	if !e.shouldRunTaskByTags(task.Tags) {
		hostResult.AppendTask(&TaskResult{
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
		hostResult.AppendTask(&TaskResult{
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

func (e *Executor) getOrParseRole(rolePath string) (*Role, error) {
	if val, ok := e.parsedRoles.Load(rolePath); ok {
		return val.(*Role), nil
	}
	parser := NewParser(e.baseDir)
	role, err := parser.ParseRole(rolePath)
	if err != nil {
		return nil, err
	}
	actual, _ := e.parsedRoles.LoadOrStore(rolePath, role)
	return actual.(*Role), nil
}

func (e *Executor) getOrParseInclude(includePath string) ([]*Task, error) {
	if val, ok := e.parsedIncludes.Load(includePath); ok {
		return cloneTasks(val.([]*Task)), nil
	}
	parser := NewParser(e.baseDir)
	tasks, err := parser.ResolveInclude(includePath)
	if err != nil {
		return nil, err
	}
	e.parsedIncludes.Store(includePath, tasks)
	return cloneTasks(tasks), nil
}

func (e *Executor) loadIncludedTasks(task *Task, vars map[string]interface{}) ([]*Task, error) {
	includePath := e.resolveIncludePath(task, vars)
	if includePath == "" {
		return nil, fmt.Errorf("%s requires file path", task.ModuleName)
	}

	includedTasks, err := e.getOrParseInclude(includePath)
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

func (e *Executor) loadPlayRoles(play *Play) ([]*Task, []*Handler, map[string]interface{}, map[string]interface{}, error) {
	if len(play.Roles) == 0 {
		return nil, nil, nil, nil, nil
	}

	parser := NewParser(e.baseDir)
	tasks := make([]*Task, 0)
	handlers := make([]*Handler, 0)
	roleVars := make(map[string]interface{})
	roleDefaults := make(map[string]interface{})
	executedRoles := make(map[string]bool)

	var loadRole func(rawRole interface{}, callStack []string, parentWhen interface{}, parentTags []string) error
	loadRole = func(rawRole interface{}, callStack []string, parentWhen interface{}, parentTags []string) error {
		roleName, inlineVars, roleWhen, roleTags := parseRoleSpec(rawRole)
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

		effectiveWhen := combineWhen(parentWhen, roleWhen)
		var effectiveTags []string
		if len(parentTags) > 0 {
			effectiveTags = append(effectiveTags, parentTags...)
		}
		if len(roleTags) > 0 {
			effectiveTags = append(effectiveTags, roleTags...)
		}

		// 1. 递归优先加载前置依赖角色 (dependencies)
		for _, dep := range role.Dependencies {
			if err := loadRole(dep, newStack, effectiveWhen, effectiveTags); err != nil {
				return err
			}
		}

		// 2. 加载 Defaults, Vars 和 InlineVars
		// 官方 Ansible 规范：role defaults 是底座变量（优先级低于 inventory host/group vars）
		// role vars 与 inlineVars 则是高优先级角色变量
		for k, v := range role.Defaults {
			if _, exists := roleDefaults[k]; !exists {
				roleDefaults[k] = v
			}
		}
		for k, v := range role.Vars {
			roleVars[k] = v
		}
		for k, v := range inlineVars {
			roleVars[k] = v
		}

		// 3. 追加任务和 Handlers (级联应用 when 条件与 tags)
		roleTasks := cloneTasks(role.Tasks)
		for _, rt := range roleTasks {
			if effectiveWhen != nil {
				rt.When = combineWhen(effectiveWhen, rt.When)
			}
			if len(effectiveTags) > 0 {
				rt.Tags = append(rt.Tags, effectiveTags...)
			}
		}
		tasks = append(tasks, roleTasks...)

		roleHandlers := cloneHandlers(role.Handlers)
		for _, rh := range roleHandlers {
			if effectiveWhen != nil {
				rh.When = combineWhen(effectiveWhen, rh.When)
			}
			if len(effectiveTags) > 0 {
				rh.Tags = append(rh.Tags, effectiveTags...)
			}
		}
		handlers = append(handlers, roleHandlers...)

		executedRoles[roleName] = true
		return nil
	}

	for _, rawRole := range play.Roles {
		if err := loadRole(rawRole, nil, nil, nil); err != nil {
			return nil, nil, nil, nil, err
		}
	}

	return tasks, handlers, roleVars, roleDefaults, nil
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

func parseRoleSpec(rawRole interface{}) (string, map[string]interface{}, interface{}, []string) {
	switch v := rawRole.(type) {
	case string:
		return v, nil, nil, nil
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
		var when interface{}
		if w, ok := v["when"]; ok {
			when = w
		}
		var tags []string
		if t, ok := v["tags"]; ok {
			tags = extractStringList(t)
		}

		for k, val := range v {
			switch k {
			case "role", "name", "vars", "when", "tags":
				continue
			default:
				inlineVars[k] = val
			}
		}
		return roleName, inlineVars, when, tags
	default:
		return "", nil, nil, nil
	}
}

func cloneHandlers(handlers []*Handler) []*Handler {
	if len(handlers) == 0 {
		return nil
	}
	cloned := make([]*Handler, 0, len(handlers))
	for _, h := range handlers {
		if h == nil {
			continue
		}
		copied := *h
		cloned = append(cloned, &copied)
	}
	return cloned
}
