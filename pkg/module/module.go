package module

import (
	"fmt"

	"go-ansible/pkg/ssh"
)

// Module 模块接口
type Module interface {
	// Name 返回模块名称
	Name() string

	// Execute 执行模块
	Execute(client *ssh.Client, params map[string]interface{}) (*Result, error)

	// Validate 验证参数
	Validate(params map[string]interface{}) error
}

// Result 模块执行结果
type Result = ssh.Result

// Registry 模块注册表
type Registry struct {
	modules map[string]Module
}

// NewRegistry 创建新的模块注册表
func NewRegistry() *Registry {
	r := &Registry{
		modules: make(map[string]Module),
	}

	// 注册内置模块
	r.Register(&CommandModule{})
	r.Register(&ShellModule{})
	r.Register(&CopyModule{})
	r.Register(&FileModule{})
	r.Register(&TemplateModule{})
	r.Register(&PingModule{})
	r.Register(&SetupModule{})
	r.Register(&YumModule{})
	r.Register(&AptModule{})
	r.Register(&ServiceModule{})
	r.Register(&UserModule{})
	r.Register(&GroupModule{})
	r.Register(&CronModule{})
	r.Register(&LineinfileModule{})
	r.Register(&StatModule{})
	r.Register(&FetchModule{})

	return r
}

// Register 注册模块
func (r *Registry) Register(module Module) {
	r.modules[module.Name()] = module
}

// Get 获取模块
func (r *Registry) Get(name string) (Module, error) {
	if module, ok := r.modules[name]; ok {
		return module, nil
	}
	return nil, fmt.Errorf("module not found: %s", name)
}

// List 列出所有模块
func (r *Registry) List() []string {
	names := make([]string, 0, len(r.modules))
	for name := range r.modules {
		names = append(names, name)
	}
	return names
}

// GetParam 获取参数值，支持类型转换
func GetParam[T any](params map[string]interface{}, key string, defaultVal T) T {
	if val, ok := params[key]; ok {
		if v, ok := val.(T); ok {
			return v
		}
	}
	return defaultVal
}

// GetParamString 获取字符串参数
func GetParamString(params map[string]interface{}, key, defaultVal string) string {
	if val, ok := params[key]; ok {
		return fmt.Sprintf("%v", val)
	}
	return defaultVal
}

// GetParamBool 获取布尔参数
func GetParamBool(params map[string]interface{}, key string, defaultVal bool) bool {
	if val, ok := params[key]; ok {
		switch v := val.(type) {
		case bool:
			return v
		case string:
			return v == "yes" || v == "true" || v == "1"
		}
	}
	return defaultVal
}
