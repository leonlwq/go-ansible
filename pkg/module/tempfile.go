package module

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"go-ansible/pkg/ssh"
	"go-ansible/pkg/util"
)

// TempfileModule 实现了 ansible.builtin.tempfile 核心功能
type TempfileModule struct{}

func (m *TempfileModule) Name() string {
	return "tempfile"
}

func (m *TempfileModule) Validate(params map[string]interface{}) error {
	state := strings.ToLower(strings.TrimSpace(GetParamString(params, "state", "file")))
	if state == "" {
		state = "file"
	}
	if state != "file" && state != "directory" {
		return fmt.Errorf("unsupported state: %s (must be 'file' or 'directory')", state)
	}
	return nil
}

func (m *TempfileModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	state := strings.ToLower(strings.TrimSpace(GetParamString(params, "state", "file")))
	if state == "" {
		state = "file"
	}
	baseDir := strings.TrimSpace(GetParamString(params, "path", ""))
	prefix := GetParamString(params, "prefix", "ansible.")
	suffix := GetParamString(params, "suffix", "")

	checkMode := IsCheckMode(params)

	if baseDir == "" {
		baseDir = "/tmp"
	}

	if checkMode {
		predicted := filepath.Join(baseDir, fmt.Sprintf("%sCHECKMODE%d%s", prefix, time.Now().UnixNano()%100000, suffix))
		return &Result{
			Changed: true,
			Message: fmt.Sprintf("check mode: tempfile would create %s (%s)", state, predicted),
			Data: map[string]interface{}{
				"path": predicted,
			},
		}, nil
	}

	// 构造多平台安全创建脚本 (Linux, macOS, Alpine/BusyBox, BSD)
	script := fmt.Sprintf(`
DIR=%s
STATE=%s
PREFIX=%s
SUFFIX=%s

mkdir -p "$DIR" 2>/dev/null || true

FLAG=""
if [ "$STATE" = "directory" ]; then
    FLAG="-d"
fi

# 1. 尝试 mktemp 创建基准唯一路径
BASE=$(mktemp $FLAG "${DIR}/${PREFIX}XXXXXX" 2>/dev/null || mktemp $FLAG -p "$DIR" "${PREFIX}XXXXXX" 2>/dev/null || true)

if [ -z "$BASE" ]; then
    # 回退机制: 生成唯一时间戳与随机因子
    RND="$(date +%%s%%N 2>/dev/null || date +%%s)$$"
    BASE="${DIR}/${PREFIX}${RND}"
    if [ "$STATE" = "directory" ]; then
        mkdir -p "$BASE"
    else
        touch "$BASE"
    fi
fi

# 2. 如果指定了 suffix，原子重命名追加后缀
TARGET="$BASE"
if [ -n "$SUFFIX" ]; then
    TARGET="${BASE}${SUFFIX}"
    mv -f "$BASE" "$TARGET"
fi

# 3. 严格设置受限权限 (目录 0700，文件 0600)
if [ "$STATE" = "directory" ]; then
    chmod 700 "$TARGET"
else
    chmod 600 "$TARGET"
fi

echo "$TARGET"
`, util.Quote(baseDir), util.Quote(state), util.Quote(prefix), util.Quote(suffix))

	res, err := client.Execute(script)
	if err != nil {
		return nil, fmt.Errorf("execute tempfile creation: %w", err)
	}

	createdPath := ""
	if res != nil {
		createdPath = strings.TrimSpace(res.Stdout)
	}

	if createdPath == "" {
		stderr := ""
		if res != nil {
			stderr = strings.TrimSpace(res.Stderr)
		}
		return nil, fmt.Errorf("failed to create temporary %s: %s", state, stderr)
	}

	return &Result{
		Changed: true,
		Message: fmt.Sprintf("temporary %s created", state),
		Data: map[string]interface{}{
			"path": createdPath,
		},
	}, nil
}
