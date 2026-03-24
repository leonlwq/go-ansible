package ssh

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"go-ansible/pkg/inventory"
)

// Client SSH 客户端封装
type Client struct {
	host      *inventory.Host
	client    *ssh.Client
	config    *ssh.ClientConfig
	mu        sync.Mutex
	connected bool
}

// ClientConfig SSH 客户端配置
type ClientConfig struct {
	Timeout         time.Duration
	MaxRetries      int
	RetryInterval   time.Duration
	KeepAlive       bool
	KeepAlivePeriod time.Duration
}

// DefaultConfig 返回默认配置
func DefaultConfig() *ClientConfig {
	return &ClientConfig{
		Timeout:         30 * time.Second,
		MaxRetries:      3,
		RetryInterval:   5 * time.Second,
		KeepAlive:       true,
		KeepAlivePeriod: 30 * time.Second,
	}
}

// NewClient 创建新的 SSH 客户端
func NewClient(host *inventory.Host, config *ClientConfig) (*Client, error) {
	if config == nil {
		config = DefaultConfig()
	}

	authMethods := make([]ssh.AuthMethod, 0)

	// 优先使用密钥认证
	if host.PrivateKey != "" {
		// 展开路径
		keyPath := expandKeyPath(host.PrivateKey)

		// 读取私钥文件
		key, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("read private key %s: %w", keyPath, err)
		}

		// 解析私钥（支持无密码和有密码的密钥）
		var signer ssh.Signer
		if host.Password != "" {
			// 如果提供了密码，尝试用密码解密密钥
			signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(host.Password))
		} else {
			signer, err = ssh.ParsePrivateKey(key)
		}
		if err != nil {
			return nil, fmt.Errorf("parse private key %s: %w", keyPath, err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}

	// 密码认证
	if host.Password != "" {
		authMethods = append(authMethods, ssh.Password(host.Password))
		authMethods = append(authMethods, ssh.KeyboardInteractive(
			func(user, instruction string, questions []string, echos []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = host.Password
				}
				return answers, nil
			},
		))
	}

	// 如果没有指定认证方式，尝试使用 SSH Agent
	if len(authMethods) == 0 {
		if agentAuth, err := sshAgentAuth(); err == nil {
			authMethods = append(authMethods, agentAuth)
		}
	}

	// 如果仍然没有认证方式，尝试默认密钥位置
	if len(authMethods) == 0 {
		defaultKeys := []string{
			"~/.ssh/id_rsa",
			"~/.ssh/id_ed25519",
			"~/.ssh/id_ecdsa",
			"~/.ssh/id_dsa",
		}
		for _, keyPath := range defaultKeys {
			expandedPath := expandKeyPath(keyPath)
			if key, err := os.ReadFile(expandedPath); err == nil {
				if signer, err := ssh.ParsePrivateKey(key); err == nil {
					authMethods = append(authMethods, ssh.PublicKeys(signer))
					break
				}
			}
		}
	}

	user := host.User
	if user == "" {
		user = "root"
	}

	sshConfig := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         config.Timeout,
	}

	return &Client{
		host:   host,
		config: sshConfig,
	}, nil
}

// expandKeyPath 展开密钥路径中的 ~ 和环境变量
func expandKeyPath(path string) string {
	if path == "" {
		return path
	}

	// 展开 ~ 为 home 目录
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		if len(path) > 1 {
			return filepath.Join(home, path[2:])
		}
		return home
	}

	// 展开环境变量
	if strings.Contains(path, "$") {
		return os.ExpandEnv(path)
	}

	return path
}

// sshAgentAuth 返回 SSH Agent 认证方法
func sshAgentAuth() (ssh.AuthMethod, error) {
	// Unix 系统使用 SSH_AUTH_SOCK 环境变量
	sshAuthSock := os.Getenv("SSH_AUTH_SOCK")
	if sshAuthSock == "" {
		return nil, fmt.Errorf("SSH_AUTH_SOCK not set")
	}

	conn, err := net.Dial("unix", sshAuthSock)
	if err != nil {
		return nil, fmt.Errorf("connect to SSH agent: %w", err)
	}

	// 使用 golang.org/x/crypto/ssh/agent 包
	// 这里简化处理，实际使用时需要导入 ssh/agent 包
	_ = conn

	return nil, fmt.Errorf("SSH agent auth not fully implemented")
}

