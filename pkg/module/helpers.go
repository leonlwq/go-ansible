package module

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go-ansible/pkg/ssh"
	"go-ansible/pkg/util"
)

const (
	changeMarkerTrue  = "__go_ansible_changed__=true"
	changeMarkerFalse = "__go_ansible_changed__=false"
)

func shellQuote(value string) string {
	return util.Quote(value)
}

func packageNames(params map[string]interface{}) string {
	if raw, ok := params["name"]; ok {
		return normalizePackageNames(raw)
	}
	if raw, ok := params["pkg"]; ok {
		return normalizePackageNames(raw)
	}
	return ""
}

func normalizePackageNames(value interface{}) string {
	switch v := value.(type) {
	case []interface{}:
		names := make([]string, 0, len(v))
		for _, item := range v {
			name := strings.TrimSpace(fmt.Sprintf("%v", item))
			if name != "" {
				names = append(names, name)
			}
		}
		return strings.Join(names, " ")
	case []string:
		names := make([]string, 0, len(v))
		for _, item := range v {
			item = strings.TrimSpace(item)
			if item != "" {
				names = append(names, item)
			}
		}
		return strings.Join(names, " ")
	default:
		raw := fmt.Sprintf("%v", value)
		raw = strings.ReplaceAll(raw, ",", " ")
		return strings.Join(strings.Fields(raw), " ")
	}
}

