package ssh

import (
	"fmt"

	"go-ansible/pkg/inventory"
)

// WinRMConnection Windows Remote Management (WinRM) 连接插件（预留接口）
type WinRMConnection struct {
	host *inventory.Host
}

// NewWinRMConnection 创建新的 WinRM 连接
func NewWinRMConnection(host *inventory.Host) *WinRMConnection {
	return &WinRMConnection{host: host}
}

func (c *WinRMConnection) Connect() error {
	port := c.host.Port
	if port <= 0 {
		port = 5985
	}
	return fmt.Errorf("winrm connection to %s:%d: native winrm transport is reserved for enterprise Windows targets", c.host.Address, port)
}

func (c *WinRMConnection) Close() error {
	return nil
}

func (c *WinRMConnection) Execute(cmd string) (*Result, error) {
	return nil, fmt.Errorf("winrm connection: execute not available on %s", c.host.Name)
}

func (c *WinRMConnection) ExecuteWithBecome(cmd string) (*Result, error) {
	return nil, fmt.Errorf("winrm connection: become not available on %s", c.host.Name)
}

func (c *WinRMConnection) Upload(localPath, remotePath string) error {
	return fmt.Errorf("winrm connection: upload not available on %s", c.host.Name)
}

func (c *WinRMConnection) Download(remotePath, localPath string) error {
	return fmt.Errorf("winrm connection: download not available on %s", c.host.Name)
}
