package ssh

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"go-ansible/pkg/inventory"
)

// LocalConnection 本地执行连接（无需 SSH 开销直接执行本地命令）
type LocalConnection struct {
	host    *inventory.Host
	timeout time.Duration
	mu      sync.Mutex
}

// NewLocalConnection 创建新的本地连接
func NewLocalConnection(host *inventory.Host) *LocalConnection {
	if host == nil {
		host = &inventory.Host{
			Name:       "localhost",
			Address:    "localhost",
			Connection: "local",
		}
	}
	return &LocalConnection{host: host}
}

// SetHost 更新当前连接的主机执行属性（如任务级提权），隔离浅拷贝防止外部并发修改污染
func (c *LocalConnection) SetHost(host *inventory.Host) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if host != nil {
		cloned := *host
		c.host = &cloned
	} else {
		c.host = nil
	}
}

// Host 返回当前本地连接的目标主机
func (c *LocalConnection) Host() *inventory.Host {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.host
}

// SetTimeout 设置执行超时
func (c *LocalConnection) SetTimeout(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timeout = d
}

// GetTimeout 获取执行超时
func (c *LocalConnection) GetTimeout() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.timeout
}

func (c *LocalConnection) Connect() error {
	return nil
}

func (c *LocalConnection) Close() error {
	return nil
}

func (c *LocalConnection) Execute(cmd string) (*Result, error) {
	c.mu.Lock()
	become := c.host != nil && c.host.Become
	c.mu.Unlock()

	if become {
		return c.ExecuteWithBecome(cmd)
	}
	return c.execute(cmd)
}

func (c *LocalConnection) ExecuteWithBecome(cmd string) (*Result, error) {
	c.mu.Lock()
	host := c.host
	c.mu.Unlock()

	fullCmd := BuildBecomeCommand(cmd, host)
	return c.execute(fullCmd)
}

func (c *LocalConnection) execute(cmdStr string) (*Result, error) {
	timeout := c.GetTimeout()
	var ctx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
		defer cancel()
	} else {
		ctx = context.Background()
	}

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", cmdStr)
	cmd.Env = append(os.Environ(), "LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8")

	stdoutBuf := NewBoundedBuffer(MaxCommandOutputBytes)
	stderrBuf := NewBoundedBuffer(MaxCommandOutputBytes)
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		hostName := "localhost"
		c.mu.Lock()
		if c.host != nil {
			hostName = c.host.Name
		}
		c.mu.Unlock()
		return &Result{
			Host:     hostName,
			Stdout:   stdoutBuf.String(),
			Stderr:   fmt.Sprintf("command timed out after %v", timeout),
			ExitCode: 124,
			Changed:  false,
			Failed:   true,
		}, fmt.Errorf("command timed out after %v", timeout)
	}

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	stdout := stdoutBuf.String()
	stderr := stderrBuf.String()

	hostName := "localhost"
	c.mu.Lock()
	if c.host != nil {
		hostName = c.host.Name
	}
	c.mu.Unlock()

	result := &Result{
		Host:     hostName,
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: exitCode,
		Changed:  true,
		Failed:   exitCode != 0,
	}

	if err != nil && exitCode != 0 {
		return result, fmt.Errorf("command execution failed with exit code %d: %s", exitCode, stderr)
	}

	return result, nil
}

func (c *LocalConnection) Upload(localPath, remotePath string) error {
	localPath = filepath.Clean(localPath)
	targetPath := filepath.Clean(remotePath)
	if dstStat, err := os.Stat(targetPath); err == nil && dstStat.IsDir() {
		targetPath = filepath.Join(targetPath, filepath.Base(localPath))
	}
	if localPath == targetPath {
		return nil
	}

	srcFile, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open src file %s: %w", localPath, err)
	}
	defer srcFile.Close()

	srcStat, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("stat src file %s: %w", localPath, err)
	}

	parentDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("create parent dir %s: %w", parentDir, err)
	}

	dstFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcStat.Mode().Perm())
	if err != nil {
		return fmt.Errorf("open dst file %s: %w", targetPath, err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("copy content from %s to %s: %w", localPath, targetPath, err)
	}
	return nil
}

func (c *LocalConnection) Download(remotePath, localPath string) error {
	return c.Upload(remotePath, localPath)
}
