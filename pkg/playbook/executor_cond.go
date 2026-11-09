package playbook

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"go-ansible/pkg/module"
	"go-ansible/pkg/util"
)

// evaluateWhen 评估 when 条件
func (e *Executor) evaluateWhen(when interface{}, vars map[string]interface{}) bool {
	switch v := when.(type) {
	case bool:
		return v
	case string:
		v = stripSurroundingParentheses(v)
		if parts, ok := splitConditionExpression(v, " or "); ok {
			for _, part := range parts {
				if e.evaluateWhen(part, vars) {
					return true
				}
			}
			return false
		}
		if parts, ok := splitConditionExpression(v, " and "); ok {
			for _, part := range parts {
				if !e.evaluateWhen(part, vars) {
					return false
				}
			}
			return true
		}
		if comparison, ok := e.evaluateComparisonCondition(v, vars); ok {
			return comparison
		}
		if strings.HasPrefix(v, "not ") {
			return !e.evaluateWhen(strings.TrimSpace(strings.TrimPrefix(v, "not ")), vars)
		}
		// 简单条件评估
		if v == "true" || v == "yes" {
			return true
		}
		if v == "false" || v == "no" {
			return false
		}
		// 检查管道过滤器，如 when: val | bool 或 when: list | length > 0
		if strings.Contains(v, "|") {
			if val, ok := e.resolveFilteredExpr(v, vars); ok {
				return truthyValue(val)
			}
		}
		// 检查变量
		if val, ok := e.lookupVarExpr(v, vars); ok {
			return truthyValue(val)
		}
		return false
	case []interface{}:
		// 多个条件，需要全部满足
		for _, cond := range v {
			if !e.evaluateWhen(cond, vars) {
				return false
			}
		}
		return true
	case []string:
		// 字符串切片条件列表，需要全部满足
		for _, cond := range v {
			if !e.evaluateWhen(cond, vars) {
				return false
			}
		}
		return true
	default:
		return true
	}
}

func (e *Executor) evaluateComparisonCondition(expr string, vars map[string]interface{}) (bool, bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return false, false
	}

	if strings.HasSuffix(expr, " is not defined") {
		name := strings.TrimSpace(strings.TrimSuffix(expr, " is not defined"))
		_, ok := e.lookupVarExpr(name, vars)
		return !ok, true
	}
	if strings.HasSuffix(expr, " is defined") {
		name := strings.TrimSpace(strings.TrimSuffix(expr, " is defined"))
		_, ok := e.lookupVarExpr(name, vars)
		return ok, true
	}

	// 支持 Ansible 核心状态测试: is succeeded / is failed / is unreachable / is skipped
	for _, suffix := range []string{" is succeeded", " is success", " is successful"} {
		if strings.HasSuffix(expr, suffix) {
			name := strings.TrimSpace(strings.TrimSuffix(expr, suffix))
			val, ok := e.lookupVarExpr(name, vars)
			return ok && isTaskResultSucceeded(val), true
		}
	}
	for _, suffix := range []string{" is not succeeded", " is not success", " is not successful"} {
		if strings.HasSuffix(expr, suffix) {
			name := strings.TrimSpace(strings.TrimSuffix(expr, suffix))
			val, ok := e.lookupVarExpr(name, vars)
			return !ok || !isTaskResultSucceeded(val), true
		}
	}
	for _, suffix := range []string{" is failed", " is failure"} {
		if strings.HasSuffix(expr, suffix) {
			name := strings.TrimSpace(strings.TrimSuffix(expr, suffix))
			val, ok := e.lookupVarExpr(name, vars)
			return ok && isTaskResultFailed(val), true
		}
	}
	for _, suffix := range []string{" is not failed", " is not failure"} {
		if strings.HasSuffix(expr, suffix) {
			name := strings.TrimSpace(strings.TrimSuffix(expr, suffix))
			val, ok := e.lookupVarExpr(name, vars)
			return !ok || !isTaskResultFailed(val), true
		}
	}
	for _, suffix := range []string{" is changed", " is change"} {
		if strings.HasSuffix(expr, suffix) {
			name := strings.TrimSpace(strings.TrimSuffix(expr, suffix))
			val, ok := e.lookupVarExpr(name, vars)
			return ok && isTaskResultChanged(val), true
		}
	}
	for _, suffix := range []string{" is not changed", " is not change"} {
		if strings.HasSuffix(expr, suffix) {
			name := strings.TrimSpace(strings.TrimSuffix(expr, suffix))
			val, ok := e.lookupVarExpr(name, vars)
			return !ok || !isTaskResultChanged(val), true
		}
	}
	if strings.HasSuffix(expr, " is not unreachable") {
		name := strings.TrimSpace(strings.TrimSuffix(expr, " is not unreachable"))
		val, ok := e.lookupVarExpr(name, vars)
		return !ok || !isTaskResultUnreachable(val), true
	}
	if strings.HasSuffix(expr, " is unreachable") {
		name := strings.TrimSpace(strings.TrimSuffix(expr, " is unreachable"))
		val, ok := e.lookupVarExpr(name, vars)
		return ok && isTaskResultUnreachable(val), true
	}
	if strings.HasSuffix(expr, " is not skipped") {
		name := strings.TrimSpace(strings.TrimSuffix(expr, " is not skipped"))
		val, ok := e.lookupVarExpr(name, vars)
		return !ok || !isTaskResultSkipped(val), true
	}
	if strings.HasSuffix(expr, " is skipped") {
		name := strings.TrimSpace(strings.TrimSuffix(expr, " is skipped"))
		val, ok := e.lookupVarExpr(name, vars)
		return ok && isTaskResultSkipped(val), true
	}

	// 支持 Ansible 核心版本比较测试语法: is version / is version_compare
	if val, ok, handled := e.evaluateVersionCondition(expr, vars); handled {
		return val, ok
	}

	if idx := findTopLevelSubstr(expr, " not in "); idx >= 0 {
		left, leftOK := e.resolveConditionOperand(strings.TrimSpace(expr[:idx]), vars)
		right, rightOK := e.resolveConditionOperand(strings.TrimSpace(expr[idx+len(" not in "):]), vars)
		if !leftOK || !rightOK {
			return false, false
		}
		return !conditionContains(right, left), true
	}
	if idx := findTopLevelSubstr(expr, " in "); idx >= 0 {
		left, leftOK := e.resolveConditionOperand(strings.TrimSpace(expr[:idx]), vars)
		right, rightOK := e.resolveConditionOperand(strings.TrimSpace(expr[idx+len(" in "):]), vars)
		if !leftOK || !rightOK {
			return false, false
		}
		return conditionContains(right, left), true
	}

	if idx, op := findTopLevelComparison(expr); idx >= 0 {
		left, leftOK := e.resolveConditionOperand(strings.TrimSpace(expr[:idx]), vars)
		right, rightOK := e.resolveConditionOperand(strings.TrimSpace(expr[idx+len(op):]), vars)
		if !leftOK || !rightOK {
			return false, false
		}
		return compareConditionValues(left, right, op), true
	}

	return false, false
}

