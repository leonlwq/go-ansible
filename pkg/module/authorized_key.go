package module

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"go-ansible/pkg/ssh"
	"go-ansible/pkg/util"
)

// AuthorizedKeyModule 实现了 ansible.posix.authorized_key 核心功能
type AuthorizedKeyModule struct{}

func (m *AuthorizedKeyModule) Name() string {
	return "authorized_key"
}

func (m *AuthorizedKeyModule) Validate(params map[string]interface{}) error {
	user := GetParamString(params, "user", "")
	if strings.TrimSpace(user) == "" {
		return fmt.Errorf("missing required parameter: user")
	}
	v, ok := params["key"]
	if !ok || v == nil {
		return fmt.Errorf("missing required parameter: key")
	}
	switch k := v.(type) {
	case string:
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("missing required parameter: key")
		}
	case []string:
		if len(k) == 0 {
			return fmt.Errorf("missing required parameter: key")
		}
	case []interface{}:
		if len(k) == 0 {
			return fmt.Errorf("missing required parameter: key")
		}
	default:
		if strings.TrimSpace(fmt.Sprintf("%v", v)) == "" {
			return fmt.Errorf("missing required parameter: key")
		}
	}
	return nil
}

type parsedSSHKey struct {
	raw     string
	options string
	keyType string
	blob    string
	comment string
	isKey   bool
}

func parseSSHKeyLine(line string) parsedSSHKey {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return parsedSSHKey{raw: line, isKey: false}
	}

	fields := strings.Fields(trimmed)
	if len(fields) < 2 {
		return parsedSSHKey{raw: line, isKey: false}
	}

	// 1. 无 options 开头: ssh-rsa AAAAB3... [comment]
	if isSSHKeyType(fields[0]) {
		comment := ""
		if len(fields) > 2 {
			comment = strings.Join(fields[2:], " ")
		}
		return parsedSSHKey{
			raw:     trimmed,
			keyType: fields[0],
			blob:    fields[1],
			comment: comment,
			isKey:   true,
		}
	}

	// 2. 带 options 开头: no-port-forwarding,from="..." ssh-rsa AAAAB3... [comment]
	if len(fields) >= 3 && isSSHKeyType(fields[1]) {
		comment := ""
		if len(fields) > 3 {
			comment = strings.Join(fields[3:], " ")
		}
		return parsedSSHKey{
			raw:     trimmed,
			options: fields[0],
			keyType: fields[1],
			blob:    fields[2],
			comment: comment,
			isKey:   true,
		}
	}

	return parsedSSHKey{raw: line, isKey: false}
}

func isSSHKeyType(t string) bool {
	switch t {
	case "ssh-rsa", "ssh-dss", "ssh-ed25519",
		"ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521",
		"sk-ssh-ed25519@openssh.com", "sk-ecdsa-sha2-nistp256@openssh.com":
		return true
	default:
		return strings.HasPrefix(t, "ssh-") || strings.HasPrefix(t, "ecdsa-") || strings.HasPrefix(t, "sk-")
	}
}

func (k parsedSSHKey) Format() string {
	if !k.isKey {
		return k.raw
	}
	var sb strings.Builder
	if k.options != "" {
		sb.WriteString(k.options)
		sb.WriteString(" ")
	}
	sb.WriteString(k.keyType)
	sb.WriteString(" ")
	sb.WriteString(k.blob)
	if k.comment != "" {
		sb.WriteString(" ")
		sb.WriteString(k.comment)
	}
	return sb.String()
}

