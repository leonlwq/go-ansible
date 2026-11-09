package util

import (
	"strconv"
	"strings"
	"unicode"
)

// CompareVersions 比较两个版本字符串是否满足 op 关系（==, !=, >, >=, <, <= 等）
func CompareVersions(v1, v2, op string) bool {
	v1 = strings.TrimSpace(v1)
	v2 = strings.TrimSpace(v2)
	op = strings.ToLower(strings.TrimSpace(op))
	if op == "" {
		op = "=="
	}

	cmp := compareVersionStrings(v1, v2)
	switch op {
	case "==", "=", "eq":
		return cmp == 0
	case "!=", "ne":
		return cmp != 0
	case ">", "gt":
		return cmp > 0
	case ">=", "ge":
		return cmp >= 0
	case "<", "lt":
		return cmp < 0
	case "<=", "le":
		return cmp <= 0
	default:
		return false
	}
}

func isPreReleaseTag(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func compareVersionStrings(v1, v2 string) int {
	splitFunc := func(r rune) bool {
		return r == '.' || r == '-' || r == '_'
	}
	p1 := strings.FieldsFunc(v1, splitFunc)
	p2 := strings.FieldsFunc(v2, splitFunc)

	maxLen := len(p1)
	if len(p2) > maxLen {
		maxLen = len(p2)
	}

	for i := 0; i < maxLen; i++ {
		var s1, s2 string
		if i < len(p1) {
			s1 = p1[i]
		}
		if i < len(p2) {
			s2 = p2[i]
		}

		if s1 == s2 {
			continue
		}

		// 一方已耗尽时的处理 (区分 pre-release 和补丁版本号)
		if s1 == "" {
			// s2 非空：若 s2 为预发布标签（如 rc1, alpha），则 s1 (正式版) 更大 (+1)；否则若 s2 为正整数数字，s1 较小 (-1)
			if isPreReleaseTag(s2) {
				return 1
			}
			return -1
		}
		if s2 == "" {
			// s1 非空：若 s1 为预发布标签，则 s1 较小 (-1)；否则若 s1 为正整数数字，s1 更大 (+1)
			if isPreReleaseTag(s1) {
				return -1
			}
			return 1
		}

		// 纯数字对比
		i1, err1 := strconv.ParseInt(s1, 10, 64)
		i2, err2 := strconv.ParseInt(s2, 10, 64)
		if err1 == nil && err2 == nil {
			if i1 < i2 {
				return -1
			}
			if i1 > i2 {
				return 1
			}
			continue
		}

		// 混合或字符对比 (若一方为数字，数字大于 pre-release 字母)
		if err1 == nil && err2 != nil {
			return 1
		}
		if err1 != nil && err2 == nil {
			return -1
		}

		if s1 < s2 {
			return -1
		}
		if s1 > s2 {
			return 1
		}
	}

	return 0
}
