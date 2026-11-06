package playbook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/module"
	"go-ansible/pkg/ssh"
)

func intPtr(v int) *int {
	return &v
}

func TestParseRoleSpec(t *testing.T) {
	name, vars, when, tags := parseRoleSpec("web")
	if name != "web" {
		t.Fatalf("expected role name web, got %q", name)
	}
	if vars != nil {
		t.Fatalf("expected nil vars for string role, got %#v", vars)
	}
	if when != nil || tags != nil {
		t.Fatalf("expected nil when/tags for string role")
	}

	name, vars, when, tags = parseRoleSpec(map[string]interface{}{
		"role": "app",
		"vars": map[string]interface{}{"port": 8080},
		"env":  "prod",
		"when": "enabled == true",
		"tags": []string{"app_tag"},
	})
	if name != "app" {
		t.Fatalf("expected role name app, got %q", name)
	}
	if vars["port"] != 8080 || vars["env"] != "prod" {
		t.Fatalf("unexpected inline vars: %#v", vars)
	}
	if when != "enabled == true" {
		t.Fatalf("expected when 'enabled == true', got %#v", when)
	}
	if len(tags) != 1 || tags[0] != "app_tag" {
		t.Fatalf("expected tags [app_tag], got %#v", tags)
	}
}

func TestLoadPlayVarsFiles(t *testing.T) {
	dir := t.TempDir()
	varsFile := filepath.Join(dir, "vars.yml")
	if err := os.WriteFile(varsFile, []byte("env: prod\nport: 8080\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	executor := NewExecutor(inventory.NewInventory())
	executor.baseDir = dir

	playVars := map[string]interface{}{
		"port": 9090,
	}
	play := &Play{VarsFiles: []string{"vars.yml"}}

	if err := executor.loadPlayVarsFiles(play, playVars); err != nil {
		t.Fatalf("loadPlayVarsFiles() error = %v", err)
	}

	if playVars["env"] != "prod" {
		t.Fatalf("expected env from vars file, got %#v", playVars["env"])
	}
	if playVars["port"] != 8080 {
		t.Fatalf("expected vars_files to override play vars, got %#v", playVars["port"])
	}
}

func TestLoadPlayRoles(t *testing.T) {
	dir := t.TempDir()
	roleDir := filepath.Join(dir, "roles", "web")
	for _, subdir := range []string{"tasks", "handlers", "defaults", "vars"} {
		if err := os.MkdirAll(filepath.Join(roleDir, subdir), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
	}

	files := map[string]string{
		filepath.Join(roleDir, "tasks", "main.yml"): `
- name: Install package
  shell: echo install
`,
		filepath.Join(roleDir, "handlers", "main.yml"): `
- name: restart service
  listen: restart web
  service:
    name: nginx
    state: restarted
`,
		filepath.Join(roleDir, "defaults", "main.yml"): "pkg_state: present\n",
		filepath.Join(roleDir, "vars", "main.yml"):     "service_name: nginx\n",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}

	executor := NewExecutor(inventory.NewInventory())
	executor.baseDir = dir

	play := &Play{
		Roles: []interface{}{
			map[string]interface{}{
				"role": "web",
				"vars": map[string]interface{}{"pkg_state": "latest"},
				"env":  "prod",
			},
		},
	}

	tasks, handlers, roleVars, roleDefaults, err := executor.loadPlayRoles(play)
	if err != nil {
		t.Fatalf("loadPlayRoles() error = %v", err)
	}

	if len(tasks) != 1 || tasks[0].ModuleName != "shell" {
		t.Fatalf("unexpected role tasks: %#v", tasks)
	}
	if len(handlers) != 1 || handlers[0].Listen != "restart web" {
		t.Fatalf("unexpected role handlers: %#v", handlers)
	}
	if roleVars["service_name"] != "nginx" || roleDefaults["pkg_state"] != "present" || roleVars["pkg_state"] != "latest" || roleVars["env"] != "prod" {
		t.Fatalf("unexpected merged role vars/defaults: roleVars=%#v, roleDefaults=%#v", roleVars, roleDefaults)
	}
}

func TestLoadPlayRolesUsesConfiguredRolePaths(t *testing.T) {
	dir := t.TempDir()
	roleDir := filepath.Join(dir, "vendor-roles", "web")
	for _, subdir := range []string{"tasks", "handlers", "defaults", "vars"} {
		if err := os.MkdirAll(filepath.Join(roleDir, subdir), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(roleDir, "tasks", "main.yml"), []byte(`
- name: Vendor role task
  shell: echo vendor
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	executor := NewExecutor(inventory.NewInventory())
	executor.baseDir = dir
	executor.SetRolePaths([]string{"vendor-roles"})

	play := &Play{
		Roles: []interface{}{"web"},
	}

	tasks, _, _, _, err := executor.loadPlayRoles(play)
	if err != nil {
		t.Fatalf("loadPlayRoles() error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].Name != "Vendor role task" {
		t.Fatalf("expected role to be resolved from configured role path, got %#v", tasks)
	}
}

func TestNotifyHandlersMatchesListen(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())
	host := &inventory.Host{Name: "web1"}
	handler := &Handler{Name: "restart service", Listen: "restart web"}

	executor.notifyHandlers("restart web", host, map[string]interface{}{}, nil, []*Handler{handler})

	if len(executor.pendingHandlers) != 1 {
		t.Fatalf("expected handler to be queued, got %d", len(executor.pendingHandlers))
	}
	if executor.pendingHandlers[0].handler.Name != "restart service" {
		t.Fatalf("unexpected handler queued: %#v", executor.pendingHandlers[0].handler)
	}
}

func TestExecuteTaskForHostSkipsRunOnceOnNonFirstHost(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())
	hosts := []*inventory.Host{{Name: "web1"}, {Name: "web2"}}
	task := &Task{Name: "Run once", RunOnce: true}

	results, err := executor.executeTaskForHost(hosts, hosts[1], task, map[string]interface{}{}, nil, map[string]bool{})
	if err != nil {
		t.Fatalf("executeTaskForHost() error = %v", err)
	}
	if len(results) != 1 || !results[0].Skipped {
		t.Fatalf("expected run_once task to be skipped on non-first host, got %#v", results)
	}
}

func TestShouldRunTaskByTags(t *testing.T) {
	tests := []struct {
		name     string
		selected []string
		skip     []string
		taskTags []string
		want     bool
	}{
		{name: "no filter runs untagged", want: true},
		{name: "no filter skips never", taskTags: []string{"never"}, want: false},
		{name: "selected tag runs matching task", selected: []string{"deploy"}, taskTags: []string{"deploy"}, want: true},
		{name: "selected tag skips untagged task", selected: []string{"deploy"}, want: false},
		{name: "selected tag runs always task", selected: []string{"deploy"}, taskTags: []string{"always"}, want: true},
		{name: "selected never runs never task", selected: []string{"never"}, taskTags: []string{"never"}, want: true},
		{name: "selected all runs normal task", selected: []string{"all"}, taskTags: []string{"deploy"}, want: true},
		{name: "selected all skips never task", selected: []string{"all"}, taskTags: []string{"never"}, want: false},
		{name: "selected all and never runs never task", selected: []string{"all", "never"}, taskTags: []string{"never"}, want: true},
		{name: "skip matching tag wins over selected", selected: []string{"deploy"}, skip: []string{"deploy"}, taskTags: []string{"deploy"}, want: false},
		{name: "skip always skips always task", selected: []string{"deploy"}, skip: []string{"always"}, taskTags: []string{"always"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := NewExecutor(inventory.NewInventory())
			executor.SetTags(tt.selected)
			executor.SetSkipTags(tt.skip)

			if got := executor.shouldRunTaskByTags(tt.taskTags); got != tt.want {
				t.Fatalf("shouldRunTaskByTags(%v) = %v, want %v", tt.taskTags, got, tt.want)
			}
		})
	}
}

func TestExecutePlayHonorsSkipTags(t *testing.T) {
	inv := inventory.NewInventory()
	host := &inventory.Host{Name: "web1", Address: "127.0.0.1", Port: 22}
	inv.AllHosts[host.Name] = host

	executor := NewExecutor(inv)
	executor.SetTags([]string{"all"})
	executor.SetSkipTags([]string{"maintenance"})

	play := &Play{
		Hosts: "web1",
		Tasks: []*Task{
			{
				Name:       "deploy",
				ModuleName: "debug",
				Tags:       []string{"deploy"},
				Params: map[string]interface{}{
					"msg": "deploy",
				},
			},
			{
				Name:       "maintenance",
				ModuleName: "debug",
				Tags:       []string{"maintenance"},
				Params: map[string]interface{}{
					"msg": "maintenance",
				},
			},
		},
	}

	result, err := executor.ExecutePlay(play)
	if err != nil {
		t.Fatalf("ExecutePlay() error = %v", err)
	}

	hostResult := result.Hosts["web1"]
	if hostResult == nil || len(hostResult.Tasks) != 2 {
		t.Fatalf("expected 2 task results, got %#v", hostResult)
	}
	if hostResult.Tasks[0].Skipped || hostResult.Tasks[0].Stdout != "deploy" {
		t.Fatalf("expected deploy task to run, got %#v", hostResult.Tasks[0])
	}
	if !hostResult.Tasks[1].Skipped {
		t.Fatalf("expected maintenance task to be skipped, got %#v", hostResult.Tasks[1])
	}
}

func TestExecutePlayHonorsHostLimit(t *testing.T) {
	inv := inventory.NewInventory()
	host1 := &inventory.Host{Name: "web1", Address: "10.0.0.1", Port: 22}
	host2 := &inventory.Host{Name: "web2", Address: "10.0.0.2", Port: 22}
	host3 := &inventory.Host{Name: "db1", Address: "10.0.0.3", Port: 22}
	inv.AllHosts[host1.Name] = host1
	inv.AllHosts[host2.Name] = host2
	inv.AllHosts[host3.Name] = host3
	inv.Groups["web"] = &inventory.Group{
		Name:  "web",
		Hosts: []*inventory.Host{host1, host2},
	}

	executor := NewExecutor(inv)
	executor.SetLimit("web,!web2")

	play := &Play{
		Hosts: "all",
		Tasks: []*Task{
			{
				Name:       "limited task",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "ran",
				},
			},
		},
	}

	result, err := executor.ExecutePlay(play)
	if err != nil {
		t.Fatalf("ExecutePlay() error = %v", err)
	}
	if len(result.Hosts) != 1 {
		t.Fatalf("expected only one limited host result, got %#v", result.Hosts)
	}
	if result.Hosts["web1"] == nil || len(result.Hosts["web1"].Tasks) != 1 {
		t.Fatalf("expected web1 to run task, got %#v", result.Hosts)
	}
	if result.Hosts["web2"] != nil || result.Hosts["db1"] != nil {
		t.Fatalf("expected web2/db1 to be excluded, got %#v", result.Hosts)
	}
}

func TestApplyHostLimitSupportsWildcardAndAddress(t *testing.T) {
	inv := inventory.NewInventory()
	host1 := &inventory.Host{Name: "web1", Address: "10.0.0.1", Port: 22}
	host2 := &inventory.Host{Name: "web2", Address: "10.0.0.2", Port: 22}
	host3 := &inventory.Host{Name: "db1", Address: "10.0.0.3", Port: 22}
	inv.AllHosts[host1.Name] = host1
	inv.AllHosts[host2.Name] = host2
	inv.AllHosts[host3.Name] = host3

	executor := NewExecutor(inv)
	executor.SetLimit("web*,!10.0.0.2")

	limited, err := executor.applyHostLimit([]*inventory.Host{host1, host2, host3})
	if err != nil {
		t.Fatalf("applyHostLimit() error = %v", err)
	}
	if len(limited) != 1 || limited[0].Name != "web1" {
		t.Fatalf("expected only web1, got %#v", limited)
	}
}

func TestExecutePlayHostsPatternSupportsUnionAndExclusion(t *testing.T) {
	inv := inventory.NewInventory()
	web1 := &inventory.Host{Name: "web1", Address: "10.0.0.1", Port: 22}
	web2 := &inventory.Host{Name: "web2", Address: "10.0.0.2", Port: 22}
	db1 := &inventory.Host{Name: "db1", Address: "10.0.0.3", Port: 22}
	cache1 := &inventory.Host{Name: "cache1", Address: "10.0.0.4", Port: 22}
	for _, host := range []*inventory.Host{web1, web2, db1, cache1} {
		inv.AllHosts[host.Name] = host
	}
	inv.Groups["web"] = &inventory.Group{Name: "web", Hosts: []*inventory.Host{web1, web2}}
	inv.Groups["db"] = &inventory.Group{Name: "db", Hosts: []*inventory.Host{db1}}

	executor := NewExecutor(inv)

	play := &Play{
		Hosts: "web:db,!web2",
		Tasks: []*Task{
			{
				Name:       "pattern task",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "ran",
				},
			},
		},
	}

	result, err := executor.ExecutePlay(play)
	if err != nil {
		t.Fatalf("ExecutePlay() error = %v", err)
	}
	if result.Hosts["web1"] == nil || result.Hosts["db1"] == nil {
		t.Fatalf("expected web1 and db1 to run, got %#v", result.Hosts)
	}
	if result.Hosts["web2"] != nil || result.Hosts["cache1"] != nil {
		t.Fatalf("expected web2/cache1 to be excluded, got %#v", result.Hosts)
	}
}

func TestInventoryMatchHostsSupportsWildcardOnly(t *testing.T) {
	inv := inventory.NewInventory()
	web1 := &inventory.Host{Name: "web1", Address: "10.0.0.1", Port: 22}
	web2 := &inventory.Host{Name: "web2", Address: "10.0.0.2", Port: 22}
	db1 := &inventory.Host{Name: "db1", Address: "10.0.0.3", Port: 22}
	for _, host := range []*inventory.Host{web1, web2, db1} {
		inv.AllHosts[host.Name] = host
	}

	hosts, err := inv.MatchHosts("web*")
	if err != nil {
		t.Fatalf("MatchHosts() error = %v", err)
	}
	if len(hosts) != 2 {
		t.Fatalf("expected 2 web hosts, got %#v", hosts)
	}
}

func TestListPlayHostsAppliesPatternLimitAndExtraVars(t *testing.T) {
	inv := inventory.NewInventory()
	web1 := &inventory.Host{Name: "web1", Address: "10.0.0.1", Port: 22}
	web2 := &inventory.Host{Name: "web2", Address: "10.0.0.2", Port: 22}
	db1 := &inventory.Host{Name: "db1", Address: "10.0.0.3", Port: 22}
	for _, host := range []*inventory.Host{web1, web2, db1} {
		inv.AllHosts[host.Name] = host
	}
	inv.Groups["web"] = &inventory.Group{Name: "web", Hosts: []*inventory.Host{web1, web2}}
	inv.Groups["db"] = &inventory.Group{Name: "db", Hosts: []*inventory.Host{db1}}

	pb := &Playbook{
		Plays: []*Play{
			{Name: "targeted", Hosts: "{{ target_group }}:db"},
		},
	}

	listed, err := ListPlayHosts(pb, inv, "!web2", map[string]interface{}{"target_group": "web"})
	if err != nil {
		t.Fatalf("ListPlayHosts() error = %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected 1 listed play, got %#v", listed)
	}
	if strings.Join(listed[0].Items, ",") != "db1,web1" {
		t.Fatalf("unexpected listed hosts: %#v", listed[0].Items)
	}
}

func TestExecutePlayHonorsStartAtTask(t *testing.T) {
	inv := inventory.NewInventory()
	host := &inventory.Host{Name: "web1", Address: "127.0.0.1", Port: 22}
	inv.AllHosts[host.Name] = host

	executor := NewExecutor(inv)
	executor.SetStartAtTask("target")

	play := &Play{
		Hosts: "web1",
		Tasks: []*Task{
			{
				Name:       "before",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "before",
				},
			},
			{
				Name: "container",
				Block: []*Task{
					{
						Name:       "target",
						ModuleName: "debug",
						Params: map[string]interface{}{
							"msg": "target",
						},
					},
				},
			},
			{
				Name:       "after",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "after",
				},
			},
		},
	}

	result, err := executor.ExecutePlay(play)
	if err != nil {
		t.Fatalf("ExecutePlay() error = %v", err)
	}

	hostResult := result.Hosts["web1"]
	if hostResult == nil || len(hostResult.Tasks) != 3 {
		t.Fatalf("expected 3 task results, got %#v", hostResult)
	}
	if !hostResult.Tasks[0].Skipped {
		t.Fatalf("expected first task to be skipped, got %#v", hostResult.Tasks[0])
	}
	if hostResult.Tasks[1].Skipped || hostResult.Tasks[1].Stdout != "target" {
		t.Fatalf("expected target task to run, got %#v", hostResult.Tasks[1])
	}
	if hostResult.Tasks[2].Skipped || hostResult.Tasks[2].Stdout != "after" {
		t.Fatalf("expected following task to run, got %#v", hostResult.Tasks[2])
	}
}

func TestListPlaybookIncludesImportedTasksAndTags(t *testing.T) {
	dir := t.TempDir()
	includePath := filepath.Join(dir, "included.yml")
	if err := os.WriteFile(includePath, []byte(`
- name: imported
  debug:
    msg: imported
  tags: imported
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	pb := &Playbook{
		Path: filepath.Join(dir, "site.yml"),
		Plays: []*Play{
			{
				Name:  "deploy",
				Hosts: "web",
				Tags:  []string{"play-tag"},
				Tasks: []*Task{
					{
						Name: "block",
						Tags: []string{"block-tag"},
						Block: []*Task{
							{
								Name:       "inside block",
								ModuleName: "debug",
								Tags:       []string{"task-tag"},
							},
						},
					},
					{
						Name:       "import file",
						ModuleName: "import_tasks",
						Params:     map[string]interface{}{"_raw_params": "included.yml"},
						Tags:       []string{"import-tag"},
					},
				},
			},
		},
	}

	listed, err := ListPlaybook(pb, nil)
	if err != nil {
		t.Fatalf("ListPlaybook() error = %v", err)
	}
	if len(listed) != 1 || len(listed[0].Tasks) != 3 {
		t.Fatalf("unexpected listed playbook: %#v", listed)
	}
	if listed[0].Tasks[0].Name != "inside block" || !containsTag(listed[0].Tasks[0].Tags, "block-tag") || !containsTag(listed[0].Tasks[0].Tags, "play-tag") {
		t.Fatalf("expected inherited block tags, got %#v", listed[0].Tasks[0])
	}
	if listed[0].Tasks[2].Name != "imported" || !containsTag(listed[0].Tasks[2].Tags, "imported") || !containsTag(listed[0].Tasks[2].Tags, "import-tag") {
		t.Fatalf("expected imported task and inherited tags, got %#v", listed[0].Tasks[2])
	}
	if !containsTag(listed[0].Tags, "play-tag") || !containsTag(listed[0].Tags, "imported") {
		t.Fatalf("expected collected tags, got %#v", listed[0].Tags)
	}
}

func TestExecutePlayAppliesRemoteUser(t *testing.T) {
	inv := inventory.NewInventory()
	host := &inventory.Host{
		Name:      "web1",
		Address:   "127.0.0.1",
		Port:      22,
		User:      "root",
		Variables: map[string]string{},
	}
	inv.AllHosts[host.Name] = host
	inv.Groups["web"] = &inventory.Group{
		Name:      "web",
		Hosts:     []*inventory.Host{host},
		Children:  []*inventory.Group{},
		Variables: map[string]string{},
	}

	executor := NewExecutor(inv)
	play := &Play{
		Hosts:      "web",
		RemoteUser: "{{ deploy_user }}",
		Vars: map[string]interface{}{
			"deploy_user": "deployer",
		},
		Tasks: []*Task{
			{
				Name:       "noop",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "ok",
				},
			},
		},
	}

	if _, err := executor.ExecutePlay(play); err != nil {
		t.Fatalf("ExecutePlay() error = %v", err)
	}
	if host.User != "deployer" {
		t.Fatalf("expected remote user to be updated, got %q", host.User)
	}
}

func TestParseLocalActionTask(t *testing.T) {
	task, err := normalizeTask(rawTask{
		"name":         "run locally",
		"local_action": "shell echo hello",
	})
	if err != nil {
		t.Fatalf("normalizeTask() error = %v", err)
	}
	if task.ModuleName != "shell" {
		t.Fatalf("expected shell module, got %q", task.ModuleName)
	}
	if task.Params["_raw_params"] != "echo hello" {
		t.Fatalf("unexpected params: %#v", task.Params)
	}
}

func TestExecuteTaskCheckModeSkipsExecution(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())
	executor.SetCheckMode(true)

	task := &Task{Name: "dry run", ModuleName: "shell", Params: map[string]interface{}{"_raw_params": "echo hi"}}
	results, err := executor.executeTask(&inventory.Host{Name: "web1"}, task, map[string]interface{}{}, nil)
	if err != nil {
		t.Fatalf("executeTask() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Failed {
		t.Fatalf("expected non-failed result, got %#v", results[0])
	}
	if results[0].Message != "check mode: execution skipped" {
		t.Fatalf("unexpected message: %#v", results[0].Message)
	}
}

func TestExecuteTaskCheckModeSupportedModuleRunsPlanner(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())
	executor.SetCheckMode(true)

	task := &Task{
		Name:       "plan directory",
		ModuleName: "file",
		Params: map[string]interface{}{
			"path":  "/tmp/go-ansible-check-mode",
			"state": "directory",
		},
		DelegateTo: "localhost",
	}

	results, err := executor.executeTask(&inventory.Host{Name: "web1"}, task, map[string]interface{}{}, nil)
	if err != nil {
		t.Fatalf("executeTask() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Message == "check mode: execution skipped" {
		t.Fatalf("expected supported module planner to run, got %#v", results[0])
	}
}

func TestSupportsCheckAndDiffModeIncludeTextModules(t *testing.T) {
	if !supportsCheckMode("lineinfile") {
		t.Fatal("expected lineinfile to support check mode")
	}
	if !supportsCheckMode("replace") {
		t.Fatal("expected replace to support check mode")
	}
	if !supportsDiffMode("lineinfile") {
		t.Fatal("expected lineinfile to support diff mode")
	}
	if !supportsDiffMode("replace") {
		t.Fatal("expected replace to support diff mode")
	}
}

func TestEvaluateUntilUsesRegisteredResult(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())
	ok := executor.evaluateUntil("probe", "probe", &module.Result{
		Stdout:   "ok",
		ExitCode: 0,
	}, nil, map[string]interface{}{
		"probe": true,
	})
	if !ok {
		t.Fatal("expected until condition to evaluate true")
	}
}

func TestIsLocalDelegate(t *testing.T) {
	if !isLocalDelegate("localhost", nil) {
		t.Fatal("expected localhost to be treated as local delegate")
	}
	if isLocalDelegate("web1", nil) {
		t.Fatal("expected web1 not to be treated as local delegate")
	}
}

func TestEffectiveGatherFactsDefaultsToFalse(t *testing.T) {
	if effectiveGatherFacts(&Play{}) {
		t.Fatal("expected gather_facts to default to false when omitted")
	}

	yes := true
	if !effectiveGatherFacts(&Play{GatherFacts: &yes}) {
		t.Fatal("expected explicit gather_facts: yes to be honored")
	}

	no := false
	if effectiveGatherFacts(&Play{GatherFacts: &no}) {
		t.Fatal("expected explicit gather_facts: no to be honored")
	}
}

func TestExecutorEffectiveGatherFactsHonorsConfigDefault(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())
	executor.SetGathering("implicit")

	if !executor.effectiveGatherFacts(&Play{}) {
		t.Fatal("expected implicit gathering config to enable gather facts by default")
	}

	no := false
	if executor.effectiveGatherFacts(&Play{GatherFacts: &no}) {
		t.Fatal("expected explicit gather_facts: no to override config default")
	}
}

func TestExecuteWithRetryHonorsDelayForFailures(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())
	start := time.Now()
	_, err := executor.executeWithRetry(&inventory.Host{Name: "web1"}, "missing", nil, &Task{
		Name:    "retry missing module",
		Retries: 1,
		Delay:   1,
	}, map[string]interface{}{}, false)
	if err == nil {
		t.Fatal("expected missing module error")
	}
	if time.Since(start) < time.Second {
		t.Fatal("expected retry delay to be applied")
	}
}

func TestResolveSerialBatchSize(t *testing.T) {
	if got := resolveSerialBatchSize("50%", 5); got != 3 {
		t.Fatalf("expected 3, got %d", got)
	}
	if got := resolveSerialBatchSize(2, 5); got != 2 {
		t.Fatalf("expected 2, got %d", got)
	}
}

func TestApplyEnvironmentToModuleParams(t *testing.T) {
	params := applyEnvironmentToModuleParams("shell", map[string]interface{}{
		"_raw_params": "echo hello",
	}, map[string]string{
		"APP_ENV": "prod",
	})

	raw, _ := params["_raw_params"].(string)
	if raw == "" || raw == "echo hello" {
		t.Fatalf("expected environment prefix in command, got %#v", params)
	}
}

func TestExceededMaxFailPercentage(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())
	result := &PlayResult{
		Hosts: map[string]*HostResult{
			"web1": {Tasks: []*TaskResult{{Failed: true}}},
			"web2": {Tasks: []*TaskResult{{Failed: false}}},
		},
	}
	if !executor.exceededMaxFailPercentage(result, 2, 40) {
		t.Fatal("expected fail percentage to be exceeded")
	}
}

func TestBuildHostBatches(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())
	hosts := []*inventory.Host{
		{Name: "web1"},
		{Name: "web2"},
		{Name: "web3"},
		{Name: "web4"},
		{Name: "web5"},
	}

	batches := executor.buildHostBatches(hosts, "40%")
	if len(batches) != 3 {
		t.Fatalf("expected 3 batches, got %d", len(batches))
	}
	if len(batches[0]) != 2 || len(batches[1]) != 2 || len(batches[2]) != 1 {
		t.Fatalf("unexpected batch sizes: %d %d %d", len(batches[0]), len(batches[1]), len(batches[2]))
	}
}

