package module

import (
	"regexp"
	"strings"
	"testing"
)

func TestFindValidate(t *testing.T) {
	mod := &FindModule{}

	if err := mod.Validate(map[string]interface{}{}); err == nil {
		t.Fatal("expected missing paths validation error")
	}
	if err := mod.Validate(map[string]interface{}{
		"paths": []interface{}{"/tmp"},
	}); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if err := mod.Validate(map[string]interface{}{
		"paths":     []interface{}{"/tmp"},
		"file_type": "socket",
	}); err == nil {
		t.Fatal("expected unsupported file_type validation error")
	}
	if err := mod.Validate(map[string]interface{}{
		"paths": []interface{}{"/tmp"},
		"age":   "10x",
	}); err == nil {
		t.Fatal("expected invalid age validation error")
	}
	if err := mod.Validate(map[string]interface{}{
		"paths":     []interface{}{"/tmp"},
		"use_regex": true,
		"patterns":  []interface{}{"["},
	}); err == nil {
		t.Fatal("expected invalid regex validation error")
	}
	if err := mod.Validate(map[string]interface{}{
		"paths": []interface{}{"/tmp"},
		"depth": -1,
	}); err == nil {
		t.Fatal("expected invalid depth validation error")
	}
	if err := mod.Validate(map[string]interface{}{
		"paths":     []interface{}{"/tmp"},
		"min_depth": -1,
	}); err == nil {
		t.Fatal("expected invalid min_depth validation error")
	}
	if err := mod.Validate(map[string]interface{}{
		"paths":       []interface{}{"/tmp"},
		"max_results": -1,
	}); err == nil {
		t.Fatal("expected invalid max_results validation error")
	}
}

func TestBuildFindCommand(t *testing.T) {
	cmd, err := buildFindCommand(findSpec{
		Paths:    []string{"/var/log"},
		Patterns: []string{"*.log", "*.out"},
		FileType: "file",
		Recurse:  false,
		Hidden:   false,
		Contains: "ERROR",
		Age:      "+2d",
		Size:     "+10k",
	})
	if err != nil {
		t.Fatalf("buildFindCommand() error = %v", err)
	}
	if !strings.Contains(cmd, "find '/var/log' -maxdepth 1") {
		t.Fatalf("expected paths and maxdepth, got %q", cmd)
	}
	if !strings.Contains(cmd, "-type f") {
		t.Fatalf("expected file type filter, got %q", cmd)
	}
	if !strings.Contains(cmd, "-name '*.log'") || !strings.Contains(cmd, "-name '*.out'") {
		t.Fatalf("expected pattern filters, got %q", cmd)
	}
	if strings.Contains(cmd, "grep -qE 'ERROR'") {
		t.Fatalf("expected contains filtering to be handled outside find command, got %q", cmd)
	}
	if !strings.Contains(cmd, "-mtime +2") || !strings.Contains(cmd, "-size +10k") {
		t.Fatalf("expected age/size filters, got %q", cmd)
	}
}

func TestBuildFindCommandRegexExcludeAndFollow(t *testing.T) {
	cmd, err := buildFindCommand(findSpec{
		Paths:    []string{"/srv/app"},
		Patterns: []string{".*\\.ya?ml$"},
		Excludes: []string{".*/vendor/.*"},
		UseRegex: true,
		Follow:   true,
	})
	if err != nil {
		t.Fatalf("buildFindCommand(regex) error = %v", err)
	}
	if !strings.Contains(cmd, "find -L '/srv/app'") {
		t.Fatalf("expected find -L, got %q", cmd)
	}
	if !strings.Contains(cmd, "-regex '.*\\.ya?ml$'") {
		t.Fatalf("expected regex pattern matching, got %q", cmd)
	}
	if !strings.Contains(cmd, "! -regex '.*/vendor/.*'") {
		t.Fatalf("expected regex excludes, got %q", cmd)
	}
	if strings.Contains(cmd, "-name '.*\\.ya?ml$'") {
		t.Fatalf("expected regex mode instead of name mode, got %q", cmd)
	}
}

