package module

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/ssh"
)

func TestTempfileModule(t *testing.T) {
	client := &ssh.LocalConnection{}
	mod := &TempfileModule{}

	// 1. Validation test
	if err := mod.Validate(map[string]interface{}{"state": "invalid"}); err == nil {
		t.Fatalf("expected error on invalid state")
	}
	if err := mod.Validate(map[string]interface{}{"state": "file"}); err != nil {
		t.Fatalf("validate file state failed: %v", err)
	}

	baseDir, err := os.MkdirTemp("", "tempfile_base_*")
	if err != nil {
		t.Fatalf("create temp base failed: %v", err)
	}
	defer os.RemoveAll(baseDir)

	// 2. Test Check Mode
	checkRes, err := mod.Execute(client, map[string]interface{}{
		"state":        "file",
		"path":         baseDir,
		"prefix":       "testchk_",
		"suffix":       ".txt",
		"__check_mode": true,
	})
	if err != nil {
		t.Fatalf("execute check mode failed: %v", err)
	}
	if !checkRes.Changed {
		t.Errorf("expected changed=true in check mode")
	}
	checkPath := checkRes.Data["path"].(string)
	if !strings.HasPrefix(filepath.Base(checkPath), "testchk_") || !strings.HasSuffix(checkPath, ".txt") {
		t.Errorf("unexpected check mode path: %s", checkPath)
	}
	if _, err := os.Stat(checkPath); err == nil {
		t.Errorf("check mode should not have created file on disk: %s", checkPath)
	}

	// 3. Test Create Temp File
	fileRes, err := mod.Execute(client, map[string]interface{}{
		"state":  "file",
		"path":   baseDir,
		"prefix": "myfile_",
		"suffix": ".log",
	})
	if err != nil {
		t.Fatalf("execute tempfile creation failed: %v", err)
	}
	if !fileRes.Changed {
		t.Errorf("expected changed=true on tempfile creation")
	}
	filePath := fileRes.Data["path"].(string)
	defer os.Remove(filePath)

	fi, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("created file does not exist: %s (%v)", filePath, err)
	}
	if fi.IsDir() {
		t.Fatalf("expected file, got directory: %s", filePath)
	}
	if !strings.HasPrefix(filepath.Base(filePath), "myfile_") || !strings.HasSuffix(filePath, ".log") {
		t.Errorf("unexpected file name pattern: %s", filePath)
	}
	perm := fi.Mode().Perm()
	if perm != 0600 {
		t.Errorf("expected 0600 permissions, got %04o", perm)
	}

	// 4. Test Create Temp Directory
	dirRes, err := mod.Execute(client, map[string]interface{}{
		"state":  "directory",
		"path":   baseDir,
		"prefix": "mydir_",
		"suffix": ".d",
	})
	if err != nil {
		t.Fatalf("execute tempdir creation failed: %v", err)
	}
	if !dirRes.Changed {
		t.Errorf("expected changed=true on tempdir creation")
	}
	dirPath := dirRes.Data["path"].(string)
	defer os.RemoveAll(dirPath)

	dirFi, err := os.Stat(dirPath)
	if err != nil {
		t.Fatalf("created directory does not exist: %s (%v)", dirPath, err)
	}
	if !dirFi.IsDir() {
		t.Fatalf("expected directory, got file: %s", dirPath)
	}
	if !strings.HasPrefix(filepath.Base(dirPath), "mydir_") || !strings.HasSuffix(dirPath, ".d") {
		t.Errorf("unexpected directory name pattern: %s", dirPath)
	}
	dirPerm := dirFi.Mode().Perm()
	if dirPerm != 0700 {
		t.Errorf("expected 0700 permissions, got %04o", dirPerm)
	}
}
