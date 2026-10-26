package util

import "fmt"

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
