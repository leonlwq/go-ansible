package ssh

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"go-ansible/pkg/inventory"
)

// DockerConnection 基于 docker exec 和 docker cp 的容器连接插件
type DockerConnection struct {
	host      *inventory.Host
	container string
	timeout   time.Duration
	mu        sync.Mutex
}

// NewDockerConnection 创建新的 Docker 容器连接
func NewDockerConnection(host *inventory.Host) *DockerConnection {
	container := host.Address
	if container == "" {
		container = host.Name
	}
	return &DockerConnection{
		host:      host,
		container: container,
	}
}

// SetHost 更新当前连接的主机执行属性（如任务级提权）
func (c *DockerConnection) SetHost(host *inventory.Host) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.host = host
	if host.Address != "" {
		c.container = host.Address
	} else if host.Name != "" {
		c.container = host.Name
	}
}

// Host 返回当前 Docker 连接的目标主机
func (c *DockerConnection) Host() *inventory.Host {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.host
}

// SetTimeout 设置命令超时
func (c *DockerConnection) SetTimeout(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timeout = d
}

// GetTimeout 获取命令超时
func (c *DockerConnection) GetTimeout() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.timeout
}

func (c *DockerConnection) Connect() error {
	cmd := exec.Command("docker", "inspect", "--format", "{{.State.Running}}", c.container)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker container %s not reachable: %w (%s)", c.container, err, strings.TrimSpace(string(out)))
	}
	if strings.TrimSpace(string(out)) != "true" {
		return fmt.Errorf("docker container %s is not running", c.container)
	}
	return nil
}

func (c *DockerConnection) Close() error {
	return nil
}

func (c *DockerConnection) Execute(cmd string) (*Result, error) {
	c.mu.Lock()
	become := false
	user := ""
	if c.host != nil {
		become = c.host.Become
		user = c.host.User
	}
	c.mu.Unlock()

	if become {
		return c.ExecuteWithBecome(cmd)
	}
	return c.execute(cmd, user)
}

func (c *DockerConnection) ExecuteWithBecome(cmd string) (*Result, error) {
	c.mu.Lock()
	becomeUser := ""
	if c.host != nil {
		becomeUser = c.host.BecomeUser
	}
	c.mu.Unlock()

	if becomeUser == "" {
		becomeUser = "root"
	}
	return c.execute(cmd, becomeUser)
}

func (c *DockerConnection) execute(cmdStr, user string) (*Result, error) {
	c.mu.Lock()
	container := c.container
	hostName := ""
	if c.host != nil {
		hostName = c.host.Name
	}
	timeout := c.timeout
	c.mu.Unlock()

	var ctx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
		defer cancel()
	} else {
		ctx = context.Background()
	}

	args := []string{"exec", "-i"}
	if user != "" {
		args = append(args, "-u", user)
	}
	args = append(args, container, "/bin/sh", "-c", cmdStr)

	cmd := exec.CommandContext(ctx, "docker", args...)
	stdoutBuf := NewBoundedBuffer(MaxCommandOutputBytes)
	stderrBuf := NewBoundedBuffer(MaxCommandOutputBytes)
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
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

	result := &Result{
		Host:     hostName,
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: exitCode,
		Changed:  true,
		Failed:   exitCode != 0,
	}

	if err != nil && exitCode != 0 {
		return result, fmt.Errorf("docker exec failed with exit code %d: %s", exitCode, stderr)
	}
	return result, nil
}

func (c *DockerConnection) Upload(localPath, remotePath string) error {
	c.mu.Lock()
	container := c.container
	c.mu.Unlock()

	target := fmt.Sprintf("%s:%s", container, remotePath)
	cmd := exec.Command("docker", "cp", localPath, target)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker cp upload %s to %s failed: %w (%s)", localPath, target, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (c *DockerConnection) Download(remotePath, localPath string) error {
	c.mu.Lock()
	container := c.container
	c.mu.Unlock()

	src := fmt.Sprintf("%s:%s", container, remotePath)
	cmd := exec.Command("docker", "cp", src, localPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker cp download %s to %s failed: %w (%s)", src, localPath, err, strings.TrimSpace(string(out)))
	}
	return nil
}
