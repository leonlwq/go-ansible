package ssh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/inventory"
)

func TestLocalConnection_Execute(t *testing.T) {
	host := &inventory.Host{
		Name:       "localhost",
		Address:    "localhost",
		Connection: "local",
	}

	conn := NewLocalConnection(host)
	if err := conn.Connect(); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer conn.Close()

	res, err := conn.Execute("echo 'hello go-ansible'")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "hello go-ansible" {
		t.Errorf("expected stdout 'hello go-ansible', got %q", res.Stdout)
	}
	if res.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", res.ExitCode)
	}
	if !res.Changed {
		t.Errorf("expected changed=true")
	}

	// Test non-zero exit code
	failRes, err := conn.Execute("exit 3")
	if err == nil {
		t.Errorf("expected error for non-zero exit code")
	}
	if failRes.ExitCode != 3 {
		t.Errorf("expected exit code 3, got %d", failRes.ExitCode)
	}
}

func TestLocalConnection_UploadAndDownload(t *testing.T) {
	tempDir := t.TempDir()
	srcFile := filepath.Join(tempDir, "src.txt")
	dstFile := filepath.Join(tempDir, "subdir", "dst.txt")
	downloadFile := filepath.Join(tempDir, "download.txt")

	content := "test content for local connection"
	if err := os.WriteFile(srcFile, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	conn := NewLocalConnection(nil)
	if err := conn.Upload(srcFile, dstFile); err != nil {
		t.Fatalf("Upload() error = %v", err)
	}

	dstData, err := os.ReadFile(dstFile)
	if err != nil {
		t.Fatalf("ReadFile(dstFile) error = %v", err)
	}
	if string(dstData) != content {
		t.Errorf("expected %q, got %q", content, string(dstData))
	}

	if err := conn.Download(dstFile, downloadFile); err != nil {
		t.Fatalf("Download() error = %v", err)
	}

	dlData, err := os.ReadFile(downloadFile)
	if err != nil {
		t.Fatalf("ReadFile(downloadFile) error = %v", err)
	}
	if string(dlData) != content {
		t.Errorf("expected %q, got %q", content, string(dlData))
	}
}

func TestConnectionFactory_Selection(t *testing.T) {
	tests := []struct {
		name       string
		host       *inventory.Host
		expectType string
	}{
		{
			name: "Explicit local",
			host: &inventory.Host{
				Name:       "node1",
				Address:    "10.0.0.1",
				Connection: "local",
			},
			expectType: "*ssh.LocalConnection",
		},
		{
			name: "Implicit localhost",
			host: &inventory.Host{
				Name:    "localhost",
				Address: "127.0.0.1",
			},
			expectType: "*ssh.LocalConnection",
		},
		{
			name: "Docker connection",
			host: &inventory.Host{
				Name:       "web_container",
				Connection: "docker",
			},
			expectType: "*ssh.DockerConnection",
		},
		{
			name: "WinRM connection",
			host: &inventory.Host{
				Name:       "win_server",
				Address:    "192.168.10.10",
				Connection: "winrm",
			},
			expectType: "*ssh.WinRMConnection",
		},
		{
			name: "Default SSH connection",
			host: &inventory.Host{
				Name:       "remote_node",
				Address:    "192.168.1.50",
				Port:       22,
				Connection: "ssh",
			},
			expectType: "*ssh.Client",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := NewConnection(tt.host, nil)
			if err != nil {
				t.Fatalf("NewConnection error: %v", err)
			}
			actualType := ""
			switch conn.(type) {
			case *LocalConnection:
				actualType = "*ssh.LocalConnection"
			case *DockerConnection:
				actualType = "*ssh.DockerConnection"
			case *WinRMConnection:
				actualType = "*ssh.WinRMConnection"
			case *Client:
				actualType = "*ssh.Client"
			default:
				t.Fatalf("unexpected connection type: %T", conn)
			}

			if actualType != tt.expectType {
				t.Errorf("expected %s, got %s", tt.expectType, actualType)
			}
		})
	}
}

func TestWinRMConnection_Reserved(t *testing.T) {
	host := &inventory.Host{
		Name:       "win_node",
		Address:    "192.168.1.100",
		Connection: "winrm",
	}
	conn := NewWinRMConnection(host)
	if err := conn.Connect(); err == nil {
		t.Error("expected Connect to return error for reserved winrm")
	} else if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("expected error message to mention 'reserved', got %v", err)
	}

	if _, err := conn.Execute("dir"); err == nil {
		t.Error("expected Execute to return error")
	}
}

func TestConnectionPool_LocalExecution(t *testing.T) {
	pool := NewConnectionPool(nil)
	defer pool.Close()

	host := &inventory.Host{
		Name:       "my_local",
		Address:    "localhost",
		Connection: "local",
	}

	conn, err := pool.Get(host)
	if err != nil {
		t.Fatalf("pool.Get error: %v", err)
	}

	res, err := conn.Execute("echo pool_test")
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "pool_test" {
		t.Errorf("expected 'pool_test', got %q", res.Stdout)
	}

	// Double check pool cache
	if !pool.Has("my_local") {
		t.Error("expected pool to have 'my_local' cached")
	}
}
