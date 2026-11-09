package module

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"go-ansible/pkg/util"
)

// IsMatchedParentheses 检查字符串内部括号是否平衡闭合（且中途 depth 从不小于 0）
func IsMatchedParentheses(s string) bool {
	return util.IsMatchedParentheses(s)
}

// HasSurroundingParentheses 检查字符串是否两端被成对的外层括号包裹
func HasSurroundingParentheses(s string) bool {
	return util.HasSurroundingParentheses(s)
}

// StripSurroundingParentheses 循环剥离成对的最外层括号
func StripSurroundingParentheses(s string) string {
	return util.StripSurroundingParentheses(s)
}

// SplitTernary 解析行内三元表达式 "true_expr if cond_expr else false_expr"
func SplitTernary(expr string) (string, string, string, bool) {
	runes := []rune(expr)
	n := len(runes)
	if n < 7 {
		return "", "", "", false
	}

	var quote rune
	parenDepth := 0
	bracketDepth := 0
	braceDepth := 0

	ifIdx := -1

	for i := 0; i < n; i++ {
		ch := runes[i]
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		if ch == '(' {
			parenDepth++
			continue
		} else if ch == ')' {
			if parenDepth > 0 {
				parenDepth--
			}
			continue
		} else if ch == '[' {
			bracketDepth++
			continue
		} else if ch == ']' {
			if bracketDepth > 0 {
				bracketDepth--
			}
			continue
		} else if ch == '{' {
			braceDepth++
			continue
		} else if ch == '}' {
			if braceDepth > 0 {
				braceDepth--
			}
			continue
		}

		if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 {
			// Check for keyword "if"
			if ifIdx == -1 && i > 0 && i+2 <= n && string(runes[i:i+2]) == "if" {
				prevIsSep := unicode.IsSpace(runes[i-1]) || runes[i-1] == ')' || runes[i-1] == ']'
				nextIsSep := i+2 == n || unicode.IsSpace(runes[i+2]) || runes[i+2] == '('
				if prevIsSep && nextIsSep {
					ifIdx = i
					i++ // loop will increment again
					continue
				}
			}

			// If "if" found, check for keyword "else"
			if ifIdx != -1 && i > ifIdx+2 && i+4 <= n && string(runes[i:i+4]) == "else" {
				prevIsSep := unicode.IsSpace(runes[i-1]) || runes[i-1] == ')' || runes[i-1] == ']'
				nextIsSep := i+4 == n || unicode.IsSpace(runes[i+4]) || runes[i+4] == '('
				if prevIsSep && nextIsSep {
					truePart := strings.TrimSpace(string(runes[:ifIdx]))
					condPart := strings.TrimSpace(string(runes[ifIdx+2 : i]))
					falsePart := strings.TrimSpace(string(runes[i+4:]))
					if truePart != "" && condPart != "" && falsePart != "" {
						return truePart, condPart, falsePart, true
					}
				}
			}
		}
	}

	return "", "", "", false
}

// SplitFilterSegments 按顶层管道符 '|' 切分过滤器链（忽略引号与括号内部的管道）
func SplitFilterSegments(expr string) []string {
	segments := make([]string, 0)
	var current strings.Builder
	var quote rune
	depth := 0

	for _, ch := range expr {
		switch {
		case quote != 0:
			current.WriteRune(ch)
			if ch == quote {
				quote = 0
			}
		case ch == '\'' || ch == '"':
			quote = ch
			current.WriteRune(ch)
		case ch == '(' || ch == '[' || ch == '{':
			depth++
			current.WriteRune(ch)
		case ch == ')' || ch == ']' || ch == '}':
			if depth > 0 {
				depth--
			}
			current.WriteRune(ch)
		case ch == '|' && depth == 0:
			segments = append(segments, strings.TrimSpace(current.String()))
			current.Reset()
		default:
			current.WriteRune(ch)
		}
	}

	segments = append(segments, strings.TrimSpace(current.String()))
	return segments
}

// SplitFilterArgs 切分过滤器括号中的参数列表（按逗号切分，忽略引号与括号内部）
func SplitFilterArgs(argString string) []string {
	args := make([]string, 0)
	var current strings.Builder
	var quote rune
	depth := 0

	for _, ch := range argString {
		switch {
		case quote != 0:
			current.WriteRune(ch)
			if ch == quote {
				quote = 0
			}
		case ch == '\'' || ch == '"':
			quote = ch
			current.WriteRune(ch)
		case ch == '(' || ch == '[' || ch == '{':
			depth++
			current.WriteRune(ch)
		case ch == ')' || ch == ']' || ch == '}':
			if depth > 0 {
				depth--
			}
			current.WriteRune(ch)
		case ch == ',' && depth == 0:
			args = append(args, strings.TrimSpace(current.String()))
			current.Reset()
		default:
			current.WriteRune(ch)
		}
	}

	if strings.TrimSpace(current.String()) != "" {
		args = append(args, strings.TrimSpace(current.String()))
	}
	return args
}

