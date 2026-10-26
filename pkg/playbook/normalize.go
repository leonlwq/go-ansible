package playbook

import (
	"fmt"
	"strings"
)

func normalizePlay(raw rawPlay) (*Play, error) {
	play := &Play{
		Name:           raw.Name,
		Hosts:          raw.Hosts,
		RemoteUser:     raw.RemoteUser,
		BecomeUser:     raw.BecomeUser,
		BecomeMethod:   raw.BecomeMethod,
		BecomePass:     raw.BecomePass,
		Vars:           make(map[string]interface{}),
		Tasks:          make([]*Task, 0, len(raw.Tasks)),
		Handlers:       make([]*Handler, 0, len(raw.Handlers)),
		PreTasks:       make([]*Task, 0, len(raw.PreTasks)),
		PostTasks:      make([]*Task, 0, len(raw.PostTasks)),
		Roles:          raw.Roles,
		Serial:         raw.Serial,
		MaxFailPct:     raw.MaxFailPct,
		AnyErrorsFatal: raw.AnyErrorsFatal,
	}
	play.ForceHandlers = extractBool(raw.ForceHandlers)
	play.Strategy = strings.ToLower(strings.TrimSpace(raw.Strategy))
	if play.Strategy == "" {
		play.Strategy = "linear"
	}
	play.Debugger = strings.ToLower(strings.TrimSpace(raw.Debugger))
	play.VarsPrompt = normalizeVarsPrompt(raw.VarsPrompt)

	play.Become = extractBool(raw.Become)

	if raw.GatherFacts != nil {
		gatherFacts := extractBool(raw.GatherFacts)
		play.GatherFacts = &gatherFacts
	}

	for key, value := range raw.Vars {
		play.Vars[key] = value
	}

	if raw.BecomePass != "" {
		play.Vars["ansible_become_pass"] = raw.BecomePass
	}
	if raw.BecomePasswd != "" {
		play.BecomePass = raw.BecomePasswd
		play.Vars["ansible_become_pass"] = raw.BecomePasswd
	}

	play.VarsFiles = append(play.VarsFiles, normalizeVarsFiles(raw.VarsFiles)...)
	play.Tags = extractStringList(raw.Tags)
	play.Environment = extractStringMap(raw.Environment)

	tasks, err := normalizeTaskList(raw.Tasks)
	if err != nil {
		return nil, fmt.Errorf("normalize tasks: %w", err)
	}
	play.Tasks = tasks

	preTasks, err := normalizeTaskList(raw.PreTasks)
	if err != nil {
		return nil, fmt.Errorf("normalize pre_tasks: %w", err)
	}
	play.PreTasks = preTasks

	postTasks, err := normalizeTaskList(raw.PostTasks)
	if err != nil {
		return nil, fmt.Errorf("normalize post_tasks: %w", err)
	}
	play.PostTasks = postTasks

	handlers, err := normalizeHandlerList(raw.Handlers)
	if err != nil {
		return nil, fmt.Errorf("normalize handlers: %w", err)
	}
	play.Handlers = handlers

	if raw.Throttle != nil {
		play.Throttle = extractInt(raw.Throttle)
	}
	if play.Throttle > 0 {
		applyThrottleToTasks(play.Tasks, play.Throttle)
		applyThrottleToTasks(play.PreTasks, play.Throttle)
		applyThrottleToTasks(play.PostTasks, play.Throttle)
	}
	if play.Debugger != "" {
		applyDebuggerToTasks(play.Tasks, play.Debugger)
		applyDebuggerToTasks(play.PreTasks, play.Debugger)
		applyDebuggerToTasks(play.PostTasks, play.Debugger)
	}

	return play, nil
}

func applyThrottleToTasks(tasks []*Task, throttle int) {
	if throttle <= 0 {
		return
	}
	for _, task := range tasks {
		if task.Throttle == 0 {
			task.Throttle = throttle
		}
		applyThrottleToTasks(task.Block, throttle)
		applyThrottleToTasks(task.Rescue, throttle)
		applyThrottleToTasks(task.Always, throttle)
	}
}

func applyDebuggerToTasks(tasks []*Task, debugger string) {
	if debugger == "" {
		return
	}
	for _, task := range tasks {
		if task.Debugger == "" {
			task.Debugger = debugger
		}
		applyDebuggerToTasks(task.Block, debugger)
		applyDebuggerToTasks(task.Rescue, debugger)
		applyDebuggerToTasks(task.Always, debugger)
	}
}

func normalizeTaskList(tasks []rawTask) ([]*Task, error) {
	result := make([]*Task, 0, len(tasks))
	for _, raw := range tasks {
		task, err := normalizeTask(raw)
		if err != nil {
			return nil, err
		}
		result = append(result, task)
	}
	return result, nil
}

func normalizeHandlerList(handlers []rawTask) ([]*Handler, error) {
	result := make([]*Handler, 0, len(handlers))
	for _, raw := range handlers {
		handler, err := normalizeHandler(raw)
		if err != nil {
			return nil, err
		}
		result = append(result, handler)
	}
	return result, nil
}

