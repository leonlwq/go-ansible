package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go-ansible/pkg/callback"
	"go-ansible/pkg/inventory"
	"go-ansible/pkg/playbook"
)

type PlaybookOptions struct {
	InventoryPath     string
	InventoryPaths    []string
	Tags              string
	SkipTags          string
	StartAtTask       string
	Limit             string
	ExtraVars         []string
	Forks             int
	RemoteUser        string
	Check             bool
	Diff              bool
	Verbose           int
	AskPass           bool
	AskBecomePass     bool
	AskVaultPass      bool
	VaultPasswordFile string
	VaultIDs          []string
	ListTasks         bool
	ListTags          bool
	ListHosts         bool
	SyntaxCheck       bool

	Connection    string
	Timeout       int
	PrivateKey    string
	SSHCommonArgs string
	SSHExtraArgs  string
	ForceHandlers bool
	Step          bool
	FlushCache    bool

	PlaybookPath string
}

func runPlaybook(args []string) {
	RunPlaybook(args, "0.1.0")
}

func RunPlaybook(args []string, version string) {
	fmt.Println("Reimplementation and reconstruction of the main features in Python version")
	cmd := NewPlaybookCmd(version)
	cmd.SetArgs(normalizeAnsibleArgs(args))
	if err := cmd.Execute(); err != nil {
		if !errors.Is(err, errExecutionFailed) {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}
}

func NewPlaybookCmd(version string) *cobra.Command {
	if version == "" {
		version = "0.1.0"
	}
	opts := &PlaybookOptions{}
	cmd := &cobra.Command{
		Use:           "ansible-playbook [options] playbook.yml",
		Short:         "Runs Ansible playbooks, executing the defined tasks on the targeted hosts.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("playbook file is required\nUsage: ansible-playbook <playbook.yml> -i <inventory> [options]")
			}
			opts.PlaybookPath = args[0]
			opts.ExtraVars, _ = cmd.Flags().GetStringArray("extra-vars")
			opts.Verbose, _ = cmd.Flags().GetCount("verbose")
			return opts.Run()
		},
	}
	cmd.SetVersionTemplate("ansible-playbook {{.Version}}\n")

	flags := cmd.Flags()
	flags.VarP(newInventoryValue(&opts.InventoryPaths), "inventory", "i", "specify inventory host path(s) or comma separated host list")
	flags.StringVarP(&opts.Tags, "tags", "t", "", "only run plays and tasks tagged with these values")
	flags.StringVar(&opts.SkipTags, "skip-tags", "", "only run plays and tasks whose tags do not match these values")
	flags.StringVar(&opts.StartAtTask, "start-at-task", "", "start the playbook at the task matching this name")
	flags.StringVarP(&opts.Limit, "limit", "l", "", "further limit selected hosts to an additional pattern")
	flags.StringArrayP("extra-vars", "e", nil, "set additional variables as key=value or YAML/JSON, if filename prepend with @")
	flags.IntVarP(&opts.Forks, "forks", "f", 0, "specify number of parallel processes to use")
	flags.StringVarP(&opts.RemoteUser, "user", "u", "", "connect as this user")
	flags.BoolVarP(&opts.Check, "check", "C", false, "don't make any changes; instead, try to predict some of the changes that may occur")
	flags.BoolVarP(&opts.Diff, "diff", "D", false, "when changing (small) files and templates, show the differences in those files")
	flags.CountP("verbose", "v", "verbose mode (-v, -vv, -vvv)")
	flags.BoolVarP(&opts.AskPass, "ask-pass", "k", false, "ask for connection password")
	flags.BoolVarP(&opts.AskBecomePass, "ask-become-pass", "K", false, "ask for privilege escalation password")
	flags.BoolVar(&opts.AskVaultPass, "ask-vault-pass", false, "ask for vault password")
	flags.StringVar(&opts.VaultPasswordFile, "vault-password-file", "", "vault password file")
	flags.StringArrayVar(&opts.VaultIDs, "vault-id", nil, "the vault identity to use (can be specified multiple times)")
	flags.BoolVar(&opts.ListTasks, "list-tasks", false, "list all tasks that would be executed")
	flags.BoolVar(&opts.ListTags, "list-tags", false, "list all available tags")
	flags.BoolVar(&opts.ListHosts, "list-hosts", false, "outputs a list of matching hosts; does not execute anything else")
	flags.BoolVar(&opts.SyntaxCheck, "syntax-check", false, "perform a syntax check on the playbook, but do not execute it")
	flags.StringVarP(&opts.Connection, "connection", "c", "", "connection type to use (default=smart)")
	flags.IntVarP(&opts.Timeout, "timeout", "T", 0, "override the SSH timeout in seconds")
	flags.StringVar(&opts.PrivateKey, "private-key", "", "use this file to authenticate the connection")
	flags.StringVar(&opts.PrivateKey, "key-file", "", "use this file to authenticate the connection")
	flags.StringVar(&opts.SSHCommonArgs, "ssh-common-args", "", "specify common arguments to pass to sftp/scp/ssh")
	flags.StringVar(&opts.SSHExtraArgs, "ssh-extra-args", "", "specify extra arguments to pass to ssh only")
	flags.BoolVar(&opts.ForceHandlers, "force-handlers", false, "run handlers even if a task fails")
	flags.BoolVar(&opts.Step, "step", false, "one-step-at-a-time: confirm each task before running")
	flags.BoolVar(&opts.FlushCache, "flush-cache", false, "clear the fact cache for every host in inventory")

	return cmd
}

