package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"go-ansible/pkg/adhoc"
	"go-ansible/pkg/inventory"
	"go-ansible/pkg/playbook"
)

const version = "0.1.0"

// parseExtraVars 解析 extra vars 字符串
// 支持格式: key=value 或 JSON 格式 {"key":"value"}
func parseExtraVars(varsStr string) map[string]interface{} {
	result := make(map[string]interface{})

	// 尝试解析为 JSON
	if strings.HasPrefix(varsStr, "{") {
		var jsonVars map[string]interface{}
		if err := json.Unmarshal([]byte(varsStr), &jsonVars); err == nil {
			return jsonVars
		}
	}

	// 解析 key=value 格式
	pairs := strings.Fields(varsStr)
	for _, pair := range pairs {
		if idx := strings.Index(pair, "="); idx > 0 {
			key := pair[:idx]
			value := pair[idx+1:]
			result[key] = value
		}
	}

	return result
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "playbook":
		runPlaybook(os.Args[2:])
	case "adhoc", "ad-hoc":
		runAdhoc(os.Args[2:])
	case "inventory":
		runInventory(os.Args[2:])
	case "version":
		fmt.Printf("go-ansible %s\n", version)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`go-ansible - A high-performance Ansible alternative written in Go

Usage:
  go-ansible <command> [options]

Commands:
  playbook    Run a playbook
  adhoc       Run ad-hoc commands
  inventory   Manage inventory
  version     Show version
  help        Show this help

Examples:
  go-ansible playbook site.yml -i inventory.ini
  go-ansible adhoc all -i inventory.ini -m ping
  go-ansible adhoc webservers -i inventory.ini -m shell -a "uptime"
  go-ansible inventory list -i inventory.ini`)
}

