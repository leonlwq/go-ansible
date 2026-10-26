package module

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go-ansible/pkg/ssh"
)

// CopyModule copy 模块
type CopyModule struct{}

func (m *CopyModule) Name() string { return "copy" }

func (m *CopyModule) Validate(params map[string]interface{}) error {
	_, hasContent := params["content"]
	_, hasSrc := params["src"]
	_, hasDest := params["dest"]

	if !hasContent && !hasSrc {
		return fmt.Errorf("copy module requires content or src")
	}
	if !hasDest {
		return fmt.Errorf("copy module requires dest")
	}
	return nil
}

func (m *CopyModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	dest := GetParamString(params, "dest", "")
	mode := GetParamMode(params, "mode", "")
	owner := GetParamString(params, "owner", "")
	group := GetParamString(params, "group", "")
	backup := GetParamBool(params, "backup", false)
	become := GetParamBool(params, "become", false)
	force := GetParamBool(params, "force", true)
	remoteSrc := GetParamBool(params, "remote_src", false)
	validate := GetParamString(params, "validate", "")
	checkMode := GetParamBool(params, "__check_mode", false)
	diffMode := GetParamBool(params, "__diff", false)

	fmt.Printf("[COPY] become=%v, remote_src=%v, params=%+v\n", become, remoteSrc, params)

	src := GetParamString(params, "src", "")

	// 1. 远端复制模式 (remote_src: true)
	if remoteSrc {
		if src == "" {
			return nil, fmt.Errorf("copy module requires src when remote_src is true")
		}
		destPath := resolveDestPath(dest, src)

		// 检查远程源文件是否存在
		checkSrcRes, _ := client.Execute(fmt.Sprintf("test -e %s", src))
		if checkSrcRes != nil && checkSrcRes.ExitCode != 0 {
			return nil, fmt.Errorf("remote source file not found: %s", src)
		}

		srcChecksum := readRemoteChecksum(client, src)
		destChecksum := readRemoteChecksum(client, destPath)

		if srcChecksum != "" && srcChecksum == destChecksum {
			return &Result{
				Changed: false,
				Message: "file already exists with matching content",
			}, nil
		}
		if destChecksum != "" && !force {
			return &Result{
				Changed: false,
				Message: "file exists with different content and force=false",
			}, nil
		}

		diff := ""
		if diffMode {
			diff = buildTextDiff(destPath, readRemoteFileContent(client, destPath), readRemoteFileContent(client, src))
		}

		if checkMode {
			return &Result{
				Changed: true,
				Message: "check mode: file would be copied remotely",
				Diff:    diff,
			}, nil
		}

		if backup && destChecksum != "" {
			client.Execute(buildBackupCommand(destPath))
		}

		destDir := filepath.Dir(destPath)
		client.Execute(fmt.Sprintf("mkdir -p %s", destDir))

		if validate != "" {
			tmpPath := fmt.Sprintf("/tmp/go-ansible-cptmp-%d-%s", os.Getpid(), filepath.Base(destPath))
			if _, err := client.Execute(fmt.Sprintf("cp -p %s %s", src, tmpPath)); err != nil {
				return nil, fmt.Errorf("copy to tmp for validation: %w", err)
			}
			if err := executeValidate(client, validate, tmpPath); err != nil {
				client.Execute(fmt.Sprintf("rm -f %s", tmpPath))
				return nil, err
			}
			if _, err := client.Execute(fmt.Sprintf("mv -f %s %s", tmpPath, destPath)); err != nil {
				return nil, fmt.Errorf("move validated file: %w", err)
			}
		} else {
			cpRes, err := client.Execute(fmt.Sprintf("cp -p %s %s", src, destPath))
			if err != nil {
				return nil, fmt.Errorf("remote copy: %w", err)
			}
			if cpRes != nil && cpRes.ExitCode != 0 {
				return nil, fmt.Errorf("remote copy failed: %s", strings.TrimSpace(cpRes.Stderr))
			}
		}

		m.setPermissions(client, destPath, mode, owner, group)
		return &Result{
			Changed: true,
			Message: "file copied remotely successfully",
			Diff:    diff,
		}, nil
	}

	// 2. 内容直接写入模式 (content)
	if src == "" {
		if content, ok := params["content"]; ok {
			contentStr := fmt.Sprintf("%v", content)
			remoteChecksum := readRemoteChecksum(client, dest)
			localChecksum := stringChecksum(contentStr)
			if localChecksum == remoteChecksum && remoteChecksum != "" {
				return &Result{
					Changed: false,
					Message: "file already exists with matching content",
				}, nil
			}
			if remoteChecksum != "" && !force {
				return &Result{
					Changed: false,
					Message: "file exists with different content and force=false",
				}, nil
			}
			diff := ""
			if diffMode {
				diff = buildTextDiff(dest, readRemoteFileContent(client, dest), contentStr)
			}
			if checkMode {
				return &Result{
					Changed: true,
					Message: "check mode: file content would be updated",
					Diff:    diff,
				}, nil
			}
			if backup && remoteChecksum != "" {
				client.Execute(buildBackupCommand(dest))
			}

			destDir := filepath.Dir(dest)
			client.Execute(fmt.Sprintf("mkdir -p %s", destDir))

			if validate != "" {
				tmpPath := fmt.Sprintf("/tmp/go-ansible-cnt-%d-%s", os.Getpid(), filepath.Base(dest))
				client.Execute(buildContentWriteCommand(tmpPath, contentStr))
				if err := executeValidate(client, validate, tmpPath); err != nil {
					client.Execute(fmt.Sprintf("rm -f %s", tmpPath))
					return nil, err
				}
				client.Execute(fmt.Sprintf("mv -f %s %s", tmpPath, dest))
			} else {
				cmd := buildContentWriteCommand(dest, contentStr)
				result, err := client.Execute(cmd)
				if err != nil {
					return result, err
				}
			}
			result := &Result{
				Changed: true,
				Message: "file content written successfully",
				Diff:    diff,
			}
			m.setPermissions(client, dest, mode, owner, group)
			return result, nil
		}
		return nil, fmt.Errorf("copy module requires src or content")
	}

	// 3. 本地文件上传模式 (src -> remote dest)
	fmt.Printf("[COPY] src=%s, dest=%s\n", src, dest)

	// 检查本地文件是否存在
	fileInfo, err := os.Stat(src)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("source file not found: %s", src)
	}
	if err != nil {
		return nil, fmt.Errorf("stat source file: %w", err)
	}
	fmt.Printf("[COPY] Source file size: %d bytes\n", fileInfo.Size())

	// 计算本地文件 checksum
	localChecksum, err := fileChecksum(src)
	if err != nil {
		return nil, fmt.Errorf("calculate local checksum: %w", err)
	}
	fmt.Printf("[COPY] Local checksum: %s\n", localChecksum)

	// 构建目标路径
	destPath := resolveDestPath(dest, src)
	fmt.Printf("[COPY] Destination path: %s\n", destPath)

	// 获取远程文件 checksum
	remoteChecksum := readRemoteChecksum(client, destPath)
	fmt.Printf("[COPY] Remote checksum: %s\n", remoteChecksum)

	// 如果 checksum 相同，跳过
	if localChecksum == remoteChecksum && remoteChecksum != "" {
		fmt.Println("[COPY] Files match, skipping")
		result := &Result{
			Changed: false,
			Message: "file already exists with matching content",
		}
		return result, nil
	}
	if remoteChecksum != "" && !force {
		return &Result{
			Changed: false,
			Message: "file exists with different content and force=false",
		}, nil
	}
	diff := ""
	if diffMode {
		data, err := os.ReadFile(src)
		if err == nil {
			diff = buildTextDiff(destPath, readRemoteFileContent(client, destPath), string(data))
		}
	}
	if checkMode {
		return &Result{
			Changed: true,
			Message: "check mode: file would be copied",
			Diff:    diff,
		}, nil
	}
	if backup && remoteChecksum != "" {
		client.Execute(buildBackupCommand(destPath))
	}

	// 确保目标目录存在
	destDir := filepath.Dir(destPath)
	fmt.Printf("[COPY] Creating directory: %s\n", destDir)
	client.Execute(fmt.Sprintf("mkdir -p %s", destDir))

	if become || validate != "" {
		fmt.Println("[COPY] Using staging temp path for become/validate")
		tmpPath := fmt.Sprintf("/tmp/go-ansible-%d-%s", os.Getpid(), filepath.Base(src))
		fmt.Printf("[COPY] Uploading to temp path: %s\n", tmpPath)

		if err := client.Upload(src, tmpPath); err != nil {
			fmt.Printf("[COPY] Upload error: %v\n", err)
			return nil, fmt.Errorf("upload file to tmp: %w", err)
		}

		if validate != "" {
			if err := executeValidate(client, validate, tmpPath); err != nil {
				client.Execute(fmt.Sprintf("rm -f %s", tmpPath))
				return nil, err
			}
		}

		// 移动到目标位置
		mvCmd := fmt.Sprintf("mv -f %s %s", tmpPath, destPath)
		fmt.Printf("[COPY] Moving file: %s\n", mvCmd)
		result, err := client.Execute(mvCmd)
		if err != nil {
			client.Execute(fmt.Sprintf("rm -f %s", tmpPath))
			fmt.Printf("[COPY] Move error: %v\n", err)
			return nil, fmt.Errorf("move file: %w", err)
		}
		if result != nil && result.ExitCode != 0 {
			client.Execute(fmt.Sprintf("rm -f %s", tmpPath))
			return nil, fmt.Errorf("move file failed: %s", strings.TrimSpace(result.Stderr))
		}

		result.Changed = true
		result.Message = "file copied successfully"
		result.Diff = diff
		m.setPermissions(client, destPath, mode, owner, group)
		return result, nil
	}

	// 普通模式：直接上传
	fmt.Println("[COPY] Using normal mode, uploading directly")
	if err := client.Upload(src, destPath); err != nil {
		fmt.Printf("[COPY] Upload error: %v\n", err)
		return nil, fmt.Errorf("upload file: %w", err)
	}
	fmt.Println("[COPY] Upload successful")

	result := &Result{
		Changed: true,
		Message: "file copied successfully",
		Diff:    diff,
	}

	m.setPermissions(client, destPath, mode, owner, group)
	return result, nil
}

