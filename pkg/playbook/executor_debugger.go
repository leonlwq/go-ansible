package playbook

import (
	"bufio"
	"crypto/md5"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"go-ansible/pkg/inventory"
)

type DebuggerAction int

const (
	DebuggerContinue DebuggerAction = iota
	DebuggerRedo
	DebuggerQuit
)

func (e *Executor) promptPlayVars(play *Play, playVars map[string]interface{}) error {
	if play == nil || len(play.VarsPrompt) == 0 {
		return nil
	}

	w := e.getStdout()
	r := bufio.NewReader(e.getStdin())

	for _, item := range play.VarsPrompt {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}

		// 检查 extra_vars 优先级覆盖：命令行 -e 已传入时跳过 prompt
		e.mu.RLock()
		_, inExtra := e.extraVars[name]
		e.mu.RUnlock()
		if inExtra {
			continue
		}

		promptText := item.Prompt
		if promptText == "" {
			promptText = name
		}

		defaultStr := ""
		if item.Default != nil {
			defaultStr = fmt.Sprintf("%v", item.Default)
		}

		displayPrompt := promptText
		if defaultStr != "" {
			displayPrompt = fmt.Sprintf("%s [%s]: ", promptText, defaultStr)
		} else {
			displayPrompt = fmt.Sprintf("%s: ", promptText)
		}

		var finalVal string
		maxAttempts := 3
		if !item.Confirm {
			maxAttempts = 1
		}

		for attempt := 0; attempt < maxAttempts; attempt++ {
			val, err := e.readPromptInput(r, w, displayPrompt, item.Private)
			if err != nil {
				return fmt.Errorf("read prompt for '%s': %w", name, err)
			}
			if val == "" && defaultStr != "" {
				val = defaultStr
			}

			if item.Confirm {
				confirmPrompt := fmt.Sprintf("confirm %s", displayPrompt)
				confirmVal, err := e.readPromptInput(r, w, confirmPrompt, item.Private)
				if err != nil {
					return fmt.Errorf("read confirm prompt for '%s': %w", name, err)
				}
				if confirmVal == "" && defaultStr != "" {
					confirmVal = defaultStr
				}
				if val != confirmVal {
					fmt.Fprintln(w, "Values do not match. Please try again.")
					if attempt == maxAttempts-1 {
						return fmt.Errorf("confirmation failed for '%s': values did not match", name)
					}
					continue
				}
			}

			finalVal = val
			break
		}

		if item.Encrypt != "" {
			finalVal = encryptPromptValue(finalVal, item.Encrypt)
		}

		playVars[name] = finalVal
		if play.Vars == nil {
			play.Vars = make(map[string]interface{})
		}
		play.Vars[name] = finalVal
	}
	e.invalidateHostVarsCache()
	return nil
}

func (e *Executor) readPromptInput(r *bufio.Reader, w io.Writer, prompt string, private bool) (string, error) {
	fmt.Fprint(w, prompt)
	// 如果是 private 模式且是真正控制台标准输入 (无注入测试 Reader)
	if private && e.Stdin == nil && term.IsTerminal(int(os.Stdin.Fd())) {
		bytePass, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(w)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(bytePass), "\r\n"), nil
	}

	line, err := r.ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func encryptPromptValue(val, enc string) string {
	enc = strings.ToLower(strings.TrimSpace(enc))
	switch enc {
	case "sha256", "sha256_crypt":
		hash := sha256.Sum256([]byte(val))
		return hex.EncodeToString(hash[:])
	case "sha512", "sha512_crypt":
		hash := sha512.Sum512([]byte(val))
		return hex.EncodeToString(hash[:])
	case "md5", "md5_crypt":
		hash := md5.Sum([]byte(val))
		return hex.EncodeToString(hash[:])
	default:
		return val
	}
}

