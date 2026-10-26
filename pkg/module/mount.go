package module

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"go-ansible/pkg/ssh"
)

// MountModule mount 模块
type MountModule struct{}

func (m *MountModule) Name() string { return "mount" }

func (m *MountModule) Validate(params map[string]interface{}) error {
	path := strings.TrimSpace(GetParamString(params, "path", ""))
	if path == "" {
		return fmt.Errorf("mount module requires path")
	}

	state := strings.ToLower(strings.TrimSpace(GetParamString(params, "state", "")))
	if state == "" {
		return fmt.Errorf("mount module requires state (mounted, unmounted, present, absent, remounted)")
	}

	validStates := map[string]bool{
		"mounted":   true,
		"unmounted": true,
		"present":   true,
		"absent":    true,
		"remounted": true,
	}
	if !validStates[state] {
		return fmt.Errorf("invalid mount state: %s", state)
	}

	if state == "mounted" || state == "present" {
		src := strings.TrimSpace(GetParamString(params, "src", ""))
		fstype := strings.TrimSpace(GetParamString(params, "fstype", ""))
		if src == "" {
			return fmt.Errorf("mount module requires src when state=%s", state)
		}
		if fstype == "" {
			return fmt.Errorf("mount module requires fstype when state=%s", state)
		}
	}

	return nil
}

type fstabEntry struct {
	Src     string
	Path    string
	Fstype  string
	Opts    string
	Dump    int
	Passno  int
	RawLine string
	IsEntry bool
}

func parseFstab(content string) []fstabEntry {
	lines := strings.Split(content, "\n")
	entries := make([]fstabEntry, 0, len(lines))

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			entries = append(entries, fstabEntry{
				RawLine: line,
				IsEntry: false,
			})
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 4 {
			entries = append(entries, fstabEntry{
				RawLine: line,
				IsEntry: false,
			})
			continue
		}

		dump := 0
		passno := 0
		if len(fields) >= 5 {
			dump, _ = strconv.Atoi(fields[4])
		}
		if len(fields) >= 6 {
			passno, _ = strconv.Atoi(fields[5])
		}

		entries = append(entries, fstabEntry{
			Src:     fields[0],
			Path:    filepath.Clean(fields[1]),
			Fstype:  fields[2],
			Opts:    fields[3],
			Dump:    dump,
			Passno:  passno,
			RawLine: line,
			IsEntry: true,
		})
	}

	return entries
}

func serializeFstab(entries []fstabEntry) string {
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsEntry {
			lines = append(lines, e.RawLine)
		} else {
			lines = append(lines, fmt.Sprintf("%-23s %-23s %-7s %-15s %d %d", e.Src, e.Path, e.Fstype, e.Opts, e.Dump, e.Passno))
		}
	}
	res := strings.Join(lines, "\n")
	if !strings.HasSuffix(res, "\n") && len(res) > 0 {
		res += "\n"
	}
	return res
}

func checkMountPointMounted(client ssh.Connection, path string) (bool, string, error) {
	cleanPath := filepath.Clean(path)
	cmd := "cat /proc/mounts 2>/dev/null || mount"
	res, err := client.Execute(cmd)
	if err != nil || res == nil || res.ExitCode != 0 {
		return false, "", err
	}

	lines := strings.Split(res.Stdout, "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			mp := strings.ReplaceAll(fields[1], "\\040", " ")
			if filepath.Clean(mp) == cleanPath {
				return true, fields[0], nil
			}
		}
	}
	return false, "", nil
}

