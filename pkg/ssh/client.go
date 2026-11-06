package ssh

import (
	"context"
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
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/util"
)

// Client SSH 客户端封装
type Client struct {
	host        *inventory.Host
	client      *ssh.Client
	config      *ssh.ClientConfig
	authClosers []io.Closer
	proxyJump   string
	mu          sync.Mutex
	connected   bool
	lastUsed    time.Time
	sftpClient  *sftp.Client
	sftpMu      sync.Mutex
}

// ClientConfig SSH 客户端配置
type ClientConfig struct {
	Timeout         time.Duration
	MaxRetries      int
	RetryInterval   time.Duration
	KeepAlive       bool
	KeepAlivePeriod time.Duration
}

type sshCommonOptions struct {
	StrictHostKeyChecking *bool
	UserKnownHostsFile    string
	ConnectTimeout        time.Duration
	ProxyJump             string
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
	authClosers := make([]io.Closer, 0)

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
		if agentAuth, closer, err := sshAgentAuth(); err == nil {
			authMethods = append(authMethods, agentAuth)
			authClosers = append(authClosers, closer)
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

	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	hostKeyCallback, err := resolveHostKeyCallback(host.SSHCommonArgs)
	if err != nil {
		return nil, err
	}
	proxyJump := ""
	if opts, err := parseSSHCommonArgs(host.SSHCommonArgs); err != nil {
		return nil, fmt.Errorf("parse ansible_ssh_common_args: %w", err)
	} else {
		if opts.ConnectTimeout > 0 {
			timeout = opts.ConnectTimeout
		}
		if opts.ProxyJump != "" {
			proxyJump = opts.ProxyJump
		}
	}

	sshConfig := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         timeout,
	}

	return &Client{
		host:        host,
		config:      sshConfig,
		authClosers: authClosers,
		proxyJump:   proxyJump,
	}, nil
}

func parseSSHCommonArgs(args string) (sshCommonOptions, error) {
	opts := sshCommonOptions{}
	if strings.TrimSpace(args) == "" {
		return opts, nil
	}

	parts, err := util.SplitArgs(args)
	if err != nil {
		return opts, err
	}

	for i := 0; i < len(parts); i++ {
		part := parts[i]
		if part == "-o" {
			if i+1 >= len(parts) {
				return opts, fmt.Errorf("missing value after -o")
			}
			i++
			if err := applySSHOption(&opts, parts[i]); err != nil {
				return opts, err
			}
			continue
		}
		if strings.HasPrefix(part, "-o") && len(part) > 2 {
			if err := applySSHOption(&opts, part[2:]); err != nil {
				return opts, err
			}
		}
	}

	return opts, nil
}

func applySSHOption(opts *sshCommonOptions, option string) error {
	key, value, ok := strings.Cut(option, "=")
	if !ok {
		return fmt.Errorf("invalid ssh option: %s", option)
	}

	switch strings.ToLower(strings.TrimSpace(key)) {
	case "stricthostkeychecking":
		parsed, ok := parseSSHBool(value)
		if !ok {
			return fmt.Errorf("unsupported StrictHostKeyChecking value: %s", value)
		}
		opts.StrictHostKeyChecking = &parsed
	case "userknownhostsfile":
		opts.UserKnownHostsFile = expandKeyPath(strings.TrimSpace(value))
	case "proxyjump":
		opts.ProxyJump = strings.TrimSpace(value)
	case "connecttimeout":
		timeoutValue := strings.TrimSpace(value)
		seconds, err := time.ParseDuration(timeoutValue + "s")
		if err != nil {
			if duration, durationErr := time.ParseDuration(timeoutValue); durationErr == nil {
				opts.ConnectTimeout = duration
				return nil
			}
			return fmt.Errorf("invalid ConnectTimeout value: %s", value)
		}
		opts.ConnectTimeout = seconds
	}

	return nil
}

func parseSSHBool(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "yes", "true", "on":
		return true, true
	case "no", "false", "off":
		return false, true
	default:
		return false, false
	}
}

func resolveHostKeyCallback(commonArgs string) (ssh.HostKeyCallback, error) {
	opts, err := parseSSHCommonArgs(commonArgs)
	if err != nil {
		return nil, fmt.Errorf("parse ansible_ssh_common_args: %w", err)
	}

	if opts.StrictHostKeyChecking == nil || !*opts.StrictHostKeyChecking {
		return ssh.InsecureIgnoreHostKey(), nil
	}

	knownHostsPath := opts.UserKnownHostsFile
	if knownHostsPath == "" {
		knownHostsPath = expandKeyPath("~/.ssh/known_hosts")
	}

	callback, err := knownhosts.New(knownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("load known hosts %s: %w", knownHostsPath, err)
	}
	return callback, nil
}

