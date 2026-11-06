package module

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/ssh"
)

// rsyncCmdRunner 允许单元测试注入模拟的 rsync 执行器
var rsyncCmdRunner = func(cmd *exec.Cmd) ([]byte, []byte, int, error) {
	var stdoutBuf, stderrBuf bytes.Buffer
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
	return stdoutBuf.Bytes(), stderrBuf.Bytes(), exitCode, err
}

// SetRsyncCmdRunnerForTest 供单元测试注入模拟的 rsync 执行器
func SetRsyncCmdRunnerForTest(fn func(cmd *exec.Cmd) ([]byte, []byte, int, error)) func() {
	orig := rsyncCmdRunner
	rsyncCmdRunner = fn
	return func() {
		rsyncCmdRunner = orig
	}
}

// SynchronizeModule 基于 rsync 协议实现本地与远端高效目录/文件同步
type SynchronizeModule struct{}

func (m *SynchronizeModule) Name() string { return "synchronize" }

func (m *SynchronizeModule) Validate(params map[string]interface{}) error {
	src := GetParamString(params, "src", "")
	dest := GetParamString(params, "dest", "")
	if src == "" || dest == "" {
		return fmt.Errorf("synchronize module requires src and dest")
	}
	return nil
}

func (m *SynchronizeModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	src := GetParamString(params, "src", "")
	dest := GetParamString(params, "dest", "")
	archive := GetParamBool(params, "archive", true)
	deleteFlag := GetParamBool(params, "delete", false)
	checksum := GetParamBool(params, "checksum", false)
	compress := GetParamBool(params, "compress", true)
	mode := strings.ToLower(GetParamString(params, "mode", "push"))
	rsyncPath := GetParamString(params, "rsync_path", "")
	privateKey := GetParamString(params, "private_key", "")
	destPort := GetParamInt(params, "dest_port", 0)
	existingOnly := GetParamBool(params, "existing_only", false)
	partial := GetParamBool(params, "partial", false)
	checkMode := IsCheckMode(params)
	diffMode := IsDiffMode(params)

	// 1. 探测主机信息
	var host *inventory.Host
	if hp, ok := client.(interface{ Host() *inventory.Host }); ok {
		host = hp.Host()
	}

	isLocal := host == nil || host.Connection == "local" || host.Name == "localhost" || host.Address == "localhost" || host.Address == "127.0.0.1"

	// 2. 构建 rsync 参数列表
	args := []string{"-i"} // --itemize-changes
	if archive {
		args = append(args, "-a")
	}
	if deleteFlag {
		args = append(args, "--delete")
	}
	if checksum {
		args = append(args, "-c")
	}
	if compress {
		args = append(args, "-z")
	}
	if checkMode {
		args = append(args, "--dry-run")
	}
	if existingOnly {
		args = append(args, "--existing")
	}
	if partial {
		args = append(args, "--partial")
	}
	if rsyncPath != "" {
		args = append(args, fmt.Sprintf("--rsync-path=%s", rsyncPath))
	}

	// 细粒度参数控制
	if _, ok := params["recursive"]; ok {
		if GetParamBool(params, "recursive", true) {
			args = append(args, "-r")
		} else {
			args = append(args, "--no-recursive")
		}
	}
	if _, ok := params["links"]; ok {
		if GetParamBool(params, "links", true) {
			args = append(args, "-l")
		} else {
			args = append(args, "--no-links")
		}
	}
	if _, ok := params["perms"]; ok {
		if GetParamBool(params, "perms", true) {
			args = append(args, "-p")
		} else {
			args = append(args, "--no-perms")
		}
	}
	if _, ok := params["times"]; ok {
		if GetParamBool(params, "times", true) {
			args = append(args, "-t")
		} else {
			args = append(args, "--no-times")
		}
	}
	if _, ok := params["owner"]; ok {
		if GetParamBool(params, "owner", true) {
			args = append(args, "-o")
		} else {
			args = append(args, "--no-owner")
		}
	}
	if _, ok := params["group"]; ok {
		if GetParamBool(params, "group", true) {
			args = append(args, "-g")
		} else {
			args = append(args, "--no-group")
		}
	}

	// rsync_opts
	if rsyncOpts, ok := params["rsync_opts"]; ok {
		switch opts := rsyncOpts.(type) {
		case []string:
			args = append(args, opts...)
		case []interface{}:
			for _, o := range opts {
				args = append(args, fmt.Sprintf("%v", o))
			}
		case string:
			args = append(args, strings.Fields(opts)...)
		}
	}

	// 3. 处理远程目标与 SSH 参数
	var srcArg, destArg string
	if isLocal {
		srcArg = src
		destArg = dest
	} else {
		remoteUser := host.User
		remoteHost := host.Address
		if remoteHost == "" {
			remoteHost = host.Name
		}
		port := host.Port
		if destPort > 0 {
			port = destPort
		}
		key := host.PrivateKey
		if privateKey != "" {
			key = privateKey
		}

		sshParts := []string{"ssh"}
		if port > 0 && port != 22 {
			sshParts = append(sshParts, fmt.Sprintf("-p %d", port))
		}
		if key != "" {
			sshParts = append(sshParts, fmt.Sprintf("-i %s", key))
		}
		if host.SSHCommonArgs != "" {
			sshParts = append(sshParts, host.SSHCommonArgs)
		}
		if host.Variables != nil {
			if extraArgs, ok := host.Variables["ansible_ssh_extra_args"]; ok && extraArgs != "" {
				sshParts = append(sshParts, extraArgs)
			}
			if sshArgs, ok := host.Variables["ansible_ssh_args"]; ok && sshArgs != "" {
				sshParts = append(sshParts, sshArgs)
			}
		}
		args = append(args, "-e", strings.Join(sshParts, " "))

		remoteTarget := remoteHost
		if remoteUser != "" {
			remoteTarget = fmt.Sprintf("%s@%s", remoteUser, remoteHost)
		}

		if mode == "pull" {
			srcArg = fmt.Sprintf("%s:%s", remoteTarget, src)
			destArg = dest
		} else { // push
			srcArg = src
			destArg = fmt.Sprintf("%s:%s", remoteTarget, dest)
		}
	}

	args = append(args, srcArg, destArg)

	// 4. 执行 rsync 命令
	cmd := exec.Command("rsync", args...)
	stdoutBytes, stderrBytes, exitCode, err := rsyncCmdRunner(cmd)
	stdout := string(stdoutBytes)
	stderr := string(stderrBytes)

	if err != nil && exitCode != 0 {
		return &Result{
			Failed:   true,
			ExitCode: exitCode,
			Stdout:   stdout,
			Stderr:   stderr,
			Message:  fmt.Sprintf("rsync failed with exit code %d: %s", exitCode, strings.TrimSpace(stderr)),
		}, fmt.Errorf("rsync failed: %s", strings.TrimSpace(stderr))
	}

	changed := rsyncChanged(stdout)
	diff := ""
	if diffMode && changed {
		diff = strings.TrimSpace(stdout)
	}

	return &Result{
		Changed:  changed,
		ExitCode: 0,
		Stdout:   stdout,
		Stderr:   stderr,
		Diff:     diff,
	}, nil
}

func rsyncChanged(stdout string) bool {
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "building file list") ||
			strings.HasPrefix(line, "sent ") ||
			strings.HasPrefix(line, "total size is") ||
			strings.HasPrefix(line, "receiving file list") ||
			strings.Contains(line, "bytes/sec") {
			continue
		}
		return true
	}
	return false
}
