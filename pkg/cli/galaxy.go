package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type GalaxyOptions struct {
	InitPath     string
	RolesPath    string
	RoleFile     string
	Force        bool
	IgnoreErrors bool
}

type GalaxyRoleRequirement struct {
	Src     string `yaml:"src" json:"src"`
	Name    string `yaml:"name" json:"name"`
	Version string `yaml:"version" json:"version"`
	SCM     string `yaml:"scm" json:"scm"`
}

func NewGalaxyCmd(version string) *cobra.Command {
	opts := &GalaxyOptions{
		InitPath:  ".",
		RolesPath: "./roles",
	}

	cmd := &cobra.Command{
		Use:   "ansible-galaxy [command] [options]",
		Short: "Perform various Role and Collection related operations",
		Long: `ansible-galaxy - Perform various Role and Collection related operations.
Supports scaffolding standard Ansible role directory structures (init) and installing roles (install).`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	roleCmd := &cobra.Command{
		Use:   "role [command]",
		Short: "Manage roles",
	}

	// 1. init command
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

	// 2. install command
	installCmd := &cobra.Command{
		Use:   "install [role_name...] [options]",
		Short: "Install roles from file(s) or specified role names/URLs",
		RunE: func(c *cobra.Command, args []string) error {
			return runGalaxyRoleInstall(args, opts)
		},
	}
	installCmd.Flags().StringVarP(&opts.RolesPath, "roles-path", "p", "./roles", "The path to the directory containing your roles. Default is ./roles")
	installCmd.Flags().StringVarP(&opts.RoleFile, "role-file", "r", "", "A file containing a list of roles to be installed.")
	installCmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Force overwriting an existing role.")
	installCmd.Flags().BoolVar(&opts.IgnoreErrors, "ignore-errors", false, "Ignore errors and continue with the next role.")

	// 3. list command
	listCmd := &cobra.Command{
		Use:   "list [options]",
		Short: "Show the name and version of each role installed in the roles_path",
		RunE: func(c *cobra.Command, args []string) error {
			return runGalaxyRoleList(opts)
		},
	}
	listCmd.Flags().StringVarP(&opts.RolesPath, "roles-path", "p", "./roles", "The path to the directory containing your roles. Default is ./roles")

	// 4. info command
	infoCmd := &cobra.Command{
		Use:   "info <role_name> [options]",
		Short: "View more details about a specific role",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runGalaxyRoleInfo(args[0], opts)
		},
	}
	infoCmd.Flags().StringVarP(&opts.RolesPath, "roles-path", "p", "./roles", "The path to the directory containing your roles. Default is ./roles")

	// 5. remove / delete command
	removeCmd := &cobra.Command{
		Use:     "remove [role_name...] [options]",
		Aliases: []string{"delete"},
		Short:   "Delete roles from the roles_path",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runGalaxyRoleRemove(args, opts)
		},
	}
	removeCmd.Flags().StringVarP(&opts.RolesPath, "roles-path", "p", "./roles", "The path to the directory containing your roles. Default is ./roles")

	roleCmd.AddCommand(initCmd)
	roleCmd.AddCommand(installCmd)
	roleCmd.AddCommand(listCmd)
	roleCmd.AddCommand(infoCmd)
	roleCmd.AddCommand(removeCmd)
	cmd.AddCommand(roleCmd)

	// Direct shortcuts (ansible-galaxy init / ansible-galaxy install / list / info)
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

	directInstallCmd := &cobra.Command{
		Use:   "install [role_name...] [options]",
		Short: "Install roles from file(s) or specified role names/URLs",
		RunE: func(c *cobra.Command, args []string) error {
			return runGalaxyRoleInstall(args, opts)
		},
	}
	directInstallCmd.Flags().StringVarP(&opts.RolesPath, "roles-path", "p", "./roles", "The path to the directory containing your roles. Default is ./roles")
	directInstallCmd.Flags().StringVarP(&opts.RoleFile, "role-file", "r", "", "A file containing a list of roles to be installed.")
	directInstallCmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Force overwriting an existing role.")
	directInstallCmd.Flags().BoolVar(&opts.IgnoreErrors, "ignore-errors", false, "Ignore errors and continue with the next role.")
	cmd.AddCommand(directInstallCmd)

	directListCmd := &cobra.Command{
		Use:   "list [options]",
		Short: "Show the name and version of each role installed in the roles_path",
		RunE: func(c *cobra.Command, args []string) error {
			return runGalaxyRoleList(opts)
		},
	}
	directListCmd.Flags().StringVarP(&opts.RolesPath, "roles-path", "p", "./roles", "The path to the directory containing your roles. Default is ./roles")
	cmd.AddCommand(directListCmd)

	directInfoCmd := &cobra.Command{
		Use:   "info <role_name> [options]",
		Short: "View more details about a specific role",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runGalaxyRoleInfo(args[0], opts)
		},
	}
	directInfoCmd.Flags().StringVarP(&opts.RolesPath, "roles-path", "p", "./roles", "The path to the directory containing your roles. Default is ./roles")
	cmd.AddCommand(directInfoCmd)

	directRemoveCmd := &cobra.Command{
		Use:     "remove [role_name...] [options]",
		Aliases: []string{"delete"},
		Short:   "Delete roles from the roles_path",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runGalaxyRoleRemove(args, opts)
		},
	}
	directRemoveCmd.Flags().StringVarP(&opts.RolesPath, "roles-path", "p", "./roles", "The path to the directory containing your roles. Default is ./roles")
	cmd.AddCommand(directRemoveCmd)

	return cmd
}