func normalizeTask(raw rawTask) (*Task, error) {
	task := &Task{
		Params: make(map[string]interface{}),
	}

	extractTaskCommonFields(raw, task)

	var err error
	task.Block, err = normalizeNestedTaskList(raw["block"])
	if err != nil {
		return nil, fmt.Errorf("normalize block: %w", err)
	}
	task.Rescue, err = normalizeNestedTaskList(raw["rescue"])
	if err != nil {
		return nil, fmt.Errorf("normalize rescue: %w", err)
	}
	task.Always, err = normalizeNestedTaskList(raw["always"])
	if err != nil {
		return nil, fmt.Errorf("normalize always: %w", err)
	}

	moduleName, params, err := extractModule(raw, reservedTaskFields)
	if err != nil {
		if len(task.Block) > 0 {
			task.ModuleName = "block"
			task.Module = map[string]interface{}{"block": raw["block"]}
			return task, nil
		}
		if task.LocalAction == "" {
			return nil, err
		}

		moduleName, params, err = parseInlineAction(task.LocalAction)
		if err != nil {
			return nil, err
		}
	}

	task.ModuleName = moduleName
	if rawValue, ok := raw[moduleName]; ok {
		task.Module = map[string]interface{}{moduleName: rawValue}
	} else {
		task.Module = map[string]interface{}{moduleName: params}
	}
	task.Params = params

	if task.ModuleName == "add_host" && raw["run_once"] == nil {
		task.RunOnce = true
	}
	if task.Throttle > 0 {
		applyThrottleToTasks(task.Block, task.Throttle)
		applyThrottleToTasks(task.Rescue, task.Throttle)
		applyThrottleToTasks(task.Always, task.Throttle)
	}
	if task.Debugger != "" {
		applyDebuggerToTasks(task.Block, task.Debugger)
		applyDebuggerToTasks(task.Rescue, task.Debugger)
		applyDebuggerToTasks(task.Always, task.Debugger)
	}

	return task, nil
}

func normalizeNestedTaskList(value interface{}) ([]*Task, error) {
	items, ok := value.([]interface{})
	if !ok || len(items) == 0 {
		return nil, nil
	}

	result := make([]*Task, 0, len(items))
	for _, item := range items {
		switch typed := item.(type) {
		case map[string]interface{}:
			task, err := normalizeTask(rawTask(typed))
			if err != nil {
				return nil, err
			}
			result = append(result, task)
		case rawTask:
			task, err := normalizeTask(typed)
			if err != nil {
				return nil, err
			}
			result = append(result, task)
		default:
			return nil, fmt.Errorf("nested task has invalid type %T", item)
		}
	}
	return result, nil
}

func normalizeHandler(raw rawTask) (*Handler, error) {
	handler := &Handler{
		Params: make(map[string]interface{}),
	}

	extractHandlerCommonFields(raw, handler)

	moduleName, params, err := extractModule(raw, reservedHandlerFields)
	if err != nil {
		return nil, err
	}

	handler.ModuleName = moduleName
	handler.Module = map[string]interface{}{moduleName: raw[moduleName]}
	handler.Params = params
	return handler, nil
}

func parseInlineAction(action string) (string, map[string]interface{}, error) {
	parts := strings.Fields(strings.TrimSpace(action))
	if len(parts) == 0 {
		return "", nil, fmt.Errorf("local_action is empty")
	}

	moduleName := parts[0]
	params := make(map[string]interface{})
	if len(parts) == 1 {
		return moduleName, params, nil
	}

	rawParams := make([]string, 0, len(parts)-1)
	for _, part := range parts[1:] {
		if idx := strings.Index(part, "="); idx > 0 {
			params[part[:idx]] = part[idx+1:]
			continue
		}
		rawParams = append(rawParams, part)
	}

	if len(rawParams) > 0 {
		params["_raw_params"] = strings.Join(rawParams, " ")
	}

	return moduleName, params, nil
}

func normalizeVarsFiles(raw interface{}) []string {
	if raw == nil {
		return nil
	}
	var res []string
	switch vf := raw.(type) {
	case string:
		if s := strings.TrimSpace(vf); s != "" {
			res = append(res, s)
		}
	case []interface{}:
		for _, item := range vf {
			switch v := item.(type) {
			case string:
				if s := strings.TrimSpace(v); s != "" {
					res = append(res, s)
				}
			case []interface{}:
				var sub []string
				for _, subItem := range v {
					if subStr := strings.TrimSpace(fmt.Sprintf("%v", subItem)); subStr != "" {
						sub = append(sub, subStr)
					}
				}
				if len(sub) > 0 {
					res = append(res, strings.Join(sub, ","))
				}
			case []string:
				if len(v) > 0 {
					res = append(res, strings.Join(v, ","))
				}
			}
		}
	case []string:
		for _, s := range vf {
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				res = append(res, trimmed)
			}
		}
	}
	return res
}

func normalizeVarsPrompt(raw interface{}) []*VarsPrompt {
	if raw == nil {
		return nil
	}
	var result []*VarsPrompt
	switch v := raw.(type) {
	case []interface{}:
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				vp := &VarsPrompt{
					Private: true, // Ansible 官方规范默认 private=true 隐藏输入
				}
				if name, ok := m["name"].(string); ok {
					vp.Name = strings.TrimSpace(name)
				}
				if prompt, ok := m["prompt"].(string); ok {
					vp.Prompt = strings.TrimSpace(prompt)
				} else {
					vp.Prompt = vp.Name
				}
				if def, ok := m["default"]; ok {
					vp.Default = def
				}
				if priv, ok := m["private"]; ok {
					vp.Private = extractBool(priv)
				}
				if conf, ok := m["confirm"]; ok {
					vp.Confirm = extractBool(conf)
				}
				if enc, ok := m["encrypt"].(string); ok {
					vp.Encrypt = strings.TrimSpace(enc)
				}
				if vp.Name != "" {
					result = append(result, vp)
				}
			}
		}
	}
	return result
}