func findTopLevelSubstr(expr, substr string) int {
	var quote byte
	parenDepth := 0
	bracketDepth := 0
	n := len(expr)
	subLen := len(substr)

	for i := 0; i <= n-subLen; i++ {
		ch := expr[i]
		switch {
		case quote != 0:
			if ch == quote {
				quote = 0
			}
		case ch == '\'' || ch == '"':
			quote = ch
		case ch == '(':
			parenDepth++
		case ch == ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case ch == '[':
			bracketDepth++
		case ch == ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case parenDepth == 0 && bracketDepth == 0:
			if expr[i:i+subLen] == substr {
				return i
			}
		}
	}
	return -1
}

func findTopLevelComparison(expr string) (int, string) {
	var quote byte
	parenDepth := 0
	bracketDepth := 0
	n := len(expr)

	for i := 0; i < n; i++ {
		ch := expr[i]
		switch {
		case quote != 0:
			if ch == quote {
				quote = 0
			}
		case ch == '\'' || ch == '"':
			quote = ch
		case ch == '(':
			parenDepth++
		case ch == ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case ch == '[':
			bracketDepth++
		case ch == ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case parenDepth == 0 && bracketDepth == 0:
			if i+2 <= n {
				two := expr[i : i+2]
				if two == "==" || two == "!=" || two == ">=" || two == "<=" {
					return i, two
				}
			}
			if ch == '>' || ch == '<' {
				return i, string(ch)
			}
		}
	}
	return -1, ""
}

func parseVersionArgs(params string) []string {
	parts := strings.Split(params, ",")
	res := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, "\"'")
		if p != "" {
			res = append(res, p)
		}
	}
	return res
}

func (e *Executor) evaluateVersionCondition(expr string, vars map[string]interface{}) (bool, bool, bool) {
	patterns := []struct {
		kw    string
		isNot bool
	}{
		{" is not version_compare(", true},
		{" is not version(", true},
		{" is version_compare(", false},
		{" is version(", false},
	}

	for _, p := range patterns {
		idx := findTopLevelSubstr(expr, p.kw)
		if idx >= 0 && strings.HasSuffix(strings.TrimSpace(expr), ")") {
			leftExpr := strings.TrimSpace(expr[:idx])
			rawParams := expr[idx+len(p.kw):]
			rawParams = strings.TrimSpace(rawParams)
			rawParams = strings.TrimSuffix(rawParams, ")")

			args := parseVersionArgs(rawParams)
			if len(args) == 0 {
				return false, false, true
			}
			targetVersion := args[0]
			op := "=="
			if len(args) > 1 {
				op = args[1]
			}

			val, ok := e.resolveConditionOperand(leftExpr, vars)
			if !ok {
				return false, false, true
			}

			res := util.CompareVersions(fmt.Sprintf("%v", val), targetVersion, op)
			if p.isNot {
				res = !res
			}
			return res, true, true
		}
	}

	return false, false, false
}

