package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"golang.org/x/term"

	"go-ansible/pkg/config"
	"go-ansible/pkg/inventory"
	"go-ansible/pkg/ssh"
	"go-ansible/pkg/util"
	"go-ansible/pkg/variable"
)

var errExecutionFailed = errors.New("execution failed")

func parseExtraVars(varsStr string) (map[string]interface{}, error) {
	return parseExtraVarsWithVault(varsStr, "")
}

func parseExtraVarsWithVault(varsStr, vaultPassword string) (map[string]interface{}, error) {
	var secrets []*variable.VaultSecret
	if vaultPassword != "" {
		secrets = []*variable.VaultSecret{{Password: vaultPassword}}
	}
	return parseExtraVarsWithVaultSecrets(varsStr, secrets)
}

func parseExtraVarsWithVaultSecrets(varsStr string, secrets []*variable.VaultSecret) (map[string]interface{}, error) {
	result := make(map[string]interface{})
	trimmed := strings.TrimSpace(varsStr)
	if trimmed == "" {
		return result, nil
	}

	if strings.HasPrefix(trimmed, "@") {
		path := expandCLIPath(strings.TrimSpace(trimmed[1:]))
		manager := variable.NewManager()
		manager.SetVaultSecrets(secrets)
		fileVars, err := manager.LoadVarsFile(path)
		if err != nil {
			return nil, fmt.Errorf("parse extra vars file %q: %w", path, err)
		}
		if fileVars == nil {
			return nil, fmt.Errorf("parse extra vars file %q: top-level value must be a mapping", path)
		}
		return fileVars, nil
	}

	if strings.HasPrefix(trimmed, "{") {
		var jsonVars map[string]interface{}
		if err := json.Unmarshal([]byte(trimmed), &jsonVars); err == nil {
			return jsonVars, nil
		}
	}

	pairs, err := util.SplitArgs(varsStr)
	if err != nil {
		return result, nil
	}
	for _, pair := range pairs {
		if idx := strings.Index(pair, "="); idx > 0 {
			key := pair[:idx]
			value := pair[idx+1:]
			result[key] = parseCLIValue(value)
		}
	}

	return result, nil
}

func parseModuleArgs(args string) map[string]interface{} {
	params := make(map[string]interface{})
	if strings.TrimSpace(args) == "" {
		return params
	}

	parts, err := util.SplitArgs(args)
	if err != nil {
		params["_raw_params"] = args
		return params
	}

	for _, part := range parts {
		if !strings.Contains(part, "=") {
			params["_raw_params"] = args
			return params
		}
		kv := strings.SplitN(part, "=", 2)
		params[kv[0]] = parseCLIValue(kv[1])
	}

	return params
}

func parseCLIValue(value string) interface{} {
	trimmed := strings.TrimSpace(value)
	lower := strings.ToLower(trimmed)
	switch lower {
	case "yes", "true", "on":
		return true
	case "no", "false", "off":
		return false
	}

	if i, err := strconv.Atoi(trimmed); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(trimmed, 64); err == nil && strings.Contains(trimmed, ".") {
		return f
	}

	return trimmed
}

func loadCLIConfig() *config.Config {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading ansible.cfg: %v\n", err)
		os.Exit(1)
	}
	return cfg
}

func applyConfigDefaultsToInventory(inv *inventory.Inventory, cfg *config.Config) {
	if cfg == nil {
		return
	}

	for _, host := range inv.AllHosts {
		if cfg.Defaults.RemoteUser != "" && host.User == "" {
			host.User = cfg.Defaults.RemoteUser
		}
		if cfg.Defaults.PrivateKeyFile != "" && host.PrivateKey == "" {
			host.PrivateKey = expandCLIPath(cfg.ResolvePath(cfg.Defaults.PrivateKeyFile))
		}
		if cfg.Defaults.HostKeyChecking != nil && !*cfg.Defaults.HostKeyChecking && host.SSHCommonArgs == "" {
			host.SSHCommonArgs = "-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"
		}
		if cfg.PrivilegeEscalation.Become != nil && *cfg.PrivilegeEscalation.Become && !host.Become {
			host.Become = true
		}
		if cfg.PrivilegeEscalation.BecomeMethod != "" && host.BecomeMethod == "" {
			host.BecomeMethod = cfg.PrivilegeEscalation.BecomeMethod
		}
		if cfg.PrivilegeEscalation.BecomeUser != "" && host.BecomeUser == "" {
			host.BecomeUser = cfg.PrivilegeEscalation.BecomeUser
		}
		if cfg.SSHConnection.SSHArgs != "" && host.SSHCommonArgs == "" {
			host.SSHCommonArgs = cfg.SSHConnection.SSHArgs
		}
	}
}

