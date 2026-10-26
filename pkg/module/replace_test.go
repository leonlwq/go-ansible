package module

import (
	"regexp"
	"strings"
	"testing"
)

func TestReplaceValidate(t *testing.T) {
	mod := &ReplaceModule{}

	if err := mod.Validate(map[string]interface{}{}); err == nil {
		t.Fatal("expected missing path validation error")
	}
	if err := mod.Validate(map[string]interface{}{"path": "/tmp/app.conf"}); err == nil {
		t.Fatal("expected missing regexp/search_string validation error")
	}
	if err := mod.Validate(map[string]interface{}{
		"path":   "/tmp/app.conf",
		"regexp": "foo",
	}); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if err := mod.Validate(map[string]interface{}{
		"path":   "/tmp/app.conf",
		"regexp": "foo",
		"before": "[",
	}); err == nil {
		t.Fatal("expected invalid before regexp validation error")
	}
	if err := mod.Validate(map[string]interface{}{
		"path":          "/tmp/app.conf",
		"regexp":        "foo",
		"search_string": "foo",
	}); err == nil {
		t.Fatal("expected regexp/search_string conflict validation error")
	}
}

func TestReplaceRegexpBehavior(t *testing.T) {
	re, err := regexp.Compile(`version=\d+`)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	before := "name=elite\nversion=1\n"
	after := re.ReplaceAllString(before, "version=2")
	if after != "name=elite\nversion=2\n" {
		t.Fatalf("unexpected replace result: %q", after)
	}

	diff := buildTextDiff("/tmp/app.conf", before, after)
	if !strings.Contains(diff, "-version=1") || !strings.Contains(diff, "+version=2") {
		t.Fatalf("unexpected diff: %q", diff)
	}
}

func TestApplyScopedReplace(t *testing.T) {
	content := "head\nBEGIN\nversion=1\nEND\nversion=1\n"
	updated, count, err := applyScopedReplace(content, `version=\d+`, "", "version=2", `BEGIN`, `END`)
	if err != nil {
		t.Fatalf("applyScopedReplace() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 scoped replace match, got %d", count)
	}
	if updated != "head\nBEGIN\nversion=2\nEND\nversion=1\n" {
		t.Fatalf("unexpected scoped replace result: %q", updated)
	}
}

func TestApplyScopedReplaceWithoutBoundaryMatches(t *testing.T) {
	content := "alpha\nversion=1\nomega\n"
	updated, count, err := applyScopedReplace(content, `version=\d+`, "", "version=2", `MISSING`, `END`)
	if err != nil {
		t.Fatalf("applyScopedReplace(no boundary) error = %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 fallback replace match, got %d", count)
	}
	if updated != "alpha\nversion=2\nomega\n" {
		t.Fatalf("unexpected replace fallback result: %q", updated)
	}
}

func TestApplyScopedReplaceSearchString(t *testing.T) {
	updated, count, err := applyScopedReplace("foo bar foo\n", "", "foo", "baz", "", "")
	if err != nil {
		t.Fatalf("applyScopedReplace(search_string) error = %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 search_string matches, got %d", count)
	}
	if updated != "baz bar baz\n" {
		t.Fatalf("unexpected search_string replace result: %q", updated)
	}
}

func TestReplaceResultDataShape(t *testing.T) {
	data := map[string]interface{}{
		"path":     "/tmp/app.conf",
		"matched":  true,
		"count":    1,
		"action":   "replace",
		"encoding": "utf-8",
		"newline":  "lf",
		"before":   "version=1\n",
		"after":    "version=2\n",
	}
	if data["path"] != "/tmp/app.conf" || data["matched"] != true || data["count"] != 1 || data["action"] != "replace" || data["encoding"] != "utf-8" || data["newline"] != "lf" {
		t.Fatalf("unexpected replace result data: %#v", data)
	}
}

func TestRegistryIncludesReplace(t *testing.T) {
	registry := NewRegistry()
	mod, err := registry.Get("replace")
	if err != nil {
		t.Fatalf("registry.Get(replace) error = %v", err)
	}
	if mod.Name() != "replace" {
		t.Fatalf("unexpected module name: %s", mod.Name())
	}
}
