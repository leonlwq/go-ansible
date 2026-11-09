package playbook

import (
	"fmt"
	"os"
	"path/filepath"

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
	playbook, err := p.Parse(data)
	if err != nil {
		return nil, err
	}
	playbook.Path = path
	return playbook, nil
}

// Parse 解析 playbook 内容
func (p *Parser) Parse(data []byte) (*Playbook, error) {
	var rawPlays []rawPlay
	if err := yaml.Unmarshal(data, &rawPlays); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	playbook := &Playbook{
		Plays: make([]*Play, 0, len(rawPlays)),
	}

	for _, rawPlay := range rawPlays {
		play, err := normalizePlay(rawPlay)
		if err != nil {
			return nil, fmt.Errorf("parse play: %w", err)
		}
		playbook.Plays = append(playbook.Plays, play)
	}

	return playbook, nil
}

// ParseRole 解析 role 目录
func (p *Parser) ParseRole(rolePath string) (*Role, error) {
	return p.ParseRoleWithOptions(rolePath, "main", "main", "main", "main")
}

func resolveRoleSubCandidates(rolePath, subDir, entry string) []string {
	if entry == "" {
		entry = "main"
	}
	base := filepath.Join(rolePath, subDir, entry)
	if filepath.Ext(entry) != "" {
		return []string{base, base + ".yml", base + ".yaml"}
	}
	return []string{base + ".yml", base + ".yaml", base}
}

// ParseRoleWithOptions 按指定入口文件名 (tasks_from / vars_from / defaults_from / handlers_from) 解析 role 目录
func (p *Parser) ParseRoleWithOptions(rolePath, tasksFrom, varsFrom, defaultsFrom, handlersFrom string) (*Role, error) {
	role := &Role{
		Tasks:    make([]*Task, 0),
		Handlers: make([]*Handler, 0),
		Vars:     make(map[string]interface{}),
		Defaults: make(map[string]interface{}),
	}

	role.Name = filepath.Base(rolePath)

	// 解析 tasks/<tasksFrom>.yml|.yaml
	tasksFound := false
	for _, tasksPath := range resolveRoleSubCandidates(rolePath, "tasks", tasksFrom) {
		if _, err := os.Stat(tasksPath); err == nil {
			tasksFound = true
			data, err := os.ReadFile(tasksPath)
			if err == nil {
				var rawTasks []interface{}
				if err := yaml.Unmarshal(data, &rawTasks); err == nil {
					for _, t := range rawTasks {
						if taskMap, ok := t.(map[string]interface{}); ok {
							task, err := normalizeTask(taskMap)
							if err == nil {
								role.Tasks = append(role.Tasks, task)
							}
						}
					}
				}
			}
			break
		}
	}
	if !tasksFound && tasksFrom != "" && tasksFrom != "main" {
		return nil, fmt.Errorf("role %q tasks file %q not found", role.Name, tasksFrom)
	}

	// 解析 handlers/<handlersFrom>.yml|.yaml
	handlersFound := false
	for _, handlersPath := range resolveRoleSubCandidates(rolePath, "handlers", handlersFrom) {
		if _, err := os.Stat(handlersPath); err == nil {
			handlersFound = true
			data, err := os.ReadFile(handlersPath)
			if err == nil {
				var rawHandlers []interface{}
				if err := yaml.Unmarshal(data, &rawHandlers); err == nil {
					for _, h := range rawHandlers {
						if handlerMap, ok := h.(map[string]interface{}); ok {
							handler, err := normalizeHandler(handlerMap)
							if err == nil {
								role.Handlers = append(role.Handlers, handler)
							}
						}
					}
				}
			}
			break
		}
	}
	if !handlersFound && handlersFrom != "" && handlersFrom != "main" {
		return nil, fmt.Errorf("role %q handlers file %q not found", role.Name, handlersFrom)
	}

	// 解析 vars/<varsFrom>.yml|.yaml
	varsFound := false
	for _, varsPath := range resolveRoleSubCandidates(rolePath, "vars", varsFrom) {
		if _, err := os.Stat(varsPath); err == nil {
			varsFound = true
			data, err := os.ReadFile(varsPath)
			if err == nil {
				yaml.Unmarshal(data, &role.Vars)
			}
			break
		}
	}
	if !varsFound && varsFrom != "" && varsFrom != "main" {
		return nil, fmt.Errorf("role %q vars file %q not found", role.Name, varsFrom)
	}

	// 解析 defaults/<defaultsFrom>.yml|.yaml
	defaultsFound := false
	for _, defaultsPath := range resolveRoleSubCandidates(rolePath, "defaults", defaultsFrom) {
		if _, err := os.Stat(defaultsPath); err == nil {
			defaultsFound = true
			data, err := os.ReadFile(defaultsPath)
			if err == nil {
				yaml.Unmarshal(data, &role.Defaults)
			}
			break
		}
	}
	if !defaultsFound && defaultsFrom != "" && defaultsFrom != "main" {
		return nil, fmt.Errorf("role %q defaults file %q not found", role.Name, defaultsFrom)
	}

	// 解析 meta/main.yml 或 meta/main.yaml (角色依赖)
	metaFiles := []string{
		filepath.Join(rolePath, "meta", "main.yml"),
		filepath.Join(rolePath, "meta", "main.yaml"),
	}
	for _, metaPath := range metaFiles {
		if _, err := os.Stat(metaPath); err == nil {
			data, err := os.ReadFile(metaPath)
			if err == nil {
				var metaData struct {
					Dependencies []interface{} `yaml:"dependencies"`
				}
				if err := yaml.Unmarshal(data, &metaData); err == nil {
					role.Dependencies = metaData.Dependencies
				}
			}
			break
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
			task, err := normalizeTask(taskMap)
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, task)
		}
	}

	return tasks, nil
}