func (e *Executor) resolveConditionOperand(operand string, vars map[string]interface{}) (interface{}, bool) {
	operand = stripSurroundingParentheses(operand)
	if operand == "" {
		return "", true
	}
	if quoted, ok := parseQuotedConditionOperand(operand); ok {
		return quoted, true
	}

	switch strings.ToLower(operand) {
	case "true", "yes":
		return true, true
	case "false", "no":
		return false, true
	case "nil", "null":
		return nil, true
	}

	if i, err := strconv.Atoi(operand); err == nil {
		return i, true
	}
	if f, err := strconv.ParseFloat(operand, 64); err == nil {
		return f, true
	}
	// 支持三元表达式作为操作数，如 ('master' if is_master else 'worker') == 'master'
	if val, ok := e.resolveTernaryExpr(operand, vars); ok {
		return val, true
	}
	// 支持管道过滤器，如 (list | length) > 0
	if strings.Contains(operand, "|") {
		if val, ok := e.resolveFilteredExpr(operand, vars); ok {
			return val, true
		}
	}
	if val, ok := e.lookupVarExpr(operand, vars); ok {
		return val, true
	}

	return operand, true
}

func parseQuotedConditionOperand(operand string) (string, bool) {
	if len(operand) < 2 {
		return "", false
	}
	quote := operand[0]
	if quote != '\'' && quote != '"' {
		return "", false
	}
	if operand[len(operand)-1] != quote {
		return "", false
	}
	for i := 1; i < len(operand)-1; i++ {
		if operand[i] == '\\' {
			i++
			continue
		}
		if operand[i] == quote {
			return "", false
		}
	}
	return operand[1 : len(operand)-1], true
}

func compareConditionValues(left, right interface{}, op string) bool {
	if lf, ok := toFloat64(left); ok {
		if rf, ok := toFloat64(right); ok {
			switch op {
			case "==":
				return lf == rf
			case "!=":
				return lf != rf
			case ">":
				return lf > rf
			case "<":
				return lf < rf
			case ">=":
				return lf >= rf
			case "<=":
				return lf <= rf
			}
		}
	}

	ls := fmt.Sprintf("%v", left)
	rs := fmt.Sprintf("%v", right)
	switch op {
	case "==":
		return ls == rs
	case "!=":
		return ls != rs
	case ">":
		return ls > rs
	case "<":
		return ls < rs
	case ">=":
		return ls >= rs
	case "<=":
		return ls <= rs
	default:
		return false
	}
}

func conditionContains(container, item interface{}) bool {
	switch v := container.(type) {
	case []interface{}:
		for _, entry := range v {
			if compareConditionValues(entry, item, "==") {
				return true
			}
		}
		return false
	case []string:
		for _, entry := range v {
			if compareConditionValues(entry, item, "==") {
				return true
			}
		}
		return false
	case string:
		return strings.Contains(v, fmt.Sprintf("%v", item))
	default:
		return strings.Contains(fmt.Sprintf("%v", container), fmt.Sprintf("%v", item))
	}
}

func isTaskResultSucceeded(val interface{}) bool {
	if val == nil {
		return false
	}
	switch m := val.(type) {
	case map[string]interface{}:
		if failed, ok := m["failed"]; ok && extractBool(failed) {
			return false
		}
		if unreach, ok := m["unreachable"]; ok && extractBool(unreach) {
			return false
		}
		if rc, ok := m["rc"]; ok {
			if rci, ok := toFloat64(rc); ok && int(rci) != 0 {
				return false
			}
		}
		return true
	case *TaskResult:
		return !m.Failed && !m.Unreachable && (m.ExitCode == 0 || m.Skipped)
	case *module.Result:
		return !m.IsFailed()
	case bool:
		return m
	default:
		return true
	}
}

func isTaskResultFailed(val interface{}) bool {
	if val == nil {
		return false
	}
	switch m := val.(type) {
	case map[string]interface{}:
		if failed, ok := m["failed"]; ok && extractBool(failed) {
			return true
		}
		if rc, ok := m["rc"]; ok {
			if rci, ok := toFloat64(rc); ok && int(rci) != 0 {
				return true
			}
		}
		return false
	case *TaskResult:
		return m.Failed || m.ExitCode != 0
	case *module.Result:
		return m.IsFailed()
	case bool:
		return !m
	default:
		return false
	}
}

