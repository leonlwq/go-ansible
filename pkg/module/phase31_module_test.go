package module

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/ssh"
)

// 1. 测试 cron 模块规范对齐：识别带有空格的注释行 `#  0 2 * * * ...`，以及无名任务就地注释/解注释
func TestPhase31_CronNormalizeAndInPlaceComment(t *testing.T) {
	// (a) 带空格的注释行识别为已经 disabled
	existingWithSpace := "# Ansible: backup\n#   0 2 * * * /bin/backup\n"
	content, changed := updateCrontabContent(existingWithSpace, "backup", "/bin/backup", "0 2 * * * /bin/backup", "present", true)
	if changed {
		t.Fatalf("expected changed=false when entry with space in comment is already disabled, got changed=true, content: %q", content)
	}

	// (b) 无名任务 (name == "") 且当前为活跃任务，切换 disabled=true 时就地注释
	activeNoName := "0 3 * * * /bin/clean_tmp\n"
	disabledRes, changed := updateCrontabContent(activeNoName, "", "/bin/clean_tmp", "0 3 * * * /bin/clean_tmp", "present", true)
	if !changed {
		t.Fatalf("expected changed=true when disabling unnamed cron job")
	}
	if !strings.HasPrefix(strings.TrimSpace(disabledRes), "#") || !strings.Contains(disabledRes, "/bin/clean_tmp") {
		t.Fatalf("expected unnamed job to be commented out, got: %q", disabledRes)
	}

	// (c) 无名任务 (name == "") 且当前为注释状态，切换 disabled=false 时就地取消注释
	commentedNoName := "# 0 3 * * * /bin/clean_tmp\n"
	enabledRes, changed := updateCrontabContent(commentedNoName, "", "/bin/clean_tmp", "0 3 * * * /bin/clean_tmp", "present", false)
	if !changed {
		t.Fatalf("expected changed=true when enabling unnamed cron job")
	}
	if strings.HasPrefix(strings.TrimSpace(enabledRes), "#") {
		t.Fatalf("expected unnamed job to be uncommented, got: %q", enabledRes)
	}
}

// 2. 测试 find 模块 age 与 size 默认前缀以及周 (w) 和秒 (s) 单位支持
func TestPhase31_FindAgeAndSizeDefaultPrefix(t *testing.T) {
	tempDir := t.TempDir()
	f1 := filepath.Join(tempDir, "old_file.log")
	_ = os.WriteFile(f1, make([]byte, 2048), 0644)
	oldTime := time.Now().Add(-10 * 24 * time.Hour)
	_ = os.Chtimes(f1, oldTime, oldTime)

	f2 := filepath.Join(tempDir, "new_file.log")
	_ = os.WriteFile(f2, make([]byte, 100), 0644)

	localConn := ssh.NewLocalConnection(&inventory.Host{Name: "localhost", Connection: "local"})
	findMod := &FindModule{}

	// age 无前缀应默认为 "+" (找早于 5 天前的文件)
	res, err := findMod.Execute(localConn, map[string]interface{}{
		"paths": tempDir,
		"age":   "5d",
	})
	if err != nil {
		t.Fatalf("find age default + failed: %v", err)
	}
	files, ok := res.Data["files"].([]map[string]interface{})
	if !ok || len(files) != 1 || !strings.Contains(files[0]["path"].(string), "old_file.log") {
		t.Fatalf("expected to find old_file.log with default +5d age, got: %v", res.Data["files"])
	}

	// 测试周 (w) 单位：早于 1 周 (1w = 7d)
	resW, err := findMod.Execute(localConn, map[string]interface{}{
		"paths": tempDir,
		"age":   "1w",
	})
	if err != nil {
		t.Fatalf("find age 1w failed: %v", err)
	}
	filesW := resW.Data["files"].([]map[string]interface{})
	if len(filesW) != 1 {
		t.Fatalf("expected 1 file for age 1w, got %d", len(filesW))
	}

	// 测试 size 默认前缀 "+" (找大于 1k 的文件)
	resSize, err := findMod.Execute(localConn, map[string]interface{}{
		"paths": tempDir,
		"size":  "1k",
	})
	if err != nil {
		t.Fatalf("find size default + failed: %v", err)
	}
	filesSize := resSize.Data["files"].([]map[string]interface{})
	if len(filesSize) != 1 || !strings.Contains(filesSize[0]["path"].(string), "old_file.log") {
		t.Fatalf("expected old_file.log with size > 1k, got: %v", resSize.Data["files"])
	}
}