func runPlaybook(args []string) {
	var inventoryPath, inventoryPathLong, tags string
	var check, verbose bool
	var extraVars []string

	// 手动解析参数，支持 playbook 文件在任意位置
	var playbookPath string

	for i := 0; i < len(args); i++ {
		arg := args[i]

		// 处理 -i 参数
		if arg == "-i" || arg == "--inventory" {
			if i+1 < len(args) {
				i++
				if arg == "-i" {
					inventoryPath = args[i]
				} else {
					inventoryPathLong = args[i]
				}
			}
			continue
		}

		// 处理 -t 参数
		if arg == "-t" || arg == "--tags" {
			if i+1 < len(args) {
				i++
				tags = args[i]
			}
			continue
		}

		// 处理 -e/--extra-vars 参数（支持多次使用）
		if arg == "-e" || arg == "--extra-vars" {
			if i+1 < len(args) {
				i++
				extraVars = append(extraVars, args[i])
			}
			continue
		}

		// 处理 --check 标志
		if arg == "--check" {
			check = true
			continue
		}

		// 处理 -v/--verbose 标志
		if arg == "-v" || arg == "--verbose" {
			verbose = true
			continue
		}

		// 非标志参数，作为 playbook 文件路径
		if !strings.HasPrefix(arg, "-") && playbookPath == "" {
			playbookPath = arg
			continue
		}
	}

	if playbookPath == "" {
		fmt.Fprintln(os.Stderr, "Error: playbook file is required")
		fmt.Fprintln(os.Stderr, "Usage: go-ansible playbook <playbook.yml> -i <inventory> [-e var=value] [-t tags]")
		os.Exit(1)
	}

	invPath := inventoryPath
	if invPath == "" {
		invPath = inventoryPathLong
	}

	if invPath == "" {
		fmt.Fprintln(os.Stderr, "Error: inventory file is required (-i)")
		fmt.Fprintln(os.Stderr, "Usage: go-ansible playbook <playbook.yml> -i <inventory> [-e var=value] [-t tags]")
		os.Exit(1)
	}

	// 解析 extra vars
	vars := make(map[string]interface{})
	for _, ev := range extraVars {
		parsed := parseExtraVars(ev)
		for k, v := range parsed {
			vars[k] = v
		}
	}

	if verbose {
		fmt.Printf("[DEBUG] Extra vars: %+v\n", vars)
	}

	// 解析 inventory
	inv, err := inventory.ParseFile(invPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing inventory: %v\n", err)
		os.Exit(1)
	}

	if verbose {
		fmt.Printf("[DEBUG] Inventory hosts: %d\n", len(inv.AllHosts))
		for name, host := range inv.AllHosts {
			fmt.Printf("[DEBUG]   Host: %s, User: %s, Become: %v\n", name, host.User, host.Become)
		}
	}

	// 解析 playbook
	fmt.Printf("Parsing playbook file: %s\n", playbookPath)
	parser := playbook.NewParser("")
	pb, err := parser.ParseFile(playbookPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing playbook: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Parsing playbook success: %s\n", playbookPath)

	if verbose {
		fmt.Printf("[DEBUG] Playbook plays: %d\n", len(pb.Plays))
		for _, play := range pb.Plays {
			fmt.Printf("[DEBUG]   Play hosts: %s, tasks: %d\n", play.Hosts, len(play.Tasks))
			for _, task := range play.Tasks {
				fmt.Printf("[DEBUG]     Task: %s, Module: %s, Tags: %v\n", task.Name, task.ModuleName, task.Tags)
			}
		}
	}

	// 执行 playbook
	fmt.Println("Creating executor...")
	executor := playbook.NewExecutor(inv)
	executor.SetVerbose(verbose)
	defer executor.Close()

	// 设置额外变量
	if len(vars) > 0 {
		fmt.Printf("Setting extra vars: %d\n", len(vars))
		executor.SetExtraVars(vars)
	}

	// 设置 tags
	if tags != "" {
		tagList := strings.Split(tags, ",")
		executor.SetTags(tagList)
		if verbose {
			fmt.Printf("[DEBUG] Filter tags: %v\n", tagList)
		}
		fmt.Printf("Filtering by tags: %v\n", tagList)
	}

	if check {
		fmt.Println("Running in check mode...")
	}

	result, err := executor.Execute(pb)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error executing playbook: %v\n", err)
		os.Exit(1)
	}

	// 输出结果 - Ansible 风格
	for _, play := range result.Plays {
		playName := play.Name
		if playName == "" {
			playName = ""
		}
		fmt.Printf("\nPLAY [%s] *************************************************************************\n", playName)

		for hostName, hostResult := range play.Hosts {
			for _, taskResult := range hostResult.Tasks {
				taskName := taskResult.Name
				if taskName == "" {
					taskName = "task"
				}

				fmt.Printf("\nTASK [%s] *************************************************************************\n", taskName)

				if taskResult.Skipped {
					fmt.Printf("skipping: [%s]\n", hostName)
				} else if taskResult.Failed {
					fmt.Printf("fatal: [%s]: FAILED!", hostName)
					if taskResult.Item != nil {
						fmt.Printf(" => (item=%v)", formatItem(taskResult.Item))
					}
					fmt.Println()
					if taskResult.Message != "" {
						fmt.Printf("  msg: %s\n", taskResult.Message)
					}
					if taskResult.Stderr != "" {
						fmt.Printf("  stderr: %s\n", taskResult.Stderr)
					}
				} else if taskResult.Changed {
					fmt.Printf("changed: [%s]", hostName)
					if taskResult.Item != nil {
						fmt.Printf(" => (item=%v)", formatItem(taskResult.Item))
					}
					if taskResult.Stdout != "" || taskResult.Stderr != "" {
						fmt.Printf("\n")
						if taskResult.Stdout != "" {
							fmt.Printf("  stdout: %s\n", taskResult.Stdout)
						}
						if taskResult.Stderr != "" {
							fmt.Printf("  stderr: %s\n", taskResult.Stderr)
						}
					} else {
						fmt.Println()
					}
				} else {
					fmt.Printf("ok: [%s]", hostName)
					if taskResult.Item != nil {
						fmt.Printf(" => (item=%v)", formatItem(taskResult.Item))
					}
					if taskResult.Stdout != "" || taskResult.Stderr != "" {
						fmt.Printf("\n")
						if taskResult.Stdout != "" {
							fmt.Printf("  stdout: %s\n", taskResult.Stdout)
						}
						if taskResult.Stderr != "" {
							fmt.Printf("  stderr: %s\n", taskResult.Stderr)
						}
					} else {
						fmt.Println()
					}
				}
			}
		}
	}

	// PLAY RECAP
	fmt.Printf("\nPLAY RECAP *****************************************************************************\n")
	for _, play := range result.Plays {
		for hostName, hostResult := range play.Hosts {
			hostFailed := 0
			hostOk := 0
			hostChanged := 0
			hostSkipped := 0
			for _, taskResult := range hostResult.Tasks {
				if taskResult.Failed {
					hostFailed++
				} else if taskResult.Changed {
					hostChanged++
				} else if taskResult.Skipped {
					hostSkipped++
				} else {
					hostOk++
				}
			}
			if hostFailed > 0 {
				fmt.Printf("\033[31m%-30s\033[0m: ok=%-3d changed=%-3d failed=%-3d skipped=%-3d unreachable=0 rescued=0 ignored=0\n",
					hostName, hostOk, hostChanged, hostFailed, hostSkipped)
			} else {
				fmt.Printf("\033[32m%-30s\033[0m: ok=%-3d changed=%-3d failed=%-3d skipped=%-3d unreachable=0 rescued=0 ignored=0\n",
					hostName, hostOk, hostChanged, hostFailed, hostSkipped)
			}
		}
	}

	if tags != "" {
		fmt.Printf("\nTags: %s\n", tags)
	}

	// 如果有失败，返回非零退出码
	for _, play := range result.Plays {
		for _, hostResult := range play.Hosts {
			for _, taskResult := range hostResult.Tasks {
				if taskResult.Failed {
					os.Exit(1)
				}
			}
		}
	}
}

