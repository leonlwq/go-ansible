package module

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"text/template"

	"go-ansible/pkg/ssh"
)

var (
	jinjaCommentPattern   = regexp.MustCompile(`(?s)\{#.*?#\}`)
	jinjaForTagPattern    = regexp.MustCompile(`\{%-?\s*for\s+([A-Za-z_][A-Za-z0-9_]*)\s+in\s+(.+?)\s*-?%\}`)
	jinjaTagPattern       = regexp.MustCompile(`\{%-?\s*(.+?)\s*-?%\}`)
	jinjaExprPattern      = regexp.MustCompile(`\{\{\s*(.+?)\s*\}\}`)
	numericLiteralPattern = regexp.MustCompile(`^-?\d+(\.\d+)?$`)

	parsedTemplateCache sync.Map
)

// TemplateModule template 模块
type TemplateModule struct{}

func (m *TemplateModule) Name() string { return "template" }

func (m *TemplateModule) Validate(params map[string]interface{}) error {
	if _, ok := params["src"]; !ok {
		return fmt.Errorf("template module requires src")
	}
	if _, ok := params["dest"]; !ok {
		return fmt.Errorf("template module requires dest")
	}
	return nil
}

func (m *TemplateModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	src := GetParamString(params, "src", "")
	dest := GetParamString(params, "dest", "")
	mode := GetParamMode(params, "mode", "")
	owner := GetParamString(params, "owner", "")
	group := GetParamString(params, "group", "")
	backup := GetParamBool(params, "backup", false)
	force := GetParamBool(params, "force", true)
	become := GetParamBool(params, "become", false)
	validate := GetParamString(params, "validate", "")
	checkMode := IsCheckMode(params)
	diffMode := IsDiffMode(params)
	varsData, _ := params["vars"].(map[string]interface{})

	if varsData == nil {
		varsData = make(map[string]interface{})
	}

	if mode == "preserve" {
		if pMode := ResolvePreserveFileMode(client, src, false); pMode != "" {
			mode = pMode
		} else {
			mode = ""
		}
	}

	// 读取模板文件
	tmplContent, err := os.ReadFile(src)
	if err != nil {
		return nil, fmt.Errorf("read template file: %w", err)
	}

	// 解析并渲染模板。先转换常见 Jinja2 语法，再交给 Go template 执行。
	renderedContent, err := RenderTemplateString(string(tmplContent), varsData)
	if err != nil {
		return nil, fmt.Errorf("render template: %w", err)
	}
	localChecksum := stringChecksum(renderedContent)
	remoteChecksum := readRemoteChecksum(client, dest)
	if localChecksum == remoteChecksum && remoteChecksum != "" {
		permChanged, err := CheckOrSetFilePermissions(client, dest, mode, owner, group, false, checkMode)
		if err != nil {
			return nil, fmt.Errorf("set file permissions: %w", err)
		}
		result := &Result{
			Changed: permChanged,
			Message: "template already rendered with matching content",
		}
		if permChanged {
			if checkMode {
				result.Message = "check mode: file permissions would be updated"
			} else {
				result.Message = "file permissions updated"
			}
		}
		return result, nil
	}
	if remoteChecksum != "" && !force {
		return &Result{
			Changed: false,
			Message: "destination exists with different content and force=false",
		}, nil
	}
	diff := ""
	if diffMode {
		diff = buildTextDiff(dest, readRemoteFileContent(client, dest), renderedContent)
	}
	if checkMode {
		return &Result{
			Changed: true,
			Message: "check mode: template would be rendered",
			Diff:    diff,
		}, nil
	}

	// 创建临时文件
	tmpFile, err := os.CreateTemp("", "go-ansible-template-*")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if _, err := tmpFile.WriteString(renderedContent); err != nil {
		return nil, fmt.Errorf("write rendered template: %w", err)
	}

	// 备份
	if backup && remoteChecksum != "" {
		client.Execute(buildBackupCommand(dest))
	}

	// 确保目标目录存在
	destDir := filepath.Dir(dest)
	client.Execute(fmt.Sprintf("mkdir -p %s", shellQuote(destDir)))
	if dirMode := GetParamMode(params, "directory_mode", ""); dirMode != "" {
		client.Execute(fmt.Sprintf("chmod %s %s", shellQuote(dirMode), shellQuote(destDir)))
	}

	if become || validate != "" {
		remoteTmp := fmt.Sprintf("/tmp/go-ansible-tmpl-%d-%s", os.Getpid(), filepath.Base(dest))
		if err := client.Upload(tmpFile.Name(), remoteTmp); err != nil {
			return nil, fmt.Errorf("upload rendered template to tmp: %w", err)
		}
		if validate != "" {
			if err := executeValidate(client, validate, remoteTmp); err != nil {
				client.Execute(fmt.Sprintf("rm -f %s", shellQuote(remoteTmp)))
				return nil, err
			}
		}
		mvCmd := fmt.Sprintf("mv -f %s %s", shellQuote(remoteTmp), shellQuote(dest))
		result, err := client.Execute(mvCmd)
		if err != nil {
			client.Execute(fmt.Sprintf("rm -f %s", shellQuote(remoteTmp)))
			return nil, fmt.Errorf("move template to dest: %w", err)
		}
		if result != nil && result.ExitCode != 0 {
			client.Execute(fmt.Sprintf("rm -f %s", shellQuote(remoteTmp)))
			return nil, fmt.Errorf("move template failed: %s", strings.TrimSpace(result.Stderr))
		}
	} else {
		// 上传渲染后的文件
		if err := client.Upload(tmpFile.Name(), dest); err != nil {
			return nil, fmt.Errorf("upload rendered template: %w", err)
		}
	}

	// 设置权限
	_, _ = SetFilePermissions(client, dest, mode, owner, group, false)

	return &Result{
		Changed: true,
		Message: "template rendered successfully",
		Diff:    diff,
	}, nil
}

