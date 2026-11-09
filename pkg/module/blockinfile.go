package module

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"go-ansible/pkg/ssh"
	"go-ansible/pkg/util"
)

// BlockinfileModule 模块实现 ansible.builtin.blockinfile
type BlockinfileModule struct{}

func (m *BlockinfileModule) Name() string { return "blockinfile" }

func (m *BlockinfileModule) Validate(params map[string]interface{}) error {
	path := getBlockinfileTarget(params)
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("blockinfile module requires path, dest, or name")
	}

	insertAfter := GetParamString(params, "insertafter", "")
	insertBefore := GetParamString(params, "insertbefore", "")
	if insertAfter != "" && insertBefore != "" {
		return fmt.Errorf("insertafter and insertbefore are mutually exclusive in blockinfile")
	}

	if insertAfter != "" && insertAfter != "EOF" {
		if _, err := util.GetRegex(insertAfter); err != nil {
			return fmt.Errorf("invalid insertafter regex: %w", err)
		}
	}
	if insertBefore != "" && insertBefore != "BOF" {
		if _, err := util.GetRegex(insertBefore); err != nil {
			return fmt.Errorf("invalid insertbefore regex: %w", err)
		}
	}

	if _, err := normalizeTextEncoding(GetParamString(params, "encoding", "")); err != nil {
		return err
	}
	return nil
}

func getBlockinfileTarget(params map[string]interface{}) string {
	for _, key := range []string{"path", "dest", "destfile", "name"} {
		if v := strings.TrimSpace(GetParamString(params, key, "")); v != "" {
			return v
		}
	}
	return ""
}

