package callback

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"go-ansible/pkg/playbook"
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

	for _, play := range result.Plays {
		playName := play.Name
		fmt.Fprintf(w, "\nPLAY [%s] *************************************************************************\n", playName)

		for _, hostName := range sortedHostNames(play.Hosts) {
			hostResult := play.Hosts[hostName]
			for _, taskResult := range hostResult.Tasks {
				taskName := taskResult.Name
				if taskName == "" {
					taskName = "task"
				}

				fmt.Fprintf(w, "\nTASK [%s] *************************************************************************\n", taskName)

				if taskResult.Skipped {
					fmt.Fprintf(w, "skipping: [%s]\n", hostName)
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
					fmt.Fprintf(w, "fatal: [%s]: UNREACHABLE!", hostName)
				case "ignored":
					fmt.Fprintf(w, "ignoring unreachable: [%s]", hostName)
				case "fatal":
					fmt.Fprintf(w, "fatal: [%s]: FAILED!", hostName)
				default:
					fmt.Fprintf(w, "%s: [%s]", status, hostName)
				}

				if taskResult.Item != nil {
					fmt.Fprintf(w, " => (item=%v)", formatItem(taskResult.Item))
				}
				fmt.Fprintln(w)

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
					fmt.Fprintf(w, "  diff:\n%s\n", indentBlock(taskResult.Diff, "    "))
				}
			}
		}
	}

	fmt.Fprintf(w, "\nPLAY RECAP *****************************************************************************\n")
	for _, play := range result.Plays {
		for _, hostName := range sortedHostNames(play.Hosts) {
			hostResult := play.Hosts[hostName]
			okCount, changedCount, failedCount, skippedCount, unreachableCount, ignoredCount := summarizeHost(hostResult)
			color := "\033[32m"
			if failedCount > 0 || (unreachableCount > 0 && ignoredCount == 0) {
				color = "\033[31m"
			} else if changedCount > 0 {
				color = "\033[33m"
			} else if unreachableCount > 0 {
				color = "\033[36m"
			}
			fmt.Fprintf(
				w,
				"%s%-30s\033[0m: ok=%-3d changed=%-3d failed=%-3d skipped=%-3d unreachable=%-3d rescued=0 ignored=%-3d\n",
				color,
				hostName,
				okCount,
				changedCount,
				failedCount,
				skippedCount,
				unreachableCount,
				ignoredCount,
			)
		}
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
			for _, tr := range hostResult.Tasks {
				taskName := tr.Name
				if taskName == "" {
					taskName = "task"
				}
				var dur time.Duration
				if !tr.Start.IsZero() && !tr.End.IsZero() && tr.End.After(tr.Start) {
					dur = tr.End.Sub(tr.Start)
				}
				if existing, ok := taskMap[taskName]; ok {
					if dur > existing {
						taskMap[taskName] = dur
					}
				} else {
					taskMap[taskName] = dur
					taskOrder = append(taskOrder, taskName)
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
		fmt.Fprintf(w, "%s %s %6.2fs\n", name, strings.Repeat("-", dashes), p.duration.Seconds())
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
				for _, tr := range hostResult.Tasks {
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
	fmt.Fprintf(w, "\nPlaybook run took %d days, %d hours, %d minutes, %d seconds\n", days, hours, minutes, seconds)
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

	payload := struct {
		Plays []*jsonPlayResult `json:"plays"`
		Tags  []string          `json:"tags,omitempty"`
	}{
		Plays: nil,
	}
	if result != nil {
		payload.Plays = make([]*jsonPlayResult, 0, len(result.Plays))
		for _, play := range result.Plays {
			jsonPlay := &jsonPlayResult{
				Name:  play.Name,
				Hosts: make(map[string]*jsonHostResult, len(play.Hosts)),
			}
			for hostName, hostResult := range play.Hosts {
				jsonHost := &jsonHostResult{
					Tasks: make([]*jsonTaskResult, 0, len(hostResult.Tasks)),
				}
				for _, taskResult := range hostResult.Tasks {
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
					if !taskResult.Start.IsZero() {
						jsonTask.Start = taskResult.Start.Format("2006-01-02T15:04:05Z07:00")
					}
					if !taskResult.End.IsZero() {
						jsonTask.End = taskResult.End.Format("2006-01-02T15:04:05Z07:00")
					}
					jsonHost.Tasks = append(jsonHost.Tasks, jsonTask)
				}
				jsonPlay.Hosts[hostName] = jsonHost
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

func indentBlock(text, prefix string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func HasFailures(result *playbook.PlaybookResult) bool {
	if result == nil {
		return false
	}
	for _, play := range result.Plays {
		for _, hostResult := range play.Hosts {
			for _, taskResult := range hostResult.Tasks {
				if taskResult.Failed {
					return true
				}
			}
		}
	}
	return false
}

func summarizeHost(hostResult *playbook.HostResult) (okCount, changedCount, failedCount, skippedCount, unreachableCount, ignoredCount int) {
	if hostResult == nil {
		return 0, 0, 0, 0, 0, 0
	}
	for _, taskResult := range hostResult.Tasks {
		if taskResult.Unreachable {
			unreachableCount++
		}
		if taskResult.Ignored {
			ignoredCount++
		}
		switch {
		case taskResult.Failed:
			failedCount++
		case taskResult.Changed:
			changedCount++
		case taskResult.Skipped:
			skippedCount++
		default:
			okCount++
		}
	}
	return okCount, changedCount, failedCount, skippedCount, unreachableCount, ignoredCount
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
