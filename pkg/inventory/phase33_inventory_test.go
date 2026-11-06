package inventory

import (
	"reflect"
	"testing"
)

// TestPhase33_ExpandHostRangePatternEdgeCases 测试主机范围展开对畸形或边界输入的防御性容错
func TestPhase33_ExpandHostRangePatternEdgeCases(t *testing.T) {
	tests := []struct {
		pattern  string
		expected []string
	}{
		{
			pattern:  "node[1:3].example.com",
			expected: []string{"node1.example.com", "node2.example.com", "node3.example.com"},
		},
		{
			pattern:  "node[:].example.com",
			expected: []string{"node[:].example.com"},
		},
		{
			pattern:  "node[1:].example.com",
			expected: []string{"node[1:].example.com"},
		},
		{
			pattern:  "node[:3].example.com",
			expected: []string{"node[:3].example.com"},
		},
	}

	for _, tt := range tests {
		actual := expandHostRangePattern(tt.pattern)
		if !reflect.DeepEqual(actual, tt.expected) {
			t.Errorf("expandHostRangePattern(%q) = %v, expected %v", tt.pattern, actual, tt.expected)
		}
	}
}

