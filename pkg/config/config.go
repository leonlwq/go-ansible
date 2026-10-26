package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Defaults struct {
	Inventory             string
	RemoteUser            string
	PrivateKeyFile        string
	VaultPasswordFile     string
	VaultIdentityList     []string
	StdoutCallback        string
	HostKeyChecking       *bool
	Forks                 int
	Timeout               int
	RolesPath             []string
	Gathering             string
	PollInterval          int
	FactCaching           string
	FactCachingConnection string
	FactCachingTimeout    int
	CallbacksEnabled      []string
}

type PrivilegeEscalation struct {
	Become        *bool
	BecomeMethod  string
	BecomeUser    string
	BecomeAskPass *bool
}

type SSHConnection struct {
	Pipelining  *bool
	SSHArgs     string
	ControlPath string
	Timeout     int
}

type Config struct {
	Path                string
	Defaults            Defaults
	PrivilegeEscalation PrivilegeEscalation
	SSHConnection       SSHConnection
}

func (c *Config) ResolvePath(path string) string {
	if path == "" {
		return path
	}
	if strings.HasPrefix(path, "~") || strings.Contains(path, "$") || filepath.IsAbs(path) || c == nil || c.Path == "" {
		return path
	}
	return filepath.Join(filepath.Dir(c.Path), path)
}

func (c *Config) ApplyEnvOverrides() {
	if c == nil {
		return
	}
	if v := os.Getenv("ANSIBLE_INVENTORY"); v != "" {
		c.Defaults.Inventory = v
	}
	if v := os.Getenv("ANSIBLE_FORKS"); v != "" {
		if forks, err := strconv.Atoi(v); err == nil && forks > 0 {
			c.Defaults.Forks = forks
		}
	}
	if v := os.Getenv("ANSIBLE_REMOTE_USER"); v != "" {
		c.Defaults.RemoteUser = v
	}
	if v := os.Getenv("ANSIBLE_PRIVATE_KEY_FILE"); v != "" {
		c.Defaults.PrivateKeyFile = v
	}
	if v := os.Getenv("ANSIBLE_VAULT_PASSWORD_FILE"); v != "" {
		c.Defaults.VaultPasswordFile = v
	}
	if v := os.Getenv("ANSIBLE_VAULT_IDENTITY_LIST"); v != "" {
		c.Defaults.VaultIdentityList = splitConfigList(v)
	}
	if v := os.Getenv("ANSIBLE_STDOUT_CALLBACK"); v != "" {
		c.Defaults.StdoutCallback = strings.ToLower(strings.TrimSpace(v))
	}
	if v := os.Getenv("ANSIBLE_HOST_KEY_CHECKING"); v != "" {
		parsed := parseBool(v)
		c.Defaults.HostKeyChecking = &parsed
	}
	if v := os.Getenv("ANSIBLE_TIMEOUT"); v != "" {
		if t, err := strconv.Atoi(v); err == nil && t > 0 {
			c.Defaults.Timeout = t
		}
	}
	if v := os.Getenv("ANSIBLE_ROLES_PATH"); v != "" {
		c.Defaults.RolesPath = splitConfigPaths(v)
	}
	if v := os.Getenv("ANSIBLE_GATHERING"); v != "" {
		c.Defaults.Gathering = strings.ToLower(strings.TrimSpace(v))
	}
	if v := os.Getenv("ANSIBLE_POLL_INTERVAL"); v != "" {
		if interval, err := strconv.Atoi(v); err == nil && interval > 0 {
			c.Defaults.PollInterval = interval
		}
	}
	if v := os.Getenv("ANSIBLE_BECOME"); v != "" {
		parsed := parseBool(v)
		c.PrivilegeEscalation.Become = &parsed
	}
	if v := os.Getenv("ANSIBLE_BECOME_METHOD"); v != "" {
		c.PrivilegeEscalation.BecomeMethod = v
	}
	if v := os.Getenv("ANSIBLE_BECOME_USER"); v != "" {
		c.PrivilegeEscalation.BecomeUser = v
	}
	if v := os.Getenv("ANSIBLE_BECOME_ASK_PASS"); v != "" {
		parsed := parseBool(v)
		c.PrivilegeEscalation.BecomeAskPass = &parsed
	}
	if v := os.Getenv("ANSIBLE_SSH_ARGS"); v != "" {
		c.SSHConnection.SSHArgs = v
	}
	if v := os.Getenv("ANSIBLE_PIPELINING"); v != "" {
		parsed := parseBool(v)
		c.SSHConnection.Pipelining = &parsed
	}
	if v := os.Getenv("ANSIBLE_SSH_CONTROL_PATH"); v != "" {
		c.SSHConnection.ControlPath = v
	}
	if v := os.Getenv("ANSIBLE_CACHE_PLUGIN"); v != "" {
		c.Defaults.FactCaching = strings.ToLower(strings.TrimSpace(v))
	} else if v := os.Getenv("ANSIBLE_FACT_CACHING"); v != "" {
		c.Defaults.FactCaching = strings.ToLower(strings.TrimSpace(v))
	}
	if v := os.Getenv("ANSIBLE_CACHE_PLUGIN_CONNECTION"); v != "" {
		c.Defaults.FactCachingConnection = v
	} else if v := os.Getenv("ANSIBLE_FACT_CACHING_CONNECTION"); v != "" {
		c.Defaults.FactCachingConnection = v
	}
	if v := os.Getenv("ANSIBLE_CACHE_PLUGIN_TIMEOUT"); v != "" {
		if to, err := strconv.Atoi(v); err == nil {
			c.Defaults.FactCachingTimeout = to
		}
	} else if v := os.Getenv("ANSIBLE_FACT_CACHING_TIMEOUT"); v != "" {
		if to, err := strconv.Atoi(v); err == nil {
			c.Defaults.FactCachingTimeout = to
		}
	}
	if v := os.Getenv("ANSIBLE_CALLBACKS_ENABLED"); v != "" {
		c.Defaults.CallbacksEnabled = splitConfigList(v)
	}
}

