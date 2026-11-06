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
	Owner     string
	Group     string
	Mode      string
	ExtraOpts []string
}

func (m *UnarchiveModule) Name() string { return "unarchive" }

func (m *UnarchiveModule) Validate(params map[string]interface{}) error {
	if GetParamString(params, "src", "") == "" {
		return fmt.Errorf("unarchive module requires src")
	}
	if GetParamString(params, "dest", "") == "" {
		return fmt.Errorf("unarchive module requires dest")
	}
	_, err := archiveExtractCommand(GetParamString(params, "src", ""), GetParamString(params, "dest", ""), nil)
	return err
}

func (m *UnarchiveModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	var extraOpts []string
	if rawOpts, ok := params["extra_opts"]; ok {
		switch v := rawOpts.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				extraOpts = strings.Fields(v)
			}
		case []string:
			extraOpts = v
		case []interface{}:
			for _, it := range v {
				extraOpts = append(extraOpts, fmt.Sprintf("%v", it))
			}
		}
	}

	spec := unarchiveSpec{
		Src:       GetParamString(params, "src", ""),
		Dest:      GetParamString(params, "dest", ""),
		RemoteSrc: GetParamBool(params, "remote_src", false),
		Creates:   GetParamString(params, "creates", ""),
		Owner:     GetParamString(params, "owner", ""),
		Group:     GetParamString(params, "group", ""),
		Mode:      GetParamMode(params, "mode", ""),
		ExtraOpts: extraOpts,
	}

	// 1. 远端 creates 预检：无论是 check 模式还是正常执行，若远端已存在 creates 指定目标，
	// 立即跳过解压（在 remote_src: false 时更可彻底避免将百兆/GB大文件白白上传网络后再删除）
	if spec.Creates != "" {
		res, _ := client.Execute(fmt.Sprintf("test -e %s", shellQuote(spec.Creates)))
		if res != nil && res.ExitCode == 0 {
			msg := fmt.Sprintf("%s exists, extraction skipped", spec.Creates)
			if IsCheckMode(params) {
				msg = fmt.Sprintf("check mode: %s exists, extraction skipped", spec.Creates)
			}
			return &Result{
				Changed: false,
				Message: msg,
			}, nil
		}
	}

	if IsCheckMode(params) {
		return &Result{
			Changed: true,
			Message: "check mode: archive would be extracted",
		}, nil
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
		ExtraOpts: spec.ExtraOpts,
	})
	if err != nil {
		return nil, err
	}

	result, err := client.Execute(cmd)
	if err != nil {
		return result, err
	}
	HandleChangedMarker(result)
	if spec.Owner != "" || spec.Group != "" || spec.Mode != "" {
		if _, err := SetFilePermissions(client, spec.Dest, spec.Mode, spec.Owner, spec.Group, true); err != nil {
			return nil, fmt.Errorf("set permissions after unarchive: %w", err)
		}
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

	extractCmd, err := archiveExtractCommand(spec.Src, spec.Dest, spec.ExtraOpts)
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

func archiveExtractCommand(src, dest string, extraOpts ...[]string) (string, error) {
	quotedSrc := shellQuote(src)
	quotedDest := shellQuote(dest)
	var opts []string
	if len(extraOpts) > 0 && len(extraOpts[0]) > 0 {
		opts = extraOpts[0]
	}
	extra := ""
	if len(opts) > 0 {
		extra = " " + strings.Join(opts, " ")
	}

	switch {
	case strings.HasSuffix(src, ".tar.gz"), strings.HasSuffix(src, ".tgz"):
		return fmt.Sprintf("tar -xzf %s -C %s%s", quotedSrc, quotedDest, extra), nil
	case strings.HasSuffix(src, ".tar.bz2"), strings.HasSuffix(src, ".tbz2"):
		return fmt.Sprintf("tar -xjf %s -C %s%s", quotedSrc, quotedDest, extra), nil
	case strings.HasSuffix(src, ".tar.xz"), strings.HasSuffix(src, ".txz"):
		return fmt.Sprintf("tar -xJf %s -C %s%s", quotedSrc, quotedDest, extra), nil
	case strings.HasSuffix(src, ".tar"):
		return fmt.Sprintf("tar -xf %s -C %s%s", quotedSrc, quotedDest, extra), nil
	case strings.HasSuffix(src, ".zip"):
		return fmt.Sprintf("unzip -o %s -d %s%s", quotedSrc, quotedDest, extra), nil
	default:
		return "", fmt.Errorf("unsupported archive format: %s", src)
	}
}
