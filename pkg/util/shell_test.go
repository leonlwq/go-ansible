package util

import (
	"testing"
)

func TestFormatEnvPrefix(t *testing.T) {
	// Empty map
	if got := FormatEnvPrefix(nil); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
	if got := FormatEnvPrefix(map[string]string{}); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}

	// Single key
	single := map[string]string{"FOO": "bar"}
	expectedSingle := "env FOO='bar' "
	if got := FormatEnvPrefix(single); got != expectedSingle {
		t.Errorf("expected %q, got %q", expectedSingle, got)
	}

	// Multiple keys sorted deterministically
	multi := map[string]string{
		"ZONE": "us-east-1",
		"APP":  "demo",
		"KEY":  "it's cool",
	}
	expectedMulti := "env APP='demo' KEY='it'\"'\"'s cool' ZONE='us-east-1' "
	if got := FormatEnvPrefix(multi); got != expectedMulti {
		t.Errorf("expected %q, got %q", expectedMulti, got)
	}
}

func TestQuoteAndSplitArgs(t *testing.T) {
	quoted := Quote("hello 'world'")
	expectedQuoted := "'hello '\\''world'\\'''"
	if quoted != expectedQuoted {
		t.Errorf("expected %s, got %s", expectedQuoted, quoted)
	}

	emptyQuoted := Quote("")
	if emptyQuoted != "''" {
		t.Errorf("expected '', got %s", emptyQuoted)
	}

	args, err := SplitArgs("ansible-playbook -i inv site.yml -e 'key=val' --tags \"web,api\"")
	if err != nil {
		t.Fatalf("unexpected SplitArgs error: %v", err)
	}
	expectedArgs := []string{"ansible-playbook", "-i", "inv", "site.yml", "-e", "key=val", "--tags", "web,api"}
	if len(args) != len(expectedArgs) {
		t.Fatalf("expected len %d, got %d", len(expectedArgs), len(args))
	}
	for i, arg := range args {
		if arg != expectedArgs[i] {
			t.Errorf("arg[%d] expected %s, got %s", i, expectedArgs[i], arg)
		}
	}
}

