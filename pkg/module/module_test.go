package module

import (
	"strings"
	"testing"
	"text/template"
)

func TestNormalizePackageNames(t *testing.T) {
	got := normalizePackageNames([]interface{}{"nginx", " redis "})
	if got != "nginx redis" {
		t.Fatalf("expected normalized package list, got %q", got)
	}

	got = normalizePackageNames("nginx, redis  curl")
	if got != "nginx redis curl" {
		t.Fatalf("expected normalized string list, got %q", got)
	}
}

func TestCopyHelpers(t *testing.T) {
	if got := resolveDestPath("/tmp/releases/", "/opt/build/app.tar.gz"); got != "/tmp/releases/app.tar.gz" {
		t.Fatalf("unexpected resolved dest path: %q", got)
	}
	if got := resolveDestPath("/tmp/app.tar.gz", "/opt/build/app.tar.gz"); got != "/tmp/app.tar.gz" {
		t.Fatalf("unexpected fixed dest path: %q", got)
	}

	cmd := buildContentWriteCommand("/tmp/app.conf", "hello")
	if !strings.Contains(cmd, "> '/tmp/app.conf'") || !strings.Contains(cmd, "base64 -d") || !strings.Contains(cmd, "GOANSIBLE_EOF_") {
		t.Fatalf("unexpected content write command: %q", cmd)
	}
	if stringChecksum("hello") == stringChecksum("world") {
		t.Fatal("expected distinct content checksums")
	}

	diff := buildTextDiff("/tmp/app.conf", "old\n", "new\n")
	if !strings.Contains(diff, "--- before: /tmp/app.conf") || !strings.Contains(diff, "-old") || !strings.Contains(diff, "+new") {
		t.Fatalf("unexpected text diff: %q", diff)
	}
	if buildTextDiff("/tmp/app.conf", "same\n", "same\n") != "" {
		t.Fatal("expected empty diff for identical content")
	}
}

func TestTextEncodingAndNewlineHelpers(t *testing.T) {
	encoding, err := normalizeTextEncoding("utf8")
	if err != nil || encoding != "utf-8" {
		t.Fatalf("expected utf8 to normalize, got encoding=%q err=%v", encoding, err)
	}
	encoding, err = normalizeTextEncoding("ascii")
	if err != nil || encoding != "us-ascii" {
		t.Fatalf("expected ascii to normalize, got encoding=%q err=%v", encoding, err)
	}
	encoding, err = normalizeTextEncoding("iso-8859-1")
	if err != nil || encoding != "latin-1" {
		t.Fatalf("expected iso-8859-1 to normalize, got encoding=%q err=%v", encoding, err)
	}
	if _, err := normalizeTextEncoding("gbk"); err == nil {
		t.Fatal("expected unsupported encoding error")
	}

	if got := applyNewlineMode("a\nb\n", "crlf"); got != "a\r\nb\r\n" {
		t.Fatalf("unexpected crlf conversion: %q", got)
	}
	if got := applyNewlineMode("a\r\nb\r\n", "lf"); got != "a\nb\n" {
		t.Fatalf("unexpected lf conversion: %q", got)
	}
}

func TestTextEncodingRoundTripHelpers(t *testing.T) {
	data, err := encodeTextContent("olá\n", "latin-1")
	if err != nil {
		t.Fatalf("encodeTextContent(latin-1) error = %v", err)
	}
	if len(data) != 4 || data[2] != 0xe1 {
		t.Fatalf("unexpected latin-1 bytes: %#v", data)
	}

	decoded, err := decodeTextContent([]byte{0x6f, 0x6c, 0xe1, 0x0a}, "latin-1")
	if err != nil {
		t.Fatalf("decodeTextContent(latin-1) error = %v", err)
	}
	if decoded != "olá\n" {
		t.Fatalf("unexpected latin-1 decoded content: %q", decoded)
	}

	if _, err := encodeTextContent("中文", "latin-1"); err == nil {
		t.Fatal("expected latin-1 encoding failure for unsupported rune")
	}
	if _, err := decodeTextContent([]byte{0xff}, "us-ascii"); err == nil {
		t.Fatal("expected us-ascii decoding failure for high byte")
	}
}