func Load() (*Config, error) {
	path := FindDefaultPath()
	if path == "" {
		cfg := &Config{}
		cfg.ApplyEnvOverrides()
		return cfg, nil
	}
	return LoadFile(path)
}

func FindDefaultPath() string {
	if path := os.Getenv("ANSIBLE_CONFIG"); path != "" && isRegularFile(path) {
		return path
	}

	if wd, err := os.Getwd(); err == nil {
		path := filepath.Join(wd, "ansible.cfg")
		if isRegularFile(path) {
			return path
		}
	}

	if home, err := os.UserHomeDir(); err == nil {
		path := filepath.Join(home, ".ansible.cfg")
		if isRegularFile(path) {
			return path
		}
	}

	systemPath := "/etc/ansible/ansible.cfg"
	if isRegularFile(systemPath) {
		return systemPath
	}

	return ""
}

func LoadFile(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config file: %w", err)
	}
	defer file.Close()

	cfg := &Config{Path: path}
	section := ""
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)

		switch section {
		case "defaults":
			switch key {
			case "inventory":
				cfg.Defaults.Inventory = value
			case "remote_user":
				cfg.Defaults.RemoteUser = value
			case "private_key_file":
				cfg.Defaults.PrivateKeyFile = value
			case "vault_password_file":
				cfg.Defaults.VaultPasswordFile = value
			case "vault_identity_list":
				cfg.Defaults.VaultIdentityList = splitConfigList(value)
			case "stdout_callback":
				cfg.Defaults.StdoutCallback = strings.ToLower(strings.TrimSpace(value))
			case "host_key_checking":
				parsed := parseBool(value)
				cfg.Defaults.HostKeyChecking = &parsed
			case "forks":
				if forks, err := strconv.Atoi(value); err == nil && forks > 0 {
					cfg.Defaults.Forks = forks
				}
			case "timeout":
				if timeout, err := strconv.Atoi(value); err == nil && timeout > 0 {
					cfg.Defaults.Timeout = timeout
				}
			case "roles_path":
				cfg.Defaults.RolesPath = splitConfigPaths(value)
			case "gathering":
				cfg.Defaults.Gathering = strings.ToLower(strings.TrimSpace(value))
			case "poll_interval":
				if interval, err := strconv.Atoi(value); err == nil && interval > 0 {
					cfg.Defaults.PollInterval = interval
				}
			case "fact_caching":
				cfg.Defaults.FactCaching = strings.ToLower(strings.TrimSpace(value))
			case "fact_caching_connection":
				cfg.Defaults.FactCachingConnection = value
			case "fact_caching_timeout":
				if to, err := strconv.Atoi(value); err == nil {
					cfg.Defaults.FactCachingTimeout = to
				}
			case "callbacks_enabled":
				cfg.Defaults.CallbacksEnabled = splitConfigList(value)
			}
		case "privilege_escalation":
			switch key {
			case "become":
				parsed := parseBool(value)
				cfg.PrivilegeEscalation.Become = &parsed
			case "become_method":
				cfg.PrivilegeEscalation.BecomeMethod = value
			case "become_user":
				cfg.PrivilegeEscalation.BecomeUser = value
			case "become_ask_pass":
				parsed := parseBool(value)
				cfg.PrivilegeEscalation.BecomeAskPass = &parsed
			}
		case "ssh_connection":
			switch key {
			case "pipelining":
				parsed := parseBool(value)
				cfg.SSHConnection.Pipelining = &parsed
			case "ssh_args":
				cfg.SSHConnection.SSHArgs = value
			case "control_path":
				cfg.SSHConnection.ControlPath = value
			case "timeout":
				if t, err := strconv.Atoi(value); err == nil && t > 0 {
					cfg.SSHConnection.Timeout = t
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan config file: %w", err)
	}

	cfg.ApplyEnvOverrides()
	return cfg, nil
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func splitConfigPaths(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ':' || r == ';'
	})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func splitConfigList(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ':' || r == ';'
	})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}
