package module

import (
	"fmt"

	"go-ansible/pkg/ssh"
)

// CronModule cron 模块
type CronModule struct{}

func (m *CronModule) Name() string { return "cron" }

func (m *CronModule) Validate(params map[string]interface{}) error {
	if _, ok := params["job"]; !ok {
		if _, ok := params["special_time"]; !ok {
			return fmt.Errorf("cron module requires job or special_time")
		}
	}
	return nil
}

func (m *CronModule) Execute(client *ssh.Client, params map[string]interface{}) (*Result, error) {
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

	if state == "absent" {
		if name != "" {
			// 按名称删除
			cmd := fmt.Sprintf("crontab -l -u %s | grep -v '%s' | crontab -u %s -", user, name, user)
			result, err := client.Execute(cmd)
			if err != nil {
				return result, err
			}
			result.Changed = true
			return result, nil
		}
		// 按 job 删除
		cmd := fmt.Sprintf("crontab -l -u %s | grep -v '%s' | crontab -u %s -", user, job, user)
		result, err := client.Execute(cmd)
		if err != nil {
			return result, err
		}
		result.Changed = true
		return result, nil
	}

	// 构建 cron 行
	var cronLine string
	if name != "" {
		cronLine = fmt.Sprintf("# Ansible: %s\n", name)
	}

	if specialTime != "" {
		cronLine += fmt.Sprintf("@%s %s", specialTime, job)
	} else {
		cronLine += fmt.Sprintf("%s %s %s %s %s %s", minute, hour, day, month, weekday, job)
	}

	// 检查是否已存在
	checkCmd := fmt.Sprintf("crontab -l -u %s 2>/dev/null", user)
	checkResult, _ := client.Execute(checkCmd)

	if name != "" && checkResult != nil {
		// 检查是否有相同名称的任务
		if contains(checkResult.Stdout, fmt.Sprintf("# Ansible: %s", name)) {
			// 更新现有任务
			escapedLine := escapeForSed(cronLine)
			updateCmd := fmt.Sprintf("crontab -l -u %s | sed '/# Ansible: %s/,+1c\\%s' | crontab -u %s -",
				user, name, escapedLine, user)
			result, err := client.Execute(updateCmd)
			if err != nil {
				return result, err
			}
			result.Changed = true
			return result, nil
		}
	}

	// 添加新任务
	addCmd := fmt.Sprintf("(crontab -l -u %s 2>/dev/null; echo '%s') | crontab -u %s -", user, cronLine, user)
	result, err := client.Execute(addCmd)
	if err != nil {
		return result, err
	}
	result.Changed = true
	return result, nil
}

func escapeForSed(s string) string {
	result := ""
	for _, c := range s {
		switch c {
		case '/', '\\', '&':
			result += "\\" + string(c)
		case '\n':
			result += "\\n"
		default:
			result += string(c)
		}
	}
	return result
}
