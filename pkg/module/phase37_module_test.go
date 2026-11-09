package module

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/ssh"
)

// 1. 测试 copy 模块 mode: preserve 继承源文件权限
func TestPhase37_CopyModulePreserveMode(t *testing.T) {
	tempDir := t.TempDir()
	srcFile := filepath.Join(tempDir, "script.sh")
	destFile := filepath.Join(tempDir, "script_dest.sh")

	// 创建可执行权限 0755 文件
	if err := os.WriteFile(srcFile, []byte("#!/bin/sh\necho hello\n"), 0755); err != nil {
		t.Fatalf("failed to write src file: %v", err)
	}
	_ = os.Chmod(srcFile, 0755)

	conn := ssh.NewLocalConnection(&inventory.Host{Name: "localhost"})
	mod := &CopyModule{}

	res, err := mod.Execute(conn, map[string]interface{}{
		"src":  srcFile,
		"dest": destFile,
		"mode": "preserve",
	})
	if err != nil {
		t.Fatalf("copy module with mode=preserve failed: %v", err)
	}
	if !res.Changed {
		t.Errorf("expected changed=true")
	}

	fi, err := os.Stat(destFile)
	if err != nil {
		t.Fatalf("stat destFile failed: %v", err)
	}
	perm := fi.Mode().Perm()
	if perm != 0755 {
		t.Errorf("expected dest permissions 0755 (preserved), got %04o", perm)
	}
}

// 2. 测试 copy 模块目录递归复制 mode: preserve
func TestPhase37_CopyDirPreserveMode(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "src_tree")
	destDir := filepath.Join(tempDir, "dest_tree")

	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("mkdir srcDir failed: %v", err)
	}
	execFile := filepath.Join(srcDir, "run.sh")
	confFile := filepath.Join(srcDir, "app.conf")

	_ = os.WriteFile(execFile, []byte("run"), 0755)
	_ = os.Chmod(execFile, 0755)
	_ = os.WriteFile(confFile, []byte("key=val"), 0644)
	_ = os.Chmod(confFile, 0644)

	conn := ssh.NewLocalConnection(&inventory.Host{Name: "localhost"})
	mod := &CopyModule{}

	res, err := mod.Execute(conn, map[string]interface{}{
		"src":  srcDir + "/",
		"dest": destDir,
		"mode": "preserve",
	})
	if err != nil {
		t.Fatalf("copy directory with mode=preserve failed: %v", err)
	}
	if !res.Changed {
		t.Errorf("expected changed=true")
	}

	destExec := filepath.Join(destDir, "run.sh")
	destConf := filepath.Join(destDir, "app.conf")

	fiExec, _ := os.Stat(destExec)
	fiConf, _ := os.Stat(destConf)

	if fiExec.Mode().Perm() != 0755 {
		t.Errorf("expected run.sh 0755, got %04o", fiExec.Mode().Perm())
	}
	if fiConf.Mode().Perm() != 0644 {
		t.Errorf("expected app.conf 0644, got %04o", fiConf.Mode().Perm())
	}
}

// 3. 测试 template 模块 mode: preserve
func TestPhase37_TemplateModulePreserveMode(t *testing.T) {
	tempDir := t.TempDir()
	tmplFile := filepath.Join(tempDir, "tmpl.j2")
	destFile := filepath.Join(tempDir, "output.conf")

	_ = os.WriteFile(tmplFile, []byte("app_name={{ app }}\n"), 0750)
	_ = os.Chmod(tmplFile, 0750)

	conn := ssh.NewLocalConnection(&inventory.Host{Name: "localhost"})
	mod := &TemplateModule{}

	res, err := mod.Execute(conn, map[string]interface{}{
		"src":  tmplFile,
		"dest": destFile,
		"mode": "preserve",
		"vars": map[string]interface{}{
			"app": "portal",
		},
	})
	if err != nil {
		t.Fatalf("template module with mode=preserve failed: %v", err)
	}
	if !res.Changed {
		t.Errorf("expected changed=true")
	}

	fi, err := os.Stat(destFile)
	if err != nil {
		t.Fatalf("stat destFile failed: %v", err)
	}
	if fi.Mode().Perm() != 0750 {
		t.Errorf("expected dest permissions 0750 (preserved from template), got %04o", fi.Mode().Perm())
	}
}

// 4. 测试 CheckOrSetFilePermissions 遇到 "preserve" 关键字时不执行任何非法的 chmod preserve
type mockConnRecording struct {
	ssh.Connection
	executed []string
}

func (m *mockConnRecording) Execute(cmd string) (*ssh.Result, error) {
	m.executed = append(m.executed, cmd)
	return &ssh.Result{ExitCode: 0, Stdout: "755:root:root\n"}, nil
}

func TestPhase37_SetFilePermissionsPreserveGuard(t *testing.T) {
	mock := &mockConnRecording{}
	changed, err := CheckOrSetFilePermissions(mock, "/var/log/app.log", "preserve", "", "", false, false)
	if err != nil {
		t.Fatalf("CheckOrSetFilePermissions failed: %v", err)
	}
	if changed {
		t.Errorf("expected changed=false for preserve mode")
	}
	for _, cmd := range mock.executed {
		if strings.Contains(cmd, "chmod preserve") {
			t.Fatalf("illegal command detected: %s", cmd)
		}
	}
}

// 5. 测试 ResolvePreserveFileMode 本地文件解析
func TestPhase37_ResolvePreserveFileMode(t *testing.T) {
	tempDir := t.TempDir()
	f := filepath.Join(tempDir, "file.txt")
	_ = os.WriteFile(f, []byte("data"), 0640)
	_ = os.Chmod(f, 0640)

	mode := ResolvePreserveFileMode(nil, f, false)
	if mode != "0640" {
		t.Errorf("expected 0640, got %s", mode)
	}

	// 空路径或不存在路径安全回退为空
	if ResolvePreserveFileMode(nil, "", false) != "" {
		t.Errorf("expected empty string for empty path")
	}
	if ResolvePreserveFileMode(nil, "/path/to/missing_file_xyz", false) != "" {
		t.Errorf("expected empty string for missing path")
	}
}
