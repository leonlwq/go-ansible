package playbook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/module"
	"go-ansible/pkg/ssh"
)

// mockPhase25Connection 模拟 SSH 连接
type mockPhase25Connection struct {
	execFunc func(cmd string) (*ssh.Result, error)
}

func (m *mockPhase25Connection) Connect() error                                         { return nil }
func (m *mockPhase25Connection) Close() error                                           { return nil }
func (m *mockPhase25Connection) Upload(localPath, remotePath string) error             { return nil }
func (m *mockPhase25Connection) Download(remotePath, localPath string) error           { return nil }
func (m *mockPhase25Connection) ExecuteWithBecome(cmd string) (*ssh.Result, error)     { return m.Execute(cmd) }
func (m *mockPhase25Connection) Execute(cmd string) (*ssh.Result, error) {
	if m.execFunc != nil {
		return m.execFunc(cmd)
	}
	return &ssh.Result{Stdout: "ok", ExitCode: 0}, nil
}

// 1. 测试 Rescue 救援成功后主机不被移出队列，后续常规任务正常执行
func TestPhase25_RescueSuccessClearsFailureForSubsequentTasks(t *testing.T) {
	inv := inventory.NewInventory()
	h := &inventory.Host{
		Name:       "node1",
		Address:    "127.0.0.1",
		Connection: "local",
	}
	inv.AllHosts[h.Name] = h

	exec := NewExecutor(inv)

	pb := &Playbook{
		Plays: []*Play{
			{
				Name:  "Test Rescue Recovery",
				Hosts: "all",
				Tasks: []*Task{
					{
						Name: "Block Task with Failure",
						Block: []*Task{
							{
								Name:       "Failing task",
								ModuleName: "fail",
								Params: map[string]interface{}{
									"msg": "simulated block error",
								},
							},
						},
						Rescue: []*Task{
							{
								Name:       "Rescue task",
								ModuleName: "debug",
								Params: map[string]interface{}{
									"msg": "recovering from error",
								},
							},
						},
					},
					{
						Name:       "Subsequent normal task",
						ModuleName: "debug",
						Params: map[string]interface{}{
							"msg": "post rescue success",
						},
					},
				},
			},
		},
	}

	result, err := exec.Execute(pb)
	if err != nil {
		t.Fatalf("unexpected playbook error: %v", err)
	}

	if len(result.Plays) == 0 {
		t.Fatalf("expected plays in result")
	}
	hostRes := result.Plays[0].Hosts["node1"]
	if hostRes == nil {
		t.Fatalf("expected host result for node1")
	}

	// 确认宿主没有被标记为失败
	if exec.isHostFailed("node1") {
		t.Fatalf("expected node1 not to be marked failed after rescue")
	}

	// 确认后续任务成功执行
	var foundSubsequent bool
	for _, task := range hostRes.Tasks {
		if task.Name == "Subsequent normal task" {
			foundSubsequent = true
			if task.Failed || task.Skipped {
				t.Fatalf("expected subsequent task to succeed, got failed=%v, skipped=%v", task.Failed, task.Skipped)
			}
		}
	}
	if !foundSubsequent {
		t.Fatalf("expected 'Subsequent normal task' to be executed")
	}
}

// 2. 测试 delegate_to: localhost 属性正确指向本地，且支持未纳管动态 IP 委派
func TestPhase25_DelegateToLocalhostAndDynamicHost(t *testing.T) {
	inv := inventory.NewInventory()
	remoteHost := &inventory.Host{
		Name:       "remote-srv",
		Address:    "10.0.0.5",
		Connection: "ssh",
	}
	inv.AllHosts[remoteHost.Name] = remoteHost

	exec := NewExecutor(inv)

	// 测试 localhost 委派
	dHost, isLocal, err := exec.resolveDelegateHost("localhost", nil)
	if err != nil {
		t.Fatalf("resolve localhost error: %v", err)
	}
	if !isLocal || dHost.Name != "localhost" {
		t.Fatalf("expected local host, got isLocal=%v, name=%s", isLocal, dHost.Name)
	}

	// 测试动态临时 IP 委派（未在清单中定义）
	dynamicIP := "192.168.100.200"
	dynHost, dynLocal, err := exec.resolveDelegateHost(dynamicIP, nil)
	if err != nil {
		t.Fatalf("expected dynamic host creation for %s, got err: %v", dynamicIP, err)
	}
	if dynLocal {
		t.Fatalf("dynamic host %s should not be local", dynamicIP)
	}
	if dynHost.Name != dynamicIP || dynHost.Address != dynamicIP || dynHost.Connection != "ssh" {
		t.Fatalf("unexpected dynamic host properties: %+v", dynHost)
	}
}