// expandKeyPath 展开密钥路径中的 ~ 和环境变量
func expandKeyPath(path string) string {
	return util.ExpandPath(path)
}

// sshAgentAuth 返回 SSH Agent 认证方法
func sshAgentAuth() (ssh.AuthMethod, io.Closer, error) {
	// Unix 系统使用 SSH_AUTH_SOCK 环境变量
	sshAuthSock := os.Getenv("SSH_AUTH_SOCK")
	if sshAuthSock == "" {
		return nil, nil, fmt.Errorf("SSH_AUTH_SOCK not set")
	}

	conn, err := net.Dial("unix", sshAuthSock)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to SSH agent: %w", err)
	}

	agentClient := agent.NewClient(conn)
	return ssh.PublicKeysCallback(agentClient.Signers), conn, nil
}

// Connect 连接到远程主机
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	addr := net.JoinHostPort(c.host.Address, fmt.Sprintf("%d", c.host.Port))

	if c.proxyJump != "" {
		jumpClient, err := dialJumpHost(c.proxyJump, c.config)
		if err != nil {
			return fmt.Errorf("ssh dial jump host %s: %w", c.proxyJump, err)
		}
		c.authClosers = append(c.authClosers, jumpClient)

		ctx := context.Background()
		if c.config != nil && c.config.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, c.config.Timeout)
			defer cancel()
		}
		conn, err := jumpClient.DialContext(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("jump host %s dial target %s: %w", c.proxyJump, addr, err)
		}
		ncc, chans, reqs, err := ssh.NewClientConn(conn, addr, c.config)
		if err != nil {
			conn.Close()
			return fmt.Errorf("ssh new client conn via jump host %s: %w", c.proxyJump, err)
		}
		c.client = ssh.NewClient(ncc, chans, reqs)
		c.connected = true
		return nil
	}

	client, err := ssh.Dial("tcp", addr, c.config)
	if err != nil {
		return fmt.Errorf("ssh dial %s: %w", addr, err)
	}

	c.client = client
	c.connected = true

	return nil
}

func dialJumpHost(jumpSpec string, baseConfig *ssh.ClientConfig) (*ssh.Client, error) {
	jumpSpec = strings.TrimSpace(jumpSpec)
	user := baseConfig.User
	hostPort := jumpSpec
	if idx := strings.Index(jumpSpec, "@"); idx >= 0 {
		user = jumpSpec[:idx]
		hostPort = jumpSpec[idx+1:]
	}
	host := hostPort
	port := "22"
	if h, p, err := net.SplitHostPort(hostPort); err == nil {
		host = h
		port = p
	}
	addr := net.JoinHostPort(host, port)

	jumpConfig := &ssh.ClientConfig{
		User:            user,
		Auth:            baseConfig.Auth,
		HostKeyCallback: baseConfig.HostKeyCallback,
		Timeout:         baseConfig.Timeout,
	}

	return ssh.Dial("tcp", addr, jumpConfig)
}

// Close 关闭连接
func (c *Client) Close() error {
	c.invalidateSFTPClient()

	c.mu.Lock()
	defer c.mu.Unlock()

	var lastErr error
	if c.connected {
		if err := c.client.Close(); err != nil {
			lastErr = err
		}
		c.connected = false
		c.client = nil
	}

	for _, closer := range c.authClosers {
		if err := closer.Close(); err != nil && lastErr == nil {
			lastErr = err
		}
	}
	c.authClosers = nil

	return lastErr
}

// SetHost 更新当前连接的主机执行属性（如任务级提权）
func (c *Client) SetHost(host *inventory.Host) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.host = host
}

// Host 返回当前连接的目标主机
func (c *Client) Host() *inventory.Host {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.host
}

// AddAuthCloser 添加需要在连接关闭时释放的资源（如跳板机连接）
func (c *Client) AddAuthCloser(closer io.Closer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.authClosers = append(c.authClosers, closer)
}

// AuthClosers 获取当前关联的认证与跳板机资源列表
func (c *Client) AuthClosers() []io.Closer {
	c.mu.Lock()
	defer c.mu.Unlock()
	res := make([]io.Closer, len(c.authClosers))
	copy(res, c.authClosers)
	return res
}

