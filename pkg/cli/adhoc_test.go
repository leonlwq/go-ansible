package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"go-ansible/pkg/adhoc"
)

func TestPrintAdhocHosts(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	oldStdout := os.Stdout
	os.Stdout = writer

	printAdhocHosts("web:db", "!web2", []string{"db1", "web1"})

	writer.Close()
	os.Stdout = oldStdout

	outputBytes, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	output := string(outputBytes)
	if !strings.Contains(output, "adhoc target: web:db") || !strings.Contains(output, "limit: !web2") || !strings.Contains(output, "hosts (2):") || !strings.Contains(output, "db1") || !strings.Contains(output, "web1") {
		t.Fatalf("unexpected adhoc list-hosts output: %q", output)
	}
}

func TestParseExtraVarsSupportsAdhocUseCases(t *testing.T) {
	got, err := parseExtraVars(`target=web env=prod ansible_port=2200`)
	if err != nil {
		t.Fatalf("parseExtraVars() error = %v", err)
	}
	if got["target"] != "web" {
		t.Fatalf("expected target=web, got %#v", got["target"])
	}
	if got["env"] != "prod" {
		t.Fatalf("expected env=prod, got %#v", got["env"])
	}
	if got["ansible_port"] != 2200 {
		t.Fatalf("expected ansible_port=2200, got %#v", got["ansible_port"])
	}
}

func TestAdhocResultDefaultOutputIncludesMessageDiffAndData(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	oldStdout := os.Stdout
	os.Stdout = writer

	result := &adhoc.Result{
		Hosts: map[string]*adhoc.HostResult{
			"web1": {
				Changed: true,
				Message: "check mode: line would be updated",
				Diff:    "--- before\n+++ after\n-old\n+new",
				Data: map[string]interface{}{
					"path": "/etc/app.conf",
				},
			},
		},
	}

	for _, hostName := range []string{"web1"} {
		hostResult := result.Hosts[hostName]
		fmt.Printf("%s | ", hostName)
		if hostResult.Failed {
			fmt.Printf("FAILED | %s\n", hostResult.Message)
		} else if hostResult.Changed {
			fmt.Printf("CHANGED\n")
		} else {
			fmt.Printf("SUCCESS\n")
		}
		if hostResult.Message != "" && !hostResult.Failed {
			fmt.Printf("msg: %s\n", hostResult.Message)
		}
		if hostResult.Diff != "" {
			fmt.Printf("DIFF:\n%s\n", hostResult.Diff)
		}
		if len(hostResult.Data) > 0 {
			fmt.Printf("DATA:\n%s\n", formatCLIResultData(hostResult.Data))
		}
	}

	writer.Close()
	os.Stdout = oldStdout

	outputBytes, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	output := string(outputBytes)
	if !strings.Contains(output, "web1 | CHANGED") {
		t.Fatalf("expected changed line, got %q", output)
	}
	if !strings.Contains(output, "msg: check mode: line would be updated") {
		t.Fatalf("expected message, got %q", output)
	}
	if !strings.Contains(output, "DIFF:") || !strings.Contains(output, "+new") {
		t.Fatalf("expected diff, got %q", output)
	}
	if !strings.Contains(output, `"path": "/etc/app.conf"`) {
		t.Fatalf("expected data output, got %q", output)
	}
}

func TestAdhocCmdFlags(t *testing.T) {
	cmd := NewAnsibleCmd("test")
	err := cmd.ParseFlags([]string{
		"-i", "hosts.ini",
		"-c", "local",
		"-T", "60",
		"--private-key", "/path/to/key",
		"-B", "300",
		"-P", "10",
		"-o",
		"-t", "/tmp/tree_output",
	})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := cmd.Flags().GetString("connection")
	if conn != "local" {
		t.Fatalf("expected connection local, got %q", conn)
	}
	timeout, _ := cmd.Flags().GetInt("timeout")
	if timeout != 60 {
		t.Fatalf("expected timeout 60, got %d", timeout)
	}
	key, _ := cmd.Flags().GetString("private-key")
	if key != "/path/to/key" {
		t.Fatalf("expected private-key /path/to/key, got %q", key)
	}
	bg, _ := cmd.Flags().GetInt("background")
	if bg != 300 {
		t.Fatalf("expected background 300, got %d", bg)
	}
	poll, _ := cmd.Flags().GetInt("poll")
	if poll != 10 {
		t.Fatalf("expected poll 10, got %d", poll)
	}
	oneline, _ := cmd.Flags().GetBool("one-line")
	if !oneline {
		t.Fatal("expected one-line true")
	}
	treeDir, _ := cmd.Flags().GetString("tree")
	if treeDir != "/tmp/tree_output" {
		t.Fatalf("expected tree /tmp/tree_output, got %q", treeDir)
	}
}

func TestWriteAdhocTreeResults(t *testing.T) {
	tempDir := t.TempDir()
	treeDir := tempDir + "/tree"

	hosts := map[string]*adhoc.HostResult{
		"host1": {
			Changed:  true,
			ExitCode: 0,
			Stdout:   "pong",
		},
		"host2": {
			Failed:   true,
			ExitCode: 1,
			Message:  "command failed",
			Stderr:   "permission denied",
		},
	}

	if err := writeAdhocTreeResults(treeDir, hosts); err != nil {
		t.Fatalf("writeAdhocTreeResults failed: %v", err)
	}

	// Verify host1 file
	f1 := treeDir + "/host1"
	data1, err := os.ReadFile(f1)
	if err != nil {
		t.Fatalf("reading %s: %v", f1, err)
	}
	if !strings.Contains(string(data1), `"changed": true`) || !strings.Contains(string(data1), `"stdout": "pong"`) {
		t.Fatalf("unexpected content for host1: %s", string(data1))
	}

	// Verify host2 file
	f2 := treeDir + "/host2"
	data2, err := os.ReadFile(f2)
	if err != nil {
		t.Fatalf("reading %s: %v", f2, err)
	}
	if !strings.Contains(string(data2), `"failed": true`) || !strings.Contains(string(data2), `"permission denied"`) {
		t.Fatalf("unexpected content for host2: %s", string(data2))
	}
}
