package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go-ansible/pkg/adhoc"
	"go-ansible/pkg/inventory"
	"go-ansible/pkg/ssh"
)

type AnsibleOptions struct {
	InventoryPath     string
	InventoryPaths    []string
	Module            string
	ModuleArgs        string
	ExtraVars         []string
	Forks             int
	ForksSet          bool
	Limit             string
	RemoteUser        string
	Become            bool
	BecomeUser        string
	BecomeMethod      string
	Check             bool
	Diff              bool
	Verbose           int
	AskPass           bool
	AskBecomePass     bool
	AskVaultPass      bool
	VaultPasswordFile string
	VaultIDs          []string
	ListHosts         bool

	Connection    string
	Timeout       int
	PrivateKey    string
	SSHCommonArgs string
	SSHExtraArgs  string
	Background    int
	Poll          int
	OneLine       bool
	Tree          string

	Target string
}

func runAdhoc(args []string) {
	RunAdhoc(args, "0.1.0")
}

func RunAdhoc(args []string, version string) {
	fmt.Println("Reimplementation and reconstruction of the main features in Python version")
	cmd := NewAnsibleCmd(version)
	cmd.SetArgs(normalizeAnsibleArgs(args))
	if err := cmd.Execute(); err != nil {
		if !errors.Is(err, errExecutionFailed) {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}
}

func NewAnsibleCmd(version string) *cobra.Command {
	if version == "" {
		version = "0.1.0"
	}
	opts := &AnsibleOptions{
		Module:       "command",
		BecomeUser:   "root",
		BecomeMethod: "sudo",
		Forks:        5,
		Poll:         15,
	}
	cmd := &cobra.Command{
		Use:           "ansible <target> [options]",
		Short:         "Define and run a single task 'playbook' against a set of hosts",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("target host/group is required\nUsage: ansible <target> -i <inventory> -m <module> -a <args> [options]")
			}
			opts.Target = args[0]
			opts.ExtraVars, _ = cmd.Flags().GetStringArray("extra-vars")
			opts.Verbose, _ = cmd.Flags().GetCount("verbose")
			opts.ForksSet = cmd.Flags().Changed("forks")
			return opts.Run()
		},
	}
	cmd.SetVersionTemplate("ansible {{.Version}}\n")

	flags := cmd.Flags()
	flags.VarP(newInventoryValue(&opts.InventoryPaths), "inventory", "i", "specify inventory host path(s) or comma separated host list")
	flags.StringVarP(&opts.Module, "module-name", "m", "command", "module name to execute")
	flags.StringVar(&opts.Module, "module", "command", "module name to execute")
	_ = flags.MarkHidden("module")
	flags.StringVarP(&opts.ModuleArgs, "args", "a", "", "module arguments")
	flags.StringArrayP("extra-vars", "e", nil, "set additional variables as key=value or YAML/JSON, if filename prepend with @")
	flags.IntVarP(&opts.Forks, "forks", "f", 5, "specify number of parallel processes to use")
	flags.StringVarP(&opts.Limit, "limit", "l", "", "further limit selected hosts to an additional pattern")
	flags.StringVarP(&opts.RemoteUser, "user", "u", "", "connect as this user")
	flags.BoolVarP(&opts.Become, "become", "b", false, "run operations with become (does not imply password prompting)")
	flags.StringVarP(&opts.BecomeUser, "become-user", "U", "root", "run operations as this user")
	flags.StringVar(&opts.BecomeMethod, "become-method", "sudo", "privilege escalation method to use")
	flags.BoolVarP(&opts.Check, "check", "C", false, "don't make any changes; instead, try to predict some of the changes that may occur")
	flags.BoolVarP(&opts.Diff, "diff", "D", false, "when changing (small) files and templates, show the differences in those files")
	flags.CountP("verbose", "v", "verbose mode (-v, -vv, -vvv)")
	flags.BoolVarP(&opts.AskPass, "ask-pass", "k", false, "ask for connection password")
	flags.BoolVarP(&opts.AskBecomePass, "ask-become-pass", "K", false, "ask for privilege escalation password")
	flags.BoolVar(&opts.AskVaultPass, "ask-vault-pass", false, "ask for vault password")
	flags.StringVar(&opts.VaultPasswordFile, "vault-password-file", "", "vault password file")
	flags.StringArrayVar(&opts.VaultIDs, "vault-id", nil, "the vault identity to use (can be specified multiple times)")
	flags.BoolVar(&opts.ListHosts, "list-hosts", false, "outputs a list of matching hosts; does not execute anything else")
	flags.StringVarP(&opts.Connection, "connection", "c", "", "connection type to use (default=smart)")
	flags.IntVarP(&opts.Timeout, "timeout", "T", 0, "override the SSH timeout in seconds")
	flags.StringVar(&opts.PrivateKey, "private-key", "", "use this file to authenticate the connection")
	flags.StringVar(&opts.PrivateKey, "key-file", "", "use this file to authenticate the connection")
	flags.StringVar(&opts.SSHCommonArgs, "ssh-common-args", "", "specify common arguments to pass to sftp/scp/ssh")
	flags.StringVar(&opts.SSHExtraArgs, "ssh-extra-args", "", "specify extra arguments to pass to ssh only")
	flags.IntVarP(&opts.Background, "background", "B", 0, "run asynchronously, failing after X seconds (default=N/A)")
	flags.IntVarP(&opts.Poll, "poll", "P", 15, "set the poll interval if using -B (default=15)")
	flags.BoolVarP(&opts.OneLine, "one-line", "o", false, "condense output")
	flags.StringVarP(&opts.Tree, "tree", "t", "", "log output to this directory")

	return cmd
}

