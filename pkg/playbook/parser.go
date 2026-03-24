package playbook

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Parser Playbook 解析器
type Parser struct {
	baseDir string
}

// NewParser 创建新的解析器
func NewParser(baseDir string) *Parser {
	return &Parser{baseDir: baseDir}
}

// ParseFile 解析 playbook 文件
func (p *Parser) ParseFile(path string) (*Playbook, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read playbook: %w", err)
	}

	if p.baseDir == "" {
		p.baseDir = filepath.Dir(path)
	}
	return p.Parse(data)
}

// Parse 解析 playbook 内容
func (p *Parser) Parse(data []byte) (*Playbook, error) {
	var rawPlays []map[string]interface{}
	if err := yaml.Unmarshal(data, &rawPlays); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	playbook := &Playbook{
		Plays: make([]*Play, 0, len(rawPlays)),
	}

	for _, rawPlay := range rawPlays {
		play, err := p.parsePlay(rawPlay)
		if err != nil {
			return nil, fmt.Errorf("parse play: %w", err)
		}
		playbook.Plays = append(playbook.Plays, play)
	}

	return playbook, nil
}

// parsePlay 解析单个 play
func (p *Parser) parsePlay(raw map[string]interface{}) (*Play, error) {
	play := &Play{
		Vars:     make(map[string]interface{}),
		Tasks:    make([]*Task, 0),
		Handlers: make([]*Handler, 0),
	}

	if name, ok := raw["name"].(string); ok {
		play.Name = name
	}

	if hosts, ok := raw["hosts"].(string); ok {
		play.Hosts = hosts
	}

	if remoteUser, ok := raw["remote_user"].(string); ok {
		play.RemoteUser = remoteUser
	}

	// 解析 become（支持 bool 和 string 类型）
	switch v := raw["become"].(type) {
	case bool:
		play.Become = v
	case string:
		play.Become = v == "yes" || v == "true" || v == "1"
	}

	if becomeUser, ok := raw["become_user"].(string); ok {
		play.BecomeUser = becomeUser
	}

	if becomeMethod, ok := raw["become_method"].(string); ok {
		play.BecomeMethod = becomeMethod
	}

	// 检查 become_pass (sudo 密码)
	if becomePass, ok := raw["become_pass"].(string); ok {
		play.Vars["ansible_become_pass"] = becomePass
	}
	if becomePass, ok := raw["become_password"].(string); ok {
		play.Vars["ansible_become_pass"] = becomePass
	}

	if gatherF, ok := raw["gather_facts"].(bool); ok {
		play.GatherFacts = &gatherF
	}

	if vars, ok := raw["vars"].(map[string]interface{}); ok {
		play.Vars = vars
	}

	if varsFiles, ok := raw["vars_files"].([]interface{}); ok {
		play.VarsFiles = make([]string, len(varsFiles))
		for i, v := range varsFiles {
			play.VarsFiles[i] = fmt.Sprintf("%v", v)
		}
	}

	if tags, ok := raw["tags"].([]interface{}); ok {
		play.Tags = make([]string, len(tags))
		for i, t := range tags {
			play.Tags[i] = fmt.Sprintf("%v", t)
		}
	}

	if env, ok := raw["environment"].(map[string]interface{}); ok {
		play.Environment = make(map[string]string)
		for k, v := range env {
			play.Environment[k] = fmt.Sprintf("%v", v)
		}
	}

	if serial, ok := raw["serial"]; ok {
		play.Serial = serial
	}

	// 解析 tasks
	if tasks, ok := raw["tasks"].([]interface{}); ok {
		play.Tasks = make([]*Task, 0, len(tasks))
		for _, t := range tasks {
			if taskMap, ok := t.(map[string]interface{}); ok {
				task, err := p.parseTask(taskMap)
				if err != nil {
					return nil, err
				}
				play.Tasks = append(play.Tasks, task)
			}
		}
	}

	// 解析 pre_tasks
	if preTasks, ok := raw["pre_tasks"].([]interface{}); ok {
		play.PreTasks = make([]*Task, 0, len(preTasks))
		for _, t := range preTasks {
			if taskMap, ok := t.(map[string]interface{}); ok {
				task, err := p.parseTask(taskMap)
				if err != nil {
					return nil, err
				}
				play.PreTasks = append(play.PreTasks, task)
			}
		}
	}

	// 解析 post_tasks
	if postTasks, ok := raw["post_tasks"].([]interface{}); ok {
		play.PostTasks = make([]*Task, 0, len(postTasks))
		for _, t := range postTasks {
			if taskMap, ok := t.(map[string]interface{}); ok {
				task, err := p.parseTask(taskMap)
				if err != nil {
					return nil, err
				}
				play.PostTasks = append(play.PostTasks, task)
			}
		}
	}

	// 解析 handlers
	if handlers, ok := raw["handlers"].([]interface{}); ok {
		play.Handlers = make([]*Handler, 0, len(handlers))
		for _, h := range handlers {
			if handlerMap, ok := h.(map[string]interface{}); ok {
				handler, err := p.parseHandler(handlerMap)
				if err != nil {
					return nil, err
				}
				play.Handlers = append(play.Handlers, handler)
			}
		}
	}

	// 解析 roles
	if roles, ok := raw["roles"].([]interface{}); ok {
		play.Roles = roles
	}

	return play, nil
}

