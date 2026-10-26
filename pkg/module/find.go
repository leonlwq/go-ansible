package module

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"go-ansible/pkg/ssh"
)

// FindModule find 模块
type FindModule struct{}

type findSpec struct {
	Paths         []string
	Patterns      []string
	Excludes      []string
	FileType      string
	Recurse       bool
	MinDepth      int
	Depth         int
	Hidden        bool
	Follow        bool
	UseRegex      bool
	MaxResults    int
	Contains      string
	ReadWholeFile bool
	Age           string
	Size          string
}

func (m *FindModule) Name() string { return "find" }

func (m *FindModule) Validate(params map[string]interface{}) error {
	spec, err := newFindSpec(params)
	if err != nil {
		return err
	}
	if len(spec.Paths) == 0 {
		return fmt.Errorf("find module requires paths")
	}
	if spec.Depth < 0 {
		return fmt.Errorf("find module depth must be >= 0")
	}
	if spec.MinDepth < 0 {
		return fmt.Errorf("find module min_depth must be >= 0")
	}
	if spec.MaxResults < 0 {
		return fmt.Errorf("find module max_results must be >= 0")
	}
	switch spec.FileType {
	case "", "file", "directory", "link", "any":
	default:
		return fmt.Errorf("unsupported find file_type: %s", spec.FileType)
	}
	if spec.UseRegex {
		for _, pattern := range spec.Patterns {
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("compile find pattern regexp %q: %w", pattern, err)
			}
		}
		for _, pattern := range spec.Excludes {
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("compile find exclude regexp %q: %w", pattern, err)
			}
		}
	}
	if spec.Contains != "" {
		if _, err := regexp.Compile(spec.Contains); err != nil {
			return fmt.Errorf("compile find contains regexp %q: %w", spec.Contains, err)
		}
	}
	if spec.Age != "" {
		if _, err := buildFindAgeExpr(spec.Age); err != nil {
			return err
		}
	}
	if spec.Size != "" {
		if _, err := buildFindSizeExpr(spec.Size); err != nil {
			return err
		}
	}
	return nil
}

func (m *FindModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	spec, err := newFindSpec(params)
	if err != nil {
		return nil, err
	}

	cmd, err := buildFindCommand(spec)
	if err != nil {
		return nil, err
	}

	result, err := client.Execute(cmd)
	if err != nil {
		return result, err
	}
	if result == nil {
		return result, nil
	}

	payload := parseFindOutput(result.Stdout)
	if spec.Contains != "" {
		filteredFiles, err := filterFindEntriesByContent(client, payload.Files, spec.Contains, spec.ReadWholeFile)
		if err != nil {
			return nil, err
		}
		payload.Files = filteredFiles
	}
	result.Changed = false
	result.Stdout = strings.Join(extractFindPaths(payload.Files), "\n")
	result.Data = map[string]interface{}{
		"files":            payload.Files,
		"matched":          len(payload.Files),
		"returned":         len(payload.Files),
		"examined":         payload.Examined,
		"skipped_by_limit": payload.SkippedByLimit,
	}
	if result.Message == "" {
		result.Message = fmt.Sprintf("matched %d path(s)", len(payload.Files))
	}
	return result, nil
}

type findOutput struct {
	Files          []map[string]interface{} `json:"files"`
	Examined       int                      `json:"examined"`
	SkippedByLimit int                      `json:"skipped_by_limit"`
}

func newFindSpec(params map[string]interface{}) (findSpec, error) {
	spec := findSpec{
		Paths:         normalizeStringList(params["paths"]),
		Patterns:      normalizeStringList(params["patterns"]),
		Excludes:      normalizeStringList(params["excludes"]),
		FileType:      strings.TrimSpace(GetParamString(params, "file_type", "")),
		Recurse:       getOptionalBool(params, "recurse", true),
		MinDepth:      normalizeInt(params["min_depth"]),
		Depth:         normalizeInt(params["depth"]),
		Hidden:        GetParamBool(params, "hidden", false),
		Follow:        GetParamBool(params, "follow", false),
		UseRegex:      GetParamBool(params, "use_regex", false),
		MaxResults:    normalizeInt(params["max_results"]),
		Contains:      GetParamString(params, "contains", ""),
		ReadWholeFile: GetParamBool(params, "read_whole_file", false),
		Age:           strings.TrimSpace(GetParamString(params, "age", "")),
		Size:          strings.TrimSpace(GetParamString(params, "size", "")),
	}
	if len(spec.Paths) == 0 {
		spec.Paths = normalizeStringList(params["path"])
	}
	if len(spec.Patterns) == 0 {
		spec.Patterns = normalizeStringList(params["pattern"])
	}
	if len(spec.Excludes) == 0 {
		spec.Excludes = normalizeStringList(params["exclude"])
	}
	return spec, nil
}