func isTaskResultUnreachable(val interface{}) bool {
	if val == nil {
		return false
	}
	switch m := val.(type) {
	case map[string]interface{}:
		if unreach, ok := m["unreachable"]; ok {
			return extractBool(unreach)
		}
		return false
	case *TaskResult:
		return m.Unreachable
	default:
		return false
	}
}

func isTaskResultSkipped(val interface{}) bool {
	if val == nil {
		return false
	}
	switch m := val.(type) {
	case map[string]interface{}:
		if skipped, ok := m["skipped"]; ok {
			return extractBool(skipped)
		}
		return false
	case *TaskResult:
		return m.Skipped
	default:
		return false
	}
}

func isTaskResultChanged(val interface{}) bool {
	if val == nil {
		return false
	}
	switch m := val.(type) {
	case map[string]interface{}:
		if changed, ok := m["changed"]; ok {
			return extractBool(changed)
		}
		return false
	case *TaskResult:
		return m.Changed
	default:
		return false
	}
}

func splitConditionExpression(expr, sep string) ([]string, bool) {
	var (
		parts   []string
		current strings.Builder
		quote   byte
		depth   int
		found   bool
	)

	for i := 0; i < len(expr); i++ {
		ch := expr[i]
		switch {
		case quote != 0:
			current.WriteByte(ch)
			if ch == quote {
				quote = 0
			}
		case ch == '\'' || ch == '"':
			quote = ch
			current.WriteByte(ch)
		case ch == '(':
			depth++
			current.WriteByte(ch)
		case ch == ')':
			if depth > 0 {
				depth--
			}
			current.WriteByte(ch)
		case depth == 0 && strings.HasPrefix(expr[i:], sep):
			parts = append(parts, strings.TrimSpace(current.String()))
			current.Reset()
			found = true
			i += len(sep) - 1
		default:
			current.WriteByte(ch)
		}
	}

	if !found {
		return nil, false
	}
	parts = append(parts, strings.TrimSpace(current.String()))
	return parts, true
}

func toFloat64(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

// evaluateLoop 评估 loop
func (e *Executor) evaluateLoop(loop interface{}, vars map[string]interface{}) []interface{} {
	value := e.resolveLoopValue(loop, vars)

	switch v := value.(type) {
	case nil:
		return nil
	case []interface{}:
		return v
	case []string:
		result := make([]interface{}, 0, len(v))
		for _, item := range v {
			result = append(result, item)
		}
		return result
	case map[string]interface{}:
		if pattern, ok := fileglobPattern(v); ok {
			return expandFileglob(pattern, e.baseDir)
		}
		if sequence, ok := sequenceSpec(v); ok {
			return expandSequence(sequence)
		}
		if together, ok := togetherSpec(v); ok {
			return e.expandTogether(together, vars)
		}
		if nested, ok := nestedSpec(v); ok {
			return e.expandNested(nested, vars)
		}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		result := make([]interface{}, 0, len(keys))
		for _, key := range keys {
			result = append(result, map[string]interface{}{
				"key":   key,
				"value": v[key],
			})
		}
		return result
	case map[string]string:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		result := make([]interface{}, 0, len(keys))
		for _, key := range keys {
			result = append(result, map[string]interface{}{
				"key":   key,
				"value": v[key],
			})
		}
		return result
	case map[interface{}]interface{}:
		keys := make([]string, 0, len(v))
		values := make(map[string]interface{}, len(v))
		for key, val := range v {
			keyStr := fmt.Sprintf("%v", key)
			keys = append(keys, keyStr)
			values[keyStr] = val
		}
		sort.Strings(keys)
		result := make([]interface{}, 0, len(keys))
		for _, key := range keys {
			result = append(result, map[string]interface{}{
				"key":   key,
				"value": values[key],
			})
		}
		return result
	default:
		return []interface{}{v}
	}
}

func (e *Executor) resolveLoopValue(loop interface{}, vars map[string]interface{}) interface{} {
	switch v := loop.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if strings.HasPrefix(trimmed, "{{") && strings.HasSuffix(trimmed, "}}") {
			expr := strings.TrimSpace(trimmed[2 : len(trimmed)-2])
			res := e.resolveVarExpr(expr, vars)
			if resStr, isStr := res.(string); !isStr || (resStr != "{{ "+expr+" }}" && resStr != trimmed) {
				return res
			}
			if resolved, ok := e.lookupVarExpr(expr, vars); ok {
				return resolved
			}
		}
		if val, ok := vars[v]; ok {
			return val
		}
		return v
	default:
		return loop
	}
}

