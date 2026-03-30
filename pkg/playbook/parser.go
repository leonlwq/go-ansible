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
	return p.Parse(data)
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
						task, err := normalizeTask(taskMap)
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
						handler, err := normalizeHandler(handlerMap)
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
			task, err := normalizeTask(taskMap)
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, task)
		}
	}

	return tasks, nil
}
