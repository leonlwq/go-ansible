package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFileParsesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ansible.cfg")
	content := `[defaults]
inventory = inventories/prod.ini
remote_user = deploy
private_key_file = ~/.ssh/id_rsa
vault_password_file = .vault-pass
stdout_callback = json
host_key_checking = False
forks = 20
timeout = 15
roles_path = roles:vendor/roles
gathering = explicit
poll_interval = 12
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}

	if cfg.Defaults.Inventory != "inventories/prod.ini" {
		t.Fatalf("expected inventory path, got %q", cfg.Defaults.Inventory)
	}
	if cfg.Defaults.RemoteUser != "deploy" {
		t.Fatalf("expected remote_user deploy, got %q", cfg.Defaults.RemoteUser)
	}
	if cfg.Defaults.PrivateKeyFile != "~/.ssh/id_rsa" {
		t.Fatalf("expected private_key_file, got %q", cfg.Defaults.PrivateKeyFile)
	}
	if cfg.Defaults.VaultPasswordFile != ".vault-pass" {
		t.Fatalf("expected vault_password_file, got %q", cfg.Defaults.VaultPasswordFile)
	}
	if cfg.Defaults.StdoutCallback != "json" {
		t.Fatalf("expected stdout_callback json, got %q", cfg.Defaults.StdoutCallback)
	}
	if cfg.Defaults.HostKeyChecking == nil || *cfg.Defaults.HostKeyChecking {
		t.Fatalf("expected host_key_checking false, got %#v", cfg.Defaults.HostKeyChecking)
	}
	if cfg.Defaults.Forks != 20 {
		t.Fatalf("expected forks 20, got %d", cfg.Defaults.Forks)
	}
	if cfg.Defaults.Timeout != 15 {
		t.Fatalf("expected timeout 15, got %d", cfg.Defaults.Timeout)
	}
	if len(cfg.Defaults.RolesPath) != 2 || cfg.Defaults.RolesPath[0] != "roles" || cfg.Defaults.RolesPath[1] != "vendor/roles" {
		t.Fatalf("unexpected roles_path: %#v", cfg.Defaults.RolesPath)
	}
	if cfg.Defaults.Gathering != "explicit" {
		t.Fatalf("expected gathering explicit, got %q", cfg.Defaults.Gathering)
	}
	if cfg.Defaults.PollInterval != 12 {
		t.Fatalf("expected poll_interval 12, got %d", cfg.Defaults.PollInterval)
	}
}

func TestFindDefaultPathPrefersEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ansible.cfg")
	if err := os.WriteFile(path, []byte("[defaults]\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	t.Setenv("ANSIBLE_CONFIG", path)

	if got := FindDefaultPath(); got != path {
		t.Fatalf("expected %q, got %q", path, got)
	}
}

func TestConfigResolvePathUsesConfigDirectory(t *testing.T) {
	cfg := &Config{
		Path: filepath.Join("/tmp/project", "ansible.cfg"),
	}

	if got := cfg.ResolvePath("inventories/prod.ini"); got != filepath.Join("/tmp/project", "inventories/prod.ini") {
		t.Fatalf("expected resolved relative path, got %q", got)
	}
	if got := cfg.ResolvePath("/etc/ansible/hosts"); got != "/etc/ansible/hosts" {
		t.Fatalf("expected absolute path unchanged, got %q", got)
	}
}

func TestSplitConfigPaths(t *testing.T) {
	got := splitConfigPaths("roles:vendor/roles;/opt/shared/roles")
	if len(got) != 3 || got[0] != "roles" || got[1] != "vendor/roles" || got[2] != "/opt/shared/roles" {
		t.Fatalf("unexpected splitConfigPaths result: %#v", got)
	}
}

func TestLoadFileParsesExtendedSections(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ansible.cfg")
	content := `[defaults]
inventory = hosts.ini

[privilege_escalation]
become = True
become_method = sudo
become_user = admin
become_ask_pass = False

