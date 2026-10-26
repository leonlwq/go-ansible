package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go-ansible/pkg/config"
	"go-ansible/pkg/inventory"
)

func TestParseExtraVarsQuotedAndTyped(t *testing.T) {
	got, err := parseExtraVars(`app_name="elite km" enabled=true replicas=3 ratio=1.5`)
	if err != nil {
		t.Fatalf("parseExtraVars() error = %v", err)
	}

	if got["app_name"] != "elite km" {
		t.Fatalf("expected quoted string to be preserved, got %#v", got["app_name"])
	}
	if got["enabled"] != true {
		t.Fatalf("expected bool true, got %#v", got["enabled"])
	}
	if got["replicas"] != 3 {
		t.Fatalf("expected int 3, got %#v", got["replicas"])
	}
	if got["ratio"] != 1.5 {
		t.Fatalf("expected float 1.5, got %#v", got["ratio"])
	}
}

func TestParseExtraVarsFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vars.yml")
	if err := os.WriteFile(path, []byte("app_name: elite\nreplicas: 2\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := parseExtraVars("@" + path)
	if err != nil {
		t.Fatalf("parseExtraVars(@file) error = %v", err)
	}
	if got["app_name"] != "elite" {
		t.Fatalf("expected app_name from file, got %#v", got["app_name"])
	}
	if got["replicas"] != 2 {
		t.Fatalf("expected replicas=2 from file, got %#v", got["replicas"])
	}
}

func TestParseExtraVarsFromVaultFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vault.yml")
	content := `$ANSIBLE_VAULT;1.1;AES256
65613534373065303762363261323634316534656431623062323163616334326632623136313831
3439313832326431386466653438383465313866373266630a626165356364336536396463363933
66363864396630363265306536656135653536323465643832383464633635333862336161646162
3063336562633630300a313861616534323337643539363765656463626136353164336439353963
65376461633630356162313432633131346661643331626235393465343862303065
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := parseExtraVarsWithVault("@"+path, "secret123")
	if err != nil {
		t.Fatalf("parseExtraVarsWithVault(@vault) error = %v", err)
	}
	if got["foo"] != "bar" {
		t.Fatalf("expected foo=bar, got %#v", got["foo"])
	}
	if got["port"] != 8080 {
		t.Fatalf("expected port=8080, got %#v", got["port"])
	}
}

func TestParseExtraVarsFromFileDecryptsInlineVaultValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vars.yml")
	content := `db_password: !vault |
  $ANSIBLE_VAULT;1.1;AES256
  65613534373065303762363261323634316534656431623062323163616334326632623136313831
  3439313832326431386466653438383465313866373266630a626165356364336536396463363933
  66363864396630363265306536656135653536323465643832383464633635333862336161646162
  3063336562633630300a313861616534323337643539363765656463626136353164336439353963
  65376461633630356162313432633131346661643331626235393465343862303065
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := parseExtraVarsWithVault("@"+path, "secret123")
	if err != nil {
		t.Fatalf("parseExtraVarsWithVault(@inline-vault) error = %v", err)
	}
	if got["db_password"] != "foo: bar\nport: 8080" {
		t.Fatalf("unexpected db_password: %#v", got["db_password"])
	}
}

