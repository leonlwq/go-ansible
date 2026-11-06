package playbook

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/module"
	"go-ansible/pkg/ssh"
	"go-ansible/pkg/util"
)

func (e *Executor) executeDebugModule(params map[string]interface{}, task *Task, vars map[string]interface{}) *module.Result {
	if vVal, ok := params["verbosity"]; ok {
		requiredVerbosity := 0
		switch v := vVal.(type) {
		case int:
			requiredVerbosity = v
		case float64:
			requiredVerbosity = int(v)
		case string:
			if iv, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				requiredVerbosity = iv
			}
		}
		if e.verbosityLevel < requiredVerbosity {
			return &module.Result{
				Changed: false,
				Message: fmt.Sprintf("debug task skipped: verbosity %d required, current is %d", requiredVerbosity, e.verbosityLevel),
				Stdout:  "",
				Data: map[string]interface{}{
					"skipped":        true,
					"skipped_reason": fmt.Sprintf("Verbosity threshold not met: %d > %d", requiredVerbosity, e.verbosityLevel),
				},
			}
		}
	}

	if msg, ok := params["msg"]; ok {
		return &module.Result{
			Changed: false,
			Message: fmt.Sprintf("%v", msg),
			Stdout:  fmt.Sprintf("%v", msg),
		}
	}

	rawVarName := ""
	if task != nil {
		rawVarName = GetString(task.Params, "var", "")
	}
	if rawVarName == "" {
		rawVarName = GetString(params, "var", "")
	}
	if rawVarName == "" {
		return &module.Result{
			Changed: false,
			Message: "debug module requires msg or var",
		}
	}

	value, ok := e.lookupVarExpr(rawVarName, vars)
	if !ok {
		return &module.Result{
			Changed: false,
			Message: fmt.Sprintf("%s: VARIABLE IS NOT DEFINED", rawVarName),
			Stdout:  fmt.Sprintf("%s: VARIABLE IS NOT DEFINED", rawVarName),
		}
	}

	output := fmt.Sprintf("%s: %v", rawVarName, value)
	return &module.Result{
		Changed: false,
		Message: output,
		Stdout:  output,
	}
}

func (e *Executor) executeFailModule(params map[string]interface{}) *module.Result {
	message := GetString(params, "msg", "")
	if message == "" {
		message = GetString(params, "message", "Failed as requested from task")
	}
	return &module.Result{
		Changed:  false,
		Failed:   true,
		Message:  message,
		Stderr:   message,
		ExitCode: 1,
	}
}

func (e *Executor) executeAssertModule(params map[string]interface{}, vars map[string]interface{}) *module.Result {
	expressions := assertExpressions(params["that"])
	if len(expressions) == 0 {
		return &module.Result{
			Changed:  false,
			Failed:   true,
			Message:  "assert requires that",
			ExitCode: 1,
		}
	}

	failed := make([]string, 0)
	for _, expr := range expressions {
		if !e.evaluateWhen(expr, vars) {
			failed = append(failed, expr)
		}
	}

	quiet := false
	if q, ok := params["quiet"].(bool); ok {
		quiet = q
	}

	if len(failed) > 0 {
		rawMsg := GetString(params, "fail_msg", "")
		if rawMsg == "" {
			rawMsg = GetString(params, "msg", "")
		}
		if rawMsg == "" {
			rawMsg = fmt.Sprintf("assertion failed: %s", strings.Join(failed, ", "))
		}
		message := e.resolveString(rawMsg, vars)
		return &module.Result{
			Changed:  false,
			Failed:   true,
			Message:  message,
			Stderr:   message,
			ExitCode: 1,
			Data: map[string]interface{}{
				"assertion":    failed[0],
				"evaluated_to": false,
			},
		}
	}

	rawSuccess := GetString(params, "success_msg", "All assertions passed")
	message := e.resolveString(rawSuccess, vars)
	stdout := message
	if quiet {
		stdout = ""
	}

	return &module.Result{
		Changed: false,
		Message: message,
		Stdout:  stdout,
	}
}

func assertExpressions(value interface{}) []string {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		return []string{v}
	case []string:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if strings.TrimSpace(item) != "" {
				result = append(result, item)
			}
		}
		return result
	case []interface{}:
		result := make([]string, 0, len(v))
		for _, item := range v {
			expr := strings.TrimSpace(fmt.Sprintf("%v", item))
			if expr != "" {
				result = append(result, expr)
			}
		}
		return result
	default:
		expr := strings.TrimSpace(fmt.Sprintf("%v", v))
		if expr == "" {
			return nil
		}
		return []string{expr}
	}
}

