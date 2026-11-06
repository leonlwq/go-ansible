package module

import (
	"fmt"
	"path/filepath"
	"strings"

	"go-ansible/pkg/ssh"
)

// PipModule 管理 Python 软件包及 Virtualenv 虚拟环境
type PipModule struct{}

func (m *PipModule) Name() string { return "pip" }

func (m *PipModule) Validate(params map[string]interface{}) error {
	name := packageNames(params)
	requirements := GetParamString(params, "requirements", "")
	if name == "" && requirements == "" {
		return fmt.Errorf("pip module requires name or requirements")
	}
	return nil
}

func (m *PipModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	name := packageNames(params)
	version := GetParamString(params, "version", "")
	requirements := GetParamString(params, "requirements", "")
	virtualenv := GetParamString(params, "virtualenv", "")
	venvCmd := GetParamString(params, "virtualenv_command", "")
	venvPython := GetParamString(params, "virtualenv_python", "")
	executable := GetParamString(params, "executable", "")
	extraArgs := GetParamString(params, "extra_args", "")
	chdir := GetParamString(params, "chdir", "")
	state := strings.ToLower(GetParamString(params, "state", "present"))
	editable := GetParamBool(params, "editable", false)
	virtualenvSitePackages := GetParamBool(params, "virtualenv_site_packages", false)
	umask := GetParamString(params, "umask", "")
	checkMode := IsCheckMode(params)

	// 1. 确定使用的 pip 可执行程序路径
	var pipBin string
	var venvCreated bool
	if virtualenv != "" {
		// 检查目标机上 virtualenv 是否已存在
		checkVenvCmd := fmt.Sprintf("test -f %s || test -f %s",
			shellQuote(filepath.Join(virtualenv, "bin", "python")),
			shellQuote(filepath.Join(virtualenv, "bin", "activate")),
		)
		venvRes, _ := client.Execute(checkVenvCmd)
		if venvRes == nil || venvRes.ExitCode != 0 {
			if checkMode {
				return &Result{
					Changed: true,
					Stdout:  fmt.Sprintf("virtualenv %s would be created", virtualenv),
				}, nil
			}
			// 创建虚拟环境
			creator := "python3 -m venv"
			if venvCmd != "" {
				creator = venvCmd
			} else if venvPython != "" {
				creator = fmt.Sprintf("%s -m venv", venvPython)
			}
			sitePackagesArg := ""
			if virtualenvSitePackages {
				sitePackagesArg = "--system-site-packages "
			}
			createCmd := fmt.Sprintf("%s %s%s", creator, sitePackagesArg, shellQuote(virtualenv))
			if chdir != "" {
				createCmd = fmt.Sprintf("cd %s && %s", shellQuote(chdir), createCmd)
			}
			if umask != "" {
				createCmd = fmt.Sprintf("umask %s && %s", umask, createCmd)
			}
			createRes, err := client.Execute(createCmd)
			if err != nil || (createRes != nil && createRes.ExitCode != 0) {
				msg := "failed to create virtualenv"
				if createRes != nil && createRes.Stderr != "" {
					msg = createRes.Stderr
				} else if err != nil {
					msg = err.Error()
				}
				return &Result{
					Failed:   true,
					ExitCode: 1,
					Stderr:   msg,
				}, fmt.Errorf("pip virtualenv creation error: %s", msg)
			}
			venvCreated = true
		}
		pipBin = filepath.Join(virtualenv, "bin", "pip")
	} else if executable != "" {
		pipBin = executable
	} else {
		pipBin = "pip3"
	}

	// 2. 规整待安装/卸载的软件包名称
	var targetPackages []string
	if name != "" {
		rawPkgs := strings.Fields(name)
		for _, p := range rawPkgs {
			if version != "" && !strings.ContainsAny(p, "=<>~") {
				targetPackages = append(targetPackages, fmt.Sprintf("%s==%s", p, version))
			} else {
				targetPackages = append(targetPackages, p)
			}
		}
	}

	// 3. Check Mode 预判检查
	if checkMode {
		return m.executeCheckMode(client, pipBin, targetPackages, requirements, state, chdir, venvCreated)
	}

	// 4. 构建执行命令
	var cmdArgs []string
	switch state {
	case "absent":
		cmdArgs = append(cmdArgs, "uninstall", "-y")
		if extraArgs != "" {
			cmdArgs = append(cmdArgs, extraArgs)
		}
		if requirements != "" {
			cmdArgs = append(cmdArgs, "-r", shellQuote(requirements))
		}
		for _, p := range targetPackages {
			cmdArgs = append(cmdArgs, shellQuote(p))
		}
	case "latest":
		cmdArgs = append(cmdArgs, "install", "--upgrade")
		if extraArgs != "" {
			cmdArgs = append(cmdArgs, extraArgs)
		}
		if requirements != "" {
			cmdArgs = append(cmdArgs, "-r", shellQuote(requirements))
		}
		for _, p := range targetPackages {
			if editable {
				cmdArgs = append(cmdArgs, "-e")
			}
			cmdArgs = append(cmdArgs, shellQuote(p))
		}
	case "forcereinstall":
		cmdArgs = append(cmdArgs, "install", "--force-reinstall")
		if extraArgs != "" {
			cmdArgs = append(cmdArgs, extraArgs)
		}
		if requirements != "" {
			cmdArgs = append(cmdArgs, "-r", shellQuote(requirements))
		}
		for _, p := range targetPackages {
			if editable {
				cmdArgs = append(cmdArgs, "-e")
			}
			cmdArgs = append(cmdArgs, shellQuote(p))
		}
	default: // present / installed
		cmdArgs = append(cmdArgs, "install")
		if extraArgs != "" {
			cmdArgs = append(cmdArgs, extraArgs)
		}
		if requirements != "" {
			cmdArgs = append(cmdArgs, "-r", shellQuote(requirements))
		}
		for _, p := range targetPackages {
			if editable {
				cmdArgs = append(cmdArgs, "-e")
			}
			cmdArgs = append(cmdArgs, shellQuote(p))
		}
	}

	pipExec := formatPipBin(pipBin)
	execCmd := fmt.Sprintf("%s %s", pipExec, strings.Join(cmdArgs, " "))
	if chdir != "" {
		execCmd = fmt.Sprintf("cd %s && %s", shellQuote(chdir), execCmd)
	}
	if umask != "" {
		execCmd = fmt.Sprintf("umask %s && %s", umask, execCmd)
	}

	result, err := client.Execute(execCmd)
	if err != nil {
		return result, err
	}

	result.Changed = venvCreated || pipChanged(state, result.Stdout, result.Stderr)
	return result, nil
}

