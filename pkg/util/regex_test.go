package util

import (
	"testing"
)

func TestGetRegex(t *testing.T) {
	re1, err := GetRegex(`^[a-z]+$`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !re1.MatchString("hello") {
		t.Fatalf("expected re1 to match hello")
	}

	re2, err := GetRegex(`^[a-z]+$`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if re1 != re2 {
		t.Fatalf("expected cached pointer to be identical")
	}

	_, err = GetRegex(`[invalid`)
	if err == nil {
		t.Fatalf("expected error for invalid regex")
	}
}

