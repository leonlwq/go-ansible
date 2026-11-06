package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go-ansible/pkg/cli"
)

var version = "0.1.0"

func main() {
	progName := filepath.Base(os.Args[0])
	progName = strings.TrimSuffix(progName, ".exe")

	// 1. Multicall dispatch via binary / symlink name
	switch progName {
	case "ansible":
		cli.RunAdhoc(os.Args[1:], version)
		return
	case "ansible-playbook":
		cli.RunPlaybook(os.Args[1:], version)
		return
	case "ansible-inventory":
		cli.RunInventory(os.Args[1:], version)
		return
	case "ansible-vault":
		cli.RunVault(os.Args[1:], version)
		return
	case "ansible-doc":
		cli.RunDoc(os.Args[1:], version)
		return
	case "ansible-config":
		cli.RunConfig(os.Args[1:], version)
		return
	case "ansible-galaxy":
		cli.RunGalaxy(os.Args[1:], version)
		return
	case "ansible-pull":
		cli.RunPull(os.Args[1:], version)
		return
	}

	// 2. Multicall dispatch via first argument
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	subCmd := strings.ToLower(strings.TrimSpace(os.Args[1]))
	args := os.Args[2:]

	switch subCmd {
	case "playbook", "ansible-playbook":
		cli.RunPlaybook(args, version)
	case "inventory", "ansible-inventory":
		cli.RunInventory(args, version)
	case "vault", "ansible-vault":
		cli.RunVault(args, version)
	case "doc", "ansible-doc":
		cli.RunDoc(args, version)
	case "config", "ansible-config":
		cli.RunConfig(args, version)
	case "galaxy", "ansible-galaxy":
		cli.RunGalaxy(args, version)
	case "pull", "ansible-pull":
		cli.RunPull(args, version)
	case "adhoc", "ansible":
		cli.RunAdhoc(args, version)
	case "version", "--version", "-v":
		fmt.Printf("go-ansible %s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		// Default fallback: if first arg is an inventory pattern or adhoc flag, execute adhoc
		if strings.HasPrefix(subCmd, "-") {
			cli.RunAdhoc(os.Args[1:], version)
		} else {
			// Could be adhoc pattern: go-ansible all -m ping
			cli.RunAdhoc(os.Args[1:], version)
		}
	}
}

func printUsage() {
	fmt.Printf(`go-ansible %s - High performance, Pythonless Ansible orchestrator in Go

Usage:
  go-ansible <command> [arguments...]

Available Commands:
  playbook    Run Ansible playbooks (ansible-playbook equivalent)
  inventory   Query and export host inventory (ansible-inventory equivalent)
  vault       Manage encrypted files and variables (ansible-vault equivalent)
  adhoc       Run ad-hoc tasks against managed hosts (ansible equivalent)
  doc         Display module documentation (ansible-doc equivalent)
  config      View and manage Ansible configuration (ansible-config equivalent)
  galaxy      Manage Ansible roles and collections (ansible-galaxy equivalent)
  pull        Pulls playbooks from a VCS and runs them locally (ansible-pull equivalent)
  version     Show version information

Multicall Symlink Mode:
  Create symlinks named ansible, ansible-playbook, etc. pointing to go-ansible.
  The binary automatically behaves as the invoked tool.
`, version)
}