// RenderTemplateString 将包含 Jinja2 语法的模板字符串预处理并基于 Go template 渲染（具备 AST 内存编译缓存加速）
func RenderTemplateString(tmplContent string, varsData map[string]interface{}) (string, error) {
	var tmpl *template.Template
	if val, ok := parsedTemplateCache.Load(tmplContent); ok {
		tmpl = val.(*template.Template)
	} else {
		parsed, err := template.New("template").Funcs(templateFuncMap()).Parse(preprocessJinjaTemplate(tmplContent))
		if err != nil {
			return "", fmt.Errorf("parse template: %w", err)
		}
		parsedTemplateCache.Store(tmplContent, parsed)
		tmpl = parsed
	}
	return renderTemplateContent(tmpl, varsData)
}

func renderTemplateContent(tmpl *template.Template, vars map[string]interface{}) (string, error) {
	var builder strings.Builder
	if err := tmpl.Execute(&builder, vars); err != nil {
		return "", err
	}
	return builder.String(), nil
}

func preprocessJinjaTemplate(content string) string {
	content = jinjaCommentPattern.ReplaceAllString(content, "")

	loopVars := collectJinjaLoopVars(content)
	content = replaceJinjaExpressions(content, loopVars)
	content = replaceJinjaTags(content, loopVars)
	return content
}

func collectJinjaLoopVars(content string) map[string]struct{} {
	loopVars := make(map[string]struct{})
	for _, match := range jinjaForTagPattern.FindAllStringSubmatch(content, -1) {
		loopVars[match[1]] = struct{}{}
	}
	return loopVars
}

func replaceJinjaTags(content string, loopVars map[string]struct{}) string {
	return jinjaTagPattern.ReplaceAllStringFunc(content, func(raw string) string {
		inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(raw, "{%"), "%}"))
		inner = strings.Trim(inner, "- ")

		switch {
		case strings.HasPrefix(inner, "if "):
			return "{{ if " + jinjaConditionToGo(strings.TrimSpace(inner[3:]), loopVars) + " }}"
		case strings.HasPrefix(inner, "elif "):
			return "{{ else if " + jinjaConditionToGo(strings.TrimSpace(inner[5:]), loopVars) + " }}"
		case inner == "else":
			return "{{ else }}"
		case inner == "endif":
			return "{{ end }}"
		case strings.HasPrefix(inner, "for "):
			parts := strings.SplitN(strings.TrimSpace(inner[4:]), " in ", 2)
			if len(parts) != 2 {
				return raw
			}
			name := strings.TrimSpace(parts[0])
			source := jinjaExprToGo(strings.TrimSpace(parts[1]), loopVars)
			return "{{ range $" + name + " := " + source + " }}"
		case inner == "endfor":
			return "{{ end }}"
		default:
			return raw
		}
	})
}

