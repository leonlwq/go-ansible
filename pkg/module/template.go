package module

import (
	"fmt"
	"io"
	"os"
	"text/template"

	"go-ansible/pkg/ssh"
)

// TemplateModule template 模块
type TemplateModule struct{}

func (m *TemplateModule) Name() string { return "template" }

func (m *TemplateModule) Validate(params map[string]interface{}) error {
	if _, ok := params["src"]; !ok {
		return fmt.Errorf("template module requires src")
	}
	if _, ok := params["dest"]; !ok {
		return fmt.Errorf("template module requires dest")
	}
	return nil
}

func (m *TemplateModule) Execute(client *ssh.Client, params map[string]interface{}) (*Result, error) {
	src := GetParamString(params, "src", "")
	dest := GetParamString(params, "dest", "")
	mode := GetParamString(params, "mode", "")
	owner := GetParamString(params, "owner", "")
	group := GetParamString(params, "group", "")
	backup := GetParamBool(params, "backup", false)
	varsData, _ := params["vars"].(map[string]interface{})

	if varsData == nil {
		varsData = make(map[string]interface{})
	}

	// 读取模板文件
	tmplContent, err := os.ReadFile(src)
	if err != nil {
		return nil, fmt.Errorf("read template file: %w", err)
	}

	// 解析模板
	tmpl, err := template.New("template").Parse(string(tmplContent))
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}

	// 创建临时文件
	tmpFile, err := os.CreateTemp("", "go-ansible-template-*")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	// 渲染模板
	if err := tmpl.Execute(tmpFile, varsData); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}

	// 备份
	if backup {
		client.Execute(fmt.Sprintf("cp -p %s %s.bak 2>/dev/null || true", dest, dest))
	}

	// 上传渲染后的文件
	if err := client.Upload(tmpFile.Name(), dest); err != nil {
		return nil, fmt.Errorf("upload rendered template: %w", err)
	}

	// 设置权限
	if mode != "" {
		client.Execute(fmt.Sprintf("chmod %s %s", mode, dest))
	}
	if owner != "" {
		chown := owner
		if group != "" {
			chown = fmt.Sprintf("%s:%s", owner, group)
		}
		client.Execute(fmt.Sprintf("chown %s %s", chown, dest))
	}

	return &Result{
		Changed: true,
		Message: "template rendered successfully",
	}, nil
}

// renderTemplate 渲染模板
func renderTemplate(templatePath string, vars map[string]interface{}) (string, error) {
	tmpl, err := template.ParseFiles(templatePath)
	if err != nil {
		return "", err
	}

	var result string
	if err := tmpl.Execute(io.Discard, vars); err != nil {
		return "", err
	}

	return result, nil
}
