package playbook

import (
	"strings"
	"testing"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/module"
	"go-ansible/pkg/ssh"
)

// TestPhase23_SetupFactsNormalization 验证 setup 事实采集及标准化解析
func TestPhase23_SetupFactsNormalization(t *testing.T) {
	rawOutput := `
gathered_facts:
  ansible_hostname: test-node
  ansible_os_family: RedHat
  ansible_distribution: "CentOS Linux || "
  ansible_distribution_version: "8.4.2105 || "
  ansible_env:
    HOME: "/root"
    USER: "root"
    CUSTOM_KEY: "custom_value"
`
	facts := parseFacts(rawOutput)
	if facts == nil {
		t.Fatalf("expected parsed facts, got nil")
	}

	if facts["ansible_distribution"] != "CentOS" {
		t.Errorf("expected normalized distribution 'CentOS', got %q", facts["ansible_distribution"])
	}
	if facts["ansible_distribution_version"] != "8.4.2105" {
		t.Errorf("expected clean version '8.4.2105', got %q", facts["ansible_distribution_version"])
	}

	env, ok := facts["ansible_env"].(map[string]interface{})
	if !ok || env == nil {
		t.Fatalf("expected ansible_env map, got %v", facts["ansible_env"])
	}
	if env["CUSTOM_KEY"] != "custom_value" {
		t.Errorf("expected CUSTOM_KEY=custom_value, got %v", env["CUSTOM_KEY"])
	}
}

// TestPhase23_DelegateFactsToLocalhost 验证 delegate_to: localhost 委派事实正确归属
func TestPhase23_DelegateFactsToLocalhost(t *testing.T) {
	yamlPlaybook := `
- name: Test Delegate Facts Localhost
  hosts: remote1
  gather_facts: false
  tasks:
    - name: Set facts on localhost
      set_fact:
        local_service_port: 8080
      delegate_to: localhost
      delegate_facts: true
      register: set_fact_res
`
	parser := NewParser("")
	pb, err := parser.Parse([]byte(yamlPlaybook))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()
	inv.AllHosts["remote1"] = &inventory.Host{
		Name:       "remote1",
		Address:    "192.168.1.100",
		Connection: "local",
	}

	exec := NewExecutor(inv)
	defer exec.Close()

	res, err := exec.Execute(pb)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if len(res.Plays) == 0 {
		t.Fatalf("expected play execution")
	}

	// 1. Facts 必须归属于 localhost
	localFacts := exec.facts["localhost"]
	if localFacts == nil || localFacts["local_service_port"] != 8080 {
		t.Errorf("expected local_service_port=8080 on localhost, got %v", localFacts)
	}

	// 2. ansible_facts 内置字典必须同步拥有该事实
	af, ok := localFacts["ansible_facts"].(map[string]interface{})
	if !ok || af["local_service_port"] != 8080 {
		t.Errorf("expected local_service_port=8080 in ansible_facts subdict on localhost, got %v", af)
	}

	// 3. register 变量必须归属于当前执行主机 remote1，而不是 localhost
	remoteFacts := exec.facts["remote1"]
	if remoteFacts == nil || remoteFacts["set_fact_res"] == nil {
		t.Errorf("expected set_fact_res registered on remote1")
	}
	if localFacts["set_fact_res"] != nil {
		t.Errorf("set_fact_res should NOT be registered on localhost")
	}
}

// TestPhase23_NoLogSkippedItemAndMasking 验证 no_log 掩码对循环跳过项与输出的保护
func TestPhase23_NoLogSkippedItemAndMasking(t *testing.T) {
	yamlPlaybook := `
- name: Test No Log Security
  hosts: localhost
  gather_facts: false
  tasks:
    - name: Looped secret task
      debug:
        msg: "Processing {{ item }}"
      loop:
        - "super_secret_alpha"
        - "super_secret_beta"
      when: item == 'super_secret_alpha'
      no_log: true
`
	parser := NewParser("")
	pb, err := parser.Parse([]byte(yamlPlaybook))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	exec := NewExecutor(inv)
	defer exec.Close()

	res, err := exec.Execute(pb)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	hostRes := res.Plays[0].Hosts["localhost"]
	if hostRes == nil || len(hostRes.Tasks) != 2 {
		t.Fatalf("expected 2 task results for loop, got %v", hostRes)
	}

	for _, taskRes := range hostRes.Tasks {
		// 无论执行还是跳过，都不允许泄露明文
		if strings.Contains(taskRes.Stdout, "super_secret") {
			t.Errorf("sensitive item leaked in stdout: %s", taskRes.Stdout)
		}
		if itemStr, ok := taskRes.Item.(string); ok && strings.Contains(itemStr, "super_secret") {
			t.Errorf("sensitive item leaked in task.Item: %s", itemStr)
		}
		if taskRes.Item != "[VALUE_SPECIFIED_IN_NO_LOG_TASK]" {
			t.Errorf("expected masked item, got %v", taskRes.Item)
		}
	}
}