func normalizeStringList(value interface{}) []string {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		return []string{strings.TrimSpace(v)}
	case []string:
		out := make([]string, 0, len(v))
		for _, item := range v {
			item = strings.TrimSpace(item)
			if item != "" {
				out = append(out, item)
			}
		}
		return out
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			text := strings.TrimSpace(fmt.Sprintf("%v", item))
			if text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		text := strings.TrimSpace(fmt.Sprintf("%v", value))
		if text == "" {
			return nil
		}
		return []string{text}
	}
}

func normalizeInt(value interface{}) int {
	switch v := value.(type) {
	case int:
		return v
	case int8:
		return int(v)
	case int16:
		return int(v)
	case int32:
		return int(v)
	case int64:
		return int(v)
	case uint:
		return int(v)
	case uint8:
		return int(v)
	case uint16:
		return int(v)
	case uint32:
		return int(v)
	case uint64:
		return int(v)
	case float32:
		return int(v)
	case float64:
		return int(v)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return n
		}
	}
	return 0
}

func buildFindCommand(spec findSpec) (string, error) {
	args := make([]string, 0, 32)
	if spec.Follow {
		args = append(args, "-L")
	}
	for _, path := range spec.Paths {
		args = append(args, shellQuote(path))
	}
	if spec.MinDepth > 0 {
		args = append(args, "-mindepth", strconv.Itoa(spec.MinDepth))
	}
	if spec.Depth > 0 {
		args = append(args, "-maxdepth", strconv.Itoa(spec.Depth))
	} else if !spec.Recurse {
		args = append(args, "-maxdepth", "1")
	}
	if !spec.Hidden {
		args = append(args, "!", "-path", shellQuote("*/.*"))
	}
	if expr := buildFindTypeExpr(spec.FileType); expr != "" {
		args = append(args, expr)
	}
	if expr := buildFindNameExpr(spec.Patterns, spec.UseRegex, false); expr != "" {
		args = append(args, expr)
	}
	if expr := buildFindNameExpr(spec.Excludes, spec.UseRegex, true); expr != "" {
		args = append(args, expr)
	}
	if spec.Age != "" {
		expr, err := buildFindAgeExpr(spec.Age)
		if err != nil {
			return "", err
		}
		args = append(args, expr)
	}
	if spec.Size != "" {
		expr, err := buildFindSizeExpr(spec.Size)
		if err != nil {
			return "", err
		}
		args = append(args, expr)
	}

	findExpr := strings.Join(args, " ")
	limitSnippet := ""
	if spec.MaxResults > 0 {
		limitSnippet = fmt.Sprintf(`
  count=$((count + 1))
  if [ "$count" -ge %d ]; then
    skipped=1
    break
  fi`, spec.MaxResults)
	}
	return fmt.Sprintf(`count=0
skipped=0
find %s -print | while IFS= read -r path; do
  [ -n "$path" ] || continue
  if [ -d "$path" ]; then
    state=directory
  elif [ -L "$path" ]; then
    state=link
  else
    state=file
  fi
  size="$(stat -c%%s "$path" 2>/dev/null || stat -f%%z "$path" 2>/dev/null || echo 0)"
  mode="$(stat -c%%a "$path" 2>/dev/null || stat -f%%Lp "$path" 2>/dev/null || echo '')"
  printf '%%s\t%%s\t%%s\t%%s\n' "$path" "$state" "$size" "$mode"
%s
done
printf '__FIND_LIMIT__\t%%s\n' "$skipped"`, findExpr, limitSnippet), nil
}

func buildFindTypeExpr(fileType string) string {
	switch fileType {
	case "", "any":
		return ""
	case "file":
		return "-type f"
	case "directory":
		return "-type d"
	case "link":
		return "-type l"
	default:
		return ""
	}
}

