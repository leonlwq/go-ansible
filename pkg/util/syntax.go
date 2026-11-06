package util

import (
	"strings"
)

// IsMatchedParentheses checks if parentheses inside s are balanced and never close before opening.
func IsMatchedParentheses(s string) bool {
	depth := 0
	var quote rune
	for _, ch := range s {
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
			depth++
		} else if ch == ')' {
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

// HasSurroundingParentheses returns true if s is enclosed by a matching pair of outermost parentheses.
func HasSurroundingParentheses(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '(' || s[len(s)-1] != ')' {
		return false
	}
	return IsMatchedParentheses(s[1 : len(s)-1])
}

// StripSurroundingParentheses repeatedly strips matched outermost parentheses until none remain.
func StripSurroundingParentheses(s string) string {
	s = strings.TrimSpace(s)
	for HasSurroundingParentheses(s) {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

// Unquote strips surrounding single or double quotes from a string if present.
func Unquote(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		first := s[0]
		last := s[len(s)-1]
		if (first == '\'' && last == '\'') || (first == '"' && last == '"') {
			return s[1 : len(s)-1], true
		}
	}
	return s, false
}

// SplitLines splits a string into lines, normalizing \r\n to \n and stripping any trailing newline.
// An empty string returns an empty slice.
func SplitLines(s string) []string {
	if s == "" {
		return []string{}
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return []string{}
	}
	return strings.Split(s, "\n")
}

// IndentBlock prepends prefix to each line of text.
func IndentBlock(text, prefix string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}


