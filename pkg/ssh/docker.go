package ssh

import (
	"fmt"
	"os/exec"
	"strings"

	"go-ansible/pkg/inventory"
)

// DockerConnection 基于 docker exec 和 docker cp 的容器连接插件
type DockerConnection struct {
	host      *inventory.Host
	container string
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
	if c.host.Become {
		return c.ExecuteWithBecome(cmd)
	}
	return c.execute(cmd, c.host.User)
}

func (c *DockerConnection) ExecuteWithBecome(cmd string) (*Result, error) {
	becomeUser := c.host.BecomeUser
	if becomeUser == "" {
		becomeUser = "root"
	}
	return c.execute(cmd, becomeUser)
}

func (c *DockerConnection) execute(cmdStr, user string) (*Result, error) {
	args := []string{"exec", "-i"}
	if user != "" {
		args = append(args, "-u", user)
	}
	args = append(args, c.container, "/bin/sh", "-c", cmdStr)

	cmd := exec.Command("docker", args...)
	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
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
		Host:     c.host.Name,
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
	target := fmt.Sprintf("%s:%s", c.container, remotePath)
	cmd := exec.Command("docker", "cp", localPath, target)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker cp upload %s to %s failed: %w (%s)", localPath, target, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (c *DockerConnection) Download(remotePath, localPath string) error {
	src := fmt.Sprintf("%s:%s", c.container, remotePath)
	cmd := exec.Command("docker", "cp", src, localPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker cp download %s to %s failed: %w (%s)", src, localPath, err, strings.TrimSpace(string(out)))
	}
	return nil
}
