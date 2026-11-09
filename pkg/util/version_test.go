package util

import "testing"

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		v1       string
		v2       string
		op       string
		expected bool
	}{
		{"20.04", "20.04", "==", true},
		{"20.04", "20.04", "!=", false},
		{"22.04", "20.04", ">", true},
		{"22.04", "20.04", ">=", true},
		{"20.04", "22.04", "<", true},
		{"20.04", "22.04", "<=", true},
		{"8.10", "8.2", ">", true},
		{"8.2", "8.10", "<", true},
		{"1.0.0-rc1", "1.0.0", "<", true},
		{"2.4.1", "2.4", ">", true},
		{"2.4", "2.4.1", "<", true},
		{"2.16.0", "2.16", ">=", true},
		{"v1.2", "1.2", "==", false},
	}

	for _, tt := range tests {
		got := CompareVersions(tt.v1, tt.v2, tt.op)
		if got != tt.expected {
			t.Errorf("CompareVersions(%q, %q, %q) = %v, expected %v", tt.v1, tt.v2, tt.op, got, tt.expected)
		}
	}
}

