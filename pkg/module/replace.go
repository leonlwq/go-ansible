package module

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"go-ansible/pkg/ssh"
	"go-ansible/pkg/util"
)

// ReplaceModule replace 模块
type ReplaceModule struct{}

func (m *ReplaceModule) Name() string { return "replace" }

func (m *ReplaceModule) Validate(params map[string]interface{}) error {
	if GetParamString(params, "path", "") == "" {
		return fmt.Errorf("replace module requires path")
	}
	regexpText := GetParamString(params, "regexp", "")
	searchString := GetParamString(params, "search_string", "")
	if regexpText == "" && searchString == "" {
		return fmt.Errorf("replace module requires regexp or search_string")
	}
	if regexpText != "" && searchString != "" {
		return fmt.Errorf("replace regexp and search_string are mutually exclusive")
	}
	if regexpText != "" {
		if _, err := util.GetRegex(regexpText); err != nil {
			return fmt.Errorf("compile regexp: %w", err)
		}
	}
	if _, err := normalizeTextEncoding(GetParamString(params, "encoding", "")); err != nil {
		return err
	}
	if before := GetParamString(params, "before", ""); before != "" {
		if _, err := util.GetRegex(before); err != nil {
			return fmt.Errorf("compile before regexp: %w", err)
		}
	}
	if after := GetParamString(params, "after", ""); after != "" {
		if _, err := util.GetRegex(after); err != nil {
			return fmt.Errorf("compile after regexp: %w", err)
		}
	}
	return nil
}

func (m *ReplaceModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	path := GetParamString(params, "path", "")
	pattern := GetParamString(params, "regexp", "")
	searchString := GetParamString(params, "search_string", "")
	replacement := GetParamString(params, "replace", "")
	beforePattern := GetParamString(params, "before", "")
	afterPattern := GetParamString(params, "after", "")
	encoding := GetParamString(params, "encoding", "")
	newline := GetParamString(params, "newline", "")
	backup := GetParamBool(params, "backup", false)
	validate := GetParamString(params, "validate", "")
	checkMode := IsCheckMode(params)
	diffMode := IsDiffMode(params)

	normalizedEncoding, err := normalizeTextEncoding(encoding)
	if err != nil {
		return nil, err
	}
	before, exists, err := readRemoteLineinfileContent(client, path, normalizedEncoding)
	if err != nil {
		return nil, err
	}
	if !exists {
		return &Result{
			Changed:  false,
			Failed:   true,
			Message:  fmt.Sprintf("path not found: %s", path),
			ExitCode: 1,
		}, nil
	}

	after, count, err := applyScopedReplace(before, pattern, searchString, replacement, beforePattern, afterPattern)
	if err != nil {
		return nil, err
	}
	after = applyNewlineMode(after, newline)
	if after == before {
		return &Result{
			Changed: false,
			Message: "content already in desired state",
			Data: map[string]interface{}{
				"path":     path,
				"matched":  false,
				"count":    0,
				"action":   "none",
				"encoding": normalizedEncoding,
				"newline":  normalizeNewlineMode(newline),
				"before":   before,
				"after":    after,
			},
		}, nil
	}

	diff := ""
	if diffMode {
		diff = buildTextDiff(path, before, after)
	}

	if checkMode {
		return &Result{
			Changed: true,
			Message: "check mode: content would be replaced",
			Diff:    diff,
			Data: map[string]interface{}{
				"path":     path,
				"matched":  true,
				"count":    count,
				"action":   "replace",
				"encoding": normalizedEncoding,
				"newline":  normalizeNewlineMode(newline),
				"before":   before,
				"after":    after,
			},
		}, nil
	}

	// 验证逻辑 (validate)
	if validate != "" {
		tmpPath := fmt.Sprintf("/tmp/go-ansible-repval-%d-%s", time.Now().UnixNano(), filepath.Base(path))
		if _, err := writeRemoteEncodedContent(client, tmpPath, after, normalizedEncoding); err != nil {
			return nil, fmt.Errorf("write validate temp file: %w", err)
		}
		defer func() {
			_, _ = client.Execute(fmt.Sprintf("rm -f %s", shellQuote(tmpPath)))
		}()

		if err := executeValidate(client, validate, tmpPath); err != nil {
			return &Result{
				Changed:  false,
				Failed:   true,
				Message:  err.Error(),
				ExitCode: 1,
			}, nil
		}
	}

	backupFile := ""
	if backup {
		_, _ = client.Execute(buildBackupCommand(path))
		backupFile = path + ".bak"
	}

	result, err := writeRemoteEncodedContent(client, path, after, normalizedEncoding)
	if err != nil {
		return result, err
	}
	result.Changed = true
	result.Message = "content replaced successfully"
	result.Diff = diff
	result.Data = map[string]interface{}{
		"path":     path,
		"matched":  true,
		"count":    count,
		"action":   "replace",
		"encoding": normalizedEncoding,
		"newline":  normalizeNewlineMode(newline),
		"before":   before,
		"after":    after,
	}
	if backupFile != "" {
		result.Data["backup_file"] = backupFile
	}
	return result, nil
}

func applyScopedReplace(content, regexpPattern, searchString, replacement, beforePattern, afterPattern string) (string, int, error) {
	start, end, err := replaceScopeBounds(content, beforePattern, afterPattern)
	if err != nil {
		return "", 0, err
	}

	prefix := content[:start]
	target := content[start:end]
	suffix := content[end:]
	if searchString != "" {
		count := strings.Count(target, searchString)
		replaced := strings.ReplaceAll(target, searchString, replacement)
		return prefix + replaced + suffix, count, nil
	}
	pattern, err := util.GetRegex(regexpPattern)
	if err != nil {
		return "", 0, fmt.Errorf("compile regexp: %w", err)
	}
	indexes := pattern.FindAllStringIndex(target, -1)
	replaced := pattern.ReplaceAllString(target, replacement)
	return prefix + replaced + suffix, len(indexes), nil
}

func replaceScopeBounds(content, beforePattern, afterPattern string) (int, int, error) {
	start := 0
	end := len(content)

	if strings.TrimSpace(beforePattern) != "" {
		re, err := util.GetRegex(beforePattern)
		if err != nil {
			return 0, 0, fmt.Errorf("compile before regexp: %w", err)
		}
		loc := re.FindStringIndex(content)
		if loc == nil {
			return 0, len(content), nil
		}
		start = loc[1]
	}

	if strings.TrimSpace(afterPattern) != "" {
		re, err := util.GetRegex(afterPattern)
		if err != nil {
			return 0, 0, fmt.Errorf("compile after regexp: %w", err)
		}
		loc := re.FindStringIndex(content[start:])
		if loc == nil {
			return start, len(content), nil
		}
		end = start + loc[0]
	}

	if start > end {
		start = end
	}
	return start, end, nil
}
