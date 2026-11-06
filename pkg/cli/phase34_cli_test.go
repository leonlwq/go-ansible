package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// 5. 测试 ansible-doc 增加 -M / --module-path 标志
func TestPhase34_Doc_ModulePathFlag(t *testing.T) {
	cmd := NewDocCmd("0.1.0")
	cmd.SetArgs([]string{"-M", "/custom/modules:/opt/ansible/plugins", "command"})
	
	// 捕获输出
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := cmd.Execute()
	w.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("unexpected error executing doc cmd with -M: %v", err)
	}

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	if !strings.Contains(output, "Execute commands on targets") && !strings.Contains(output, "command") {
		t.Fatalf("expected documentation for command module, got: %s", output)
	}
}

// 8. 测试 ansible Ad-hoc 命令的 -o / --one-line 紧凑输出模式
func TestPhase34_Adhoc_OneLineFlag(t *testing.T) {
	cmd := NewAnsibleCmd("0.1.0")
	flag := cmd.Flags().Lookup("one-line")
	if flag == nil {
		t.Fatalf("expected --one-line / -o flag to exist on ansible command")
	}
	if flag.Shorthand != "o" {
		t.Fatalf("expected shorthand 'o', got %q", flag.Shorthand)
	}
}

