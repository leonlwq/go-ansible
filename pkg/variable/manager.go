package variable

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Manager 变量管理器
type Manager struct {
	globals   map[string]interface{}
	groupVars map[string]map[string]interface{}
	hostVars  map[string]map[string]interface{}
}

// NewManager 创建新的变量管理器
func NewManager() *Manager {
	return &Manager{
		globals:   make(map[string]interface{}),
		groupVars: make(map[string]map[string]interface{}),
		hostVars:  make(map[string]map[string]interface{}),
	}
}

// SetGlobal 设置全局变量
func (m *Manager) SetGlobal(key string, value interface{}) {
	m.globals[key] = value
}

// GetGlobal 获取全局变量
func (m *Manager) GetGlobal(key string) (interface{}, bool) {
	val, ok := m.globals[key]
	return val, ok
}

// SetGroupVar 设置组变量
func (m *Manager) SetGroupVar(group, key string, value interface{}) {
	if _, ok := m.groupVars[group]; !ok {
		m.groupVars[group] = make(map[string]interface{})
	}
	m.groupVars[group][key] = value
}

// GetGroupVar 获取组变量
func (m *Manager) GetGroupVar(group, key string) (interface{}, bool) {
	if vars, ok := m.groupVars[group]; ok {
		val, exists := vars[key]
		return val, exists
	}
	return nil, false
}

// SetHostVar 设置主机变量
func (m *Manager) SetHostVar(host, key string, value interface{}) {
	if _, ok := m.hostVars[host]; !ok {
		m.hostVars[host] = make(map[string]interface{})
	}
	m.hostVars[host][key] = value
}

// GetHostVar 获取主机变量
func (m *Manager) GetHostVar(host, key string) (interface{}, bool) {
	if vars, ok := m.hostVars[host]; ok {
		val, exists := vars[key]
		return val, exists
	}
	return nil, false
}

// Resolve 为指定主机组解析变量（按优先级合并）
func (m *Manager) Resolve(host string, groups []string) map[string]interface{} {
	result := make(map[string]interface{})

	// 全局变量（最低优先级）
	for k, v := range m.globals {
		result[k] = v
	}

	// 组变量
	for _, group := range groups {
		if vars, ok := m.groupVars[group]; ok {
			for k, v := range vars {
				result[k] = v
			}
		}
	}

	// 主机变量（最高优先级）
	if vars, ok := m.hostVars[host]; ok {
		for k, v := range vars {
			result[k] = v
		}
	}

	return result
}

// LoadVarsFile 加载变量文件
func (m *Manager) LoadVarsFile(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var vars map[string]interface{}
	if err := yaml.Unmarshal(data, &vars); err != nil {
		return nil, err
	}

	return vars, nil
}

// LoadGroupVarsDir 加载组变量目录
func (m *Manager) LoadGroupVarsDir(dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}

	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yml" && ext != ".yaml" {
			return nil
		}

		groupName := strings.TrimSuffix(filepath.Base(path), ext)
		vars, err := m.LoadVarsFile(path)
		if err != nil {
			return err
		}

		if _, ok := m.groupVars[groupName]; !ok {
			m.groupVars[groupName] = make(map[string]interface{})
		}
		for k, v := range vars {
			m.groupVars[groupName][k] = v
		}

		return nil
	})
}

// LoadHostVarsDir 加载主机变量目录
func (m *Manager) LoadHostVarsDir(dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}

	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yml" && ext != ".yaml" {
			return nil
		}

		hostName := strings.TrimSuffix(filepath.Base(path), ext)
		vars, err := m.LoadVarsFile(path)
		if err != nil {
			return err
		}

		if _, ok := m.hostVars[hostName]; !ok {
			m.hostVars[hostName] = make(map[string]interface{})
		}
		for k, v := range vars {
			m.hostVars[hostName][k] = v
		}

		return nil
	})
}

// Merge 合并变量（后者覆盖前者）
func (m *Manager) Merge(base, override map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range base {
		result[k] = v
	}
	for k, v := range override {
		result[k] = v
	}
	return result
}

// Eval 简单表达式求值
func Eval(expr string, vars map[string]interface{}) (interface{}, error) {
	expr = strings.TrimSpace(expr)

	// 布尔值
	if expr == "true" || expr == "yes" {
		return true, nil
	}
	if expr == "false" || expr == "no" {
		return false, nil
	}

	// 数字
	// TODO: 实现数字解析

	// 变量引用
	if val, ok := vars[expr]; ok {
		return val, nil
	}

	// 简单字符串
	return expr, nil
}

// Interpolate 替换字符串中的变量引用
func Interpolate(s string, vars map[string]interface{}) string {
	result := s
	for k, v := range vars {
		// 替换 {{ var }} 和 {{var}}
		result = strings.ReplaceAll(result, "{{ "+k+" }}", fmt.Sprintf("%v", v))
		result = strings.ReplaceAll(result, "{{"+k+"}}", fmt.Sprintf("%v", v))
		result = strings.ReplaceAll(result, "{{ "+k+"}}", fmt.Sprintf("%v", v))
		result = strings.ReplaceAll(result, "{{"+k+" }}", fmt.Sprintf("%v", v))
	}
	return result
}
