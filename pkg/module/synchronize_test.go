package module

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/ssh"
)

type mockSyncConn struct {
	host *inventory.Host
}

func (c *mockSyncConn) Connect() error                          { return nil }
func (c *mockSyncConn) Close() error                            { return nil }
func (c *mockSyncConn) Execute(cmd string) (*ssh.Result, error) { return nil, nil }
func (c *mockSyncConn) ExecuteWithBecome(cmd string) (*ssh.Result, error) {
	return nil, nil
}
func (c *mockSyncConn) Upload(local, remote string) error   { return nil }
func (c *mockSyncConn) Download(remote, local string) error { return nil }
func (c *mockSyncConn) Host() *inventory.Host               { return c.host }

func TestSynchronizeModule_Validate(t *testing.T) {
	mod := &SynchronizeModule{}
	if err := mod.Validate(map[string]interface{}{}); err == nil {
		t.Fatal("expected error without src and dest")
	}
	if err := mod.Validate(map[string]interface{}{"src": "/a"}); err == nil {
		t.Fatal("expected error without dest")
	}
	if err := mod.Validate(map[string]interface{}{"src": "/a", "dest": "/b"}); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestSynchronizeModule_LocalExecutionAndIdempotency(t *testing.T) {
	mod := &SynchronizeModule{}
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src") + "/"
	destDir := filepath.Join(tmpDir, "dest") + "/"

	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(srcDir, "sample.txt")
	if err := os.WriteFile(testFile, []byte("rsync sync payload"), 0644); err != nil {
		t.Fatal(err)
	}

	conn := &mockSyncConn{
		host: &inventory.Host{
			Name:       "localhost",
			Connection: "local",
		},
	}

	// 1. Check Mode
	checkRes, err := mod.Execute(conn, map[string]interface{}{
		"src":                 srcDir,
		"dest":                destDir,
		"_ansible_check_mode": true,
	})
	if err != nil {
		t.Fatalf("check mode failed: %v", err)
	}
	if !checkRes.Changed {
		t.Errorf("expected Changed=true in check mode when dest lacks files")
	}
	if _, err := os.Stat(filepath.Join(destDir, "sample.txt")); !os.IsNotExist(err) {
		t.Errorf("file should not exist in dest after check mode")
	}

	// 2. Real Execution -> Changed = true
	res1, err := mod.Execute(conn, map[string]interface{}{
		"src":  srcDir,
		"dest": destDir,
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res1.Changed {
		t.Errorf("expected Changed=true on initial transfer")
	}
	data, err := os.ReadFile(filepath.Join(destDir, "sample.txt"))
	if err != nil {
		t.Fatalf("failed to read synchronized file: %v", err)
	}
	if string(data) != "rsync sync payload" {
		t.Errorf("unexpected file content: %s", string(data))
	}

	// 3. Idempotent re-run -> Changed = false
	res2, err := mod.Execute(conn, map[string]interface{}{
		"src":  srcDir,
		"dest": destDir,
	})
	if err != nil {
		t.Fatalf("idempotent execute failed: %v", err)
	}
	if res2.Changed {
		t.Errorf("expected Changed=false when files are already synchronized")
	}
}

func TestSynchronizeModule_RemoteCommandLine(t *testing.T) {
	mod := &SynchronizeModule{}

	var capturedArgs []string
	origRunner := rsyncCmdRunner
	defer func() { rsyncCmdRunner = origRunner }()

	rsyncCmdRunner = func(cmd *exec.Cmd) ([]byte, []byte, int, error) {
		capturedArgs = cmd.Args
		return []byte(">f+++++++ file.txt\n"), nil, 0, nil
	}

	conn := &mockSyncConn{
		host: &inventory.Host{
			Name:          "web01",
			Address:       "192.168.10.50",
			User:          "ubuntu",
			Port:          2222,
			PrivateKey:    "/home/user/.ssh/deploy_key",
			SSHCommonArgs: "-o StrictHostKeyChecking=no",
		},
	}

	res, err := mod.Execute(conn, map[string]interface{}{
		"src":        "/local/app/",
		"dest":       "/remote/app/",
		"delete":     true,
		"rsync_path": "sudo rsync",
		"rsync_opts": []string{"--exclude=.git", "--timeout=30"},
		"mode":       "push",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res.Changed {
		t.Errorf("expected Changed=true")
	}

	joined := strings.Join(capturedArgs, " ")
	t.Logf("Captured rsync command: %s", joined)

	if !strings.Contains(joined, "--delete") {
		t.Errorf("expected --delete in args: %s", joined)
	}
	if !strings.Contains(joined, "--rsync-path=sudo rsync") {
		t.Errorf("expected --rsync-path in args: %s", joined)
	}
	if !strings.Contains(joined, "--exclude=.git") {
		t.Errorf("expected --exclude=.git in args: %s", joined)
	}
	if !strings.Contains(joined, "-p 2222") {
		t.Errorf("expected ssh port 2222 in args: %s", joined)
	}
	if !strings.Contains(joined, "-i /home/user/.ssh/deploy_key") {
		t.Errorf("expected ssh private key in args: %s", joined)
	}
	if !strings.Contains(joined, "ubuntu@192.168.10.50:/remote/app/") {
		t.Errorf("expected push remote destination in args: %s", joined)
	}

	// Test pull mode
	capturedArgs = nil
	_, err = mod.Execute(conn, map[string]interface{}{
		"src":  "/remote/backup.tar.gz",
		"dest": "/local/backups/",
		"mode": "pull",
	})
	if err != nil {
		t.Fatalf("pull execute failed: %v", err)
	}
	pullJoined := strings.Join(capturedArgs, " ")
	if !strings.Contains(pullJoined, "ubuntu@192.168.10.50:/remote/backup.tar.gz /local/backups/") {
		t.Errorf("expected pull remote source in args: %s", pullJoined)
	}
}