func TestSlurpModuleValidateAndCommand(t *testing.T) {
	mod := &SlurpModule{}
	if err := mod.Validate(map[string]interface{}{}); err == nil {
		t.Fatal("expected validation error for missing src")
	}
	if err := mod.Validate(map[string]interface{}{"src": "/tmp/app.conf"}); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	cmd := buildSlurpCommand("/tmp/app's.conf")
	if !strings.Contains(cmd, "base64") || !strings.Contains(cmd, "openssl base64 -A") {
		t.Fatalf("expected base64 fallback command, got %q", cmd)
	}
	if !strings.Contains(cmd, shellQuote("/tmp/app's.conf")) {
		t.Fatalf("expected quoted source path, got %q", cmd)
	}
}

func TestRegistryIncludesSlurp(t *testing.T) {
	registry := NewRegistry()
	mod, err := registry.Get("slurp")
	if err != nil {
		t.Fatalf("registry.Get(slurp) error = %v", err)
	}
	if mod.Name() != "slurp" {
		t.Fatalf("unexpected module name: %s", mod.Name())
	}
}

func TestBuildFileStateCommand(t *testing.T) {
	cmd, err := buildFileStateCommand("directory", "/data/app", "", false, false)
	if err != nil {
		t.Fatalf("buildFileStateCommand() error = %v", err)
	}
	if !strings.Contains(cmd, "mkdir -p") || !strings.Contains(cmd, "__go_ansible_changed__") {
		t.Fatalf("unexpected directory command: %q", cmd)
	}

	cmd, err = buildFileStateCommand("link", "/tmp/current", "/tmp/releases/v1", false, true)
	if err != nil {
		t.Fatalf("buildFileStateCommand(link) error = %v", err)
	}
	if !strings.Contains(cmd, "ln -snf") || !strings.Contains(cmd, "readlink") {
		t.Fatalf("unexpected link command: %q", cmd)
	}

	cmd, err = buildFileStateCommand("link", "/tmp/current", "/tmp/releases/v1", true, true)
	if err != nil {
		t.Fatalf("buildFileStateCommand(link backup) error = %v", err)
	}
	if !strings.Contains(cmd, "cp -a") {
		t.Fatalf("expected backup command, got %q", cmd)
	}
}

func TestBuildFileStateCommandCheckModeDoesNotMutate(t *testing.T) {
	cmd, err := buildFileStateCommand("directory", "/data/app", "", false, false, true)
	if err != nil {
		t.Fatalf("buildFileStateCommand(check directory) error = %v", err)
	}
	if strings.Contains(cmd, "mkdir") {
		t.Fatalf("check mode directory command should not mutate, got %q", cmd)
	}
	if !strings.Contains(cmd, changeMarkerTrue) || !strings.Contains(cmd, changeMarkerFalse) {
		t.Fatalf("check mode directory command should report markers, got %q", cmd)
	}

	cmd, err = buildFileStateCommand("absent", "/data/app", "", true, false, true)
	if err != nil {
		t.Fatalf("buildFileStateCommand(check absent) error = %v", err)
	}
	if strings.Contains(cmd, "rm -rf") || strings.Contains(cmd, "cp -a") {
		t.Fatalf("check mode absent command should not mutate, got %q", cmd)
	}
}

func TestFileStateChanged(t *testing.T) {
	if !fileStateChanged("touch", "") {
		t.Fatal("expected touch to be treated as changed")
	}
	if fileStateChanged("directory", "__go_ansible_changed__=false") {
		t.Fatal("expected directory false marker to be unchanged")
	}
	if !fileStateChanged("directory", "__go_ansible_changed__=true") {
		t.Fatal("expected directory true marker to be changed")
	}
}

func TestParseStatOutput(t *testing.T) {
	stat := parseStatOutput(`exists: true
stat:
  isdir: false
  isfile: true
  size: 42
  mode: 644
  checksum: abc123
`, "/tmp/app.conf")

	if stat["exists"] != true || stat["isfile"] != true || stat["isdir"] != false {
		t.Fatalf("unexpected boolean stat fields: %#v", stat)
	}
	if stat["size"] != int64(42) || stat["mode"] != "644" || stat["checksum"] != "abc123" || stat["path"] != "/tmp/app.conf" {
		t.Fatalf("unexpected stat fields: %#v", stat)
	}

	missing := parseStatOutput("exists: false\n", "/tmp/missing")
	if missing["exists"] != false || missing["isfile"] != false || missing["isdir"] != false {
		t.Fatalf("unexpected missing stat fields: %#v", missing)
	}
}

