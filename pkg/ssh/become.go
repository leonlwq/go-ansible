package ssh

import (
	"fmt"
	"strings"

	"go-ansible/pkg/inventory"
	"go-ansible/pkg/util"
)

// BuildBecomeCommand 根据主机 Become 属性构造提权包裹命令 (sudo / su / 自定义 become_exe)
func BuildBecomeCommand(cmd string, host *inventory.Host) string {
	becomeUser := "root"
	becomeMethod := "sudo"
	becomePass := ""
	becomeExe := "sudo"
	becomeFlags := ""
	if host != nil {
		if host.BecomeUser != "" {
			becomeUser = host.BecomeUser
		}
		if host.BecomeMethod != "" {
			becomeMethod = host.BecomeMethod
		}
		becomePass = host.BecomePass
		if host.BecomeExe != "" {
			becomeExe = host.BecomeExe
		}
		if host.BecomeFlags != "" {
			becomeFlags = " " + strings.TrimSpace(host.BecomeFlags)
		}
	}

	quotedCmd := util.Quote(cmd)

	if becomeMethod == "sudo" {
		if becomePass != "" {
			return fmt.Sprintf("printf '%%s\\n' %s | sudo -S -p ''%s -u %s /bin/sh -c %s", util.Quote(becomePass), becomeFlags, becomeUser, quotedCmd)
		}
		return fmt.Sprintf("sudo -n%s -u %s /bin/sh -c %s", becomeFlags, becomeUser, quotedCmd)
	} else if becomeMethod == "su" {
		if becomePass != "" {
			return fmt.Sprintf("printf '%%s\\n' %s | su%s - %s -c %s", util.Quote(becomePass), becomeFlags, becomeUser, quotedCmd)
		}
		return fmt.Sprintf("su%s - %s -c %s", becomeFlags, becomeUser, quotedCmd)
	}
	if becomeFlags != "" {
		return fmt.Sprintf("%s%s /bin/sh -c %s", becomeExe, becomeFlags, quotedCmd)
	}
	return fmt.Sprintf("%s /bin/sh -c %s", becomeExe, quotedCmd)
}
