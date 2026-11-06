package playbook

import (
	"fmt"
	"strconv"
	"strings"

	"go-ansible/pkg/util"
)

var reservedTaskFields = map[string]struct{}{
	"name":               {},
	"block":              {},
	"rescue":             {},
	"always":             {},
	"vars":               {},
	"when":               {},
	"changed_when":       {},
	"failed_when":        {},
	"loop":               {},
	"with_items":         {},
	"with_dict":          {},
	"with_fileglob":      {},
	"with_sequence":      {},
	"with_together":      {},
	"with_nested":        {},
	"loop_control":       {},
	"register":           {},
	"until":              {},
	"retries":            {},
	"delay":              {},
	"async":              {},
	"poll":               {},
	"ignore_errors":      {},
	"ignore_unreachable": {},
	"no_log":             {},
	"delegate_to":        {},
	"delegate_facts":     {},
	"local_action":       {},
	"run_once":           {},
	"tags":               {},
	"notify":             {},
	"become":             {},
	"become_user":        {},
	"become_method":      {},
	"become_pass":        {},
	"become_password":    {},
	"throttle":           {},
	"timeout":            {},
	"debugger":           {},
	"args":               {},
	"environment":        {},
	"check_mode":         {},
	"diff":               {},
}

var reservedHandlerFields = map[string]struct{}{
	"name":               {},
	"listen":             {},
	"notify":             {},
	"loop":               {},
	"with_items":         {},
	"with_dict":          {},
	"with_fileglob":      {},
	"with_sequence":      {},
	"with_together":      {},
	"with_nested":        {},
	"tags":               {},
	"become":             {},
	"become_user":        {},
	"become_method":      {},
	"become_pass":        {},
	"become_password":    {},
	"debugger":           {},
	"args":               {},
	"ignore_unreachable": {},
	"changed_when":       {},
	"failed_when":        {},
	"when":               {},
	"delegate_to":        {},
	"no_log":             {},
}

