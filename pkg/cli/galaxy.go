package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

type GalaxyOptions struct {
	InitPath string
	Force    bool
}

func NewGalaxyCmd(version string) *cobra.Command {
	opts := &GalaxyOptions{
		InitPath: ".",
	}

	cmd := &cobra.Command{
		Use:   "ansible-galaxy [command] [options]",
		Short: "Perform various Role and Collection related operations",
		Long: `ansible-galaxy - Perform various Role and Collection related operations.
Supports scaffolding standard Ansible role directory structures (init).`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	roleCmd := &cobra.Command{
		Use:   "role [command]",
		Short: "Manage roles",
	}

	initCmd := &cobra.Command{
		Use:   "init <role_name> [options]",
		Short: "Initialize a new role with the standard directory layout",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runGalaxyRoleInit(args[0], opts)
		},
	}
	initCmd.Flags().StringVar(&opts.InitPath, "init-path", ".", "The path in which the skeleton role will be created. The default is the current working directory.")
	initCmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Force overwriting an existing role or files.")

	roleCmd.AddCommand(initCmd)
	cmd.AddCommand(roleCmd)

	directInitCmd := &cobra.Command{
		Use:   "init <role_name> [options]",
		Short: "Initialize a new role with the standard directory layout",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runGalaxyRoleInit(args[0], opts)
		},
	}
	directInitCmd.Flags().StringVar(&opts.InitPath, "init-path", ".", "The path in which the skeleton role will be created. The default is the current working directory.")
	directInitCmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Force overwriting an existing role or files.")
	cmd.AddCommand(directInitCmd)

	return cmd
}

func runGalaxyRoleInit(roleName string, opts *GalaxyOptions) error {
	if roleName == "" {
		return errors.New("role name cannot be empty")
	}

	baseDir := opts.InitPath
	if baseDir == "" {
		baseDir = "."
	}
	targetDir := filepath.Join(baseDir, roleName)

	if fi, err := os.Stat(targetDir); err == nil && fi.IsDir() {
		if !opts.Force {
			return fmt.Errorf("the directory %s already exists. Use --force to overwrite", targetDir)
		}
	}

	dirs := []string{
		filepath.Join(targetDir, "defaults"),
		filepath.Join(targetDir, "files"),
		filepath.Join(targetDir, "handlers"),
		filepath.Join(targetDir, "meta"),
		filepath.Join(targetDir, "tasks"),
		filepath.Join(targetDir, "templates"),
		filepath.Join(targetDir, "tests"),
		filepath.Join(targetDir, "vars"),
	}

	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", d, err)
		}
	}

	files := map[string]string{
		filepath.Join(targetDir, "README.md"): fmt.Sprintf(`# %s

A brief description of the role goes here.

## Requirements

Any pre-requisites that may not be covered by Ansible itself or the role should be mentioned here.

## Role Variables

A description of the settable variables for this role should go here.

## Dependencies

A list of other roles hosted on Galaxy should go here.

## Example Playbook

    - hosts: servers
      roles:
         - { role: %s, x: 42 }

## License

BSD / MIT / Apache-2.0
`, roleName, roleName),

		filepath.Join(targetDir, "defaults", "main.yml"): fmt.Sprintf("---\n# defaults file for %s\n", roleName),
		filepath.Join(targetDir, "handlers", "main.yml"): fmt.Sprintf("---\n# handlers file for %s\n", roleName),
		filepath.Join(targetDir, "meta", "main.yml"): fmt.Sprintf(`galaxy_info:
  author: your name
  description: role description
  company: your company (optional)
  license: MIT
  min_ansible_version: 2.9

  platforms:
    - name: GenericLinux
      versions:
        - all

  galaxy_tags: []

dependencies: []
`),
		filepath.Join(targetDir, "tasks", "main.yml"):  fmt.Sprintf("---\n# tasks file for %s\n", roleName),
		filepath.Join(targetDir, "vars", "main.yml"):   fmt.Sprintf("---\n# vars file for %s\n", roleName),
		filepath.Join(targetDir, "tests", "inventory"): "localhost ansible_connection=local\n",
		filepath.Join(targetDir, "tests", "test.yml"): fmt.Sprintf(`---
- hosts: localhost
  remote_user: root
  roles:
    - %s
`, roleName),
	}

	for path, content := range files {
		if !opts.Force {
			if _, err := os.Stat(path); err == nil {
				continue
			}
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("failed to write file %s: %w", path, err)
		}
	}

	fmt.Printf("- Role %s was created successfully\n", roleName)
	return nil
}

func RunGalaxy(args []string, version string) {
	fmt.Println("Reimplementation and reconstruction of the main features in Python version")
	cmd := NewGalaxyCmd(version)
	cmd.SetArgs(normalizeAnsibleArgs(args))
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
