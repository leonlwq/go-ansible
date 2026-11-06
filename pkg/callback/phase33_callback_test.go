package callback

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"go-ansible/pkg/playbook"
)

// TestPhase33_MultiPlayRecapDeduplication 测试多 Play 场景下 PLAY RECAP 跨 Play 汇总且每主机去重仅显示一行
func TestPhase33_MultiPlayRecapDeduplication(t *testing.T) {
	result := &playbook.PlaybookResult{
		Plays: []*playbook.PlayResult{
			{
				Name: "play 1: setup",
				Hosts: map[string]*playbook.HostResult{
					"web1.example.com": {
						Tasks: []*playbook.TaskResult{
							{Name: "task 1", Changed: true},
							{Name: "task 2"},
						},
					},
					"db1.example.com": {
						Tasks: []*playbook.TaskResult{
							{Name: "task 1"},
						},
					},
				},
			},
			{
				Name: "play 2: deploy",
				Hosts: map[string]*playbook.HostResult{
					"web1.example.com": {
						Tasks: []*playbook.TaskResult{
							{Name: "task 3", Changed: true},
						},
					},
				},
			},
		},
	}

	var buf bytes.Buffer
	renderer := &DefaultRenderer{}
	if err := renderer.Render(&buf, result, ""); err != nil {
		t.Fatalf("renderer.Render failed: %v", err)
	}

	output := buf.String()
	recapIndex := strings.Index(output, "PLAY RECAP")
	if recapIndex == -1 {
		t.Fatalf("expected PLAY RECAP section in output")
	}

	recapSection := output[recapIndex:]
	webCount := strings.Count(recapSection, "web1.example.com")
	if webCount != 1 {
		t.Fatalf("expected web1.example.com to appear exactly 1 time in PLAY RECAP, got %d times in:\n%s", webCount, recapSection)
	}

	// 验证标准 Ansible 列顺序: ok, changed, unreachable, failed, skipped, rescued, ignored
	if !strings.Contains(recapSection, "ok=1   changed=2   unreachable=0   failed=0   skipped=0   rescued=0   ignored=0") {
		t.Fatalf("expected aggregated counts (ok=1, changed=2, unreachable=0, failed=0) in recap, got:\n%s", recapSection)
	}
}

// TestPhase33_JSONCallbackTopLevelStats 测试 JSON Callback 顶层包含 stats 统计汇总字典
func TestPhase33_JSONCallbackTopLevelStats(t *testing.T) {
	result := &playbook.PlaybookResult{
		Plays: []*playbook.PlayResult{
			{
				Name: "play 1",
				Hosts: map[string]*playbook.HostResult{
					"node1": {
						Tasks: []*playbook.TaskResult{
							{Name: "task 1", Changed: true},
							{Name: "task 2"},
						},
					},
				},
			},
		},
	}

	var buf bytes.Buffer
	renderer := &JSONRenderer{}
	if err := renderer.Render(&buf, result, ""); err != nil {
		t.Fatalf("renderer.Render failed: %v", err)
	}

	var parsed struct {
		Plays []interface{} `json:"plays"`
		Stats map[string]struct {
			OK          int `json:"ok"`
			Changed     int `json:"changed"`
			Unreachable int `json:"unreachable"`
			Failures    int `json:"failures"`
			Skipped     int `json:"skipped"`
			Rescued     int `json:"rescued"`
			Ignored     int `json:"ignored"`
		} `json:"stats"`
	}

	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("unmarshal json callback output failed: %v", err)
	}

	if parsed.Stats == nil {
		t.Fatalf("expected top-level 'stats' object in JSON output")
	}

	nodeStats, exists := parsed.Stats["node1"]
	if !exists {
		t.Fatalf("expected stats for 'node1' in JSON output")
	}
	if nodeStats.OK != 1 || nodeStats.Changed != 1 || nodeStats.Failures != 0 {
		t.Fatalf("unexpected node1 stats: %+v", nodeStats)
	}
}
