package playbook

import (
	"testing"

	"go-ansible/pkg/inventory"
)

// TestPhase33_MetaClearHostErrors 测试 meta: clear_host_errors 正常恢复主机错误状态
func TestPhase33_MetaClearHostErrors(t *testing.T) {
	exec := NewExecutor(nil)
	host1 := &inventory.Host{Name: "web1"}

	exec.mu.Lock()
	exec.failedHosts = map[string]bool{"web1": true, "web2": true}
	exec.mu.Unlock()

	// 1. 指定 host 清除单个主机错误
	res := exec.executeMetaModule(host1, map[string]interface{}{"_raw_params": "clear_host_errors"}, nil)
	if res.Failed {
		t.Fatalf("expected clear_host_errors to succeed, got failed: %s", res.Message)
	}

	exec.mu.RLock()
	failed1 := exec.failedHosts["web1"]
	failed2 := exec.failedHosts["web2"]
	exec.mu.RUnlock()

	if failed1 {
		t.Errorf("expected web1 failure to be cleared")
	}
	if !failed2 {
		t.Errorf("expected web2 to remain failed")
	}

	// 2. host 为 nil 时清除全部主机错误
	resAll := exec.executeMetaModule(nil, map[string]interface{}{"_raw_params": "clear_host_errors"}, nil)
	if resAll.Failed {
		t.Fatalf("expected clear_host_errors(nil) to succeed")
	}

	exec.mu.RLock()
	failed2After := exec.failedHosts["web2"]
	exec.mu.RUnlock()

	if failed2After {
		t.Errorf("expected all host failures to be cleared")
	}
}

// TestPhase33_MaxFailPercentageRescuedAndUnreachable 测试 max_fail_percentage 对 rescued 与 unreachable 的准确判断
func TestPhase33_MaxFailPercentageRescuedAndUnreachable(t *testing.T) {
	exec := NewExecutor(nil)

	// Case 1: 失败的任务被 Rescue 了，不应算作失败主机
	resultRescued := &PlayResult{
		Hosts: map[string]*HostResult{
			"h1": {
				Tasks: []*TaskResult{
					{Name: "step 1", Failed: true, Rescued: true},
					{Name: "step 2 rescue", Changed: true},
				},
			},
			"h2": {
				Tasks: []*TaskResult{
					{Name: "step 1", Changed: true},
				},
			},
		},
	}

	// 2 台主机，1 台 rescued，失败数为 0，max_fail_percentage: 20% 不应熔断
	if exec.exceededMaxFailPercentage(resultRescued, 2, 20) {
		t.Errorf("rescued task should not trigger max_fail_percentage exceeded")
	}

	// Case 2: 主机发生 Unreachable（未 ignore），应当算作失败主机
	resultUnreachable := &PlayResult{
		Hosts: map[string]*HostResult{
			"h1": {
				Tasks: []*TaskResult{
					{Name: "step 1", Unreachable: true, Ignored: false},
				},
			},
			"h2": {
				Tasks: []*TaskResult{
					{Name: "step 1", Changed: true},
				},
			},
		},
	}

	// 2 台主机，1 台 unreachable，失败数 1 (50%)，max_fail_percentage: 40% 应该熔断
	if !exec.exceededMaxFailPercentage(resultUnreachable, 2, 40) {
		t.Errorf("unreachable task should count as failure and trigger max_fail_percentage exceeded")
	}
}