func (opts *AnsibleOptions) Run() error {
	cfg := loadCLIConfig()

	invPaths := opts.InventoryPaths
	if len(invPaths) == 0 && opts.InventoryPath != "" {
		invPaths = []string{opts.InventoryPath}
	}
	if len(invPaths) == 0 && cfg.Defaults.Inventory != "" {
		invPaths = []string{cfg.ResolvePath(cfg.Defaults.Inventory)}
	}
	var inv *inventory.Inventory
	if len(invPaths) == 0 {
		fmt.Fprintf(os.Stderr, "[WARNING]: No inventory was parsed, only implicit localhost is available\n")
		inv = inventory.NewInventory()
		inv.EnsureLocalhost()
	} else {
		opts.InventoryPath = invPaths[0]
		var err error
		inv, err = inventory.ParseSources(invPaths...)
		if err != nil {
			return fmt.Errorf("parsing inventory: %w", err)
		}
	}

	forks := opts.Forks
	if !opts.ForksSet && cfg.Defaults.Forks > 0 {
		forks = cfg.Defaults.Forks
	}

	var defaultSSHPass string
	if opts.AskPass {
		pass, err := promptPassword("SSH password: ")
		if err != nil {
			return fmt.Errorf("reading SSH password: %w", err)
		}
		defaultSSHPass = pass
	}

	var defaultBecomePass string
	if opts.AskBecomePass {
		pass, err := promptPassword("BECOME password[defaults to SSH password]: ")
		if err != nil {
			return fmt.Errorf("reading become password: %w", err)
		}
		if pass == "" && defaultSSHPass != "" {
			pass = defaultSSHPass
		}
		defaultBecomePass = pass
	}
	applyConfigDefaultsToInventory(inv, cfg)
	applyConnectionOptionsToInventory(inv, ConnectionOptions{
		Connection:    opts.Connection,
		Timeout:       opts.Timeout,
		PrivateKey:    opts.PrivateKey,
		SSHCommonArgs: opts.SSHCommonArgs,
		SSHExtraArgs:  opts.SSHExtraArgs,
	})

	resolvedLimit, err := resolveLimitPattern(opts.Limit)
	if err != nil {
		return fmt.Errorf("resolving limit: %w", err)
	}
	opts.Limit = resolvedLimit

	if opts.RemoteUser != "" {
		for _, host := range inv.AllHosts {
			host.User = opts.RemoteUser
		}
	}
	if defaultSSHPass != "" {
		for _, host := range inv.AllHosts {
			if host.Password == "" {
				host.Password = defaultSSHPass
			}
		}
	}

	if opts.Become {
		for _, host := range inv.AllHosts {
			host.Become = true
			if opts.BecomeUser != "" {
				host.BecomeUser = opts.BecomeUser
			}
			if opts.BecomeMethod != "" {
				host.BecomeMethod = opts.BecomeMethod
			}
			if defaultBecomePass != "" && host.BecomePass == "" {
				host.BecomePass = defaultBecomePass
			}
		}
	}

	sshCfg := sshClientConfigFromDefaults(cfg)
	if opts.Timeout > 0 {
		if sshCfg == nil {
			sshCfg = ssh.DefaultConfig()
		}
		sshCfg.Timeout = time.Duration(opts.Timeout) * time.Second
	}
	adhocExecutor := adhoc.NewAdhocWithConfig(inv, forks, sshCfg)
	adhocExecutor.SetLimit(opts.Limit)
	adhocExecutor.SetCheckMode(opts.Check)
	adhocExecutor.SetDiffMode(opts.Diff)
	adhocExecutor.SetAsync(opts.Background, opts.Poll)
	defer adhocExecutor.Close()

	if opts.ListHosts {
		hosts, err := adhocExecutor.ListHosts(opts.Target)
		if err != nil {
			return fmt.Errorf("listing hosts: %w", err)
		}
		printAdhocHosts(opts.Target, opts.Limit, hosts)
		return nil
	}

	vaultSecrets, err := resolveVaultSecrets(cfg, opts.VaultPasswordFile, opts.VaultIDs, opts.AskVaultPass)
	if err != nil {
		return fmt.Errorf("loading vault password: %w", err)
	}

	params := make(map[string]interface{})
	if opts.ModuleArgs != "" {
		params = parseModuleArgs(opts.ModuleArgs)
	}

	vars := make(map[string]interface{})
	for _, ev := range opts.ExtraVars {
		parsed, err := parseExtraVarsWithVaultSecrets(ev, vaultSecrets)
		if err != nil {
			return fmt.Errorf("parsing extra vars: %w", err)
		}
		for k, v := range parsed {
			vars[k] = v
		}
	}
	if len(vars) > 0 {
		adhocExecutor.SetExtraVars(vars)
	}

	if opts.Check {
		fmt.Println("Running in check mode...")
	}
	if opts.Diff {
		fmt.Println("Running with diff output...")
	}

	isVerbose := opts.Verbose > 0
	if isVerbose {
		fmt.Printf("[DEBUG] Inventory hosts: %d\n", len(inv.AllHosts))
		for name, host := range inv.AllHosts {
			fmt.Printf("[DEBUG]   Host: %s, User: %s, Become: %v\n", name, host.User, host.Become)
		}
		fmt.Printf("[DEBUG] Module: %s, Args: %+v\n", opts.Module, params)
	}

	result, err := adhocExecutor.Execute(opts.Target, opts.Module, params)
	if err != nil {
		return fmt.Errorf("executing adhoc: %w", err)
	}

	if opts.Tree != "" {
		if treeErr := writeAdhocTreeResults(opts.Tree, result.Hosts); treeErr != nil {
			fmt.Fprintf(os.Stderr, "Error writing tree output: %v\n", treeErr)
		}
	}

	if opts.OneLine {
		for _, hostName := range sortedCLIAdhocHostNames(result.Hosts) {
			hostResult := result.Hosts[hostName]
			status := "SUCCESS"
			if hostResult.Failed {
				status = "FAILED"
			} else if hostResult.Changed {
				status = "CHANGED"
			}
			payload := make(map[string]interface{})
			payload["changed"] = hostResult.Changed
			if hostResult.ExitCode != 0 {
				payload["rc"] = hostResult.ExitCode
			}
			if hostResult.Stdout != "" {
				payload["stdout"] = hostResult.Stdout
			}
			if hostResult.Stderr != "" {
				payload["stderr"] = hostResult.Stderr
			}
			if hostResult.Message != "" {
				payload["msg"] = hostResult.Message
			}
			for k, v := range hostResult.Data {
				payload[k] = v
			}
			raw, _ := json.Marshal(payload)
			fmt.Printf("%s | %s => %s\n", hostName, status, string(raw))
		}
	} else if isVerbose {
		fmt.Println(result.FormatResult())
	} else {
		for _, hostName := range sortedCLIAdhocHostNames(result.Hosts) {
			hostResult := result.Hosts[hostName]
			fmt.Printf("%s | ", hostName)
			if hostResult.Failed {
				fmt.Printf("FAILED | %s\n", hostResult.Message)
			} else if hostResult.Changed {
				fmt.Printf("CHANGED\n")
			} else {
				fmt.Printf("SUCCESS\n")
			}

			if hostResult.Message != "" && !hostResult.Failed {
				fmt.Printf("msg: %s\n", hostResult.Message)
			}
			if hostResult.Stdout != "" {
				fmt.Printf("%s\n", hostResult.Stdout)
			}
			if hostResult.Stderr != "" {
				fmt.Printf("STDERR: %s\n", hostResult.Stderr)
			}
			if hostResult.Diff != "" {
				fmt.Printf("DIFF:\n%s\n", hostResult.Diff)
			}
			if len(hostResult.Data) > 0 {
				fmt.Printf("DATA:\n%s\n", formatCLIResultData(hostResult.Data))
			}
		}
	}

	if !result.IsAllSuccess() {
		return errExecutionFailed
	}
	return nil
}