func (m *BlockinfileModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	path := getBlockinfileTarget(params)
	state := strings.TrimSpace(GetParamString(params, "state", "present"))
	blockText := GetParamString(params, "block", "")
	if blockText == "" {
		blockText = GetParamString(params, "content", "")
	}

	marker := GetParamString(params, "marker", "# {mark} ANSIBLE MANAGED BLOCK")
	markerBegin := GetParamString(params, "marker_begin", "BEGIN")
	markerEnd := GetParamString(params, "marker_end", "END")

	insertAfter := GetParamString(params, "insertafter", "EOF")
	insertBefore := GetParamString(params, "insertbefore", "")
	create := GetParamBool(params, "create", false)
	backup := GetParamBool(params, "backup", false)
	validate := GetParamString(params, "validate", "")
	encoding := GetParamString(params, "encoding", "")
	owner := GetParamString(params, "owner", "")
	group := GetParamString(params, "group", "")
	mode := GetParamMode(params, "mode", "")

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
	if !exists && !create {
		return &Result{
			Changed:  false,
			Failed:   true,
			Message:  fmt.Sprintf("path not found: %s", path),
			ExitCode: 1,
		}, nil
	}

	after, changed, err := transformBlockContent(before, blockSpec{
		State:        state,
		Block:        blockText,
		Marker:       marker,
		MarkerBegin:  markerBegin,
		MarkerEnd:    markerEnd,
		InsertAfter:  insertAfter,
		InsertBefore: insertBefore,
	})
	if err != nil {
		return nil, err
	}

	if !changed {
		if exists && (owner != "" || group != "" || mode != "") {
			permChanged, err := CheckOrSetFilePermissions(client, path, mode, owner, group, false, checkMode)
			if err != nil {
				return nil, fmt.Errorf("set file permissions: %w", err)
			}
			if permChanged {
				if checkMode {
					return &Result{
						Changed: true,
						Message: "check mode: file permissions would be updated",
					}, nil
				}
				return &Result{
					Changed: true,
					Message: "file permissions updated",
					Data: map[string]interface{}{
						"path":     path,
						"created":  false,
						"encoding": normalizedEncoding,
						"before":   before,
						"after":    after,
					},
				}, nil
			}
		}
		return &Result{
			Changed: false,
			Message: "content already in desired state",
			Data: map[string]interface{}{
				"path":     path,
				"created":  false,
				"encoding": normalizedEncoding,
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
			Message: "check mode: block would be updated",
			Diff:    diff,
			Data: map[string]interface{}{
				"path":     path,
				"created":  !exists && create && state != "absent",
				"encoding": normalizedEncoding,
				"before":   before,
				"after":    after,
			},
		}, nil
	}

	// 验证逻辑 (validate)
	if validate != "" {
		tmpPath := fmt.Sprintf("/tmp/go-ansible-blockval-%d-%s", time.Now().UnixNano(), filepath.Base(path))
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

	// 备份已有文件
	backupFile := ""
	if backup && exists {
		_, _ = client.Execute(buildBackupCommand(path))
		backupFile = path + ".bak"
	}

	result, err := writeRemoteEncodedContent(client, path, after, normalizedEncoding)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return result, nil
	}

	if owner != "" || group != "" || mode != "" {
		_, _ = SetFilePermissions(client, path, mode, owner, group, false)
	}

	return &Result{
		Changed: true,
		Message: "block updated",
		Diff:    diff,
		Data: map[string]interface{}{
			"path":        path,
			"backup_file": backupFile,
			"created":     !exists && create && state != "absent",
			"encoding":    normalizedEncoding,
			"before":      before,
			"after":       after,
		},
	}, nil
}

type blockSpec struct {
	State        string
	Block        string
	Marker       string
	MarkerBegin  string
	MarkerEnd    string
	InsertAfter  string
	InsertBefore string
}

func transformBlockContent(content string, spec blockSpec) (string, bool, error) {
	hasTrailingNewline := strings.HasSuffix(content, "\n")
	lines := splitContentLines(content)

	markerBegin := strings.ReplaceAll(spec.Marker, "{mark}", spec.MarkerBegin)
	markerEnd := strings.ReplaceAll(spec.Marker, "{mark}", spec.MarkerEnd)

	// 查找现有 marker 索引
	startIdx := -1
	endIdx := -1
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if startIdx == -1 && trimmed == strings.TrimSpace(markerBegin) {
			startIdx = i
		} else if startIdx != -1 && trimmed == strings.TrimSpace(markerEnd) {
			endIdx = i
			break
		}
	}

	var blockLines []string
	cleanBlock := strings.TrimRight(spec.Block, "\r\n")
	if cleanBlock != "" {
		for _, bl := range strings.Split(cleanBlock, "\n") {
			blockLines = append(blockLines, strings.TrimRight(bl, "\r"))
		}
	}

	state := spec.State
	if state == "" {
		state = "present"
	}

	// 如果 state == absent 或 (state == present 且 blockText 为空)
	if state == "absent" || (state == "present" && len(blockLines) == 0) {
		if startIdx == -1 || endIdx == -1 {
			return content, false, nil
		}
		// 移除标记及其中间内容
		newLines := append([]string{}, lines[:startIdx]...)
		if endIdx+1 < len(lines) {
			newLines = append(newLines, lines[endIdx+1:]...)
		}
		res := joinContentLines(newLines, hasTrailingNewline && len(newLines) > 0)
		return res, true, nil
	}

	// 构造完整区块行
	fullBlockLines := make([]string, 0, len(blockLines)+2)
	fullBlockLines = append(fullBlockLines, markerBegin)
	fullBlockLines = append(fullBlockLines, blockLines...)
	fullBlockLines = append(fullBlockLines, markerEnd)

	// 如果现有 marker 已经存在，进行内容对比与替换
	if startIdx != -1 && endIdx != -1 {
		existingBlockLines := lines[startIdx : endIdx+1]
		if slicesEqual(existingBlockLines, fullBlockLines) {
			return content, false, nil
		}
		// 替换
		newLines := make([]string, 0, len(lines)-(endIdx-startIdx+1)+len(fullBlockLines))
		newLines = append(newLines, lines[:startIdx]...)
		newLines = append(newLines, fullBlockLines...)
		if endIdx+1 < len(lines) {
			newLines = append(newLines, lines[endIdx+1:]...)
		}
		res := joinContentLines(newLines, hasTrailingNewline || len(newLines) > 0)
		return res, true, nil
	}

	// marker 不存在，进行插入
	if len(lines) == 0 {
		res := joinContentLines(fullBlockLines, true)
		return res, true, nil
	}

	var newLines []string
	inserted := false

	if spec.InsertBefore == "BOF" {
		newLines = append(newLines, fullBlockLines...)
		newLines = append(newLines, lines...)
		inserted = true
	} else if spec.InsertBefore != "" {
		re, err := util.GetRegex(spec.InsertBefore)
		if err != nil {
			return "", false, fmt.Errorf("compile insertbefore: %w", err)
		}
		for _, l := range lines {
			if !inserted && re.MatchString(l) {
				newLines = append(newLines, fullBlockLines...)
				inserted = true
			}
			newLines = append(newLines, l)
		}
	} else if spec.InsertAfter != "" && spec.InsertAfter != "EOF" {
		re, err := util.GetRegex(spec.InsertAfter)
		if err != nil {
			return "", false, fmt.Errorf("compile insertafter: %w", err)
		}
		lastMatch := -1
		for i, l := range lines {
			if re.MatchString(l) {
				lastMatch = i
			}
		}
		if lastMatch != -1 {
			for i, l := range lines {
				newLines = append(newLines, l)
				if i == lastMatch {
					newLines = append(newLines, fullBlockLines...)
					inserted = true
				}
			}
		}
	}

	if !inserted {
		// 默认追加到 EOF
		newLines = append(newLines, lines...)
		newLines = append(newLines, fullBlockLines...)
	}

	res := joinContentLines(newLines, hasTrailingNewline || len(newLines) > 0)
	return res, true, nil
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
