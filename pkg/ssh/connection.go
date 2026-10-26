package ssh

// Connection 通用连接接口，支持 SSH、Local、Docker、WinRM 等多种底层实现
type Connection interface {
	// Connect 建立连接
	Connect() error

	// Close 关闭连接
	Close() error

	// Execute 执行命令并返回输出
	Execute(cmd string) (*Result, error)

	// ExecuteWithBecome 使用提权执行命令
	ExecuteWithBecome(cmd string) (*Result, error)

	// Upload 上传文件
	Upload(localPath, remotePath string) error

	// Download 下载文件
	Download(remotePath, localPath string) error
}
