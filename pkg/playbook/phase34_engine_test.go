package playbook

import (
	"context"
	"strings"
	"testing"
	"time"

	"go-ansible/pkg/inventory"
)

// 6. 测试 ansible_user 优先级高于 Play 级的 remote_user
func TestPhase34_AnsibleUser_PrecedenceOverRemoteUser(t *testing.T) {
	inv := inventory.NewInventory()
	h1 := &inventory.Host{
		Name:    "host1",
		Address: "127.0.0.1",
		User:    "custom_ansible_user",
		Variables: map[string]string{
			"ansible_user": "custom_ansible_user",
		},
	}
	h2 := &inventory.Host{
		Name:    "host2",
		Address: "127.0.0.1",
	}
	inv.AddHost(h1)
	inv.AddHost(h2)

	executor := NewExecutor(inv)
	play := &Play{
		Name:       "Test Remote User Precedence",
		Hosts:      "all",
		RemoteUser: "play_default_user",
		Tasks: []*Task{
			{
				Name:       "echo user",
				ModuleName: "command",
				Params:     map[string]interface{}{"cmd": "whoami"},
			},
		},
	}

	_, _ = executor.ExecutePlay(play)

	// h1 指定了 ansible_user，不应被 Play 级的 remote_user 覆盖
	if h1.User != "custom_ansible_user" {
		t.Fatalf("expected host1 user to remain custom_ansible_user, got: %q", h1.User)
	}
	// h2 未指定 ansible_user，应继承 Play 级的 remote_user
	if h2.User != "play_default_user" {
		t.Fatalf("expected host2 user to become play_default_user, got: %q", h2.User)
	}
}

// 12. 测试 buildAnsibleLoopContext 注入 previtem 与 nextitem
func TestPhase34_AnsibleLoop_ExtendedAttributes(t *testing.T) {
	items := []interface{}{"alpha", "beta", "gamma"}

	ctx0 := buildAnsibleLoopContext(0, items)
	if ctx0["first"] != true {
		t.Fatalf("expected first=true for index 0")
	}
	if ctx0["last"] != false {
		t.Fatalf("expected last=false for index 0")
	}
	if _, hasPrev := ctx0["previtem"]; hasPrev {
		t.Fatalf("expected no previtem for first item")
	}
	if ctx0["nextitem"] != "beta" {
		t.Fatalf("expected nextitem=beta, got: %v", ctx0["nextitem"])
	}

	ctx1 := buildAnsibleLoopContext(1, items)
	if ctx1["previtem"] != "alpha" {
		t.Fatalf("expected previtem=alpha, got: %v", ctx1["previtem"])
	}
	if ctx1["nextitem"] != "gamma" {
		t.Fatalf("expected nextitem=gamma, got: %v", ctx1["nextitem"])
	}

	ctx2 := buildAnsibleLoopContext(2, items)
	if ctx2["first"] != false {
		t.Fatalf("expected first=false for index 2")
	}
	if ctx2["last"] != true {
		t.Fatalf("expected last=true for index 2")
	}
	if ctx2["previtem"] != "beta" {
		t.Fatalf("expected previtem=beta, got: %v", ctx2["previtem"])
	}
	if _, hasNext := ctx2["nextitem"]; hasNext {
		t.Fatalf("expected no nextitem for last item")
	}
}

// 13. 测试 Executor.sleep 支持 context 取消中断
func TestPhase34_ExecutorSleep_ContextCancellation(t *testing.T) {
	executor := NewExecutor(nil)
	ctx, cancel := context.WithCancel(context.Background())
	executor.SetContext(ctx)

	start := time.Now()
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	// 要求 sleep 5 秒，但应在 ~20ms 时响应 cancel 立即退出
	ok := executor.sleep(5 * time.Second)
	elapsed := time.Since(start)

	if ok {
		t.Fatalf("expected sleep to return false upon context cancel")
	}
	if elapsed > 1*time.Second {
		t.Fatalf("expected sleep to cancel within 1s, took %v", elapsed)
	}
}

// 15. 测试 vars_prompt 变量更新后 hostvars 缓存即时失效
func TestPhase34_VarsPrompt_CacheInvalidation(t *testing.T) {
	inv := inventory.NewInventory()
	h := &inventory.Host{Name: "web1", Address: "127.0.0.1"}
	inv.AddHost(h)

	executor := NewExecutor(inv)
	play := &Play{
		Name:  "Test Prompt Invalidation",
		Hosts: "all",
		VarsPrompt: []*VarsPrompt{
			{
				Name:    "admin_secret",
				Prompt:  "Enter password",
				Default: "prompted_pass_999",
			},
		},
	}

	// 初始构建快照
	_ = executor.buildHostVarsSnapshot(h, nil)

	// 执行 promptPlayVars
	playVars := make(map[string]interface{})
	executor.Stdin = strings.NewReader("\n")
	err := executor.promptPlayVars(play, playVars)
	if err != nil {
		t.Fatalf("promptPlayVars failed: %v", err)
	}

	// 验证 play.Vars 与 playVars 中存在 admin_secret
	if play.Vars["admin_secret"] != "prompted_pass_999" {
		t.Fatalf("expected play.Vars to have admin_secret")
	}

	// 验证 cachedHostVarsSnapshot 已被安全清空，下次读取重新拉取
	executor.hostVarsMu.RLock()
	isNil := executor.cachedHostVarsSnapshot == nil
	executor.hostVarsMu.RUnlock()

	if !isNil {
		t.Fatalf("expected cachedHostVarsSnapshot to be invalidated after promptPlayVars")
	}
}
