package module

import (
	"fmt"
	"strings"

	"go-ansible/pkg/ssh"
)

type GetURLModule struct{}

type getURLSpec struct {
	URL           string
	Dest          string
	Checksum      string
	Mode          string
	Owner         string
	Group         string
	Timeout       int
	Force         bool
	Backup        bool
	ValidateCerts bool
}

func (m *GetURLModule) Name() string { return "get_url" }

func (m *GetURLModule) Validate(params map[string]interface{}) error {
	if GetParamString(params, "url", "") == "" {
		return fmt.Errorf("get_url module requires url")
	}
	if GetParamString(params, "dest", "") == "" {
		return fmt.Errorf("get_url module requires dest")
	}
	if checksum := GetParamString(params, "checksum", ""); checksum != "" {
		if _, _, err := parseChecksumSpec(checksum); err != nil {
			return err
		}
	}
	return nil
}

func (m *GetURLModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	spec := getURLSpec{
		URL:           GetParamString(params, "url", ""),
		Dest:          GetParamString(params, "dest", ""),
		Checksum:      GetParamString(params, "checksum", ""),
		Mode:          GetParamMode(params, "mode", ""),
		Owner:         GetParamString(params, "owner", ""),
		Group:         GetParamString(params, "group", ""),
		Timeout:       GetParam[int](params, "timeout", 30),
		Force:         GetParamBool(params, "force", true),
		Backup:        GetParamBool(params, "backup", false),
		ValidateCerts: getOptionalBool(params, "validate_certs", true),
	}

	cmd, err := buildGetURLCommand(spec)
	if err != nil {
		return nil, err
	}

	result, err := client.Execute(cmd)
	if err != nil {
		return result, err
	}
	if cleaned, changed, found := parseChangedMarker(result.Stdout); found {
		result.Stdout = cleaned
		result.Changed = changed
	}
	if cleaned, _, found := parseChangedMarker(result.Stderr); found {
		result.Stderr = cleaned
	}
	return result, nil
}

func buildGetURLCommand(spec getURLSpec) (string, error) {
	if spec.URL == "" {
		return "", fmt.Errorf("get_url module requires url")
	}
	if spec.Dest == "" {
		return "", fmt.Errorf("get_url module requires dest")
	}
	if spec.Timeout <= 0 {
		spec.Timeout = 30
	}

	checksumAlgo := ""
	checksumValue := ""
	if spec.Checksum != "" {
		algo, value, err := parseChecksumSpec(spec.Checksum)
		if err != nil {
			return "", err
		}
		checksumAlgo = algo
		checksumValue = value
	}

	quotedDest := shellQuote(spec.Dest)
	quotedURL := shellQuote(spec.URL)
	lines := []string{
		"set -e",
		fmt.Sprintf("timeout=%d", spec.Timeout),
		`tmp_file="$(mktemp /tmp/go-ansible-geturl.XXXXXX)"`,
		`cleanup() { rm -f "$tmp_file"; }`,
		`trap cleanup EXIT`,
	}

	if checksumAlgo != "" {
		lines = append(lines,
			fmt.Sprintf("expected_checksum=%s", shellQuote(checksumValue)),
			fmt.Sprintf(`if [ -f %s ] && [ "$(%s)" = "$expected_checksum" ]; then`, quotedDest, checksumCommandForAlgo(quotedDest, checksumAlgo)),
			fmt.Sprintf("  echo %s", changeMarkerFalse),
			"  exit 0",
			"fi",
		)
	} else if !spec.Force {
		lines = append(lines,
			fmt.Sprintf(`if [ -e %s ]; then`, quotedDest),
			fmt.Sprintf("  echo %s", changeMarkerFalse),
			"  exit 0",
			"fi",
		)
	}

	lines = append(lines,
		fmt.Sprintf(`mkdir -p "$(dirname %s)"`, quotedDest),
		buildDownloaderScript(spec.ValidateCerts),
		fmt.Sprintf(`download_file %s "$tmp_file" "$timeout"`, quotedURL),
	)

	if checksumAlgo != "" {
		lines = append(lines,
			fmt.Sprintf(`downloaded_checksum="$(%s)"`, checksumCommandForAlgo(`"$tmp_file"`, checksumAlgo)),
			`if [ "$downloaded_checksum" != "$expected_checksum" ]; then`,
			`  echo "checksum mismatch" >&2`,
			`  exit 1`,
			`fi`,
		)
	}

	lines = append(lines,
		fmt.Sprintf(`existing_checksum="$(%s || true)"`, checksumCommandForAlgo(quotedDest, "sha256")),
		fmt.Sprintf(`downloaded_checksum="$(%s)"`, checksumCommandForAlgo(`"$tmp_file"`, "sha256")),
		`if [ -n "$existing_checksum" ] && [ "$existing_checksum" = "$downloaded_checksum" ]; then`,
		fmt.Sprintf("  echo %s", changeMarkerFalse),
		"  exit 0",
		"fi",
	)

	if spec.Backup {
		lines = append(lines,
			fmt.Sprintf(`if [ -e %s ]; then`, quotedDest),
			fmt.Sprintf(`  cp -p %s %s.bak 2>/dev/null || true`, quotedDest, quotedDest),
			`fi`,
		)
	}

	lines = append(lines,
		fmt.Sprintf(`mv "$tmp_file" %s`, quotedDest),
		`tmp_file=""`,
	)

	if spec.Mode != "" {
		lines = append(lines, fmt.Sprintf("chmod %s %s", spec.Mode, quotedDest))
	}
	if spec.Owner != "" {
		chownTarget := spec.Owner
		if spec.Group != "" {
			chownTarget = spec.Owner + ":" + spec.Group
		}
		lines = append(lines, fmt.Sprintf("chown %s %s", shellQuote(chownTarget), quotedDest))
	} else if spec.Group != "" {
		lines = append(lines, fmt.Sprintf("chgrp %s %s", shellQuote(spec.Group), quotedDest))
	}

	lines = append(lines, fmt.Sprintf("echo %s", changeMarkerTrue))
	return strings.Join(lines, "\n"), nil
}