func fileglobPattern(value map[string]interface{}) (string, bool) {
	if len(value) != 1 {
		return "", false
	}
	pattern, ok := value["__with_fileglob__"]
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%v", pattern), true
}

func sequenceSpec(value map[string]interface{}) (string, bool) {
	if len(value) != 1 {
		return "", false
	}
	spec, ok := value["__with_sequence__"]
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%v", spec), true
}

func togetherSpec(value map[string]interface{}) (interface{}, bool) {
	if len(value) != 1 {
		return nil, false
	}
	spec, ok := value["__with_together__"]
	return spec, ok
}

func nestedSpec(value map[string]interface{}) (interface{}, bool) {
	if len(value) != 1 {
		return nil, false
	}
	spec, ok := value["__with_nested__"]
	return spec, ok
}

func expandFileglob(pattern string, baseDir string) []interface{} {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil
	}

	resolvedPattern := pattern
	if !filepath.IsAbs(resolvedPattern) && baseDir != "" {
		resolvedPattern = filepath.Join(baseDir, resolvedPattern)
	}

	matches, err := filepath.Glob(resolvedPattern)
	if err != nil {
		return []interface{}{}
	}
	sort.Strings(matches)

	result := make([]interface{}, 0, len(matches))
	for _, match := range matches {
		result = append(result, match)
	}
	return result
}

func expandSequence(spec string) []interface{} {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil
	}

	args, err := util.SplitArgs(spec)
	if err != nil {
		return nil
	}

	start := 0
	end := 0
	stride := 1
	format := "%d"
	startSet := false
	endSet := false

	for _, arg := range args {
		key, value, ok := strings.Cut(arg, "=")
		if !ok {
			continue
		}

		switch strings.ToLower(strings.TrimSpace(key)) {
		case "start":
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil
			}
			start = parsed
			startSet = true
		case "end":
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil
			}
			end = parsed
			endSet = true
		case "stride":
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || parsed == 0 {
				return nil
			}
			stride = parsed
		case "format":
			if strings.TrimSpace(value) != "" {
				format = value
			}
		}
	}

	if !startSet || !endSet {
		return nil
	}

	if start < end && stride < 0 {
		stride = -stride
	}
	if start > end && stride > 0 {
		stride = -stride
	}

	result := make([]interface{}, 0)
	for i := start; ; i += stride {
		if stride > 0 && i > end {
			break
		}
		if stride < 0 && i < end {
			break
		}
		result = append(result, fmt.Sprintf(format, i))
	}
	return result
}

func (e *Executor) expandTogether(spec interface{}, vars map[string]interface{}) []interface{} {
	lists := e.loopListSet(spec, vars)
	if len(lists) == 0 {
		return nil
	}

	maxLen := 0
	for _, list := range lists {
		if len(list) > maxLen {
			maxLen = len(list)
		}
	}

	result := make([]interface{}, 0, maxLen)
	for i := 0; i < maxLen; i++ {
		row := make([]interface{}, 0, len(lists))
		for _, list := range lists {
			if i < len(list) {
				row = append(row, list[i])
			} else {
				row = append(row, nil)
			}
		}
		result = append(result, row)
	}
	return result
}

func (e *Executor) expandNested(spec interface{}, vars map[string]interface{}) []interface{} {
	lists := e.loopListSet(spec, vars)
	if len(lists) == 0 {
		return nil
	}

	result := []interface{}{[]interface{}{}}
	for _, list := range lists {
		next := make([]interface{}, 0, len(result)*len(list))
		for _, prefix := range result {
			prefixList, _ := prefix.([]interface{})
			for _, item := range list {
				combined := make([]interface{}, 0, len(prefixList)+1)
				combined = append(combined, prefixList...)
				combined = append(combined, item)
				next = append(next, combined)
			}
		}
		result = next
	}
	return result
}

func (e *Executor) loopListSet(spec interface{}, vars map[string]interface{}) [][]interface{} {
	items, ok := spec.([]interface{})
	if !ok {
		return nil
	}

	lists := make([][]interface{}, 0, len(items))
	for _, item := range items {
		value := e.resolveLoopListItem(item, vars)
		switch typed := value.(type) {
		case []interface{}:
			lists = append(lists, typed)
		case []string:
			list := make([]interface{}, 0, len(typed))
			for _, entry := range typed {
				list = append(list, entry)
			}
			lists = append(lists, list)
		default:
			lists = append(lists, []interface{}{typed})
		}
	}
	return lists
}

func (e *Executor) resolveLoopListItem(item interface{}, vars map[string]interface{}) interface{} {
	switch typed := item.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if strings.HasPrefix(trimmed, "{{") && strings.HasSuffix(trimmed, "}}") {
			expr := strings.TrimSpace(trimmed[2 : len(trimmed)-2])
			if resolved, ok := e.lookupVarExpr(expr, vars); ok {
				return resolved
			}
		}
	}
	return e.resolveLoopValue(item, vars)
}

