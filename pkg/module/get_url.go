package module

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
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
	URLUsername   string
	URLPassword   string
	Headers       map[string]string
	TmpDest       string
}

func parseHeadersParam(params map[string]interface{}) map[string]string {
	val, ok := params["headers"]
	if !ok || val == nil {
		return nil
	}
	result := make(map[string]string)
	switch v := val.(type) {
	case map[string]interface{}:
		for k, item := range v {
			result[k] = fmt.Sprintf("%v", item)
		}
	case map[string]string:
		for k, item := range v {
			result[k] = item
		}
	case string:
		lines := strings.Split(v, "\n")
		for _, line := range lines {
			if idx := strings.Index(line, ":"); idx != -1 {
				k := strings.TrimSpace(line[:idx])
				val := strings.TrimSpace(line[idx+1:])
				if k != "" {
					result[k] = val
				}
			}
		}
	}
	return result
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
	dest := GetParamString(params, "dest", "")
	rawURL := GetParamString(params, "url", "")
	if strings.HasSuffix(dest, "/") {
		urlBase := path.Base(rawURL)
		if urlBase != "" && urlBase != "." && urlBase != "/" {
			dest = filepath.Join(dest, urlBase)
		}
	} else if client != nil && dest != "" {
		checkDirCmd := fmt.Sprintf("[ -d %s ]", shellQuote(dest))
		if res, err := client.Execute(checkDirCmd); err == nil && res != nil && res.ExitCode == 0 {
			urlBase := path.Base(rawURL)
			if urlBase != "" && urlBase != "." && urlBase != "/" {
				dest = filepath.Join(dest, urlBase)
			}
		}
	}

	spec := getURLSpec{
		URL:           rawURL,
		Dest:          dest,
		Checksum:      GetParamString(params, "checksum", ""),
		Mode:          GetParamMode(params, "mode", ""),
		Owner:         GetParamString(params, "owner", ""),
		Group:         GetParamString(params, "group", ""),
		Timeout:       GetParamInt(params, "timeout", 30),
		Force:         GetParamBool(params, "force", true),
		Backup:        GetParamBool(params, "backup", false),
		ValidateCerts: getOptionalBool(params, "validate_certs", true),
		URLUsername:   GetParamString(params, "url_username", GetParamString(params, "username", "")),
		URLPassword:   GetParamString(params, "url_password", GetParamString(params, "password", "")),
		Headers:       parseHeadersParam(params),
		TmpDest:       GetParamString(params, "tmp_dest", "/tmp"),
	}

	if IsCheckMode(params) {
		checksumAlgo := ""
		checksumValue := ""
		if spec.Checksum != "" {
			if algo, val, err := parseChecksumSpec(spec.Checksum); err == nil {
				checksumAlgo = algo
				checksumValue = val
			}
		}
		quotedDest := shellQuote(spec.Dest)
		checkCmd := fmt.Sprintf(`if [ ! -e %s ]; then echo "missing"; elif [ -d %s ]; then echo "isdir"; else echo "exists"; fi`, quotedDest, quotedDest)
		res, err := client.Execute(checkCmd)
		if err != nil {
			return nil, err
		}
		status := strings.TrimSpace(res.Stdout)
		if status == "missing" {
			return &Result{Changed: true, Message: "check mode: target file missing, would download"}, nil
		}
		if checksumAlgo != "" {
			verifyCmd := fmt.Sprintf(`if [ "$(%s 2>/dev/null)" = %s ]; then echo "match"; else echo "mismatch"; fi`,
				checksumCommandForAlgo(quotedDest, checksumAlgo), shellQuote(checksumValue))
			vRes, _ := client.Execute(verifyCmd)
			if strings.TrimSpace(vRes.Stdout) == "match" {
				return &Result{Changed: false, Message: "check mode: file exists and checksum matches"}, nil
			}
			return &Result{Changed: true, Message: "check mode: checksum mismatch, would download"}, nil
		}
		if !spec.Force {
			return &Result{Changed: false, Message: "check mode: file exists and force=false"}, nil
		}
		return &Result{Changed: true, Message: "check mode: file exists but would be re-downloaded (force=true)"}, nil
	}

	cmd, err := buildGetURLCommand(spec)
	if err != nil {
		return nil, err
	}

	result, err := client.Execute(cmd)
	if err != nil {
		return result, err
	}
	HandleChangedMarker(result)
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

	tmpDir := spec.TmpDest
	if tmpDir == "" {
		tmpDir = "/tmp"
	}
	tmpDir = strings.TrimRight(tmpDir, "/")

	quotedDest := shellQuote(spec.Dest)
	quotedURL := shellQuote(spec.URL)
	lines := []string{
		"set -e",
		fmt.Sprintf("timeout=%d", spec.Timeout),
		fmt.Sprintf(`tmp_file="$(mktemp "%s/go-ansible-geturl.XXXXXX")"`, tmpDir),
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
		buildDownloaderScript(spec.ValidateCerts, spec.URLUsername, spec.URLPassword, spec.Headers),
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
			fmt.Sprintf(`  %s`, buildBackupCommand(spec.Dest)),
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
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", "", fmt.Errorf("empty checksum specification")
	}

	algo, value, ok := strings.Cut(spec, ":")
	if !ok {
		// Ansible 官方规范：支持无算法前缀的裸 Hash，根据十六进制长度自动探测算法
		bare := strings.ToLower(spec)
		isHex := true
		for _, c := range bare {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				isHex = false
				break
			}
		}
		if isHex {
			switch len(bare) {
			case 32:
				return "md5", bare, nil
			case 40:
				return "sha1", bare, nil
			case 56:
				return "sha224", bare, nil
			case 64:
				return "sha256", bare, nil
			case 96:
				return "sha384", bare, nil
			case 128:
				return "sha512", bare, nil
			}
		}
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

func buildDownloaderScript(validateCerts bool, username, password string, headers map[string]string) string {
	curlExtra := ""
	wgetExtra := ""
	if !validateCerts {
		curlExtra += " -k"
		wgetExtra += " --no-check-certificate"
	}
	if username != "" || password != "" {
		curlExtra += fmt.Sprintf(" -u %s:%s", shellQuote(username), shellQuote(password))
		wgetExtra += fmt.Sprintf(" --http-user=%s --http-password=%s", shellQuote(username), shellQuote(password))
	}
	if len(headers) > 0 {
		keys := make([]string, 0, len(headers))
		for k := range headers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			hVal := fmt.Sprintf("%s: %s", k, headers[k])
			curlExtra += fmt.Sprintf(" -H %s", shellQuote(hVal))
			wgetExtra += fmt.Sprintf(" --header=%s", shellQuote(hVal))
		}
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
