package cli

import (
	"fmt"
	"os"
)

func Run(args []string, version string) {
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	switch args[0] {
	case "playbook":
		runPlaybook(args[1:])
	case "adhoc", "ad-hoc":
		runAdhoc(args[1:])
	case "inventory":
		runInventory(args[1:])
	case "version":
		fmt.Printf("go-ansible %s\n", version)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", args[0])
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