// resolveVariables 解析变量
func (e *Executor) resolveVariables(params map[string]interface{}, vars map[string]interface{}) map[string]interface{} {
	resolved := make(map[string]interface{})
	for k, v := range params {
		val := e.resolveValue(v, vars)
		if str, ok := val.(string); ok && (str == "__omit_place_holder__" || strings.TrimSpace(str) == "__omit_place_holder__") {
			continue
		}
		resolved[k] = val
	}
	return resolved
}

// resolveValue 解析单个值
func (e *Executor) resolveValue(v interface{}, vars map[string]interface{}) interface{} {
	switch val := v.(type) {
	case string:
		trimmed := strings.TrimSpace(val)
		if strings.HasPrefix(trimmed, "{{") && strings.HasSuffix(trimmed, "}}") && !strings.Contains(trimmed[2:len(trimmed)-2], "{{") {
			expr := strings.TrimSpace(trimmed[2 : len(trimmed)-2])
			res := e.resolveVarExpr(expr, vars)
			if resStr, isStr := res.(string); !isStr || (resStr != "{{ "+expr+" }}" && resStr != trimmed) {
				return res
			}
		}
		return e.resolveString(val, vars)
	case map[string]interface{}:
		return e.resolveVariables(val, vars)
	case rawTask:
		return e.resolveVariables(map[string]interface{}(val), vars)
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, item := range val {
			result[i] = e.resolveValue(item, vars)
		}
		return result
	default:
		return v
	}
}

// resolveString 解析字符串中的变量
func (e *Executor) resolveString(s string, vars map[string]interface{}) string {
	if !strings.Contains(s, "{{") {
		return s
	}

	result := s

	// 循环替换直到没有变量（支持嵌套变量）
	maxIterations := 10
	for i := 0; i < maxIterations; i++ {
		oldResult := result
		searchFrom := 0

		// 匹配 {{ xxx }} 或 {{xxx}}
		for {
			start := strings.Index(result[searchFrom:], "{{")
			if start == -1 {
				break
			}
			start += searchFrom
			end := strings.Index(result[start:], "}}")
			if end == -1 {
				break
			}
			end += start

			// 提取变量名
			varExpr := result[start+2 : end]
			varExpr = strings.TrimSpace(varExpr)

			// 解析变量值
			varValue := e.resolveVarExpr(varExpr, vars)
			replacement := fmt.Sprintf("%v", varValue)
			placeholder := result[start : end+2]

			// 未解析出的占位符保持原样，并继续向后扫描，避免卡在同一处死循环。
			if replacement == placeholder || replacement == "{{ "+varExpr+" }}" {
				searchFrom = end + 2
				continue
			}

			// 替换
			result = result[:start] + replacement + result[end+2:]
			searchFrom = start + len(replacement)
		}

		if result == oldResult {
			break
		}
	}

	return result
}

// resolveVarExpr 解析变量表达式，支持嵌套属性如 item.src
func (e *Executor) resolveVarExpr(expr string, vars map[string]interface{}) interface{} {
	if value, ok := e.resolveLookupExpr(expr, vars); ok {
		return value
	}
	if value, ok := e.resolveTernaryExpr(expr, vars); ok {
		return value
	}
	if filtered, ok := e.resolveFilteredExpr(expr, vars); ok {
		return filtered
	}
	if value, ok := e.lookupVarExpr(expr, vars); ok {
		return value
	}
	return "{{ " + expr + " }}"
}

func (e *Executor) resolveFilteredExpr(expr string, vars map[string]interface{}) (interface{}, bool) {
	parts := splitFilterSegments(expr)
	if len(parts) <= 1 {
		return nil, false
	}

	baseExpr := stripSurroundingParentheses(parts[0])

	var baseValue interface{}
	if val, ok := e.resolveTernaryExpr(baseExpr, vars); ok {
		baseValue = val
	} else if val, ok := e.resolveLookupExpr(baseExpr, vars); ok {
		baseValue = val
	} else if val, ok := e.lookupVarExpr(baseExpr, vars); ok {
		baseValue = val
	} else if quoted, ok := parseQuotedConditionOperand(baseExpr); ok {
		baseValue = quoted
	} else if lit := parseFilterLiteral(baseExpr); lit != baseExpr {
		baseValue = lit
	} else {
		baseValue = nil
	}

	current := baseValue
	for _, segment := range parts[1:] {
		next, applied, err := e.applyTemplateFilter(strings.TrimSpace(segment), current)
		if err != nil || !applied {
			return nil, false
		}
		current = next
	}
	return current, true
}

func splitFilterSegments(expr string) []string {
	return module.SplitFilterSegments(expr)
}