func TestNotifyHandlersPreservesEnvironment(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())
	host := &inventory.Host{Name: "web1"}
	handler := &Handler{Name: "restart service"}
	env := map[string]string{"APP_ENV": "prod"}

	executor.notifyHandlers("restart service", host, map[string]interface{}{}, env, []*Handler{handler})

	if len(executor.pendingHandlers) != 1 {
		t.Fatalf("expected 1 pending handler, got %d", len(executor.pendingHandlers))
	}
	if executor.pendingHandlers[0].env["APP_ENV"] != "prod" {
		t.Fatalf("expected handler env to be preserved, got %#v", executor.pendingHandlers[0].env)
	}
}

func TestTaskVarsForHostMergesInventoryAndFacts(t *testing.T) {
	inv := inventory.NewInventory()
	host := &inventory.Host{
		Name:      "web1.example.com",
		Address:   "10.0.0.1",
		Port:      2222,
		User:      "deploy",
		Variables: map[string]string{"host_var": "host", "shared": "host"},
	}
	inv.AllHosts[host.Name] = host
	inv.Variables["global_var"] = "global"
	inv.Groups["web"] = &inventory.Group{
		Name:      "web",
		Hosts:     []*inventory.Host{host},
		Variables: map[string]string{"group_var": "group", "shared": "group"},
	}

	executor := NewExecutor(inv)
	executor.facts[host.Name] = map[string]interface{}{
		"ansible_hostname": "web1",
		"probe":            map[string]interface{}{"failed": false},
	}

	vars := executor.taskVarsForHost(host, map[string]interface{}{
		"play_var": "play",
		"shared":   "play",
	})

	if vars["global_var"] != "global" || vars["group_var"] != "group" || vars["host_var"] != "host" {
		t.Fatalf("expected inventory vars to be merged, got %#v", vars)
	}
	if vars["shared"] != "play" {
		t.Fatalf("expected play vars to override inventory vars, got %#v", vars["shared"])
	}
	if vars["inventory_hostname"] != "web1.example.com" || vars["inventory_hostname_short"] != "web1" {
		t.Fatalf("unexpected inventory hostname vars: %#v", vars)
	}
	if vars["ansible_hostname"] != "web1" {
		t.Fatalf("expected gathered facts to be merged, got %#v", vars["ansible_hostname"])
	}
}

