package playbook

import "fmt"

var reservedTaskFields = map[string]struct{}{
	"name":          {},
	"when":          {},
	"loop":          {},
	"with_items":    {},
	"register":      {},
	"until":         {},
	"retries":       {},
	"delay":         {},
	"ignore_errors": {},
	"no_log":        {},
	"delegate_to":   {},
	"local_action":  {},
	"run_once":      {},
	"tags":          {},
	"notify":        {},
	"become":        {},
	"become_user":   {},
}

var reservedHandlerFields = map[string]struct{}{
	"name":       {},
	"listen":     {},
	"loop":       {},
	"with_items": {},
}

func extractModule(raw map[string]interface{}, reserved map[string]struct{}) (string, map[string]interface{}, error) {
	candidates := make([]string, 0, 1)
	for key := range raw {
		if _, ok := reserved[key]; ok {
			continue
		}
		candidates = append(candidates, key)
	}

	if len(candidates) == 0 {
		return "", nil, fmt.Errorf("task has no module")
	}
	if len(candidates) > 1 {
		return "", nil, fmt.Errorf("task has multiple module candidates: %v", candidates)
	}

	name := candidates[0]
	params := make(map[string]interface{})

	switch v := raw[name].(type) {
	case map[string]interface{}:
		params = v
	case string:
		params["_raw_params"] = v
	default:
		params["_raw_params"] = fmt.Sprintf("%v", v)
	}

	return name, params, nil
}

func extractStringList(v interface{}) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		return []string{t}
	case []interface{}:
		result := make([]string, 0, len(t))
		for _, item := range t {
			result = append(result, fmt.Sprintf("%v", item))
		}
		return result
	case []string:
		result := make([]string, len(t))
		copy(result, t)
		return result
	default:
		return []string{fmt.Sprintf("%v", t)}
	}
}

func extractBool(v interface{}) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "yes" || t == "true" || t == "1"
	default:
		return false
	}
}

func extractInt(v interface{}) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	default:
		return 0
	}
}

func extractStringMap(v map[string]interface{}) map[string]string {
	if len(v) == 0 {
		return nil
	}

	result := make(map[string]string, len(v))
	for key, value := range v {
		result[key] = fmt.Sprintf("%v", value)
	}
	return result
}

func extractTaskCommonFields(raw map[string]interface{}, task *Task) {
	if name, ok := raw["name"].(string); ok {
		task.Name = name
	}

	if when, ok := raw["when"]; ok {
		task.When = when
	}

	if loop, ok := raw["loop"]; ok {
		task.Loop = loop
	}
	if withItems, ok := raw["with_items"]; ok {
		task.Loop = withItems
	}

	if register, ok := raw["register"].(string); ok {
		task.Register = register
	}

	if until, ok := raw["until"]; ok {
		task.Until = until
	}

	if retries, ok := raw["retries"]; ok {
		task.Retries = extractInt(retries)
	}

	if delay, ok := raw["delay"]; ok {
		task.Delay = extractInt(delay)
	}

	if ignore, ok := raw["ignore_errors"]; ok {
		task.IgnoreError = extractBool(ignore)
	}

	if noLog, ok := raw["no_log"]; ok {
		task.NoLog = extractBool(noLog)
	}

	if delegateTo, ok := raw["delegate_to"].(string); ok {
		task.DelegateTo = delegateTo
	}

	if localAction, ok := raw["local_action"].(string); ok {
		task.LocalAction = localAction
	}

	if runOnce, ok := raw["run_once"]; ok {
		task.RunOnce = extractBool(runOnce)
	}

	task.Tags = extractStringList(raw["tags"])

	if notify, ok := raw["notify"]; ok {
		task.Notify = notify
	}

	if become, ok := raw["become"]; ok {
		task.Become = extractBool(become)
	}

	if becomeUser, ok := raw["become_user"].(string); ok {
		task.BecomeUser = becomeUser
	}
}

func extractHandlerCommonFields(raw map[string]interface{}, handler *Handler) {
	if name, ok := raw["name"].(string); ok {
		handler.Name = name
	}

	if listen, ok := raw["listen"].(string); ok {
		handler.Listen = listen
	}

	if loop, ok := raw["loop"]; ok {
		handler.Loop = loop
	}
	if withItems, ok := raw["with_items"]; ok {
		handler.WithItems = withItems
		handler.Loop = withItems
	}
}
