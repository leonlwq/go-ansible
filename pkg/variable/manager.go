package variable

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Manager 变量管理器
type Manager struct {
	globals      map[string]interface{}
	groupVars    map[string]map[string]interface{}
	hostVars     map[string]map[string]interface{}
	vaultPass    string
	vaultSecrets []*VaultSecret
}

// NewManager 创建新的变量管理器
func NewManager() *Manager {
	return &Manager{
		globals:      make(map[string]interface{}),
		groupVars:    make(map[string]map[string]interface{}),
		hostVars:     make(map[string]map[string]interface{}),
		vaultSecrets: make([]*VaultSecret, 0),
	}
}

func (m *Manager) SetVaultPassword(password string) {
	m.vaultPass = password
}

// SetVaultSecrets 设置多个 Vault 密钥列表
func (m *Manager) SetVaultSecrets(secrets []*VaultSecret) {
	m.vaultSecrets = secrets
}

// AddVaultSecret 追加单个 Vault 密钥
func (m *Manager) AddVaultSecret(secret *VaultSecret) {
	if secret != nil {
		m.vaultSecrets = append(m.vaultSecrets, secret)
	}
}

func (m *Manager) decryptVaultData(data []byte) ([]byte, error) {
	if len(m.vaultSecrets) > 0 {
		plaintext, _, err := DecryptVaultWithSecrets(data, m.vaultSecrets)
		if err == nil {
			return plaintext, nil
		}
		if m.vaultPass == "" {
			return nil, err
		}
	}
	return DecryptVault(data, m.vaultPass)
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
	if IsVaultData(data) {
		data, err = m.decryptVaultData(data)
		if err != nil {
			return nil, err
		}
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	value, err := m.decodeYAMLNode(&root)
	if err != nil {
		return nil, err
	}
	vars, err := m.resolveVaultValues(value)
	if err != nil {
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
	if i, err := strconv.Atoi(expr); err == nil {
		return i, nil
	}
	if f, err := strconv.ParseFloat(expr, 64); err == nil {
		return f, nil
	}

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

func (m *Manager) resolveVaultValues(value interface{}) (map[string]interface{}, error) {
	resolved, err := m.resolveVaultNode(value)
	if err != nil {
		return nil, err
	}
	if typed, ok := resolved.(map[string]interface{}); ok {
		return typed, nil
	}
	return nil, fmt.Errorf("expected vars file to decode to map[string]interface{}")
}

func (m *Manager) resolveVaultNode(value interface{}) (interface{}, error) {
	switch typed := value.(type) {
	case map[string]interface{}:
		resolved := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			next, err := m.resolveVaultNode(item)
			if err != nil {
				return nil, err
			}
			resolved[key] = next
		}
		return resolved, nil
	case []interface{}:
		resolved := make([]interface{}, 0, len(typed))
		for _, item := range typed {
			next, err := m.resolveVaultNode(item)
			if err != nil {
				return nil, err
			}
			resolved = append(resolved, next)
		}
		return resolved, nil
	case vaultInlineValue:
		plaintext, err := m.decryptVaultData([]byte(typed))
		if err != nil {
			return nil, err
		}
		return strings.TrimRight(string(plaintext), "\n"), nil
	default:
		return value, nil
	}
}

type vaultInlineValue string

func (m *Manager) decodeYAMLNode(node *yaml.Node) (interface{}, error) {
	if node == nil {
		return nil, nil
	}

	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) == 0 {
			return nil, nil
		}
		return m.decodeYAMLNode(node.Content[0])
	case yaml.MappingNode:
		result := make(map[string]interface{}, len(node.Content)/2)
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i].Value
			value, err := m.decodeYAMLNode(node.Content[i+1])
			if err != nil {
				return nil, err
			}
			result[key] = value
		}
		return result, nil
	case yaml.SequenceNode:
		result := make([]interface{}, 0, len(node.Content))
		for _, child := range node.Content {
			value, err := m.decodeYAMLNode(child)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		return result, nil
	case yaml.ScalarNode:
		if node.Tag == "!vault" {
			return vaultInlineValue(node.Value), nil
		}
		var value interface{}
		if err := node.Decode(&value); err != nil {
			return nil, err
		}
		return value, nil
	default:
		var value interface{}
		if err := node.Decode(&value); err != nil {
			return nil, err
		}
		return value, nil
	}
}
