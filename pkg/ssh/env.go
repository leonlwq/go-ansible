package ssh

import (
	"time"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/util"
)

// EnvConnection wraps an underlying Connection to inject environment variables into executed commands.
type EnvConnection struct {
	underlying Connection
	env        map[string]string
}

// NewEnvConnection creates a Connection wrapper that injects environment variables into Execute and ExecuteWithBecome.
func NewEnvConnection(conn Connection, env map[string]string) Connection {
	if conn == nil {
		return nil
	}
	if len(env) == 0 {
		return conn
	}
	// If already an EnvConnection, merge environments
	if existing, ok := conn.(*EnvConnection); ok {
		merged := make(map[string]string, len(existing.env)+len(env))
		for k, v := range existing.env {
			merged[k] = v
		}
		for k, v := range env {
			merged[k] = v
		}
		return &EnvConnection{
			underlying: existing.underlying,
			env:        merged,
		}
	}
	return &EnvConnection{
		underlying: conn,
		env:        env,
	}
}

func (e *EnvConnection) Connect() error {
	return e.underlying.Connect()
}

func (e *EnvConnection) Close() error {
	return e.underlying.Close()
}

func (e *EnvConnection) Upload(localPath, remotePath string) error {
	return e.underlying.Upload(localPath, remotePath)
}

func (e *EnvConnection) Download(remotePath, localPath string) error {
	return e.underlying.Download(remotePath, localPath)
}

func (e *EnvConnection) buildEnvCommand(cmd string) string {
	if len(e.env) == 0 {
		return cmd
	}
	return util.FormatEnvPrefix(e.env) + cmd
}

func (e *EnvConnection) Execute(cmd string) (*Result, error) {
	return e.underlying.Execute(e.buildEnvCommand(cmd))
}

func (e *EnvConnection) ExecuteWithBecome(cmd string) (*Result, error) {
	return e.underlying.ExecuteWithBecome(e.buildEnvCommand(cmd))
}

// Unwrap 返回被包装的底层连接
func (e *EnvConnection) Unwrap() Connection {
	return e.underlying
}

// SetHost 透传设置主机属性到底层连接
func (e *EnvConnection) SetHost(host *inventory.Host) {
	if setter, ok := e.underlying.(interface{ SetHost(*inventory.Host) }); ok {
		setter.SetHost(host)
	}
}

// Host 透传获取底层连接的主机对象
func (e *EnvConnection) Host() *inventory.Host {
	if getter, ok := e.underlying.(interface{ Host() *inventory.Host }); ok {
		return getter.Host()
	}
	return nil
}

// SetTimeout 透传设置超时到底层连接
func (e *EnvConnection) SetTimeout(d time.Duration) {
	if setter, ok := e.underlying.(interface{ SetTimeout(time.Duration) }); ok {
		setter.SetTimeout(d)
	}
}

// GetTimeout 透传获取底层连接的超时
func (e *EnvConnection) GetTimeout() time.Duration {
	if getter, ok := e.underlying.(interface{ GetTimeout() time.Duration }); ok {
		return getter.GetTimeout()
	}
	return 0
}