func (opts *PlaybookOptions) Run() error {
	cfg := loadCLIConfig()

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

	vaultSecrets, err := resolveVaultSecrets(cfg, opts.VaultPasswordFile, opts.VaultIDs, opts.AskVaultPass)
	if err != nil {
		return fmt.Errorf("loading vault password: %w", err)
	}

	vars := make(map[string]interface{})
	if defaultBecomePass != "" {
		vars["ansible_become_pass"] = defaultBecomePass
	}
	for _, ev := range opts.ExtraVars {
		parsed, err := parseExtraVarsWithVaultSecrets(ev, vaultSecrets)
		if err != nil {
			return fmt.Errorf("parsing extra vars: %w", err)
		}
		for k, v := range parsed {
			vars[k] = v
		}
	}

	isVerbose := opts.Verbose > 0
	if isVerbose {
		fmt.Printf("[DEBUG] Extra vars: %+v\n", vars)
	}

	fmt.Printf("Parsing playbook file: %s\n", opts.PlaybookPath)
	parser := playbook.NewParser("")
	pb, err := parser.ParseFile(opts.PlaybookPath)
	if err != nil {
		return fmt.Errorf("parsing playbook: %w", err)
	}
	fmt.Printf("Parsing playbook success: %s\n", opts.PlaybookPath)

	if opts.SyntaxCheck {
		fmt.Printf("playbook: %s\n", opts.PlaybookPath)
		return nil
	}

	if isVerbose {
		fmt.Printf("[DEBUG] Playbook plays: %d\n", len(pb.Plays))
		for _, play := range pb.Plays {
			fmt.Printf("[DEBUG]   Play hosts: %s, tasks: %d\n", play.Hosts, len(play.Tasks))
			for _, task := range play.Tasks {
				fmt.Printf("[DEBUG]     Task: %s, Module: %s, Tags: %v\n", task.Name, task.ModuleName, task.Tags)
			}
		}
	}

	resolvedRolePaths := make([]string, 0, len(cfg.Defaults.RolesPath))
	for _, rolePath := range cfg.Defaults.RolesPath {
		resolvedRolePaths = append(resolvedRolePaths, cfg.ResolvePath(rolePath))
	}

	if opts.ListTasks || opts.ListTags {
		listed, err := playbook.ListPlaybook(pb, resolvedRolePaths)
		if err != nil {
			return fmt.Errorf("listing playbook: %w", err)
		}
		if opts.ListTags {
			printPlaybookTags(listed)
		}
		if opts.ListTasks {
			printPlaybookTasks(listed)
		}
		return nil
	}

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
	if defaultBecomePass != "" {
		for _, host := range inv.AllHosts {
			host.Become = true
			if host.BecomePass == "" {
				host.BecomePass = defaultBecomePass
			}
		}
	}

	if isVerbose {
		fmt.Printf("[DEBUG] Inventory hosts: %d\n", len(inv.AllHosts))
		for name, host := range inv.AllHosts {
			fmt.Printf("[DEBUG]   Host: %s, User: %s, Become: %v\n", name, host.User, host.Become)
		}
	}

	if opts.ListHosts {
		listedHosts, err := playbook.ListPlayHosts(pb, inv, opts.Limit, vars)
		if err != nil {
			return fmt.Errorf("listing hosts: %w", err)
		}
		printPlaybookHosts(listedHosts)
		return nil
	}

	fmt.Println("Creating executor...")
	executor := playbook.NewExecutor(inv)
	executor.SetVerbose(isVerbose)
	executor.SetVerbosityLevel(opts.Verbose)
	executor.SetCheckMode(opts.Check)
	executor.SetDiffMode(opts.Diff)
	executor.SetLimit(opts.Limit)
	executor.SetInventoryPath(opts.InventoryPath)
	sshCfg := sshClientConfigFromDefaults(cfg)
	if opts.Timeout > 0 {
		sshCfg.Timeout = time.Duration(opts.Timeout) * time.Second
	}
	executor.SetSSHConfig(sshCfg)
	executor.SetRolePaths(resolvedRolePaths)
	executor.SetGathering(cfg.Defaults.Gathering)
	executor.SetPollInterval(cfg.Defaults.PollInterval)
	if len(vaultSecrets) > 0 {
		executor.SetVaultPassword(vaultSecrets[0].Password)
		executor.SetVaultSecrets(vaultSecrets)
	}
	executor.SetStepMode(opts.Step)
	executor.SetForceHandlers(opts.ForceHandlers)
	executor.SetFactCaching(cfg.Defaults.FactCaching, cfg.ResolvePath(cfg.Defaults.FactCachingConnection), cfg.Defaults.FactCachingTimeout)
	if opts.FlushCache {
		executor.FlushCache()
	}
	forks := opts.Forks
	if forks <= 0 {
		forks = cfg.Defaults.Forks
	}
	if forks > 0 {
		executor.SetForks(forks)
	}
	defer executor.Close()

	if len(vars) > 0 {
		fmt.Printf("Setting extra vars: %d\n", len(vars))
		executor.SetExtraVars(vars)
	}

	if opts.Tags != "" {
		tagList := strings.Split(opts.Tags, ",")
		executor.SetTags(tagList)
		if isVerbose {
			fmt.Printf("[DEBUG] Filter tags: %v\n", tagList)
		}
		fmt.Printf("Filtering by tags: %v\n", tagList)
	}

	if opts.SkipTags != "" {
		skipTagList := strings.Split(opts.SkipTags, ",")
		executor.SetSkipTags(skipTagList)
		if isVerbose {
			fmt.Printf("[DEBUG] Skip tags: %v\n", skipTagList)
		}
		fmt.Printf("Skipping tags: %v\n", skipTagList)
	}

	if opts.StartAtTask != "" {
		executor.SetStartAtTask(opts.StartAtTask)
		fmt.Printf("Starting at task: %s\n", opts.StartAtTask)
	}
	if opts.Limit != "" {
		fmt.Printf("Limiting hosts: %s\n", opts.Limit)
	}

	if opts.Check {
		fmt.Println("Running in check mode...")
	}
	if opts.Diff {
		fmt.Println("Running with diff output...")
	}

	renderer, err := callback.NewWithCallbacks(cfg.Defaults.StdoutCallback, cfg.Defaults.CallbacksEnabled)
	if err != nil {
		return fmt.Errorf("selecting stdout callback: %w", err)
	}

	result, err := executor.Execute(pb)
	if err != nil {
		return fmt.Errorf("executing playbook: %w", err)
	}
	if err := renderer.Render(os.Stdout, result, opts.Tags); err != nil {
		return fmt.Errorf("rendering playbook result: %w", err)
	}
	if callback.HasFailures(result) {
		var failedHosts []string
		failedSet := make(map[string]bool)
		for _, playRes := range result.Plays {
			for hostName, hostRes := range playRes.Hosts {
				for _, t := range hostRes.Tasks {
					if t.Failed {
						if !failedSet[hostName] {
							failedSet[hostName] = true
							failedHosts = append(failedHosts, hostName)
						}
					}
				}
			}
		}
		if retryFile, err := writeRetryFile(opts.PlaybookPath, failedHosts); err == nil && retryFile != "" {
			fmt.Printf("\n[WARNING]: Failure using method (retry): failed hosts written to: %s\n", retryFile)
		}
		return errExecutionFailed
	}
	return nil
}