func (e *Executor) applyTemplateFilter(segment string, value interface{}) (interface{}, bool, error) {
	return module.ApplyFilter(segment, value)
}

func splitFilterArgs(argString string) []string {
	return module.SplitFilterArgs(argString)
}

func parseFilterLiteral(raw string) interface{} {
	return module.ParseFilterLiteral(raw)
}

func (e *Executor) parseVarAccessPath(expr string, vars map[string]interface{}) []string {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil
	}

	parts := make([]string, 0)
	var current strings.Builder
	inBracket := false
	var quoteChar rune = 0
	wasQuoted := false

	runes := []rune(expr)
	n := len(runes)

	for i := 0; i < n; i++ {
		ch := runes[i]
		if quoteChar != 0 {
			if ch == quoteChar {
				quoteChar = 0
			} else {
				current.WriteRune(ch)
			}
			continue
		}

		switch ch {
		case '\'', '"':
			quoteChar = ch
			wasQuoted = true
		case '[':
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
			inBracket = true
			wasQuoted = false
		case ']':
			inBracket = false
			segment := strings.TrimSpace(current.String())
			current.Reset()
			if segment != "" {
				if !wasQuoted && vars != nil {
					if _, err := strconv.Atoi(segment); err != nil {
						if val, ok := e.lookupVarExpr(segment, vars); ok && val != nil {
							segment = fmt.Sprintf("%v", val)
						}
					}
				}
				parts = append(parts, segment)
			}
			wasQuoted = false
		case '.':
			if !inBracket {
				if current.Len() > 0 {
					parts = append(parts, current.String())
					current.Reset()
				}
			} else {
				current.WriteRune(ch)
			}
		default:
			current.WriteRune(ch)
		}
	}

	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts
}

func (e *Executor) lookupVarExpr(expr string, vars map[string]interface{}) (interface{}, bool) {
	if vars == nil {
		return nil, false
	}
	if val, ok := vars[expr]; ok {
		return val, true
	}

	parts := e.parseVarAccessPath(expr, vars)
	if len(parts) == 0 {
		return nil, false
	}

	var current interface{} = vars
	for _, part := range parts {
		if part == "" {
			continue
		}

		matched := false
		switch v := current.(type) {
		case map[string]interface{}:
			if val, ok := v[part]; ok {
				current = val
				matched = true
			}
		case map[string]map[string]interface{}:
			if val, ok := v[part]; ok {
				current = val
				matched = true
			}
		case map[string]string:
			if val, ok := v[part]; ok {
				current = val
				matched = true
			}
		case map[string][]string:
			if val, ok := v[part]; ok {
				current = val
				matched = true
			}
		case rawTask:
			if val, ok := v[part]; ok {
				current = val
				matched = true
			}
		case []interface{}:
			idx, err := strconv.Atoi(part)
			if err == nil && idx >= 0 && idx < len(v) {
				current = v[idx]
				matched = true
			}
		case []string:
			idx, err := strconv.Atoi(part)
			if err == nil && idx >= 0 && idx < len(v) {
				current = v[idx]
				matched = true
			}
		}

		if matched {
			continue
		}

		rv := reflect.ValueOf(current)
		if !rv.IsValid() {
			return nil, false
		}
		if rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
			rv = rv.Elem()
			if !rv.IsValid() {
				return nil, false
			}
		}

		switch rv.Kind() {
		case reflect.Map:
			mapKey := reflect.ValueOf(part)
			val := rv.MapIndex(mapKey)
			if !val.IsValid() {
				return nil, false
			}
			current = val.Interface()
		case reflect.Slice, reflect.Array:
			idx, err := strconv.Atoi(part)
			if err != nil || idx < 0 || idx >= rv.Len() {
				return nil, false
			}
			current = rv.Index(idx).Interface()
		default:
			return nil, false
		}
	}

	return current, true
}

func truthyValue(value interface{}) bool {
	return util.IsTruthy(value)
}

func (e *Executor) resolveTernaryExpr(expr string, vars map[string]interface{}) (interface{}, bool) {
	expr = stripSurroundingParentheses(expr)

	truePart, condPart, falsePart, ok := splitTernary(expr)
	if !ok {
		return nil, false
	}

	condResult := e.evaluateWhen(condPart, vars)
	if condResult {
		return e.evaluateTernaryOperand(truePart, vars), true
	}
	return e.evaluateTernaryOperand(falsePart, vars), true
}

