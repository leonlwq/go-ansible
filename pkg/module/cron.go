package module

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"go-ansible/pkg/ssh"
)

// CronModule cron 模块
type CronModule struct{}

func (m *CronModule) Name() string { return "cron" }

func (m *CronModule) Validate(params map[string]interface{}) error {
	state := GetParamString(params, "state", "present")
	isEnv := GetParamBool(params, "env", false)
	if state != "absent" && !isEnv {
		if strings.TrimSpace(GetParamString(params, "job", "")) == "" {
			return fmt.Errorf("cron module requires job")
		}
	}
	if isEnv && strings.TrimSpace(GetParamString(params, "name", "")) == "" {
		return fmt.Errorf("cron module requires name when env is set")
	}
	specialTime := strings.TrimPrefix(strings.TrimSpace(GetParamString(params, "special_time", "")), "@")
	if specialTime != "" {
		validSpecialTimes := map[string]bool{
			"reboot": true, "yearly": true, "annually": true,
			"monthly": true, "weekly": true, "daily": true,
			"midnight": true, "hourly": true,
		}
		if !validSpecialTimes[strings.ToLower(specialTime)] {
			return fmt.Errorf("invalid special_time: %s", specialTime)
		}
	}
	return nil
}

func (m *CronModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	name := GetParamString(params, "name", "")
	state := GetParamString(params, "state", "present")
	minute := GetParamString(params, "minute", "*")
	hour := GetParamString(params, "hour", "*")
	day := GetParamString(params, "day", "*")
	weekday := GetParamString(params, "weekday", "*")
	month := GetParamString(params, "month", "*")
	job := GetParamString(params, "job", "")
	user := GetParamString(params, "user", "root")
	specialTime := GetParamString(params, "special_time", "")
	disabled := GetParamBool(params, "disabled", false)
	cronFile := GetParamString(params, "cron_file", "")
	backup := GetParamBool(params, "backup", false)
	isEnv := GetParamBool(params, "env", false)

	var filePath string
	if cronFile != "" {
		filePath = cronFile
		if !strings.HasPrefix(filePath, "/") {
			filePath = "/etc/cron.d/" + cronFile
		}
	}

	// 检查现有 crontab 内容
	existingContent := ""
	if cronFile != "" {
		checkCmd := fmt.Sprintf("cat %s 2>/dev/null", shellQuote(filePath))
		checkResult, _ := client.Execute(checkCmd)
		if checkResult != nil && checkResult.ExitCode == 0 {
			existingContent = checkResult.Stdout
		}
	} else {
		checkCmd := fmt.Sprintf("crontab -l -u %s 2>/dev/null", shellQuote(user))
		checkResult, _ := client.Execute(checkCmd)
		if checkResult != nil && checkResult.ExitCode == 0 {
			existingContent = checkResult.Stdout
		}
	}

	// 构造待写入的 job line (cron_file 需要包含 user 列)
	var jobLine string
	if isEnv {
		val := GetParamString(params, "value", job)
		if val != "" && !strings.HasPrefix(val, "\"") && strings.ContainsAny(val, " \t") {
			jobLine = fmt.Sprintf("%s=%q", name, val)
		} else {
			jobLine = fmt.Sprintf("%s=%s", name, val)
		}
	} else if cronFile != "" {
		if specialTime != "" {
			specialTime = strings.TrimPrefix(strings.TrimSpace(specialTime), "@")
			jobLine = fmt.Sprintf("@%s %s %s", specialTime, user, job)
		} else if job != "" {
			jobLine = fmt.Sprintf("%s %s %s %s %s %s %s", minute, hour, day, month, weekday, user, job)
		}
	} else {
		if specialTime != "" {
			specialTime = strings.TrimPrefix(strings.TrimSpace(specialTime), "@")
			jobLine = fmt.Sprintf("@%s %s", specialTime, job)
		} else if job != "" {
			jobLine = fmt.Sprintf("%s %s %s %s %s %s", minute, hour, day, month, weekday, job)
		}
	}

	newContent, changed := updateCrontabContent(existingContent, name, job, jobLine, state, disabled)

	if !changed {
		return &Result{
			Changed: false,
			Message: "cron entry already in desired state",
		}, nil
	}

	if IsCheckMode(params) {
		msg := "check mode: cron entry would change"
		if state == "absent" {
			msg = "check mode: cron entry would be removed"
		}
		return &Result{
			Changed: true,
			Message: msg,
		}, nil
	}

	var backupPath string
	if backup && existingContent != "" {
		ts := time.Now().UnixNano()
		if cronFile != "" {
			fileName := strings.TrimPrefix(cronFile, "/etc/cron.d/")
			backupPath = fmt.Sprintf("/tmp/%s.%d.bak", fileName, ts)
			_, _ = client.Execute(fmt.Sprintf("cp -p %s %s 2>/dev/null", shellQuote(filePath), shellQuote(backupPath)))
		} else {
			backupPath = fmt.Sprintf("/tmp/crontab.%s.%d.bak", user, ts)
			b64 := base64.StdEncoding.EncodeToString([]byte(existingContent))
			_, _ = client.Execute(fmt.Sprintf("printf '%%s' %s | base64 -d > %s 2>/dev/null", shellQuote(b64), shellQuote(backupPath)))
		}
	}

	// 执行实际变更
	var applyCmd string
	trimmedNew := strings.TrimSpace(newContent)
	if cronFile != "" {
		if trimmedNew == "" {
			applyCmd = fmt.Sprintf("rm -f %s 2>/dev/null || true", shellQuote(filePath))
		} else {
			b64 := base64.StdEncoding.EncodeToString([]byte(newContent))
			applyCmd = fmt.Sprintf("printf '%%s' %s | base64 -d > %s && chmod 0644 %s", shellQuote(b64), shellQuote(filePath), shellQuote(filePath))
		}
	} else {
		if trimmedNew == "" {
			applyCmd = fmt.Sprintf("crontab -r -u %s 2>/dev/null || true", shellQuote(user))
		} else {
			b64 := base64.StdEncoding.EncodeToString([]byte(newContent))
			applyCmd = fmt.Sprintf("printf '%%s' %s | base64 -d | crontab -u %s -", shellQuote(b64), shellQuote(user))
		}
	}

	result, err := client.Execute(applyCmd)
	if err != nil {
		return result, err
	}
	result.Changed = true
	if backupPath != "" {
		if result.Data == nil {
			result.Data = make(map[string]interface{})
		}
		result.Data["backup_file"] = backupPath
	}
	return result, nil
}