// parseTask 解析单个 task
func (p *Parser) parseTask(raw map[string]interface{}) (*Task, error) {
	task := &Task{
		Params: make(map[string]interface{}),
	}

	if name, ok := raw["name"].(string); ok {
		task.Name = name
	}

	if when, ok := raw["when"]; ok {
		task.When = when
	}

	if loop, ok := raw["loop"]; ok {
		task.Loop = loop
	}

	// 支持 with_items (等同于 loop)
	if withItems, ok := raw["with_items"]; ok {
		task.Loop = withItems
	}

	if register, ok := raw["register"].(string); ok {
		task.Register = register
	}

	if until, ok := raw["until"]; ok {
		task.Until = until
	}

	if retries, ok := raw["retries"].(int); ok {
		task.Retries = retries
	}

	if delay, ok := raw["delay"].(int); ok {
		task.Delay = delay
	}

	if ignore, ok := raw["ignore_errors"].(bool); ok {
		task.IgnoreError = ignore
	}

	if noLog, ok := raw["no_log"].(bool); ok {
		task.NoLog = noLog
	}

	if delegateTo, ok := raw["delegate_to"].(string); ok {
		task.DelegateTo = delegateTo
	}

	if runOnce, ok := raw["run_once"].(bool); ok {
		task.RunOnce = runOnce
	}

	if tags, ok := raw["tags"].([]interface{}); ok {
		task.Tags = make([]string, len(tags))
		for i, t := range tags {
			task.Tags[i] = fmt.Sprintf("%v", t)
		}
	} else if tagStr, ok := raw["tags"].(string); ok {
		// 支持单个 tag 作为字符串
		task.Tags = []string{tagStr}
	}

	if notify, ok := raw["notify"]; ok {
		task.Notify = notify
	}

	if become, ok := raw["become"].(bool); ok {
		task.Become = become
	}

	if becomeUser, ok := raw["become_user"].(string); ok {
		task.BecomeUser = becomeUser
	}

	// 提取模块名和参数
	moduleKeys := []string{
		"shell", "command", "copy", "file", "template", "ping", "setup",
		"yum", "apt", "service", "user", "group", "cron", "lineinfile",
		"stat", "fetch", "debug", "assert", "fail", "meta", "include",
		"import_tasks", "include_tasks", "set_fact", "add_host",
		"wait_for", "uri", "get_url", "unarchive", "git", "pip",
	}

	for _, key := range moduleKeys {
		if val, ok := raw[key]; ok {
			task.ModuleName = key
			task.Module = make(map[string]interface{})
			task.Module[key] = val

			// 解析模块参数
			switch v := val.(type) {
			case map[string]interface{}:
				task.Params = v
			case string:
				task.Params["_raw_params"] = v
			default:
				task.Params["_raw_params"] = fmt.Sprintf("%v", v)
			}
			break
		}
	}

	// 检查 local_action
	if localAction, ok := raw["local_action"].(string); ok {
		task.LocalAction = localAction
	}

	return task, nil
}

