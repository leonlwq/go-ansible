package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"go-ansible/pkg/module"
)

func TestDocListAndSnippet(t *testing.T) {
	// 1. 验证元数据存在性
	copyDoc, ok := module.GetModuleDoc("copy")
	if !ok {
		t.Fatal("expected doc for copy module")
	}
	if copyDoc.ShortDesc == "" || len(copyDoc.Options) == 0 {
		t.Fatalf("unexpected copyDoc: %#v", copyDoc)
	}

	snippet := module.FormatSnippet(copyDoc)
	if !strings.Contains(snippet, "copy:") || !strings.Contains(snippet, "dest:") {
		t.Fatalf("unexpected snippet output: %s", snippet)
	}

	docText := module.FormatDoc(copyDoc)
	if !strings.Contains(docText, "> COPY") || !strings.Contains(docText, "DESCRIPTION:") || !strings.Contains(docText, "OPTIONS") {
		t.Fatalf("unexpected docText output: %s", docText)
	}

	// 2. 测试 CLI list
	reader, writer, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = writer

	optsList := &DocOptions{List: true}
	err := optsList.Run()
	writer.Close()
	os.Stdout = oldStdout
	if err != nil {
		t.Fatal(err)
	}

	listOut, _ := io.ReadAll(reader)
	outStr := string(listOut)
	if !strings.Contains(outStr, "copy") || !strings.Contains(outStr, "command") || !strings.Contains(outStr, "template") {
		t.Fatalf("unexpected list output: %s", outStr)
	}

	// 3. 测试 CLI snippet
	var buf bytes.Buffer
	reader, writer, _ = os.Pipe()
	os.Stdout = writer
	optsSnippet := &DocOptions{Snippet: true, Module: "template"}
	err = optsSnippet.Run()
	writer.Close()
	os.Stdout = oldStdout
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(&buf, reader)
	if !strings.Contains(buf.String(), "template:") || !strings.Contains(buf.String(), "src:") {
		t.Fatalf("unexpected snippet command output: %s", buf.String())
	}
}

func TestDocCommandFlagParsing(t *testing.T) {
	cmd := NewDocCmd("test")
	err := cmd.ParseFlags([]string{"-l", "-s"})
	if err != nil {
		t.Fatal(err)
	}
	list, _ := cmd.Flags().GetBool("list")
	snippet, _ := cmd.Flags().GetBool("snippet")
	if !list || !snippet {
		t.Fatalf("expected list=true, snippet=true")
	}
}