func (e *Executor) executeSetFactModule(host *inventory.Host, params map[string]interface{}) *module.Result {
	if host == nil {
		return &module.Result{
			Changed: false,
			Message: "set_fact requires host context",
			Failed:  true,
		}
	}

	cacheable := false
	filteredParams := make(map[string]interface{}, len(params))
	for k, v := range params {
		if k == "cacheable" {
			cacheable = util.IsTruthy(v)
		} else {
			filteredParams[k] = v
		}
	}

	e.mergeAnsibleFacts(host.Name, filteredParams)

	if cacheable {
		facts := e.getHostFacts(host.Name)
		e.saveCachedFacts(host.Name, facts)
	}

	return &module.Result{
		Changed: false,
		Message: "facts updated",
	}
}

func (e *Executor) executeAddHostModule(params map[string]interface{}, vars map[string]interface{}) *module.Result {
	rawName := GetString(params, "name", GetString(params, "hostname", ""))
	hostName := strings.TrimSpace(e.resolveString(rawName, vars))
	if hostName == "" {
		return &module.Result{
			Failed:   true,
			Message:  "add_host requires name or hostname",
			ExitCode: 1,
		}
	}

	e.mu.Lock()
	if e.inventory == nil {
		e.inventory = inventory.NewInventory()
	}
	inv := e.inventory
	e.mu.Unlock()

	// 查找或新建主机
	var host *inventory.Host
	if existing, err := inv.GetHost(hostName); err == nil && existing != nil {
		host = existing
	} else {
		host = &inventory.Host{
			Name:      hostName,
			Address:   hostName,
			Port:      22,
			Variables: make(map[string]string),
		}
	}

	if host.Variables == nil {
		host.Variables = make(map[string]string)
	}

	var groupNames []string
	if gVal, ok := params["groups"]; ok {
		groupNames = append(groupNames, parseGroupList(gVal, e, vars)...)
	}
	if gVal, ok := params["group"]; ok {
		groupNames = append(groupNames, parseGroupList(gVal, e, vars)...)
	}

	resolvedVars := make(map[string]string)
	for k, v := range params {
		if k == "name" || k == "hostname" || k == "groups" || k == "group" {
			continue
		}
		resolvedVars[k] = e.resolveString(fmt.Sprintf("%v", v), vars)
	}

	e.mu.Lock()
	if e.facts == nil {
		e.facts = make(map[string]map[string]interface{})
	}
	if e.facts[hostName] == nil {
		e.facts[hostName] = make(map[string]interface{})
	}

	for k, valStr := range resolvedVars {
		host.Variables[k] = valStr
		e.facts[hostName][k] = valStr

		switch k {
		case "ansible_host":
			host.Address = valStr
		case "ansible_port":
			if p, err := strconv.Atoi(valStr); err == nil {
				host.Port = p
			}
		case "ansible_user":
			host.User = valStr
		case "ansible_ssh_pass", "ansible_password":
			host.Password = valStr
		case "ansible_ssh_private_key_file":
			host.PrivateKey = valStr
		case "ansible_connection":
			host.Variables["ansible_connection"] = valStr
		}
	}
	e.mu.Unlock()

	inv.AddHost(host, groupNames...)

	e.invalidateGroupsCache()
	e.invalidateHostVarsCache()
	inv.InvalidateResolvedVarsCache()

	return &module.Result{
		Changed: true,
		Message: fmt.Sprintf("added host %s to inventory", hostName),
	}
}

func (e *Executor) executeGroupByModule(host *inventory.Host, params map[string]interface{}, vars map[string]interface{}) *module.Result {
	if host == nil {
		return &module.Result{
			Failed:   true,
			Message:  "group_by requires host context",
			ExitCode: 1,
		}
	}

	rawKey := GetString(params, "key", "")
	groupName := strings.TrimSpace(e.resolveString(rawKey, vars))
	if groupName == "" {
		return &module.Result{
			Failed:   true,
			Message:  "group_by requires key",
			ExitCode: 1,
		}
	}

	e.mu.Lock()
	if e.inventory == nil {
		e.inventory = inventory.NewInventory()
	}
	inv := e.inventory
	e.mu.Unlock()

	grp := &inventory.Group{
		Name:      groupName,
		Hosts:     []*inventory.Host{host},
		Children:  make([]*inventory.Group, 0),
		Variables: make(map[string]string),
	}

	var parentNames []string
	if parentsVal, ok := params["parents"]; ok {
		parentNames = parseGroupList(parentsVal, e, vars)
	}

	inv.AddGroup(grp, parentNames...)

	e.invalidateGroupsCache()
	e.invalidateHostVarsCache()
	inv.InvalidateResolvedVarsCache()

	return &module.Result{
		Changed: true,
		Message: fmt.Sprintf("host %s added to group %s", host.Name, groupName),
	}
}