func loadVarsDirsToInventory(inv *inventory.Inventory, secrets []*variable.VaultSecret, dirs ...string) {
	if inv == nil {
		return
	}
	manager := variable.NewManager()
	manager.SetVaultSecrets(secrets)

	for _, baseDir := range dirs {
		if strings.TrimSpace(baseDir) == "" {
			continue
		}
		// 1. group_vars
		groupVarsDir := filepath.Join(baseDir, "group_vars")
		if info, err := os.Stat(groupVarsDir); err == nil && info.IsDir() {
			_ = filepath.Walk(groupVarsDir, func(path string, fi os.FileInfo, err error) error {
				if err != nil || fi.IsDir() {
					return nil
				}
				ext := strings.ToLower(filepath.Ext(path))
				if ext != ".yml" && ext != ".yaml" && ext != ".json" {
					return nil
				}
				groupName, ok := variable.ExtractTargetNameFromVarsPath(groupVarsDir, path)
				if !ok || groupName == "" {
					return nil
				}
				fileVars, err := manager.LoadVarsFile(path)
				if err == nil && fileVars != nil {
					if groupName == "all" {
						if inv.Vars == nil {
							inv.Vars = make(map[string]interface{})
						}
						for k, v := range fileVars {
							inv.Variables[k] = fmt.Sprintf("%v", v)
							inv.Vars[k] = v
						}
					}
					group, ok := inv.Groups[groupName]
					if !ok {
						group = &inventory.Group{
							Name:      groupName,
							Variables: make(map[string]string),
						}
						inv.Groups[groupName] = group
					}
					if group.Variables == nil {
						group.Variables = make(map[string]string)
					}
					for k, v := range fileVars {
						group.Variables[k] = fmt.Sprintf("%v", v)
						inv.SetGroupVar(groupName, k, v)
					}
				}
				return nil
			})
			inv.ApplyGroupVars()
		}

		// 2. host_vars
		hostVarsDir := filepath.Join(baseDir, "host_vars")
		if info, err := os.Stat(hostVarsDir); err == nil && info.IsDir() {
			_ = filepath.Walk(hostVarsDir, func(path string, fi os.FileInfo, err error) error {
				if err != nil || fi.IsDir() {
					return nil
				}
				ext := strings.ToLower(filepath.Ext(path))
				if ext != ".yml" && ext != ".yaml" && ext != ".json" {
					return nil
				}
				hostName, ok := variable.ExtractTargetNameFromVarsPath(hostVarsDir, path)
				if !ok || hostName == "" {
					return nil
				}
				fileVars, err := manager.LoadVarsFile(path)
				if err == nil && fileVars != nil {
					for _, host := range inv.AllHosts {
						if host.Name == hostName {
							for k, v := range fileVars {
								strVal := fmt.Sprintf("%v", v)
								host.Variables[k] = strVal
								inv.ApplyConnectionVar(host, k, strVal, true)
							}
						}
					}
					for k, v := range fileVars {
						inv.SetHostVar(hostName, k, v)
					}
				}
				return nil
			})
		}
	}
}

func resolveVaultPassword(cfg *config.Config) (string, error) {
	return resolveVaultPasswordWithPrompt(cfg, "", false)
}

func resolveVaultPasswordWithPrompt(cfg *config.Config, vaultPasswordFile string, askVaultPass bool) (string, error) {
	secrets, err := resolveVaultSecrets(cfg, vaultPasswordFile, nil, askVaultPass)
	if err != nil {
		return "", err
	}
	if len(secrets) > 0 {
		return secrets[0].Password, nil
	}
	return "", nil
}

