package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"go-ansible/pkg/config"
)

type ConfigOptions struct {
	ConfigFile  string
	OnlyChanged bool
}

func RunConfig(args []string, version string) {
	fmt.Println("Reimplementation and reconstruction of the main features in Python version")
	cmd := NewConfigCmd(version)
	cmd.SetArgs(normalizeAnsibleArgs(args))
	if err := cmd.Execute(); err != nil {
		if !errors.Is(err, errExecutionFailed) {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}
}

func NewConfigCmd(version string) *cobra.Command {
	if version == "" {
		version = "0.1.0"
	}
	opts := &ConfigOptions{}

	cmd := &cobra.Command{
		Use:           "ansible-config [view|dump|init] [options]",
		Short:         "View, dump, or create Ansible configuration",
		Long:          "ansible-config command line tool to inspect current Ansible configuration values, config files in use, or generate initial configs.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	cmd.SetVersionTemplate("ansible-config {{.Version}}\n")

	viewCmd := &cobra.Command{
		Use:   "view",
		Short: "View active configuration file",
		RunE: func(c *cobra.Command, args []string) error {
			return runConfigView(opts)
		},
	}

	dumpCmd := &cobra.Command{
		Use:   "dump",
		Short: "Dump all active configurations",
		RunE: func(c *cobra.Command, args []string) error {
			return runConfigDump(opts)
		},
	}
	dumpCmd.Flags().BoolVar(&opts.OnlyChanged, "only-changed", false, "Only show configuration options that have changed from the default")

	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Generate starter ansible.cfg configuration file",
		RunE: func(c *cobra.Command, args []string) error {
			return runConfigInit()
		},
	}

	cmd.PersistentFlags().StringVarP(&opts.ConfigFile, "config", "c", "", "Path to configuration file")

	cmd.AddCommand(viewCmd, dumpCmd, initCmd)
	return cmd
}

func getConfig(opts *ConfigOptions) (*config.Config, error) {
	if opts.ConfigFile != "" {
		return config.LoadFile(opts.ConfigFile)
	}
	return config.Load()
}

func runConfigView(opts *ConfigOptions) error {
	cfg, err := getConfig(opts)
	if err != nil {
		return err
	}
	if cfg.Path == "" {
		fmt.Println("# No config file found; using default configuration values.")
		return nil
	}
	fmt.Printf("# Using config file: %s\n", cfg.Path)
	data, err := os.ReadFile(cfg.Path)
	if err != nil {
		return fmt.Errorf("reading config file %q: %w", cfg.Path, err)
	}
	fmt.Print(string(data))
	return nil
}

type configItem struct {
	Key     string
	Value   string
	Default string
	Changed bool
}

func runConfigDump(opts *ConfigOptions) error {
	cfg, err := getConfig(opts)
	if err != nil {
		return err
	}

	items := []configItem{
		{
			Key:     "DEFAULT_FORKS",
			Value:   fmt.Sprintf("%d", cfg.Defaults.Forks),
			Default: "5",
			Changed: cfg.Defaults.Forks > 0 && cfg.Defaults.Forks != 5,
		},
		{
			Key:     "DEFAULT_TIMEOUT",
			Value:   fmt.Sprintf("%d", cfg.Defaults.Timeout),
			Default: "10",
			Changed: cfg.Defaults.Timeout > 0 && cfg.Defaults.Timeout != 10,
		},
		{
			Key:     "DEFAULT_REMOTE_USER",
			Value:   cfg.Defaults.RemoteUser,
			Default: "root",
			Changed: cfg.Defaults.RemoteUser != "" && cfg.Defaults.RemoteUser != "root",
		},
		{
			Key:     "DEFAULT_PRIVATE_KEY_FILE",
			Value:   cfg.Defaults.PrivateKeyFile,
			Default: "None",
			Changed: cfg.Defaults.PrivateKeyFile != "",
		},
		{
			Key:     "DEFAULT_VAULT_PASSWORD_FILE",
			Value:   cfg.Defaults.VaultPasswordFile,
			Default: "None",
			Changed: cfg.Defaults.VaultPasswordFile != "",
		},
		{
			Key:     "DEFAULT_STDOUT_CALLBACK",
			Value:   cfg.Defaults.StdoutCallback,
			Default: "default",
			Changed: cfg.Defaults.StdoutCallback != "" && cfg.Defaults.StdoutCallback != "default",
		},
		{
			Key:     "DEFAULT_GATHERING",
			Value:   cfg.Defaults.Gathering,
			Default: "implicit",
			Changed: cfg.Defaults.Gathering != "",
		},
		{
			Key:     "DEFAULT_POLL_INTERVAL",
			Value:   fmt.Sprintf("%d", cfg.Defaults.PollInterval),
			Default: "15",
			Changed: cfg.Defaults.PollInterval > 0 && cfg.Defaults.PollInterval != 15,
		},
		{
			Key:     "DEFAULT_ROLES_PATH",
			Value:   strings.Join(cfg.Defaults.RolesPath, ":"),
			Default: "None",
			Changed: len(cfg.Defaults.RolesPath) > 0,
		},
	}

	if cfg.Defaults.HostKeyChecking != nil {
		val := "True"
		if !*cfg.Defaults.HostKeyChecking {
			val = "False"
		}
		items = append(items, configItem{
			Key:     "HOST_KEY_CHECKING",
			Value:   val,
			Default: "True",
			Changed: !*cfg.Defaults.HostKeyChecking,
		})
	} else {
		items = append(items, configItem{
			Key:     "HOST_KEY_CHECKING",
			Value:   "True",
			Default: "True",
			Changed: false,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Key < items[j].Key
	})

	for _, item := range items {
		if opts.OnlyChanged && !item.Changed {
			continue
		}
		origin := "default"
		if item.Changed {
			origin = cfg.Path
		}
		val := item.Value
		if val == "" || val == "0" {
			val = item.Default
		}
		fmt.Printf("%s(%s) = %s\n", item.Key, origin, val)
	}

	return nil
}

func runConfigInit() error {
	template := `# ansible.cfg (Generated by go-ansible)
[defaults]
inventory = hosts.ini
remote_user = root
forks = 5
timeout = 10
host_key_checking = False
stdout_callback = default
# private_key_file = ~/.ssh/id_rsa
# vault_password_file = ~/.vault_pass.txt
# roles_path = ./roles:/etc/ansible/roles
`
	fmt.Print(template)
	return nil
}
