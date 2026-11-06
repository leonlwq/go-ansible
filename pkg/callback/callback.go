package callback

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"golang.org/x/term"

	"go-ansible/pkg/playbook"
	"go-ansible/pkg/util"
)

type Renderer interface {
	Render(io.Writer, *playbook.PlaybookResult, string) error
}

func New(name string) (Renderer, error) {
	return NewWithCallbacks(name, nil)
}

func NewWithCallbacks(name string, callbacks []string) (Renderer, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "default":
		r := &DefaultRenderer{}
		for _, cb := range callbacks {
			switch strings.ToLower(strings.TrimSpace(cb)) {
			case "profile_tasks":
				r.ProfileTasks = true
			case "timer":
				r.Timer = true
			}
		}
		return r, nil
	case "profile_tasks":
		return &DefaultRenderer{ProfileTasks: true}, nil
	case "timer":
		return &DefaultRenderer{Timer: true}, nil
	case "json":
		return &JSONRenderer{}, nil
	default:
		return nil, fmt.Errorf("unsupported stdout callback: %s", name)
	}
}

type DefaultRenderer struct {
	ProfileTasks bool
	Timer        bool
}

func (r *DefaultRenderer) Render(w io.Writer, result *playbook.PlaybookResult, tags string) error {
	if result == nil {
		return nil
	}

	cp := getColorPalette(w)

	for _, play := range result.Plays {
		playName := play.Name
		fmt.Fprintf(w, "\nPLAY [%s] *************************************************************************\n", playName)

		for _, hostName := range sortedHostNames(play.Hosts) {
			hostResult := play.Hosts[hostName]
			if hostResult == nil {
				continue
			}
			for _, taskResult := range hostResult.GetTasks() {
				taskName := taskResult.Name
				if taskName == "" {
					taskName = "task"
				}

				fmt.Fprintf(w, "\nTASK [%s] *************************************************************************\n", taskName)

				if taskResult.Skipped {
					fmt.Fprintf(w, "%sskipping: [%s]%s\n", cp.Cyan, hostName, cp.Reset)
					continue
				}

				status := "ok"
				if taskResult.Unreachable {
					if taskResult.Ignored {
						status = "ignored"
					} else {
						status = "unreachable"
					}
				} else if taskResult.Failed {
					status = "fatal"
				} else if taskResult.Changed {
					status = "changed"
				}

				switch status {
				case "unreachable":
					fmt.Fprintf(w, "%sfatal: [%s]: UNREACHABLE!%s", cp.Red, hostName, cp.Reset)
				case "ignored":
					fmt.Fprintf(w, "%signoring unreachable: [%s]%s", cp.Cyan, hostName, cp.Reset)
				case "fatal":
					fmt.Fprintf(w, "%sfatal: [%s]: FAILED!%s", cp.Red, hostName, cp.Reset)
				case "changed":
					fmt.Fprintf(w, "%schanged: [%s]%s", cp.Yellow, hostName, cp.Reset)
				default:
					fmt.Fprintf(w, "%sok: [%s]%s", cp.Green, hostName, cp.Reset)
				}

				if taskResult.Item != nil {
					if taskResult.NoLog {
						fmt.Fprintf(w, " => (item=[VALUE_SPECIFIED_IN_NO_LOG_TASK])")
					} else {
						fmt.Fprintf(w, " => (item=%v)", formatItem(taskResult.Item))
					}
				}
				fmt.Fprintln(w)

				if taskResult.NoLog {
					if taskResult.Failed {
						fmt.Fprintf(w, "  fatal: [%s]: FAILED! => {\"censored\": \"the output has been hidden due to the fact that 'no_log: true' was specified for this result\"}\n", hostName)
					}
					continue
				}

				if taskResult.Message != "" {
					fmt.Fprintf(w, "  msg: %s\n", taskResult.Message)
				}
				if taskResult.Stdout != "" {
					fmt.Fprintf(w, "  stdout: %s\n", taskResult.Stdout)
				}
				if taskResult.Stderr != "" {
					fmt.Fprintf(w, "  stderr: %s\n", taskResult.Stderr)
				}
				if taskResult.Diff != "" {
					coloredDiff := formatColoredDiff(taskResult.Diff, cp)
					fmt.Fprintf(w, "  diff:\n%s\n", util.IndentBlock(coloredDiff, "    "))
				}
			}
		}
	}

	type recapStats struct {
		ok          int
		changed     int
		unreachable int
		failed      int
		skipped     int
		rescued     int
		ignored     int
	}

	hostAgg := make(map[string]*recapStats)
	for _, play := range result.Plays {
		if play == nil {
			continue
		}
		for hostName, hostResult := range play.Hosts {
			if hostResult == nil {
				continue
			}
			st, ok := hostAgg[hostName]
			if !ok {
				st = &recapStats{}
				hostAgg[hostName] = st
			}
			okCount, changedCount, failedCount, skippedCount, unreachableCount, rescuedCount, ignoredCount := summarizeHost(hostResult)
			st.ok += okCount
			st.changed += changedCount
			st.unreachable += unreachableCount
			st.failed += failedCount
			st.skipped += skippedCount
			st.rescued += rescuedCount
			st.ignored += ignoredCount
		}
	}

	names := make([]string, 0, len(hostAgg))
	for h := range hostAgg {
		names = append(names, h)
	}
	sort.Strings(names)

	fmt.Fprintf(w, "\nPLAY RECAP *****************************************************************************\n")
	for _, hostName := range names {
		st := hostAgg[hostName]
		color := cp.Green
		if st.failed > 0 || (st.unreachable > 0 && st.ignored == 0) {
			color = cp.Red
		} else if st.changed > 0 {
			color = cp.Yellow
		} else if st.unreachable > 0 {
			color = cp.Cyan
		}
		fmt.Fprintf(
			w,
			"%s%-30s%s: ok=%-3d changed=%-3d unreachable=%-3d failed=%-3d skipped=%-3d rescued=%-3d ignored=%-3d\n",
			color,
			hostName,
			cp.Reset,
			st.ok,
			st.changed,
			st.unreachable,
			st.failed,
			st.skipped,
			st.rescued,
			st.ignored,
		)
	}

	if tags != "" {
		fmt.Fprintf(w, "\nTags: %s\n", tags)
	}

	if r.ProfileTasks && result != nil {
		r.renderProfileTasks(w, result)
	}
	if r.Timer && result != nil {
		r.renderTimer(w, result)
	}

	return nil
}