func TestTransformLineinfileContentPresentAndAbsent(t *testing.T) {
	updated, changed, err := transformLineinfileContent("alpha\nbeta\n", lineinfileSpec{
		State: "present",
		Line:  "gamma",
	})
	if err != nil {
		t.Fatalf("transformLineinfileContent(present append) error = %v", err)
	}
	if !changed || updated != "alpha\nbeta\ngamma\n" {
		t.Fatalf("unexpected append result: changed=%v updated=%q", changed, updated)
	}

	updated, changed, err = transformLineinfileContent("alpha\nbeta\ngamma\n", lineinfileSpec{
		State: "present",
		Line:  "gamma",
	})
	if err != nil {
		t.Fatalf("transformLineinfileContent(present noop) error = %v", err)
	}
	if changed || updated != "alpha\nbeta\ngamma\n" {
		t.Fatalf("unexpected noop result: changed=%v updated=%q", changed, updated)
	}

	updated, changed, err = transformLineinfileContent("alpha\nbeta\ngamma\n", lineinfileSpec{
		State: "absent",
		Line:  "beta",
	})
	if err != nil {
		t.Fatalf("transformLineinfileContent(absent) error = %v", err)
	}
	if !changed || updated != "alpha\ngamma\n" {
		t.Fatalf("unexpected absent result: changed=%v updated=%q", changed, updated)
	}
}

func TestTransformLineinfileContentRegexpAndInsertions(t *testing.T) {
	updated, changed, err := transformLineinfileContent("name=elite\nversion=1\n", lineinfileSpec{
		State:  "present",
		Line:   "version=2",
		Regexp: `^version=`,
	})
	if err != nil {
		t.Fatalf("transformLineinfileContent(regexp replace) error = %v", err)
	}
	if !changed || updated != "name=elite\nversion=2\n" {
		t.Fatalf("unexpected regexp replace result: changed=%v updated=%q", changed, updated)
	}

	updated, changed, err = transformLineinfileContent("a\nc\n", lineinfileSpec{
		State:        "present",
		Line:         "b",
		InsertBefore: "^c$",
	})
	if err != nil {
		t.Fatalf("transformLineinfileContent(insertbefore) error = %v", err)
	}
	if !changed || updated != "a\nb\nc\n" {
		t.Fatalf("unexpected insertbefore result: changed=%v updated=%q", changed, updated)
	}

	updated, changed, err = transformLineinfileContent("a\nc\n", lineinfileSpec{
		State:       "present",
		Line:        "b",
		InsertAfter: "^a$",
	})
	if err != nil {
		t.Fatalf("transformLineinfileContent(insertafter) error = %v", err)
	}
	if !changed || updated != "a\nb\nc\n" {
		t.Fatalf("unexpected insertafter result: changed=%v updated=%q", changed, updated)
	}

	updated, changed, err = transformLineinfileContent("a\nc\n", lineinfileSpec{
		State:        "present",
		Line:         "b",
		InsertBefore: "BOF",
	})
	if err != nil {
		t.Fatalf("transformLineinfileContent(insertbefore=BOF) error = %v", err)
	}
	if !changed || updated != "b\na\nc\n" {
		t.Fatalf("unexpected insertbefore=BOF result: changed=%v updated=%q", changed, updated)
	}

	updated, changed, err = transformLineinfileContent("a\nc\n", lineinfileSpec{
		State:       "present",
		Line:        "b",
		InsertAfter: "EOF",
	})
	if err != nil {
		t.Fatalf("transformLineinfileContent(insertafter=EOF) error = %v", err)
	}
	if !changed || updated != "a\nc\nb\n" {
		t.Fatalf("unexpected insertafter=EOF result: changed=%v updated=%q", changed, updated)
	}
}

func TestTransformLineinfileContentFirstMatchAndBackrefs(t *testing.T) {
	updated, changed, err := transformLineinfileContent("version=1\nversion=2\n", lineinfileSpec{
		State:  "present",
		Line:   "version=3",
		Regexp: `^version=`,
	})
	if err != nil {
		t.Fatalf("transformLineinfileContent(default last match) error = %v", err)
	}
	if !changed || updated != "version=3\n" {
		t.Fatalf("unexpected default last-match result: changed=%v updated=%q", changed, updated)
	}

	updated, changed, err = transformLineinfileContent("version=1\nversion=2\n", lineinfileSpec{
		State:      "present",
		Line:       "version=3",
		Regexp:     `^version=`,
		FirstMatch: true,
	})
	if err != nil {
		t.Fatalf("transformLineinfileContent(firstmatch) error = %v", err)
	}
	if !changed || updated != "version=3\n" {
		t.Fatalf("unexpected firstmatch result: changed=%v updated=%q", changed, updated)
	}

	updated, changed, err = transformLineinfileContent("listen 80;\n", lineinfileSpec{
		State:    "present",
		Line:     "listen $1 default_server;",
		Regexp:   `^listen ([0-9]+);$`,
		Backrefs: true,
	})
	if err != nil {
		t.Fatalf("transformLineinfileContent(backrefs) error = %v", err)
	}
	if !changed || updated != "listen 80 default_server;\n" {
		t.Fatalf("unexpected backrefs result: changed=%v updated=%q", changed, updated)
	}

	updated, changed, err = transformLineinfileContent("server_name app;\n", lineinfileSpec{
		State:    "present",
		Line:     "listen $1 default_server;",
		Regexp:   `^listen ([0-9]+);$`,
		Backrefs: true,
	})
	if err != nil {
		t.Fatalf("transformLineinfileContent(backrefs no match) error = %v", err)
	}
	if changed || updated != "server_name app;\n" {
		t.Fatalf("unexpected backrefs no-match result: changed=%v updated=%q", changed, updated)
	}
}