// Connect 连接到远程主机
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	addr := net.JoinHostPort(c.host.Address, fmt.Sprintf("%d", c.host.Port))
	client, err := ssh.Dial("tcp", addr, c.config)
	if err != nil {
		return fmt.Errorf("ssh dial %s: %w", addr, err)
	}

	c.client = client
	c.connected = true

	return nil
}

// Close 关闭连接
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		return nil
	}

	err := c.client.Close()
	c.connected = false
	c.client = nil
	return err
}

// Execute 执行命令并返回输出
func (c *Client) Execute(cmd string) (*Result, error) {
	// 如果启用了 become，使用 sudo 执行
	if c.host.Become {
		return c.ExecuteWithBecome(cmd)
	}
	return c.execute(cmd)
}

// ExecuteWithBecome 使用 become（sudo）执行命令
func (c *Client) ExecuteWithBecome(cmd string) (*Result, error) {
	becomeUser := c.host.BecomeUser
	if becomeUser == "" {
		becomeUser = "root"
	}

	becomeMethod := c.host.BecomeMethod
	if becomeMethod == "" {
		becomeMethod = "sudo"
	}

	var sudoCmd string
	if becomeMethod == "sudo" {
		// 构建 sudo 命令
		if c.host.BecomePass != "" {
			// 有密码时，使用 -S 从 stdin 读取密码
			sudoCmd = fmt.Sprintf("echo '%s' | sudo -S -u %s", c.host.BecomePass, becomeUser)
		} else {
			// 无密码时，使用 -n 避免提示（需要 NOPASSWD 配置）
			sudoCmd = fmt.Sprintf("sudo -n -u %s", becomeUser)
		}
	} else if becomeMethod == "su" {
		if c.host.BecomePass != "" {
			sudoCmd = fmt.Sprintf("echo '%s' | su - %s -c", c.host.BecomePass, becomeUser)
			return c.execute(fmt.Sprintf("%s '%s'", sudoCmd, cmd))
		}
		sudoCmd = fmt.Sprintf("su - %s -c", becomeUser)
		return c.execute(fmt.Sprintf("%s '%s'", sudoCmd, cmd))
	} else {
		// 使用自定义 become exe
		becomeExe := c.host.BecomeExe
		if becomeExe == "" {
			becomeExe = "sudo"
		}
		sudoCmd = becomeExe
	}

	// 执行 sudo 命令
	fullCmd := fmt.Sprintf("%s %s", sudoCmd, cmd)
	return c.execute(fullCmd)
}

// execute 实际执行命令的内部方法（带超时）
func (c *Client) execute(cmd string) (*Result, error) {
	if err := c.Connect(); err != nil {
		return nil, err
	}

	session, err := c.client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	defer session.Close()

	// 设置环境变量
	session.Setenv("LANG", "en_US.UTF-8")
	session.Setenv("LC_ALL", "en_US.UTF-8")

	stdout, err := session.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := session.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := session.Start(cmd); err != nil {
		return nil, fmt.Errorf("start command: %w", err)
	}

	// 使用 channel 和超时来避免卡住
	type outputResult struct {
		stdout string
		stderr string
		err    error
	}

	doneCh := make(chan outputResult, 1)

	go func() {
		var stdoutStr, stderrStr string
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			data, err := io.ReadAll(stdout)
			if err == nil {
				stdoutStr = string(data)
			}
		}()

		go func() {
			defer wg.Done()
			data, err := io.ReadAll(stderr)
			if err == nil {
				stderrStr = string(data)
			}
		}()

		err := session.Wait()
		wg.Wait()

		doneCh <- outputResult{
			stdout: stdoutStr,
			stderr: stderrStr,
			err:    err,
		}
	}()

	// 等待命令执行完成，设置超时
	timeout := time.After(5 * time.Minute)
	select {
	case res := <-doneCh:
		result := &Result{
			Stdout: res.stdout,
			Stderr: res.stderr,
			Host:   c.host.Name,
		}

		if res.err != nil {
			if exitErr, ok := res.err.(*ssh.ExitError); ok {
				result.ExitCode = exitErr.ExitStatus()
			} else {
				return result, fmt.Errorf("wait command: %w", res.err)
			}
		}

		return result, nil

	case <-timeout:
		session.Close()
		return &Result{
			Host:    c.host.Name,
			Failed:  true,
			Message: "command timed out after 5 minutes",
		}, fmt.Errorf("command timed out")
	}
}