func resolveVaultSecrets(cfg *config.Config, vaultPasswordFile string, vaultIDs []string, askVaultPass bool) ([]*variable.VaultSecret, error) {
	var secrets []*variable.VaultSecret

	// 1. 显式命令行 --vault-id
	for _, raw := range vaultIDs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		id, source := variable.ParseVaultIDString(raw)
		resolvedSource := source
		if cfg != nil {
			resolvedSource = cfg.ResolvePath(source)
		}
		sec, err := variable.LoadVaultSecret(id, resolvedSource, promptPassword)
		if err != nil {
			return nil, err
		}
		secrets = append(secrets, sec)
	}

	// 2. 显式命令行 --vault-password-file
	if vaultPasswordFile != "" {
		resolved := vaultPasswordFile
		if cfg != nil {
			resolved = cfg.ResolvePath(vaultPasswordFile)
		}
		path := expandCLIPath(resolved)
		sec, err := variable.LoadVaultSecret("", path, nil)
		if err != nil {
			return nil, err
		}
		secrets = append(secrets, sec)
	}

	// 3. 环境变量 ANSIBLE_VAULT_PASSWORD
	if password := os.Getenv("ANSIBLE_VAULT_PASSWORD"); password != "" {
		secrets = append(secrets, &variable.VaultSecret{ID: "", Password: password})
	}

	// 4. 配置文件中的 vault_identity_list 与 vault_password_file
	if cfg != nil {
		for _, raw := range cfg.Defaults.VaultIdentityList {
			id, source := variable.ParseVaultIDString(raw)
			resolvedSource := cfg.ResolvePath(source)
			sec, err := variable.LoadVaultSecret(id, resolvedSource, promptPassword)
			if err == nil {
				secrets = append(secrets, sec)
			}
		}
		if cfg.Defaults.VaultPasswordFile != "" && vaultPasswordFile == "" {
			path := expandCLIPath(cfg.ResolvePath(cfg.Defaults.VaultPasswordFile))
			sec, err := variable.LoadVaultSecret("", path, nil)
			if err == nil {
				secrets = append(secrets, sec)
			}
		}
	}

	// 5. 交互式录入
	if askVaultPass {
		pass, err := promptPassword("Vault password: ")
		if err != nil {
			return nil, err
		}
		secrets = append(secrets, &variable.VaultSecret{ID: "", Password: pass})
	}

	return secrets, nil
}

var promptPasswordFunc = defaultPromptPassword

func promptPassword(prompt string) (string, error) {
	if promptPasswordFunc != nil {
		return promptPasswordFunc(prompt)
	}
	return defaultPromptPassword(prompt)
}

func defaultPromptPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		passBytes, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(passBytes)), nil
	}
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func sshClientConfigFromDefaults(cfg *config.Config) *ssh.ClientConfig {
	if cfg == nil {
		return nil
	}

	clientConfig := ssh.DefaultConfig()
	if cfg.Defaults.Timeout > 0 {
		clientConfig.Timeout = time.Duration(cfg.Defaults.Timeout) * time.Second
	}
	return clientConfig
}

func expandCLIPath(path string) string {
	return util.ExpandPath(path)
}

func normalizeAnsibleArgs(args []string) []string {
	normalized := make([]string, 0, len(args))
	for _, arg := range args {
		switch {
		case arg == "-become":
			normalized = append(normalized, "--become")
		case strings.HasPrefix(arg, "-become-user="):
			normalized = append(normalized, "--become-user="+strings.TrimPrefix(arg, "-become-user="))
		case arg == "-become-user":
			normalized = append(normalized, "--become-user")
		case strings.HasPrefix(arg, "-become-method="):
			normalized = append(normalized, "--become-method="+strings.TrimPrefix(arg, "-become-method="))
		case arg == "-become-method":
			normalized = append(normalized, "--become-method")
		default:
			normalized = append(normalized, arg)
		}
	}
	return normalized
}

type inventoryValue struct {
	paths *[]string
}

func newInventoryValue(paths *[]string) *inventoryValue {
	return &inventoryValue{paths: paths}
}

func (v *inventoryValue) String() string {
	if v.paths == nil || len(*v.paths) == 0 {
		return ""
	}
	return (*v.paths)[len(*v.paths)-1]
}

func (v *inventoryValue) Set(s string) error {
	if v.paths != nil {
		*v.paths = append(*v.paths, s)
	}
	return nil
}

func (v *inventoryValue) Type() string {
	return "string"
}

func resolveLimitPattern(limit string) (string, error) {
	limit = strings.TrimSpace(limit)
	if limit == "" {
		return "", nil
	}
	if strings.HasPrefix(limit, "@") {
		path := expandCLIPath(strings.TrimSpace(limit[1:]))
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read limit file %q: %w", path, err)
		}
		lines := strings.Split(string(data), "\n")
		var hosts []string
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			hosts = append(hosts, line)
		}
		if len(hosts) == 0 {
			return "", fmt.Errorf("limit file %q contains no valid hosts", path)
		}
		return strings.Join(hosts, ","), nil
	}
	return limit, nil
}

func writeRetryFile(playbookPath string, failedHosts []string) (string, error) {
	if len(failedHosts) == 0 {
		return "", nil
	}
	base := filepath.Base(playbookPath)
	ext := filepath.Ext(base)
	nameWithoutExt := strings.TrimSuffix(base, ext)
	retryFile := nameWithoutExt + ".retry"

	sort.Strings(failedHosts)
	content := strings.Join(failedHosts, "\n") + "\n"
	if err := os.WriteFile(retryFile, []byte(content), 0644); err != nil {
		return "", err
	}
	return retryFile, nil
}

type ConnectionOptions struct {
	Connection    string
	Timeout       int
	PrivateKey    string
	SSHCommonArgs string
	SSHExtraArgs  string
}