// parseHandler 解析单个 handler
func (p *Parser) parseHandler(raw map[string]interface{}) (*Handler, error) {
	handler := &Handler{
		Params: make(map[string]interface{}),
	}

	if name, ok := raw["name"].(string); ok {
		handler.Name = name
	}

	if listen, ok := raw["listen"].(string); ok {
		handler.Listen = listen
	}

	// 提取模块
	moduleKeys := []string{
		"shell", "command", "copy", "file", "template",
		"yum", "apt", "service", "systemd",
	}

	for _, key := range moduleKeys {
		if val, ok := raw[key]; ok {
			handler.ModuleName = key
			handler.Module = make(map[string]interface{})
			handler.Module[key] = val

			switch v := val.(type) {
			case map[string]interface{}:
				handler.Params = v
			case string:
				handler.Params["_raw_params"] = v
			default:
				handler.Params["_raw_params"] = fmt.Sprintf("%v", v)
			}
			break
		}
	}

	return handler, nil
}

// ParseRole 解析 role 目录
func (p *Parser) ParseRole(rolePath string) (*Role, error) {
	role := &Role{
		Tasks:    make([]*Task, 0),
		Handlers: make([]*Handler, 0),
		Vars:     make(map[string]interface{}),
		Defaults: make(map[string]interface{}),
	}

	role.Name = filepath.Base(rolePath)

	// 解析 tasks/main.yml
	tasksPath := filepath.Join(rolePath, "tasks", "main.yml")
	if _, err := os.Stat(tasksPath); err == nil {
		data, err := os.ReadFile(tasksPath)
		if err == nil {
			var rawTasks []interface{}
			if err := yaml.Unmarshal(data, &rawTasks); err == nil {
				for _, t := range rawTasks {
					if taskMap, ok := t.(map[string]interface{}); ok {
						task, err := p.parseTask(taskMap)
						if err == nil {
							role.Tasks = append(role.Tasks, task)
						}
					}
				}
			}
		}
	}

	// 解析 handlers/main.yml
	handlersPath := filepath.Join(rolePath, "handlers", "main.yml")
	if _, err := os.Stat(handlersPath); err == nil {
		data, err := os.ReadFile(handlersPath)
		if err == nil {
			var rawHandlers []interface{}
			if err := yaml.Unmarshal(data, &rawHandlers); err == nil {
				for _, h := range rawHandlers {
					if handlerMap, ok := h.(map[string]interface{}); ok {
						handler, err := p.parseHandler(handlerMap)
						if err == nil {
							role.Handlers = append(role.Handlers, handler)
						}
					}
				}
			}
		}
	}

	// 解析 vars/main.yml
	varsPath := filepath.Join(rolePath, "vars", "main.yml")
	if _, err := os.Stat(varsPath); err == nil {
		data, err := os.ReadFile(varsPath)
		if err == nil {
			yaml.Unmarshal(data, &role.Vars)
		}
	}

	// 解析 defaults/main.yml
	defaultsPath := filepath.Join(rolePath, "defaults", "main.yml")
	if _, err := os.Stat(defaultsPath); err == nil {
		data, err := os.ReadFile(defaultsPath)
		if err == nil {
			yaml.Unmarshal(data, &role.Defaults)
		}
	}

	return role, nil
}

// ResolveInclude 解析 include 语句
func (p *Parser) ResolveInclude(includePath string) ([]*Task, error) {
	// 处理相对路径
	if !filepath.IsAbs(includePath) {
		includePath = filepath.Join(p.baseDir, includePath)
	}

	data, err := os.ReadFile(includePath)
	if err != nil {
		return nil, fmt.Errorf("read include file: %w", err)
	}

	var rawTasks []interface{}
	if err := yaml.Unmarshal(data, &rawTasks); err != nil {
		return nil, fmt.Errorf("parse include file: %w", err)
	}

	tasks := make([]*Task, 0, len(rawTasks))
	for _, t := range rawTasks {
		if taskMap, ok := t.(map[string]interface{}); ok {
			task, err := p.parseTask(taskMap)
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, task)
		}
	}

	return tasks, nil
}

// isYAMLFile 检查是否为 YAML 文件
func isYAMLFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yml" || ext == ".yaml"
}
