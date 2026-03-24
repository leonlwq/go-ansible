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

func (m *CopyModule) Execute(client *ssh.Client, params map[string]interface{}) (*Result, error) {
	dest := GetParamString(params, "dest", "")
	mode := GetParamString(params, "mode", "")
	owner := GetParamString(params, "owner", "")
	group := GetParamString(params, "group", "")
	backup := GetParamBool(params, "backup", false)
	become := GetParamBool(params, "become", false)

	fmt.Printf("[COPY] become=%v, params=%+v\n", become, params)

	// 文件模式
	src := GetParamString(params, "src", "")
	if src == "" {
		// 检查内容模式
		if content, ok := params["content"]; ok {
			contentStr := fmt.Sprintf("%v", content)
			cmd := fmt.Sprintf("cat > %s << 'GOANSIBLE_EOF'\n%s\nGOANSIBLE_EOF", dest, contentStr)
			result, err := client.Execute(cmd)
			if err != nil {
				return result, err
			}
			result.Changed = true
			m.setPermissions(client, dest, mode, owner, group)
			return result, nil
		}
		return nil, fmt.Errorf("copy module requires src or content")
	}

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

	// 备份
	if backup {
		client.Execute(fmt.Sprintf("cp -p %s %s.bak 2>/dev/null || true", dest, dest))
	}

	// 计算本地文件 checksum
	localChecksum, err := fileChecksum(src)
	if err != nil {
		return nil, fmt.Errorf("calculate local checksum: %w", err)
	}
	fmt.Printf("[COPY] Local checksum: %s\n", localChecksum)

	// 构建目标路径
	var destPath string
	// 检查 dest 是否是目录（以 / 结尾或是已存在的目录）
	if dest[len(dest)-1] == '/' {
		// 如果 dest 是目录，使用源文件名
		destPath = dest + filepath.Base(src)
	} else {
		destPath = dest
	}
	fmt.Printf("[COPY] Destination path: %s\n", destPath)

	// 获取远程文件 checksum
	checkResult, _ := client.Execute(fmt.Sprintf("sha256sum %s 2>/dev/null | cut -d' ' -f1 || shasum -a 256 %s 2>/dev/null | cut -d' ' -f1", destPath, destPath))
	remoteChecksum := ""
	if checkResult != nil {
		remoteChecksum = strings.TrimSpace(checkResult.Stdout)
	}
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

	// 确保目标目录存在
	destDir := filepath.Dir(destPath)
	fmt.Printf("[COPY] Creating directory: %s\n", destDir)
	client.Execute(fmt.Sprintf("mkdir -p %s", destDir))

	if become {
		fmt.Println("[COPY] Using become mode")
		// become 模式：先上传到临时目录，再 sudo mv
		tmpPath := fmt.Sprintf("/tmp/go-ansible-%d-%s", os.Getpid(), filepath.Base(src))
		fmt.Printf("[COPY] Uploading to temp path: %s\n", tmpPath)

		// 上传到临时目录
		if err := client.Upload(src, tmpPath); err != nil {
			fmt.Printf("[COPY] Upload error: %v\n", err)
			return nil, fmt.Errorf("upload file to tmp: %w", err)
		}
		fmt.Println("[COPY] Upload successful")

		// 移动到目标位置
		mvCmd := fmt.Sprintf("mv %s %s", tmpPath, destPath)
		fmt.Printf("[COPY] Moving file: %s\n", mvCmd)
		result, err := client.Execute(mvCmd)
		if err != nil {
			// 清理临时文件
			client.Execute(fmt.Sprintf("rm -f %s", tmpPath))
			fmt.Printf("[COPY] Move error: %v\n", err)
			return nil, fmt.Errorf("move file: %w", err)
		}
		fmt.Println("[COPY] Move successful")

		result.Changed = true
		result.Message = "file copied successfully"
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
	}

	m.setPermissions(client, destPath, mode, owner, group)
	return result, nil
}

func (m *CopyModule) setPermissions(client *ssh.Client, path, mode, owner, group string) {
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

func (m *FetchModule) Execute(client *ssh.Client, params map[string]interface{}) (*Result, error) {
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