func buildFindNameExpr(patterns []string, useRegex, negate bool) string {
	if len(patterns) == 0 {
		return ""
	}
	parts := make([]string, 0, len(patterns)*5)
	if negate {
		parts = append(parts, "!")
	}
	if len(patterns) > 1 {
		parts = append(parts, "\\(")
	}
	for index, pattern := range patterns {
		if index > 0 {
			parts = append(parts, "-o")
		}
		if useRegex {
			parts = append(parts, "-regex", shellQuote(pattern))
		} else {
			parts = append(parts, "-name", shellQuote(pattern))
		}
	}
	if len(patterns) > 1 {
		parts = append(parts, "\\)")
	}
	return strings.Join(parts, " ")
}

func buildFindAgeExpr(age string) (string, error) {
	original := strings.TrimSpace(age)
	if original == "" {
		return "", nil
	}
	prefix := ""
	age = original
	if strings.HasPrefix(age, "+") || strings.HasPrefix(age, "-") {
		prefix = age[:1]
		age = age[1:]
	}
	if len(age) < 2 {
		return "", fmt.Errorf("invalid find age: %s", original)
	}
	unit := age[len(age)-1]
	value := age[:len(age)-1]
	number, err := strconv.Atoi(value)
	if err != nil {
		return "", fmt.Errorf("invalid find age: %s", original)
	}
	switch unit {
	case 'd':
		return fmt.Sprintf("-mtime %s%d", prefix, number), nil
	case 'h':
		return fmt.Sprintf("-mmin %s%d", prefix, number*60), nil
	case 'm':
		return fmt.Sprintf("-mmin %s%d", prefix, number), nil
	default:
		return "", fmt.Errorf("unsupported find age unit: %c", unit)
	}
}

func buildFindSizeExpr(size string) (string, error) {
	original := strings.TrimSpace(size)
	if original == "" {
		return "", nil
	}
	prefix := ""
	size = original
	if strings.HasPrefix(size, "+") || strings.HasPrefix(size, "-") {
		prefix = size[:1]
		size = size[1:]
	}
	if len(size) < 2 {
		return "", fmt.Errorf("invalid find size: %s", original)
	}
	unit := size[len(size)-1]
	value := size[:len(size)-1]
	if _, err := strconv.Atoi(value); err != nil {
		return "", fmt.Errorf("invalid find size: %s", original)
	}
	switch unit {
	case 'k', 'K':
		return fmt.Sprintf("-size %s%sk", prefix, value), nil
	case 'm', 'M':
		return fmt.Sprintf("-size %s%sM", prefix, value), nil
	case 'g', 'G':
		return fmt.Sprintf("-size %s%sG", prefix, value), nil
	case 'b', 'B':
		return fmt.Sprintf("-size %s%sc", prefix, value), nil
	default:
		return "", fmt.Errorf("unsupported find size unit: %c", unit)
	}
}

func parseFindOutput(stdout string) findOutput {
	output := findOutput{
		Files: make([]map[string]interface{}, 0),
	}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "__FIND_LIMIT__\t") {
			value := strings.TrimPrefix(line, "__FIND_LIMIT__\t")
			if skipped, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
				output.SkippedByLimit = skipped
			}
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 4 {
			continue
		}
		entry := map[string]interface{}{
			"path":  parts[0],
			"state": parts[1],
			"mode":  parts[3],
		}
		if size, err := strconv.ParseInt(parts[2], 10, 64); err == nil {
			entry["size"] = size
		}
		output.Files = append(output.Files, entry)
		output.Examined++
	}
	return output
}

func extractFindPaths(files []map[string]interface{}) []string {
	paths := make([]string, 0, len(files))
	for _, entry := range files {
		if path, ok := entry["path"].(string); ok && path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func filterFindEntriesByContent(client ssh.Connection, files []map[string]interface{}, pattern string, readWholeFile bool) ([]map[string]interface{}, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("compile find contains regexp: %w", err)
	}

	filtered := make([]map[string]interface{}, 0, len(files))
	for _, entry := range files {
		state, _ := entry["state"].(string)
		if state != "file" {
			continue
		}
		path, _ := entry["path"].(string)
		if path == "" {
			continue
		}

		content, err := readRemoteFileContentWithEncoding(client, path, "utf-8")
		if err != nil {
			continue
		}

		if contentMatchesRegexp(content, re, readWholeFile) {
			filtered = append(filtered, entry)
		}
	}
	return filtered, nil
}

func contentMatchesRegexp(content string, re *regexp.Regexp, readWholeFile bool) bool {
	if readWholeFile {
		return re.MatchString(content)
	}
	for _, line := range splitContentLines(content) {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}
