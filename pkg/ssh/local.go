package ssh

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go-ansible/pkg/inventory"
)

// LocalConnection 本地执行连接（无需 SSH 开销直接执行本地命令）
type LocalConnection struct {
	host *inventory.Host
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

func (c *LocalConnection) Connect() error {
	return nil
}

func (c *LocalConnection) Close() error {
	return nil
}

func (c *LocalConnection) Execute(cmd string) (*Result, error) {
	if c.host != nil && c.host.Become {
		return c.ExecuteWithBecome(cmd)
	}
	return c.execute(cmd)
}

func (c *LocalConnection) ExecuteWithBecome(cmd string) (*Result, error) {
	becomeUser := c.host.BecomeUser
	if becomeUser == "" {
		becomeUser = "root"
	}
	becomeMethod := c.host.BecomeMethod
	if becomeMethod == "" {
		becomeMethod = "sudo"
	}

	var becomeCmd string
	if becomeMethod == "sudo" {
		if c.host.BecomePass != "" {
			becomeCmd = fmt.Sprintf("echo '%s' | sudo -S -u %s", c.host.BecomePass, becomeUser)
		} else {
			becomeCmd = fmt.Sprintf("sudo -n -u %s", becomeUser)
		}
	} else if becomeMethod == "su" {
		if c.host.BecomePass != "" {
			becomeCmd = fmt.Sprintf("echo '%s' | su - %s -c", c.host.BecomePass, becomeUser)
			return c.execute(fmt.Sprintf("%s '%s'", becomeCmd, cmd))
		}
		becomeCmd = fmt.Sprintf("su - %s -c", becomeUser)
		return c.execute(fmt.Sprintf("%s '%s'", becomeCmd, cmd))
	} else {
		becomeExe := c.host.BecomeExe
		if becomeExe == "" {
			becomeExe = "sudo"
		}
		becomeCmd = becomeExe
	}

	fullCmd := fmt.Sprintf("%s %s", becomeCmd, cmd)
	return c.execute(fullCmd)
}

func (c *LocalConnection) execute(cmdStr string) (*Result, error) {
	cmd := exec.Command("/bin/sh", "-c", cmdStr)
	cmd.Env = append(os.Environ(), "LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8")

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
		return result, fmt.Errorf("command execution failed with exit code %d: %s", exitCode, stderr)
	}

	return result, nil
}

func (c *LocalConnection) Upload(localPath, remotePath string) error {
	localPath = filepath.Clean(localPath)
	remotePath = filepath.Clean(remotePath)
	if localPath == remotePath {
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

	parentDir := filepath.Dir(remotePath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("create parent dir %s: %w", parentDir, err)
	}

	dstFile, err := os.OpenFile(remotePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcStat.Mode().Perm())
	if err != nil {
		return fmt.Errorf("open dst file %s: %w", remotePath, err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("copy content from %s to %s: %w", localPath, remotePath, err)
	}
	return nil
}

func (c *LocalConnection) Download(remotePath, localPath string) error {
	return c.Upload(remotePath, localPath)
}
