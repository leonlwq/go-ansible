package util

import (
	"fmt"
	"sort"
	"strings"
)

// FormatEnvPrefix 格式化环境变量前缀（按键名升序排列以保证确定性输出）
// 如 map[string]string{"A": "1", "B": "2"} 格式化为 "env A='1' B='2' "
func FormatEnvPrefix(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}

	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("env ")
	for _, k := range keys {
		v := env[k]
		escaped := strings.ReplaceAll(v, `'`, `'"'"'`)
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteByte('\'')
		b.WriteString(escaped)
		b.WriteString("' ")
	}
	return b.String()
}

// Quote 将字符串转义为符合 POSIX 规范的单引号安全字符串。
func Quote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// SplitArgs 按 shell 风格切分参数，支持简单的单双引号和转义。
func SplitArgs(input string) ([]string, error) {
	var (
		args     []string
		current  []rune
		inQuote  rune
		escaping bool
	)

	flush := func() {
		if len(current) == 0 {
			return
		}
		args = append(args, string(current))
		current = current[:0]
	}

	for _, r := range input {
		switch {
		case escaping:
			current = append(current, r)
			escaping = false
		case r == '\\' && inQuote != '\'':
			escaping = true
		case inQuote != 0:
			if r == inQuote {
				inQuote = 0
			} else {
				current = append(current, r)
			}
		case r == '\'' || r == '"':
			inQuote = r
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flush()
		default:
			current = append(current, r)
		}
	}

	if escaping {
		return nil, fmt.Errorf("unfinished escape sequence")
	}
	if inQuote != 0 {
		return nil, fmt.Errorf("unterminated quoted string")
	}

	flush()
	return args, nil
}