type taskProfile struct {
	name     string
	duration time.Duration
}

func (r *DefaultRenderer) renderProfileTasks(w io.Writer, result *playbook.PlaybookResult) {
	taskMap := make(map[string]time.Duration)
	var taskOrder []string

	for _, play := range result.Plays {
		for _, hostResult := range play.Hosts {
			if hostResult == nil {
				continue
			}
			for _, tr := range hostResult.GetTasks() {
				taskName := tr.Name
				if taskName == "" {
					taskName = "task"
				}
				displayKey := taskName
				if play.Name != "" {
					displayKey = play.Name + " : " + taskName
				}
				var dur time.Duration
				if !tr.Start.IsZero() && !tr.End.IsZero() && tr.End.After(tr.Start) {
					dur = tr.End.Sub(tr.Start)
				}
				if existing, ok := taskMap[displayKey]; ok {
					if dur > existing {
						taskMap[displayKey] = dur
					}
				} else {
					taskMap[displayKey] = dur
					taskOrder = append(taskOrder, displayKey)
				}
			}
		}
	}

	if len(taskOrder) == 0 {
		return
	}

	profiles := make([]taskProfile, 0, len(taskMap))
	for name, dur := range taskMap {
		profiles = append(profiles, taskProfile{name: name, duration: dur})
	}

	sort.Slice(profiles, func(i, j int) bool {
		if profiles[i].duration == profiles[j].duration {
			return profiles[i].name < profiles[j].name
		}
		return profiles[i].duration > profiles[j].duration
	})

	limit := 10
	if len(profiles) < limit {
		limit = len(profiles)
	}

	fmt.Fprintf(w, "\n===============================================================================\n")
	for i := 0; i < limit; i++ {
		p := profiles[i]
		name := p.name
		if len(name) > 60 {
			name = name[:57] + "..."
		}
		dashes := 70 - len(name)
		if dashes < 2 {
			dashes = 2
		}
		durStr := fmt.Sprintf("%6.2fs", p.duration.Seconds())
		if p.duration > 0 && p.duration < 10*time.Millisecond {
			durStr = fmt.Sprintf("%6.3fs", p.duration.Seconds())
		}
		fmt.Fprintf(w, "%s %s %s\n", name, strings.Repeat("-", dashes), durStr)
	}
}

