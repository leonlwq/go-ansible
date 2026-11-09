package module

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/ssh"
)

type mockPhase35Conn struct {
	commands []string
	outputs  map[string]*ssh.Result
}

func newMockPhase35Conn() *mockPhase35Conn {
	return &mockPhase35Conn{
		outputs: make(map[string]*ssh.Result),
	}
}

func (m *mockPhase35Conn) Connect() error { return nil }
func (m *mockPhase35Conn) Close() error   { return nil }
func (m *mockPhase35Conn) Execute(cmd string) (*ssh.Result, error) {
	m.commands = append(m.commands, cmd)
	for pattern, res := range m.outputs {
		if strings.Contains(cmd, pattern) {
			return res, nil
		}
	}
	return &ssh.Result{ExitCode: 0}, nil
}
func (m *mockPhase35Conn) ExecuteWithBecome(cmd string) (*ssh.Result, error) {
	return m.Execute(cmd)
}
func (m *mockPhase35Conn) Upload(src, dest string) error   { return nil }
func (m *mockPhase35Conn) Download(src, dest string) error { return nil }

// 1. 验证 CheckOrSetFilePermissions 及 copy/template/lineinfile/blockinfile/file 在 check_mode 下绝不修改远端权限
func TestPhase35_CheckModeDoesNotMutateFilePermissions(t *testing.T) {
	conn := newMockPhase35Conn()
	// 模拟远端权限为 0644:root:root，目标请求 0600:www:www
	conn.outputs["stat -c"] = &ssh.Result{
		Stdout:   "644:root:root\n",
		ExitCode: 0,
	}

	changed, err := CheckOrSetFilePermissions(conn, "/etc/app.conf", "0600", "www", "www", false, true)
	if err != nil {
		t.Fatalf("CheckOrSetFilePermissions error: %v", err)
	}
	if !changed {
		t.Fatalf("expected changed=true when permissions differ in check_mode")
	}
	for _, cmd := range conn.commands {
		if strings.HasPrefix(cmd, "chmod ") || strings.HasPrefix(cmd, "chown ") || strings.HasPrefix(cmd, "chgrp ") {
			t.Fatalf("check_mode must NOT execute chmod/chown/chgrp, got: %s", cmd)
		}
	}

	// 当远端权限已一致时，FileModule 在 check_mode 下应报告 changed=false
	conn2 := newMockPhase35Conn()
	conn2.outputs["if [ -d '/data/app' ]"] = &ssh.Result{
		Stdout:   changeMarkerFalse + "\n",
		ExitCode: 0,
	}
	conn2.outputs["stat -c"] = &ssh.Result{
		Stdout:   "755:root:root\n",
		ExitCode: 0,
	}
	fileMod := &FileModule{}
	resFile, err := fileMod.Execute(conn2, map[string]interface{}{
		"path":       "/data/app",
		"state":      "directory",
		"mode":       "0755",
		"owner":      "root",
		"group":      "root",
		"check_mode": true,
	})
	if err != nil {
		t.Fatalf("FileModule.Execute error: %v", err)
	}
	if resFile.Changed {
		t.Fatalf("expected FileModule in check_mode to report changed=false when directory and permissions already match")
	}

	// LineinfileModule 在内容已匹配但权限不同时，check_mode 下仅报告 changed=true 且不执行 chmod
	tmpDir := t.TempDir()
	targetFile := filepath.Join(tmpDir, "config.ini")
	if err := os.WriteFile(targetFile, []byte("port=8080\n"), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	localConn := ssh.NewLocalConnection(&inventory.Host{Name: "localhost"})
	lineMod := &LineinfileModule{}
	resLine, err := lineMod.Execute(localConn, map[string]interface{}{
		"path":       targetFile,
		"line":       "port=8080",
		"mode":       "0600",
		"check_mode": true,
	})
	if err != nil {
		t.Fatalf("LineinfileModule.Execute error: %v", err)
	}
	if !resLine.Changed {
		t.Fatalf("expected lineinfile to report changed=true when mode differs in check_mode")
	}
	info, err := os.Stat(targetFile)
	if err != nil {
		t.Fatalf("os.Stat error: %v", err)
	}
	if info.Mode().Perm() != 0644 {
		t.Fatalf("lineinfile check_mode mutated file mode to %o, expected 0644", info.Mode().Perm())
	}
}

// 2. 验证 StatModule 扩展属性 (uid/gid/pw_name/gr_name/readable/writable/executable) 与 checksum_algorithm
func TestPhase35_StatModuleExtendedFieldsAndAlgorithms(t *testing.T) {
	tmpDir := t.TempDir()
	scriptFile := filepath.Join(tmpDir, "run$test.sh")
	if err := os.WriteFile(scriptFile, []byte("#!/bin/sh\necho ok\n"), 0755); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	localConn := ssh.NewLocalConnection(&inventory.Host{Name: "localhost"})
	statMod := &StatModule{}
	res, err := statMod.Execute(localConn, map[string]interface{}{
		"path":               scriptFile,
		"checksum_algorithm": "sha256",
	})
	if err != nil {
		t.Fatalf("StatModule.Execute error: %v", err)
	}
	statMap, ok := res.Data["stat"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected stat map in Data, got %#v", res.Data)
	}
	if statMap["exists"] != true || statMap["isfile"] != true || statMap["executable"] != true || statMap["readable"] != true || statMap["writable"] != true {
		t.Fatalf("unexpected boolean stat attributes: %#v", statMap)
	}
	if _, ok := statMap["uid"].(int); !ok {
		t.Fatalf("expected int uid in stat output, got %T (%v)", statMap["uid"], statMap["uid"])
	}
	if _, ok := statMap["gid"].(int); !ok {
		t.Fatalf("expected int gid in stat output, got %T (%v)", statMap["gid"], statMap["gid"])
	}
	if pwName, _ := statMap["pw_name"].(string); pwName == "" {
		t.Fatalf("expected non-empty pw_name in stat output, got %#v", statMap)
	}
	checksum, _ := statMap["checksum"].(string)
	if len(checksum) != 64 {
		t.Fatalf("expected 64-char sha256 checksum by default/request, got %q", checksum)
	}
}

// 3. 验证 TemplateModule 路径转义与 directory_mode 支持
func TestPhase35_TemplateModuleShellQuoteAndDirectoryMode(t *testing.T) {
	tmpDir := t.TempDir()
	tmplPath := filepath.Join(tmpDir, "app.conf.j2")
	if err := os.WriteFile(tmplPath, []byte("listen={{ port }}\n"), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	destDir := filepath.Join(tmpDir, "dir with space")
	destPath := filepath.Join(destDir, "app.conf")

	localConn := ssh.NewLocalConnection(&inventory.Host{Name: "localhost"})
	tmplMod := &TemplateModule{}
	res, err := tmplMod.Execute(localConn, map[string]interface{}{
		"src":            tmplPath,
		"dest":           destPath,
		"directory_mode": 0750,
		"mode":           0640,
		"become":         true,
		"vars": map[string]interface{}{
			"port": 9090,
		},
	})
	if err != nil {
		t.Fatalf("TemplateModule.Execute error: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected template to report changed=true")
	}
	dirInfo, err := os.Stat(destDir)
	if err != nil {
		t.Fatalf("expected destDir to be created: %v", err)
	}
	if dirInfo.Mode().Perm() != 0750 {
		t.Fatalf("expected directory_mode 0750, got %o", dirInfo.Mode().Perm())
	}
	fileInfo, err := os.Stat(destPath)
	if err != nil {
		t.Fatalf("expected destPath to exist: %v", err)
	}
	if fileInfo.Mode().Perm() != 0640 {
		t.Fatalf("expected file mode 0640, got %o", fileInfo.Mode().Perm())
	}
}

// 4. 验证 ReplaceModule 补齐 mode / owner / group 权限联动与 dest 别名
func TestPhase35_ReplaceModulePermissionsAndAliases(t *testing.T) {
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "service.conf")
	if err := os.WriteFile(target, []byte("env=prod\n"), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	localConn := ssh.NewLocalConnection(&inventory.Host{Name: "localhost"})
	repMod := &ReplaceModule{}
	// 内容已处于期望状态，但 mode 从 0644 调整为 0600
	res, err := repMod.Execute(localConn, map[string]interface{}{
		"dest":    target,
		"regexp":  `env=dev`,
		"replace": "env=prod",
		"mode":    "0600",
	})
	if err != nil {
		t.Fatalf("ReplaceModule.Execute error: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected ReplaceModule to report changed=true when mode is updated")
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("os.Stat error: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected mode 0600, got %o", info.Mode().Perm())
	}
}

// 5. 验证 GitModule 支持 check_mode 与 before/after Commit SHA 返回值注入
func TestPhase35_GitModuleCheckModeAndBeforeAfterRevs(t *testing.T) {
	baseDir := t.TempDir()
	originPath, workPath := initGitOrigin(t, baseDir)
	destPath := filepath.Join(baseDir, "checkout")

	localConn := ssh.NewLocalConnection(&inventory.Host{Name: "localhost"})
	gitMod := &GitModule{}

	// 1. Check Mode 下克隆不会创建目录，但报告 Changed: true 并返回 after SHA
	checkRes, err := gitMod.Execute(localConn, map[string]interface{}{
		"repo":       originPath,
		"dest":       destPath,
		"version":    "main",
		"check_mode": true,
	})
	if err != nil {
		t.Fatalf("GitModule check_mode error: %v", err)
	}
	if !checkRes.Changed {
		t.Fatalf("expected git check_mode to report changed=true for uncloned repo")
	}
	if _, err := os.Stat(destPath); !os.IsNotExist(err) {
		t.Fatalf("git check_mode must not create destination repo")
	}

	// 2. 真实克隆应返回 before="" 和非空 after SHA
	cloneRes, err := gitMod.Execute(localConn, map[string]interface{}{
		"repo":    originPath,
		"dest":    destPath,
		"version": "main",
	})
	if err != nil {
		t.Fatalf("GitModule clone error: %v", err)
	}
	if !cloneRes.Changed {
		t.Fatalf("expected git clone to report changed=true")
	}
	firstSHA, _ := cloneRes.Data["after"].(string)
	if len(firstSHA) < 7 {
		t.Fatalf("expected valid 'after' commit SHA in git result data, got %#v", cloneRes.Data)
	}

	// 3. 推送新提交后更新，应返回 before=firstSHA 和更新后的 after=secondSHA
	pushCommit(t, workPath, "feature.txt", "v2\n")
	updateRes, err := gitMod.Execute(localConn, map[string]interface{}{
		"repo":    originPath,
		"dest":    destPath,
		"version": "main",
	})
	if err != nil {
		t.Fatalf("GitModule update error: %v", err)
	}
	if !updateRes.Changed {
		t.Fatalf("expected git update to report changed=true")
	}
	if updateRes.Data["before"] != firstSHA {
		t.Fatalf("expected before=%q, got %v", firstSHA, updateRes.Data["before"])
	}
	if updateRes.Data["after"] == "" || updateRes.Data["after"] == firstSHA {
		t.Fatalf("expected new after SHA, got %v", updateRes.Data["after"])
	}
}

// 6. 验证 CopyModule remote_src 幂等分支权限检查与 ArchiveModule exclude_path 支持
func TestPhase35_CopyRemoteSrcPermissionsAndArchiveExclude(t *testing.T) {
	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "src.txt")
	destFile := filepath.Join(tmpDir, "dest.txt")
	if err := os.WriteFile(srcFile, []byte("same-content\n"), 0644); err != nil {
		t.Fatalf("WriteFile src error: %v", err)
	}
	if err := os.WriteFile(destFile, []byte("same-content\n"), 0644); err != nil {
		t.Fatalf("WriteFile dest error: %v", err)
	}

	localConn := ssh.NewLocalConnection(&inventory.Host{Name: "localhost"})
	copyMod := &CopyModule{}
	res, err := copyMod.Execute(localConn, map[string]interface{}{
		"src":        srcFile,
		"dest":       destFile,
		"remote_src": true,
		"mode":       0600,
	})
	if err != nil {
		t.Fatalf("CopyModule remote_src error: %v", err)
	}
	if !res.Changed {
		t.Fatalf("expected CopyModule remote_src to update permissions when checksum matches")
	}
	info, err := os.Stat(destFile)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("expected destFile mode 0600, got %o (err=%v)", info.Mode().Perm(), err)
	}

	// 验证 ArchiveModule exclude_path 命令生成
	cmd, err := createArchiveCommand(archiveSpec{
		Paths:       []string{"/srv/app"},
		ExcludePath: []string{"*.log", ".git"},
		Dest:        "/tmp/app.tar.gz",
		Format:      "tar.gz",
	})
	if err != nil {
		t.Fatalf("createArchiveCommand error: %v", err)
	}
	if !strings.Contains(cmd, "--exclude='*.log'") || !strings.Contains(cmd, "--exclude='.git'") {
		t.Fatalf("expected tar --exclude flags in command, got %q", cmd)
	}
}

