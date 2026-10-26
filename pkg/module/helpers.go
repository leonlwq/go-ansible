package module

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"

	"go-ansible/pkg/ssh"
)

const (
	changeMarkerTrue  = "__go_ansible_changed__=true"
	changeMarkerFalse = "__go_ansible_changed__=false"
)

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
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

func contains(text, needle string) bool {
	return strings.Contains(text, needle)
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