func (e *Executor) shouldTriggerDebugger(task *Task, result *TaskResult) bool {
	if result == nil {
		return false
	}
	mode := ""
	if task != nil && task.Debugger != "" {
		mode = strings.ToLower(strings.TrimSpace(task.Debugger))
	} else if e.currentPlayDebugger != "" {
		mode = strings.ToLower(strings.TrimSpace(e.currentPlayDebugger))
	} else {
		envDebug := strings.ToLower(strings.TrimSpace(os.Getenv("ANSIBLE_ENABLE_TASK_DEBUGGER")))
		if envDebug == "true" || envDebug == "1" || envDebug == "yes" {
			mode = "on_failed"
		}
	}

	if mode == "" || mode == "never" || mode == "false" || mode == "no" {
		return false
	}

	switch mode {
	case "always":
		return true
	case "on_failed":
		return result.Failed
	case "on_unreachable":
		return result.Unreachable
	case "on_skipped":
		return result.Skipped
	default:
		return false
	}
}

func (e *Executor) runDebugger(host *inventory.Host, task *Task, result *TaskResult, allVars map[string]interface{}) DebuggerAction {
	e.debuggerMu.Lock()
	defer e.debuggerMu.Unlock()

	w := e.getStdout()
	r := bufio.NewReader(e.getStdin())

	hostName := "localhost"
	if host != nil {
		hostName = host.Name
	}
	taskName := ""
	if task != nil {
		taskName = task.Name
	}

	fmt.Fprintf(w, "\n[Debugger] Host: %s, Task: %s\n", hostName, taskName)
	if result != nil && result.Failed {
		fmt.Fprintf(w, "[Debugger] Task failed: %s\n", result.Message)
	}

	for {
		fmt.Fprintf(w, "[%s] TASK: %s (debug)> ", hostName, taskName)
		line, err := r.ReadString('\n')
		if err != nil && len(line) == 0 {
			return DebuggerContinue
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, " ", 2)
		cmd := strings.ToLower(parts[0])
		var arg string
		if len(parts) > 1 {
			arg = strings.TrimSpace(parts[1])
		}

		switch cmd {
		case "c", "continue":
			return DebuggerContinue
		case "r", "redo":
			fmt.Fprintln(w, "Re-running task...")
			return DebuggerRedo
		case "q", "quit":
			fmt.Fprintln(w, "Quitting playbook...")
			return DebuggerQuit
		case "h", "help":
			fmt.Fprintln(w, "Commands:")
			fmt.Fprintln(w, "  p/print <expr>       Print result or variable (e.g. result, result.rc, task.args, task_vars)")
			fmt.Fprintln(w, "  u/update_task <k=v>  Update task arguments (e.g. cmd=\"echo 123\", task.args['msg']='hi')")
			fmt.Fprintln(w, "  r/redo               Re-run current task")
			fmt.Fprintln(w, "  c/continue           Continue playbook execution")
			fmt.Fprintln(w, "  q/quit               Abort playbook execution")
		case "p", "print":
			if arg == "" {
				fmt.Fprintln(w, "Usage: p <expr>")
				continue
			}
			e.printDebuggerExpr(w, arg, task, result, allVars)
		case "u", "update_task":
			if arg == "" {
				fmt.Fprintln(w, "Usage: u <param_name=value>")
				continue
			}
			key, val, err := parseUpdateTaskInput(arg)
			if err != nil {
				fmt.Fprintf(w, "Error parsing update: %v\n", err)
				continue
			}
			if task.Params == nil {
				task.Params = make(map[string]interface{})
			}
			task.Params[key] = val
			if key == "cmd" || key == "_raw_params" {
				task.Params["cmd"] = val
				task.Params["_raw_params"] = val
			}
			fmt.Fprintf(w, "Task args updated: %s = %v\n", key, val)
		default:
			fmt.Fprintf(w, "Unknown command '%s'. Type 'help' for available commands.\n", cmd)
		}
	}
}