// ParseFilterLiteral 解析过滤器实参字面量（字符串、布尔、数字、JSON字典/列表）
func ParseFilterLiteral(raw string) interface{} {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 {
		if (raw[0] == '\'' && raw[len(raw)-1] == '\'') || (raw[0] == '"' && raw[len(raw)-1] == '"') {
			return raw[1 : len(raw)-1]
		}
	}
	switch strings.ToLower(raw) {
	case "true", "yes":
		return true
	case "false", "no":
		return false
	case "none", "null":
		return nil
	case "omit":
		return "__omit_place_holder__"
	}
	if strings.HasPrefix(raw, "{") && strings.HasSuffix(raw, "}") {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &m); err == nil {
			return m
		}
		if err := json.Unmarshal([]byte(convertSingleQuotesToJSON(raw)), &m); err == nil {
			return m
		}
	}
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		var list []interface{}
		if err := json.Unmarshal([]byte(raw), &list); err == nil {
			return list
		}
		if err := json.Unmarshal([]byte(convertSingleQuotesToJSON(raw)), &list); err == nil {
			return list
		}
	}
	if i, err := strconv.Atoi(raw); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil && strings.Contains(raw, ".") {
		return f
	}
	return raw
}

func convertSingleQuotesToJSON(raw string) string {
	var buf strings.Builder
	inSingle := false
	inDouble := false
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		switch {
		case inDouble:
			buf.WriteByte(ch)
			if ch == '\\' && i+1 < len(raw) {
				i++
				buf.WriteByte(raw[i])
			} else if ch == '"' {
				inDouble = false
			}
		case inSingle:
			if ch == '\\' && i+1 < len(raw) {
				i++
				buf.WriteByte(raw[i])
			} else if ch == '\'' {
				buf.WriteByte('"')
				inSingle = false
			} else if ch == '"' {
				buf.WriteString("\\\"")
			} else {
				buf.WriteByte(ch)
			}
		default:
			if ch == '\'' {
				buf.WriteByte('"')
				inSingle = true
			} else if ch == '"' {
				buf.WriteByte('"')
				inDouble = true
			} else {
				buf.WriteByte(ch)
			}
		}
	}
	return buf.String()
}