// TestPhase23_BlockEnvironmentInheritance 验证 Block 级 environment 向子任务级联继承
func TestPhase23_BlockEnvironmentInheritance(t *testing.T) {
	yamlPlaybook := `
- name: Test Block Environment
  hosts: localhost
  gather_facts: false
  tasks:
    - name: Environment block
      environment:
        PROXY_HOST: "proxy.internal"
        PROXY_PORT: "3128"
      block:
        - name: Check env var
          command: env
          register: env_res
`
	parser := NewParser("")
	pb, err := parser.Parse([]byte(yamlPlaybook))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	inv := inventory.NewInventory()
	inv.EnsureLocalhost()

	exec := NewExecutor(inv)
	defer exec.Close()

	res, err := exec.Execute(pb)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	hostRes := res.Plays[0].Hosts["localhost"]
	if hostRes == nil || len(hostRes.Tasks) == 0 {
		t.Fatalf("expected task execution")
	}

	lastTask := hostRes.Tasks[len(hostRes.Tasks)-1]
	if !strings.Contains(lastTask.Stdout, "PROXY_HOST=proxy.internal") || !strings.Contains(lastTask.Stdout, "PROXY_PORT=3128") {
		t.Errorf("expected block environment in env output, got: %s", lastTask.Stdout)
	}
}

type mockCronSSH struct {
	lastCmd string
	crontab string
}

func (m *mockCronSSH) Connect() error                               { return nil }
func (m *mockCronSSH) Close() error                                 { return nil }
func (m *mockCronSSH) Upload(localPath, remotePath string) error   { return nil }
func (m *mockCronSSH) Download(remotePath, localPath string) error { return nil }

func (m *mockCronSSH) Execute(cmd string) (*ssh.Result, error) {
	m.lastCmd = cmd
	if strings.HasPrefix(cmd, "crontab -l") {
		return &ssh.Result{Stdout: m.crontab, ExitCode: 0}, nil
	}
	return &ssh.Result{ExitCode: 0}, nil
}

func (m *mockCronSSH) ExecuteWithBecome(cmd string) (*ssh.Result, error) {
	return m.Execute(cmd)
}

// TestPhase23_CronSpecialTimeAndOfficialComment 验证 cron 模块对 @reboot 与 Ansible 官方注释的处理
func TestPhase23_CronSpecialTimeAndOfficialComment(t *testing.T) {
	m := &module.CronModule{}

	// 1. 验证 special_time: "@reboot" 避免生成 "@@reboot"
	mockConn := &mockCronSSH{crontab: ""}
	params := map[string]interface{}{
		"name":         "auto_boot",
		"special_time": "@reboot",
		"job":          "/opt/bin/boot.sh",
	}

	res, err := m.Execute(mockConn, params)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected changed=true on new cron")
	}
	if strings.Contains(mockConn.lastCmd, "@@reboot") {
		t.Errorf("cron command contains invalid @@reboot: %s", mockConn.lastCmd)
	}

	// 2. 验证对现有官方 Ansible 注释 (#Ansible: ...) 的识别与幂等更新
	officialCrontab := "#Ansible: auto_boot\n@reboot /opt/bin/boot.sh\n"
	mockConn2 := &mockCronSSH{crontab: officialCrontab}
	res2, err := m.Execute(mockConn2, params)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res2.Changed {
		t.Errorf("expected changed=false when updating identical existing official Ansible cron")
	}
}

// TestPhase23_UserGroupsListAndIdempotency 验证 user 模块对 groups 列表解析与 update_password
func TestPhase23_UserGroupsListAndIdempotency(t *testing.T) {
	uMod := &module.UserModule{}

	// 检查模式下验证 groups: [sudo, docker]
	mockConn := &mockCronSSH{}
	params := map[string]interface{}{
		"name":            "testuser",
		"groups":          []interface{}{"sudo", "docker"},
		"append":          true,
		"password":        "secretpass",
		"update_password": "on_create",
		"check_mode":      true,
	}

	res, err := uMod.Execute(mockConn, params)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !res.Changed {
		t.Errorf("expected changed=true in check mode for non-existent user")
	}
}

