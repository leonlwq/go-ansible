package module

import (
	"fmt"
	"strings"

	"go-ansible/pkg/ssh"
)

type ArchiveModule struct{}

type archiveSpec struct {
	Paths       []string
	ExcludePath []string
	Dest        string
	Format      string
	Remove      bool
	Backup      bool
	Mode        string
	Owner       string
	Group       string
}

func (m *ArchiveModule) Name() string { return "archive" }

func (m *ArchiveModule) Validate(params map[string]interface{}) error {
	spec := archiveSpec{
		Paths:       archivePaths(params),
		ExcludePath: archiveExcludePaths(params),
		Dest:        GetParamString(params, "dest", ""),
		Format:      normalizeArchiveFormat(GetParamString(params, "format", "")),
	}
	if len(spec.Paths) == 0 {
		return fmt.Errorf("archive module requires path")
	}
	if spec.Dest == "" {
		return fmt.Errorf("archive module requires dest")
	}
	_, err := createArchiveCommand(spec)
	return err
}

func (m *ArchiveModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	spec := archiveSpec{
		Paths:       archivePaths(params),
		ExcludePath: archiveExcludePaths(params),
		Dest:        GetParamString(params, "dest", ""),
		Format:      normalizeArchiveFormat(GetParamString(params, "format", "")),
		Remove:      GetParamBool(params, "remove", false),
		Backup:      GetParamBool(params, "backup", false),
		Mode:        GetParamMode(params, "mode", ""),
		Owner:       GetParamString(params, "owner", ""),
		Group:       GetParamString(params, "group", ""),
	}

	if IsCheckMode(params) {
		res, _ := client.Execute(fmt.Sprintf("test -e %s", shellQuote(spec.Dest)))
		destExists := res != nil && res.ExitCode == 0
		msg := "check mode: archive file would be created"
		if destExists {
			msg = "check mode: archive file would be updated"
		}
		return &Result{
			Changed: true,
			Message: msg,
		}, nil
	}

	cmd, err := buildArchiveCommand(spec)
	if err != nil {
		return nil, err
	}

	result, err := client.Execute(cmd)
	if err != nil {
		return result, err
	}
	HandleChangedMarker(result)
	if !result.Changed && (spec.Mode != "" || spec.Owner != "" || spec.Group != "") {
		permChanged, permErr := CheckOrSetFilePermissions(client, spec.Dest, spec.Mode, spec.Owner, spec.Group, false, false)
		if permErr != nil {
			return nil, fmt.Errorf("set archive file permissions: %w", permErr)
		}
		if permChanged {
			result.Changed = true
			result.Message = "archive permissions updated"
		}
	}
	return result, nil
}

func archiveExcludePaths(params map[string]interface{}) []string {
	raw, ok := params["exclude_path"]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s := strings.TrimSpace(fmt.Sprintf("%v", item))
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s := strings.TrimSpace(item)
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		s := strings.TrimSpace(fmt.Sprintf("%v", raw))
		if s == "" {
			return nil
		}
		parts := strings.Split(s, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
}

func archivePaths(params map[string]interface{}) []string {
	raw, ok := params["path"]
	if !ok {
		raw, ok = params["paths"]
		if !ok {
			return nil
		}
	}

	switch v := raw.(type) {
	case []interface{}:
		paths := make([]string, 0, len(v))
		for _, item := range v {
			path := strings.TrimSpace(fmt.Sprintf("%v", item))
			if path != "" {
				paths = append(paths, path)
			}
		}
		return paths
	case []string:
		paths := make([]string, 0, len(v))
		for _, item := range v {
			item = strings.TrimSpace(item)
			if item != "" {
				paths = append(paths, item)
			}
		}
		return paths
	default:
		value := strings.TrimSpace(fmt.Sprintf("%v", raw))
		if value == "" {
			return nil
		}
		return []string{value}
	}
}

func normalizeArchiveFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "gz", "tgz", "tar.gz":
		return "tar.gz"
	case "tar":
		return "tar"
	case "bz2", "tbz2", "tar.bz2":
		return "tar.bz2"
	case "xz", "txz", "tar.xz":
		return "tar.xz"
	case "zip":
		return "zip"
	default:
		return strings.ToLower(strings.TrimSpace(format))
	}
}