func parseGroupList(val interface{}, e *Executor, vars map[string]interface{}) []string {
	var result []string
	switch v := val.(type) {
	case string:
		parts := strings.Split(v, ",")
		for _, p := range parts {
			trimmed := strings.TrimSpace(e.resolveString(p, vars))
			if trimmed != "" {
				result = append(result, trimmed)
			}
		}
	case []interface{}:
		for _, item := range v {
			trimmed := strings.TrimSpace(e.resolveString(fmt.Sprintf("%v", item), vars))
			if trimmed != "" {
				result = append(result, trimmed)
			}
		}
	case []string:
		for _, item := range v {
			trimmed := strings.TrimSpace(e.resolveString(item, vars))
			if trimmed != "" {
				result = append(result, trimmed)
			}
		}
	}
	return result
}

func (e *Executor) executeMetaModule(host *inventory.Host, params map[string]interface{}, vars map[string]interface{}) *module.Result {
	action := GetString(params, "_raw_params", "")
	if action == "" {
		action = GetString(params, "action", "")
	}
	action = strings.TrimSpace(action)

	switch action {
	case "flush_handlers":
		if e.currentResult == nil {
			return &module.Result{
				Changed:  false,
				Failed:   true,
				Message:  "flush_handlers requires active play result",
				ExitCode: 1,
			}
		}
		if err := e.flushHandlers(e.currentResult); err != nil {
			return &module.Result{
				Changed:  false,
				Failed:   true,
				Message:  err.Error(),
				ExitCode: 1,
			}
		}
		return &module.Result{
			Changed: false,
			Message: "handlers flushed",
		}
	case "clear_facts":
		if host != nil {
			e.mu.Lock()
			delete(e.facts, host.Name)
			e.mu.Unlock()
			e.invalidateHostVarsCache(host.Name)
		}
		return &module.Result{
			Changed: false,
			Message: "facts cleared",
		}
	case "clear_host_errors":
		if host != nil && host.Name != "" {
			e.clearHostFailed(host.Name)
		} else {
			e.mu.Lock()
			e.failedHosts = make(map[string]bool)
			e.mu.Unlock()
		}
		return &module.Result{
			Changed: false,
			Message: "host errors cleared",
		}
	case "reset_connection":
		if host != nil && host.Name != "" && e.pool != nil {
			_ = e.pool.Remove(host.Name)
		}
		return &module.Result{
			Changed: false,
			Message: "connection reset",
		}
	case "refresh_inventory":
		if strings.TrimSpace(e.inventoryPath) == "" {
			return &module.Result{
				Changed: false,
				Message: "refresh_inventory requires configured inventory path",
				Failed:  true,
			}
		}
		loader := e.inventoryLoader
		if loader == nil {
			loader = inventory.ParseFile
		}
		reloaded, err := loader(e.inventoryPath)
		if err != nil {
			return &module.Result{
				Changed:  false,
				Failed:   true,
				Message:  fmt.Sprintf("refresh inventory: %v", err),
				ExitCode: 1,
			}
		}
		if reloaded == nil {
			return &module.Result{
				Changed: false,
				Failed:  true,
				Message: "refresh inventory: loader returned nil inventory",
			}
		}
		e.mu.RLock()
		extraVars := make(map[string]interface{}, len(e.extraVars))
		for key, value := range e.extraVars {
			extraVars[key] = value
		}
		e.mu.RUnlock()
		reloaded.ApplyConnectionVars(extraVars)
		e.mu.Lock()
		if e.pool != nil {
			_ = e.pool.Close()
		}
		e.inventory = reloaded
		e.pool = ssh.NewConnectionPool(e.sshConfig)
		e.mu.Unlock()
		e.invalidateGroupsCache()
		e.invalidateHostVarsCache()
		return &module.Result{
			Changed: false,
			Message: "inventory refreshed",
			Stdout:  e.inventoryPath,
		}
	case "end_host":
		if host != nil {
			e.markHostEnded(host.Name)
		}
		return &module.Result{
			Changed: false,
			Message: "ending host",
		}
	case "end_play":
		e.markEndPlay()
		return &module.Result{
			Changed: false,
			Message: "ending play",
		}
	case "noop":
		return &module.Result{
			Changed: false,
			Message: "noop",
		}
	default:
		_ = vars
		return &module.Result{
			Changed:  false,
			Failed:   true,
			Message:  fmt.Sprintf("unsupported meta action: %s", action),
			ExitCode: 1,
		}
	}
}

