package util

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// ExpandPath 统一展开路径中的用户家目录 (~ 或 ~user) 以及环境变量 ($VAR 或 ${VAR})。
func ExpandPath(path string) string {
	if path == "" {
		return path
	}

	// 1. 展开环境变量 (支持 $VAR 和 ${VAR})
	if strings.Contains(path, "$") {
		path = os.ExpandEnv(path)
	}

	// 2. 展开家目录
	if strings.HasPrefix(path, "~") {
		if path == "~" {
			if home, err := os.UserHomeDir(); err == nil {
				return home
			}
			return path
		}

		if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
			home, err := os.UserHomeDir()
			if err != nil {
				return path
			}
			return filepath.Join(home, path[2:])
		}

		// 处理 ~username/path 语法
		slashIdx := strings.IndexAny(path, "/\\")
		var username string
		var subPath string
		if slashIdx == -1 {
			username = path[1:]
			subPath = ""
		} else {
			username = path[1:slashIdx]
			subPath = path[slashIdx+1:]
		}

		if u, err := user.Lookup(username); err == nil {
			if subPath == "" {
				return u.HomeDir
			}
			return filepath.Join(u.HomeDir, subPath)
		}
	}

	return path
}

