package cli

import (
	"fmt"
	"os"
	"strings"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/playbook"
)

func runPlaybook(args []string) {
	var inventoryPath, inventoryPathLong, tags string
	var check, verbose bool
	var extraVars []string
	var playbookPath string

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

		if arg == "-t" || arg == "--tags" {
			if i+1 < len(args) {
				i++
				tags = args[i]
			}
			continue
		}

		if arg == "-e" || arg == "--extra-vars" {
			if i+1 < len(args) {
				i++
				extraVars = append(extraVars, args[i])
			}
			continue
		}

		if arg == "--check" {
			check = true
			continue
		}

		if arg == "-v" || arg == "--verbose" {
			verbose = true
			continue
		}

		if !strings.HasPrefix(arg, "-") && playbookPath == "" {
			playbookPath = arg
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

	fmt.Println("Creating executor...")
	executor := playbook.NewExecutor(inv)
	executor.SetVerbose(verbose)
	defer executor.Close()

	if len(vars) > 0 {
		fmt.Printf("Setting extra vars: %d\n", len(vars))
		executor.SetExtraVars(vars)
	}

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

	for _, play := range result.Plays {
		playName := play.Name
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