// 3. 测试 fetch 模块多主机目录隔离（inventory_hostname 自动注入）
func TestPhase25_FetchHostIsolation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "phase25_fetch_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	mockClient := &mockPhase25Connection{
		execFunc: func(cmd string) (*ssh.Result, error) {
			if strings.HasPrefix(cmd, "test -f") {
				return &ssh.Result{ExitCode: 0}, nil
			}
			if strings.Contains(cmd, "sha256sum") {
				return &ssh.Result{Stdout: "mockhash123", ExitCode: 0}, nil
			}
			return &ssh.Result{ExitCode: 0}, nil
		},
	}

	fetchMod := &module.FetchModule{}

	// 模拟 host1
	params1 := map[string]interface{}{
		"src":                "/var/log/syslog",
		"dest":               tmpDir,
		"inventory_hostname": "web-01",
	}
	res1, err := fetchMod.Execute(mockClient, params1)
	if err != nil {
		t.Fatalf("fetch1 error: %v", err)
	}
	if !strings.Contains(res1.Data["dest"].(string), filepath.Join(tmpDir, "web-01")) {
		t.Fatalf("expected dest for web-01 under web-01 dir, got %v", res1.Data["dest"])
	}

	// 模拟 host2
	params2 := map[string]interface{}{
		"src":                "/var/log/syslog",
		"dest":               tmpDir,
		"inventory_hostname": "web-02",
	}
	res2, err := fetchMod.Execute(mockClient, params2)
	if err != nil {
		t.Fatalf("fetch2 error: %v", err)
	}
	if !strings.Contains(res2.Data["dest"].(string), filepath.Join(tmpDir, "web-02")) {
		t.Fatalf("expected dest for web-02 under web-02 dir, got %v", res2.Data["dest"])
	}

	if res1.Data["dest"] == res2.Data["dest"] {
		t.Fatalf("fetch destinations collided between web-01 and web-02!")
	}
}

// 4. 测试 lineinfile Python 风格反向引用 \1 展开与 Last Match 插入
func TestPhase25_LineinfileBackrefsAndLastMatch(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "phase25_lineinfile_*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	initialContent := "Port 22\nListenAddress 0.0.0.0\nListenAddress 127.0.0.1\nMaxAuthTries 5\n"
	_ = os.WriteFile(tmpFile.Name(), []byte(initialContent), 0644)

	localConn := &ssh.LocalConnection{}
	mod := &module.LineinfileModule{}

	// 4.1 测试 Python 反向引用 \1 替换
	backrefParams := map[string]interface{}{
		"path":     tmpFile.Name(),
		"regexp":   `^ListenAddress\s+([0-9\.]+)`,
		"line":     `# Binding to \1`,
		"backrefs": true,
	}
	res, err := mod.Execute(localConn, backrefParams)
	if err != nil {
		t.Fatalf("lineinfile backrefs error: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected backrefs line to change")
	}

	content, _ := os.ReadFile(tmpFile.Name())
	if !strings.Contains(string(content), "# Binding to 127.0.0.1") {
		t.Fatalf("expected Python backref \\1 to expand to IP, got:\n%s", string(content))
	}

	// 4.2 测试默认 Last Match（插入在最后一个匹配行之后）
	_ = os.WriteFile(tmpFile.Name(), []byte("ITEM 1\nITEM 2\nOTHER\n"), 0644)
	insertParams := map[string]interface{}{
		"path":        tmpFile.Name(),
		"insertafter": "^ITEM",
		"line":        "INSERTED_DEFAULT_LAST",
	}
	_, err = mod.Execute(localConn, insertParams)
	if err != nil {
		t.Fatalf("insertafter error: %v", err)
	}
	c2, _ := os.ReadFile(tmpFile.Name())
	lines := strings.Split(strings.TrimSpace(string(c2)), "\n")
	// 预期结构: ITEM 1, ITEM 2, INSERTED_DEFAULT_LAST, OTHER
	if len(lines) < 4 || lines[2] != "INSERTED_DEFAULT_LAST" {
		t.Fatalf("expected INSERTED_DEFAULT_LAST after ITEM 2 (last match), got lines: %v", lines)
	}

	// 4.3 测试 firstmatch: true（插入在第一个匹配项之后）
	_ = os.WriteFile(tmpFile.Name(), []byte("ITEM 1\nITEM 2\nOTHER\n"), 0644)
	firstMatchParams := map[string]interface{}{
		"path":        tmpFile.Name(),
		"insertafter": "^ITEM",
		"firstmatch":  true,
		"line":        "INSERTED_FIRST",
	}
	_, err = mod.Execute(localConn, firstMatchParams)
	if err != nil {
		t.Fatalf("insertafter firstmatch error: %v", err)
	}
	c3, _ := os.ReadFile(tmpFile.Name())
	lines3 := strings.Split(strings.TrimSpace(string(c3)), "\n")
	// 预期结构: ITEM 1, INSERTED_FIRST, ITEM 2, OTHER
	if len(lines3) < 4 || lines3[1] != "INSERTED_FIRST" {
		t.Fatalf("expected INSERTED_FIRST after ITEM 1 (first match), got lines: %v", lines3)
	}

	// 4.4 校验互斥约束
	invalidParams := map[string]interface{}{
		"path":         tmpFile.Name(),
		"insertafter":  "foo",
		"insertbefore": "bar",
	}
	if err := mod.Validate(invalidParams); err == nil {
		t.Fatalf("expected error when both insertafter and insertbefore are specified")
	}

	invalidBackref := map[string]interface{}{
		"path":     tmpFile.Name(),
		"backrefs": true,
		"line":     "test",
	}
	if err := mod.Validate(invalidBackref); err == nil {
		t.Fatalf("expected error when backrefs is true without regexp")
	}
}

