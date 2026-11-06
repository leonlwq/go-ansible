package ssh

import (
	"strings"
	"testing"
)

func TestBoundedBuffer(t *testing.T) {
	buf := NewBoundedBuffer(100)
	n, err := buf.Write([]byte(strings.Repeat("a", 60)))
	if err != nil || n != 60 {
		t.Fatalf("unexpected write 1: n=%d err=%v", n, err)
	}

	n, err = buf.Write([]byte(strings.Repeat("b", 60)))
	if err != nil || n != 60 {
		t.Fatalf("unexpected write 2: n=%d err=%v", n, err)
	}

	res := buf.String()
	if !strings.Contains(res, "...[output truncated: exceeded maximum limit of 16MB]...") {
		t.Fatalf("expected truncation notice in output, got: %s", res)
	}

	prefix := res[:100]
	if len(prefix) != 100 {
		t.Fatalf("expected 100 prefix bytes, got %d", len(prefix))
	}
	if !strings.HasPrefix(prefix, strings.Repeat("a", 60)) {
		t.Fatalf("expected prefix with 60 a's")
	}
}

func TestReadBoundedOutput(t *testing.T) {
	reader := strings.NewReader(strings.Repeat("x", 200))
	out, err := readBoundedOutput(reader, 100)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(out, "...[output truncated") {
		t.Fatalf("expected truncation in readBoundedOutput, got: %s", out)
	}
}