func TestExecuteTaskLoopRegisterStoresAggregateResult(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())
	executor.SetCheckMode(true)

	task := &Task{
		Name:       "loop register",
		ModuleName: "shell",
		Params:     map[string]interface{}{"_raw_params": "echo hi"},
		Loop:       []interface{}{"a", "b"},
		Register:   "probe",
	}

	results, err := executor.executeTask(&inventory.Host{Name: "web1"}, task, map[string]interface{}{}, nil)
	if err != nil {
		t.Fatalf("executeTask() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 loop results, got %d", len(results))
	}

	registered, ok := executor.facts["web1"]["probe"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected registered result map, got %#v", executor.facts["web1"]["probe"])
	}

	items, ok := registered["results"].([]map[string]interface{})
	if !ok || len(items) != 2 {
		t.Fatalf("expected 2 registered loop items, got %#v", registered["results"])
	}
	if items[0]["item"] != "a" || items[1]["item"] != "b" {
		t.Fatalf("unexpected registered items: %#v", items)
	}
}

func TestStoreRegisteredResultMergesModuleData(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())
	task := &Task{Register: "slurped"}

	executor.storeRegisteredResult("web1", task, []*TaskResult{
		{
			Stdout:   "aGVsbG8=",
			ExitCode: 0,
			Data: map[string]interface{}{
				"content":  "aGVsbG8=",
				"encoding": "base64",
				"source":   "/tmp/app.conf",
			},
		},
	})

	registered, ok := executor.facts["web1"]["slurped"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected registered result map, got %#v", executor.facts["web1"]["slurped"])
	}
	if registered["content"] != "aGVsbG8=" || registered["encoding"] != "base64" || registered["source"] != "/tmp/app.conf" {
		t.Fatalf("expected slurp data in registered result, got %#v", registered)
	}

	items := registered["results"].([]map[string]interface{})
	if len(items) != 1 || items[0]["content"] != "aGVsbG8=" {
		t.Fatalf("expected slurp data in loop result entry, got %#v", items)
	}
}

func TestStoreRegisteredResultMergesTextModuleData(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())
	task := &Task{Register: "edited"}

	executor.storeRegisteredResult("web1", task, []*TaskResult{
		{
			Changed: true,
			Data: map[string]interface{}{
				"path":        "/tmp/app.conf",
				"matched":     true,
				"count":       1,
				"action":      "replace",
				"created":     false,
				"encoding":    "utf-8",
				"newline":     "lf",
				"before":      "option=old\n",
				"after":       "option=new\n",
				"backup_file": "/tmp/app.conf.bak",
			},
		},
	})

	registered, ok := executor.facts["web1"]["edited"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected registered result map, got %#v", executor.facts["web1"]["edited"])
	}
	if registered["path"] != "/tmp/app.conf" || registered["matched"] != true || registered["count"] != 1 || registered["action"] != "replace" || registered["encoding"] != "utf-8" || registered["newline"] != "lf" || registered["backup_file"] != "/tmp/app.conf.bak" {
		t.Fatalf("expected text module data in registered result, got %#v", registered)
	}

	items := registered["results"].([]map[string]interface{})
	if len(items) != 1 || items[0]["after"] != "option=new\n" || items[0]["action"] != "replace" || items[0]["created"] != false {
		t.Fatalf("expected text module data in loop result entry, got %#v", items)
	}
}

func TestEvaluateWhenSupportsNestedValues(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())
	vars := map[string]interface{}{
		"probe": map[string]interface{}{"failed": false},
	}

	if executor.evaluateWhen("probe.failed", vars) {
		t.Fatal("expected nested false value to evaluate false")
	}
	if !executor.evaluateWhen("not probe.failed", vars) {
		t.Fatal("expected not probe.failed to evaluate true")
	}
}

