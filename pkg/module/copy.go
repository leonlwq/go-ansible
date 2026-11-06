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
	checkMode := IsCheckMode(params)
	diffMode := IsDiffMode(params)

	src := GetParamString(params, "src", "")

	// 1. 远端复制模式 (remote_src: true)
	if remoteSrc {
		if src == "" {
			return nil, fmt.Errorf("copy module requires src when remote_src is true")
		}
		destPath := resolveDestPath(dest, src)

		// 检查远程源文件是否存在
		checkSrcRes, _ := client.Execute(fmt.Sprintf("test -e %s", shellQuote(src)))
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
		client.Execute(fmt.Sprintf("mkdir -p %s", shellQuote(destDir)))

		if validate != "" {
			tmpPath := fmt.Sprintf("/tmp/go-ansible-cptmp-%d-%s", os.Getpid(), filepath.Base(destPath))
			if _, err := client.Execute(fmt.Sprintf("cp -p %s %s", shellQuote(src), shellQuote(tmpPath))); err != nil {
				return nil, fmt.Errorf("copy to tmp for validation: %w", err)
			}
			if err := executeValidate(client, validate, tmpPath); err != nil {
				client.Execute(fmt.Sprintf("rm -f %s", shellQuote(tmpPath)))
				return nil, err
			}
			if _, err := client.Execute(fmt.Sprintf("mv -f %s %s", shellQuote(tmpPath), shellQuote(destPath))); err != nil {
				return nil, fmt.Errorf("move validated file: %w", err)
			}
		} else {
			cpRes, err := client.Execute(fmt.Sprintf("cp -p %s %s", shellQuote(src), shellQuote(destPath)))
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
				permChanged, err := SetFilePermissions(client, dest, mode, owner, group, false)
				if err != nil {
					return nil, fmt.Errorf("set file permissions: %w", err)
				}
				result := &Result{
					Changed: permChanged,
					Message: "file already exists with matching content",
				}
				if permChanged {
					result.Message = "file permissions updated"
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
			client.Execute(fmt.Sprintf("mkdir -p %s", shellQuote(destDir)))
			if dirMode := GetParamString(params, "directory_mode", ""); dirMode != "" {
				client.Execute(fmt.Sprintf("chmod %s %s", shellQuote(dirMode), shellQuote(destDir)))
			}

			if validate != "" {
				tmpPath := fmt.Sprintf("/tmp/go-ansible-cnt-%d-%s", os.Getpid(), filepath.Base(dest))
				if _, err := writeRemoteEncodedContent(client, tmpPath, contentStr, "utf-8"); err != nil {
					return nil, err
				}
				if err := executeValidate(client, validate, tmpPath); err != nil {
					client.Execute(fmt.Sprintf("rm -f %s", shellQuote(tmpPath)))
					return nil, err
				}
				client.Execute(fmt.Sprintf("mv -f %s %s", shellQuote(tmpPath), shellQuote(dest)))
			} else {
				if _, err := writeRemoteEncodedContent(client, dest, contentStr, "utf-8"); err != nil {
					return nil, err
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
	// 检查本地文件是否存在
	fileInfo, err := os.Stat(src)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("source file not found: %s", src)
	}
	if err != nil {
		return nil, fmt.Errorf("stat source file: %w", err)
	}

	if fileInfo.IsDir() {
		return m.executeCopyDirectory(client, src, dest, mode, owner, group, force, checkMode, diffMode)
	}

	// 计算本地文件 checksum
	localChecksum, err := fileChecksum(src)
	if err != nil {
		return nil, fmt.Errorf("calculate local checksum: %w", err)
	}

	// 构建目标路径
	destPath := resolveDestPath(dest, src)

	// 获取远程文件 checksum
	remoteChecksum := readRemoteChecksum(client, destPath)

	// 如果 checksum 相同，跳过传输，但仍需检查并应用权限和属组
	if localChecksum == remoteChecksum && remoteChecksum != "" {
		permChanged, err := SetFilePermissions(client, destPath, mode, owner, group, false)
		if err != nil {
			return nil, fmt.Errorf("set file permissions: %w", err)
		}
		result := &Result{
			Changed: permChanged,
			Message: "file already exists with matching content",
		}
		if permChanged {
			result.Message = "file permissions updated"
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
	client.Execute(fmt.Sprintf("mkdir -p %s", shellQuote(destDir)))
	if dirMode := GetParamString(params, "directory_mode", ""); dirMode != "" {
		client.Execute(fmt.Sprintf("chmod %s %s", shellQuote(dirMode), shellQuote(destDir)))
	}

	if become || validate != "" {
		tmpPath := fmt.Sprintf("/tmp/go-ansible-%d-%s", os.Getpid(), filepath.Base(src))

		if err := client.Upload(src, tmpPath); err != nil {
			return nil, fmt.Errorf("upload file to tmp: %w", err)
		}

		if validate != "" {
			if err := executeValidate(client, validate, tmpPath); err != nil {
				client.Execute(fmt.Sprintf("rm -f %s", shellQuote(tmpPath)))
				return nil, err
			}
		}

		// 移动到目标位置
		mvCmd := fmt.Sprintf("mv -f %s %s", shellQuote(tmpPath), shellQuote(destPath))
		result, err := client.Execute(mvCmd)
		if err != nil {
			client.Execute(fmt.Sprintf("rm -f %s", shellQuote(tmpPath)))
			return nil, fmt.Errorf("move file: %w", err)
		}
		if result != nil && result.ExitCode != 0 {
			client.Execute(fmt.Sprintf("rm -f %s", shellQuote(tmpPath)))
			return nil, fmt.Errorf("move file failed: %s", strings.TrimSpace(result.Stderr))
		}

		result.Changed = true
		result.Message = "file copied successfully"
		result.Diff = diff
		m.setPermissions(client, destPath, mode, owner, group)
		return result, nil
	}

	// 普通模式：直接上传
	if err := client.Upload(src, destPath); err != nil {
		return nil, fmt.Errorf("upload file: %w", err)
	}

	result := &Result{
		Changed: true,
		Message: "file copied successfully",
		Diff:    diff,
	}

	m.setPermissions(client, destPath, mode, owner, group)
	return result, nil
}

func (m *CopyModule) executeCopyDirectory(client ssh.Connection, src, dest, mode, owner, group string, force, checkMode, diffMode bool) (*Result, error) {
	hasTrailingSlash := strings.HasSuffix(src, "/") || strings.HasSuffix(src, string(filepath.Separator))
	srcClean := filepath.Clean(src)
	baseName := filepath.Base(srcClean)

	changed := false
	var copiedFiles []string

	err := filepath.Walk(srcClean, func(localPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcClean, localPath)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		var remotePath string
		if hasTrailingSlash {
			remotePath = filepath.Join(dest, rel)
		} else {
			remotePath = filepath.Join(dest, baseName, rel)
		}

		if info.IsDir() {
			checkDir, _ := client.Execute(fmt.Sprintf("test -d %s", shellQuote(remotePath)))
			if checkDir == nil || checkDir.ExitCode != 0 {
				changed = true
				if !checkMode {
					if _, err := client.Execute(fmt.Sprintf("mkdir -p %s", shellQuote(remotePath))); err != nil {
						return fmt.Errorf("create remote directory %s: %w", remotePath, err)
					}
					m.setPermissions(client, remotePath, mode, owner, group)
				}
			}
			return nil
		}

		localChecksum, err := fileChecksum(localPath)
		if err != nil {
			return fmt.Errorf("calculate local checksum for %s: %w", localPath, err)
		}

		remoteChecksum := readRemoteChecksum(client, remotePath)
		if localChecksum == remoteChecksum && remoteChecksum != "" {
			return nil
		}

		if remoteChecksum != "" && !force {
			return nil
		}

		changed = true
		copiedFiles = append(copiedFiles, remotePath)

		if !checkMode {
			remoteParent := filepath.Dir(remotePath)
			_, _ = client.Execute(fmt.Sprintf("mkdir -p %s", shellQuote(remoteParent)))

			if err := client.Upload(localPath, remotePath); err != nil {
				return fmt.Errorf("upload %s to %s: %w", localPath, remotePath, err)
			}
			m.setPermissions(client, remotePath, mode, owner, group)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	if checkMode {
		return &Result{
			Changed: changed,
			Message: fmt.Sprintf("check mode: %d file(s) would be copied", len(copiedFiles)),
			Data: map[string]interface{}{
				"dest":         dest,
				"copied_files": copiedFiles,
			},
		}, nil
	}

	return &Result{
		Changed: changed,
		Message: fmt.Sprintf("copied %d file(s)", len(copiedFiles)),
		Data: map[string]interface{}{
			"dest":         dest,
			"copied_files": copiedFiles,
		},
	}, nil
}

func readRemoteChecksum(client ssh.Connection, path string) string {
	checkResult, _ := client.Execute(checksumCommand(path))
	if checkResult == nil {
		return ""
	}
	return strings.TrimSpace(checkResult.Stdout)
}

func (m *CopyModule) setPermissions(client ssh.Connection, path, mode, owner, group string) {
	_, _ = SetFilePermissions(client, path, mode, owner, group, false)
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
	flat := GetParamBool(params, "flat", false)
	failOnMissing := GetParamBool(params, "fail_on_missing", true)
	checkMode := IsCheckMode(params)
	hostname := GetParamString(params, "inventory_hostname", "")
	if hostname == "" {
		hostname = "localhost"
	}

	// 1. 检查远端文件是否存在
	checkRes, _ := client.Execute(fmt.Sprintf("test -f %s", shellQuote(src)))
	if checkRes == nil || checkRes.ExitCode != 0 {
		if failOnMissing {
			return &Result{
				Changed:  false,
				Failed:   true,
				Message:  fmt.Sprintf("remote file not found: %s", src),
				ExitCode: 1,
			}, nil
		}
		return &Result{
			Changed: false,
			Message: fmt.Sprintf("remote file not found, ignored: %s", src),
		}, nil
	}

	// 2. 计算本地目标路径
	var localPath string
	if flat {
		if strings.HasSuffix(dest, "/") || strings.HasSuffix(dest, string(filepath.Separator)) {
			localPath = filepath.Join(dest, filepath.Base(src))
		} else {
			if fi, err := os.Stat(dest); err == nil && fi.IsDir() {
				localPath = filepath.Join(dest, filepath.Base(src))
			} else {
				localPath = dest
			}
		}
	} else {
		cleanSrc := strings.TrimPrefix(filepath.Clean(src), string(filepath.Separator))
		localPath = filepath.Join(dest, hostname, cleanSrc)
	}

	// 3. 比对校验和以保证幂等
	remoteChecksum := readRemoteChecksum(client, src)
	if fi, err := os.Stat(localPath); err == nil && !fi.IsDir() {
		localChecksum, _ := fileChecksum(localPath)
		if remoteChecksum != "" && localChecksum != "" && remoteChecksum == localChecksum {
			return &Result{
				Changed: false,
				Message: fmt.Sprintf("file %s is already up to date at %s", src, localPath),
				Data: map[string]interface{}{
					"dest":     localPath,
					"file":     localPath,
					"checksum": localChecksum,
				},
			}, nil
		}
	}

	if checkMode {
		return &Result{
			Changed: true,
			Message: fmt.Sprintf("check mode: %s would be fetched to %s", src, localPath),
			Data: map[string]interface{}{
				"dest": localPath,
				"file": localPath,
			},
		}, nil
	}

	// 4. 确保本地父目录存在
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return nil, fmt.Errorf("create local directory: %w", err)
	}

	if err := client.Download(src, localPath); err != nil {
		return nil, fmt.Errorf("download file: %w", err)
	}

	newChecksum, _ := fileChecksum(localPath)
	return &Result{
		Changed: true,
		Message: fmt.Sprintf("fetched %s to %s", src, localPath),
		Data: map[string]interface{}{
			"dest":     localPath,
			"file":     localPath,
			"checksum": newChecksum,
		},
	}, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