func TestTransformLineinfileContentSearchString(t *testing.T) {
	updated, changed, err := transformLineinfileContent("option=old\noption=keep\n", lineinfileSpec{
		State:        "present",
		Line:         "option=new",
		SearchString: "option=",
	})
	if err != nil {
		t.Fatalf("transformLineinfileContent(search_string present) error = %v", err)
	}
	if !changed || updated != "option=new\n" {
		t.Fatalf("unexpected search_string present result: changed=%v updated=%q", changed, updated)
	}

	updated, changed, err = transformLineinfileContent("debug=true\ndebug=false\n", lineinfileSpec{
		State:        "absent",
		SearchString: "debug=",
	})
	if err != nil {
		t.Fatalf("transformLineinfileContent(search_string absent) error = %v", err)
	}
	if !changed || updated != "" {
		t.Fatalf("unexpected search_string absent result: changed=%v updated=%q", changed, updated)
	}
}

func TestTransformLineinfileContentCreateEdgeCases(t *testing.T) {
	updated, changed, err := transformLineinfileContent("", lineinfileSpec{
		State: "present",
		Line:  "option=value",
	})
	if err != nil {
		t.Fatalf("transformLineinfileContent(create present) error = %v", err)
	}
	if !changed || updated != "option=value\n" {
		t.Fatalf("unexpected create present result: changed=%v updated=%q", changed, updated)
	}

	updated, changed, err = transformLineinfileContent("", lineinfileSpec{
		State: "absent",
		Line:  "option=value",
	})
	if err != nil {
		t.Fatalf("transformLineinfileContent(create absent) error = %v", err)
	}
	if changed || updated != "" {
		t.Fatalf("unexpected create absent result: changed=%v updated=%q", changed, updated)
	}
}

func TestLineinfileValidateSearchStringConstraints(t *testing.T) {
	mod := &LineinfileModule{}

	if err := mod.Validate(map[string]interface{}{
		"path":          "/tmp/app.conf",
		"search_string": "option=",
		"regexp":        "^option=",
		"line":          "option=new",
	}); err == nil {
		t.Fatal("expected regexp and search_string conflict")
	}

	if err := mod.Validate(map[string]interface{}{
		"path":          "/tmp/app.conf",
		"search_string": "listen ",
		"backrefs":      true,
		"line":          "listen $1 default_server;",
	}); err == nil {
		t.Fatal("expected backrefs with search_string to fail")
	}
}

func TestLineinfileResultDataShape(t *testing.T) {
	data := map[string]interface{}{
		"path":     "/tmp/app.conf",
		"matched":  true,
		"count":    1,
		"action":   "replace",
		"created":  false,
		"encoding": "utf-8",
		"newline":  "lf",
		"before":   "option=old\n",
		"after":    "option=new\n",
	}
	if data["path"] != "/tmp/app.conf" || data["matched"] != true || data["count"] != 1 || data["action"] != "replace" || data["encoding"] != "utf-8" || data["newline"] != "lf" {
		t.Fatalf("unexpected lineinfile result data: %#v", data)
	}
}