func TestEvaluateWhenSupportsComparisons(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())
	vars := map[string]interface{}{
		"probe": map[string]interface{}{
			"rc":     1,
			"stdout": "ready",
			"tags":   []interface{}{"api", "blue"},
		},
	}

	if !executor.evaluateWhen("probe.rc != 0", vars) {
		t.Fatal("expected numeric comparison to evaluate true")
	}
	if !executor.evaluateWhen(`probe.stdout == "ready"`, vars) {
		t.Fatal("expected string comparison to evaluate true")
	}
	if executor.evaluateWhen("missing_var is defined", vars) {
		t.Fatal("expected missing_var is defined to evaluate false")
	}
	if !executor.evaluateWhen("missing_var is not defined", vars) {
		t.Fatal("expected missing_var is not defined to evaluate true")
	}
	if !executor.evaluateWhen(`probe.rc != 0 and probe.stdout == "ready"`, vars) {
		t.Fatal("expected and expression to evaluate true")
	}
	if !executor.evaluateWhen(`probe.stdout == "missing" or probe.stdout == "ready"`, vars) {
		t.Fatal("expected or expression to evaluate true")
	}
	if !executor.evaluateWhen(`"api" in probe.tags`, vars) {
		t.Fatal("expected in expression to evaluate true")
	}
	if !executor.evaluateWhen(`"green" not in probe.tags`, vars) {
		t.Fatal("expected not in expression to evaluate true")
	}
}

func TestResolveStringDoesNotLoopOnMissingVariable(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())
	done := make(chan string, 1)

	go func() {
		done <- executor.resolveString("restart {{ MissingVar }}", map[string]interface{}{})
	}()

	select {
	case got := <-done:
		if got != "restart {{ MissingVar }}" {
			t.Fatalf("unexpected resolved string: %q", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("resolveString() hung on missing variable")
	}
}

func TestResolveStringSupportsFilters(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())
	got := executor.resolveString("env={{ Env | default('prod') | upper }} host={{ Host | quote }}", map[string]interface{}{
		"Host": "web 01",
	})
	if got != "env=PROD host='web 01'" {
		t.Fatalf("unexpected filtered resolve result: %q", got)
	}

	got = executor.resolveString("tags={{ Tags | join(',') }} msg={{ Message | trim | replace('foo', 'bar') }}", map[string]interface{}{
		"Tags":    []interface{}{"api", "blue"},
		"Message": "  foo-ready  ",
	})
	if got != "tags=api,blue msg=bar-ready" {
		t.Fatalf("unexpected filtered join/replace result: %q", got)
	}

	got = executor.resolveString("count={{ Tags | length }} first={{ Tags | first }} last={{ Tags | last }} parts={{ CSV | split(',') | join('|') }}", map[string]interface{}{
		"Tags": []interface{}{"api", "blue"},
		"CSV":  "a,b,c",
	})
	if got != "count=2 first=api last=blue parts=a|b|c" {
		t.Fatalf("unexpected list filter result: %q", got)
	}

	got = executor.resolveString("json={{ Data | to_json }} value={{ Raw | from_json | first }}", map[string]interface{}{
		"Data": map[string]interface{}{"name": "api"},
		"Raw":  `["x","y"]`,
	})
	if got != `json={"name":"api"} value=x` {
		t.Fatalf("unexpected json filter result: %q", got)
	}
}

func TestResolveStringSupportsLookupEnvAndFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(path, []byte("file-secret\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	t.Setenv("GO_ANSIBLE_LOOKUP_SECRET", "env-secret")

	executor := NewExecutor(inventory.NewInventory())
	executor.baseDir = dir

	got := executor.resolveString("env={{ lookup('env', 'GO_ANSIBLE_LOOKUP_SECRET') }} file={{ lookup('file', 'secret.txt') }}", map[string]interface{}{})
	if got != "env=env-secret file=file-secret" {
		t.Fatalf("unexpected lookup result: %q", got)
	}
}

func TestResolveStringSupportsLookupPipeAndTemplate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "message.j2")
	if err := os.WriteFile(path, []byte("hello {{ Name }}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	executor := NewExecutor(inventory.NewInventory())
	executor.baseDir = dir

	got := executor.resolveString("pipe={{ lookup('pipe', 'printf pipe-ok') }} template={{ lookup('template', 'message.j2') }}", map[string]interface{}{
		"Name": "elite",
	})
	if got != "pipe=pipe-ok template=hello elite" {
		t.Fatalf("unexpected lookup pipe/template result: %q", got)
	}
}

func TestResolveStringSupportsPathAndBase64Filters(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())

	got := executor.resolveString("base={{ Path | basename }} dir={{ Path | dirname }} enc={{ Secret | b64encode }} dec={{ Encoded | b64decode }}", map[string]interface{}{
		"Path":    "/tmp/app/config.yml",
		"Secret":  "hello",
		"Encoded": "d29ybGQ=",
	})
	if got != "base=config.yml dir=/tmp/app enc=aGVsbG8= dec=world" {
		t.Fatalf("unexpected path/base64 filter result: %q", got)
	}
}

func TestResolveValueSupportsRawTaskLoopItem(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())
	value := executor.resolveValue(rawTask{
		"cmd": "echo {{ ProjectName }}",
	}, map[string]interface{}{
		"ProjectName": "elite.km",
	})

	resolved, ok := value.(map[string]interface{})
	if !ok {
		t.Fatalf("expected resolved rawTask to become map[string]interface{}, got %#v", value)
	}
	if resolved["cmd"] != "echo elite.km" {
		t.Fatalf("unexpected resolved command: %#v", resolved)
	}
}

func TestApplyExecutionOutcomeMarksNonZeroExitCodeAsFailed(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())
	result := &TaskResult{}

	executor.applyExecutionOutcome(result, &Task{}, &module.Result{
		ExitCode: 127,
		Stderr:   "command not found",
	}, nil)

	if !result.Failed {
		t.Fatal("expected non-zero exit code to mark task failed")
	}
	if result.Message == "" {
		t.Fatal("expected failure message to be populated")
	}
}

func TestApplyTaskResultConditionsOverridesChangedAndFailed(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())
	result := &TaskResult{
		Changed:  true,
		Failed:   true,
		Stdout:   "ok",
		ExitCode: 1,
	}
	task := &Task{
		Register:    "probe",
		ChangedWhen: "probe.rc == 0",
		FailedWhen:  `probe.stdout != "ok"`,
	}

	executor.applyTaskResultConditions(result, task, map[string]interface{}{})

	if result.Changed {
		t.Fatal("expected changed_when to override changed=false")
	}
	if result.Failed {
		t.Fatal("expected failed_when to override failed=false")
	}
}

func TestExecuteTaskMergesTaskVars(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())

	task := &Task{
		Name:       "task vars",
		ModuleName: "debug",
		Params:     map[string]interface{}{"msg": "{{ app_name }}"},
		Vars: map[string]interface{}{
			"app_name": "elite.km",
		},
	}

	results, err := executor.executeTask(&inventory.Host{Name: "web1"}, task, map[string]interface{}{}, nil)
	if err != nil {
		t.Fatalf("executeTask() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Stdout != "elite.km" {
		t.Fatalf("expected debug stdout from task vars, got %#v", results[0])
	}
}

func TestExecuteTaskDebugVarUsesCurrentContext(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())

	task := &Task{
		Name:       "debug var",
		ModuleName: "debug",
		Params:     map[string]interface{}{"var": "probe.stdout"},
	}

	results, err := executor.executeTask(&inventory.Host{Name: "web1"}, task, map[string]interface{}{
		"probe": map[string]interface{}{"stdout": "pong"},
	}, nil)
	if err != nil {
		t.Fatalf("executeTask() error = %v", err)
	}
	if results[0].Stdout != "probe.stdout: pong" {
		t.Fatalf("unexpected debug output: %#v", results[0])
	}
}

func TestExecuteTaskSetFactStoresResolvedFacts(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())

	task := &Task{
		Name:       "set fact",
		ModuleName: "set_fact",
		Params: map[string]interface{}{
			"app_name": "{{ ProjectName }}",
			"enabled":  true,
		},
	}

	results, err := executor.executeTask(&inventory.Host{Name: "web1"}, task, map[string]interface{}{
		"ProjectName": "elite.km",
	}, nil)
	if err != nil {
		t.Fatalf("executeTask() error = %v", err)
	}
	if len(results) != 1 || results[0].Failed {
		t.Fatalf("unexpected task results: %#v", results)
	}

	if executor.facts["web1"]["app_name"] != "elite.km" || executor.facts["web1"]["enabled"] != true {
		t.Fatalf("expected set_fact to store resolved facts, got %#v", executor.facts["web1"])
	}
}

func TestExecuteTaskIncludeVarsLoadsFile(t *testing.T) {
	dir := t.TempDir()
	varsPath := filepath.Join(dir, "app.yml")
	if err := os.WriteFile(varsPath, []byte("app_name: elite.km\nenabled: true\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	executor := NewExecutor(inventory.NewInventory())
	executor.baseDir = dir

	task := &Task{
		Name:       "include vars",
		ModuleName: "include_vars",
		Params: map[string]interface{}{
			"file": "app.yml",
		},
	}

	results, err := executor.executeTask(&inventory.Host{Name: "web1"}, task, map[string]interface{}{}, nil)
	if err != nil {
		t.Fatalf("executeTask() error = %v", err)
	}
	if len(results) != 1 || results[0].Failed {
		t.Fatalf("unexpected task results: %#v", results)
	}
	if executor.facts["web1"]["app_name"] != "elite.km" || executor.facts["web1"]["enabled"] != true {
		t.Fatalf("expected include_vars facts, got %#v", executor.facts["web1"])
	}
}

func TestExecuteTaskIncludeVarsLoadsDirectoryWithNamespace(t *testing.T) {
	dir := t.TempDir()
	varsDir := filepath.Join(dir, "vars")
	if err := os.MkdirAll(varsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	files := map[string]string{
		filepath.Join(varsDir, "01-base.yml"):     "app_name: elite.km\nport: 8080\n",
		filepath.Join(varsDir, "02-override.yml"): "port: 9090\n",
		filepath.Join(varsDir, "README.txt"):      "ignored\n",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}

	executor := NewExecutor(inventory.NewInventory())
	executor.baseDir = dir

	task := &Task{
		Name:       "include vars dir",
		ModuleName: "include_vars",
		Params: map[string]interface{}{
			"dir":                       "vars",
			"name":                      "app",
			"ignore_unknown_extensions": true,
		},
	}

	results, err := executor.executeTask(&inventory.Host{Name: "web1"}, task, map[string]interface{}{}, nil)
	if err != nil {
		t.Fatalf("executeTask() error = %v", err)
	}
	if len(results) != 1 || results[0].Failed {
		t.Fatalf("unexpected task results: %#v", results)
	}

	appVars, ok := executor.facts["web1"]["app"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected namespaced vars, got %#v", executor.facts["web1"])
	}
	if appVars["app_name"] != "elite.km" || appVars["port"] != 9090 {
		t.Fatalf("expected merged namespaced vars, got %#v", appVars)
	}
}

func TestExecuteTaskFailModuleFailsTask(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())

	task := &Task{
		Name:       "fail task",
		ModuleName: "fail",
		Params: map[string]interface{}{
			"msg": "boom",
		},
	}

	results, err := executor.executeTask(&inventory.Host{Name: "web1"}, task, map[string]interface{}{}, nil)
	if err != nil {
		t.Fatalf("executeTask() error = %v", err)
	}
	if len(results) != 1 || !results[0].Failed || results[0].Message != "boom" {
		t.Fatalf("unexpected fail result: %#v", results)
	}
}

func TestExecuteTaskAssertModule(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())

	task := &Task{
		Name:       "assert task",
		ModuleName: "assert",
		Params: map[string]interface{}{
			"that":        []interface{}{"app_enabled", "port == 8080"},
			"success_msg": "assert ok",
		},
	}

	results, err := executor.executeTask(&inventory.Host{Name: "web1"}, task, map[string]interface{}{
		"app_enabled": true,
		"port":        8080,
	}, nil)
	if err != nil {
		t.Fatalf("executeTask() error = %v", err)
	}
	if len(results) != 1 || results[0].Failed || results[0].Stdout != "assert ok" {
		t.Fatalf("unexpected assert result: %#v", results)
	}
}

func TestExecuteTaskAssertModuleFailsOnCondition(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())

	task := &Task{
		Name:       "assert fail",
		ModuleName: "assert",
		Params: map[string]interface{}{
			"that":     []interface{}{"app_enabled", "port == 8080"},
			"fail_msg": "assert failed",
		},
	}

	results, err := executor.executeTask(&inventory.Host{Name: "web1"}, task, map[string]interface{}{
		"app_enabled": true,
		"port":        80,
	}, nil)
	if err != nil {
		t.Fatalf("executeTask() error = %v", err)
	}
	if len(results) != 1 || !results[0].Failed || results[0].Message != "assert failed" {
		t.Fatalf("unexpected assert failure result: %#v", results)
	}
}