func readRemoteChecksum(client ssh.Connection, path string) string {
	checkResult, _ := client.Execute(checksumCommand(path))
	if checkResult == nil {
		return ""
	}
	return strings.TrimSpace(checkResult.Stdout)
}

func (m *CopyModule) setPermissions(client ssh.Connection, path, mode, owner, group string) {
	if mode != "" {
		client.Execute(fmt.Sprintf("chmod %s %s", mode, path))
	}
	if owner != "" {
		chown := owner
		if group != "" {
			chown = fmt.Sprintf("%s:%s", owner, group)
		}
		client.Execute(fmt.Sprintf("chown %s %s", chown, path))
	}
}

func fileChecksum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// FetchModule fetch 模块
type FetchModule struct{}

func (m *FetchModule) Name() string { return "fetch" }

func (m *FetchModule) Validate(params map[string]interface{}) error {
	if _, ok := params["src"]; !ok {
		return fmt.Errorf("fetch module requires src")
	}
	if _, ok := params["dest"]; !ok {
		return fmt.Errorf("fetch module requires dest")
	}
	return nil
}

func (m *FetchModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	src := GetParamString(params, "src", "")
	dest := GetParamString(params, "dest", "")

	if err := client.Download(src, dest); err != nil {
		return nil, fmt.Errorf("download file: %w", err)
	}

	return &Result{
		Changed: true,
		Message: fmt.Sprintf("fetched %s to %s", src, dest),
	}, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
