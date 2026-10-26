package cli

import (
	"testing"
)

func TestNewPlaybookCmdFlagParsing(t *testing.T) {
	cmd := NewPlaybookCmd("0.1.0")
	var capturedOpts *PlaybookOptions

	// Check flags presence and defaults
	if cmd.Flags().Lookup("inventory") == nil {
		t.Fatal("expected --inventory flag")
	}
	if cmd.Flags().ShorthandLookup("i") == nil {
		t.Fatal("expected -i shorthand")
	}
	if cmd.Flags().ShorthandLookup("t") == nil {
		t.Fatal("expected -t shorthand")
	}
	if cmd.Flags().ShorthandLookup("l") == nil {
		t.Fatal("expected -l shorthand")
	}
	if cmd.Flags().ShorthandLookup("f") == nil {
		t.Fatal("expected -f shorthand")
	}
	if cmd.Flags().ShorthandLookup("C") == nil {
		t.Fatal("expected -C shorthand")
	}
	if cmd.Flags().ShorthandLookup("D") == nil {
		t.Fatal("expected -D shorthand")
	}
	if cmd.Flags().ShorthandLookup("v") == nil {
		t.Fatal("expected -v shorthand")
	}

	// Test syntax-check dry run execution
	cmd = NewPlaybookCmd("0.1.0")
	args := []string{
		"../../testdata/playbooks/valid_syntax.yml",
		"-i", "inventory.ini",
		"-t", "tag1,tag2",
		"--skip-tags", "tag3",
		"--start-at-task", "deploy",
		"-l", "webservers",
		"-e", "foo=bar",
		"-e", "num=42",
		"-f", "10",
		"-u", "admin",
		"-C",
		"-D",
		"-vv",
		"--syntax-check",
	}
	cmd.SetArgs(args)
	if err := cmd.ParseFlags(args); err == nil {
		// Verify flag binding
		extraVars, _ := cmd.Flags().GetStringArray("extra-vars")
		if len(extraVars) != 2 || extraVars[0] != "foo=bar" || extraVars[1] != "num=42" {
			t.Fatalf("unexpected extra-vars: %v", extraVars)
		}
		forks, _ := cmd.Flags().GetInt("forks")
		if forks != 10 {
			t.Fatalf("expected forks 10, got %d", forks)
		}
		check, _ := cmd.Flags().GetBool("check")
		if !check {
			t.Fatal("expected check mode true")
		}
		diff, _ := cmd.Flags().GetBool("diff")
		if !diff {
			t.Fatal("expected diff mode true")
		}
		verbose, _ := cmd.Flags().GetCount("verbose")
		if verbose != 2 {
			t.Fatalf("expected verbose count 2 for -vv, got %d", verbose)
		}
	}
	_ = capturedOpts
}

func TestNewAnsibleCmdFlagParsing(t *testing.T) {
	cmd := NewAnsibleCmd("0.1.0")

	if cmd.Flags().Lookup("module-name") == nil {
		t.Fatal("expected --module-name flag")
	}
	if cmd.Flags().ShorthandLookup("m") == nil {
		t.Fatal("expected -m shorthand")
	}
	if cmd.Flags().ShorthandLookup("a") == nil {
		t.Fatal("expected -a shorthand")
	}
	if cmd.Flags().ShorthandLookup("b") == nil {
		t.Fatal("expected -b shorthand")
	}
	if cmd.Flags().ShorthandLookup("U") == nil {
		t.Fatal("expected -U shorthand")
	}

	// Test normalizing legacy -become flags and positional target
	rawArgs := []string{
		"-i", "hosts",
		"-m", "shell",
		"-a", "echo hello",
		"-become",
		"-become-user=deploy",
		"-become-method=sudo",
		"-f", "8",
		"-v",
		"webservers",
	}
	normalized := normalizeAnsibleArgs(rawArgs)

	cmd.SetArgs(normalized)
	if err := cmd.ParseFlags(normalized); err != nil {
		t.Fatalf("ParseFlags() error = %v", err)
	}

	become, _ := cmd.Flags().GetBool("become")
	if !become {
		t.Fatal("expected become true")
	}
	becomeUser, _ := cmd.Flags().GetString("become-user")
	if becomeUser != "deploy" {
		t.Fatalf("expected becomeUser deploy, got %q", becomeUser)
	}
	becomeMethod, _ := cmd.Flags().GetString("become-method")
	if becomeMethod != "sudo" {
		t.Fatalf("expected becomeMethod sudo, got %q", becomeMethod)
	}
	modName, _ := cmd.Flags().GetString("module-name")
	if modName != "shell" {
		t.Fatalf("expected module shell, got %q", modName)
	}
	modArgs, _ := cmd.Flags().GetString("args")
	if modArgs != "echo hello" {
		t.Fatalf("expected args 'echo hello', got %q", modArgs)
	}
}

func TestNewInventoryCmdFlagParsing(t *testing.T) {
	cmd := NewInventoryCmd("0.1.0")

	if cmd.Flags().Lookup("inventory") == nil {
		t.Fatal("expected --inventory flag")
	}
	if cmd.Flags().Lookup("list") == nil {
		t.Fatal("expected --list flag")
	}
	if cmd.Flags().Lookup("host") == nil {
		t.Fatal("expected --host flag")
	}
	if cmd.Flags().ShorthandLookup("y") == nil {
		t.Fatal("expected -y shorthand")
	}
	if cmd.Flags().Lookup("graph") == nil {
		t.Fatal("expected --graph flag")
	}

	args := []string{"-i", "hosts.ini", "--list", "-y"}
	cmd.SetArgs(args)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags() error = %v", err)
	}

	list, _ := cmd.Flags().GetBool("list")
	if !list {
		t.Fatal("expected list true")
	}
	yaml, _ := cmd.Flags().GetBool("yaml")
	if !yaml {
		t.Fatal("expected yaml true")
	}
	inv, _ := cmd.Flags().GetString("inventory")
	if inv != "hosts.ini" {
		t.Fatalf("expected inventory hosts.ini, got %q", inv)
	}
}

func TestCliMultiInventoryParsing(t *testing.T) {
	cmd := NewPlaybookCmd("0.1.0")
	args := []string{"site.yml", "-i", "inv1.ini", "-i", "inv2.yml"}
	cmd.SetArgs(args)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags error = %v", err)
	}

	invFlag := cmd.Flags().Lookup("inventory")
	if invFlag == nil {
		t.Fatal("expected inventory flag")
	}
}