func TestExecuteTaskMetaClearFacts(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())
	executor.facts["web1"] = map[string]interface{}{
		"app_name": "elite.km",
	}

	task := &Task{
		Name:       "clear facts",
		ModuleName: "meta",
		Params: map[string]interface{}{
			"_raw_params": "clear_facts",
		},
	}

	results, err := executor.executeTask(&inventory.Host{Name: "web1"}, task, map[string]interface{}{}, nil)
	if err != nil {
		t.Fatalf("executeTask() error = %v", err)
	}
	if len(results) != 1 || results[0].Failed {
		t.Fatalf("unexpected meta result: %#v", results)
	}
	if _, ok := executor.facts["web1"]; ok {
		t.Fatalf("expected facts to be cleared, got %#v", executor.facts["web1"])
	}
}

func TestExecuteTaskMetaResetConnection(t *testing.T) {
	inv := inventory.NewInventory()
	host := &inventory.Host{Name: "web1"}
	inv.AllHosts[host.Name] = host

	executor := NewExecutor(inv)
	task := &Task{
		Name:       "reset connection",
		ModuleName: "meta",
		Params: map[string]interface{}{
			"_raw_params": "reset_connection",
		},
	}

	pool := ssh.NewConnectionPool(nil)
	pool.Store(host.Name, &ssh.Client{})
	executor.pool = pool

	results, err := executor.executeTask(host, task, map[string]interface{}{}, nil)
	if err != nil {
		t.Fatalf("executeTask() error = %v", err)
	}
	if len(results) != 1 || results[0].Failed {
		t.Fatalf("unexpected meta result: %#v", results)
	}
	if results[0].Message != "connection reset" {
		t.Fatalf("unexpected reset message: %#v", results[0])
	}
	if pool.Has(host.Name) {
		t.Fatalf("expected cached connection for %s to be removed", host.Name)
	}
}

func TestExecuteTaskMetaRefreshInventory(t *testing.T) {
	inv := inventory.NewInventory()
	originalHost := &inventory.Host{Name: "web1", Address: "10.0.0.1", Variables: map[string]string{}}
	inv.AllHosts[originalHost.Name] = originalHost

	executor := NewExecutor(inv)
	executor.SetInventoryPath("/tmp/inventory.ini")
	executor.SetExtraVars(map[string]interface{}{
		"ansible_user": "deploy",
	})
	executor.SetInventoryLoader(func(path string) (*inventory.Inventory, error) {
		if path != "/tmp/inventory.ini" {
			t.Fatalf("unexpected inventory path: %s", path)
		}
		reloaded := inventory.NewInventory()
		reloaded.AllHosts["web2"] = &inventory.Host{
			Name:      "web2",
			Address:   "10.0.0.2",
			Variables: map[string]string{},
		}
		return reloaded, nil
	})

	task := &Task{
		Name:       "refresh inventory",
		ModuleName: "meta",
		Params: map[string]interface{}{
			"_raw_params": "refresh_inventory",
		},
	}

	results, err := executor.executeTask(originalHost, task, map[string]interface{}{}, nil)
	if err != nil {
		t.Fatalf("executeTask() error = %v", err)
	}
	if len(results) != 1 || results[0].Failed {
		t.Fatalf("unexpected meta result: %#v", results)
	}
	if results[0].Message != "inventory refreshed" {
		t.Fatalf("unexpected refresh message: %#v", results[0])
	}
	if executor.inventory == inv {
		t.Fatal("expected executor inventory to be replaced")
	}
	reloadedHost := executor.inventory.AllHosts["web2"]
	if reloadedHost == nil {
		t.Fatalf("expected reloaded host web2, got %#v", executor.inventory.AllHosts)
	}
	if reloadedHost.User != "deploy" {
		t.Fatalf("expected extra vars to be re-applied after refresh, got user=%q", reloadedHost.User)
	}
}

func TestExecuteTaskAsyncLocalShellWithPoll(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())
	executor.SetPollInterval(1)

	task := &Task{
		Name:       "async local shell",
		ModuleName: "shell",
		Async:      2,
		Poll:       intPtr(1),
		Params: map[string]interface{}{
			"_raw_params": "printf ready",
		},
		LocalAction: "shell printf ready",
	}

	results, err := executor.executeTask(&inventory.Host{Name: "web1"}, task, map[string]interface{}{}, nil)
	if err != nil {
		t.Fatalf("executeTask() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Stdout != "ready" {
		t.Fatalf("expected async command output, got %#v", results[0])
	}
}

func TestExecutePlayMetaFlushHandlersRunsImmediately(t *testing.T) {
	inv := inventory.NewInventory()
	host := &inventory.Host{Name: "web1", Address: "127.0.0.1", Port: 22}
	inv.AllHosts[host.Name] = host

	executor := NewExecutor(inv)

	play := &Play{
		Hosts: "web1",
		Tasks: []*Task{
			{
				Name:       "queue handler",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "queue",
				},
				ChangedWhen: true,
				Notify:      "restart app",
			},
			{
				Name:       "flush now",
				ModuleName: "meta",
				Params: map[string]interface{}{
					"_raw_params": "flush_handlers",
				},
			},
			{
				Name:       "after flush",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "after",
				},
			},
		},
		Handlers: []*Handler{
			{
				Name:       "restart app",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "restarted",
				},
			},
		},
	}

	result, err := executor.ExecutePlay(play)
	if err != nil {
		t.Fatalf("ExecutePlay() error = %v", err)
	}

	hostResult := result.Hosts["web1"]
	if hostResult == nil || len(hostResult.Tasks) != 4 {
		t.Fatalf("expected 4 task results, got %#v", hostResult)
	}
	if hostResult.Tasks[1].Name != "restart app" || hostResult.Tasks[1].Stdout != "restarted" {
		t.Fatalf("expected handler to run during flush, got %#v", hostResult.Tasks[1])
	}
	if hostResult.Tasks[2].Name != "flush now" || hostResult.Tasks[2].Message != "handlers flushed" {
		t.Fatalf("expected meta flush result after handler, got %#v", hostResult.Tasks[2])
	}
	if hostResult.Tasks[3].Name != "after flush" || hostResult.Tasks[3].Stdout != "after" {
		t.Fatalf("expected task after flush to continue, got %#v", hostResult.Tasks[3])
	}
}

func TestExecutePlayMetaEndHostStopsOnlyCurrentHost(t *testing.T) {
	inv := inventory.NewInventory()
	host1 := &inventory.Host{Name: "web1", Address: "127.0.0.1", Port: 22}
	host2 := &inventory.Host{Name: "web2", Address: "127.0.0.1", Port: 22}
	inv.AllHosts[host1.Name] = host1
	inv.AllHosts[host2.Name] = host2

	executor := NewExecutor(inv)

	play := &Play{
		Hosts: "all",
		Tasks: []*Task{
			{
				Name:       "stop first host",
				ModuleName: "meta",
				Params: map[string]interface{}{
					"_raw_params": "end_host",
				},
				When: `inventory_hostname == "web1"`,
			},
			{
				Name:       "after end host",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "after",
				},
			},
		},
	}

	result, err := executor.ExecutePlay(play)
	if err != nil {
		t.Fatalf("ExecutePlay() error = %v", err)
	}

	web1 := result.Hosts["web1"]
	web2 := result.Hosts["web2"]
	if web1 == nil || web2 == nil {
		t.Fatalf("expected both host results, got %#v", result.Hosts)
	}
	if len(web1.Tasks) != 1 || web1.Tasks[0].Name != "stop first host" {
		t.Fatalf("expected web1 to stop after meta end_host, got %#v", web1.Tasks)
	}
	if len(web2.Tasks) != 2 || web2.Tasks[1].Name != "after end host" || web2.Tasks[1].Stdout != "after" {
		t.Fatalf("expected web2 to continue, got %#v", web2.Tasks)
	}
}

func TestExecutePlayMetaEndPlayStopsRemainingExecution(t *testing.T) {
	inv := inventory.NewInventory()
	host1 := &inventory.Host{Name: "web1", Address: "127.0.0.1", Port: 22}
	host2 := &inventory.Host{Name: "web2", Address: "127.0.0.1", Port: 22}
	inv.AllHosts[host1.Name] = host1
	inv.AllHosts[host2.Name] = host2

	executor := NewExecutor(inv)

	play := &Play{
		Hosts: "all",
		Tasks: []*Task{
			{
				Name:       "end play",
				ModuleName: "meta",
				Params: map[string]interface{}{
					"_raw_params": "end_play",
				},
				When: `inventory_hostname == "web1"`,
			},
			{
				Name:       "should not run",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "after",
				},
			},
		},
	}

	result, err := executor.ExecutePlay(play)
	if err != nil {
		t.Fatalf("ExecutePlay() error = %v", err)
	}

	web1 := result.Hosts["web1"]
	if web1 == nil || len(web1.Tasks) != 1 || web1.Tasks[0].Name != "end play" {
		t.Fatalf("expected web1 to execute end_play only, got %#v", web1)
	}
	if web2 := result.Hosts["web2"]; web2 != nil && len(web2.Tasks) > 0 {
		t.Fatalf("expected web2 to have no executed tasks after end_play, got %#v", web2.Tasks)
	}
}