func (m *PipModule) executeCheckMode(client ssh.Connection, pipBin string, pkgs []string, requirements, state, chdir string, venvCreated bool) (*Result, error) {
	if venvCreated {
		return &Result{Changed: true, Stdout: "check mode: virtualenv would be created and packages applied"}, nil
	}

	if requirements != "" || state == "latest" || state == "forcereinstall" {
		return &Result{Changed: true, Stdout: fmt.Sprintf("check mode: state=%s would apply updates", state)}, nil
	}

	pipExec := formatPipBin(pipBin)

	if state == "absent" {
		for _, p := range pkgs {
			baseName := pkgBaseName(p)
			checkCmd := fmt.Sprintf("%s show %s", pipExec, shellQuote(baseName))
			if chdir != "" {
				checkCmd = fmt.Sprintf("cd %s && %s", shellQuote(chdir), checkCmd)
			}
			res, _ := client.Execute(checkCmd)
			if res != nil && res.ExitCode == 0 {
				return &Result{Changed: true, Stdout: fmt.Sprintf("check mode: package %s would be removed", baseName)}, nil
			}
		}
		return &Result{Changed: false, Stdout: "check mode: all packages already absent"}, nil
	}

	// state == present
	allSatisfied := true
	for _, p := range pkgs {
		baseName := pkgBaseName(p)
		expectedVer := pkgExpectedVersion(p)

		checkCmd := fmt.Sprintf("%s show %s", pipExec, shellQuote(baseName))
		if chdir != "" {
			checkCmd = fmt.Sprintf("cd %s && %s", shellQuote(chdir), checkCmd)
		}
		res, _ := client.Execute(checkCmd)
		if res == nil || res.ExitCode != 0 {
			allSatisfied = false
			break
		}
		if expectedVer != "" {
			curVer := parsePipShowVersion(res.Stdout)
			if curVer != expectedVer {
				allSatisfied = false
				break
			}
		}
	}

	if !allSatisfied {
		return &Result{Changed: true, Stdout: "check mode: packages would be installed or upgraded"}, nil
	}
	return &Result{Changed: false, Stdout: "check mode: all packages already satisfied"}, nil
}

func pipChanged(state, stdout, stderr string) bool {
	combined := stdout + "\n" + stderr

	if state == "absent" {
		if strings.Contains(combined, "Successfully uninstalled") {
			return true
		}
		if strings.Contains(combined, "WARNING: Skipping") || strings.Contains(combined, "not installed") {
			return false
		}
		return false
	}

	if strings.Contains(combined, "Successfully installed") ||
		strings.Contains(combined, "Installing collected packages") ||
		strings.Contains(combined, "Uninstalling ") {
		return true
	}

	if strings.Contains(combined, "Requirement already satisfied") {
		return false
	}

	return strings.TrimSpace(stdout) != ""
}

func pkgBaseName(pkg string) string {
	for _, sep := range []string{"==", ">=", "<=", "~=", "!=", "<", ">"} {
		if idx := strings.Index(pkg, sep); idx != -1 {
			return strings.TrimSpace(pkg[:idx])
		}
	}
	return strings.TrimSpace(pkg)
}

func pkgExpectedVersion(pkg string) string {
	if idx := strings.Index(pkg, "=="); idx != -1 {
		return strings.TrimSpace(pkg[idx+2:])
	}
	return ""
}

func parsePipShowVersion(showOutput string) string {
	for _, line := range strings.Split(showOutput, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Version:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Version:"))
		}
	}
	return ""
}

func formatPipBin(pipBin string) string {
	if strings.Contains(pipBin, "/") || strings.Contains(pipBin, " ") {
		return shellQuote(pipBin)
	}
	return pipBin
}