func replaceJinjaExpressions(content string, loopVars map[string]struct{}) string {
	return jinjaExprPattern.ReplaceAllStringFunc(content, func(raw string) string {
		inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(raw, "{{"), "}}"))
		return "{{ " + jinjaExprToGo(inner, loopVars) + " }}"
	})
}

func jinjaExprToGo(expr string, loopVars map[string]struct{}) string {
	expr = strings.TrimSpace(expr)
	for HasSurroundingParentheses(expr) {
		expr = strings.TrimSpace(expr[1 : len(expr)-1])
	}
	if truePart, condPart, falsePart, ok := SplitTernary(expr); ok {
		trueGo := jinjaExprToGo(truePart, loopVars)
		falseGo := jinjaExprToGo(falsePart, loopVars)
		condGo := jinjaConditionToGo(condPart, loopVars)
		return fmt.Sprintf("(ternary %s %s %s)", trueGo, falseGo, condGo)
	}

	parts := SplitFilterSegments(expr)
	if len(parts) == 0 {
		return expr
	}

	current := jinjaValueToGo(parts[0], loopVars)
	for _, filter := range parts[1:] {
		name, args := parseJinjaFilter(filter)
		goArgs := make([]string, 0, len(args)+1)
		for _, arg := range args {
			goArgs = append(goArgs, jinjaValueToGo(arg, loopVars))
		}
		goArgs = append(goArgs, current)
		current = fmt.Sprintf("(%s %s)", name, strings.Join(goArgs, " "))
	}
	return current
}

func jinjaConditionToGo(cond string, loopVars map[string]struct{}) string {
	cond = strings.TrimSpace(cond)
	for HasSurroundingParentheses(cond) {
		cond = strings.TrimSpace(cond[1 : len(cond)-1])
	}
	if strings.HasPrefix(cond, "not ") {
		return fmt.Sprintf("(not %s)", jinjaConditionToGo(strings.TrimSpace(strings.TrimPrefix(cond, "not ")), loopVars))
	}
	if strings.HasSuffix(cond, " is defined") {
		varName := strings.TrimSpace(strings.TrimSuffix(cond, " is defined"))
		return fmt.Sprintf("(ne %s nil)", jinjaValueToGo(varName, loopVars))
	}
	if strings.HasSuffix(cond, " is not defined") {
		varName := strings.TrimSpace(strings.TrimSuffix(cond, " is not defined"))
		return fmt.Sprintf("(eq %s nil)", jinjaValueToGo(varName, loopVars))
	}
	for _, op := range []struct {
		sym string
		fn  string
	}{
		{"==", "eq"},
		{"!=", "ne"},
		{">=", "ge"},
		{"<=", "le"},
		{">", "gt"},
		{"<", "lt"},
	} {
		if idx := strings.Index(cond, op.sym); idx >= 0 {
			left := strings.TrimSpace(cond[:idx])
			right := strings.TrimSpace(cond[idx+len(op.sym):])
			return fmt.Sprintf("(%s %s %s)", op.fn, jinjaExprToGo(left, loopVars), jinjaExprToGo(right, loopVars))
		}
	}
	return jinjaExprToGo(cond, loopVars)
}

func parseJinjaFilter(filter string) (string, []string) {
	filter = strings.TrimSpace(filter)
	if open := strings.Index(filter, "("); open != -1 && strings.HasSuffix(filter, ")") {
		return strings.TrimSpace(filter[:open]), SplitFilterArgs(filter[open+1 : len(filter)-1])
	}
	return filter, nil
}