func applyConnectionOptionsToInventory(inv *inventory.Inventory, opts ConnectionOptions) {
	if inv == nil {
		return
	}
	for _, host := range inv.AllHosts {
		if opts.Connection != "" {
			host.Connection = opts.Connection
		}
		if opts.PrivateKey != "" {
			host.PrivateKey = expandCLIPath(opts.PrivateKey)
		}
		var args []string
		if host.SSHCommonArgs != "" {
			args = append(args, host.SSHCommonArgs)
		}
		if opts.SSHCommonArgs != "" {
			args = append(args, opts.SSHCommonArgs)
		}
		if opts.SSHExtraArgs != "" {
			args = append(args, opts.SSHExtraArgs)
		}
		if len(args) > 0 {
			host.SSHCommonArgs = strings.Join(args, " ")
		}
	}
}

// CommonCLIOptions encapsulates CLI flags and options shared across ansible and ansible-playbook.
type CommonCLIOptions struct {
	InventoryPath     string
	InventoryPaths    []string
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
}

// RegisterCommonFlags registers standard connection, privilege escalation, vault and execution flags.
func (opts *CommonCLIOptions) RegisterCommonFlags(flags *pflag.FlagSet) {
	flags.VarP(newInventoryValue(&opts.InventoryPaths), "inventory", "i", "specify inventory host path(s) or comma separated host list")
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
}

// ExecutionEnv holds the pre-execution inventory, credentials, and settings.
type ExecutionEnv struct {
	Inventory         *inventory.Inventory
	VaultSecrets      []*variable.VaultSecret
	DefaultSSHPass    string
	DefaultBecomePass string
	Forks             int
}

// SetupExecutionEnv processes CLI credentials, inventory, group/host vars, limits, and host properties.
func (opts *CommonCLIOptions) SetupExecutionEnv(cfg *config.Config, extraVarsDirs ...string) (*ExecutionEnv, error) {
	var defaultSSHPass string
	if opts.AskPass {
		pass, err := promptPassword("SSH password: ")
		if err != nil {
			return nil, fmt.Errorf("reading SSH password: %w", err)
		}
		defaultSSHPass = pass
	}

	var defaultBecomePass string
	if opts.AskBecomePass {
		pass, err := promptPassword("BECOME password[defaults to SSH password]: ")
		if err != nil {
			return nil, fmt.Errorf("reading become password: %w", err)
		}
		if pass == "" && defaultSSHPass != "" {
			pass = defaultSSHPass
		}
		defaultBecomePass = pass
	}

	vaultSecrets, err := resolveVaultSecrets(cfg, opts.VaultPasswordFile, opts.VaultIDs, opts.AskVaultPass)
	if err != nil {
		return nil, fmt.Errorf("loading vault password: %w", err)
	}

	invPaths := opts.InventoryPaths
	if len(invPaths) == 0 && opts.InventoryPath != "" {
		invPaths = []string{opts.InventoryPath}
	}
	if len(invPaths) == 0 && cfg != nil && cfg.Defaults.Inventory != "" {
		invPaths = []string{cfg.ResolvePath(cfg.Defaults.Inventory)}
	}

	var inv *inventory.Inventory
	if len(invPaths) == 0 {
		fmt.Fprintf(os.Stderr, "[WARNING]: No inventory was parsed, only implicit localhost is available\n")
		inv = inventory.NewInventory()
		inv.EnsureLocalhost()
	} else {
		opts.InventoryPath = invPaths[0]
		var parseErr error
		inv, parseErr = inventory.ParseSources(invPaths...)
		if parseErr != nil {
			return nil, fmt.Errorf("parsing inventory: %w", parseErr)
		}
	}

	varsDirs := make([]string, 0, len(extraVarsDirs)+len(invPaths))
	// 按照 Ansible 官方规范，Inventory 变量先加载，Playbook 级变量后加载并覆盖清单变量
	for _, p := range invPaths {
		varsDirs = append(varsDirs, filepath.Dir(p))
	}
	varsDirs = append(varsDirs, extraVarsDirs...)
	loadVarsDirsToInventory(inv, vaultSecrets, varsDirs...)

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
		return nil, fmt.Errorf("resolving limit: %w", err)
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
			if host.BecomePass == "" {
				host.BecomePass = defaultBecomePass
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

	forks := opts.Forks
	if !opts.ForksSet && cfg != nil && cfg.Defaults.Forks > 0 {
		forks = cfg.Defaults.Forks
	}

	return &ExecutionEnv{
		Inventory:         inv,
		VaultSecrets:      vaultSecrets,
		DefaultSSHPass:    defaultSSHPass,
		DefaultBecomePass: defaultBecomePass,
		Forks:             forks,
	}, nil
}
