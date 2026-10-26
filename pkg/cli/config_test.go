package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigViewAndDump(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "ansible.cfg")
	content := `[defaults]
inventory = my_hosts.ini
forks = 20
remote_user = deployer
timeout = 45
`
	if err := os.WriteFile(cfgFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	opts := &ConfigOptions{
		ConfigFile: cfgFile,
	}

	// 1. 测试 view
	reader, writer, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = writer
	err := runConfigView(opts)
	writer.Close()
	os.Stdout = oldStdout
	if err != nil {
		t.Fatal(err)
	}

	viewBytes, _ := io.ReadAll(reader)
	viewOut := string(viewBytes)
	if !strings.Contains(viewOut, "# Using config file:") || !strings.Contains(viewOut, "remote_user = deployer") {
		t.Fatalf("unexpected view output: %s", viewOut)
	}

	// 2. 测试 dump
	reader, writer, _ = os.Pipe()
	os.Stdout = writer
	err = runConfigDump(opts)
	writer.Close()
	os.Stdout = oldStdout
	if err != nil {
		t.Fatal(err)
	}

	dumpBytes, _ := io.ReadAll(reader)
	dumpOut := string(dumpBytes)
	if !strings.Contains(dumpOut, "DEFAULT_FORKS(") || !strings.Contains(dumpOut, "= 20") {
		t.Fatalf("unexpected dump output: %s", dumpOut)
	}
	if !strings.Contains(dumpOut, "DEFAULT_TIMEOUT(") || !strings.Contains(dumpOut, "= 45") {
		t.Fatalf("unexpected dump timeout: %s", dumpOut)
	}

	// 3. 测试 dump --only-changed
	opts.OnlyChanged = true
	reader, writer, _ = os.Pipe()
	os.Stdout = writer
	err = runConfigDump(opts)
	writer.Close()
	os.Stdout = oldStdout
	if err != nil {
		t.Fatal(err)
	}
	onlyChangedBytes, _ := io.ReadAll(reader)
	onlyChangedOut := string(onlyChangedBytes)
	if strings.Contains(onlyChangedOut, "(default)") {
		t.Fatalf("dump --only-changed should not contain default entries: %s", onlyChangedOut)
	}

	// 4. 测试 init
	var buf bytes.Buffer
	reader, writer, _ = os.Pipe()
	os.Stdout = writer
	err = runConfigInit()
	writer.Close()
	os.Stdout = oldStdout
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(&buf, reader)
	if !strings.Contains(buf.String(), "[defaults]") || !strings.Contains(buf.String(), "inventory = hosts.ini") {
		t.Fatalf("unexpected init template: %s", buf.String())
	}
}

func TestConfigCommandRouting(t *testing.T) {
	cmd := NewConfigCmd("test")
	if len(cmd.Commands()) != 3 {
		t.Fatalf("expected 3 subcommands (view, dump, init), got %d", len(cmd.Commands()))
	}
}
