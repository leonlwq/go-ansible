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

func promptPassword(prompt string) (string, error) {
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
	if path == "" {
		return path
	}
	if path[0] == '~' {
		home, err := os.UserHomeDir()
		if err == nil {
			if len(path) > 1 {
				return filepath.Join(home, path[2:])
			}
			return home
		}
	}
	if strings.Contains(path, "$") {
		return os.ExpandEnv(path)
	}
	return path
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
