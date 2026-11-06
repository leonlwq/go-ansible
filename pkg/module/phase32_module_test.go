package module

import (
	"strings"
	"testing"
)

func TestPhase32_GetParamGenericNumericFallbacks(t *testing.T) {
	params := map[string]interface{}{
		"depth_float":   float64(5),
		"depth_int64":   int64(10),
		"depth_string":  "15",
		"timeout_float": float64(60),
		"enabled_str":   "yes",
	}

	// 1. 测试泛型 GetParam[int] 能够兜底类型断言
	if val := GetParam[int](params, "depth_float", 0); val != 5 {
		t.Fatalf("expected 5 for depth_float, got %d", val)
	}
	if val := GetParam[int](params, "depth_int64", 0); val != 10 {
		t.Fatalf("expected 10 for depth_int64, got %d", val)
	}
	if val := GetParam[int](params, "depth_string", 0); val != 15 {
		t.Fatalf("expected 15 for depth_string, got %d", val)
	}

	// 2. 测试 GetParamInt 正常解析
	if val := GetParamInt(params, "timeout_float", 30); val != 60 {
		t.Fatalf("expected 60 for timeout_float, got %d", val)
	}

	// 3. 测试泛型 GetParam[bool] 兜底
	if val := GetParam[bool](params, "enabled_str", false); !val {
		t.Fatalf("expected true for enabled_str, got false")
	}
}

func TestPhase32_GetURLBareHashChecksumAutoDetection(t *testing.T) {
	tests := []struct {
		name     string
		spec     string
		wantAlgo string
		wantHash string
		wantErr  bool
	}{
		{
			name:     "Explicit sha256 prefix",
			spec:     "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			wantAlgo: "sha256",
			wantHash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			wantErr:  false,
		},
		{
			name:     "Bare md5 (32 hex chars)",
			spec:     "d41d8cd98f00b204e9800998ecf8427e",
			wantAlgo: "md5",
			wantHash: "d41d8cd98f00b204e9800998ecf8427e",
			wantErr:  false,
		},
		{
			name:     "Bare sha1 (40 hex chars)",
			spec:     "da39a3ee5e6b4b0d3255bfef95601890afd80709",
			wantAlgo: "sha1",
			wantHash: "da39a3ee5e6b4b0d3255bfef95601890afd80709",
			wantErr:  false,
		},
		{
			name:     "Bare sha224 (56 hex chars)",
			spec:     "d14a028c2a3a2bc9476102bb288234c415a2b01f828ea62ac5b3e42f",
			wantAlgo: "sha224",
			wantHash: "d14a028c2a3a2bc9476102bb288234c415a2b01f828ea62ac5b3e42f",
			wantErr:  false,
		},
		{
			name:     "Bare sha256 (64 hex chars)",
			spec:     "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			wantAlgo: "sha256",
			wantHash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			wantErr:  false,
		},
		{
			name:     "Bare sha384 (96 hex chars)",
			spec:     "38b060a751ac96384cd9327eb1b1e36a21fdb71114be07434c0cc7bf63f6e1da274edebfe76f65fbd51ad2f14898b95b",
			wantAlgo: "sha384",
			wantHash: "38b060a751ac96384cd9327eb1b1e36a21fdb71114be07434c0cc7bf63f6e1da274edebfe76f65fbd51ad2f14898b95b",
			wantErr:  false,
		},
		{
			name:     "Bare sha512 (128 hex chars)",
			spec:     "cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e",
			wantAlgo: "sha512",
			wantHash: "cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e",
			wantErr:  false,
		},
		{
			name:    "Invalid length bare string",
			spec:    "abcd1234ef",
			wantErr: true,
		},
		{
			name:    "Invalid non-hex chars",
			spec:    "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			algo, hash, err := parseChecksumSpec(tt.spec)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseChecksumSpec(%q) error = %v, wantErr %v", tt.spec, err, tt.wantErr)
			}
			if !tt.wantErr {
				if algo != tt.wantAlgo {
					t.Errorf("algo = %q, want %q", algo, tt.wantAlgo)
				}
				if hash != tt.wantHash {
					t.Errorf("hash = %q, want %q", hash, tt.wantHash)
				}
			}
		})
	}
}

func TestPhase32_LineinfilePresentMultiMatchPreservation(t *testing.T) {
	initialContent := `# configuration
port = 80
# secondary
port = 8080
# end
`
	lines := strings.Split(strings.TrimRight(initialContent, "\n"), "\n")

	// 1. 默认替换最后一个匹配，严禁删除其他匹配行
	updated, changed, err := ensureLinePresent(lines, lineinfileSpec{
		State:  "present",
		Regexp: `^port\s*=`,
		Line:   "port = 443",
	})
	if err != nil {
		t.Fatalf("ensureLinePresent failed: %v", err)
	}
	if !changed {
		t.Fatalf("expected changed=true")
	}

	// 必须保留 5 行：首个 port = 80 必须保留，末尾 port = 8080 变为 port = 443
	if len(updated) != 5 {
		t.Fatalf("expected 5 lines preserved, got %d lines: %v", len(updated), updated)
	}
	if updated[1] != "port = 80" {
		t.Errorf("expected line 1 to be 'port = 80', got %q", updated[1])
	}
	if updated[3] != "port = 443" {
		t.Errorf("expected line 3 to be 'port = 443', got %q", updated[3])
	}

	// 2. firstmatch=true 替换第一个匹配，严禁删除后续匹配行
	updatedFirst, changedFirst, err := ensureLinePresent(lines, lineinfileSpec{
		State:      "present",
		Regexp:     `^port\s*=`,
		Line:       "port = 8443",
		FirstMatch: true,
	})
	if err != nil {
		t.Fatalf("ensureLinePresent firstmatch failed: %v", err)
	}
	if !changedFirst {
		t.Fatalf("expected changedFirst=true")
	}
	if len(updatedFirst) != 5 {
		t.Fatalf("expected 5 lines preserved, got %d lines: %v", len(updatedFirst), updatedFirst)
	}
	if updatedFirst[1] != "port = 8443" {
		t.Errorf("expected line 1 to be 'port = 8443', got %q", updatedFirst[1])
	}
	if updatedFirst[3] != "port = 8080" {
		t.Errorf("expected line 3 to remain 'port = 8080', got %q", updatedFirst[3])
	}
}

func TestPhase32_CronSpecialTimeWhitespaceNormalization(t *testing.T) {
	// 验证带多空格的 @reboot 周期行具备幂等性
	existing := `@reboot   /usr/local/bin/backup.sh`
	newContent, changed := updateCrontabContent(existing, "", "/usr/local/bin/backup.sh", "@reboot /usr/local/bin/backup.sh", "present", false)
	if changed {
		t.Fatalf("expected idempotent match for whitespace differences in cron line, got changed=true, newContent=%q", newContent)
	}
}