func (e *Executor) executeIncludeVarsModule(host *inventory.Host, params map[string]interface{}) (*module.Result, bool, error) {
	if host == nil {
		return &module.Result{
			Changed: false,
			Message: "include_vars requires host context",
			Failed:  true,
		}, true, nil
	}

	path := GetString(params, "file", "")
	if path == "" {
		path = GetString(params, "_raw_params", "")
	}
	dir := GetString(params, "dir", "")
	if path == "" && dir == "" {
		return &module.Result{
			Changed: false,
			Message: "include_vars requires file or dir",
			Failed:  true,
		}, true, nil
	}

	name := GetString(params, "name", "")
	ignoreUnknown := util.ToBool(params["ignore_unknown_extensions"])
	loaded := make(map[string]interface{})
	files := []string{}

	if path != "" {
		resolvedPath := e.resolvePath(path)
		vars, err := e.varManager.LoadVarsFile(resolvedPath)
		if err != nil {
			return nil, true, err
		}
		mergeMap(loaded, vars)
		files = append(files, resolvedPath)
	}

	if dir != "" {
		resolvedDir := e.resolvePath(dir)
		dirFiles, err := includeVarsDirFiles(resolvedDir, ignoreUnknown)
		if err != nil {
			return nil, true, err
		}
		for _, file := range dirFiles {
			vars, err := e.varManager.LoadVarsFile(file)
			if err != nil {
				return nil, true, err
			}
			mergeMap(loaded, vars)
			files = append(files, file)
		}
	}

	e.mu.Lock()
	if e.facts[host.Name] == nil {
		e.facts[host.Name] = make(map[string]interface{})
	}
	if name != "" {
		e.facts[host.Name][name] = loaded
	} else {
		for key, value := range loaded {
			e.facts[host.Name][key] = value
		}
	}
	e.mu.Unlock()
	e.invalidateHostVarsCache(host.Name)

	return &module.Result{
		Changed: false,
		Message: fmt.Sprintf("included %d vars file(s)", len(files)),
		Stdout:  strings.Join(files, "\n"),
	}, true, nil
}

func includeVarsDirFiles(dir string, ignoreUnknown bool) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		switch ext {
		case ".yml", ".yaml", ".json":
			files = append(files, filepath.Join(dir, entry.Name()))
		default:
			if !ignoreUnknown {
				return nil, fmt.Errorf("unsupported vars file extension: %s", entry.Name())
			}
		}
	}
	sort.Strings(files)
	return files, nil
}

func mergeMap(dst, src map[string]interface{}) {
	for key, value := range src {
		dst[key] = value
	}
}

func (e *Executor) resolveLookupExpr(expr string, vars map[string]interface{}) (interface{}, bool) {
	expr = strings.TrimSpace(expr)
	if !strings.HasPrefix(expr, "lookup(") || !strings.HasSuffix(expr, ")") {
		return nil, false
	}

	args := splitFilterArgs(expr[len("lookup(") : len(expr)-1])
	if len(args) < 2 {
		return nil, false
	}

	name := fmt.Sprintf("%v", parseFilterLiteral(args[0]))
	key := fmt.Sprintf("%v", parseFilterLiteral(args[1]))
	if strings.Contains(key, "{{") {
		key = e.resolveString(key, vars)
	}

	var extraArgs []interface{}
	for _, arg := range args[2:] {
		val := parseFilterLiteral(arg)
		extraArgs = append(extraArgs, val)
	}

	allArgs := append([]interface{}{key}, extraArgs...)
	val, err := module.ExecuteLookupWithVars(e.baseDir, name, vars, allArgs...)
	if err != nil {
		return nil, false
	}
	return val, true
}
