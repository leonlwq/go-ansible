package util

import (
	"testing"
)

func TestSyntaxParenthesesAndUnquote(t *testing.T) {
	// Test IsMatchedParentheses
	if !IsMatchedParentheses("(a + b) * (c + d)") {
		t.Errorf("expected balanced parentheses")
	}
	if IsMatchedParentheses("(a + b))") {
		t.Errorf("expected unbalanced parentheses")
	}
	if !IsMatchedParentheses("('(' + ')')") {
		t.Errorf("expected quotes inside parentheses to be ignored")
	}

	// Test HasSurroundingParentheses
	if !HasSurroundingParentheses("(a and b)") {
		t.Errorf("expected surrounding parentheses")
	}
	if HasSurroundingParentheses("(a) and (b)") {
		t.Errorf("expected false for two independent parenthesized groups")
	}

	// Test StripSurroundingParentheses
	if StripSurroundingParentheses("((  a or b  ))") != "a or b" {
		t.Errorf("expected stripped 'a or b', got %q", StripSurroundingParentheses("((  a or b  ))"))
	}
	if StripSurroundingParentheses("(a) and (b)") != "(a) and (b)" {
		t.Errorf("expected unchanged, got %q", StripSurroundingParentheses("(a) and (b)"))
	}

	// Test Unquote
	if unquoted, ok := Unquote("'hello world'"); !ok || unquoted != "hello world" {
		t.Errorf("expected unquoted 'hello world', got %q, %v", unquoted, ok)
	}
	if unquoted, ok := Unquote(`"double quote"`); !ok || unquoted != "double quote" {
		t.Errorf("expected unquoted 'double quote', got %q, %v", unquoted, ok)
	}
	if unquoted, ok := Unquote("raw_string"); ok || unquoted != "raw_string" {
		t.Errorf("expected raw_string false, got %q, %v", unquoted, ok)
	}
}

func TestTruthyAndToBool(t *testing.T) {
	// ToBool
	trueCases := []interface{}{true, "True", "YES", "1", "on", "Y", "t", 1, 100}
	for _, tc := range trueCases {
		if !ToBool(tc) {
			t.Errorf("expected ToBool(%v) == true", tc)
		}
	}
	falseCases := []interface{}{false, "False", "NO", "0", "off", "N", "f", 0, nil, ""}
	for _, fc := range falseCases {
		if ToBool(fc) {
			t.Errorf("expected ToBool(%v) == false", fc)
		}
	}

	// IsTruthy
	if IsTruthy(nil) || IsTruthy(false) || IsTruthy("") || IsTruthy("0") || IsTruthy("false") || IsTruthy("none") {
		t.Errorf("expected false for falsy values")
	}
	if IsTruthy([]string{}) || IsTruthy([]interface{}{}) || IsTruthy(map[string]interface{}{}) {
		t.Errorf("expected false for empty slices and maps")
	}
	if !IsTruthy([]string{"item1"}) || !IsTruthy([]interface{}{1}) || !IsTruthy(map[string]string{"k": "v"}) {
		t.Errorf("expected true for non-empty slices and maps")
	}
}

func TestIndentBlock(t *testing.T) {
	input := "line1\nline2\nline3\n"
	expected := "  line1\n  line2\n  line3"
	got := IndentBlock(input, "  ")
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}

	single := "single line"
	if IndentBlock(single, ">> ") != ">> single line" {
		t.Errorf("expected '>> single line', got %q", IndentBlock(single, ">> "))
	}
}