func (e *Executor) evaluateTernaryOperand(operand string, vars map[string]interface{}) interface{} {
	operand = stripSurroundingParentheses(operand)
	if operand == "" {
		return ""
	}

	// 嵌套三元（例如右结合 'b' if c2 else 'c'）
	if val, ok := e.resolveTernaryExpr(operand, vars); ok {
		return val
	}
	// 管道过滤器，例如 'foo' | upper 或 val | default('fallback')
	if strings.Contains(operand, "|") {
		if val, ok := e.resolveFilteredExpr(operand, vars); ok {
			return val
		}
	}
	// lookup 函数
	if val, ok := e.resolveLookupExpr(operand, vars); ok {
		return val
	}
	// 引号字符串字面量
	if quoted, ok := parseQuotedConditionOperand(operand); ok {
		return quoted
	}
	switch strings.ToLower(operand) {
	case "true", "yes":
		return true
	case "false", "no":
		return false
	case "none", "null", "nil":
		return nil
	}
	if strings.HasPrefix(operand, "{") && strings.HasSuffix(operand, "}") {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(operand), &m); err == nil {
			return m
		}
	}
	if strings.HasPrefix(operand, "[") && strings.HasSuffix(operand, "]") {
		var list []interface{}
		if err := json.Unmarshal([]byte(operand), &list); err == nil {
			return list
		}
	}
	if i, err := strconv.Atoi(operand); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(operand, 64); err == nil && strings.Contains(operand, ".") {
		return f
	}
	// 变量查找
	if val, ok := e.lookupVarExpr(operand, vars); ok {
		return val
	}
	return operand
}

func stripSurroundingParentheses(s string) string {
	return util.StripSurroundingParentheses(s)
}

func splitTernary(expr string) (string, string, string, bool) {
	return module.SplitTernary(expr)
}

// matchTags 检查任务标签是否匹配
func (e *Executor) matchTags(taskTags []string) bool {
	if len(e.GetTags()) == 0 {
		return true
	}
	return e.shouldRunTaskByTags(taskTags)
}

func (e *Executor) shouldRunTaskByTags(taskTags []string) bool {
	selectedTags := e.GetTags()
	skipTags := e.GetSkipTags()
	taskTags = normalizeTagList(taskTags)

	if shouldSkipByTags(skipTags, taskTags) {
		return false
	}

	isAlways := containsTag(taskTags, "always")
	isNever := containsTag(taskTags, "never")
	isUntagged := len(taskTags) == 0

	// 1. 未指定 --tags 时的默认行为：执行除 never 外的所有任务
	if len(selectedTags) == 0 {
		return !isNever
	}

	// 2. always 任务最高优先级运行
	if isAlways {
		return true
	}

	// 3. --tags all
	if containsTag(selectedTags, "all") {
		if isNever && !containsTag(selectedTags, "never") {
			return false
		}
		return true
	}

	// 4. --tags untagged: 运行所有未声明标签的任务
	if containsTag(selectedTags, "untagged") && isUntagged {
		return true
	}

	// 5. --tags tagged: 运行所有包含显式标签（且非 never）的任务
	if containsTag(selectedTags, "tagged") && !isUntagged && !isNever {
		return true
	}

	if isUntagged {
		return false
	}

	// 官方规范：带有 never 标签的任务，除非命令行显式指定 --tags never，否则任何其它匹配标签均不得运行
	if isNever && !containsTag(selectedTags, "never") {
		return false
	}

	return matchAnyTag(selectedTags, taskTags)
}

func shouldSkipByTags(skipTags, taskTags []string) bool {
	if len(skipTags) == 0 {
		return false
	}
	isAlways := containsTag(taskTags, "always")
	// 官方规范：带有 always 标签的任务除非显式 --skip-tags always，否则即便指定 --skip-tags all 也不应跳过
	if isAlways && !containsTag(skipTags, "always") {
		return false
	}
	if containsTag(skipTags, "always") && isAlways {
		return true
	}
	if containsTag(skipTags, "all") {
		return true
	}
	isUntagged := len(taskTags) == 0
	if containsTag(skipTags, "untagged") && isUntagged {
		return true
	}
	if containsTag(skipTags, "tagged") && !isUntagged {
		return true
	}
	return matchAnyTag(skipTags, taskTags)
}

func matchAnyTag(selectedTags, taskTags []string) bool {
	if len(selectedTags) == 0 || len(taskTags) == 0 {
		return false
	}
	for _, tag := range selectedTags {
		if tag == "all" {
			return true
		}
		for _, taskTag := range taskTags {
			if tag == taskTag {
				return true
			}
		}
	}
	return false
}

func containsTag(tags []string, target string) bool {
	for _, tag := range tags {
		if tag == target {
			return true
		}
	}
	return false
}

func filterOutTag(tags []string, excluded string) []string {
	if len(tags) == 0 {
		return nil
	}
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		if tag == excluded {
			continue
		}
		result = append(result, tag)
	}
	return result
}

func normalizeTagList(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		result = append(result, tag)
	}
	return result
}