func runGalaxyRoleRemove(roleNames []string, opts *GalaxyOptions) error {
	rolesPath := opts.RolesPath
	if rolesPath == "" {
		rolesPath = "./roles"
	}

	for _, name := range roleNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		targetDir := filepath.Join(rolesPath, name)
		info, err := os.Stat(targetDir)
		if os.IsNotExist(err) {
			fmt.Printf("- %s is not installed, skipping.\n", name)
			continue
		}
		if err != nil {
			return fmt.Errorf("stat role directory %s: %w", targetDir, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", targetDir)
		}
		if err := os.RemoveAll(targetDir); err != nil {
			return fmt.Errorf("failed to remove role %s: %w", name, err)
		}
		fmt.Printf("- successfully removed %s\n", name)
	}
	return nil
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

func runGalaxyRoleInstall(args []string, opts *GalaxyOptions) error {
	var reqs []GalaxyRoleRequirement

	if opts.RoleFile != "" {
		parsed, err := parseRequirementsFile(opts.RoleFile)
		if err != nil {
			return err
		}
		reqs = append(reqs, parsed...)
	}

	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			continue
		}
		reqs = append(reqs, GalaxyRoleRequirement{
			Src:  arg,
			Name: extractRoleName(arg),
		})
	}

	if len(reqs) == 0 {
		return errors.New("no roles specified to install. Provide role names or use -r / --role-file")
	}

	rolesPath := opts.RolesPath
	if rolesPath == "" {
		rolesPath = "./roles"
	}

	for _, req := range reqs {
		if err := installSingleRole(req, rolesPath, opts.Force); err != nil {
			if opts.IgnoreErrors {
				fmt.Fprintf(os.Stderr, "failed to install %s: %v\n", req.Name, err)
				continue
			}
			return err
		}
	}

	return nil
}

func parseRequirementsFile(path string) ([]GalaxyRoleRequirement, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading role requirements file %s: %w", path, err)
	}

	// Try standard list of struct
	var listReqs []GalaxyRoleRequirement
	if err := yaml.Unmarshal(data, &listReqs); err == nil && len(listReqs) > 0 && listReqs[0].Src != "" {
		for i := range listReqs {
			if listReqs[i].Name == "" {
				listReqs[i].Name = extractRoleName(listReqs[i].Src)
			}
		}
		return listReqs, nil
	}

	// Try generic interface representation
	var root interface{}
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parsing requirements yaml: %w", err)
	}

	var items []interface{}
	switch v := root.(type) {
	case []interface{}:
		items = v
	case map[string]interface{}:
		if rolesList, ok := v["roles"].([]interface{}); ok {
			items = rolesList
		}
	}

	var parsed []GalaxyRoleRequirement
	for _, item := range items {
		switch it := item.(type) {
		case string:
			parsed = append(parsed, GalaxyRoleRequirement{
				Src:  it,
				Name: extractRoleName(it),
			})
		case map[string]interface{}:
			src, _ := it["src"].(string)
			name, _ := it["name"].(string)
			version, _ := it["version"].(string)
			scm, _ := it["scm"].(string)
			if name == "" && src != "" {
				name = extractRoleName(src)
			}
			parsed = append(parsed, GalaxyRoleRequirement{
				Src:     src,
				Name:    name,
				Version: version,
				SCM:     scm,
			})
		}
	}

	return parsed, nil
}

func extractRoleName(src string) string {
	src = strings.TrimSpace(src)
	src = strings.TrimSuffix(src, ".git")
	src = strings.TrimSuffix(src, "/")

	if idx := strings.LastIndex(src, "/"); idx != -1 {
		src = src[idx+1:]
	} else if idx := strings.LastIndex(src, ":"); idx != -1 {
		src = src[idx+1:]
	} else if idx := strings.LastIndex(src, "."); idx != -1 {
		src = src[idx+1:]
	}

	if strings.HasPrefix(src, "ansible-role-") {
		return strings.TrimPrefix(src, "ansible-role-")
	}
	return src
}

