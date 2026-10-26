package module

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go-ansible/pkg/ssh"
)

type UnarchiveModule struct{}

type unarchiveSpec struct {
	Src       string
	Dest      string
	RemoteSrc bool
	Creates   string
}

func (m *UnarchiveModule) Name() string { return "unarchive" }

func (m *UnarchiveModule) Validate(params map[string]interface{}) error {
	if GetParamString(params, "src", "") == "" {
		return fmt.Errorf("unarchive module requires src")
	}
	if GetParamString(params, "dest", "") == "" {
		return fmt.Errorf("unarchive module requires dest")
	}
	_, err := archiveExtractCommand(GetParamString(params, "src", ""), GetParamString(params, "dest", ""))
	return err
}

func (m *UnarchiveModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	spec := unarchiveSpec{
		Src:       GetParamString(params, "src", ""),
		Dest:      GetParamString(params, "dest", ""),
		RemoteSrc: GetParamBool(params, "remote_src", false),
		Creates:   GetParamString(params, "creates", ""),
	}

	srcPath := spec.Src
	cleanupPath := ""
	if !spec.RemoteSrc {
		if _, err := os.Stat(spec.Src); err != nil {
			return nil, fmt.Errorf("stat local archive: %w", err)
		}
		tmpPath := filepath.Join("/tmp", fmt.Sprintf("go-ansible-unarchive-%d-%s", os.Getpid(), filepath.Base(spec.Src)))
		if err := client.Upload(spec.Src, tmpPath); err != nil {
			return nil, fmt.Errorf("upload archive: %w", err)
		}
		srcPath = tmpPath
		cleanupPath = tmpPath
	}
	if cleanupPath != "" {
		defer client.Execute(fmt.Sprintf("rm -f %s", shellQuote(cleanupPath)))
	}

	cmd, err := buildUnarchiveCommand(unarchiveSpec{
		Src:       srcPath,
		Dest:      spec.Dest,
		RemoteSrc: true,
		Creates:   spec.Creates,
	})
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

func buildUnarchiveCommand(spec unarchiveSpec) (string, error) {
	if spec.Src == "" {
		return "", fmt.Errorf("unarchive module requires src")
	}
	if spec.Dest == "" {
		return "", fmt.Errorf("unarchive module requires dest")
	}

	extractCmd, err := archiveExtractCommand(spec.Src, spec.Dest)
	if err != nil {
		return "", err
	}

	lines := []string{
		"set -e",
		fmt.Sprintf("mkdir -p %s", shellQuote(spec.Dest)),
	}
	if spec.Creates != "" {
		lines = append(lines,
			fmt.Sprintf("if [ -e %s ]; then", shellQuote(spec.Creates)),
			fmt.Sprintf("  echo %s", changeMarkerFalse),
			"  exit 0",
			"fi",
		)
	}

	lines = append(lines, extractCmd, fmt.Sprintf("echo %s", changeMarkerTrue))
	return strings.Join(lines, "\n"), nil
}

func archiveExtractCommand(src, dest string) (string, error) {
	quotedSrc := shellQuote(src)
	quotedDest := shellQuote(dest)

	switch {
	case strings.HasSuffix(src, ".tar.gz"), strings.HasSuffix(src, ".tgz"):
		return fmt.Sprintf("tar -xzf %s -C %s", quotedSrc, quotedDest), nil
	case strings.HasSuffix(src, ".tar.bz2"), strings.HasSuffix(src, ".tbz2"):
		return fmt.Sprintf("tar -xjf %s -C %s", quotedSrc, quotedDest), nil
	case strings.HasSuffix(src, ".tar.xz"), strings.HasSuffix(src, ".txz"):
		return fmt.Sprintf("tar -xJf %s -C %s", quotedSrc, quotedDest), nil
	case strings.HasSuffix(src, ".tar"):
		return fmt.Sprintf("tar -xf %s -C %s", quotedSrc, quotedDest), nil
	case strings.HasSuffix(src, ".zip"):
		return fmt.Sprintf("unzip -o %s -d %s", quotedSrc, quotedDest), nil
	default:
		return "", fmt.Errorf("unsupported archive format: %s", src)
	}
}