func TestParseExtraVarsFromVaultFileRequiresPassword(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vault.yml")
	content := `$ANSIBLE_VAULT;1.1;AES256
65613534373065303762363261323634316534656431623062323163616334326632623136313831
3439313832326431386466653438383465313866373266630a626165356364336536396463363933
66363864396630363265306536656135653536323465643832383464633635333862336161646162
3063336562633630300a313861616534323337643539363765656463626136353164336439353963
65376461633630356162313432633131346661643331626235393465343862303065
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := parseExtraVarsWithVault("@"+path, ""); err == nil {
		t.Fatal("expected vault extra vars to require password")
	}
}

func TestParseModuleArgsKeyValueAndRaw(t *testing.T) {
	params := parseModuleArgs(`name="backup job" minute=0 enabled=yes`)
	if params["name"] != "backup job" {
		t.Fatalf("expected quoted module arg, got %#v", params["name"])
	}
	if params["minute"] != 0 {
		t.Fatalf("expected minute=0, got %#v", params["minute"])
	}
	if params["enabled"] != true {
		t.Fatalf("expected enabled=true, got %#v", params["enabled"])
	}

	raw := parseModuleArgs(`echo "hello world"`)
	if raw["_raw_params"] != `echo "hello world"` {
		t.Fatalf("expected raw params fallback, got %#v", raw["_raw_params"])
	}
}

func TestApplyConfigDefaultsToInventory(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	inv := inventory.NewInventory()
	host := &inventory.Host{
		Name:      "app01",
		Address:   "10.0.0.10",
		Port:      22,
		Variables: map[string]string{},
	}
	inv.AllHosts[host.Name] = host

	hostKeyChecking := false
	cfg := &config.Config{
		Defaults: config.Defaults{
			RemoteUser:      "deploy",
			PrivateKeyFile:  "~/.ssh/id_rsa",
			HostKeyChecking: &hostKeyChecking,
		},
	}

	applyConfigDefaultsToInventory(inv, cfg)

	if host.User != "deploy" {
		t.Fatalf("expected user deploy, got %q", host.User)
	}
	expectedKey := filepath.Join(homeDir, ".ssh/id_rsa")
	if host.PrivateKey != expectedKey {
		t.Fatalf("expected private key %q, got %q", expectedKey, host.PrivateKey)
	}
	if host.SSHCommonArgs != "-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null" {
		t.Fatalf("unexpected SSHCommonArgs: %q", host.SSHCommonArgs)
	}
}

func TestApplyConfigDefaultsDoesNotOverrideExistingHostSettings(t *testing.T) {
	inv := inventory.NewInventory()
	host := &inventory.Host{
		Name:          "app01",
		Address:       "10.0.0.10",
		Port:          22,
		User:          "root",
		PrivateKey:    "/tmp/existing",
		SSHCommonArgs: "-o StrictHostKeyChecking=yes",
		Variables:     map[string]string{},
	}
	inv.AllHosts[host.Name] = host

	hostKeyChecking := false
	cfg := &config.Config{
		Defaults: config.Defaults{
			RemoteUser:      "deploy",
			PrivateKeyFile:  "~/.ssh/id_rsa",
			HostKeyChecking: &hostKeyChecking,
		},
	}

	applyConfigDefaultsToInventory(inv, cfg)

	if host.User != "root" {
		t.Fatalf("expected existing user to be preserved, got %q", host.User)
	}
	if host.PrivateKey != "/tmp/existing" {
		t.Fatalf("expected existing private key to be preserved, got %q", host.PrivateKey)
	}
	if host.SSHCommonArgs != "-o StrictHostKeyChecking=yes" {
		t.Fatalf("expected existing SSHCommonArgs to be preserved, got %q", host.SSHCommonArgs)
	}
}

func TestApplyConfigDefaultsResolvesPrivateKeyRelativeToConfig(t *testing.T) {
	inv := inventory.NewInventory()
	host := &inventory.Host{
		Name:      "app01",
		Address:   "10.0.0.10",
		Port:      22,
		Variables: map[string]string{},
	}
	inv.AllHosts[host.Name] = host

	cfg := &config.Config{
		Path: filepath.Join("/tmp/project", "ansible.cfg"),
		Defaults: config.Defaults{
			PrivateKeyFile: "keys/deploy",
		},
	}

	applyConfigDefaultsToInventory(inv, cfg)

	if host.PrivateKey != filepath.Join("/tmp/project", "keys/deploy") {
		t.Fatalf("expected resolved private key path, got %q", host.PrivateKey)
	}
}

func TestSSHClientConfigFromDefaults(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{
			Timeout: 15,
		},
	}

	sshCfg := sshClientConfigFromDefaults(cfg)
	if sshCfg == nil {
		t.Fatal("expected ssh config")
	}
	if sshCfg.Timeout != 15*time.Second {
		t.Fatalf("expected timeout 15s, got %v", sshCfg.Timeout)
	}
}

func TestResolveVaultPasswordFromFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	passFile := filepath.Join(dir, ".vault-pass")
	if err := os.WriteFile(passFile, []byte("file-secret\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg := &config.Config{
		Path: filepath.Join(dir, "ansible.cfg"),
		Defaults: config.Defaults{
			VaultPasswordFile: ".vault-pass",
		},
	}

	password, err := resolveVaultPassword(cfg)
	if err != nil {
		t.Fatalf("resolveVaultPassword() error = %v", err)
	}
	if password != "file-secret" {
		t.Fatalf("expected file-secret, got %q", password)
	}

	t.Setenv("ANSIBLE_VAULT_PASSWORD", "env-secret")
	password, err = resolveVaultPassword(cfg)
	if err != nil {
		t.Fatalf("resolveVaultPassword(env) error = %v", err)
	}
	if password != "env-secret" {
		t.Fatalf("expected env-secret, got %q", password)
	}
}

func TestResolveLimitPattern(t *testing.T) {
	// 1. 常规模式
	got, err := resolveLimitPattern("web1,web2")
	if err != nil || got != "web1,web2" {
		t.Fatalf("expected web1,web2, got %q, err %v", got, err)
	}

	// 2. 空字符
	got, err = resolveLimitPattern("")
	if err != nil || got != "" {
		t.Fatalf("expected empty, got %q, err %v", got, err)
	}

	// 3. @file 正常读取，包含注释与空行
	dir := t.TempDir()
	retryPath := filepath.Join(dir, "failed.retry")
	content := "# comment line\nweb1\n\nweb2\n# trailing comment\n"
	if err := os.WriteFile(retryPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	got, err = resolveLimitPattern("@" + retryPath)
	if err != nil {
		t.Fatalf("unexpected error reading retry file: %v", err)
	}
	if got != "web1,web2" {
		t.Fatalf("expected web1,web2, got %q", got)
	}

	// 4. @file 文件不存在报错
	_, err = resolveLimitPattern("@/non/existent/path.retry")
	if err == nil {
		t.Fatal("expected error for non existent retry file")
	}
}

func TestWriteRetryFile(t *testing.T) {
	dir := t.TempDir()
	playbookPath := filepath.Join(dir, "deploy.yml")

	// 无失败主机时不写入
	rf, err := writeRetryFile(playbookPath, nil)
	if err != nil || rf != "" {
		t.Fatalf("expected empty retry file, got %q, err %v", rf, err)
	}

	// 写入失败主机并去重/排序
	currDir, _ := os.Getwd()
	defer os.Chdir(currDir)
	_ = os.Chdir(dir)

	rf, err = writeRetryFile(playbookPath, []string{"web2", "web1"})
	if err != nil {
		t.Fatal(err)
	}
	if rf != "deploy.retry" {
		t.Fatalf("expected deploy.retry, got %q", rf)
	}

	data, err := os.ReadFile(rf)
	if err != nil {
		t.Fatal(err)
	}
	expected := "web1\nweb2\n"
	if string(data) != expected {
		t.Fatalf("expected %q, got %q", expected, string(data))
	}
}

func TestApplyConnectionOptionsToInventory(t *testing.T) {
	inv := inventory.NewInventory()
	h := &inventory.Host{
		Name:    "srv1",
		Address: "10.0.0.1",
	}
	inv.AllHosts[h.Name] = h

	opts := ConnectionOptions{
		Connection:    "local",
		PrivateKey:    "~/.ssh/test_id_rsa",
		SSHCommonArgs: "-o StrictHostKeyChecking=no",
		SSHExtraArgs:  "-o Port=2222",
	}

	applyConnectionOptionsToInventory(inv, opts)

	if h.Connection != "local" {
		t.Fatalf("expected connection local, got %q", h.Connection)
	}
	if !filepath.IsAbs(h.PrivateKey) {
		t.Fatalf("expected expanded private key path, got %q", h.PrivateKey)
	}
	if h.SSHCommonArgs != "-o StrictHostKeyChecking=no -o Port=2222" {
		t.Fatalf("unexpected ssh common args: %q", h.SSHCommonArgs)
	}
}
