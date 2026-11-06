package module

import (
	"fmt"
	"strings"
	"unicode"

	"go-ansible/pkg/ssh"
)

// PackageFactsModule 实现了 ansible.builtin.package_facts 核心功能
type PackageFactsModule struct{}

func (m *PackageFactsModule) Name() string {
	return "package_facts"
}

func (m *PackageFactsModule) Validate(params map[string]interface{}) error {
	mgr := strings.ToLower(strings.TrimSpace(GetParamString(params, "manager", "auto")))
	if mgr != "" && mgr != "auto" && mgr != "apt" && mgr != "rpm" && mgr != "apk" && mgr != "pacman" && mgr != "brew" {
		return fmt.Errorf("unsupported manager: %s (supported: auto, apt, rpm, apk, pacman, brew)", mgr)
	}
	return nil
}

func (m *PackageFactsModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	mgr := strings.ToLower(strings.TrimSpace(GetParamString(params, "manager", "auto")))
	if mgr == "" {
		mgr = "auto"
	}

	script := fmt.Sprintf(`
MGR="%s"
if [ "$MGR" = "apt" ] || ([ "$MGR" = "auto" ] && command -v dpkg-query >/dev/null 2>&1); then
    echo "MGR:apt"
    dpkg-query -W -f='${Package}\t${Version}\t${Architecture}\n' 2>/dev/null || true
elif [ "$MGR" = "rpm" ] || ([ "$MGR" = "auto" ] && command -v rpm >/dev/null 2>&1); then
    echo "MGR:rpm"
    rpm -qa --qf '%%{NAME}\t%%{VERSION}-%%{RELEASE}\t%%{ARCH}\n' 2>/dev/null || true
elif [ "$MGR" = "apk" ] || ([ "$MGR" = "auto" ] && command -v apk >/dev/null 2>&1); then
    echo "MGR:apk"
    apk info -v 2>/dev/null || true
elif [ "$MGR" = "pacman" ] || ([ "$MGR" = "auto" ] && command -v pacman >/dev/null 2>&1); then
    echo "MGR:pacman"
    pacman -Q 2>/dev/null || true
elif [ "$MGR" = "brew" ] || ([ "$MGR" = "auto" ] && command -v brew >/dev/null 2>&1); then
    echo "MGR:brew"
    brew list --versions 2>/dev/null || true
elif [ "$(uname -s)" = "Darwin" ] && command -v pkgutil >/dev/null 2>&1; then
    echo "MGR:pkgutil"
    pkgutil --pkgs 2>/dev/null || true
else
    echo "MGR:none"
fi
`, mgr)

	res, err := client.Execute(script)
	if err != nil {
		return nil, fmt.Errorf("execute package_facts probe: %w", err)
	}

	stdout := ""
	if res != nil {
		stdout = res.Stdout
	}

	packages := parsePackageFactsOutput(stdout)

	return &Result{
		Changed: false,
		Data: map[string]interface{}{
			"ansible_facts": map[string]interface{}{
				"packages": packages,
			},
		},
		Message: fmt.Sprintf("%d packages discovered", len(packages)),
	}, nil
}

func parsePackageFactsOutput(output string) map[string]interface{} {
	packages := make(map[string]interface{})
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")

	currentMgr := ""

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "MGR:") {
			currentMgr = strings.TrimPrefix(trimmed, "MGR:")
			continue
		}

		if currentMgr == "" || currentMgr == "none" {
			continue
		}

		var name, version, arch string
		source := currentMgr

		switch currentMgr {
		case "apt", "rpm":
			// Tab separated: name \t version \t arch
			parts := strings.Split(trimmed, "\t")
			if len(parts) >= 2 {
				name = strings.TrimSpace(parts[0])
				version = strings.TrimSpace(parts[1])
				if len(parts) >= 3 {
					arch = strings.TrimSpace(parts[2])
				}
			} else {
				fields := strings.Fields(trimmed)
				if len(fields) >= 2 {
					name = fields[0]
					version = fields[1]
					if len(fields) >= 3 {
						arch = fields[2]
					}
				}
			}

		case "apk":
			// e.g. alpine-keys-2.4-r1 or busybox-1.35.0-r29
			name, version = parseApkLine(trimmed)

		case "pacman", "brew":
			// Space separated: name version
			fields := strings.Fields(trimmed)
			if len(fields) >= 1 {
				name = fields[0]
				if len(fields) >= 2 {
					version = fields[1]
				}
			}

		case "pkgutil":
			name = trimmed
			version = "unknown"
		}

		if name == "" {
			continue
		}

		pkgEntry := map[string]interface{}{
			"name":    name,
			"version": version,
			"arch":    arch,
			"source":  source,
		}

		if existing, ok := packages[name].([]interface{}); ok {
			packages[name] = append(existing, pkgEntry)
		} else {
			packages[name] = []interface{}{pkgEntry}
		}
	}

	return packages
}

func parseApkLine(line string) (string, string) {
	// Format: <name>-<version>-<release>
	// Find the first hyphen that is followed by a digit
	idx := -1
	for i := 0; i < len(line)-1; i++ {
		if line[i] == '-' && unicode.IsDigit(rune(line[i+1])) {
			idx = i
			break
		}
	}
	if idx != -1 {
		return line[:idx], line[idx+1:]
	}
	return line, "unknown"
}