func buildArchiveCommand(spec archiveSpec) (string, error) {
	if len(spec.Paths) == 0 {
		return "", fmt.Errorf("archive module requires path")
	}
	if spec.Dest == "" {
		return "", fmt.Errorf("archive module requires dest")
	}

	createCmd, err := createArchiveCommand(spec)
	if err != nil {
		return "", err
	}

	quotedDest := shellQuote(spec.Dest)
	lines := []string{
		"set -e",
		fmt.Sprintf(`mkdir -p "$(dirname %s)"`, quotedDest),
		fmt.Sprintf(`tmp_archive="$(mktemp %s.XXXXXX)"`, quotedDest),
		`cleanup() { rm -f "$tmp_archive"; }`,
		`trap cleanup EXIT`,
		createCmd,
		fmt.Sprintf(`if [ -f %s ]; then`, quotedDest),
		fmt.Sprintf(`  existing_checksum="$(%s)"`, checksumCommandForAlgo(quotedDest, "sha256")),
		fmt.Sprintf(`  new_checksum="$(%s)"`, checksumCommandForAlgo(`"$tmp_archive"`, "sha256")),
		`  if [ "$existing_checksum" = "$new_checksum" ]; then`,
		fmt.Sprintf("    echo %s", changeMarkerFalse),
		"    exit 0",
		"  fi",
		"fi",
	}

	if spec.Backup {
		lines = append(lines,
			fmt.Sprintf(`if [ -e %s ]; then`, quotedDest),
			fmt.Sprintf(`  %s`, buildBackupCommand(spec.Dest)),
			`fi`,
		)
	}

	lines = append(lines,
		fmt.Sprintf(`mv "$tmp_archive" %s`, quotedDest),
		`tmp_archive=""`,
	)

	if spec.Remove {
		for _, path := range spec.Paths {
			lines = append(lines, fmt.Sprintf("rm -rf %s", shellQuote(path)))
		}
	}

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

func createArchiveCommand(spec archiveSpec) (string, error) {
	paths := make([]string, 0, len(spec.Paths))
	for _, path := range spec.Paths {
		paths = append(paths, shellQuote(path))
	}

	tarExcludes := ""
	zipExcludes := ""
	if len(spec.ExcludePath) > 0 {
		tParts := make([]string, 0, len(spec.ExcludePath))
		zParts := make([]string, 0, len(spec.ExcludePath))
		for _, ex := range spec.ExcludePath {
			tParts = append(tParts, "--exclude="+shellQuote(ex))
			zParts = append(zParts, shellQuote(ex))
		}
		tarExcludes = strings.Join(tParts, " ") + " "
		zipExcludes = " -x " + strings.Join(zParts, " ")
	}

	switch spec.Format {
	case "tar.gz":
		return fmt.Sprintf("tar -cf - %s%s | gzip -n > \"$tmp_archive\"", tarExcludes, strings.Join(paths, " ")), nil
	case "tar":
		return fmt.Sprintf("tar -cf \"$tmp_archive\" %s%s", tarExcludes, strings.Join(paths, " ")), nil
	case "tar.bz2":
		return fmt.Sprintf("tar -cf - %s%s | bzip2 -c > \"$tmp_archive\"", tarExcludes, strings.Join(paths, " ")), nil
	case "tar.xz":
		return fmt.Sprintf("tar -cf - %s%s | xz -c > \"$tmp_archive\"", tarExcludes, strings.Join(paths, " ")), nil
	case "zip":
		return fmt.Sprintf("zip -qr \"$tmp_archive\" %s%s", strings.Join(paths, " "), zipExcludes), nil
	default:
		return "", fmt.Errorf("unsupported archive format: %s", spec.Format)
	}
}