func TestBuildUserCommands(t *testing.T) {
	cmd, passwordCmd, changed, err := buildUserCommands(userCommandSpec{
		Name:     "deploy",
		State:    "present",
		Shell:    "/bin/bash",
		Password: "$6$hash",
		Home:     "/home/deploy",
		Expires:  "2026-12-31",
	}, true)
	if err != nil {
		t.Fatalf("buildUserCommands() error = %v", err)
	}
	if !changed {
		t.Fatal("expected existing user update to be marked changed")
	}
	if !strings.Contains(cmd, "usermod") || !strings.Contains(cmd, "-s '/bin/bash'") {
		t.Fatalf("unexpected usermod command: %q", cmd)
	}
	if !strings.Contains(cmd, "-d '/home/deploy'") || !strings.Contains(cmd, "-e '2026-12-31'") {
		t.Fatalf("expected home and expires flags, got %q", cmd)
	}
	if !strings.Contains(passwordCmd, "usermod -p '$6$hash'") {
		t.Fatalf("unexpected password command: %q", passwordCmd)
	}

	cmd, passwordCmd, changed, err = buildUserCommands(userCommandSpec{
		Name:  "deploy",
		State: "absent",
	}, false)
	if err != nil {
		t.Fatalf("buildUserCommands(absent) error = %v", err)
	}
	if changed || cmd != "" || passwordCmd != "" {
		t.Fatalf("expected absent missing user to be noop, got cmd=%q passwordCmd=%q changed=%v", cmd, passwordCmd, changed)
	}

	cmd, _, changed, err = buildUserCommands(userCommandSpec{
		Name:  "deploy",
		State: "locked",
	}, true)
	if err != nil {
		t.Fatalf("buildUserCommands(locked) error = %v", err)
	}
	if !changed || !strings.Contains(cmd, "usermod -L") {
		t.Fatalf("unexpected locked command: %q", cmd)
	}

	cmd, _, changed, err = buildUserCommands(userCommandSpec{
		Name:       "deploy",
		State:      "present",
		CreateHome: true,
		Home:       "/srv/deploy",
		MoveHome:   true,
	}, false)
	if err != nil {
		t.Fatalf("buildUserCommands(create) error = %v", err)
	}
	if !changed || !strings.Contains(cmd, "useradd") || !strings.Contains(cmd, "-m") || !strings.Contains(cmd, "-d '/srv/deploy'") {
		t.Fatalf("unexpected useradd command: %q", cmd)
	}
}

func TestBuildGroupCommand(t *testing.T) {
	cmd, changed, err := buildGroupCommand(groupCommandSpec{
		Name:  "deploy",
		State: "absent",
	}, false)
	if err != nil {
		t.Fatalf("buildGroupCommand() error = %v", err)
	}
	if changed || cmd != "" {
		t.Fatalf("expected noop for missing group, got cmd=%q changed=%v", cmd, changed)
	}
}

func TestPackageChangedDetection(t *testing.T) {
	if yumChanged("present", "Nothing to do", "") {
		t.Fatal("expected yum no-op output to be unchanged")
	}
	if !yumChanged("present", "Installed:\n  nginx", "") {
		t.Fatal("expected yum install output to be changed")
	}
	if aptChanged("present", "nginx is already the newest version", "") {
		t.Fatal("expected apt newest-version output to be unchanged")
	}
	if !aptChanged("absent", "Removing nginx", "") {
		t.Fatal("expected apt remove output to be changed")
	}
}

func TestNormalizePackageState(t *testing.T) {
	state, err := normalizePackageState("apt", "purge")
	if err != nil || state != "purged" {
		t.Fatalf("expected apt purge -> purged, got state=%q err=%v", state, err)
	}

	state, err = normalizePackageState("yum", "deleted")
	if err != nil || state != "absent" {
		t.Fatalf("expected yum deleted -> absent, got state=%q err=%v", state, err)
	}

	state, err = normalizePackageState("yum", "upgrade")
	if err != nil || state != "latest" {
		t.Fatalf("expected yum upgrade -> latest, got state=%q err=%v", state, err)
	}
}

func TestBuildServiceStateCommand(t *testing.T) {
	cmd, changed, err := buildServiceStateCommand(true, "nginx", "started")
	if err != nil {
		t.Fatalf("buildServiceStateCommand() error = %v", err)
	}
	if changed {
		t.Fatal("expected started command to rely on marker-based change detection")
	}
	if !strings.Contains(cmd, "systemctl is-active") || !strings.Contains(cmd, changeMarkerTrue) {
		t.Fatalf("unexpected systemd start command: %q", cmd)
	}

	cmd, changed, err = buildServiceStateCommand(false, "nginx", "restarted")
	if err != nil {
		t.Fatalf("buildServiceStateCommand(restarted) error = %v", err)
	}
	if !changed || !strings.Contains(cmd, "service 'nginx' restart") {
		t.Fatalf("unexpected restart command: %q", cmd)
	}
}

