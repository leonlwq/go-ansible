package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go-ansible/pkg/callback"
	"go-ansible/pkg/playbook"
)

type PlaybookOptions struct {
	CommonCLIOptions

	PlaybookPath  string
	Tags          string
	SkipTags      string
	StartAtTask   string
	ListTasks     bool
	ListTags      bool
	SyntaxCheck   bool
	ForceHandlers bool
	Step          bool
	FlushCache    bool
}

func RunPlaybook(args []string, version string) {
	fmt.Fprintln(os.Stderr, "Reimplementation and reconstruction of the main features in Python version")
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
	opts := &PlaybookOptions{
		CommonCLIOptions: CommonCLIOptions{
			BecomeUser:   "root",
			BecomeMethod: "sudo",
			Forks:        5,
		},
	}
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
			opts.ForksSet = cmd.Flags().Changed("forks")
			return opts.Run()
		},
	}
	cmd.SetVersionTemplate("ansible-playbook {{.Version}}\n")

	flags := cmd.Flags()
	opts.RegisterCommonFlags(flags)
	flags.StringVarP(&opts.Tags, "tags", "t", "", "only run plays and tasks tagged with these values")
	flags.StringVar(&opts.SkipTags, "skip-tags", "", "only run plays and tasks whose tags do not match these values")
	flags.StringVar(&opts.StartAtTask, "start-at-task", "", "start the playbook at the task matching this name")
	flags.BoolVar(&opts.ListTasks, "list-tasks", false, "list all tasks that would be executed")
	flags.BoolVar(&opts.ListTags, "list-tags", false, "list all available tags")
	flags.BoolVar(&opts.SyntaxCheck, "syntax-check", false, "perform a syntax check on the playbook, but do not execute it")
	flags.BoolVar(&opts.ForceHandlers, "force-handlers", false, "run handlers even if a task fails")
	flags.BoolVar(&opts.Step, "step", false, "one-step-at-a-time: confirm each task before running")
	flags.BoolVar(&opts.FlushCache, "flush-cache", false, "clear the fact cache for every host in inventory")

	return cmd
}

func (opts *PlaybookOptions) Run() error {
	cfg := loadCLIConfig()

	isVerbose := opts.Verbose > 0
	isJSON := strings.ToLower(strings.TrimSpace(cfg.Defaults.StdoutCallback)) == "json"
	printInfo := func(format string, a ...interface{}) {
		if isJSON {
			fmt.Fprintf(os.Stderr, format+"\n", a...)
		} else {
			fmt.Printf(format+"\n", a...)
		}
	}

	printInfo("Parsing playbook file: %s", opts.PlaybookPath)
	parser := playbook.NewParser("")
	pb, err := parser.ParseFile(opts.PlaybookPath)
	if err != nil {
		return fmt.Errorf("parsing playbook: %w", err)
	}
	printInfo("Parsing playbook success: %s", opts.PlaybookPath)

	if opts.SyntaxCheck {
		fmt.Printf("playbook: %s\n", opts.PlaybookPath)
		return nil
	}

	if isVerbose {
		printInfo("[DEBUG] Playbook plays: %d", len(pb.Plays))
		for _, play := range pb.Plays {
			printInfo("[DEBUG]   Play hosts: %s, tasks: %d", play.Hosts, len(play.Tasks))
			for _, task := range play.Tasks {
				printInfo("[DEBUG]     Task: %s, Module: %s, Tags: %v", task.Name, task.ModuleName, task.Tags)
			}
		}
	}

	resolvedRolePaths := make([]string, 0, len(cfg.Defaults.RolesPath))
	for _, rolePath := range cfg.Defaults.RolesPath {
		resolvedRolePaths = append(resolvedRolePaths, cfg.ResolvePath(rolePath))
	}

	if opts.ListTasks || opts.ListTags {
		var tagList []string
		if opts.Tags != "" {
			tagList = strings.Split(opts.Tags, ",")
		}
		var skipTagList []string
		if opts.SkipTags != "" {
			skipTagList = strings.Split(opts.SkipTags, ",")
		}
		listed, err := playbook.ListPlaybookWithTags(pb, resolvedRolePaths, tagList, skipTagList)
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

	env, err := opts.SetupExecutionEnv(cfg, filepath.Dir(opts.PlaybookPath))
	if err != nil {
		return err
	}
	inv := env.Inventory
	vaultSecrets := env.VaultSecrets
	forks := env.Forks

	vars := make(map[string]interface{})
	if env.DefaultBecomePass != "" {
		vars["ansible_become_pass"] = env.DefaultBecomePass
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

	if isVerbose {
		printInfo("[DEBUG] Extra vars: %+v", vars)
		printInfo("[DEBUG] Inventory hosts: %d", len(inv.AllHosts))
		for name, host := range inv.AllHosts {
			printInfo("[DEBUG]   Host: %s, User: %s, Become: %v", name, host.User, host.Become)
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

	printInfo("Creating executor...")
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
	if forks > 0 {
		executor.SetForks(forks)
	}
	defer executor.Close()

	if len(vars) > 0 {
		printInfo("Setting extra vars: %d", len(vars))
		executor.SetExtraVars(vars)
	}

	if opts.Tags != "" {
		tagList := strings.Split(opts.Tags, ",")
		executor.SetTags(tagList)
		if isVerbose {
			printInfo("[DEBUG] Filter tags: %v", tagList)
		}
		printInfo("Filtering by tags: %v", tagList)
	}

	if opts.SkipTags != "" {
		skipTagList := strings.Split(opts.SkipTags, ",")
		executor.SetSkipTags(skipTagList)
		if isVerbose {
			printInfo("[DEBUG] Skip tags: %v", skipTagList)
		}
		printInfo("Skipping tags: %v", skipTagList)
	}

	if opts.StartAtTask != "" {
		executor.SetStartAtTask(opts.StartAtTask)
		printInfo("Starting at task: %s", opts.StartAtTask)
	}
	if opts.Limit != "" {
		printInfo("Limiting hosts: %s", opts.Limit)
	}

	if opts.Check {
		printInfo("Running in check mode...")
	}
	if opts.Diff {
		printInfo("Running with diff output...")
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
				if hostRes == nil {
					continue
				}
				for _, t := range hostRes.GetTasks() {
					if (t.Failed && !t.Rescued) || (t.Unreachable && !t.Ignored) {
						if !failedSet[hostName] {
							failedSet[hostName] = true
							failedHosts = append(failedHosts, hostName)
						}
						break
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
