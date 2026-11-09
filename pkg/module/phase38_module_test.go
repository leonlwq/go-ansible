package module

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/ssh"
)

// 1. 验证 Git 模块 recursive 子模块支持与克隆脚本生成
func TestPhase38_GitSubmodulesRecursive(t *testing.T) {
	specWithRec := gitSpec{
		Repo:      "https://github.com/example/repo.git",
		Dest:      "/tmp/repo_rec",
		Clone:     true,
		Update:    true,
		Recursive: true,
	}

	scriptWithRec := buildGitPresentScript(specWithRec)
	if !strings.Contains(scriptWithRec, "--recursive") {
		t.Fatalf("expected --recursive in clone args, got: %s", scriptWithRec)
	}
	if !strings.Contains(scriptWithRec, "submodule update --init --recursive") {
		t.Fatalf("expected submodule update --init --recursive in script, got: %s", scriptWithRec)
	}

	specNoRec := gitSpec{
		Repo:      "https://github.com/example/repo.git",
		Dest:      "/tmp/repo_norec",
		Clone:     true,
		Update:    true,
		Recursive: false,
	}

	scriptNoRec := buildGitPresentScript(specNoRec)
	if strings.Contains(scriptNoRec, "'--recursive'") {
		t.Fatalf("expected NO '--recursive' in clone args when recursive: false, got: %s", scriptNoRec)
	}
	if strings.Contains(scriptNoRec, `if [ "true" = "true" ] && [ -f /tmp/repo_norec/.gitmodules ]`) {
		t.Fatalf("expected submodule block to be guarded by false when recursive: false, got: %s", scriptNoRec)
	}
}

// 2. 验证 Command 模块与 Shell 模块 stdin / stdin_add_newline 输入流
func TestPhase38_CommandAndShellStdinPipe(t *testing.T) {
	localConn := ssh.NewLocalConnection(&inventory.Host{Name: "localhost"})

	// 2.1 Command 模块注入 stdin 并追加换行符
	cmdMod := &CommandModule{}
	res1, err := cmdMod.Execute(localConn, map[string]interface{}{
		"cmd":               "cat",
		"stdin":             "hello ansible stdin",
		"stdin_add_newline": true,
	})
	if err != nil {
		t.Fatalf("CommandModule.Execute stdin failed: %v", err)
	}
	if strings.TrimSpace(res1.Stdout) != "hello ansible stdin" {
		t.Fatalf("expected stdout 'hello ansible stdin', got %q", res1.Stdout)
	}

	// 2.2 Shell 模块注入多行 stdin
	shellMod := &ShellModule{}
	res2, err := shellMod.Execute(localConn, map[string]interface{}{
		"cmd":               "grep -i 'world'",
		"stdin":             "line1\nhello world\nline3",
		"stdin_add_newline": true,
	})
	if err != nil {
		t.Fatalf("ShellModule.Execute stdin failed: %v", err)
	}
	if strings.TrimSpace(res2.Stdout) != "hello world" {
		t.Fatalf("expected grep output 'hello world', got %q", res2.Stdout)
	}

	// 2.3 stdin_add_newline: false 验证
	res3, err := cmdMod.Execute(localConn, map[string]interface{}{
		"cmd":               "wc -c",
		"stdin":             "12345",
		"stdin_add_newline": false,
	})
	if err != nil {
		t.Fatalf("CommandModule.Execute wc -c failed: %v", err)
	}
	if strings.TrimSpace(res3.Stdout) != "5" {
		t.Fatalf("expected byte count '5', got %q", strings.TrimSpace(res3.Stdout))
	}
}

// 3. 验证 Stat 模块返回 atime 与 ctime 时间戳字段
func TestPhase38_StatAtimeAndCtime(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "stat_test.txt")
	if err := os.WriteFile(testFile, []byte("phase 38 stat test"), 0644); err != nil {
		t.Fatalf("write temp file failed: %v", err)
	}

	localConn := ssh.NewLocalConnection(&inventory.Host{Name: "localhost"})
	statMod := &StatModule{}

	res, err := statMod.Execute(localConn, map[string]interface{}{
		"path": testFile,
	})
	if err != nil {
		t.Fatalf("StatModule.Execute failed: %v", err)
	}

	statData, ok := res.Data["stat"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected res.Data['stat'] to be a map, got %T", res.Data["stat"])
	}

	if exists, _ := statData["exists"].(bool); !exists {
		t.Fatalf("expected file to exist")
	}

	atime, ok := statData["atime"].(float64)
	if !ok || atime <= 0 {
		t.Fatalf("expected positive float64 atime, got %v (%T)", statData["atime"], statData["atime"])
	}

	mtime, ok := statData["mtime"].(float64)
	if !ok || mtime <= 0 {
		t.Fatalf("expected positive float64 mtime, got %v (%T)", statData["mtime"], statData["mtime"])
	}

	ctime, ok := statData["ctime"].(float64)
	if !ok || ctime <= 0 {
		t.Fatalf("expected positive float64 ctime, got %v (%T)", statData["ctime"], statData["ctime"])
	}
}