func TestBuildFindCommandDepthAndMaxResults(t *testing.T) {
	cmd, err := buildFindCommand(findSpec{
		Paths:      []string{"/srv/app"},
		MinDepth:   1,
		Depth:      3,
		Recurse:    true,
		MaxResults: 5,
	})
	if err != nil {
		t.Fatalf("buildFindCommand(depth/max_results) error = %v", err)
	}
	if !strings.Contains(cmd, "find '/srv/app' -mindepth 1 -maxdepth 3") {
		t.Fatalf("expected explicit min/max depth, got %q", cmd)
	}
	if !strings.Contains(cmd, `if [ "$count" -ge 5 ]; then`) {
		t.Fatalf("expected max_results guard, got %q", cmd)
	}
	if !strings.Contains(cmd, `printf '__FIND_LIMIT__\t%s\n' "$skipped"`) {
		t.Fatalf("expected skipped_by_limit sentinel, got %q", cmd)
	}
}

func TestBuildFindAgeAndSizeExpr(t *testing.T) {
	ageExpr, err := buildFindAgeExpr("-3h")
	if err != nil {
		t.Fatalf("buildFindAgeExpr() error = %v", err)
	}
	if ageExpr != "-mmin -180" {
		t.Fatalf("unexpected age expression: %q", ageExpr)
	}

	sizeExpr, err := buildFindSizeExpr("+5M")
	if err != nil {
		t.Fatalf("buildFindSizeExpr() error = %v", err)
	}
	if sizeExpr != "-size +5M" {
		t.Fatalf("unexpected size expression: %q", sizeExpr)
	}
}

func TestParseFindOutput(t *testing.T) {
	output := parseFindOutput("/tmp/app.log\tfile\t128\t644\n/tmp/conf\tdirectory\t4096\t755\n__FIND_LIMIT__\t1\n")
	if output.Examined != 2 || len(output.Files) != 2 {
		t.Fatalf("unexpected parsed output: %#v", output)
	}
	if output.Files[0]["path"] != "/tmp/app.log" || output.Files[0]["state"] != "file" || output.Files[0]["size"] != int64(128) {
		t.Fatalf("unexpected first file entry: %#v", output.Files[0])
	}
	if output.SkippedByLimit != 1 {
		t.Fatalf("expected skipped_by_limit=1, got %#v", output)
	}
	if len(output.Files) != 2 {
		t.Fatalf("expected returned=2 via file count, got %#v", output)
	}
}

func TestFindValidateContainsRegexp(t *testing.T) {
	mod := &FindModule{}
	if err := mod.Validate(map[string]interface{}{
		"paths":    []interface{}{"/tmp"},
		"contains": "[",
	}); err == nil {
		t.Fatal("expected invalid contains regexp validation error")
	}
}

func TestContentMatchesRegexp(t *testing.T) {
	if contentMatchesRegexp("a\nb\n", regexp.MustCompile(`(?s)a.*b`), false) {
		t.Fatal("expected line mode not to match across lines")
	}
	if !contentMatchesRegexp("a\nb\n", regexp.MustCompile(`(?s)a.*b`), true) {
		t.Fatal("expected read_whole_file mode to match across lines")
	}
	if !contentMatchesRegexp("prefix\nalpha-beta\nsuffix\n", regexp.MustCompile(`alpha.*beta`), false) {
		t.Fatal("expected single-line match to succeed")
	}
}

func TestRegistryIncludesFind(t *testing.T) {
	registry := NewRegistry()
	mod, err := registry.Get("find")
	if err != nil {
		t.Fatalf("registry.Get(find) error = %v", err)
	}
	if mod.Name() != "find" {
		t.Fatalf("unexpected module name: %s", mod.Name())
	}
}