func TestBuildServiceEnabledCommand(t *testing.T) {
	cmd := buildServiceEnabledCommand(true, "nginx", true)
	if !strings.Contains(cmd, "systemctl is-enabled") || !strings.Contains(cmd, "systemctl enable") {
		t.Fatalf("unexpected enabled command: %q", cmd)
	}

	cmd = buildServiceEnabledCommand(true, "nginx", false)
	if !strings.Contains(cmd, "systemctl disable") || !strings.Contains(cmd, changeMarkerFalse) {
		t.Fatalf("unexpected disable command: %q", cmd)
	}

	cmd = buildServiceEnabledCommand(false, "nginx", true)
	if !strings.Contains(cmd, "chkconfig --list") || !strings.Contains(cmd, "update-rc.d") || !strings.Contains(cmd, changeMarkerTrue) {
		t.Fatalf("unexpected sysv enabled command: %q", cmd)
	}
}

func TestBuildSystemdMaskedCommand(t *testing.T) {
	cmd := buildSystemdMaskedCommand("nginx", true)
	if !strings.Contains(cmd, "systemctl mask") || !strings.Contains(cmd, "grep -qx masked") || !strings.Contains(cmd, changeMarkerTrue) {
		t.Fatalf("unexpected masked command: %q", cmd)
	}

	cmd = buildSystemdMaskedCommand("nginx", false)
	if !strings.Contains(cmd, "systemctl unmask") || !strings.Contains(cmd, changeMarkerFalse) {
		t.Fatalf("unexpected unmasked command: %q", cmd)
	}
}

func TestParseChangedMarker(t *testing.T) {
	cleaned, changed, found := parseChangedMarker("ok\n" + changeMarkerTrue)
	if !found || !changed || cleaned != "ok" {
		t.Fatalf("unexpected parse result: cleaned=%q changed=%v found=%v", cleaned, changed, found)
	}
}

func TestSystemdValidate(t *testing.T) {
	mod := &SystemdModule{}

	if err := mod.Validate(map[string]interface{}{}); err == nil {
		t.Fatal("expected validation error for missing name and daemon flags")
	}
	if err := mod.Validate(map[string]interface{}{"daemon_reload": true}); err != nil {
		t.Fatalf("expected daemon_reload-only validate success, got %v", err)
	}
	if err := mod.Validate(map[string]interface{}{"name": "nginx", "state": "started"}); err != nil {
		t.Fatalf("expected named state validate success, got %v", err)
	}
	if err := mod.Validate(map[string]interface{}{"enabled": true}); err == nil {
		t.Fatal("expected enabled without name to fail")
	}
}

func TestRenderTemplateContent(t *testing.T) {
	tmpl, err := template.New("app").Funcs(templateFuncMap()).Parse("name={{ .name }} port={{ .port }}")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	rendered, err := renderTemplateContent(tmpl, map[string]interface{}{
		"name": "elite",
		"port": 8080,
	})
	if err != nil {
		t.Fatalf("renderTemplateContent() error = %v", err)
	}
	if rendered != "name=elite port=8080" {
		t.Fatalf("unexpected rendered content: %q", rendered)
	}
}

func TestTemplateFuncMapFilters(t *testing.T) {
	tmpl, err := template.New("app").Funcs(templateFuncMap()).Parse(`env={{ .env | default "prod" | upper }} tags={{ .tags | join "," }} host={{ .host | quote }} msg={{ .msg | trim | replace "foo" "bar" }} count={{ .tags | length }} first={{ .tags | first }} last={{ .tags | last }} parts={{ .csv | split "," | join "|" }} json={{ .data | to_json }} base={{ .path | basename }} dir={{ .path | dirname }} enc={{ .secret | b64encode }} dec={{ .encoded | b64decode }}`)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	rendered, err := renderTemplateContent(tmpl, map[string]interface{}{
		"tags":    []interface{}{"api", "green"},
		"host":    "web 01",
		"msg":     "  foo-ready  ",
		"csv":     "a,b,c",
		"data":    map[string]interface{}{"name": "api"},
		"path":    "/tmp/app/config.yml",
		"secret":  "hello",
		"encoded": "d29ybGQ=",
	})
	if err != nil {
		t.Fatalf("renderTemplateContent(filters) error = %v", err)
	}

	expected := `env=PROD tags=api,green host='web 01' msg=bar-ready count=2 first=api last=green parts=a|b|c json={"name":"api"} base=config.yml dir=/tmp/app enc=aGVsbG8= dec=world`
	if rendered != expected {
		t.Fatalf("unexpected rendered content: %q", rendered)
	}
}