func TestExecuteTaskAsyncLocalShellNoPollStartsDetached(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())

	task := &Task{
		Name:       "async detached",
		ModuleName: "shell",
		Async:      30,
		Poll:       intPtr(0),
		Params: map[string]interface{}{
			"_raw_params": "sleep 1",
		},
		LocalAction: "shell sleep 1",
	}

	results, err := executor.executeTask(&inventory.Host{Name: "web1"}, task, map[string]interface{}{}, nil)
	if err != nil {
		t.Fatalf("executeTask() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Changed {
		t.Fatalf("expected detached async task to be marked changed, got %#v", results[0])
	}
	if results[0].Stdout == "" {
		t.Fatalf("expected pid output for detached async task, got %#v", results[0])
	}
}

func TestExecuteTaskAsyncRejectsUnsupportedModule(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())

	task := &Task{
		Name:       "async copy",
		ModuleName: "copy",
		Async:      10,
		Poll:       intPtr(1),
		Params: map[string]interface{}{
			"src":  "a",
			"dest": "b",
		},
	}

	results, err := executor.executeTask(&inventory.Host{Name: "web1"}, task, map[string]interface{}{}, nil)
	if err != nil {
		t.Fatalf("executeTask() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Failed || !strings.Contains(results[0].Message, "async is only supported") {
		t.Fatalf("expected unsupported async module failure, got %#v", results[0])
	}
}

func TestEvaluateLoopSupportsWithDictStyleMaps(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())

	items := executor.evaluateLoop(map[string]interface{}{
		"admin": 9090,
		"app":   8080,
	}, map[string]interface{}{})

	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %#v", items)
	}

	first, ok := items[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected first item map, got %#v", items[0])
	}
	second, ok := items[1].(map[string]interface{})
	if !ok {
		t.Fatalf("expected second item map, got %#v", items[1])
	}

	if first["key"] != "admin" || first["value"] != 9090 {
		t.Fatalf("unexpected first dict item: %#v", first)
	}
	if second["key"] != "app" || second["value"] != 8080 {
		t.Fatalf("unexpected second dict item: %#v", second)
	}
}

func TestExecuteTaskWithDictLoopDebug(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())

	task := &Task{
		Name:       "dict loop",
		ModuleName: "debug",
		Loop:       "{{ ports }}",
		Params: map[string]interface{}{
			"msg": "{{ item.key }}={{ item.value }}",
		},
	}

	results, err := executor.executeTask(&inventory.Host{Name: "web1"}, task, map[string]interface{}{
		"ports": map[string]interface{}{
			"app":   8080,
			"admin": 9090,
		},
	}, nil)
	if err != nil {
		t.Fatalf("executeTask() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %#v", results)
	}
	if results[0].Stdout != "admin=9090" || results[1].Stdout != "app=8080" {
		t.Fatalf("unexpected dict loop output: %#v", results)
	}
}

func TestExecuteTaskLoopControl(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())

	task := &Task{
		Name:       "loop control",
		ModuleName: "debug",
		Loop:       []interface{}{"api", "worker"},
		LoopControl: LoopControl{
			LoopVar:  "service",
			IndexVar: "service_index",
			Label:    "{{ service }}",
		},
		Params: map[string]interface{}{
			"msg": "{{ service_index }}:{{ service }}",
		},
	}

	results, err := executor.executeTask(&inventory.Host{Name: "web1"}, task, map[string]interface{}{}, nil)
	if err != nil {
		t.Fatalf("executeTask() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %#v", results)
	}
	if results[0].Stdout != "0:api" || results[1].Stdout != "1:worker" {
		t.Fatalf("unexpected loop_control output: %#v", results)
	}
	if results[0].Item != "api" || results[1].Item != "worker" {
		t.Fatalf("expected loop_control label to drive item display, got %#v", results)
	}
}

func TestEvaluateLoopSupportsWithTogether(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())

	items := executor.evaluateLoop(map[string]interface{}{
		"__with_together__": []interface{}{
			"{{ users }}",
			"{{ groups }}",
		},
	}, map[string]interface{}{
		"users":  []interface{}{"alice", "bob"},
		"groups": []interface{}{"admin"},
	})

	if len(items) != 2 {
		t.Fatalf("expected 2 together items, got %#v", items)
	}
	first, _ := items[0].([]interface{})
	second, _ := items[1].([]interface{})
	if len(first) != 2 || first[0] != "alice" || first[1] != "admin" {
		t.Fatalf("unexpected first together item: %#v", first)
	}
	if len(second) != 2 || second[0] != "bob" || second[1] != nil {
		t.Fatalf("unexpected second together item: %#v", second)
	}
}

func TestExecuteTaskWithNestedLoopDebug(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())

	task := &Task{
		Name:       "nested loop",
		ModuleName: "debug",
		Loop: map[string]interface{}{
			"__with_nested__": []interface{}{
				[]interface{}{"a", "b"},
				[]interface{}{1, 2},
			},
		},
		Params: map[string]interface{}{
			"msg": "{{ item.0 }}={{ item.1 }}",
		},
	}

	results, err := executor.executeTask(&inventory.Host{Name: "web1"}, task, map[string]interface{}{}, nil)
	if err != nil {
		t.Fatalf("executeTask() error = %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("expected 4 nested results, got %#v", results)
	}
	want := []string{"a=1", "a=2", "b=1", "b=2"}
	for i, result := range results {
		if result.Stdout != want[i] {
			t.Fatalf("unexpected nested result %d: %#v", i, result)
		}
	}
}

func TestEvaluateLoopSupportsWithFileglob(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}

	executor := NewExecutor(inventory.NewInventory())
	executor.baseDir = dir

	items := executor.evaluateLoop(map[string]interface{}{
		"__with_fileglob__": "*.txt",
	}, map[string]interface{}{})

	if len(items) != 2 {
		t.Fatalf("expected 2 fileglob items, got %#v", items)
	}
	if items[0] != filepath.Join(dir, "a.txt") || items[1] != filepath.Join(dir, "b.txt") {
		t.Fatalf("unexpected fileglob items: %#v", items)
	}
}

func TestExecuteTaskWithFileglobLoopDebug(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"app.conf", "db.conf"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}

	executor := NewExecutor(inventory.NewInventory())
	executor.baseDir = dir

	task := &Task{
		Name:       "fileglob loop",
		ModuleName: "debug",
		Loop: map[string]interface{}{
			"__with_fileglob__": "*.conf",
		},
		Params: map[string]interface{}{
			"msg": "{{ item }}",
		},
	}

	results, err := executor.executeTask(&inventory.Host{Name: "web1"}, task, map[string]interface{}{}, nil)
	if err != nil {
		t.Fatalf("executeTask() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %#v", results)
	}
	if results[0].Stdout != filepath.Join(dir, "app.conf") || results[1].Stdout != filepath.Join(dir, "db.conf") {
		t.Fatalf("unexpected fileglob loop output: %#v", results)
	}
}

func TestEvaluateLoopSupportsWithSequence(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())

	items := executor.evaluateLoop(map[string]interface{}{
		"__with_sequence__": "start=1 end=3 format=user%02d",
	}, map[string]interface{}{})

	if len(items) != 3 {
		t.Fatalf("expected 3 sequence items, got %#v", items)
	}
	if items[0] != "user01" || items[1] != "user02" || items[2] != "user03" {
		t.Fatalf("unexpected sequence items: %#v", items)
	}
}

func TestExecuteTaskWithSequenceLoopDebug(t *testing.T) {
	executor := NewExecutor(inventory.NewInventory())

	task := &Task{
		Name:       "sequence loop",
		ModuleName: "debug",
		Loop: map[string]interface{}{
			"__with_sequence__": "start=2 end=6 stride=2 format=%02d",
		},
		Params: map[string]interface{}{
			"msg": "{{ item }}",
		},
	}

	results, err := executor.executeTask(&inventory.Host{Name: "web1"}, task, map[string]interface{}{}, nil)
	if err != nil {
		t.Fatalf("executeTask() error = %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %#v", results)
	}
	if results[0].Stdout != "02" || results[1].Stdout != "04" || results[2].Stdout != "06" {
		t.Fatalf("unexpected sequence loop output: %#v", results)
	}
}