func installSingleRole(req GalaxyRoleRequirement, rolesPath string, force bool) error {
	if req.Src == "" {
		return fmt.Errorf("empty role src")
	}
	roleName := req.Name
	if roleName == "" {
		roleName = extractRoleName(req.Src)
	}

	targetDir := filepath.Join(rolesPath, roleName)

	if fi, err := os.Stat(targetDir); err == nil && fi.IsDir() {
		if !force {
			fmt.Printf("- %s is already installed, skipping...\n", roleName)
			return nil
		}
		if err := os.RemoveAll(targetDir); err != nil {
			return fmt.Errorf("failed to remove existing role %s: %w", targetDir, err)
		}
	}

	if err := os.MkdirAll(rolesPath, 0755); err != nil {
		return fmt.Errorf("failed to create roles directory %s: %w", rolesPath, err)
	}

	// 1. If src is a local directory, copy it directly
	if fi, err := os.Stat(req.Src); err == nil && fi.IsDir() {
		fmt.Printf("- extracting %s from local %s to %s\n", roleName, req.Src, targetDir)
		if err := copyDir(req.Src, targetDir); err != nil {
			return fmt.Errorf("copy local role: %w", err)
		}
		fmt.Printf("- %s was installed successfully\n", roleName)
		return nil
	}

	// 2. Git Clone from remote repository
	gitURL := resolveGitURL(req.Src)
	fmt.Printf("- extracting %s to %s\n", roleName, targetDir)

	gitArgs := []string{"clone"}
	if req.Version != "" {
		gitArgs = append(gitArgs, "-b", req.Version, "--depth", "1", gitURL, targetDir)
	} else {
		gitArgs = append(gitArgs, "--depth", "1", gitURL, targetDir)
	}

	cmd := exec.Command("git", gitArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Fallback without depth limit
		fbCmd := exec.Command("git", "clone", gitURL, targetDir)
		if fbOut, fbErr := fbCmd.CombinedOutput(); fbErr != nil {
			return fmt.Errorf("failed to clone %s: %s (fallback: %s)", gitURL, string(out), string(fbOut))
		}
		if req.Version != "" {
			coCmd := exec.Command("git", "-C", targetDir, "checkout", req.Version)
			_ = coCmd.Run()
		}
	}

	fmt.Printf("- %s was installed successfully\n", roleName)
	return nil
}

func resolveGitURL(src string) string {
	src = strings.TrimSpace(src)
	src = strings.TrimPrefix(src, "git+")
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "git@") || strings.HasPrefix(src, "ssh://") {
		return src
	}
	if strings.Contains(src, ".") && !strings.Contains(src, "/") {
		parts := strings.SplitN(src, ".", 2)
		return fmt.Sprintf("https://github.com/%s/ansible-role-%s.git", parts[0], parts[1])
	}
	if strings.Contains(src, "/") && !strings.Contains(src, ":") {
		return fmt.Sprintf("https://github.com/%s.git", src)
	}
	return src
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

func runGalaxyRoleList(opts *GalaxyOptions) error {
	rolesPath := opts.RolesPath
	if rolesPath == "" {
		rolesPath = "./roles"
	}
	absPath, err := filepath.Abs(rolesPath)
	if err != nil {
		absPath = rolesPath
	}
	fmt.Printf("# %s\n", absPath)
	entries, err := os.ReadDir(rolesPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		roleName := entry.Name()
		version := "(unknown version)"
		metaFile := filepath.Join(rolesPath, roleName, "meta", "main.yml")
		if data, err := os.ReadFile(metaFile); err == nil {
			var meta map[string]interface{}
			if err := yaml.Unmarshal(data, &meta); err == nil {
				if gInfo, ok := meta["galaxy_info"].(map[string]interface{}); ok {
					if v, ok := gInfo["version"].(string); ok && v != "" {
						version = v
					}
				}
			}
		}
		fmt.Printf("- %s, %s\n", roleName, version)
	}
	return nil
}

func runGalaxyRoleInfo(roleName string, opts *GalaxyOptions) error {
	rolesPath := opts.RolesPath
	if rolesPath == "" {
		rolesPath = "./roles"
	}
	roleDir := filepath.Join(rolesPath, roleName)
	if fi, err := os.Stat(roleDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("role %s was not found in %s", roleName, rolesPath)
	}
	absDir, _ := filepath.Abs(roleDir)
	fmt.Printf("- role: %s\n", roleName)
	fmt.Printf("  path: %s\n", absDir)
	metaFile := filepath.Join(roleDir, "meta", "main.yml")
	if data, err := os.ReadFile(metaFile); err == nil {
		var meta map[string]interface{}
		if err := yaml.Unmarshal(data, &meta); err == nil {
			if gInfo, ok := meta["galaxy_info"].(map[string]interface{}); ok {
				if author, ok := gInfo["author"].(string); ok {
					fmt.Printf("  author: %s\n", author)
				}
				if desc, ok := gInfo["description"].(string); ok {
					fmt.Printf("  description: %s\n", desc)
				}
				if license, ok := gInfo["license"].(string); ok {
					fmt.Printf("  license: %s\n", license)
				}
			}
		}
	}
	return nil
}

func RunGalaxy(args []string, version string) {
	fmt.Fprintln(os.Stderr, "Reimplementation and reconstruction of the main features in Python version")
	cmd := NewGalaxyCmd(version)
	cmd.SetArgs(normalizeAnsibleArgs(args))
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
