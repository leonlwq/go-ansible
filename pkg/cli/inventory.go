package cli

import (
	"fmt"
	"os"
	"strings"

	"go-ansible/pkg/inventory"
)

func runInventory(args []string) {
	var inventoryPath, inventoryPathLong string
	var hostName string
	var list bool

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

		if arg == "--host" {
			if i+1 < len(args) {
				i++
				hostName = args[i]
			}
			continue
		}

		if arg == "--list" {
			list = true
			continue
		}

		if !strings.HasPrefix(arg, "-") {
			if arg == "list" {
				list = true
			} else if hostName == "" {
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

	if list || hostName == "" {
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

	if hostName == "" {
		return
	}

	h, err := inv.GetHost(hostName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Host not found: %s\n", hostName)
		os.Exit(1)
	}

	fmt.Printf("Host: %s\n", h.Name)
	fmt.Printf("  Address: %s\n", h.Address)
	fmt.Printf("  Port: %d\n", h.Port)
	fmt.Printf("  User: %s\n", h.User)

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