func hasAnySubstring(text string, needles ...string) bool {
	lower := strings.ToLower(text)
	for _, needle := range needles {
		if strings.Contains(lower, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func parseChangedMarker(text string) (string, bool, bool) {
	cleaned := strings.ReplaceAll(text, changeMarkerTrue, "")
	cleaned = strings.ReplaceAll(cleaned, changeMarkerFalse, "")
	cleaned = strings.TrimSpace(cleaned)
	switch {
	case strings.Contains(text, changeMarkerTrue):
		return cleaned, true, true
	case strings.Contains(text, changeMarkerFalse):
		return cleaned, false, true
	default:
		return strings.TrimSpace(text), false, false
	}
}

// HandleChangedMarker 统一解析并剔除 Result Stdout/Stderr 中的 changed 标记，更新 Result.Changed
func HandleChangedMarker(result *Result) {
	if result == nil {
		return
	}
	if cleaned, changed, found := parseChangedMarker(result.Stdout); found {
		result.Stdout = cleaned
		result.Changed = changed
	}
	if cleaned, _, found := parseChangedMarker(result.Stderr); found {
		result.Stderr = cleaned
	}
}

func checksumCommand(path string) string {
	quotedPath := shellQuote(path)
	return fmt.Sprintf("sha256sum %s 2>/dev/null | cut -d' ' -f1 || shasum -a 256 %s 2>/dev/null | cut -d' ' -f1", quotedPath, quotedPath)
}

func stringChecksum(content string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
}

func resolveDestPath(dest, src string) string {
	if dest == "" {
		return dest
	}
	if strings.HasSuffix(dest, "/") {
		parts := strings.Split(src, "/")
		return dest + parts[len(parts)-1]
	}
	return dest
}

func buildBackupCommand(path string) string {
	quotedPath := shellQuote(path)
	return fmt.Sprintf("cp -p %s %s.bak 2>/dev/null || true", quotedPath, quotedPath)
}

func buildContentWriteCommand(dest, content string) string {
	cmd, err := buildEncodedContentWriteCommand(dest, content, "utf-8")
	if err != nil {
		return ""
	}
	return cmd
}

func readRemoteFileContent(client ssh.Connection, path string) string {
	content, err := readRemoteFileContentWithEncoding(client, path, "utf-8")
	if err != nil {
		return ""
	}
	return content
}

func normalizeTextEncoding(encoding string) (string, error) {
	encoding = strings.ToLower(strings.TrimSpace(encoding))
	if encoding == "" {
		return "utf-8", nil
	}
	switch encoding {
	case "utf-8", "utf8":
		return "utf-8", nil
	case "ascii", "us-ascii":
		return "us-ascii", nil
	case "latin-1", "latin1", "iso-8859-1", "iso8859-1":
		return "latin-1", nil
	default:
		return "", fmt.Errorf("unsupported encoding: %s", encoding)
	}
}

func normalizeNewlineMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "crlf":
		return "crlf"
	case "lf", "":
		return "lf"
	default:
		return "lf"
	}
}

func applyNewlineMode(content, mode string) string {
	mode = normalizeNewlineMode(mode)
	if mode == "crlf" {
		content = strings.ReplaceAll(content, "\r\n", "\n")
		content = strings.ReplaceAll(content, "\r", "\n")
		return strings.ReplaceAll(content, "\n", "\r\n")
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	return content
}

func buildTextDiff(path, before, after string) string {
	if before == after {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("--- before: ")
	builder.WriteString(path)
	builder.WriteString("\n+++ after: ")
	builder.WriteString(path)
	builder.WriteString("\n")

	beforeLines := strings.Split(strings.TrimRight(before, "\n"), "\n")
	afterLines := strings.Split(strings.TrimRight(after, "\n"), "\n")
	if len(beforeLines) == 1 && beforeLines[0] == "" {
		beforeLines = nil
	}
	if len(afterLines) == 1 && afterLines[0] == "" {
		afterLines = nil
	}

	for _, line := range beforeLines {
		builder.WriteString("-")
		builder.WriteString(line)
		builder.WriteString("\n")
	}
	for _, line := range afterLines {
		builder.WriteString("+")
		builder.WriteString(line)
		builder.WriteString("\n")
	}
	return strings.TrimRight(builder.String(), "\n")
}

func buildReadFileBase64Command(path string) string {
	quotedPath := shellQuote(path)
	return fmt.Sprintf("if [ -e %s ]; then if command -v base64 >/dev/null 2>&1; then base64 < %s | tr -d '\\n'; else openssl base64 -A -in %s; fi; fi", quotedPath, quotedPath, quotedPath)
}

func readRemoteFileBytes(client ssh.Connection, path string) ([]byte, error) {
	result, err := client.Execute(buildReadFileBase64Command(path))
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}

	encoded := strings.TrimSpace(result.Stdout)
	if encoded == "" {
		return []byte{}, nil
	}

	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode remote file content for %s: %w", path, err)
	}
	return data, nil
}

func readRemoteFileContentWithEncoding(client ssh.Connection, path, encoding string) (string, error) {
	normalizedEncoding, err := normalizeTextEncoding(encoding)
	if err != nil {
		return "", err
	}

	data, err := readRemoteFileBytes(client, path)
	if err != nil {
		return "", err
	}
	return decodeTextContent(data, normalizedEncoding)
}

func buildEncodedContentWriteCommand(dest, content, encoding string) (string, error) {
	normalizedEncoding, err := normalizeTextEncoding(encoding)
	if err != nil {
		return "", err
	}

	data, err := encodeTextContent(content, normalizedEncoding)
	if err != nil {
		return "", err
	}

	payload := base64.StdEncoding.EncodeToString(data)
	marker := "GOANSIBLE_EOF_" + stringChecksum(payload)[:12]
	return fmt.Sprintf("cat << '%s' | { if command -v base64 >/dev/null 2>&1; then base64 -d; else openssl base64 -A -d; fi; } > %s\n%s\n%s", marker, shellQuote(dest), payload, marker), nil
}

func writeRemoteEncodedContent(client ssh.Connection, dest, content, encoding string) (*Result, error) {
	normalizedEncoding, err := normalizeTextEncoding(encoding)
	if err != nil {
		return nil, err
	}

	data, err := encodeTextContent(content, normalizedEncoding)
	if err != nil {
		return nil, err
	}

	// For content <= 512KB, use fast shell pipeline with base64
	if len(data) <= 512*1024 {
		writeCmd, err := buildEncodedContentWriteCommand(dest, content, normalizedEncoding)
		if err != nil {
			return nil, err
		}
		return client.Execute(writeCmd)
	}

	// For content > 512KB, stream via temporary file to bypass Linux ARG_MAX limits
	tmpFile, err := os.CreateTemp("", "go-ansible-write-*")
	if err != nil {
		return nil, fmt.Errorf("create local temp file: %w", err)
	}
	defer func() {
		_ = os.Remove(tmpFile.Name())
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return nil, fmt.Errorf("write local temp file: %w", err)
	}
	_ = tmpFile.Close()

	remoteTmp := fmt.Sprintf("/tmp/go-ansible-upload-%d-%d", os.Getpid(), time.Now().UnixNano())
	if err := client.Upload(tmpFile.Name(), remoteTmp); err != nil {
		return nil, fmt.Errorf("upload large content: %w", err)
	}
	defer func() {
		_, _ = client.Execute(fmt.Sprintf("rm -f %s", shellQuote(remoteTmp)))
	}()

	mvCmd := fmt.Sprintf("cp -f %s %s", shellQuote(remoteTmp), shellQuote(dest))
	return client.Execute(mvCmd)
}

func decodeTextContent(data []byte, encoding string) (string, error) {
	normalizedEncoding, err := normalizeTextEncoding(encoding)
	if err != nil {
		return "", err
	}

	switch normalizedEncoding {
	case "utf-8":
		if !utf8.Valid(data) {
			return "", fmt.Errorf("content is not valid utf-8")
		}
		return string(data), nil
	case "us-ascii":
		for _, b := range data {
			if b > 0x7f {
				return "", fmt.Errorf("content contains non-ascii bytes")
			}
		}
		return string(data), nil
	case "latin-1":
		var builder strings.Builder
		builder.Grow(len(data))
		for _, b := range data {
			builder.WriteRune(rune(b))
		}
		return builder.String(), nil
	default:
		return "", fmt.Errorf("unsupported encoding: %s", normalizedEncoding)
	}
}

func encodeTextContent(content, encoding string) ([]byte, error) {
	normalizedEncoding, err := normalizeTextEncoding(encoding)
	if err != nil {
		return nil, err
	}

	switch normalizedEncoding {
	case "utf-8":
		return []byte(content), nil
	case "us-ascii":
		data := make([]byte, 0, len(content))
		for _, r := range content {
			if r > 0x7f {
				return nil, fmt.Errorf("content contains rune %q not representable in us-ascii", r)
			}
			data = append(data, byte(r))
		}
		return data, nil
	case "latin-1":
		data := make([]byte, 0, len(content))
		for _, r := range content {
			if r > 0xff {
				return nil, fmt.Errorf("content contains rune %q not representable in latin-1", r)
			}
			data = append(data, byte(r))
		}
		return data, nil
	default:
		return nil, fmt.Errorf("unsupported encoding: %s", normalizedEncoding)
	}
}

// IsCheckMode 统一判定模块参数中是否启用了 check mode (试运行)
func IsCheckMode(params map[string]interface{}) bool {
	return GetParamBool(params, "__check_mode", false) ||
		GetParamBool(params, "_ansible_check_mode", false) ||
		GetParamBool(params, "check_mode", false)
}

// IsDiffMode 统一判定模块参数中是否启用了 diff mode (差量对比)
func IsDiffMode(params map[string]interface{}) bool {
	return GetParamBool(params, "__diff", false) ||
		GetParamBool(params, "_ansible_diff", false) ||
		GetParamBool(params, "diff", false)
}

func safeParamQuote(val string) string {
	if strings.ContainsAny(val, " \t\n\r;|&$`\"'<>(){}\\") {
		return shellQuote(val)
	}
	return val
}

// ResolvePreserveFileMode 根据源文件探测并解析待保留的八进制权限字符串（如 0644、0755）
func ResolvePreserveFileMode(client ssh.Connection, src string, remoteSrc bool) string {
	src = strings.TrimSpace(src)
	if src == "" {
		return ""
	}
	if remoteSrc {
		if client != nil {
			statCmd := fmt.Sprintf("stat -c '%%a' %s 2>/dev/null || stat -f '%%Lp' %s 2>/dev/null", shellQuote(src), shellQuote(src))
			if res, err := client.Execute(statCmd); err == nil && res != nil && res.ExitCode == 0 {
				m := strings.TrimSpace(res.Stdout)
				if m != "" {
					return m
				}
			}
		}
		return ""
	}
	if fi, err := os.Stat(src); err == nil {
		return fmt.Sprintf("%04o", fi.Mode().Perm())
	}
	return ""
}

// SetFilePermissions 统一对远程文件/目录应用 mode、owner、group 权限与属组设置，
// 自动转义路径，支持递归 (-R)，支持仅指定 group（通过 chgrp），
// 并具备幂等探测（非递归模式下预检当前权限属组，已一致时免执行 chmod/chown），返回是否有变更和错误。
func SetFilePermissions(client ssh.Connection, path, mode, owner, group string, recurse bool) (bool, error) {
	return CheckOrSetFilePermissions(client, path, mode, owner, group, recurse, false)
}

// CheckOrSetFilePermissions 检查或设置远程文件/目录的 mode、owner、group 权限与属组。
// 当 checkMode 为 true 时，仅通过只读 stat 探测远端当前权限并计算是否需要变更，绝不执行写操作。
func CheckOrSetFilePermissions(client ssh.Connection, path, mode, owner, group string, recurse, checkMode bool) (bool, error) {
	if mode == "preserve" {
		mode = ""
	}
	if client == nil || path == "" || (mode == "" && owner == "" && group == "") {
		return false, nil
	}
	changed := false
	quotedPath := shellQuote(path)

	needChmod := (mode != "")
	needChown := (owner != "")
	needChgrp := (group != "")

	if !recurse && (mode != "" || owner != "" || group != "") {
		statCmd := fmt.Sprintf("stat -c '%%a:%%U:%%G' %s 2>/dev/null || stat -f '%%Lp:%%Su:%%Sg' %s 2>/dev/null", quotedPath, quotedPath)
		if res, err := client.Execute(statCmd); err == nil && res != nil && res.ExitCode == 0 {
			parts := strings.Split(strings.TrimSpace(res.Stdout), ":")
			if len(parts) == 3 && parts[0] != "" {
				curMode := parts[0]
				curOwner := parts[1]
				curGroup := parts[2]

				if mode != "" {
					modeInt, err1 := strconv.ParseInt(mode, 8, 32)
					curModeInt, err2 := strconv.ParseInt(curMode, 8, 32)
					if err1 == nil && err2 == nil {
						needChmod = (modeInt != curModeInt)
					} else {
						needChmod = (strings.TrimLeft(mode, "0") != strings.TrimLeft(curMode, "0"))
					}
				}

				if owner != "" {
					needChown = (owner != curOwner)
				}
				if group != "" {
					needChgrp = (group != curGroup)
				}
			}
		}
	}

	if checkMode {
		return needChmod || needChown || needChgrp, nil
	}

	if needChmod {
		quotedMode := safeParamQuote(mode)
		chmodCmd := fmt.Sprintf("chmod %s %s", quotedMode, quotedPath)
		if recurse {
			chmodCmd = fmt.Sprintf("chmod -R %s %s", quotedMode, quotedPath)
		}
		if _, err := client.Execute(chmodCmd); err != nil {
			return changed, fmt.Errorf("failed to set mode: %w", err)
		}
		changed = true
	}

	if needChown || (needChgrp && owner != "") {
		target := owner
		if group != "" {
			target = fmt.Sprintf("%s:%s", owner, group)
		}
		quotedTarget := safeParamQuote(target)
		chownCmd := fmt.Sprintf("chown %s %s", quotedTarget, quotedPath)
		if recurse {
			chownCmd = fmt.Sprintf("chown -R %s %s", quotedTarget, quotedPath)
		}
		if _, err := client.Execute(chownCmd); err != nil {
			return changed, fmt.Errorf("failed to set owner: %w", err)
		}
		changed = true
	} else if needChgrp {
		quotedGroup := safeParamQuote(group)
		chgrpCmd := fmt.Sprintf("chgrp %s %s", quotedGroup, quotedPath)
		if recurse {
			chgrpCmd = fmt.Sprintf("chgrp -R %s %s", quotedGroup, quotedPath)
		}
		if _, err := client.Execute(chgrpCmd); err != nil {
			return changed, fmt.Errorf("failed to set group: %w", err)
		}
		changed = true
	}

	return changed, nil
}

// MergeModuleResults 将后续执行结果合并到基础结果中，累加 stdout/stderr/msg 并更新 exitCode 和 changed/failed 状态
func MergeModuleResults(base, next *Result, changed bool) *Result {
	if base == nil {
		base = &Result{}
	}
	if next == nil {
		base.Changed = base.Changed || changed
		return base
	}
	if next.Stdout != "" {
		if base.Stdout != "" {
			base.Stdout += "\n"
		}
		base.Stdout += next.Stdout
	}
	if next.Stderr != "" {
		if base.Stderr != "" {
			base.Stderr += "\n"
		}
		base.Stderr += next.Stderr
	}
	if next.Message != "" {
		if base.Message != "" {
			base.Message += "\n"
		}
		base.Message += next.Message
	}
	base.ExitCode = next.ExitCode
	base.Failed = base.Failed || next.Failed
	base.Changed = base.Changed || changed || next.Changed
	return base
}

// GetOptionalBoolParam 提取可选的布尔参数，返回 (布尔值, 参数是否存在)
func GetOptionalBoolParam(params map[string]interface{}, key string) (bool, bool) {
	value, ok := params[key]
	if !ok || value == nil {
		return false, false
	}
	switch v := value.(type) {
	case bool:
		return v, true
	case string:
		return util.ToBool(v), true
	case int, int64, float64:
		return util.IsTruthy(v), true
	default:
		return false, false
	}
}

// ParseHeadersParam 统一解析 HTTP 请求头参数，兼容 map[string]interface{}、map[string]string 与按行切分的 Header: Value 字符串。
func ParseHeadersParam(value interface{}) map[string]string {
	if value == nil {
		return map[string]string{}
	}
	result := make(map[string]string)
	switch v := value.(type) {
	case map[string]interface{}:
		for k, item := range v {
			k = strings.TrimSpace(k)
			if k != "" {
				result[k] = fmt.Sprintf("%v", item)
			}
		}
	case map[string]string:
		for k, item := range v {
			k = strings.TrimSpace(k)
			if k != "" {
				result[k] = item
			}
		}
	case string:
		lines := strings.Split(v, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if idx := strings.Index(line, ":"); idx != -1 {
				k := strings.TrimSpace(line[:idx])
				val := strings.TrimSpace(line[idx+1:])
				if k != "" {
					result[k] = val
				}
			}
		}
	}
	return result
}