func parseUpdateTaskInput(input string) (string, string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", fmt.Errorf("empty input")
	}

	idx := strings.Index(input, "=")
	if idx == -1 {
		return "", "", fmt.Errorf("missing '=' in expression")
	}

	left := strings.TrimSpace(input[:idx])
	right := strings.TrimSpace(input[idx+1:])

	// 去除右侧外层单/双引号
	if (strings.HasPrefix(right, "\"") && strings.HasSuffix(right, "\"")) ||
		(strings.HasPrefix(right, "'") && strings.HasSuffix(right, "'")) {
		if len(right) >= 2 {
			right = right[1 : len(right)-1]
		}
	}

	// 处理左侧 key，如 task.args['cmd'] 或 args['cmd'] 或 task.args["cmd"]
	if start := strings.Index(left, "["); start != -1 {
		if end := strings.Index(left, "]"); end > start {
			key := left[start+1 : end]
			key = strings.Trim(key, "\"' ")
			return key, right, nil
		}
	}

	// 处理左侧带点语法，如 task.args.cmd 或 args.cmd
	if strings.Contains(left, ".") {
		parts := strings.Split(left, ".")
		key := strings.TrimSpace(parts[len(parts)-1])
		return key, right, nil
	}

	return left, right, nil
}

func (e *Executor) printDebuggerExpr(w io.Writer, expr string, task *Task, result *TaskResult, allVars map[string]interface{}) {
	expr = strings.TrimSpace(expr)
	switch expr {
	case "result", "r":
		m := map[string]interface{}{
			"failed":      false,
			"changed":     false,
			"skipped":     false,
			"unreachable": false,
			"rc":          0,
			"stdout":      "",
			"stderr":      "",
			"msg":         "",
		}
		if result != nil {
			m["failed"] = result.Failed
			m["changed"] = result.Changed
			m["skipped"] = result.Skipped
			m["unreachable"] = result.Unreachable
			m["rc"] = result.ExitCode
			m["stdout"] = result.Stdout
			m["stderr"] = result.Stderr
			m["msg"] = result.Message
			if len(result.Data) > 0 {
				m["data"] = result.Data
			}
		}
		b, _ := json.MarshalIndent(m, "", "  ")
		fmt.Fprintln(w, string(b))
		return
	case "result.rc", "result.exit_code", "rc":
		if result != nil {
			fmt.Fprintln(w, result.ExitCode)
		} else {
			fmt.Fprintln(w, 0)
		}
		return
	case "result.stdout", "stdout":
		if result != nil {
			fmt.Fprintln(w, result.Stdout)
		}
		return
	case "result.stderr", "stderr":
		if result != nil {
			fmt.Fprintln(w, result.Stderr)
		}
		return
	case "result.msg", "result.message", "msg":
		if result != nil {
			fmt.Fprintln(w, result.Message)
		}
		return
	case "result.failed", "failed":
		if result != nil {
			fmt.Fprintln(w, result.Failed)
		} else {
			fmt.Fprintln(w, false)
		}
		return
	case "result.changed", "changed":
		if result != nil {
			fmt.Fprintln(w, result.Changed)
		} else {
			fmt.Fprintln(w, false)
		}
		return
	case "result.skipped", "skipped":
		if result != nil {
			fmt.Fprintln(w, result.Skipped)
		} else {
			fmt.Fprintln(w, false)
		}
		return
	case "task.args", "args", "task.params":
		if task != nil && task.Params != nil {
			b, _ := json.MarshalIndent(task.Params, "", "  ")
			fmt.Fprintln(w, string(b))
		} else {
			fmt.Fprintln(w, "{}")
		}
		return
	case "task_vars", "vars":
		b, _ := json.MarshalIndent(allVars, "", "  ")
		fmt.Fprintln(w, string(b))
		return
	}

	if strings.HasPrefix(expr, "task.args.") || strings.HasPrefix(expr, "args.") {
		parts := strings.SplitN(expr, ".", 2)
		if task != nil && task.Params != nil {
			if v, ok := task.Params[parts[1]]; ok {
				fmt.Fprintln(w, v)
				return
			}
		}
	}

	if v, ok := allVars[expr]; ok {
		switch tv := v.(type) {
		case string:
			fmt.Fprintln(w, tv)
		default:
			b, err := json.Marshal(v)
			if err == nil {
				fmt.Fprintln(w, string(b))
			} else {
				fmt.Fprintf(w, "%v\n", v)
			}
		}
		return
	}

	if strings.HasPrefix(expr, "result.data.") {
		subKey := strings.TrimPrefix(expr, "result.data.")
		if result != nil && result.Data != nil {
			if v, ok := result.Data[subKey]; ok {
				fmt.Fprintln(w, v)
				return
			}
		}
	}

	fmt.Fprintln(w, "<undefined>")
}