func printAdhocHosts(target, limit string, hosts []string) {
	fmt.Printf("adhoc target: %s\n", target)
	if strings.TrimSpace(limit) != "" {
		fmt.Printf("limit: %s\n", limit)
	}
	fmt.Printf("hosts (%d):\n", len(hosts))
	for _, host := range hosts {
		fmt.Printf("  %s\n", host)
	}
}

func formatCLIResultData(data map[string]interface{}) string {
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", data)
	}
	return string(encoded)
}

func sortedCLIAdhocHostNames(hosts map[string]*adhoc.HostResult) []string {
	names := make([]string, 0, len(hosts))
	for name := range hosts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func writeAdhocTreeResults(treeDir string, hosts map[string]*adhoc.HostResult) error {
	if treeDir == "" {
		return nil
	}
	expanded := expandCLIPath(treeDir)
	if err := os.MkdirAll(expanded, 0o755); err != nil {
		return fmt.Errorf("creating tree directory %s: %w", expanded, err)
	}

	for hostName, hostResult := range hosts {
		payload := make(map[string]interface{})
		payload["changed"] = hostResult.Changed
		payload["rc"] = hostResult.ExitCode
		if hostResult.Failed {
			payload["failed"] = true
		}
		if hostResult.Message != "" {
			payload["msg"] = hostResult.Message
		}
		if hostResult.Stdout != "" {
			payload["stdout"] = hostResult.Stdout
		}
		if hostResult.Stderr != "" {
			payload["stderr"] = hostResult.Stderr
		}
		if hostResult.Diff != "" {
			payload["diff"] = hostResult.Diff
		}
		for k, v := range hostResult.Data {
			payload[k] = v
		}

		data, err := json.MarshalIndent(payload, "", "    ")
		if err != nil {
			return fmt.Errorf("marshaling result for host %s: %w", hostName, err)
		}

		filePath := filepath.Join(expanded, hostName)
		if err := os.WriteFile(filePath, append(data, '\n'), 0o644); err != nil {
			return fmt.Errorf("writing tree file for host %s: %w", hostName, err)
		}
	}
	return nil
}
