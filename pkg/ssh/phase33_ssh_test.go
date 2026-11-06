package ssh

import (
	"testing"
	"time"

	"go-ansible/pkg/inventory"
)

// TestPhase33_SSHClientTimeoutFallback 测试客户端超时默认 10s 兜底保护
func TestPhase33_SSHClientTimeoutFallback(t *testing.T) {
	host := &inventory.Host{
		Name:    "testhost",
		Address: "127.0.0.1",
		Port:    2222,
	}

	// 传入 Timeout <= 0 的配置
	client, err := NewClient(host, &ClientConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	if client.config.Timeout < 10*time.Second {
		t.Errorf("expected default timeout >= 10s when 0 was provided, got: %v", client.config.Timeout)
	}
}

// TestPhase33_ResultFailedFlag 测试 Result.IsFailed 与 Failed 字段显式一致性
func TestPhase33_ResultFailedFlag(t *testing.T) {
	res := &Result{
		ExitCode: 2,
		Failed:   true,
	}
	if !res.IsFailed() {
		t.Errorf("expected IsFailed() to be true")
	}
	if !res.Failed {
		t.Errorf("expected Failed to be true")
	}
}