func (m *AuthorizedKeyModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	user := strings.TrimSpace(GetParamString(params, "user", ""))
	state := strings.ToLower(strings.TrimSpace(GetParamString(params, "state", "present")))
	if state == "" {
		state = "present"
	}
	customPath := strings.TrimSpace(GetParamString(params, "path", ""))
	exclusive := GetParamBool(params, "exclusive", false)
	manageDir := GetParamBool(params, "manage_dir", true)
	keyOptions := strings.TrimSpace(GetParamString(params, "key_options", ""))
	comment := strings.TrimSpace(GetParamString(params, "comment", ""))
	validateCerts := GetParamBool(params, "validate_certs", true)

	checkMode := IsCheckMode(params)
	diffMode := IsDiffMode(params)

	// 1. 如果 key 是 URL，从远程/外部获取内容；支持数组列表参数
	var rawKeyLines []string
	if rawList, ok := params["key"].([]interface{}); ok {
		for _, it := range rawList {
			rawKeyLines = append(rawKeyLines, fmt.Sprintf("%v", it))
		}
	} else if strList, ok := params["key"].([]string); ok {
		rawKeyLines = append(rawKeyLines, strList...)
	} else {
		rawKey := strings.TrimSpace(GetParamString(params, "key", ""))
		if strings.HasPrefix(rawKey, "http://") || strings.HasPrefix(rawKey, "https://") {
			fetched, err := fetchKeyFromURL(rawKey, validateCerts)
			if err != nil {
				return nil, fmt.Errorf("fetch key from %s: %w", rawKey, err)
			}
			rawKey = fetched
		}
		rawKeyLines = strings.Split(rawKey, "\n")
	}

	// 2. 解析待处理的 key 列表
	var targetKeys []parsedSSHKey
	for _, l := range rawKeyLines {
		pk := parseSSHKeyLine(l)
		if pk.isKey {
			if comment != "" {
				pk.comment = comment
			}
			if keyOptions != "" {
				pk.options = keyOptions
			}
			targetKeys = append(targetKeys, pk)
		}
	}
	if len(targetKeys) == 0 {
		return nil, fmt.Errorf("no valid SSH public keys found in key parameter")
	}

	// 3. 确定目标路径与目录
	targetFile := customPath
	if targetFile == "" {
		homeDir := resolveUserHomeDir(client, user)
		targetFile = filepath.Join(homeDir, ".ssh", "authorized_keys")
	}
	targetDir := filepath.Dir(targetFile)

	// 4. 读取现有 authorized_keys 内容
	existingRaw := ""
	readRes, err := client.Execute(fmt.Sprintf("cat %s 2>/dev/null || true", util.Quote(targetFile)))
	if err == nil && readRes != nil {
		existingRaw = readRes.Stdout
	}

	var existingLines []string
	if strings.TrimSpace(existingRaw) != "" {
		existingLines = strings.Split(strings.TrimRight(existingRaw, "\r\n"), "\n")
	}

	var existingKeys []parsedSSHKey
	for _, l := range existingLines {
		existingKeys = append(existingKeys, parseSSHKeyLine(l))
	}

	// 5. 根据 state 处理内容
	var newLines []string
	changed := false

	switch state {
	case "absent":
		for _, ek := range existingKeys {
			if !ek.isKey {
				newLines = append(newLines, ek.raw)
				continue
			}
			// 检查是否匹配待删除的 key
			isMatch := false
			for _, tk := range targetKeys {
				if ek.keyType == tk.keyType && ek.blob == tk.blob {
					isMatch = true
					break
				}
			}
			if isMatch {
				changed = true
			} else {
				newLines = append(newLines, ek.Format())
			}
		}

	case "present":
		if exclusive {
			// exclusive 模式：保留所有非 key 行（如头部说明注释、空白行），排他管理 public keys
			matchedIndices := make(map[int]bool)
			for _, ek := range existingKeys {
				if !ek.isKey {
					newLines = append(newLines, ek.raw)
					continue
				}
				matchedIdx := -1
				for i, tk := range targetKeys {
					if ek.keyType == tk.keyType && ek.blob == tk.blob {
						matchedIdx = i
						break
					}
				}
				if matchedIdx >= 0 {
					matchedIndices[matchedIdx] = true
					tk := targetKeys[matchedIdx]
					if comment == "" {
						tk.comment = ek.comment
					}
					if keyOptions == "" {
						tk.options = ek.options
					}
					formatted := tk.Format()
					if formatted != ek.raw {
						changed = true
					}
					newLines = append(newLines, formatted)
				} else {
					// 移除未在 targetKeys 中的 key
					changed = true
				}
			}
			// 追加尚未添加的 targetKeys
			for i, tk := range targetKeys {
				if !matchedIndices[i] {
					newLines = append(newLines, tk.Format())
					changed = true
				}
			}
		} else {
			// 非 exclusive 模式：增量添加缺失的 key，更新匹配 key 的 options/comment
			matchedIndices := make(map[int]bool)
			for _, ek := range existingKeys {
				if !ek.isKey {
					newLines = append(newLines, ek.raw)
					continue
				}
				matchedIdx := -1
				for i, tk := range targetKeys {
					if ek.keyType == tk.keyType && ek.blob == tk.blob {
						matchedIdx = i
						break
					}
				}
				if matchedIdx >= 0 {
					matchedIndices[matchedIdx] = true
					tk := targetKeys[matchedIdx]
					// 未指定 comment/options 时，保留原有属性
					if comment == "" {
						tk.comment = ek.comment
					}
					if keyOptions == "" {
						tk.options = ek.options
					}
					formatted := tk.Format()
					if formatted != ek.raw {
						changed = true
						newLines = append(newLines, formatted)
					} else {
						newLines = append(newLines, ek.raw)
					}
				} else {
					newLines = append(newLines, ek.Format())
				}
			}

			// 追加尚未存在的 targetKeys
			for i, tk := range targetKeys {
				if !matchedIndices[i] {
					newLines = append(newLines, tk.Format())
					changed = true
				}
			}
		}

	default:
		return nil, fmt.Errorf("unsupported state: %s (must be present or absent)", state)
	}

	newContent := ""
	if len(newLines) > 0 {
		newContent = strings.Join(newLines, "\n") + "\n"
	}
	oldContent := ""
	if len(existingLines) > 0 {
		oldContent = strings.Join(existingLines, "\n") + "\n"
	}

	// 最终内容比对校验 changed
	if oldContent == newContent {
		changed = false
	}

	// 6. 构造 Diff
	diffText := ""
	if diffMode && changed {
		diffText = buildTextDiff(targetFile, oldContent, newContent)
	}

	if checkMode {
		return &Result{
			Changed:  changed,
			Diff:     diffText,
			Message:  "authorized keys would be modified (check mode)",
			ExitCode: 0,
		}, nil
	}

	if !changed {
		return &Result{
			Changed:  false,
			Diff:     "",
			Message:  "authorized keys already up to date",
			ExitCode: 0,
		}, nil
	}

	// 7. 写入远程文件
	// 确保目标目录权限 (0700)
	if manageDir {
		ensureDirCmd := fmt.Sprintf("mkdir -p %s && chmod 0700 %s && chown %s %s 2>/dev/null || true",
			util.Quote(targetDir), util.Quote(targetDir), util.Quote(user), util.Quote(targetDir))
		if _, err := client.Execute(ensureDirCmd); err != nil {
			return nil, fmt.Errorf("ensure authorized_keys dir %s: %w", targetDir, err)
		}
	}

	marker := fmt.Sprintf("__EOF_AUTHKEY_%d__", time.Now().UnixNano())
	writeCmd := fmt.Sprintf("cat << '%s' > %s.tmp && mv %s.tmp %s && chmod 0600 %s && chown %s %s 2>/dev/null || true\n%s\n%s",
		marker, util.Quote(targetFile), util.Quote(targetFile), util.Quote(targetFile), util.Quote(targetFile), util.Quote(user), util.Quote(targetFile), newContent, marker)

	writeRes, err := client.Execute(writeCmd)
	if err != nil {
		return nil, fmt.Errorf("write authorized_keys to %s: %w", targetFile, err)
	}
	if writeRes != nil && writeRes.ExitCode != 0 {
		return nil, fmt.Errorf("failed to write %s: %s", targetFile, writeRes.Stderr)
	}

	return &Result{
		Changed:  true,
		Diff:     diffText,
		Message:  fmt.Sprintf("authorized keys updated for user %s", user),
		ExitCode: 0,
	}, nil
}

func resolveUserHomeDir(client ssh.Connection, user string) string {
	res, err := client.Execute(fmt.Sprintf("getent passwd %s 2>/dev/null | cut -d: -f6", util.Quote(user)))
	if err == nil && res != nil && strings.TrimSpace(res.Stdout) != "" {
		return strings.TrimSpace(res.Stdout)
	}
	// macOS fallback
	res, err = client.Execute(fmt.Sprintf("dscl . -read /Users/%s NFSHomeDirectory 2>/dev/null | awk '{print $2}'", util.Quote(user)))
	if err == nil && res != nil && strings.TrimSpace(res.Stdout) != "" {
		return strings.TrimSpace(res.Stdout)
	}
	if user == "root" {
		return "/root"
	}
	return "/home/" + user
}

func fetchKeyFromURL(rawURL string, validateCerts bool) (string, error) {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: !validateCerts},
	}
	httpClient := &http.Client{
		Transport: tr,
		Timeout:   10 * time.Second,
	}
	resp, err := httpClient.Get(rawURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("status code %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