func (r *DefaultRenderer) renderTimer(w io.Writer, result *playbook.PlaybookResult) {
	var totalDuration time.Duration
	if !result.Start.IsZero() && !result.End.IsZero() && result.End.After(result.Start) {
		totalDuration = result.End.Sub(result.Start)
	} else {
		var earliest, latest time.Time
		for _, play := range result.Plays {
			for _, hostResult := range play.Hosts {
				if hostResult == nil {
					continue
				}
				for _, tr := range hostResult.GetTasks() {
					if !tr.Start.IsZero() && (earliest.IsZero() || tr.Start.Before(earliest)) {
						earliest = tr.Start
					}
					if !tr.End.IsZero() && (latest.IsZero() || tr.End.After(latest)) {
						latest = tr.End
					}
				}
			}
		}
		if !earliest.IsZero() && !latest.IsZero() && latest.After(earliest) {
			totalDuration = latest.Sub(earliest)
		}
	}

	days := int(totalDuration.Hours()) / 24
	hours := int(totalDuration.Hours()) % 24
	minutes := int(totalDuration.Minutes()) % 60
	seconds := int(totalDuration.Seconds()) % 60
	millis := totalDuration.Milliseconds() % 1000
	if totalDuration > 0 && totalDuration < time.Second {
		fmt.Fprintf(w, "\nPlaybook run took %d days, %d hours, %d minutes, %d seconds (%d ms)\n", days, hours, minutes, seconds, millis)
	} else {
		fmt.Fprintf(w, "\nPlaybook run took %d days, %d hours, %d minutes, %d seconds\n", days, hours, minutes, seconds)
	}
}

func formatColoredDiff(diff string, cp ColorPalette) string {
	if diff == "" {
		return ""
	}
	lines := strings.Split(diff, "\n")
	var coloredLines []string
	for _, l := range lines {
		if strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++") {
			coloredLines = append(coloredLines, cp.Green+l+cp.Reset)
		} else if strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "---") {
			coloredLines = append(coloredLines, cp.Red+l+cp.Reset)
		} else if strings.HasPrefix(l, "@@") {
			coloredLines = append(coloredLines, cp.Cyan+l+cp.Reset)
		} else {
			coloredLines = append(coloredLines, l)
		}
	}
	return strings.Join(coloredLines, "\n")
}

type JSONRenderer struct{}