func isCronCommentFor(line, name string) bool {
	trimmed := strings.TrimSpace(line)
	if name == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "#Ansible: ") {
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "#Ansible: ")) == name
	}
	if strings.HasPrefix(trimmed, "# Ansible: ") {
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "# Ansible: ")) == name
	}
	return false
}

func normalizeCronJob(line string) string {
	trimmed := strings.TrimSpace(line)
	for strings.HasPrefix(trimmed, "#") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
	}
	return strings.Join(strings.Fields(trimmed), " ")
}

func isMatchingCronLine(existingLine, targetLine string, disabled bool) bool {
	existingTrimmed := strings.TrimSpace(existingLine)
	targetTrimmed := strings.TrimSpace(targetLine)
	if existingTrimmed == targetTrimmed || strings.Join(strings.Fields(existingTrimmed), " ") == strings.Join(strings.Fields(targetTrimmed), " ") {
		return true
	}
	if disabled {
		if strings.HasPrefix(existingTrimmed, "#") && normalizeCronJob(existingTrimmed) == normalizeCronJob(targetTrimmed) {
			return true
		}
	}
	return false
}

func updateCrontabContent(existing, name, job, jobLine, state string, disabled bool) (string, bool) {
	lines := strings.Split(existing, "\n")
	// 剔除末尾多余空行，统一规范化
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	comment := ""
	if name != "" {
		comment = fmt.Sprintf("# Ansible: %s", name)
	}

	effectiveJobLine := jobLine
	if disabled && effectiveJobLine != "" {
		if !strings.HasPrefix(strings.TrimSpace(effectiveJobLine), "#") {
			effectiveJobLine = "#" + effectiveJobLine
		}
	}

	if state == "absent" {
		changed := false
		var newLines []string
		skipNext := false
		for _, line := range lines {
			if skipNext {
				skipNext = false
				changed = true
				continue
			}
			if isCronCommentFor(line, name) {
				skipNext = true
				changed = true
				continue
			}
			if name != "" && strings.HasPrefix(strings.TrimSpace(line), name+"=") {
				changed = true
				continue
			}
			if name == "" && job != "" && strings.Contains(line, job) {
				changed = true
				continue
			}
			newLines = append(newLines, line)
		}
		if !changed {
			return existing, false
		}
		out := strings.Join(newLines, "\n")
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		return out, true
	}

	// state == "present"
	var newLines []string
	found := false
	changed := false

	if name != "" {
		for i := 0; i < len(lines); i++ {
			line := lines[i]
			if isCronCommentFor(line, name) {
				found = true
				newLines = append(newLines, comment)
				if i+1 < len(lines) {
					if isMatchingCronLine(lines[i+1], effectiveJobLine, disabled) {
						newLines = append(newLines, lines[i+1])
					} else {
						newLines = append(newLines, effectiveJobLine)
						changed = true
					}
					i++
				} else {
					newLines = append(newLines, effectiveJobLine)
					changed = true
				}
				continue
			}
			newLines = append(newLines, line)
		}
		if !found {
			newLines = append(newLines, comment, effectiveJobLine)
			changed = true
		}
	} else {
		// 没有名字，纯根据 job 内容匹配
		for _, line := range lines {
			if isMatchingCronLine(line, effectiveJobLine, disabled) {
				found = true
				newLines = append(newLines, line)
			} else if disabled && normalizeCronJob(line) == normalizeCronJob(jobLine) && !found {
				// 活跃任务就地注释
				found = true
				newLines = append(newLines, effectiveJobLine)
				changed = true
			} else if !disabled && normalizeCronJob(line) == normalizeCronJob(jobLine) && strings.HasPrefix(strings.TrimSpace(line), "#") && !found {
				// 注释任务就地激活
				found = true
				newLines = append(newLines, effectiveJobLine)
				changed = true
			} else {
				newLines = append(newLines, line)
			}
		}
		if !found {
			newLines = append(newLines, effectiveJobLine)
			changed = true
		}
	}

	out := strings.Join(newLines, "\n")
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out, changed
}