func printPlaybookTasks(plays []playbook.ListedPlay) {
	for _, play := range plays {
		name := play.Name
		if name == "" {
			name = play.Hosts
		}
		fmt.Printf("\nplaybook: %s\n", name)
		fmt.Println("  tasks:")
		for _, task := range play.Tasks {
			taskName := task.Name
			if taskName == "" {
				taskName = task.ModuleName
			}
			if len(task.Tags) > 0 {
				fmt.Printf("    %s\tTAGS: [%s]\n", taskName, strings.Join(task.Tags, ", "))
			} else {
				fmt.Printf("    %s\n", taskName)
			}
		}
	}
}

func printPlaybookTags(plays []playbook.ListedPlay) {
	seen := make(map[string]struct{})
	for _, play := range plays {
		for _, tag := range play.Tags {
			seen[tag] = struct{}{}
		}
	}
	tags := make([]string, 0, len(seen))
	for tag := range seen {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	fmt.Printf("\nplaybook: tags [%s]\n", strings.Join(tags, ", "))
}

func printPlaybookHosts(plays []playbook.ListedPlayHosts) {
	for _, play := range plays {
		name := play.Name
		if name == "" {
			name = play.Hosts
		}
		fmt.Printf("\nplaybook: %s\n", name)
		fmt.Printf("  pattern: %s\n", play.Hosts)
		fmt.Printf("  hosts (%d):\n", len(play.Items))
		for _, host := range play.Items {
			fmt.Printf("    %s\n", host)
		}
	}
}
