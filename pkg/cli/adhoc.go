package cli

import (
	"fmt"
	"os"
	"strings"

	"go-ansible/pkg/adhoc"
	"go-ansible/pkg/inventory"
)

func runAdhoc(args []string) {
	var inventoryPath, inventoryPathLong string
	var module, moduleLong string = "command", "command"
	var moduleArgs, moduleArgsLong string
	var forks int = 5
	var verbose, verboseLong bool
	var become bool
	var becomeUser string = "root"
	var becomeMethod string = "sudo"
	var target string

	for i := 0; i < len(args); i++ {
		arg := args[i]

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

		if arg == "--forks" {
			if i+1 < len(args) {
				i++
				fmt.Sscanf(args[i], "%d", &forks)
			}
			continue
		}

		if arg == "-v" || arg == "--verbose" {
			if arg == "-v" {
				verbose = true
			} else {
				verboseLong = true
			}
			continue
		}

		if arg == "--become" || arg == "-become" || arg == "-b" {
			become = true
			continue
		}

		if arg == "--become-user" || arg == "-become-user" || arg == "-U" {
			if i+1 < len(args) {
				i++
				becomeUser = args[i]
			}
			continue
		}

		if arg == "--become-method" || arg == "-become-method" {
			if i+1 < len(args) {
				i++
				becomeMethod = args[i]
			}
			continue
		}

		if !strings.HasPrefix(arg, "-") && target == "" {
			target = arg
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

	inv, err := inventory.ParseFile(invPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing inventory: %v\n", err)
		os.Exit(1)
	}

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

	adhocExecutor := adhoc.NewAdhoc(inv, forks)
	defer adhocExecutor.Close()

	params := make(map[string]interface{})
	if modArgs != "" {
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

	result, err := adhocExecutor.Execute(target, modName, params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

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

	if !result.IsAllSuccess() {
		os.Exit(1)
	}
}
