package module

import (
	"strings"
	"testing"
)

// 1. 验证 lookup('random_choice') 与 lookup('dict') 插件解析
func TestPhase39_LookupRandomChoiceAndDict(t *testing.T) {
	// 1.1 random_choice 多个参数
	candidates := []string{"node1", "node2", "node3"}
	res1, err := ExecuteLookupWithVars("", "random_choice", nil, "node1", "node2", "node3")
	if err != nil {
		t.Fatalf("lookup('random_choice') error: %v", err)
	}
	res1Str := strings.TrimSpace(res1.(string))
	found := false
	for _, c := range candidates {
		if res1Str == c {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected one of %v, got %q", candidates, res1Str)
	}

	// 1.2 random_choice 传入切片
	res2, err := ExecuteLookupWithVars("", "random_choice", nil, []interface{}{"alpha", "beta", "gamma"})
	if err != nil {
		t.Fatalf("lookup('random_choice' slice) error: %v", err)
	}
	res2Str := strings.TrimSpace(res2.(string))
	if res2Str != "alpha" && res2Str != "beta" && res2Str != "gamma" {
		t.Fatalf("expected alpha/beta/gamma, got %q", res2Str)
	}

	// 1.3 dict 查找插件将字典结构解构成 [{key: ..., value: ...}]
	testDict := map[string]interface{}{
		"port": 8080,
		"host": "127.0.0.1",
	}
	res3, err := ExecuteLookupWithVars("", "dict", nil, testDict)
	if err != nil {
		t.Fatalf("lookup('dict') error: %v", err)
	}
	list, ok := res3.([]map[string]interface{})
	if !ok || len(list) != 2 {
		t.Fatalf("expected 2 items from dict lookup, got %#v", res3)
	}
	// 升序排列 host 优先于 port
	if list[0]["key"] != "host" || list[0]["value"] != "127.0.0.1" {
		t.Fatalf("expected first item host=127.0.0.1, got %#v", list[0])
	}
	if list[1]["key"] != "port" || list[1]["value"] != 8080 {
		t.Fatalf("expected second item port=8080, got %#v", list[1])
	}
}

// 2. 验证 file/stat/user/group/service 常用别名参数支持
func TestPhase39_ModuleParameterAliases(t *testing.T) {
	// 2.1 FileModule 接受 name 别名
	fileMod := &FileModule{}
	if err := fileMod.Validate(map[string]interface{}{"name": "/tmp/testfile", "state": "touch"}); err != nil {
		t.Fatalf("FileModule validate with 'name' alias failed: %v", err)
	}

	// 2.2 StatModule 接受 name 与 dest 别名
	statMod := &StatModule{}
	if err := statMod.Validate(map[string]interface{}{"name": "/tmp/testfile"}); err != nil {
		t.Fatalf("StatModule validate with 'name' alias failed: %v", err)
	}
	if err := statMod.Validate(map[string]interface{}{"dest": "/tmp/testfile"}); err != nil {
		t.Fatalf("StatModule validate with 'dest' alias failed: %v", err)
	}

	// 2.3 UserModule 接受 user 别名
	userMod := &UserModule{}
	if err := userMod.Validate(map[string]interface{}{"user": "alice", "state": "present"}); err != nil {
		t.Fatalf("UserModule validate with 'user' alias failed: %v", err)
	}

	// 2.4 GroupModule 接受 group 别名
	groupMod := &GroupModule{}
	if err := groupMod.Validate(map[string]interface{}{"group": "devteam", "state": "present"}); err != nil {
		t.Fatalf("GroupModule validate with 'group' alias failed: %v", err)
	}

	// 2.5 ServiceModule 接受 service 别名
	svcMod := &ServiceModule{}
	if err := svcMod.Validate(map[string]interface{}{"service": "nginx", "state": "started"}); err != nil {
		t.Fatalf("ServiceModule validate with 'service' alias failed: %v", err)
	}
}

// 3. 验证 Unarchive 模块 exclude 参数装配
func TestPhase39_UnarchiveExclude(t *testing.T) {
	// 3.1 tar 格式的 --exclude 装配
	cmdTar, err := archiveExtractCommandWithExclude("/tmp/package.tar.gz", "/opt/app", nil, []string{"doc", "*.md"})
	if err != nil {
		t.Fatalf("archiveExtractCommandWithExclude tar error: %v", err)
	}
	if !strings.Contains(cmdTar, "--exclude='doc'") || !strings.Contains(cmdTar, "--exclude='*.md'") {
		t.Fatalf("expected --exclude options in tar command, got: %s", cmdTar)
	}

	// 3.2 zip 格式的 -x 装配
	cmdZip, err := archiveExtractCommandWithExclude("/tmp/package.zip", "/opt/app", nil, []string{"temp/*", "*.log"})
	if err != nil {
		t.Fatalf("archiveExtractCommandWithExclude zip error: %v", err)
	}
	if !strings.Contains(cmdZip, "-x 'temp/*'") || !strings.Contains(cmdZip, "'*.log'") {
		t.Fatalf("expected -x options in zip command, got: %s", cmdZip)
	}
}