// ApplyFilter 执行命名模板过滤器
func ApplyFilter(segment string, value interface{}) (interface{}, bool, error) {
	name := strings.TrimSpace(segment)
	args := []string{}
	if open := strings.Index(name, "("); open != -1 && strings.HasSuffix(name, ")") {
		argString := name[open+1 : len(name)-1]
		name = strings.TrimSpace(name[:open])
		args = SplitFilterArgs(argString)
	}

	switch name {
	case "default":
		if len(args) == 0 {
			return value, false, fmt.Errorf("default filter requires one argument")
		}
		var filterArgs []interface{}
		if len(args) > 1 {
			arg1 := strings.TrimSpace(args[1])
			if strings.HasPrefix(strings.ToLower(arg1), "boolean=") {
				boolVal := strings.TrimSpace(arg1[len("boolean="):])
				filterArgs = []interface{}{ParseFilterLiteral(args[0]), ParseFilterLiteral(boolVal), value}
			} else {
				filterArgs = []interface{}{ParseFilterLiteral(args[0]), ParseFilterLiteral(args[1]), value}
			}
		} else {
			filterArgs = []interface{}{ParseFilterLiteral(args[0]), value}
		}
		return TemplateDefaultValue(filterArgs...), true, nil
	case "mandatory":
		valStr := strings.TrimSpace(fmt.Sprintf("%v", value))
		if value == nil || valStr == "" || valStr == "<nil>" {
			errMsg := "Mandatory variable is not defined or is empty"
			if len(args) > 0 {
				errMsg = fmt.Sprintf("%v", ParseFilterLiteral(args[0]))
			}
			return nil, false, fmt.Errorf("%s", errMsg)
		}
		return value, true, nil
	case "ternary":
		if len(args) < 2 {
			return value, false, fmt.Errorf("ternary filter requires at least two arguments")
		}
		tArgs := make([]interface{}, 0, len(args)+1)
		for _, a := range args {
			tArgs = append(tArgs, ParseFilterLiteral(a))
		}
		tArgs = append(tArgs, value)
		return TemplateTernary(tArgs...), true, nil
	case "dict2items":
		res, err := TemplateDict2Items(value)
		if err != nil {
			return value, false, err
		}
		return res, true, nil
	case "items2dict":
		res, err := TemplateItems2Dict(value, args...)
		if err != nil {
			return value, false, err
		}
		return res, true, nil
	case "quote":
		return TemplateQuoteValue(value), true, nil
	case "upper":
		return strings.ToUpper(fmt.Sprintf("%v", value)), true, nil
	case "lower":
		return strings.ToLower(fmt.Sprintf("%v", value)), true, nil
	case "trim":
		return strings.TrimSpace(fmt.Sprintf("%v", value)), true, nil
	case "replace":
		if len(args) < 2 {
			return value, false, fmt.Errorf("replace filter requires two arguments")
		}
		return strings.ReplaceAll(fmt.Sprintf("%v", value), fmt.Sprintf("%v", ParseFilterLiteral(args[0])), fmt.Sprintf("%v", ParseFilterLiteral(args[1]))), true, nil
	case "join":
		if len(args) == 0 {
			return value, false, fmt.Errorf("join filter requires one argument")
		}
		return strings.Join(TemplateNormalizeList(value), fmt.Sprintf("%v", ParseFilterLiteral(args[0]))), true, nil
	case "split":
		if len(args) == 0 {
			return strings.Fields(fmt.Sprintf("%v", value)), true, nil
		}
		sep := fmt.Sprintf("%v", ParseFilterLiteral(args[0]))
		return strings.Split(fmt.Sprintf("%v", value), sep), true, nil
	case "length":
		return TemplateLength(value), true, nil
	case "first":
		item, ok := TemplateIndex(value, 0)
		return item, ok, nil
	case "last":
		length := TemplateLength(value)
		if length <= 0 {
			return nil, false, nil
		}
		item, ok := TemplateIndex(value, length-1)
		return item, ok, nil
	case "to_json":
		data, err := json.Marshal(value)
		if err != nil {
			return value, false, err
		}
		return string(data), true, nil
	case "to_nice_json":
		indent := "  "
		if len(args) > 0 {
			if n, err := strconv.Atoi(strings.TrimSpace(args[0])); err == nil && n >= 0 {
				indent = strings.Repeat(" ", n)
			}
		}
		data, err := json.MarshalIndent(value, "", indent)
		if err != nil {
			return value, false, err
		}
		return string(data), true, nil
	case "from_json":
		var decoded interface{}
		if err := json.Unmarshal([]byte(fmt.Sprintf("%v", value)), &decoded); err != nil {
			return value, false, err
		}
		return decoded, true, nil
	case "to_yaml":
		str, err := TemplateToYAML(value)
		if err != nil {
			return value, false, err
		}
		return str, true, nil
	case "to_nice_yaml":
		var indentArgs []interface{}
		if len(args) > 0 {
			indentArgs = append(indentArgs, args[0])
		}
		str, err := TemplateToNiceYAML(value, indentArgs...)
		if err != nil {
			return value, false, err
		}
		return str, true, nil
	case "from_yaml":
		decoded, err := TemplateFromYAML(value)
		if err != nil {
			return value, false, err
		}
		return decoded, true, nil
	case "basename":
		return filepath.Base(fmt.Sprintf("%v", value)), true, nil
	case "dirname":
		return filepath.Dir(fmt.Sprintf("%v", value)), true, nil
	case "b64encode":
		if b, ok := value.([]byte); ok {
			return base64.StdEncoding.EncodeToString(b), true, nil
		}
		return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%v", value))), true, nil
	case "b64decode":
		data, err := base64.StdEncoding.DecodeString(fmt.Sprintf("%v", value))
		if err != nil {
			return value, false, err
		}
		return string(data), true, nil
	case "to_datetime":
		parsedArgs := []interface{}{value}
		for _, a := range args {
			parsedArgs = append(parsedArgs, ParseFilterLiteral(a))
		}
		return TemplateToDatetime(parsedArgs...), true, nil
	case "strftime":
		parsedArgs := []interface{}{value}
		for _, a := range args {
			parsedArgs = append(parsedArgs, ParseFilterLiteral(a))
		}
		return TemplateStrftime(parsedArgs...), true, nil
	case "urlencode":
		return TemplateUrlencode(value), true, nil
	case "type_debug":
		return TemplateTypeDebug(value), true, nil
	case "combine":
		parsedArgs := make([]interface{}, 0, len(args)+1)
		parsedArgs = append(parsedArgs, value)
		for _, a := range args {
			parsedArgs = append(parsedArgs, ParseFilterLiteral(a))
		}
		res, err := TemplateCombine(parsedArgs...)
		return res, err == nil, err
	case "regex_replace":
		if len(args) < 2 {
			return value, false, fmt.Errorf("regex_replace requires 2 arguments")
		}
		return TemplateRegexReplace(ParseFilterLiteral(args[0]), ParseFilterLiteral(args[1]), value), true, nil
	case "regex_search":
		if len(args) < 1 {
			return "", false, fmt.Errorf("regex_search requires 1 argument")
		}
		return TemplateRegexSearch(ParseFilterLiteral(args[0]), value), true, nil
	case "regex_findall":
		if len(args) < 1 {
			return []string{}, false, fmt.Errorf("regex_findall requires 1 argument")
		}
		return TemplateRegexFindAll(ParseFilterLiteral(args[0]), value), true, nil
	case "selectattr":
		parsedArgs := make([]interface{}, 0, len(args)+1)
		for _, a := range args {
			parsedArgs = append(parsedArgs, ParseFilterLiteral(a))
		}
		parsedArgs = append(parsedArgs, value)
		if len(parsedArgs) == 1 {
			return []interface{}{}, false, fmt.Errorf("selectattr requires at least attribute name")
		}
		return TemplateSelectAttr(parsedArgs[0], parsedArgs[1:]...), true, nil
	case "rejectattr":
		parsedArgs := make([]interface{}, 0, len(args)+1)
		for _, a := range args {
			parsedArgs = append(parsedArgs, ParseFilterLiteral(a))
		}
		parsedArgs = append(parsedArgs, value)
		if len(parsedArgs) == 1 {
			return []interface{}{}, false, fmt.Errorf("rejectattr requires at least attribute name")
		}
		return TemplateRejectAttr(parsedArgs[0], parsedArgs[1:]...), true, nil
	case "map":
		parsedArgs := make([]interface{}, 0, len(args)+1)
		for _, a := range args {
			parsedArgs = append(parsedArgs, ParseFilterLiteral(a))
		}
		parsedArgs = append(parsedArgs, value)
		if len(parsedArgs) == 1 {
			return []interface{}{}, false, fmt.Errorf("map requires at least 1 argument")
		}
		return TemplateMap(parsedArgs[0], parsedArgs[1:]...), true, nil
	case "ipaddr":
		if len(args) == 0 {
			return TemplateIpAddr(value), true, nil
		}
		return TemplateIpAddr(ParseFilterLiteral(args[0]), value), true, nil
	case "flatten":
		return TemplateFlatten(value), true, nil
	case "unique":
		return TemplateUnique(value), true, nil
	case "difference":
		if len(args) == 0 {
			return value, false, fmt.Errorf("difference requires 1 argument")
		}
		return TemplateDifference(ParseFilterLiteral(args[0]), value), true, nil
	case "intersect":
		if len(args) == 0 {
			return value, false, fmt.Errorf("intersect requires 1 argument")
		}
		return TemplateIntersect(ParseFilterLiteral(args[0]), value), true, nil
	case "union":
		if len(args) == 0 {
			return value, false, fmt.Errorf("union requires 1 argument")
		}
		return TemplateUnion(ParseFilterLiteral(args[0]), value), true, nil
	case "min":
		return TemplateMin(value), true, nil
	case "max":
		return TemplateMax(value), true, nil
	case "sum":
		return TemplateSum(value), true, nil
	case "bool":
		return TemplateBool(value), true, nil
	case "int":
		if len(args) > 0 {
			return TemplateInt(ParseFilterLiteral(args[0]), value), true, nil
		}
		return TemplateInt(value), true, nil
	case "float":
		if len(args) > 0 {
			return TemplateFloat(ParseFilterLiteral(args[0]), value), true, nil
		}
		return TemplateFloat(value), true, nil
	case "abs":
		return TemplateAbs(value), true, nil
	case "hash":
		if len(args) == 0 {
			return TemplateHash("sha1", value), true, nil
		}
		return TemplateHash(ParseFilterLiteral(args[0]), value), true, nil
	case "checksum":
		return TemplateChecksum(value), true, nil
	case "urlsplit":
		if len(args) == 0 {
			return TemplateUrlsplit(value), true, nil
		}
		return TemplateUrlsplit(ParseFilterLiteral(args[0]), value), true, nil
	case "version", "version_compare":
		var target, op string
		if len(args) > 0 {
			target = fmt.Sprintf("%v", ParseFilterLiteral(args[0]))
		}
		if len(args) > 1 {
			op = fmt.Sprintf("%v", ParseFilterLiteral(args[1]))
		}
		return util.CompareVersions(fmt.Sprintf("%v", value), target, op), true, nil
	default:
		return nil, false, nil
	}
}