// 3. 测试 uri 模块 return_content 判定、JSON 结构解析与复杂 body 序列化
func TestPhase31_URIReturnContentAndJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			var bodyMap map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&bodyMap)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":   "created",
				"received": bodyMap,
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "pong",
			"code":    200,
		})
	}))
	defer ts.Close()

	uriMod := &URIModule{}
	localConn := ssh.NewLocalConnection(&inventory.Host{Name: "localhost", Connection: "local"})

	// (a) return_content=false (默认): 不应包含 content
	resDefault, err := uriMod.Execute(localConn, map[string]interface{}{
		"url": ts.URL,
	})
	if err != nil {
		t.Fatalf("uri get failed: %v", err)
	}
	if _, ok := resDefault.Data["content"]; ok {
		t.Fatalf("expected no content when return_content=false (default)")
	}

	// (b) return_content=true: 包含 content 且成功解析为 json 字典
	resWithContent, err := uriMod.Execute(localConn, map[string]interface{}{
		"url":            ts.URL,
		"return_content": true,
	})
	if err != nil {
		t.Fatalf("uri get with return_content failed: %v", err)
	}
	if resWithContent.Data["content"] == "" {
		t.Fatalf("expected content to be returned")
	}
	jsonMap, ok := resWithContent.Data["json"].(map[string]interface{})
	if !ok || jsonMap["message"] != "pong" {
		t.Fatalf("expected json map with message=pong, got: %v", resWithContent.Data["json"])
	}

	// (c) 复杂 body (map) 自动序列化为 JSON
	resPost, err := uriMod.Execute(localConn, map[string]interface{}{
		"url":            ts.URL,
		"method":         "POST",
		"return_content": true,
		"body": map[string]interface{}{
			"cluster": "prod-1",
			"active":  true,
		},
	})
	if err != nil {
		t.Fatalf("uri post failed: %v", err)
	}
	postJson, ok := resPost.Data["json"].(map[string]interface{})
	if !ok || postJson["status"] != "created" {
		t.Fatalf("expected status=created in post json, got: %v", resPost.Data["json"])
	}
	recv, ok := postJson["received"].(map[string]interface{})
	if !ok || recv["cluster"] != "prod-1" {
		t.Fatalf("expected received payload to preserve cluster=prod-1, got: %v", recv)
	}
}

// 4. 测试 file 模块 modification_time / access_time 与 preserve
func TestPhase31_FileModificationAndAccessTime(t *testing.T) {
	// (a) modification_time 绝对时间戳构建
	cmdTouch, err := buildFileStateCommandWithTimes("touch", "/opt/app.conf", "", false, false, "202611061200", "preserve")
	if err != nil {
		t.Fatalf("buildFileStateCommandWithTimes failed: %v", err)
	}
	if !strings.Contains(cmdTouch, "-t 202611061200") {
		t.Fatalf("expected -t 202611061200 in command, got: %s", cmdTouch)
	}
	if !strings.Contains(cmdTouch, "-m") {
		t.Fatalf("expected -m with preserve access_time, got: %s", cmdTouch)
	}

	// (b) 绝对时间戳规范化
	tsFormatted := formatTouchTimestamp("2026-11-06 12:00:00")
	if tsFormatted != "202611061200.00" {
		t.Fatalf("expected 202611061200.00, got: %s", tsFormatted)
	}

	// (c) preserve 关键字识别：当文件已存在时保持不变 (changed=false)
	cmdFile, err := buildFileStateCommandWithTimes("file", "/var/log/app.log", "", false, false, "preserve", "preserve")
	if err != nil {
		t.Fatalf("buildFileStateCommandWithTimes preserve failed: %v", err)
	}
	if !strings.Contains(cmdFile, "__go_ansible_changed__=false") {
		t.Fatalf("expected changed=false with preserve times, got: %s", cmdFile)
	}
}

// 5. 测试 urlsplit 过滤器、checksum 过滤器与 hash 默认 sha1
func TestPhase31_UrlsplitAndChecksumFilters(t *testing.T) {
	// (a) checksum 过滤器
	cs := TemplateChecksum("hello world")
	// sha1 of "hello world" is 2aae6c35c94fcfb415dbe95f408b9ce91ee846ed
	if cs != "2aae6c35c94fcfb415dbe95f408b9ce91ee846ed" {
		t.Fatalf("expected 2aae6c35c94fcfb415dbe95f408b9ce91ee846ed, got %s", cs)
	}

	// (b) hash 过滤器单参数默认 sha1
	hSha1 := TemplateHash("hello world")
	if hSha1 != cs {
		t.Fatalf("expected hash('hello world') to match checksum, got: %s", hSha1)
	}

	// (c) urlsplit 过滤器
	res := TemplateUrlsplit("https://user:pass@example.com:8443/api/v1/resource?status=active#section2", "")
	splitMap, ok := res.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map from urlsplit, got: %T", res)
	}
	if splitMap["scheme"] != "https" {
		t.Errorf("expected scheme https, got %v", splitMap["scheme"])
	}
	if splitMap["netloc"] != "user:pass@example.com:8443" {
		t.Errorf("expected netloc user:pass@example.com:8443, got %v", splitMap["netloc"])
	}
	if splitMap["hostname"] != "example.com" {
		t.Errorf("expected hostname example.com, got %v", splitMap["hostname"])
	}
	if splitMap["port"] != 8443 {
		t.Errorf("expected port 8443, got %v", splitMap["port"])
	}
	if splitMap["path"] != "/api/v1/resource" {
		t.Errorf("expected path /api/v1/resource, got %v", splitMap["path"])
	}
	if splitMap["query"] != "status=active" {
		t.Errorf("expected query status=active, got %v", splitMap["query"])
	}
	if splitMap["fragment"] != "section2" {
		t.Errorf("expected fragment section2, got %v", splitMap["fragment"])
	}

	// (d) urlsplit 单元素提取
	pathVal := TemplateUrlsplit("https://example.com/test/path", "path")
	if pathVal != "/test/path" {
		t.Fatalf("expected /test/path, got %v", pathVal)
	}
}
