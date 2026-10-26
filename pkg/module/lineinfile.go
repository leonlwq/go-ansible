package module

import (
	"fmt"
	"regexp"
	"strings"

	"go-ansible/pkg/ssh"
)

// LineinfileModule lineinfile 模块
type LineinfileModule struct{}

func (m *LineinfileModule) Name() string { return "lineinfile" }

func (m *LineinfileModule) Validate(params map[string]interface{}) error {
	if strings.TrimSpace(GetParamString(params, "path", "")) == "" {
		return fmt.Errorf("lineinfile module requires path")
	}

	state := strings.TrimSpace(GetParamString(params, "state", "present"))
	line := GetParamString(params, "line", "")
	regexpText := GetParamString(params, "regexp", "")
	searchString := GetParamString(params, "search_string", "")
	if state != "absent" && line == "" && regexpText == "" {
		if searchString == "" {
			return fmt.Errorf("lineinfile module requires line, regexp, or search_string")
		}
	}
	if regexpText != "" {
		if _, err := regexp.Compile(regexpText); err != nil {
			return fmt.Errorf("compile regexp: %w", err)
		}
	}
	if _, err := normalizeTextEncoding(GetParamString(params, "encoding", "")); err != nil {
		return err
	}
	if regexpText != "" && searchString != "" {
		return fmt.Errorf("lineinfile regexp and search_string are mutually exclusive")
	}
	if GetParamBool(params, "backrefs", false) && searchString != "" {
		return fmt.Errorf("lineinfile backrefs requires regexp, not search_string")
	}
	return nil
}

