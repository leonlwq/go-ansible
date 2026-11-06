package module

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"go-ansible/pkg/ssh"
	"go-ansible/pkg/util"
)

// RebootModule reboot 原生模块，用于声明式重启目标主机并等待恢复
type RebootModule struct{}

func (m *RebootModule) Name() string {
	return "reboot"
}

func (m *RebootModule) Validate(params map[string]interface{}) error {
	return nil
}

func (m *RebootModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	checkMode := GetParamBool(params, "_ansible_check_mode", false)
	rebootTimeout := GetParamInt(params, "reboot_timeout", 600)
	connectTimeout := GetParamInt(params, "connect_timeout", 5)
	postRebootDelay := GetParamInt(params, "post_reboot_delay", 0)
	preRebootDelay := GetParamInt(params, "pre_reboot_delay", 0)
	testCommand := GetParamString(params, "test_command", "whoami")
	searchRegex := GetParamString(params, "search_regex", "")
	bootTimeCommand := GetParamString(params, "boot_time_command", "cat /proc/sys/kernel/random/boot_id 2>/dev/null || uptime -s 2>/dev/null || who -b 2>/dev/null")
	rebootCommand := GetParamString(params, "reboot_command", "")
	msg := GetParamString(params, "msg", "Reboot initiated by Ansible")

	// 动态配置客户端单次探测连接超时并在退出时恢复
	if ts, ok := client.(interface{ SetTimeout(time.Duration) }); ok && connectTimeout > 0 {
		if gt, ok := client.(interface{ GetTimeout() time.Duration }); ok {
			origTimeout := gt.GetTimeout()
			defer ts.SetTimeout(origTimeout)
		}
		ts.SetTimeout(time.Duration(connectTimeout) * time.Second)
	}

	if checkMode {
		return &Result{
			Changed: true,
			Message: "Reboot would be initiated",
			Data: map[string]interface{}{
				"rebooted": true,
				"elapsed":  0,
				"msg":      msg,
			},
		}, nil
	}

	startTime := time.Now()

	// 1. 获取重启前 Boot ID
	var previousBootID string
	if client != nil {
		res, err := client.Execute(bootTimeCommand)
		if err == nil && res != nil && res.ExitCode == 0 {
			previousBootID = strings.TrimSpace(res.Stdout)
		}
	}

	// 2. Pre-reboot delay
	if preRebootDelay > 0 {
		time.Sleep(time.Duration(preRebootDelay) * time.Second)
	}

	// 3. 下发 reboot 命令（附带广播信息）
	cmd := rebootCommand
	if cmd == "" {
		cmd = fmt.Sprintf("shutdown -r now %s >/dev/null 2>&1 || /bin/sh -c 'sleep 1; reboot' >/dev/null 2>&1 &", util.Quote(msg))
	}
	if client != nil {
		_, _ = client.Execute(cmd)
		_ = client.Close()
	}

	// 4. 等待重启并轮询恢复上线
	deadline := startTime.Add(time.Duration(rebootTimeout) * time.Second)
	// 短暂休眠给系统处理下线
	time.Sleep(200 * time.Millisecond)

	var searchRe *regexp.Regexp
	if searchRegex != "" {
		searchRe, _ = util.GetRegex(searchRegex)
	}

	var rebootedSuccessfully bool
	for {
		if time.Now().After(deadline) {
			break
		}

		if client != nil {
			if err := client.Connect(); err == nil {
				res, err := client.Execute(testCommand)
				if err == nil && res != nil && res.ExitCode == 0 {
					passed := true
					if searchRe != nil {
						if !searchRe.MatchString(res.Stdout) {
							passed = false
						}
					}
					if passed && previousBootID != "" {
						bRes, bErr := client.Execute(bootTimeCommand)
						if bErr == nil && bRes != nil && bRes.ExitCode == 0 {
							if strings.TrimSpace(bRes.Stdout) == previousBootID {
								passed = false
							}
						}
					}
					if passed {
						rebootedSuccessfully = true
						break
					}
				}
			}
		}

		time.Sleep(500 * time.Millisecond)
	}

	elapsed := int(time.Since(startTime).Seconds())
	if !rebootedSuccessfully {
		return &Result{
			Failed:  true,
			Message: fmt.Sprintf("Timed out waiting for host to reboot after %d seconds", rebootTimeout),
			Data: map[string]interface{}{
				"rebooted": false,
				"elapsed":  elapsed,
				"msg":      msg,
			},
		}, fmt.Errorf("timed out waiting for host to reboot after %d seconds", rebootTimeout)
	}

	// 5. Post-reboot delay
	if postRebootDelay > 0 {
		time.Sleep(time.Duration(postRebootDelay) * time.Second)
	}

	return &Result{
		Changed: true,
		Message: "Reboot succeeded",
		Data: map[string]interface{}{
			"rebooted": true,
			"elapsed":  elapsed,
			"msg":      msg,
		},
	}, nil
}