func jinjaValueToGo(value string, loopVars map[string]struct{}) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	if strings.HasPrefix(value, "lookup(") && strings.HasSuffix(value, ")") {
		innerArgs := value[len("lookup(") : len(value)-1]
		argList := SplitFilterArgs(innerArgs)
		goArgs := make([]string, 0, len(argList))
		for _, arg := range argList {
			goArgs = append(goArgs, jinjaValueToGo(arg, loopVars))
		}
		return fmt.Sprintf("(lookup %s)", strings.Join(goArgs, " "))
	}
	if strings.Contains(value, "=") && !strings.Contains(value, "==") && !strings.Contains(value, "!=") && !strings.Contains(value, "<=") && !strings.Contains(value, ">=") {
		eqIdx := strings.Index(value, "=")
		valPart := strings.TrimSpace(value[eqIdx+1:])
		return jinjaValueToGo(valPart, loopVars)
	}
	if len(value) >= 2 {
		if (value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"') {
			return strconvQuote(value[1 : len(value)-1])
		}
	}
	switch strings.ToLower(value) {
	case "true":
		return "true"
	case "false":
		return "false"
	case "none", "null":
		return "nil"
	}
	if _, err := strconv.ParseFloat(value, 64); err == nil && numericLiteralPattern.MatchString(value) {
		return value
	}

	if goExpr, ok := convertJinjaAccessToGo(value, loopVars); ok {
		return goExpr
	}

	parts := strings.Split(value, ".")
	if parts[0] == "hostvars" && len(parts) > 1 {
		tokens := make([]string, len(parts)-1)
		for i, p := range parts[1:] {
			tokens[i] = strconvQuote(p)
		}
		return fmt.Sprintf("(index .hostvars %s)", strings.Join(tokens, " "))
	}
	if parts[0] == "groups" && len(parts) > 1 {
		tokens := make([]string, len(parts)-1)
		for i, p := range parts[1:] {
			if _, err := strconv.Atoi(p); err == nil {
				tokens[i] = p
			} else {
				tokens[i] = strconvQuote(p)
			}
		}
		return fmt.Sprintf("(index .groups %s)", strings.Join(tokens, " "))
	}

	if _, ok := loopVars[parts[0]]; ok {
		return "$" + value
	}
	return "." + value
}

func convertJinjaAccessToGo(value string, loopVars map[string]struct{}) (string, bool) {
	if !strings.Contains(value, "[") {
		return "", false
	}

	idx := strings.Index(value, "[")
	base := strings.TrimSpace(value[:idx])
	if base == "" {
		return "", false
	}

	var baseExpr string
	if _, ok := loopVars[base]; ok {
		baseExpr = "$" + base
	} else {
		baseExpr = "." + base
	}

	tokens := make([]string, 0)
	rest := value[idx:]
	runes := []rune(rest)
	n := len(runes)

	var current strings.Builder
	inBracket := false
	var quote rune = 0

	for i := 0; i < n; i++ {
		ch := runes[i]
		if quote != 0 {
			if ch == quote {
				quote = 0
			} else {
				current.WriteRune(ch)
			}
			continue
		}

		switch ch {
		case '\'', '"':
			quote = ch
		case '[':
			inBracket = true
			current.Reset()
		case ']':
			inBracket = false
			segment := strings.TrimSpace(current.String())
			current.Reset()
			if segment != "" {
				if _, err := strconv.Atoi(segment); err == nil {
					tokens = append(tokens, segment)
				} else if _, ok := loopVars[segment]; ok {
					tokens = append(tokens, "$"+segment)
				} else {
					tokens = append(tokens, strconvQuote(segment))
				}
			}
		case '.':
			if !inBracket {
				j := i + 1
				var dotIdent strings.Builder
				for j < n && (runes[j] == '_' || (runes[j] >= 'a' && runes[j] <= 'z') || (runes[j] >= 'A' && runes[j] <= 'Z') || (runes[j] >= '0' && runes[j] <= '9') || runes[j] == '-') {
					dotIdent.WriteRune(runes[j])
					j++
				}
				if dotIdent.Len() > 0 {
					tokens = append(tokens, strconvQuote(dotIdent.String()))
					i = j - 1
				}
			} else {
				current.WriteRune(ch)
			}
		default:
			current.WriteRune(ch)
		}
	}

	if len(tokens) == 0 {
		return "", false
	}

	return fmt.Sprintf("(index %s %s)", baseExpr, strings.Join(tokens, " ")), true
}

func strconvQuote(value string) string {
	return fmt.Sprintf("%q", value)
}
