package module

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go-ansible/pkg/ssh"
)

// AsyncStatusModule 异步任务状态查询与清理模块
type AsyncStatusModule struct{}

func (m *AsyncStatusModule) Name() string {
	return "async_status"
}

func (m *AsyncStatusModule) Validate(params map[string]interface{}) error {
	jid := GetParam(params, "jid", "")
	if strings.TrimSpace(jid) == "" {
		return fmt.Errorf("jid is required")
	}
	return nil
}

func (m *AsyncStatusModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	jid := strings.TrimSpace(GetParam(params, "jid", ""))
	if jid == "" {
		return nil, fmt.Errorf("jid is required")
	}

	mode := strings.ToLower(strings.TrimSpace(GetParam(params, "mode", "status")))
	if GetParam(params, "cleanup", false) {
		mode = "cleanup"
	}

	escapedJid := strings.ReplaceAll(jid, `'`, `'\''`)

	if mode == "cleanup" {
		cleanupCmd := fmt.Sprintf(`rm -f "$HOME/.ansible_async/%s"* "/tmp/.ansible_async/%s"* 2>/dev/null; echo "CLEANUP_DONE"`, escapedJid, escapedJid)
		res, err := client.Execute(cleanupCmd)
		if err != nil {
			return nil, fmt.Errorf("async cleanup failed: %w", err)
		}
		return &Result{
			Changed: false,
			Message: fmt.Sprintf("async job %s cleaned up", jid),
			Stdout:  strings.TrimSpace(res.Stdout),
			Data: map[string]interface{}{
				"ansible_job_id": jid,
				"finished":       1,
				"erased":         fmt.Sprintf("~/.ansible_async/%s", jid),
			},
		}, nil
	}

	// 模式为 status：查询任务状态
	queryScript := fmt.Sprintf(`
DIR="$HOME/.ansible_async"
if [ ! -f "$DIR/%s" ]; then
    DIR="/tmp/.ansible_async"
fi
if [ -f "$DIR/%s" ]; then
    cat "$DIR/%s"
    echo "---ANS_ASYNC_DATA---"
    if [ -f "$DIR/%s.out" ]; then cat "$DIR/%s.out"; fi
    echo "---ANS_ASYNC_ERR---"
    if [ -f "$DIR/%s.err" ]; then cat "$DIR/%s.err"; fi
else
    echo "NO_SUCH_JOB"
fi
`, escapedJid, escapedJid, escapedJid, escapedJid, escapedJid, escapedJid, escapedJid)

	queryRes, err := client.Execute(queryScript)
	if err != nil {
		return nil, fmt.Errorf("failed to query async job status: %w", err)
	}

	rawOutput := strings.TrimSpace(queryRes.Stdout)
	if rawOutput == "NO_SUCH_JOB" {
		return &Result{
			Failed:   true,
			ExitCode: 1,
			Message:  fmt.Sprintf("could not find async job %s", jid),
			Data: map[string]interface{}{
				"ansible_job_id": jid,
				"finished":       1,
				"failed":         true,
			},
		}, nil
	}

	parts := strings.Split(rawOutput, "---ANS_ASYNC_DATA---")
	if len(parts) < 2 {
		return &Result{
			Failed:   true,
			ExitCode: 1,
			Message:  fmt.Sprintf("invalid status file format for job %s: %s", jid, rawOutput),
		}, nil
	}

	statusJSONStr := strings.TrimSpace(parts[0])
	rest := parts[1]
	subParts := strings.Split(rest, "---ANS_ASYNC_ERR---")
	stdout := strings.TrimSpace(subParts[0])
	stderr := ""
	if len(subParts) > 1 {
		stderr = strings.TrimSpace(subParts[1])
	}

	var statusMap map[string]interface{}
	if err := json.Unmarshal([]byte(statusJSONStr), &statusMap); err != nil {
		return &Result{
			Failed:   true,
			ExitCode: 1,
			Message:  fmt.Sprintf("unmarshal async job status: %v, raw: %s", err, statusJSONStr),
		}, nil
	}

	finished := 0
	if fVal, ok := statusMap["finished"]; ok {
		switch f := fVal.(type) {
		case float64:
			finished = int(f)
		case int:
			finished = f
		case string:
			if i, err := strconv.Atoi(f); err == nil {
				finished = i
			}
		}
	}

	if finished == 1 {
		rc := 0
		if rcVal, ok := statusMap["rc"]; ok {
			switch r := rcVal.(type) {
			case float64:
				rc = int(r)
			case int:
				rc = r
			case string:
				if i, err := strconv.Atoi(r); err == nil {
					rc = i
				}
			}
		}

		statusMap["stdout"] = stdout
		statusMap["stderr"] = stderr
		statusMap["rc"] = rc
		statusMap["finished"] = 1
		statusMap["started"] = 1

		if rc != 0 {
			msg := fmt.Sprintf("async job failed with exit code %d", rc)
			if stderr != "" {
				msg = stderr
			}
			return &Result{
				Failed:   true,
				ExitCode: rc,
				Stdout:   stdout,
				Stderr:   stderr,
				Message:  msg,
				Data:     statusMap,
			}, nil
		}

		return &Result{
			Changed:  true,
			ExitCode: 0,
			Stdout:   stdout,
			Stderr:   stderr,
			Message:  fmt.Sprintf("async job %s finished successfully", jid),
			Data:     statusMap,
		}, nil
	}

	// finished == 0: 检查进程是否仍然存活
	pidVal := statusMap["pid"]
	if pidVal != nil {
		checkPidCmd := fmt.Sprintf("kill -0 %v 2>/dev/null; echo $?", pidVal)
		pidRes, _ := client.Execute(checkPidCmd)
		if strings.TrimSpace(pidRes.Stdout) != "0" {
			// 进程已退出，再次等待一瞬间重查是否刚写入 finished
			time.Sleep(200 * time.Millisecond)
			reQuery, _ := client.Execute(queryScript)
			if strings.Contains(reQuery.Stdout, `"finished":1`) || strings.Contains(reQuery.Stdout, `"finished": 1`) {
				// 递归或重新返回已完成状态
				return m.Execute(client, params)
			}
			// 进程异常消失但未写 finished
			return &Result{
				Failed:   true,
				ExitCode: 1,
				Message:  fmt.Sprintf("async job %s died unexpectedly", jid),
				Data: map[string]interface{}{
					"ansible_job_id": jid,
					"finished":       1,
					"failed":         true,
				},
			}, nil
		}
	}

	statusMap["finished"] = 0
	statusMap["started"] = 1
	return &Result{
		Changed: false,
		Message: fmt.Sprintf("async job %s is still running", jid),
		Data:    statusMap,
	}, nil
}
