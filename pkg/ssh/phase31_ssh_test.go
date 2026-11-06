package ssh

import (
	"strings"
	"sync"
	"testing"
	"time"

	"go-ansible/pkg/inventory"
)

// 9 & 10. 测试 SSHClient.Close 与 getSFTPClient 并发锁解耦无死锁，以及 JumpHost 资源释放
func TestPhase31_ClientCloseSFTPNoDeadlock(t *testing.T) {
	host := &inventory.Host{
		Name:    "dummy_deadlock_host",
		Address: "127.0.0.1",
		Port:    2222,
	}
	cfg := &ClientConfig{
		Timeout: 50 * time.Millisecond,
	}

	// 模拟并发调用 getSFTPClient 与 Close，验证消除 AB-BA 交叉死锁
	for i := 0; i < 20; i++ {
		client, _ := NewClient(host, cfg)
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			_, _ = client.getSFTPClient()
		}()

		go func() {
			defer wg.Done()
			_ = client.Close()
		}()

		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			// 成功并发结束，无死锁
		case <-time.After(2 * time.Second):
			t.Fatalf("iteration %d: deadlock detected between getSFTPClient and Close", i)
		}
	}
}

// 13 & 14. 测试 LocalConnection 与 DockerConnection 超时控制
func TestPhase31_LocalAndDockerConnectionTimeout(t *testing.T) {
	// (a) LocalConnection 超时
	localHost := &inventory.Host{
		Name:       "localhost",
		Address:    "localhost",
		Connection: "local",
	}
	localConn := NewLocalConnection(localHost)
	localConn.SetTimeout(200 * time.Millisecond)

	if localConn.GetTimeout() != 200*time.Millisecond {
		t.Fatalf("expected timeout 200ms, got %v", localConn.GetTimeout())
	}

	start := time.Now()
	res, err := localConn.Execute("sleep 2")
	elapsed := time.Since(start)

	if elapsed > 1500*time.Millisecond {
		t.Fatalf("local command took %v, timeout did not terminate command promptly", elapsed)
	}
	if err == nil {
		t.Fatalf("expected error on command timeout")
	}
	if res == nil || res.ExitCode != 124 {
		t.Fatalf("expected exit code 124 on timeout, got res=%v", res)
	}
	if !strings.Contains(res.Stderr, "timed out") {
		t.Fatalf("expected stderr to mention timeout, got %q", res.Stderr)
	}

	// (b) DockerConnection 超时配置与读取
	dockerHost := &inventory.Host{
		Name:       "test-container",
		Address:    "test-container",
		Connection: "docker",
	}
	dockerConn := NewDockerConnection(dockerHost)
	dockerConn.SetTimeout(500 * time.Millisecond)

	if dockerConn.GetTimeout() != 500*time.Millisecond {
		t.Fatalf("expected docker timeout 500ms, got %v", dockerConn.GetTimeout())
	}
}
