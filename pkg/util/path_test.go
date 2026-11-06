package util

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("unable to get user home dir")
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty", "", ""},
		{"tilde only", "~", home},
		{"tilde slash", "~/test.txt", filepath.Join(home, "test.txt")},
		{"plain relative", "foo/bar", "foo/bar"},
		{"plain absolute", "/var/log/app.log", "/var/log/app.log"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExpandPath(tt.input)
			if got != tt.expected {
				t.Errorf("ExpandPath(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}

	// Test env var expansion
	os.Setenv("TEST_EXPAND_DIR", "/custom/dir")
	defer os.Unsetenv("TEST_EXPAND_DIR")
	gotEnv := ExpandPath("$TEST_EXPAND_DIR/file.cfg")
	if gotEnv != "/custom/dir/file.cfg" {
		t.Errorf("expected /custom/dir/file.cfg, got %q", gotEnv)
	}
}