// containsString 检查字符串切片是否包含指定字符串
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func runAdhoc(args []string) {
	var inventoryPath, inventoryPathLong string
	var module, moduleLong string = "command", "command"
	var moduleArgs, moduleArgsLong string
	var forks int = 5
	var verbose, verboseLong bool
	var become bool
	var becomeUser string = "root"
	var becomeMethod string = "sudo"

	// 手动解析参数，支持 target 在任意位置
	var target string

	for i := 0; i < len(args); i++ {
		arg := args[i]

		// 处理 -i 参数
		if arg == "-i" || arg == "--inventory" {
			if i+1 < len(args) {
				i++
				if arg == "-i" {
					inventoryPath = args[i]
				} else {
					inventoryPathLong = args[i]
				}
			}
			continue
		}

		// 处理 -m 参数
		if arg == "-m" || arg == "--module" {
			if i+1 < len(args) {
				i++
				if arg == "-m" {
					module = args[i]
				} else {
					moduleLong = args[i]
				}
			}
			continue
		}

		// 处理 -a 参数
		if arg == "-a" || arg == "--args" {
			if i+1 < len(args) {
				i++
				if arg == "-a" {
					moduleArgs = args[i]
				} else {
					moduleArgsLong = args[i]
				}
			}
			continue
		}

		// 处理 --forks 参数
		if arg == "--forks" {
			if i+1 < len(args) {
				i++
				fmt.Sscanf(args[i], "%d", &forks)
			}
			continue
		}

		// 处理 -v/--verbose 标志
		if arg == "-v" || arg == "--verbose" {
			if arg == "-v" {
				verbose = true
			} else {
				verboseLong = true
			}
			continue
		}

		// 处理 -become/--become/-b 标志
		if arg == "--become" || arg == "-become" || arg == "-b" {
			become = true
			continue
		}

		// 处理 --become-user/-become-user 参数
		if arg == "--become-user" || arg == "-become-user" || arg == "-U" {
			if i+1 < len(args) {
				i++
				becomeUser = args[i]
			}
			continue
		}

		// 处理 --become-method/-become-method 参数
		if arg == "--become-method" || arg == "-become-method" {
			if i+1 < len(args) {
				i++
				becomeMethod = args[i]
			}
			continue
		}

		// 非标志参数，作为 target
		if !strings.HasPrefix(arg, "-") && target == "" {
			target = arg
			continue
		}
	}

	if target == "" {
		fmt.Fprintln(os.Stderr, "Error: target host/group is required")
		fmt.Fprintln(os.Stderr, "Usage: go-ansible adhoc <target> -i <inventory> -m <module> -a <args> <-become>")
		os.Exit(1)
	}

	invPath := inventoryPath
	if invPath == "" {
		invPath = inventoryPathLong
	}
	modName := module
	if modName == "command" && moduleLong != "command" {
		modName = moduleLong
	}
	modArgs := moduleArgs
	if modArgs == "" {
		modArgs = moduleArgsLong
	}
	isVerbose := verbose || verboseLong

	if invPath == "" {
		fmt.Fprintln(os.Stderr, "Error: inventory file is required (-i)")
		fmt.Fprintln(os.Stderr, "Usage: go-ansible adhoc <target> -i <inventory> -m <module> -a <args> <-become>")
		os.Exit(1)
	}

	// 解析 inventory
	inv, err := inventory.ParseFile(invPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing inventory: %v\n", err)
		os.Exit(1)
	}

	// 如果命令行指定了 --become，应用到所有主机
	if become {
		for _, host := range inv.AllHosts {
			host.Become = true
			if becomeUser != "" {
				host.BecomeUser = becomeUser
			}
			if becomeMethod != "" {
				host.BecomeMethod = becomeMethod
			}
		}
	}

	// 创建 ad-hoc 执行器
	adhocExecutor := adhoc.NewAdhoc(inv, forks)
	defer adhocExecutor.Close()

	// 解析模块参数
	params := make(map[string]interface{})
	if modArgs != "" {
		// 简单解析 key=value 格式
		pairs := strings.Split(modArgs, " ")
		for _, pair := range pairs {
			if strings.Contains(pair, "=") {
				kv := strings.SplitN(pair, "=", 2)
				params[kv[0]] = kv[1]
			} else {
				params["_raw_params"] = modArgs
				break
			}
		}
	}

	// 执行
	result, err := adhocExecutor.Execute(target, modName, params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// 输出结果
	if isVerbose {
		fmt.Println(result.FormatResult())
	} else {
		for hostName, hostResult := range result.Hosts {
			fmt.Printf("%s | ", hostName)
			if hostResult.Failed {
				fmt.Printf("FAILED | %s\n", hostResult.Message)
			} else if hostResult.Changed {
				fmt.Printf("CHANGED\n")
			} else {
				fmt.Printf("SUCCESS\n")
			}

			if hostResult.Stdout != "" {
				fmt.Printf("%s\n", hostResult.Stdout)
			}
		}
	}

	// 返回码
	if !result.IsAllSuccess() {
		os.Exit(1)
	}
}

func runInventory(args []string) {
	var inventoryPath, inventoryPathLong string
	var hostName string
	var list bool

	// 手动解析参数
	for i := 0; i < len(args); i++ {
		arg := args[i]

		// 处理 -i 参数
		if arg == "-i" || arg == "--inventory" {
			if i+1 < len(args) {
				i++
				if arg == "-i" {
					inventoryPath = args[i]
				} else {
					inventoryPathLong = args[i]
				}
			}
			continue
		}

		// 处理 --host 参数
		if arg == "--host" {
			if i+1 < len(args) {
				i++
				hostName = args[i]
			}
			continue
		}

		// 处理 --list 标志
		if arg == "--list" {
			list = true
			continue
		}

		// 非标志参数
		if !strings.HasPrefix(arg, "-") {
			if arg == "list" {
				list = true
			} else if hostName == "" && arg != "list" {
				hostName = arg
			}
		}
	}

	invPath := inventoryPath
	if invPath == "" {
		invPath = inventoryPathLong
	}

	if invPath == "" {
		fmt.Fprintln(os.Stderr, "Error: inventory file is required (-i)")
		fmt.Fprintln(os.Stderr, "Usage: go-ansible inventory -i <inventory> [--list|--host <name>]")
		os.Exit(1)
	}

	inv, err := inventory.ParseFile(invPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing inventory: %v\n", err)
		os.Exit(1)
	}

	if list || (hostName == "") {
		// 列出所有主机和组
		fmt.Println("Groups:")
		for name, group := range inv.Groups {
			fmt.Printf("  [%s]\n", name)
			for _, h := range group.Hosts {
				fmt.Printf("    %s (%s:%d)\n", h.Name, h.Address, h.Port)
			}
			if len(group.Children) > 0 {
				fmt.Println("  :children")
				for _, child := range group.Children {
					fmt.Printf("    %s\n", child.Name)
				}
			}
			if len(group.Variables) > 0 {
				fmt.Println("  :vars")
				for k, v := range group.Variables {
					fmt.Printf("    %s=%s\n", k, v)
				}
			}
			fmt.Println()
		}
	}

	if hostName != "" {
		// 显示主机信息
		h, err := inv.GetHost(hostName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Host not found: %s\n", hostName)
			os.Exit(1)
		}

		fmt.Printf("Host: %s\n", h.Name)
		fmt.Printf("  Address: %s\n", h.Address)
		fmt.Printf("  Port: %d\n", h.Port)
		fmt.Printf("  User: %s\n", h.User)

		// 显示认证方式
		if h.PrivateKey != "" {
			fmt.Printf("  PrivateKey: %s\n", h.PrivateKey)
		} else if h.Password != "" {
			fmt.Printf("  Auth: Password\n")
		} else {
			fmt.Printf("  Auth: Default (SSH Agent or Default Keys)\n")
		}

		if len(h.Variables) > 0 {
			fmt.Println("  Variables:")
			for k, v := range h.Variables {
				fmt.Printf("    %s=%s\n", k, v)
			}
		}
	}
}

// formatItem 格式化 item 信息用于显示
func formatItem(item interface{}) string {
	switch v := item.(type) {
	case map[string]interface{}:
		// 对于 map，显示 key=value 格式
		parts := make([]string, 0, len(v))
		for key, val := range v {
			parts = append(parts, fmt.Sprintf("'%s': '%v'", key, val))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case map[interface{}]interface{}:
		parts := make([]string, 0, len(v))
		for key, val := range v {
			parts = append(parts, fmt.Sprintf("'%v': '%v'", key, val))
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