func TestExecutePlayIncludeTasksInheritsTagsAndVars(t *testing.T) {
	dir := t.TempDir()
	includePath := filepath.Join(dir, "included.yml")
	if err := os.WriteFile(includePath, []byte(`
- name: set included fact
  set_fact:
    included_name: "{{ ProjectName }}"
- name: show included fact
  debug:
    var: included_name
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	inv := inventory.NewInventory()
	host := &inventory.Host{Name: "web1", Address: "127.0.0.1", Port: 22}
	inv.AllHosts[host.Name] = host

	executor := NewExecutor(inv)
	executor.baseDir = dir
	executor.SetTags([]string{"restart"})

	play := &Play{
		Hosts: "web1",
		Tasks: []*Task{
			{
				Name:       "include deploy steps",
				ModuleName: "include_tasks",
				Params:     map[string]interface{}{"_raw_params": "included.yml"},
				Tags:       []string{"restart"},
				Vars: map[string]interface{}{
					"ProjectName": "elite.km",
				},
			},
		},
	}

	result, err := executor.ExecutePlay(play)
	if err != nil {
		t.Fatalf("ExecutePlay() error = %v", err)
	}

	hostResult := result.Hosts["web1"]
	if hostResult == nil || len(hostResult.Tasks) != 2 {
		t.Fatalf("expected 2 included task results, got %#v", hostResult)
	}
	if hostResult.Tasks[1].Stdout != "included_name: elite.km" {
		t.Fatalf("unexpected included debug output: %#v", hostResult.Tasks[1])
	}
}

func TestExecutePlayImportTasksHonorsParentWhen(t *testing.T) {
	dir := t.TempDir()
	includePath := filepath.Join(dir, "included.yml")
	if err := os.WriteFile(includePath, []byte(`
- name: should not run
  debug:
    msg: "ran"
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	inv := inventory.NewInventory()
	host := &inventory.Host{Name: "web1", Address: "127.0.0.1", Port: 22}
	inv.AllHosts[host.Name] = host

	executor := NewExecutor(inv)
	executor.baseDir = dir

	play := &Play{
		Hosts: "web1",
		Tasks: []*Task{
			{
				Name:       "import deploy steps",
				ModuleName: "import_tasks",
				Params:     map[string]interface{}{"_raw_params": "included.yml"},
				When:       false,
			},
		},
	}

	result, err := executor.ExecutePlay(play)
	if err != nil {
		t.Fatalf("ExecutePlay() error = %v", err)
	}

	hostResult := result.Hosts["web1"]
	if hostResult == nil || len(hostResult.Tasks) != 1 {
		t.Fatalf("expected 1 skipped import task result, got %#v", hostResult)
	}
	if !hostResult.Tasks[0].Skipped {
		t.Fatalf("expected import task to be skipped, got %#v", hostResult.Tasks[0])
	}
}

func TestExecutePlayBlockRunsRescueAndAlways(t *testing.T) {
	inv := inventory.NewInventory()
	host := &inventory.Host{Name: "web1", Address: "127.0.0.1", Port: 22}
	inv.AllHosts[host.Name] = host

	executor := NewExecutor(inv)

	play := &Play{
		Hosts: "web1",
		Tasks: []*Task{
			{
				Name: "guarded block",
				Block: []*Task{
					{
						Name:       "missing module",
						ModuleName: "missing",
						Params:     map[string]interface{}{},
					},
				},
				Rescue: []*Task{
					{
						Name:       "recover",
						ModuleName: "set_fact",
						Params: map[string]interface{}{
							"recovered": true,
						},
					},
				},
				Always: []*Task{
					{
						Name:       "cleanup",
						ModuleName: "debug",
						Params: map[string]interface{}{
							"msg": "cleanup",
						},
					},
				},
			},
		},
	}

	result, err := executor.ExecutePlay(play)
	if err != nil {
		t.Fatalf("ExecutePlay() error = %v", err)
	}

	hostResult := result.Hosts["web1"]
	if hostResult == nil || len(hostResult.Tasks) != 3 {
		t.Fatalf("expected 3 task results, got %#v", hostResult)
	}
	if !hostResult.Tasks[0].Failed {
		t.Fatalf("expected block task failure, got %#v", hostResult.Tasks[0])
	}
	if executor.facts["web1"]["recovered"] != true {
		t.Fatalf("expected rescue to set recovered fact, got %#v", executor.facts["web1"])
	}
	if hostResult.Tasks[2].Stdout != "cleanup" {
		t.Fatalf("expected always task to run, got %#v", hostResult.Tasks[2])
	}
}

func TestExecutePlayBlockInheritsParentVarsAndTags(t *testing.T) {
	inv := inventory.NewInventory()
	host := &inventory.Host{Name: "web1", Address: "127.0.0.1", Port: 22}
	inv.AllHosts[host.Name] = host

	executor := NewExecutor(inv)
	executor.SetTags([]string{"deploy"})

	play := &Play{
		Hosts: "web1",
		Tasks: []*Task{
			{
				Name: "tagged block",
				Tags: []string{"deploy"},
				Vars: map[string]interface{}{
					"ProjectName": "elite.km",
				},
				Block: []*Task{
					{
						Name:       "show name",
						ModuleName: "debug",
						Params: map[string]interface{}{
							"msg": "{{ ProjectName }}",
						},
					},
				},
			},
		},
	}

	result, err := executor.ExecutePlay(play)
	if err != nil {
		t.Fatalf("ExecutePlay() error = %v", err)
	}

	hostResult := result.Hosts["web1"]
	if hostResult == nil || len(hostResult.Tasks) != 1 {
		t.Fatalf("expected 1 task result, got %#v", hostResult)
	}
	if hostResult.Tasks[0].Stdout != "elite.km" {
		t.Fatalf("expected block child to inherit vars and tags, got %#v", hostResult.Tasks[0])
	}
}

func TestExecutePlayAnyErrorsFatalStopsPlay(t *testing.T) {
	inv := inventory.NewInventory()
	host := &inventory.Host{Name: "web1", Address: "127.0.0.1", Port: 22}
	inv.AllHosts[host.Name] = host

	executor := NewExecutor(inv)

	play := &Play{
		Hosts:          "web1",
		AnyErrorsFatal: true,
		Tasks: []*Task{
			{
				Name:       "failing task",
				ModuleName: "missing",
				Params:     map[string]interface{}{},
			},
			{
				Name:       "should not run",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "after failure",
				},
			},
		},
	}

	result, err := executor.ExecutePlay(play)
	if err == nil || !strings.Contains(err.Error(), "any_errors_fatal") {
		t.Fatalf("expected any_errors_fatal error, got %v", err)
	}

	hostResult := result.Hosts["web1"]
	if hostResult == nil || len(hostResult.Tasks) < 1 {
		t.Fatalf("expected task results, got %#v", hostResult)
	}
	if !hostResult.Tasks[0].Failed {
		t.Fatalf("expected first task to fail, got %#v", hostResult.Tasks[0])
	}
	if len(hostResult.Tasks) > 1 && hostResult.Tasks[1].Name == "should not run" && hostResult.Tasks[1].Stdout == "after failure" {
		t.Fatalf("expected any_errors_fatal to interrupt normal success flow, got %#v", hostResult.Tasks)
	}
}

func TestExecutePlayFailedHostSkipsLaterTaskPhases(t *testing.T) {
	inv := inventory.NewInventory()
	host := &inventory.Host{Name: "web1", Address: "127.0.0.1", Port: 22}
	inv.AllHosts[host.Name] = host

	executor := NewExecutor(inv)

	play := &Play{
		Hosts: "web1",
		Tasks: []*Task{
			{
				Name:       "failing task",
				ModuleName: "missing",
				Params:     map[string]interface{}{},
			},
		},
		PostTasks: []*Task{
			{
				Name:       "post should not run",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "post",
				},
			},
		},
	}

	result, err := executor.ExecutePlay(play)
	if err != nil {
		t.Fatalf("ExecutePlay() error = %v", err)
	}

	hostResult := result.Hosts["web1"]
	if hostResult == nil || len(hostResult.Tasks) != 1 {
		t.Fatalf("expected only failing task result, got %#v", hostResult)
	}
	if hostResult.Tasks[0].Name != "failing task" || !hostResult.Tasks[0].Failed {
		t.Fatalf("unexpected first task result: %#v", hostResult.Tasks[0])
	}
}

func TestExecuteTaskMetaClearHostErrors(t *testing.T) {
	inv := inventory.NewInventory()
	host := &inventory.Host{Name: "web1"}
	inv.AllHosts[host.Name] = host

	executor := NewExecutor(inv)
	executor.failedHosts = map[string]bool{"web1": true}

	task := &Task{
		Name:       "clear host errors",
		ModuleName: "meta",
		Params: map[string]interface{}{
			"_raw_params": "clear_host_errors",
		},
	}

	results, err := executor.executeTask(host, task, map[string]interface{}{}, nil)
	if err != nil {
		t.Fatalf("executeTask() error = %v", err)
	}
	if len(results) != 1 || results[0].Failed {
		t.Fatalf("unexpected meta result: %#v", results)
	}
	if executor.isHostFailed(host.Name) {
		t.Fatalf("expected host failure state to be cleared")
	}
}

func TestExecutePlayForceHandlersRunsQueuedHandlersOnFailure(t *testing.T) {
	inv := inventory.NewInventory()
	host := &inventory.Host{Name: "web1", Address: "127.0.0.1", Port: 22}
	inv.AllHosts[host.Name] = host

	executor := NewExecutor(inv)

	play := &Play{
		Hosts:         "web1",
		ForceHandlers: true,
		Tasks: []*Task{
			{
				Name:       "queue handler",
				ModuleName: "set_fact",
				Params: map[string]interface{}{
					"changed_marker": true,
				},
				Notify:      "restart service",
				ChangedWhen: true,
			},
			{
				Name:       "failing task",
				ModuleName: "missing",
				Params:     map[string]interface{}{},
			},
		},
		Handlers: []*Handler{
			{
				Name:       "restart service",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "handler-ran",
				},
			},
		},
	}

	result, err := executor.ExecutePlay(play)
	_ = err

	hostResult := result.Hosts["web1"]
	if hostResult == nil || len(hostResult.Tasks) < 3 {
		t.Fatalf("expected queued handler to run after failure, got %#v", hostResult)
	}
	last := hostResult.Tasks[len(hostResult.Tasks)-1]
	if last.Name != "restart service" || last.Stdout != "handler-ran" {
		t.Fatalf("expected forced handler execution, got %#v", last)
	}
}

func TestExecutePlayLinearStrategyConcurrency(t *testing.T) {
	inv := inventory.NewInventory()
	h1 := &inventory.Host{Name: "web1", Address: "127.0.0.1", Port: 22}
	h2 := &inventory.Host{Name: "web2", Address: "127.0.0.1", Port: 22}
	inv.AllHosts[h1.Name] = h1
	inv.AllHosts[h2.Name] = h2

	executor := NewExecutor(inv)
	executor.SetForks(5)

	play := &Play{
		Hosts:    "all",
		Strategy: "linear",
		Tasks: []*Task{
			{
				Name:       "parallel sleep",
				ModuleName: "shell",
				Params: map[string]interface{}{
					"_raw_params": "sleep 0.2",
				},
				DelegateTo: "localhost",
			},
			{
				Name:       "task 2",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "barrier passed",
				},
			},
		},
	}

	start := time.Now()
	res, err := executor.ExecutePlay(play)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("ExecutePlay() error = %v", err)
	}
	if len(res.Hosts) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(res.Hosts))
	}
	for _, h := range []string{"web1", "web2"} {
		hr := res.Hosts[h]
		if hr == nil || len(hr.Tasks) != 2 {
			t.Fatalf("host %s missing tasks: %#v", h, hr)
		}
		if hr.Tasks[0].Failed || hr.Tasks[1].Failed {
			t.Fatalf("host %s task failed: %#v", h, hr.Tasks)
		}
	}

	// Two 0.2s sleeps in parallel should take ~0.2s-0.35s, definitely less than serial 0.40s.
	if elapsed >= 380*time.Millisecond {
		t.Fatalf("expected parallel execution under 380ms, took %v", elapsed)
	}
}

func TestExecutePlayFreeStrategy(t *testing.T) {
	inv := inventory.NewInventory()
	h1 := &inventory.Host{Name: "web1", Address: "127.0.0.1", Port: 22}
	h2 := &inventory.Host{Name: "web2", Address: "127.0.0.1", Port: 22}
	inv.AllHosts[h1.Name] = h1
	inv.AllHosts[h2.Name] = h2

	executor := NewExecutor(inv)
	executor.SetForks(5)

	play := &Play{
		Hosts:    "all",
		Strategy: "free",
		Tasks: []*Task{
			{
				Name:       "debug 1",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "first",
				},
			},
			{
				Name:       "debug 2",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "second",
				},
			},
		},
	}

	res, err := executor.ExecutePlay(play)
	if err != nil {
		t.Fatalf("ExecutePlay() error = %v", err)
	}
	if len(res.Hosts) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(res.Hosts))
	}
	for _, h := range []string{"web1", "web2"} {
		hr := res.Hosts[h]
		if hr == nil || len(hr.Tasks) != 2 {
			t.Fatalf("host %s missing tasks: %#v", h, hr)
		}
		if hr.Tasks[0].Stdout != "first" || hr.Tasks[1].Stdout != "second" {
			t.Fatalf("host %s task output mismatch: %#v", h, hr.Tasks)
		}
	}
}

