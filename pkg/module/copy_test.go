package module

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/ssh"
)

func TestCopyDirectoryRecursion(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "conf.d")
	destDir := filepath.Join(tempDir, "remote_conf.d")

	// Create src structure:
	// conf.d/
	//   app.conf
	//   extra/
	//     sub.conf
	if err := os.MkdirAll(filepath.Join(srcDir, "extra"), 0755); err != nil {
		t.Fatalf("mkdir src error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "app.conf"), []byte("server { port 80; }"), 0644); err != nil {
		t.Fatalf("write file error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "extra", "sub.conf"), []byte("sub config"), 0644); err != nil {
		t.Fatalf("write sub file error: %v", err)
	}

	conn := ssh.NewLocalConnection(&inventory.Host{Name: "localhost"})
	mod := &CopyModule{}

	// 1. Copy directory with trailing slash (copies contents into destDir)
	res, err := mod.Execute(conn, map[string]interface{}{
		"src":  srcDir + "/",
		"dest": destDir,
	})
	if err != nil {
		t.Fatalf("copy directory failed: %v", err)
	}
	if !res.Changed {
		t.Errorf("expected changed=true on initial directory copy")
	}

	// Verify target files exist
	appConf := filepath.Join(destDir, "app.conf")
	subConf := filepath.Join(destDir, "extra", "sub.conf")
	if data, err := os.ReadFile(appConf); err != nil || string(data) != "server { port 80; }" {
		t.Errorf("expected app.conf content, got err=%v, data=%s", err, string(data))
	}
	if data, err := os.ReadFile(subConf); err != nil || string(data) != "sub config" {
		t.Errorf("expected sub.conf content, got err=%v, data=%s", err, string(data))
	}

	// 2. Idempotency test (identical re-run)
	res2, err := mod.Execute(conn, map[string]interface{}{
		"src":  srcDir + "/",
		"dest": destDir,
	})
	if err != nil {
		t.Fatalf("copy directory re-run failed: %v", err)
	}
	if res2.Changed {
		t.Errorf("expected changed=false on identical directory re-run, got changed=true")
	}

	// 3. Check Mode test
	// Add new file to srcDir
	if err := os.WriteFile(filepath.Join(srcDir, "new.conf"), []byte("new"), 0644); err != nil {
		t.Fatalf("write new file: %v", err)
	}
	resCheck, err := mod.Execute(conn, map[string]interface{}{
		"src":        srcDir + "/",
		"dest":       destDir,
		"check_mode": true,
	})
	if err != nil {
		t.Fatalf("copy directory check mode failed: %v", err)
	}
	if !resCheck.Changed {
		t.Errorf("expected changed=true in check mode when new file exists")
	}
	// Verify new.conf was NOT written to destDir
	if _, err := os.Stat(filepath.Join(destDir, "new.conf")); err == nil {
		t.Errorf("new.conf was created in check mode!")
	}
}

func TestFetchMultiHostIsolationAndIdempotency(t *testing.T) {
	tempDir := t.TempDir()
	remoteFile := filepath.Join(tempDir, "remote_data.txt")
	if err := os.WriteFile(remoteFile, []byte("node payload"), 0644); err != nil {
		t.Fatalf("write remote file: %v", err)
	}

	backupDir := filepath.Join(tempDir, "backups")
	conn := ssh.NewLocalConnection(&inventory.Host{Name: "localhost"})
	mod := &FetchModule{}

	// 1. Fetch from node1 (default flat=false)
	res1, err := mod.Execute(conn, map[string]interface{}{
		"src":                remoteFile,
		"dest":               backupDir,
		"inventory_hostname": "node1",
	})
	if err != nil {
		t.Fatalf("fetch node1 error: %v", err)
	}
	if !res1.Changed {
		t.Errorf("expected changed=true on initial fetch")
	}

	// 2. Fetch from node2 (different host)
	res2, err := mod.Execute(conn, map[string]interface{}{
		"src":                remoteFile,
		"dest":               backupDir,
		"inventory_hostname": "node2",
	})
	if err != nil {
		t.Fatalf("fetch node2 error: %v", err)
	}
	if !res2.Changed {
		t.Errorf("expected changed=true on node2 fetch")
	}

	// Verify both files exist under isolated directories
	cleanSrc := strings.TrimPrefix(filepath.Clean(remoteFile), string(filepath.Separator))
	node1File := filepath.Join(backupDir, "node1", cleanSrc)
	node2File := filepath.Join(backupDir, "node2", cleanSrc)

	if data, err := os.ReadFile(node1File); err != nil || string(data) != "node payload" {
		t.Errorf("node1File missing or content mismatch: %v, %s", err, string(data))
	}
	if data, err := os.ReadFile(node2File); err != nil || string(data) != "node payload" {
		t.Errorf("node2File missing or content mismatch: %v, %s", err, string(data))
	}

	// 3. Idempotency test (fetch node1 again)
	resIdempotent, err := mod.Execute(conn, map[string]interface{}{
		"src":                remoteFile,
		"dest":               backupDir,
		"inventory_hostname": "node1",
	})
	if err != nil {
		t.Fatalf("fetch node1 idempotent error: %v", err)
	}
	if resIdempotent.Changed {
		t.Errorf("expected changed=false on identical re-fetch, got changed=true")
	}

	// 4. Flat fetch test
	flatFile := filepath.Join(backupDir, "flat_download.txt")
	resFlat, err := mod.Execute(conn, map[string]interface{}{
		"src":   remoteFile,
		"dest":  flatFile,
		"flat":  true,
	})
	if err != nil {
		t.Fatalf("flat fetch error: %v", err)
	}
	if !resFlat.Changed {
		t.Errorf("expected changed=true on flat fetch")
	}
	if data, err := os.ReadFile(flatFile); err != nil || string(data) != "node payload" {
		t.Errorf("flatFile mismatch: %v, %s", err, string(data))
	}

	// 5. Check Mode test
	resCheck, err := mod.Execute(conn, map[string]interface{}{
		"src":        remoteFile,
		"dest":       filepath.Join(backupDir, "check_mode_target.txt"),
		"flat":       true,
		"check_mode": true,
	})
	if err != nil {
		t.Fatalf("fetch check mode error: %v", err)
	}
	if !resCheck.Changed {
		t.Errorf("expected changed=true in check mode")
	}
	if _, err := os.Stat(filepath.Join(backupDir, "check_mode_target.txt")); err == nil {
		t.Errorf("file created in check mode!")
	}
}