// 5. 测试 Special Tags (tagged, untagged, always, never, all)
func TestPhase25_SpecialTags(t *testing.T) {
	exec := NewExecutor(nil)

	// 5.1 untagged 测试
	exec.SetTags([]string{"untagged"})
	exec.SetSkipTags(nil)

	if !exec.shouldRunTaskByTags([]string{}) {
		t.Fatalf("expected --tags untagged to run untagged task")
	}
	if exec.shouldRunTaskByTags([]string{"web"}) {
		t.Fatalf("expected --tags untagged to skip tagged task")
	}

	// 5.2 tagged 测试
	exec.SetTags([]string{"tagged"})
	if !exec.shouldRunTaskByTags([]string{"db"}) {
		t.Fatalf("expected --tags tagged to run tagged task")
	}
	if exec.shouldRunTaskByTags([]string{}) {
		t.Fatalf("expected --tags tagged to skip untagged task")
	}
	if exec.shouldRunTaskByTags([]string{"never"}) {
		t.Fatalf("expected --tags tagged to skip never task")
	}

	// 5.3 --skip-tags all 保护 always
	exec.SetTags(nil)
	exec.SetSkipTags([]string{"all"})
	if !exec.shouldRunTaskByTags([]string{"always"}) {
		t.Fatalf("expected --skip-tags all NOT to skip always task")
	}
	if exec.shouldRunTaskByTags([]string{"common"}) {
		t.Fatalf("expected --skip-tags all to skip common task")
	}

	// 5.4 显式 --skip-tags always 跳过 always
	exec.SetSkipTags([]string{"always"})
	if exec.shouldRunTaskByTags([]string{"always"}) {
		t.Fatalf("expected --skip-tags always to skip always task")
	}
}

// 6. 测试 to_nice_json 过滤器与 Lookup 错误返回
func TestPhase25_ToNiceJsonAndLookupErrors(t *testing.T) {
	// 6.1 to_nice_json
	data := map[string]interface{}{
		"name": "ansible",
		"version": 2,
	}
	filterRes, ok, err := module.ApplyFilter("to_nice_json", data)
	if err != nil || !ok {
		t.Fatalf("to_nice_json failed: %v", err)
	}
	jsonStr, _ := filterRes.(string)
	if !strings.Contains(jsonStr, "\n  \"name\": \"ansible\"") {
		t.Fatalf("expected indented json from to_nice_json, got:\n%s", jsonStr)
	}

	// 6.2 lookup('file') 错误抛出
	_, err = module.ExecuteLookup("", "file", "/nonexistent/path/for/test.txt")
	if err == nil {
		t.Fatalf("expected error from lookup('file') on nonexistent path, got nil")
	}

	// 6.3 lookup('pipe') 错误抛出
	_, err = module.ExecuteLookup("", "pipe", "exit 42")
	if err == nil {
		t.Fatalf("expected error from lookup('pipe') on command failure, got nil")
	}
}

// 7. 测试 Cron 模块新校验
func TestPhase25_CronValidation(t *testing.T) {
	cronMod := &module.CronModule{}

	// 缺少 job 报错
	err := cronMod.Validate(map[string]interface{}{
		"special_time": "reboot",
	})
	if err == nil {
		t.Fatalf("expected error when job is missing in cron")
	}

	// 非法 special_time 报错
	err = cronMod.Validate(map[string]interface{}{
		"job":          "/bin/echo hi",
		"special_time": "every_minute",
	})
	if err == nil {
		t.Fatalf("expected error on invalid special_time 'every_minute'")
	}

	// 正确的 special_time 校验通过
	err = cronMod.Validate(map[string]interface{}{
		"job":          "/bin/echo hi",
		"special_time": "reboot",
	})
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}
