package ssh

import (
	"strings"

	"go-ansible/pkg/inventory"
)

// NewConnection 根据 Host 配置自动选择并创建对应协议的 Connection
func NewConnection(host *inventory.Host, cfg *ClientConfig) (Connection, error) {
	connType := strings.ToLower(strings.TrimSpace(host.Connection))
	if connType == "" && host.Variables != nil {
		connType = strings.ToLower(strings.TrimSpace(host.Variables["ansible_connection"]))
	}

	// 自动探测本地连接：未指定连接且地址或名称为 localhost/127.0.0.1/::1 时默认 local
	if connType == "" || connType == "smart" {
		addr := strings.ToLower(strings.TrimSpace(host.Address))
		name := strings.ToLower(strings.TrimSpace(host.Name))
		if addr == "localhost" || addr == "127.0.0.1" || addr == "::1" || name == "localhost" {
			connType = "local"
		} else {
			connType = "ssh"
		}
	}

	switch connType {
	case "local":
		return NewLocalConnection(host), nil
	case "docker":
		return NewDockerConnection(host), nil
	case "winrm":
		return NewWinRMConnection(host), nil
	default:
		// 默认 SSH 协议
		return NewClient(host, cfg)
	}
}