// Execute 执行命令并返回输出
func (c *Client) Execute(cmd string) (*Result, error) {
	c.mu.Lock()
	become := c.host != nil && c.host.Become
	c.mu.Unlock()

	// 如果启用了 become，使用 sudo 执行
	if become {
		return c.ExecuteWithBecome(cmd)
	}
	return c.execute(cmd)
}

// ExecuteWithBecome 使用 become（sudo/su）执行命令
func (c *Client) ExecuteWithBecome(cmd string) (*Result, error) {
	c.mu.Lock()
	host := c.host
	c.mu.Unlock()

	fullCmd := BuildBecomeCommand(cmd, host)
	return c.execute(fullCmd)
}

// SetTimeout 动态更新 SSH 客户端连接超时时间
func (c *Client) SetTimeout(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.config != nil && d > 0 {
		c.config.Timeout = d
	}
}

// GetTimeout 获取当前 SSH 客户端连接超时时间
func (c *Client) GetTimeout() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.config != nil {
		return c.config.Timeout
	}
	return 0
}

// IsAlive 快速检测底层 SSH 物理连接是否仍然活跃
func (c *Client) IsAlive() bool {
	c.mu.Lock()
	client := c.client
	connected := c.connected
	c.mu.Unlock()
	if !connected || client == nil {
		return false
	}
	_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
	return err == nil
}