func (r *JSONRenderer) Render(w io.Writer, result *playbook.PlaybookResult, tags string) error {
	type jsonTaskResult struct {
		Name        string      `json:"name"`
		Host        string      `json:"host,omitempty"`
		Changed     bool        `json:"changed"`
		Failed      bool        `json:"failed"`
		Skipped     bool        `json:"skipped"`
		Unreachable bool        `json:"unreachable,omitempty"`
		Ignored     bool        `json:"ignored,omitempty"`
		Stdout      string      `json:"stdout,omitempty"`
		Stderr      string      `json:"stderr,omitempty"`
		ExitCode    int         `json:"exit_code,omitempty"`
		Message     string      `json:"message,omitempty"`
		Diff        string      `json:"diff,omitempty"`
		Data        interface{} `json:"data,omitempty"`
		Item        interface{} `json:"item,omitempty"`
		Start       string      `json:"start,omitempty"`
		End         string      `json:"end,omitempty"`
	}
	type jsonHostResult struct {
		Tasks []*jsonTaskResult `json:"tasks"`
	}
	type jsonPlayResult struct {
		Name  string                     `json:"name"`
		Hosts map[string]*jsonHostResult `json:"hosts"`
	}
	type jsonHostStats struct {
		OK          int `json:"ok"`
		Changed     int `json:"changed"`
		Unreachable int `json:"unreachable"`
		Failures    int `json:"failures"`
		Skipped     int `json:"skipped"`
		Rescued     int `json:"rescued"`
		Ignored     int `json:"ignored"`
	}

	payload := struct {
		Plays []*jsonPlayResult         `json:"plays"`
		Stats map[string]*jsonHostStats `json:"stats,omitempty"`
		Tags  []string                  `json:"tags,omitempty"`
	}{
		Plays: nil,
		Stats: make(map[string]*jsonHostStats),
	}
	if result != nil {
		payload.Plays = make([]*jsonPlayResult, 0, len(result.Plays))
		for _, play := range result.Plays {
			jsonPlay := &jsonPlayResult{
				Name:  play.Name,
				Hosts: make(map[string]*jsonHostResult, len(play.Hosts)),
			}
			for hostName, hostResult := range play.Hosts {
				var tasksSnapshot []*playbook.TaskResult
				if hostResult != nil {
					tasksSnapshot = hostResult.GetTasks()
				}
				jsonHost := &jsonHostResult{
					Tasks: make([]*jsonTaskResult, 0, len(tasksSnapshot)),
				}
				for _, taskResult := range tasksSnapshot {
					jsonTask := &jsonTaskResult{
						Name:        taskResult.Name,
						Host:        taskResult.Host,
						Changed:     taskResult.Changed,
						Failed:      taskResult.Failed,
						Skipped:     taskResult.Skipped,
						Unreachable: taskResult.Unreachable,
						Ignored:     taskResult.Ignored,
						Stdout:      taskResult.Stdout,
						Stderr:      taskResult.Stderr,
						ExitCode:    taskResult.ExitCode,
						Message:     taskResult.Message,
						Diff:        taskResult.Diff,
						Data:        taskResult.Data,
						Item:        taskResult.Item,
					}
					if taskResult.NoLog {
						jsonTask.Stdout = "[VALUE_SPECIFIED_IN_NO_LOG_TASK]"
						jsonTask.Stderr = ""
						jsonTask.Message = "[VALUE_SPECIFIED_IN_NO_LOG_TASK]"
						jsonTask.Diff = ""
						jsonTask.Data = map[string]interface{}{"censored": "the output has been hidden due to the fact that 'no_log: true' was specified for this result"}
						if jsonTask.Item != nil {
							jsonTask.Item = "[VALUE_SPECIFIED_IN_NO_LOG_TASK]"
						}
					}
					if !taskResult.Start.IsZero() {
						jsonTask.Start = taskResult.Start.Format("2006-01-02T15:04:05Z07:00")
					}
					if !taskResult.End.IsZero() {
						jsonTask.End = taskResult.End.Format("2006-01-02T15:04:05Z07:00")
					}
					jsonHost.Tasks = append(jsonHost.Tasks, jsonTask)
				}
				jsonPlay.Hosts[hostName] = jsonHost

				st, ok := payload.Stats[hostName]
				if !ok {
					st = &jsonHostStats{}
					payload.Stats[hostName] = st
				}
				okCount, changedCount, failedCount, skippedCount, unreachableCount, rescuedCount, ignoredCount := summarizeHost(hostResult)
				st.OK += okCount
				st.Changed += changedCount
				st.Unreachable += unreachableCount
				st.Failures += failedCount
				st.Skipped += skippedCount
				st.Rescued += rescuedCount
				st.Ignored += ignoredCount
			}
			payload.Plays = append(payload.Plays, jsonPlay)
		}
	}
	if tags != "" {
		payload.Tags = splitTags(tags)
	}

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(payload)
}