func parseChecksumSpec(spec string) (string, string, error) {
	algo, value, ok := strings.Cut(strings.TrimSpace(spec), ":")
	if !ok {
		return "", "", fmt.Errorf("invalid checksum format: %s", spec)
	}

	algo = strings.ToLower(strings.TrimSpace(algo))
	value = strings.ToLower(strings.TrimSpace(value))
	if algo == "" || value == "" {
		return "", "", fmt.Errorf("invalid checksum format: %s", spec)
	}

	switch algo {
	case "md5", "sha1", "sha224", "sha256", "sha384", "sha512":
		return algo, value, nil
	default:
		return "", "", fmt.Errorf("unsupported checksum algorithm: %s", algo)
	}
}

func checksumCommandForAlgo(pathExpr, algo string) string {
	switch algo {
	case "md5":
		return fmt.Sprintf("md5sum %s 2>/dev/null | cut -d' ' -f1 || md5 -q %s 2>/dev/null", pathExpr, pathExpr)
	case "sha1":
		return fmt.Sprintf("sha1sum %s 2>/dev/null | cut -d' ' -f1 || shasum -a 1 %s 2>/dev/null | cut -d' ' -f1", pathExpr, pathExpr)
	case "sha224":
		return fmt.Sprintf("sha224sum %s 2>/dev/null | cut -d' ' -f1 || shasum -a 224 %s 2>/dev/null | cut -d' ' -f1", pathExpr, pathExpr)
	case "sha256":
		return fmt.Sprintf("sha256sum %s 2>/dev/null | cut -d' ' -f1 || shasum -a 256 %s 2>/dev/null | cut -d' ' -f1", pathExpr, pathExpr)
	case "sha384":
		return fmt.Sprintf("sha384sum %s 2>/dev/null | cut -d' ' -f1 || shasum -a 384 %s 2>/dev/null | cut -d' ' -f1", pathExpr, pathExpr)
	case "sha512":
		return fmt.Sprintf("sha512sum %s 2>/dev/null | cut -d' ' -f1 || shasum -a 512 %s 2>/dev/null | cut -d' ' -f1", pathExpr, pathExpr)
	default:
		return fmt.Sprintf("sha256sum %s 2>/dev/null | cut -d' ' -f1 || shasum -a 256 %s 2>/dev/null | cut -d' ' -f1", pathExpr, pathExpr)
	}
}

func buildDownloaderScript(validateCerts bool) string {
	curlExtra := ""
	wgetExtra := ""
	if !validateCerts {
		curlExtra = " -k"
		wgetExtra = " --no-check-certificate"
	}

	return fmt.Sprintf(`download_file() {
  url="$1"
  output="$2"
  timeout="$3"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL%s --connect-timeout "$timeout" --max-time "$timeout" -o "$output" "$url"
    return
  fi
  if command -v wget >/dev/null 2>&1; then
    wget -q%s --timeout="$timeout" -O "$output" "$url"
    return
  fi
  echo "curl or wget is required for get_url" >&2
  exit 1
}`, curlExtra, wgetExtra)
}

func getOptionalBool(params map[string]interface{}, key string, defaultValue bool) bool {
	value, ok := params[key]
	if !ok {
		return defaultValue
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "yes", "true", "1", "on":
			return true
		case "no", "false", "0", "off":
			return false
		}
	}
	return defaultValue
}