func normalizeModuleName(name string) string {
	name = strings.TrimSpace(name)
	// 针对 FQCN 全限定集合命名如 ansible.builtin.copy, ansible.posix.firewalld, community.general.sysctl 进行平滑去前缀
	if strings.Contains(name, ".") {
		parts := strings.Split(name, ".")
		return parts[len(parts)-1]
	}
	return name
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

	rawName := candidates[0]
	name := normalizeModuleName(rawName)
	params := make(map[string]interface{})

	switch v := raw[rawName].(type) {
	case map[string]interface{}:
		for k, val := range v {
			params[k] = val
		}
	case rawTask:
		for k, val := range v {
			params[k] = val
		}
	case string:
		params["_raw_params"] = v
	default:
		params["_raw_params"] = fmt.Sprintf("%v", v)
	}

	// 支持 task / handler 级别的 args: 语法糖，将 args 字典中的参数合并到 params 中
	if rawArgs, ok := raw["args"]; ok {
		if argsMap := extractInterfaceMap(rawArgs); argsMap != nil {
			for k, val := range argsMap {
				params[k] = val
			}
		}
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
	return util.ToBool(v)
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

func extractInterfaceMap(v interface{}) map[string]interface{} {
	switch typed := v.(type) {
	case nil:
		return nil
	case map[string]interface{}:
		result := make(map[string]interface{}, len(typed))
		for key, value := range typed {
			result[key] = value
		}
		return result
	case rawTask:
		result := make(map[string]interface{}, len(typed))
		for key, value := range typed {
			result[key] = value
		}
		return result
	default:
		return nil
	}
}

func extractTaskCommonFields(raw map[string]interface{}, task *Task) {
	if name, ok := raw["name"].(string); ok {
		task.Name = name
	}

	task.Vars = extractInterfaceMap(raw["vars"])

	if when, ok := raw["when"]; ok {
		task.When = when
	}
	if changedWhen, ok := raw["changed_when"]; ok {
		task.ChangedWhen = changedWhen
	}
	if failedWhen, ok := raw["failed_when"]; ok {
		task.FailedWhen = failedWhen
	}

	if loop, ok := raw["loop"]; ok {
		task.Loop = loop
	}
	if withItems, ok := raw["with_items"]; ok {
		task.Loop = withItems
	}
	if withDict, ok := raw["with_dict"]; ok {
		task.Loop = withDict
	}
	if withFileglob, ok := raw["with_fileglob"]; ok {
		task.Loop = map[string]interface{}{"__with_fileglob__": withFileglob}
	}
	if withSequence, ok := raw["with_sequence"]; ok {
		task.Loop = map[string]interface{}{"__with_sequence__": withSequence}
	}
	if withTogether, ok := raw["with_together"]; ok {
		task.Loop = map[string]interface{}{"__with_together__": withTogether}
	}
	if withNested, ok := raw["with_nested"]; ok {
		task.Loop = map[string]interface{}{"__with_nested__": withNested}
	}
	if loopControl := extractInterfaceMap(raw["loop_control"]); loopControl != nil {
		if loopVar, ok := loopControl["loop_var"]; ok {
			task.LoopControl.LoopVar = fmt.Sprintf("%v", loopVar)
		}
		if indexVar, ok := loopControl["index_var"]; ok {
			task.LoopControl.IndexVar = fmt.Sprintf("%v", indexVar)
		}
		if label, ok := loopControl["label"]; ok {
			task.LoopControl.Label = fmt.Sprintf("%v", label)
		}
		if extended, ok := loopControl["extended"]; ok {
			task.LoopControl.Extended = extractBool(extended)
		}
		if pause, ok := loopControl["pause"]; ok {
			switch p := pause.(type) {
			case float64:
				task.LoopControl.Pause = p
			case int:
				task.LoopControl.Pause = float64(p)
			case int64:
				task.LoopControl.Pause = float64(p)
			case string:
				if val, err := strconv.ParseFloat(strings.TrimSpace(p), 64); err == nil {
					task.LoopControl.Pause = val
				}
			}
		}
	}

	if register, ok := raw["register"].(string); ok {
		task.Register = register
	}

	if until, ok := raw["until"]; ok {
		task.Until = until
		if _, hasRetries := raw["retries"]; !hasRetries {
			task.Retries = 3
		}
		if _, hasDelay := raw["delay"]; !hasDelay {
			task.Delay = 5
		}
	}

	if retries, ok := raw["retries"]; ok {
		task.Retries = extractInt(retries)
	}

	if delay, ok := raw["delay"]; ok {
		task.Delay = extractInt(delay)
	}
	if async, ok := raw["async"]; ok {
		task.Async = extractInt(async)
	}
	if poll, ok := raw["poll"]; ok {
		value := extractInt(poll)
		task.Poll = &value
	}

	if ignore, ok := raw["ignore_errors"]; ok {
		task.IgnoreError = extractBool(ignore)
	}

	if ignoreUnreachable, ok := raw["ignore_unreachable"]; ok {
		task.IgnoreUnreachable = extractBool(ignoreUnreachable)
	}

	if noLog, ok := raw["no_log"]; ok {
		task.NoLog = extractBool(noLog)
	}

	if delegateTo, ok := raw["delegate_to"].(string); ok {
		task.DelegateTo = delegateTo
	}

	if delegateFacts, ok := raw["delegate_facts"]; ok {
		task.DelegateFacts = extractBool(delegateFacts)
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
		b := extractBool(become)
		task.Become = &b
	}

	if becomeUser, ok := raw["become_user"].(string); ok {
		task.BecomeUser = becomeUser
	}
	if becomeMethod, ok := raw["become_method"].(string); ok {
		task.BecomeMethod = becomeMethod
	}
	if becomePass, ok := raw["become_pass"]; ok {
		task.BecomePass = fmt.Sprintf("%v", becomePass)
	}
	if becomePasswd, ok := raw["become_password"]; ok && task.BecomePass == "" {
		task.BecomePass = fmt.Sprintf("%v", becomePasswd)
	}

	if throttle, ok := raw["throttle"]; ok {
		task.Throttle = extractInt(throttle)
	}

	if timeout, ok := raw["timeout"]; ok {
		task.Timeout = extractInt(timeout)
	}

	if debugger, ok := raw["debugger"]; ok {
		task.Debugger = strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", debugger)))
	}

	if env, ok := raw["environment"]; ok {
		if envMap := extractInterfaceMap(env); envMap != nil {
			task.Environment = extractStringMap(envMap)
		}
	}

	if checkMode, ok := raw["check_mode"]; ok {
		cm := extractBool(checkMode)
		task.CheckMode = &cm
	}
	if diff, ok := raw["diff"]; ok {
		df := extractBool(diff)
		task.Diff = &df
	}
}

func extractHandlerCommonFields(raw map[string]interface{}, handler *Handler) {
	if name, ok := raw["name"].(string); ok {
		handler.Name = name
	}

	if listenVal, ok := raw["listen"]; ok && listenVal != nil {
		switch lv := listenVal.(type) {
		case string:
			handler.Listen = lv
			handler.Listens = []string{lv}
		case []interface{}:
			for _, item := range lv {
				if s := strings.TrimSpace(fmt.Sprintf("%v", item)); s != "" {
					handler.Listens = append(handler.Listens, s)
				}
			}
			if len(handler.Listens) > 0 {
				handler.Listen = handler.Listens[0]
			}
		case []string:
			handler.Listens = lv
			if len(lv) > 0 {
				handler.Listen = lv[0]
			}
		}
	}

	if notify, ok := raw["notify"]; ok {
		handler.Notify = notify
	}

	if loop, ok := raw["loop"]; ok {
		handler.Loop = loop
	}
	if withItems, ok := raw["with_items"]; ok {
		handler.WithItems = withItems
		handler.Loop = withItems
	}
	if withDict, ok := raw["with_dict"]; ok {
		handler.Loop = withDict
	}
	if withFileglob, ok := raw["with_fileglob"]; ok {
		handler.Loop = map[string]interface{}{"__with_fileglob__": withFileglob}
	}
	if withSequence, ok := raw["with_sequence"]; ok {
		handler.Loop = map[string]interface{}{"__with_sequence__": withSequence}
	}
	if withTogether, ok := raw["with_together"]; ok {
		handler.Loop = map[string]interface{}{"__with_together__": withTogether}
	}
	if withNested, ok := raw["with_nested"]; ok {
		handler.Loop = map[string]interface{}{"__with_nested__": withNested}
	}

	if when, ok := raw["when"]; ok {
		handler.When = when
	}
	if delegateTo, ok := raw["delegate_to"].(string); ok {
		handler.DelegateTo = delegateTo
	}

	handler.Tags = extractStringList(raw["tags"])

	if become, ok := raw["become"]; ok {
		b := extractBool(become)
		handler.Become = &b
	}
	if becomeUser, ok := raw["become_user"].(string); ok {
		handler.BecomeUser = becomeUser
	}
	if becomeMethod, ok := raw["become_method"].(string); ok {
		handler.BecomeMethod = becomeMethod
	}
	if becomePass, ok := raw["become_pass"]; ok {
		handler.BecomePass = fmt.Sprintf("%v", becomePass)
	}
	if becomePasswd, ok := raw["become_password"]; ok && handler.BecomePass == "" {
		handler.BecomePass = fmt.Sprintf("%v", becomePasswd)
	}
	if noLog, ok := raw["no_log"]; ok {
		handler.NoLog = extractBool(noLog)
	}
}