func HasFailures(result *playbook.PlaybookResult) bool {
	if result == nil {
		return false
	}
	for _, play := range result.Plays {
		for _, hostResult := range play.Hosts {
			if hostResult == nil {
				continue
			}
			for _, taskResult := range hostResult.GetTasks() {
				if (taskResult.Failed && !taskResult.Rescued) || (taskResult.Unreachable && !taskResult.Ignored) {
					return true
				}
			}
		}
	}
	return false
}

func summarizeHost(hostResult *playbook.HostResult) (okCount, changedCount, failedCount, skippedCount, unreachableCount, rescuedCount, ignoredCount int) {
	if hostResult == nil {
		return 0, 0, 0, 0, 0, 0, 0
	}
	for _, taskResult := range hostResult.GetTasks() {
		if taskResult.Unreachable {
			unreachableCount++
		}
		if taskResult.Ignored {
			ignoredCount++
		}
		switch {
		case taskResult.Failed:
			if taskResult.Rescued {
				rescuedCount++
			} else {
				failedCount++
			}
		case taskResult.Changed:
			changedCount++
		case taskResult.Skipped:
			skippedCount++
		default:
			okCount++
		}
	}
	return okCount, changedCount, failedCount, skippedCount, unreachableCount, rescuedCount, ignoredCount
}

func sortedHostNames(hosts map[string]*playbook.HostResult) []string {
	names := make([]string, 0, len(hosts))
	for name := range hosts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func splitTags(tags string) []string {
	parts := strings.Split(tags, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func formatItem(item interface{}) string {
	switch v := item.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, fmt.Sprintf("'%s': '%v'", key, v[key]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case map[interface{}]interface{}:
		keys := make([]string, 0, len(v))
		values := make(map[string]interface{}, len(v))
		for key, val := range v {
			keyStr := fmt.Sprintf("%v", key)
			keys = append(keys, keyStr)
			values[keyStr] = val
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, fmt.Sprintf("'%v': '%v'", key, values[key]))
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

// ColorPalette 定义终端色彩 ANSI 转义序列
type ColorPalette struct {
	Reset   string
	Red     string
	Green   string
	Yellow  string
	Blue    string
	Magenta string
	Cyan    string
	White   string
}

// getColorPalette 根据输出流特性与环境变量获取色彩配置
func getColorPalette(w io.Writer) ColorPalette {
	if !shouldUseColor(w) {
		return ColorPalette{}
	}
	return ColorPalette{
		Reset:   "\033[0m",
		Red:     "\033[31m",
		Green:   "\033[32m",
		Yellow:  "\033[33m",
		Blue:    "\033[34m",
		Magenta: "\033[35m",
		Cyan:    "\033[36m",
		White:   "\033[37m",
	}
}

// shouldUseColor 判定是否启用 ANSI 色彩输出
func shouldUseColor(w io.Writer) bool {
	// 1. NO_COLOR 规范 (https://no-color.org) 或 ANSIBLE_NOCOLOR
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if v := strings.ToLower(os.Getenv("ANSIBLE_NOCOLOR")); v == "1" || v == "true" || v == "yes" {
		return false
	}

	// 2. ANSIBLE_FORCE_COLOR
	if v := strings.ToLower(os.Getenv("ANSIBLE_FORCE_COLOR")); v == "1" || v == "true" || v == "yes" {
		return true
	}

	// 3. TTY 终端探测
	if f, ok := w.(*os.File); ok {
		return term.IsTerminal(int(f.Fd()))
	}

	return false
}
