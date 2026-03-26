package cli

import (
	"encoding/json"
	"fmt"
	"strings"
)

func parseExtraVars(varsStr string) map[string]interface{} {
	result := make(map[string]interface{})

	if strings.HasPrefix(varsStr, "{") {
		var jsonVars map[string]interface{}
		if err := json.Unmarshal([]byte(varsStr), &jsonVars); err == nil {
			return jsonVars
		}
	}

	pairs := strings.Fields(varsStr)
	for _, pair := range pairs {
		if idx := strings.Index(pair, "="); idx > 0 {
			key := pair[:idx]
			value := pair[idx+1:]
			result[key] = value
		}
	}

	return result
}

func formatItem(item interface{}) string {
	switch v := item.(type) {
	case map[string]interface{}:
		parts := make([]string, 0, len(v))
		for key, val := range v {
			parts = append(parts, fmt.Sprintf("'%s': '%v'", key, val))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case map[interface{}]interface{}:
		parts := make([]string, 0, len(v))
		for key, val := range v {
			parts = append(parts, fmt.Sprintf("'%v': '%v'", key, val))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []interface{}:
		parts := make([]string, 0, len(v))
		for _, val := range v {
			parts = append(parts, formatItem(val))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return fmt.Sprintf("%v", item)
	}
}