func TestPreprocessJinjaTemplate(t *testing.T) {
	source := `{# ignored #}
name={{ app_name | default('demo') | upper }}
{% if enabled %}
enabled=yes
{% else %}
enabled=no
{% endif %}
{% for port in ports %}
port={{ port }}
{% endfor %}
`

	tmpl, err := template.New("jinja").Funcs(templateFuncMap()).Parse(preprocessJinjaTemplate(source))
	if err != nil {
		t.Fatalf("Parse(preprocessed jinja) error = %v", err)
	}
	rendered, err := renderTemplateContent(tmpl, map[string]interface{}{
		"enabled": true,
		"ports":   []interface{}{8080, 9090},
	})
	if err != nil {
		t.Fatalf("renderTemplateContent(jinja) error = %v", err)
	}

	expected := `
name=DEMO

enabled=yes


port=8080

port=9090

`
	if rendered != expected {
		t.Fatalf("unexpected jinja rendered content:\n%q", rendered)
	}
}

func TestPreprocessJinjaHostVarsAndBracketAccess(t *testing.T) {
	source := `db_ip={{ hostvars['db-01']['ansible_host'] }}
db_port={{ hostvars['db-01'].port }}
first_web={{ groups['web'][0] }}
`
	tmpl, err := template.New("jinja").Funcs(templateFuncMap()).Parse(preprocessJinjaTemplate(source))
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	rendered, err := renderTemplateContent(tmpl, map[string]interface{}{
		"hostvars": map[string]map[string]interface{}{
			"db-01": {
				"ansible_host": "192.168.1.50",
				"port":         5432,
			},
		},
		"groups": map[string][]string{
			"web": {"web-01", "web-02"},
		},
	})
	if err != nil {
		t.Fatalf("render error = %v", err)
	}
	expected := "db_ip=192.168.1.50\ndb_port=5432\nfirst_web=web-01\n"
	if rendered != expected {
		t.Fatalf("expected:\n%q\ngot:\n%q", expected, rendered)
	}
}

func TestBuildWaitForCondition(t *testing.T) {
	condition, successMessage, timeoutMessage, err := buildWaitForCondition(waitForSpec{
		Path:        "/tmp/app.log",
		SearchRegex: "ready",
		State:       "present",
	})
	if err != nil {
		t.Fatalf("buildWaitForCondition(path) error = %v", err)
	}
	if !strings.Contains(condition, "grep -qE") || !strings.Contains(successMessage, "matching ready") || !strings.Contains(timeoutMessage, "/tmp/app.log") {
		t.Fatalf("unexpected path wait_for condition: %q %q %q", condition, successMessage, timeoutMessage)
	}

	condition, _, _, err = buildWaitForCondition(waitForSpec{
		Host:  "127.0.0.1",
		Port:  "8080",
		State: "absent",
	})
	if err != nil {
		t.Fatalf("buildWaitForCondition(port) error = %v", err)
	}
	if !strings.Contains(condition, "! echo > /dev/tcp/127.0.0.1/8080") {
		t.Fatalf("unexpected port wait_for condition: %q", condition)
	}
}