[ssh_connection]
pipelining = True
ssh_args = -C -o ControlMaster=auto
control_path = /tmp/ansible-ssh-%%h
timeout = 25
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}

	if cfg.PrivilegeEscalation.Become == nil || !*cfg.PrivilegeEscalation.Become {
		t.Errorf("expected become true, got %#v", cfg.PrivilegeEscalation.Become)
	}
	if cfg.PrivilegeEscalation.BecomeMethod != "sudo" {
		t.Errorf("expected become_method sudo, got %s", cfg.PrivilegeEscalation.BecomeMethod)
	}
	if cfg.PrivilegeEscalation.BecomeUser != "admin" {
		t.Errorf("expected become_user admin, got %s", cfg.PrivilegeEscalation.BecomeUser)
	}
	if cfg.PrivilegeEscalation.BecomeAskPass == nil || *cfg.PrivilegeEscalation.BecomeAskPass {
		t.Errorf("expected become_ask_pass false, got %#v", cfg.PrivilegeEscalation.BecomeAskPass)
	}

	if cfg.SSHConnection.Pipelining == nil || !*cfg.SSHConnection.Pipelining {
		t.Errorf("expected pipelining true, got %#v", cfg.SSHConnection.Pipelining)
	}
	if cfg.SSHConnection.SSHArgs != "-C -o ControlMaster=auto" {
		t.Errorf("expected ssh_args, got %s", cfg.SSHConnection.SSHArgs)
	}
	if cfg.SSHConnection.ControlPath != "/tmp/ansible-ssh-%%h" {
		t.Errorf("expected control_path, got %s", cfg.SSHConnection.ControlPath)
	}
	if cfg.SSHConnection.Timeout != 25 {
		t.Errorf("expected timeout 25, got %d", cfg.SSHConnection.Timeout)
	}
}

func TestConfigApplyEnvOverrides(t *testing.T) {
	t.Setenv("ANSIBLE_FORKS", "42")
	t.Setenv("ANSIBLE_REMOTE_USER", "env_user")
	t.Setenv("ANSIBLE_BECOME", "yes")
	t.Setenv("ANSIBLE_BECOME_USER", "env_root")
	t.Setenv("ANSIBLE_SSH_ARGS", "-o ForwardAgent=yes")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if cfg.Defaults.Forks != 42 {
		t.Errorf("expected forks 42 from env, got %d", cfg.Defaults.Forks)
	}
	if cfg.Defaults.RemoteUser != "env_user" {
		t.Errorf("expected remote user 'env_user' from env, got %s", cfg.Defaults.RemoteUser)
	}
	if cfg.PrivilegeEscalation.Become == nil || !*cfg.PrivilegeEscalation.Become {
		t.Errorf("expected become true from env")
	}
	if cfg.PrivilegeEscalation.BecomeUser != "env_root" {
		t.Errorf("expected become_user 'env_root' from env, got %s", cfg.PrivilegeEscalation.BecomeUser)
	}
	if cfg.SSHConnection.SSHArgs != "-o ForwardAgent=yes" {
		t.Errorf("expected ssh_args from env, got %s", cfg.SSHConnection.SSHArgs)
	}
}

func TestLoadFileFactCachingAndCallbacks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ansible.cfg")
	content := `[defaults]
fact_caching = jsonfile
fact_caching_connection = /tmp/facts_cache
fact_caching_timeout = 86400
callbacks_enabled = profile_tasks, timer
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}

	if cfg.Defaults.FactCaching != "jsonfile" {
		t.Errorf("expected fact_caching jsonfile, got %s", cfg.Defaults.FactCaching)
	}
	if cfg.Defaults.FactCachingConnection != "/tmp/facts_cache" {
		t.Errorf("expected fact_caching_connection /tmp/facts_cache, got %s", cfg.Defaults.FactCachingConnection)
	}
	if cfg.Defaults.FactCachingTimeout != 86400 {
		t.Errorf("expected fact_caching_timeout 86400, got %d", cfg.Defaults.FactCachingTimeout)
	}
	if len(cfg.Defaults.CallbacksEnabled) != 2 || cfg.Defaults.CallbacksEnabled[0] != "profile_tasks" || cfg.Defaults.CallbacksEnabled[1] != "timer" {
		t.Errorf("unexpected callbacks_enabled: %#v", cfg.Defaults.CallbacksEnabled)
	}
}
