package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-ansible/pkg/config"
	"go-ansible/pkg/playbook"
)

func TestPhase30_GroupVarsDirectoryPrecedence(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Inventory directory with group_vars/all.yml
	invDir := filepath.Join(tempDir, "inventory")
	invGroupVarsDir := filepath.Join(invDir, "group_vars")
	if err := os.MkdirAll(invGroupVarsDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	invFile := filepath.Join(invDir, "hosts.ini")
	if err := os.WriteFile(invFile, []byte("[all]\nserver1\n"), 0644); err != nil {
		t.Fatalf("write invFile failed: %v", err)
	}
	invVars := `
app_port: 8080
cluster_name: my-cluster
`
	if err := os.WriteFile(filepath.Join(invGroupVarsDir, "all.yml"), []byte(invVars), 0644); err != nil {
		t.Fatalf("write inv group_vars failed: %v", err)
	}

	// 2. Playbook directory with group_vars/all.yml
	pbDir := filepath.Join(tempDir, "playbooks")
	pbGroupVarsDir := filepath.Join(pbDir, "group_vars")
	if err := os.MkdirAll(pbGroupVarsDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	pbVars := `
app_port: 9090
`
	if err := os.WriteFile(filepath.Join(pbGroupVarsDir, "all.yml"), []byte(pbVars), 0644); err != nil {
		t.Fatalf("write pb group_vars failed: %v", err)
	}

	opts := &CommonCLIOptions{
		InventoryPaths: []string{invFile},
	}

	// Pass pbDir as extraVarsDirs
	env, err := opts.SetupExecutionEnv(&config.Config{}, pbDir)
	if err != nil {
		t.Fatalf("SetupExecutionEnv failed: %v", err)
	}

	// In the resulting inventory variables:
	// cluster_name should come from inventory group_vars
	if env.Inventory.Variables["cluster_name"] != "my-cluster" {
		t.Errorf("expected cluster_name to be 'my-cluster', got %q", env.Inventory.Variables["cluster_name"])
	}
	// app_port should be overridden by playbook group_vars (9090 > 8080)
	if env.Inventory.Variables["app_port"] != "9090" {
		t.Errorf("expected app_port to be '9090' (playbook precedence), got %q", env.Inventory.Variables["app_port"])
	}
}

func TestPhase30_RetryFileGenerationLogic(t *testing.T) {
	tempDir := t.TempDir()
	pbFile := filepath.Join(tempDir, "test_play.yml")

	// Create PlaybookResult:
	// Host 1: Has a failed task that is rescued (Rescued == true). Should NOT be in retry list.
	// Host 2: Has an unreachable task. SHOULD be in retry list.
	// Host 3: Has a failed task not rescued. SHOULD be in retry list.
	// Host 4: Has a failed task that is ignored. Should NOT be in retry list.
	res := &playbook.PlaybookResult{
		Plays: []*playbook.PlayResult{
			{
				Name: "play1",
				Hosts: map[string]*playbook.HostResult{
					"host_rescued": {
						Tasks: []*playbook.TaskResult{
							{Name: "task1", Host: "host_rescued", Failed: true, Rescued: true},
						},
					},
					"host_unreachable": {
						Tasks: []*playbook.TaskResult{
							{Name: "task2", Host: "host_unreachable", Unreachable: true},
						},
					},
					"host_failed": {
						Tasks: []*playbook.TaskResult{
							{Name: "task3", Host: "host_failed", Failed: true, Rescued: false},
						},
					},
					"host_ignored": {
						Tasks: []*playbook.TaskResult{
							{Name: "task4", Host: "host_ignored", Failed: true, Ignored: true},
						},
					},
				},
			},
		},
	}

	// Replicate the failed host extraction logic as in runPlaybookCommand
	var failedHosts []string
	failedSet := make(map[string]bool)
	for _, playRes := range res.Plays {
		for hostName, hostRes := range playRes.Hosts {
			if hostRes == nil {
				continue
			}
			for _, t := range hostRes.GetTasks() {
				if (t.Failed && !t.Rescued && !t.Ignored) || (t.Unreachable && !t.Ignored) {
					if !failedSet[hostName] {
						failedSet[hostName] = true
						failedHosts = append(failedHosts, hostName)
					}
					break
				}
			}
		}
	}

	if failedSet["host_rescued"] {
		t.Errorf("host_rescued should NOT be in failedHosts")
	}
	if failedSet["host_ignored"] {
		t.Errorf("host_ignored should NOT be in failedHosts")
	}
	if !failedSet["host_unreachable"] {
		t.Errorf("host_unreachable SHOULD be in failedHosts")
	}
	if !failedSet["host_failed"] {
		t.Errorf("host_failed SHOULD be in failedHosts")
	}

	retryPath, err := writeRetryFile(pbFile, failedHosts)
	if err != nil {
		t.Fatalf("writeRetryFile failed: %v", err)
	}
	defer os.Remove(retryPath)

	data, err := os.ReadFile(retryPath)
	if err != nil {
		t.Fatalf("failed to read retry file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "host_unreachable") {
		t.Errorf("expected retry file to contain host_unreachable")
	}
	if !strings.Contains(content, "host_failed") {
		t.Errorf("expected retry file to contain host_failed")
	}
	if strings.Contains(content, "host_rescued") {
		t.Errorf("retry file must NOT contain host_rescued")
	}
}