// Upload 上传文件到远程主机
// Upload 使用 SFTP 上传文件到远程主机
func (c *Client) Upload(localPath, remotePath string) error {
	if err := c.Connect(); err != nil {
		return err
	}

	fmt.Printf("[SSH] Uploading %s to %s\n", localPath, remotePath)

	// 创建 SFTP 客户端
	sftpClient, err := sftp.NewClient(c.client)
	if err != nil {
		return fmt.Errorf("create sftp client: %w", err)
	}
	defer sftpClient.Close()

	// 打开本地文件
	localFile, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open local file: %w", err)
	}
	defer localFile.Close()

	// 获取本地文件信息
	stat, err := localFile.Stat()
	if err != nil {
		return fmt.Errorf("stat local file: %w", err)
	}
	fmt.Printf("[SSH] File size: %d bytes\n", stat.Size())

	// 构建目标路径
	destPath := remotePath
	if remotePath[len(remotePath)-1] == '/' {
		destPath = remotePath + filepath.Base(localPath)
	}
	fmt.Printf("[SSH] Destination path: %s\n", destPath)

	// 确保目标目录存在
	destDir := filepath.Dir(destPath)
	fmt.Printf("[SSH] Creating directory: %s\n", destDir)
	sftpClient.MkdirAll(destDir)

	// 创建远程文件
	remoteFile, err := sftpClient.Create(destPath)
	if err != nil {
		return fmt.Errorf("create remote file %s: %w", destPath, err)
	}
	defer remoteFile.Close()

	// 复制文件内容
	written, err := io.Copy(remoteFile, localFile)
	if err != nil {
		return fmt.Errorf("copy file content: %w", err)
	}
	fmt.Printf("[SSH] Written %d bytes\n", written)

	fmt.Println("[SSH] Upload successful")
	return nil
}

// Download 使用 SFTP 从远程主机下载文件
func (c *Client) Download(remotePath, localPath string) error {
	if err := c.Connect(); err != nil {
		return err
	}

	fmt.Printf("[SSH] Downloading %s to %s\n", remotePath, localPath)

	// 创建 SFTP 客户端
	sftpClient, err := sftp.NewClient(c.client)
	if err != nil {
		return fmt.Errorf("create sftp client: %w", err)
	}
	defer sftpClient.Close()

	// 打开远程文件
	remoteFile, err := sftpClient.Open(remotePath)
	if err != nil {
		return fmt.Errorf("open remote file %s: %w", remotePath, err)
	}
	defer remoteFile.Close()

	// 创建本地文件
	localFile, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("create local file: %w", err)
	}
	defer localFile.Close()

	// 复制文件内容
	written, err := io.Copy(localFile, remoteFile)
	if err != nil {
		return fmt.Errorf("copy file content: %w", err)
	}
	fmt.Printf("[SSH] Downloaded %d bytes\n", written)

	fmt.Println("[SSH] Download successful")
	return nil
}

// Result 命令执行结果
type Result struct {
	Host     string
	Stdout   string
	Stderr   string
	ExitCode int
	Changed  bool
	Failed   bool
	Message  string
}

// IsChanged 判断是否有变更
func (r *Result) IsChanged() bool {
	return r.Changed
}

// IsFailed 判断是否失败
func (r *Result) IsFailed() bool {
	return r.Failed || r.ExitCode != 0
}