// newSession 安全获取底层 ssh.Session，在连接失效时持有互斥锁自愈重连，杜绝并发竞态与空指针解引用
func (c *Client) newSession() (*ssh.Session, error) {
	c.mu.Lock()
	if !c.connected || c.client == nil {
		c.mu.Unlock()
		if err := c.Connect(); err != nil {
			return nil, err
		}
		c.mu.Lock()
	}

	client := c.client
	if client == nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("ssh client not connected")
	}

	needKeepAlive := !c.lastUsed.IsZero() && time.Since(c.lastUsed) > 30*time.Second
	c.mu.Unlock()

	if needKeepAlive {
		if _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); err != nil {
			c.mu.Lock()
			c.connected = false
			if c.client != nil {
				_ = c.client.Close()
				c.client = nil
			}
			for _, closer := range c.authClosers {
				_ = closer.Close()
			}
			c.authClosers = nil
			c.mu.Unlock()
			c.invalidateSFTPClient()

			if recErr := c.Connect(); recErr != nil {
				return nil, fmt.Errorf("keepalive reconnect failed: %w", recErr)
			}
			c.mu.Lock()
			client = c.client
			c.mu.Unlock()
		}
	}

	session, err := client.NewSession()
	if err == nil {
		c.mu.Lock()
		c.lastUsed = time.Now()
		c.mu.Unlock()
		return session, nil
	}

	// 探测空闲长连接被对端关闭的情况，在锁内重置并自愈重连
	c.mu.Lock()
	c.connected = false
	if c.client != nil {
		_ = c.client.Close()
		c.client = nil
	}
	for _, closer := range c.authClosers {
		_ = closer.Close()
	}
	c.authClosers = nil
	c.mu.Unlock()
	c.invalidateSFTPClient()

	if recErr := c.Connect(); recErr != nil {
		return nil, fmt.Errorf("reconnect failed: %w (original error: %v)", recErr, err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client == nil {
		return nil, fmt.Errorf("ssh client not connected after reconnect")
	}
	sess, sessErr := c.client.NewSession()
	if sessErr != nil {
		return nil, fmt.Errorf("create session after reconnect: %w", sessErr)
	}
	c.lastUsed = time.Now()
	return sess, nil
}

// execute 实际执行命令的内部方法（带超时）
func (c *Client) execute(cmd string) (*Result, error) {
	session, err := c.newSession()
	if err != nil {
		return nil, err
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
			str, err := readBoundedOutput(stdout, MaxCommandOutputBytes)
			if err == nil {
				stdoutStr = str
			}
		}()

		go func() {
			defer wg.Done()
			str, err := readBoundedOutput(stderr, MaxCommandOutputBytes)
			if err == nil {
				stderrStr = str
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
	timeout := 5 * time.Minute
	if c.GetTimeout() > 0 {
		timeout = c.GetTimeout()
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
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
		result.Failed = result.ExitCode != 0

		return result, nil

	case <-timer.C:
		session.Close()
		return &Result{
			Host:    c.host.Name,
			Failed:  true,
			Message: fmt.Sprintf("command timed out after %v", timeout),
		}, fmt.Errorf("command timed out")
	}
}

// getSFTPClient 获取或复用长连接 SFTP 客户端，支持断开自动重连
func (c *Client) getSFTPClient() (*sftp.Client, error) {
	c.sftpMu.Lock()
	defer c.sftpMu.Unlock()

	if c.sftpClient != nil {
		return c.sftpClient, nil
	}

	if err := c.Connect(); err != nil {
		return nil, err
	}

	c.mu.Lock()
	sshClient := c.client
	c.mu.Unlock()

	if sshClient == nil {
		return nil, fmt.Errorf("ssh client not connected")
	}

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		// 探测空闲连接异常，自愈重连一次
		c.mu.Lock()
		c.connected = false
		if c.client != nil {
			_ = c.client.Close()
			c.client = nil
		}
		for _, closer := range c.authClosers {
			_ = closer.Close()
		}
		c.authClosers = nil
		c.mu.Unlock()

		if recErr := c.Connect(); recErr == nil {
			c.mu.Lock()
			sshClient = c.client
			c.mu.Unlock()
			if sshClient != nil {
				sftpClient, err = sftp.NewClient(sshClient)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("create sftp client: %w", err)
		}
	}

	c.sftpClient = sftpClient
	return c.sftpClient, nil
}

// invalidateSFTPClient 关闭并清空当前 SFTP 客户端缓存
func (c *Client) invalidateSFTPClient() {
	c.sftpMu.Lock()
	defer c.sftpMu.Unlock()
	if c.sftpClient != nil {
		_ = c.sftpClient.Close()
		c.sftpClient = nil
	}
}

// Upload 使用 SFTP 上传文件到远程主机（复用长连接 SFTP 会话）
func (c *Client) Upload(localPath, remotePath string) error {
	sftpClient, err := c.getSFTPClient()
	if err != nil {
		return err
	}

	// 打开本地文件
	localFile, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open local file: %w", err)
	}
	defer localFile.Close()

	// 构建目标路径：若指定以 / 结尾或远端路径已是目录，自动拼接本地文件名
	destPath := remotePath
	if len(remotePath) > 0 && remotePath[len(remotePath)-1] == '/' {
		destPath = remotePath + filepath.Base(localPath)
	} else if st, err := sftpClient.Stat(remotePath); err == nil && st.IsDir() {
		destPath = filepath.Join(remotePath, filepath.Base(localPath))
	}

	// 确保目标目录存在
	destDir := filepath.Dir(destPath)
	_ = sftpClient.MkdirAll(destDir)

	// 创建远程文件
	remoteFile, err := sftpClient.Create(destPath)
	if err != nil {
		// 如果 SFTP 客户端失效，清理缓存并重试一次
		c.invalidateSFTPClient()
		if sftpClient, recErr := c.getSFTPClient(); recErr == nil {
			_ = sftpClient.MkdirAll(destDir)
			remoteFile, err = sftpClient.Create(destPath)
		}
		if err != nil {
			return fmt.Errorf("create remote file %s: %w", destPath, err)
		}
	}

	// 复制文件内容并显式刷新与校验关闭
	if _, err := io.Copy(remoteFile, localFile); err != nil {
		_ = remoteFile.Close()
		return fmt.Errorf("copy file content: %w", err)
	}
	if err := remoteFile.Close(); err != nil {
		return fmt.Errorf("flush remote file %s: %w", destPath, err)
	}

	return nil
}

// Download 使用 SFTP 从远程主机下载文件（复用长连接 SFTP 会话）
func (c *Client) Download(remotePath, localPath string) error {
	sftpClient, err := c.getSFTPClient()
	if err != nil {
		return err
	}

	// 打开远程文件
	remoteFile, err := sftpClient.Open(remotePath)
	if err != nil {
		// 如果 SFTP 客户端失效，清理缓存并重试一次
		c.invalidateSFTPClient()
		if sftpClient, recErr := c.getSFTPClient(); recErr == nil {
			remoteFile, err = sftpClient.Open(remotePath)
		}
		if err != nil {
			return fmt.Errorf("open remote file %s: %w", remotePath, err)
		}
	}
	defer remoteFile.Close()

	// 创建本地文件
	localFile, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("create local file: %w", err)
	}
	defer localFile.Close()

	// 复制文件内容
	if _, err := io.Copy(localFile, remoteFile); err != nil {
		return fmt.Errorf("copy file content: %w", err)
	}

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
	Diff     string
	Data     map[string]interface{}
}

// IsChanged 判断是否有变更
func (r *Result) IsChanged() bool {
	return r.Changed
}

// IsFailed 判断是否失败
func (r *Result) IsFailed() bool {
	return r.Failed || r.ExitCode != 0
}