func (m *MountModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	path := filepath.Clean(strings.TrimSpace(GetParamString(params, "path", "")))
	src := strings.TrimSpace(GetParamString(params, "src", ""))
	fstype := strings.TrimSpace(GetParamString(params, "fstype", ""))
	opts := GetParamString(params, "opts", "defaults")
	state := strings.ToLower(strings.TrimSpace(GetParamString(params, "state", "")))
	fstabPath := GetParamString(params, "fstab", "/etc/fstab")
	boot := GetParamBool(params, "boot", true)

	dump := 0
	if d, ok := params["dump"]; ok {
		if i, err := strconv.Atoi(fmt.Sprintf("%v", d)); err == nil {
			dump = i
		}
	}
	passno := 0
	if p, ok := params["passno"]; ok {
		if i, err := strconv.Atoi(fmt.Sprintf("%v", p)); err == nil {
			passno = i
		}
	}

	checkMode := GetParamBool(params, "_ansible_check_mode", false)
	diffMode := GetParamBool(params, "_ansible_diff", false)

	// 1. 查询当前挂载状态
	isMounted, currentDev, _ := checkMountPointMounted(client, path)

	// 2. 读取并解析 fstab
	beforeFstab := readRemoteFileContent(client, fstabPath)
	entries := parseFstab(beforeFstab)

	fstabIdx := -1
	for i, e := range entries {
		if e.IsEntry && e.Path == path {
			fstabIdx = i
			break
		}
	}

	fstabChanged := false
	mountChanged := false

	switch state {
	case "present":
		// 仅维护 fstab
		if fstabIdx == -1 {
			entries = append(entries, fstabEntry{
				Src:     src,
				Path:    path,
				Fstype:  fstype,
				Opts:    opts,
				Dump:    dump,
				Passno:  passno,
				IsEntry: true,
			})
			fstabChanged = true
		} else {
			old := entries[fstabIdx]
			if old.Src != src || old.Fstype != fstype || old.Opts != opts || old.Dump != dump || old.Passno != passno {
				entries[fstabIdx] = fstabEntry{
					Src:     src,
					Path:    path,
					Fstype:  fstype,
					Opts:    opts,
					Dump:    dump,
					Passno:  passno,
					IsEntry: true,
				}
				fstabChanged = true
			}
		}

	case "mounted":
		if boot {
			if fstabIdx == -1 {
				entries = append(entries, fstabEntry{
					Src:     src,
					Path:    path,
					Fstype:  fstype,
					Opts:    opts,
					Dump:    dump,
					Passno:  passno,
					IsEntry: true,
				})
				fstabChanged = true
			} else {
				old := entries[fstabIdx]
				if old.Src != src || old.Fstype != fstype || old.Opts != opts || old.Dump != dump || old.Passno != passno {
					entries[fstabIdx] = fstabEntry{
						Src:     src,
						Path:    path,
						Fstype:  fstype,
						Opts:    opts,
						Dump:    dump,
						Passno:  passno,
						IsEntry: true,
					}
					fstabChanged = true
				}
			}
		}

		if !isMounted {
			mountChanged = true
		}

	case "unmounted":
		if isMounted {
			mountChanged = true
		}

	case "absent":
		if isMounted {
			mountChanged = true
		}
		if fstabIdx != -1 {
			entries = append(entries[:fstabIdx], entries[fstabIdx+1:]...)
			fstabChanged = true
		}

	case "remounted":
		if isMounted {
			mountChanged = true
		} else {
			mountChanged = true
		}
	}

	afterFstab := beforeFstab
	if fstabChanged {
		afterFstab = serializeFstab(entries)
	}

	changed := fstabChanged || mountChanged

	diff := ""
	if diffMode && fstabChanged {
		diff = buildTextDiff(fstabPath, beforeFstab, afterFstab)
	}

	if checkMode {
		return &Result{
			Changed:  changed,
			Diff:     diff,
			ExitCode: 0,
			Message:  fmt.Sprintf("check mode: mount %s (state=%s, fstab_changed=%v, mount_changed=%v)", path, state, fstabChanged, mountChanged),
			Data: map[string]interface{}{
				"path":       path,
				"src":        src,
				"fstype":     fstype,
				"state":      state,
				"is_mounted": isMounted,
			},
		}, nil
	}

	if !changed {
		return &Result{
			Changed:  false,
			ExitCode: 0,
			Message:  fmt.Sprintf("mount %s already in desired state (%s)", path, state),
			Data: map[string]interface{}{
				"path":       path,
				"src":        src,
				"fstype":     fstype,
				"state":      state,
				"is_mounted": isMounted,
			},
		}, nil
	}

	// 3. 执行 fstab 写入
	if fstabChanged {
		writeCmd := buildContentWriteCommand(fstabPath, afterFstab)
		if writeCmd != "" {
			if writeRes, err := client.Execute(writeCmd); err != nil || (writeRes != nil && writeRes.ExitCode != 0) {
				errMsg := ""
				if writeRes != nil {
					errMsg = writeRes.Stderr
				}
				return &Result{
					Failed:   true,
					ExitCode: 1,
					Message:  fmt.Sprintf("failed to update fstab file %s: %s", fstabPath, errMsg),
				}, fmt.Errorf("write %s: %v %s", fstabPath, err, errMsg)
			}
		}
	}

	// 4. 执行挂载 / 卸载动作
	switch state {
	case "mounted":
		if !isMounted {
			// 创建挂载点目录
			_, _ = client.Execute(fmt.Sprintf("mkdir -p %s", shellQuote(path)))
			// 挂载
			mountCmd := fmt.Sprintf("mount %s 2>/dev/null || mount -t %s -o %s %s %s", shellQuote(path), shellQuote(fstype), shellQuote(opts), shellQuote(src), shellQuote(path))
			if mRes, err := client.Execute(mountCmd); err != nil || (mRes != nil && mRes.ExitCode != 0) {
				errMsg := ""
				if mRes != nil {
					errMsg = mRes.Stderr
				}
				return &Result{
					Failed:   true,
					ExitCode: 1,
					Message:  fmt.Sprintf("failed to mount %s: %s", path, errMsg),
				}, fmt.Errorf("mount %s: %v %s", path, err, errMsg)
			}
		}
	case "unmounted", "absent":
		if isMounted {
			umountCmd := fmt.Sprintf("umount %s", shellQuote(path))
			if uRes, err := client.Execute(umountCmd); err != nil || (uRes != nil && uRes.ExitCode != 0) {
				errMsg := ""
				if uRes != nil {
					errMsg = uRes.Stderr
				}
				return &Result{
					Failed:   true,
					ExitCode: 1,
					Message:  fmt.Sprintf("failed to unmount %s: %s", path, errMsg),
				}, fmt.Errorf("umount %s: %v %s", path, err, errMsg)
			}
		}
	case "remounted":
		if isMounted {
			remountCmd := fmt.Sprintf("mount -o remount,%s %s", opts, shellQuote(path))
			if rRes, err := client.Execute(remountCmd); err != nil || (rRes != nil && rRes.ExitCode != 0) {
				errMsg := ""
				if rRes != nil {
					errMsg = rRes.Stderr
				}
				return &Result{
					Failed:   true,
					ExitCode: 1,
					Message:  fmt.Sprintf("failed to remount %s: %s", path, errMsg),
				}, fmt.Errorf("remount %s: %v %s", path, err, errMsg)
			}
		} else {
			_, _ = client.Execute(fmt.Sprintf("mkdir -p %s", shellQuote(path)))
			mountCmd := fmt.Sprintf("mount -t %s -o %s %s %s", shellQuote(fstype), shellQuote(opts), shellQuote(src), shellQuote(path))
			if mRes, err := client.Execute(mountCmd); err != nil || (mRes != nil && mRes.ExitCode != 0) {
				errMsg := ""
				if mRes != nil {
					errMsg = mRes.Stderr
				}
				return &Result{
					Failed:   true,
					ExitCode: 1,
					Message:  fmt.Sprintf("failed to mount %s on remount: %s", path, errMsg),
				}, fmt.Errorf("mount on remount: %v %s", err, errMsg)
			}
		}
	}

	return &Result{
		Changed:  true,
		Diff:     diff,
		ExitCode: 0,
		Message:  fmt.Sprintf("mount %s successfully updated to state=%s", path, state),
		Data: map[string]interface{}{
			"path":       path,
			"src":        src,
			"fstype":     fstype,
			"state":      state,
			"old_device": currentDev,
		},
	}, nil
}
