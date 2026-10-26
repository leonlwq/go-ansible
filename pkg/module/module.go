package module

import (
	"fmt"
	"strconv"
	"strings"

	"go-ansible/pkg/ssh"
)

// Module 模块接口
type Module interface {
	// Name 返回模块名称
	Name() string

	// Execute 执行模块
	Execute(client ssh.Connection, params map[string]interface{}) (*Result, error)

	// Validate 验证参数
	Validate(params map[string]interface{}) error
}

// Result 模块执行结果
type Result = ssh.Result

// Connection 连接接口
type Connection = ssh.Connection

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
	r.Register(&GetURLModule{})
	r.Register(&UnarchiveModule{})
	r.Register(&ArchiveModule{})
	r.Register(&DockerContainerModule{})
	r.Register(&K8sModule{})
	r.Register(&URIModule{})
	r.Register(&PingModule{})
	r.Register(&SetupModule{})
	r.Register(&YumModule{})
	r.Register(&AptModule{})
	r.Register(&PackageModule{})
	r.Register(&ServiceModule{})
	r.Register(&SystemdModule{})
	r.Register(&UserModule{})
	r.Register(&GroupModule{})
	r.Register(&CronModule{})
	r.Register(&LineinfileModule{})
	r.Register(&ReplaceModule{})
	r.Register(&FindModule{})
	r.Register(&StatModule{})
	r.Register(&SlurpModule{})
	r.Register(&FetchModule{})
	r.Register(&WaitForModule{})
	r.Register(&GitModule{})
	r.Register(&PauseModule{})
	r.Register(&AsyncStatusModule{})
	r.Register(&SysctlModule{})
	r.Register(&HostnameModule{})
	r.Register(&MountModule{})
	r.Register(&WaitForConnectionModule{})
	r.Register(&RebootModule{})

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

// GetParamMode 获取文件权限参数，数值类型按八进制权限位解释
func GetParamMode(params map[string]interface{}, key, defaultVal string) string {
	val, ok := params[key]
	if !ok {
		return defaultVal
	}

	switch v := val.(type) {
	case int:
		return strconv.FormatInt(int64(v), 8)
	case int8:
		return strconv.FormatInt(int64(v), 8)
	case int16:
		return strconv.FormatInt(int64(v), 8)
	case int32:
		return strconv.FormatInt(int64(v), 8)
	case int64:
		return strconv.FormatInt(v, 8)
	case uint:
		return strconv.FormatUint(uint64(v), 8)
	case uint8:
		return strconv.FormatUint(uint64(v), 8)
	case uint16:
		return strconv.FormatUint(uint64(v), 8)
	case uint32:
		return strconv.FormatUint(uint64(v), 8)
	case uint64:
		return strconv.FormatUint(v, 8)
	case float64:
		return strconv.FormatInt(int64(v), 8)
	case float32:
		return strconv.FormatInt(int64(v), 8)
	default:
		return fmt.Sprintf("%v", val)
	}
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

// GetParamInt 获取整数参数
func GetParamInt(params map[string]interface{}, key string, defaultVal int) int {
	if val, ok := params[key]; ok {
		switch v := val.(type) {
		case int:
			return v
		case int64:
			return int(v)
		case float64:
			return int(v)
		case string:
			if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				return i
			}
		}
	}
	return defaultVal
}

// executeValidate 在受管端执行文件校验命令（支持 %s 占位符替换或作为追加参数）
func executeValidate(client ssh.Connection, validate, filePath string) error {
	if validate == "" {
		return nil
	}
	var valCmd string
	if strings.Contains(validate, "%s") {
		valCmd = strings.ReplaceAll(validate, "%s", filePath)
	} else {
		valCmd = fmt.Sprintf("%s %s", validate, filePath)
	}
	res, err := client.Execute(valCmd)
	if err != nil {
		return fmt.Errorf("validate command execution error: %w", err)
	}
	if res != nil && (res.ExitCode != 0 || res.Failed) {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(res.Stdout)
		}
		if msg == "" {
			msg = fmt.Sprintf("exit code %d", res.ExitCode)
		}
		return fmt.Errorf("validate failed: %s", msg)
	}
	return nil
}
