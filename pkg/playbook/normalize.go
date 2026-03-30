package playbook

import "fmt"

func normalizePlay(raw rawPlay) (*Play, error) {
	play := &Play{
		Name:         raw.Name,
		Hosts:        raw.Hosts,
		RemoteUser:   raw.RemoteUser,
		BecomeUser:   raw.BecomeUser,
		BecomeMethod: raw.BecomeMethod,
		Vars:         make(map[string]interface{}),
		Tasks:        make([]*Task, 0, len(raw.Tasks)),
		Handlers:     make([]*Handler, 0, len(raw.Handlers)),
		PreTasks:     make([]*Task, 0, len(raw.PreTasks)),
		PostTasks:    make([]*Task, 0, len(raw.PostTasks)),
		Roles:        raw.Roles,
		Serial:       raw.Serial,
	}

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
		play.Vars["ansible_become_pass"] = raw.BecomePasswd
	}

	play.VarsFiles = append(play.VarsFiles, raw.VarsFiles...)
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

	return play, nil
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

	moduleName, params, err := extractModule(raw, reservedTaskFields)
	if err != nil {
		return nil, err
	}

	task.ModuleName = moduleName
	task.Module = map[string]interface{}{moduleName: raw[moduleName]}
	task.Params = params
	return task, nil
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