func TestExecutePlayHostVarsAndMagicVariables(t *testing.T) {
	inv := inventory.NewInventory()
	hDb := &inventory.Host{
		Name:      "db1",
		Address:   "10.0.0.2",
		Port:      22,
		Variables: map[string]string{"port": "5432"},
	}
	hWeb := &inventory.Host{
		Name:      "web1",
		Address:   "10.0.0.1",
		Port:      22,
		Variables: map[string]string{},
	}
	inv.AllHosts[hDb.Name] = hDb
	inv.AllHosts[hWeb.Name] = hWeb
	inv.Groups["db"] = &inventory.Group{Name: "db", Hosts: []*inventory.Host{hDb}}
	inv.Groups["web"] = &inventory.Group{Name: "web", Hosts: []*inventory.Host{hWeb}}

	executor := NewExecutor(inv)

	// Step 1: Set fact on db1
	play1 := &Play{
		Hosts: "db1",
		Tasks: []*Task{
			{
				Name:       "set db fact",
				ModuleName: "set_fact",
				Params: map[string]interface{}{
					"db_status": "ready",
				},
			},
		},
	}
	if _, err := executor.ExecutePlay(play1); err != nil {
		t.Fatalf("play1 failed: %v", err)
	}

	// Step 2: Read hostvars and magic variables from web1
	play2 := &Play{
		Hosts: "web1",
		Tasks: []*Task{
			{
				Name:       "check hostvars",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "connect to {{ hostvars['db1']['ansible_host'] }}:{{ hostvars['db1']['port'] }} status={{ hostvars['db1']['db_status'] }} via {{ groups['db'][0] }} in groups {{ group_names | join(',') }}",
				},
			},
		},
	}
	res2, err := executor.ExecutePlay(play2)
	if err != nil {
		t.Fatalf("play2 failed: %v", err)
	}

	hr := res2.Hosts["web1"]
	if hr == nil || len(hr.Tasks) != 1 {
		t.Fatalf("unexpected web1 tasks: %#v", hr)
	}
	expected := "connect to 10.0.0.2:5432 status=ready via db1 in groups web"
	if hr.Tasks[0].Stdout != expected {
		t.Fatalf("expected msg %q, got %q", expected, hr.Tasks[0].Stdout)
	}
}

func TestExecutePlayExpandedFilters(t *testing.T) {
	inv := inventory.NewInventory()
	host := &inventory.Host{Name: "web1", Address: "127.0.0.1", Port: 22}
	inv.AllHosts[host.Name] = host

	executor := NewExecutor(inv)

	play := &Play{
		Hosts: "web1",
		Vars: map[string]interface{}{
			"raw_text": "api-v1-prod",
			"numbers":  []interface{}{3, 1, 4, 1, 5, 9, 2, 6},
		},
		Tasks: []*Task{
			{
				Name:       "regex test",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "{{ raw_text | regex_replace('v1', 'v2') }}",
				},
			},
			{
				Name:       "unique list test",
				ModuleName: "debug",
				Params: map[string]interface{}{
					"msg": "{{ numbers | unique | length }}",
				},
			},
		},
	}

	res, err := executor.ExecutePlay(play)
	if err != nil {
		t.Fatalf("ExecutePlay error: %v", err)
	}
	hr := res.Hosts["web1"]
	if hr == nil || len(hr.Tasks) != 2 {
		t.Fatalf("unexpected tasks: %#v", hr)
	}
	if hr.Tasks[0].Stdout != "api-v2-prod" {
		t.Fatalf("expected api-v2-prod, got %q", hr.Tasks[0].Stdout)
	}
	if hr.Tasks[1].Stdout != "7" {
		t.Fatalf("expected 7 unique numbers, got %q", hr.Tasks[1].Stdout)
	}
}

func TestStepModeExecution(t *testing.T) {
	inv := inventory.NewInventory()
	h := &inventory.Host{
		Name:       "local1",
		Connection: "local",
	}
	inv.AllHosts[h.Name] = h
	g := &inventory.Group{Name: "all", Hosts: []*inventory.Host{h}}
	inv.Groups["all"] = g

	executor := NewExecutor(inv)
	executor.SetStepMode(true)

	answers := []string{"n", "y", "c"}
	callCount := 0
	executor.SetStepPromptFunc(func(taskName string) (string, error) {
		if callCount < len(answers) {
			ans := answers[callCount]
			callCount++
			return ans, nil
		}
		return "y", nil
	})

	play := &Play{
		Hosts: "all",
		Tasks: []*Task{
			{
				Name:       "task 1 - skipped by step",
				ModuleName: "debug",
				Params:     map[string]interface{}{"msg": "one"},
			},
			{
				Name:       "task 2 - accepted by step",
				ModuleName: "debug",
				Params:     map[string]interface{}{"msg": "two"},
			},
			{
				Name:       "task 3 - continue",
				ModuleName: "debug",
				Params:     map[string]interface{}{"msg": "three"},
			},
			{
				Name:       "task 4 - should run without prompt",
				ModuleName: "debug",
				Params:     map[string]interface{}{"msg": "four"},
			},
		},
	}

	res, err := executor.ExecutePlay(play)
	if err != nil {
		t.Fatalf("ExecutePlay error: %v", err)
	}
	hr := res.Hosts["local1"]
	if hr == nil || len(hr.Tasks) != 4 {
		t.Fatalf("expected 4 tasks recorded, got %d", len(hr.Tasks))
	}
	if !hr.Tasks[0].Skipped {
		t.Fatalf("task 1 should be skipped due to step answer 'n'")
	}
	if hr.Tasks[1].Skipped {
		t.Fatalf("task 2 should not be skipped due to step answer 'y'")
	}
	if hr.Tasks[2].Skipped || hr.Tasks[3].Skipped {
		t.Fatalf("task 3 and 4 should not be skipped after continue")
	}
	if callCount != 3 {
		t.Fatalf("expected 3 prompts before continue, got %d", callCount)
	}
}

func TestForceHandlersCLI(t *testing.T) {
	inv := inventory.NewInventory()
	h := &inventory.Host{
		Name:       "local1",
		Connection: "local",
	}
	inv.AllHosts[h.Name] = h
	g := &inventory.Group{Name: "all", Hosts: []*inventory.Host{h}}
	inv.Groups["all"] = g

	executor := NewExecutor(inv)
	executor.SetForceHandlers(true)

	handlerExecuted := false
	handler := &Handler{
		Name:       "restart app",
		ModuleName: "command",
		Params: map[string]interface{}{
			"cmd": "echo handler executed",
		},
	}

	play := &Play{
		Hosts: "all",
		Handlers: []*Handler{
			handler,
		},
		Tasks: []*Task{
			{
				Name:       "trigger handler",
				ModuleName: "command",
				Params: map[string]interface{}{
					"cmd": "echo triggering",
				},
				Notify: []string{"restart app"},
			},
			{
				Name:       "fail task",
				ModuleName: "fail",
				Params: map[string]interface{}{
					"msg": "intentional failure",
				},
			},
		},
	}

	res, err := executor.ExecutePlay(play)
	_ = err

	hr := res.Hosts["local1"]
	if hr == nil || len(hr.Tasks) < 3 {
		t.Fatalf("expected at least 3 tasks recorded, got %#v", hr)
	}

	// 验证第二步 fail 模块确实失败了
	if !hr.Tasks[1].Failed {
		t.Fatal("expected fail task to be marked as failed")
	}

	for _, task := range hr.Tasks {
		if task.Name == "restart app" {
			handlerExecuted = true
			break
		}
	}
	if !handlerExecuted {
		t.Fatal("expected handler to execute under force_handlers even though play had failures")
	}
}

func TestExecuteTaskIncludeRole(t *testing.T) {
	tempDir := t.TempDir()
	roleDir := filepath.Join(tempDir, "roles", "common")
	tasksDir := filepath.Join(roleDir, "tasks")
	varsDir := filepath.Join(roleDir, "vars")
	_ = os.MkdirAll(tasksDir, 0755)
	_ = os.MkdirAll(varsDir, 0755)

	tasksYaml := `- name: Say hello from role
  debug:
    msg: "Hello from {{ role_greeting }}"
`
	varsYaml := `role_greeting: "embedded_role_value"`

	_ = os.WriteFile(filepath.Join(tasksDir, "main.yml"), []byte(tasksYaml), 0644)
	_ = os.WriteFile(filepath.Join(varsDir, "main.yml"), []byte(varsYaml), 0644)

	inv := inventory.NewInventory()
	h := &inventory.Host{
		Name:       "node1",
		Connection: "local",
	}
	inv.AllHosts[h.Name] = h
	inv.Groups["all"] = &inventory.Group{Name: "all", Hosts: []*inventory.Host{h}}

	executor := NewExecutor(inv)
	executor.SetRolePaths([]string{filepath.Join(tempDir, "roles")})

	play := &Play{
		Hosts: "all",
		Tasks: []*Task{
			{
				Name:       "include common role dynamically",
				ModuleName: "include_role",
				Params: map[string]interface{}{
					"name": "common",
				},
				When: "true",
			},
			{
				Name:       "skip common role by condition",
				ModuleName: "include_role",
				Params: map[string]interface{}{
					"name": "common",
				},
				When: "false",
			},
		},
	}

	res, err := executor.ExecutePlay(play)
	if err != nil {
		t.Fatalf("ExecutePlay error: %v", err)
	}

	hr := res.Hosts["node1"]
	if hr == nil {
		t.Fatal("missing host node1 in results")
	}

	// 验证第一个 role task 成功执行并拿到 role vars 渲染
	foundTask := false
	for _, task := range hr.Tasks {
		if task.Name == "Say hello from role" {
			foundTask = true
			if task.Stdout != "Hello from embedded_role_value" {
				t.Fatalf("expected greeting message, got %q", task.Stdout)
			}
		}
	}
	if !foundTask {
		t.Fatal("expected 'Say hello from role' to have executed")
	}

	// 验证第二个 include_role 被 when 条件跳过
	foundSkipped := false
	for _, task := range hr.Tasks {
		if task.Name == "skip common role by condition" && task.Skipped {
			foundSkipped = true
			break
		}
	}
	if !foundSkipped {
		t.Fatal("expected second include_role task to be recorded as skipped")
	}
}
