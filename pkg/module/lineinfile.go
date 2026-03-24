package module

import (
	"fmt"

	"go-ansible/pkg/ssh"
)

// LineinfileModule lineinfile 模块
type LineinfileModule struct{}

func (m *LineinfileModule) Name() string { return "lineinfile" }

func (m *LineinfileModule) Validate(params map[string]interface{}) error {
	if _, ok := params["path"]; !ok {
		return fmt.Errorf("lineinfile module requires path")
	}
	if _, ok := params["line"]; !ok {
		if _, ok := params["regexp"]; !ok {
			return fmt.Errorf("lineinfile module requires line or regexp")
		}
	}
	return nil
}

func (m *LineinfileModule) Execute(client *ssh.Client, params map[string]interface{}) (*Result, error) {
	path := GetParamString(params, "path", "")
	state := GetParamString(params, "state", "present")
	line := GetParamString(params, "line", "")
	regexp := GetParamString(params, "regexp", "")
	create := GetParamBool(params, "create", false)
	insertafter := GetParamString(params, "insertafter", "")
	insertbefore := GetParamString(params, "insertbefore", "")
	backup := GetParamBool(params, "backup", false)

	if create {
		client.Execute(fmt.Sprintf("touch %s", path))
	}

	if backup {
		client.Execute(fmt.Sprintf("cp -p %s %s.bak", path, path))
	}

	if state == "absent" {
		var cmd string
		if regexp != "" {
			cmd = fmt.Sprintf("sed -i '/%s/d' %s", regexp, path)
		} else {
			cmd = fmt.Sprintf("sed -i '/%s/d' %s", escapeForSed(line), path)
		}
		result, err := client.Execute(cmd)
		if err != nil {
			return result, err
		}
		result.Changed = true
		return result, nil
	}

	// state == present
	if regexp != "" {
		// 检查是否有匹配的行
		checkCmd := fmt.Sprintf("grep -qE '%s' %s", regexp, path)
		checkResult, _ := client.Execute(checkCmd)

		if checkResult.ExitCode == 0 {
			// 替换匹配的行
			replaceCmd := fmt.Sprintf("sed -i 's|%s.*|%s|' %s", regexp, escapeForSed(line), path)
			result, err := client.Execute(replaceCmd)
			if err != nil {
				return result, err
			}
			result.Changed = true
			return result, nil
		}
	}

	// 检查行是否存在
	checkCmd := fmt.Sprintf("grep -qFx '%s' %s", escapeForGrep(line), path)
	checkResult, _ := client.Execute(checkCmd)

	if checkResult.ExitCode == 0 {
		return &Result{Changed: false, Message: "line already exists"}, nil
	}

	// 添加行
	var cmd string
	if insertafter != "" {
		cmd = fmt.Sprintf("sed -i '/%s/a\\%s' %s", insertafter, escapeForSed(line), path)
	} else if insertbefore != "" {
		cmd = fmt.Sprintf("sed -i '/%s/i\\%s' %s", insertbefore, escapeForSed(line), path)
	} else {
		cmd = fmt.Sprintf("echo '%s' >> %s", escapeForEcho(line), path)
	}

	result, err := client.Execute(cmd)
	if err != nil {
		return result, err
	}
	result.Changed = true
	return result, nil
}

func escapeForGrep(s string) string {
	result := ""
	for _, c := range s {
		switch c {
		case '/', '\\', '.', '*', '[', ']', '^', '$':
			result += "\\" + string(c)
		default:
			result += string(c)
		}
	}
	return result
}

func escapeForEcho(s string) string {
	result := ""
	for _, c := range s {
		switch c {
		case '\\':
			result += "\\\\"
		case '\'':
			result += "\\'"
		default:
			result += string(c)
		}
	}
	return result
}