func TestExpandedTemplateFilters(t *testing.T) {
	// Test combine
	m1 := map[string]interface{}{"a": 1, "nested": map[string]interface{}{"x": 10}}
	m2 := map[string]interface{}{"b": 2, "nested": map[string]interface{}{"y": 20}}
	comb, err := TemplateCombine(m1, m2, "recursive=true")
	if err != nil {
		t.Fatalf("combine error: %v", err)
	}
	if comb["a"] != 1 || comb["b"] != 2 {
		t.Fatalf("combine top-level mismatch: %#v", comb)
	}
	nested, _ := comb["nested"].(map[string]interface{})
	if nested["x"] != 10 || nested["y"] != 20 {
		t.Fatalf("recursive combine mismatch: %#v", nested)
	}

	// Test regex filters
	replaced := TemplateRegexReplace(`\d+`, "NUM", "item 123 and 456")
	if replaced != "item NUM and NUM" {
		t.Fatalf("regex_replace: %q", replaced)
	}
	searched := TemplateRegexSearch(`[a-z]+`, "123 abc 456")
	if searched != "abc" {
		t.Fatalf("regex_search: %q", searched)
	}
	allMatches := TemplateRegexFindAll(`\d+`, "a 1 b 22 c 333")
	if len(allMatches) != 3 || allMatches[1] != "22" {
		t.Fatalf("regex_findall: %#v", allMatches)
	}

	// Test selectattr, rejectattr, map
	servers := []interface{}{
		map[string]interface{}{"name": "web1", "active": true, "env": "prod"},
		map[string]interface{}{"name": "web2", "active": false, "env": "dev"},
		map[string]interface{}{"name": "db1", "active": true, "env": "prod"},
	}
	activeServers := TemplateSelectAttr("active", "equalto", true, servers)
	if len(activeServers) != 2 {
		t.Fatalf("selectattr equalto: %#v", activeServers)
	}
	devServers := TemplateRejectAttr("env", "equalto", "prod", servers)
	if len(devServers) != 1 {
		t.Fatalf("rejectattr: %#v", devServers)
	}
	names := TemplateMap("attribute", "name", servers)
	if len(names) != 3 || names[0] != "web1" {
		t.Fatalf("map attribute: %#v", names)
	}
	upperNames := TemplateMap("upper", []interface{}{"apple", "banana"})
	if upperNames[0] != "APPLE" || upperNames[1] != "BANANA" {
		t.Fatalf("map upper: %#v", upperNames)
	}

	// Test ipaddr
	if !TemplateIpAddr("ipv4", "192.168.1.1").(bool) {
		t.Fatal("expected 192.168.1.1 to be ipv4")
	}
	if TemplateIpAddr("ipv4", "2001:db8::1").(bool) {
		t.Fatal("expected 2001:db8::1 not to be ipv4")
	}
	if TemplateIpAddr("netmask", "10.0.0.0/24") != "255.255.255.0" {
		t.Fatalf("expected 255.255.255.0, got %v", TemplateIpAddr("netmask", "10.0.0.0/24"))
	}

	// Test list operations: flatten, unique, difference, intersect, union
	flat := TemplateFlatten([]interface{}{[]interface{}{1, 2}, []interface{}{3, []interface{}{4, 5}}})
	if len(flat) != 5 || flat[4] != 5 {
		t.Fatalf("flatten: %#v", flat)
	}
	uniq := TemplateUnique([]interface{}{1, 2, 2, 3, 1})
	if len(uniq) != 3 {
		t.Fatalf("unique: %#v", uniq)
	}
	diff := TemplateDifference([]interface{}{2, 3}, []interface{}{1, 2, 3, 4})
	if len(diff) != 2 || diff[0] != 1 || diff[1] != 4 {
		t.Fatalf("difference: %#v", diff)
	}
	inter := TemplateIntersect([]interface{}{2, 3, 5}, []interface{}{1, 2, 3, 4})
	if len(inter) != 2 || inter[0] != 2 || inter[1] != 3 {
		t.Fatalf("intersect: %#v", inter)
	}
	uni := TemplateUnion([]interface{}{3, 4, 5}, []interface{}{1, 2, 3})
	if len(uni) != 5 {
		t.Fatalf("union: %#v", uni)
	}

	// Test math: min, max, sum, abs
	if TemplateMin([]interface{}{5, 2, 9, 1}) != 1 {
		t.Fatalf("min: %v", TemplateMin([]interface{}{5, 2, 9, 1}))
	}
	if TemplateMax([]interface{}{5, 2, 9, 1}) != 9 {
		t.Fatalf("max: %v", TemplateMax([]interface{}{5, 2, 9, 1}))
	}
	if TemplateSum([]interface{}{1, 2, 3, 4}) != 10 {
		t.Fatalf("sum: %v", TemplateSum([]interface{}{1, 2, 3, 4}))
	}
	if TemplateAbs(-42) != 42 {
		t.Fatalf("abs: %v", TemplateAbs(-42))
	}

	// Test type conversions: bool, int, float
	if !TemplateBool("yes") || TemplateBool("no") {
		t.Fatal("bool conversion error")
	}
	if TemplateInt(10, "42") != 42 || TemplateInt(10, "invalid") != 10 {
		t.Fatal("int conversion error")
	}
	if TemplateFloat(1.5, "3.14") != 3.14 {
		t.Fatal("float conversion error")
	}

	// Test hash
	md5Hash := TemplateHash("md5", "hello")
	if md5Hash != "5d41402abc4b2a76b9719d911017c592" {
		t.Fatalf("md5 hash: %s", md5Hash)
	}
	sha256Hash := TemplateHash("sha256", "hello")
	if sha256Hash != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Fatalf("sha256 hash: %s", sha256Hash)
	}
}