func (m *LineinfileModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	path := GetParamString(params, "path", "")
	state := strings.TrimSpace(GetParamString(params, "state", "present"))
	line := GetParamString(params, "line", "")
	regexpText := GetParamString(params, "regexp", "")
	searchString := GetParamString(params, "search_string", "")
	backrefs := GetParamBool(params, "backrefs", false)
	firstmatch := GetParamBool(params, "firstmatch", false)
	create := GetParamBool(params, "create", false)
	insertafter := GetParamString(params, "insertafter", "")
	insertbefore := GetParamString(params, "insertbefore", "")
	encoding := GetParamString(params, "encoding", "")
	newline := GetParamString(params, "newline", "")
	backup := GetParamBool(params, "backup", false)
	checkMode := GetParamBool(params, "__check_mode", false)
	diffMode := GetParamBool(params, "__diff", false)

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

	after, changed, err := transformLineinfileContent(before, lineinfileSpec{
		State:        state,
		Line:         line,
		Regexp:       regexpText,
		SearchString: searchString,
		Backrefs:     backrefs,
		FirstMatch:   firstmatch,
		InsertAfter:  insertafter,
		InsertBefore: insertbefore,
	})
	if err != nil {
		return nil, err
	}
	after = applyNewlineMode(after, newline)
	if !changed {
		return &Result{
			Changed: false,
			Message: "content already in desired state",
			Data: map[string]interface{}{
				"path":     path,
				"matched":  false,
				"count":    0,
				"action":   "none",
				"created":  !exists && create && state != "absent" && after != "",
				"encoding": normalizedEncoding,
				"newline":  normalizeNewlineMode(newline),
				"before":   before,
				"after":    after,
			},
		}, nil
	}

	matched, count, action, err := describeLineinfileChange(before, lineinfileSpec{
		State:        state,
		Line:         line,
		Regexp:       regexpText,
		SearchString: searchString,
		Backrefs:     backrefs,
		FirstMatch:   firstmatch,
		InsertAfter:  insertafter,
		InsertBefore: insertbefore,
	}, after)
	if err != nil {
		return nil, err
	}

	diff := ""
	if diffMode {
		diff = buildTextDiff(path, before, after)
	}
	if checkMode {
		return &Result{
			Changed: true,
			Message: "check mode: line would be updated",
			Diff:    diff,
			Data: map[string]interface{}{
				"path":     path,
				"matched":  matched,
				"count":    count,
				"action":   action,
				"created":  !exists && create && state != "absent" && after != "",
				"encoding": normalizedEncoding,
				"newline":  normalizeNewlineMode(newline),
				"before":   before,
				"after":    after,
			},
		}, nil
	}

	backupFile := ""
	if backup && exists {
		_, _ = client.Execute(buildBackupCommand(path))
		backupFile = path + ".bak"
	}

	writeCmd, err := buildEncodedContentWriteCommand(path, after, normalizedEncoding)
	if err != nil {
		return nil, err
	}
	result, err := client.Execute(writeCmd)
	if err != nil {
		return result, err
	}
	result.Changed = true
	result.Message = "line updated successfully"
	result.Diff = diff
	result.Data = map[string]interface{}{
		"path":     path,
		"matched":  matched,
		"count":    count,
		"action":   action,
		"created":  !exists && create && state != "absent" && after != "",
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

type lineinfileSpec struct {
	State        string
	Line         string
	Regexp       string
	SearchString string
	Backrefs     bool
	FirstMatch   bool
	InsertAfter  string
	InsertBefore string
}

func readRemoteLineinfileContent(client ssh.Connection, path, encoding string) (string, bool, error) {
	check, err := client.Execute(fmt.Sprintf("test -e %s", shellQuote(path)))
	if err != nil {
		return "", false, err
	}
	if check == nil || check.ExitCode != 0 {
		return "", false, nil
	}
	content, err := readRemoteFileContentWithEncoding(client, path, encoding)
	if err != nil {
		return "", false, err
	}
	return content, true, nil
}

func transformLineinfileContent(content string, spec lineinfileSpec) (string, bool, error) {
	hasTrailingNewline := strings.HasSuffix(content, "\n")
	lines := splitContentLines(content)

	switch spec.State {
	case "", "present":
		updated, changed, err := ensureLinePresent(lines, spec)
		if err != nil {
			return "", false, err
		}
		return joinContentLines(updated, hasTrailingNewline || len(updated) > 0), changed, nil
	case "absent":
		updated, changed, err := ensureLineAbsent(lines, spec)
		if err != nil {
			return "", false, err
		}
		return joinContentLines(updated, hasTrailingNewline && len(updated) > 0), changed, nil
	default:
		return "", false, fmt.Errorf("unsupported lineinfile state: %s", spec.State)
	}
}

func ensureLinePresent(lines []string, spec lineinfileSpec) ([]string, bool, error) {
	match, err := buildLineMatcher(spec)
	if err != nil {
		return nil, false, err
	}
	if match != nil {
		matchIndexes := make([]int, 0)
		for i, current := range lines {
			if match(current) {
				matchIndexes = append(matchIndexes, i)
			}
		}
		if len(matchIndexes) == 0 {
			if spec.Backrefs {
				return lines, false, nil
			}
			updated, changed := insertLine(lines, spec.Line, spec.InsertAfter, spec.InsertBefore)
			return updated, changed, nil
		}

		targetIndex := matchIndexes[len(matchIndexes)-1]
		if spec.FirstMatch {
			targetIndex = matchIndexes[0]
		}

		out := make([]string, len(lines))
		copy(out, lines)
		replacement := spec.Line
		if spec.Backrefs {
			pattern, _ := regexp.Compile(spec.Regexp)
			replacement = pattern.ReplaceAllString(out[targetIndex], spec.Line)
		}

		changed := false
		if out[targetIndex] != replacement {
			out[targetIndex] = replacement
			changed = true
		}

		removed := 0
		for _, index := range matchIndexes {
			if index == targetIndex {
				continue
			}
			adjusted := index - removed
			if adjusted < 0 || adjusted >= len(out) {
				continue
			}
			out = append(out[:adjusted], out[adjusted+1:]...)
			removed++
			changed = true
		}
		return out, changed, nil
	}

	for _, current := range lines {
		if current == spec.Line {
			return lines, false, nil
		}
	}
	updated, changed := insertLine(lines, spec.Line, spec.InsertAfter, spec.InsertBefore)
	return updated, changed, nil
}

func ensureLineAbsent(lines []string, spec lineinfileSpec) ([]string, bool, error) {
	matcher, err := buildLineMatcher(spec)
	if err != nil {
		return nil, false, err
	}
	out := make([]string, 0, len(lines))
	changed := false
	for _, current := range lines {
		matched := false
		if matcher != nil {
			matched = matcher(current)
		} else {
			matched = current == spec.Line
		}
		if matched {
			changed = true
			continue
		}
		out = append(out, current)
	}
	return out, changed, nil
}

func buildLineMatcher(spec lineinfileSpec) (func(string) bool, error) {
	if spec.Regexp != "" {
		pattern, err := regexp.Compile(spec.Regexp)
		if err != nil {
			return nil, fmt.Errorf("compile regexp: %w", err)
		}
		return pattern.MatchString, nil
	}
	if spec.SearchString != "" {
		search := spec.SearchString
		return func(line string) bool {
			return strings.Contains(line, search)
		}, nil
	}
	return nil, nil
}

func describeLineinfileChange(before string, spec lineinfileSpec, after string) (bool, int, string, error) {
	lines := splitContentLines(before)
	matcher, err := buildLineMatcher(spec)
	if err != nil {
		return false, 0, "", err
	}

	matchCount := 0
	if matcher != nil {
		for _, current := range lines {
			if matcher(current) {
				matchCount++
			}
		}
	}

	switch spec.State {
	case "absent":
		if matchCount > 0 {
			return true, matchCount, "delete", nil
		}
		return false, 0, "none", nil
	default:
		if matchCount > 0 {
			return true, matchCount, "replace", nil
		}
		if before != after {
			return false, 1, "insert", nil
		}
		return false, 0, "none", nil
	}
}

func insertLine(lines []string, line, insertafter, insertbefore string) ([]string, bool) {
	if len(lines) == 0 {
		return []string{line}, true
	}

	if insertbefore != "" {
		if strings.EqualFold(strings.TrimSpace(insertbefore), "BOF") {
			out := make([]string, 0, len(lines)+1)
			out = append(out, line)
			out = append(out, lines...)
			return out, true
		}
		if index, ok := findInsertIndex(lines, insertbefore, true); ok {
			out := make([]string, 0, len(lines)+1)
			out = append(out, lines[:index]...)
			out = append(out, line)
			out = append(out, lines[index:]...)
			return out, true
		}
	}

	if insertafter != "" {
		if strings.EqualFold(strings.TrimSpace(insertafter), "EOF") {
			out := make([]string, 0, len(lines)+1)
			out = append(out, lines...)
			out = append(out, line)
			return out, true
		}
		if index, ok := findInsertIndex(lines, insertafter, false); ok {
			out := make([]string, 0, len(lines)+1)
			out = append(out, lines[:index+1]...)
			out = append(out, line)
			out = append(out, lines[index+1:]...)
			return out, true
		}
	}

	out := make([]string, 0, len(lines)+1)
	out = append(out, lines...)
	out = append(out, line)
	return out, true
}

func findInsertIndex(lines []string, patternText string, before bool) (int, bool) {
	if patternText == "" {
		return 0, false
	}
	pattern, err := regexp.Compile(patternText)
	if err == nil {
		for i, current := range lines {
			if pattern.MatchString(current) {
				return i, true
			}
		}
	}
	for i, current := range lines {
		if current == patternText {
			return i, true
		}
	}
	_ = before
	return 0, false
}

func splitContentLines(content string) []string {
	if content == "" {
		return nil
	}
	trimmed := strings.TrimSuffix(content, "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func joinContentLines(lines []string, trailingNewline bool) string {
	if len(lines) == 0 {
		if trailingNewline {
			return ""
		}
		return ""
	}
	joined := strings.Join(lines, "\n")
	if trailingNewline {
		return joined + "\n"
	}
	return joined
}

func escapeForGrep(s string) string {
	result := ""
	for _, c := range s {
		switch c {
		case '/', '\\', '.', '*', '[', ']', '^', '$':
			result += "\\" + string(c)
		default:
			result += string(c)
		}
	}
	return result
}

func escapeForEcho(s string) string {
	result := ""
	for _, c := range s {
		switch c {
		case '\\':
			result += "\\\\"
		case '\'':
			result += "\\'"
		default:
			result += string(c)
		}
	}
	return result
}
