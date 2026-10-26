package module

import (
	"fmt"
	"time"

	"go-ansible/pkg/ssh"
)

// WaitForConnectionModule wait_for_connection 模块，用于等待远程节点网络与连接就绪
type WaitForConnectionModule struct{}

func (m *WaitForConnectionModule) Name() string {
	return "wait_for_connection"
}

func (m *WaitForConnectionModule) Validate(params map[string]interface{}) error {
	return nil
}

func (m *WaitForConnectionModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	checkMode := GetParamBool(params, "_ansible_check_mode", false)
	delay := GetParamInt(params, "delay", 0)
	sleepSec := GetParamInt(params, "sleep", 1)
	timeout := GetParamInt(params, "timeout", 600)
	testCommand := GetParamString(params, "test_command", "echo ping")

	if sleepSec <= 0 {
		sleepSec = 1
	}

	if checkMode {
		return &Result{
			Changed: false,
			Message: "Connection check mode ok",
			Data: map[string]interface{}{
				"elapsed": 0,
			},
		}, nil
	}

	if delay > 0 {
		time.Sleep(time.Duration(delay) * time.Second)
	}

	startTime := time.Now()
	deadline := startTime.Add(time.Duration(timeout) * time.Second)

	for {
		if client != nil {
			err := client.Connect()
			if err == nil {
				res, execErr := client.Execute(testCommand)
				if execErr == nil && res != nil && res.ExitCode == 0 {
					elapsed := int(time.Since(startTime).Seconds())
					return &Result{
						Changed: false,
						Message: "Connection established successfully",
						Data: map[string]interface{}{
							"elapsed": elapsed,
						},
					}, nil
				}
			}
		}

		if time.Now().After(deadline) {
			elapsed := int(time.Since(startTime).Seconds())
			return &Result{
				Failed:  true,
				Message: fmt.Sprintf("Timed out waiting for connection after %d seconds", timeout),
				Data: map[string]interface{}{
					"elapsed": elapsed,
				},
			}, fmt.Errorf("timed out waiting for connection after %d seconds", timeout)
		}

		time.Sleep(time.Duration(sleepSec) * time.Second)
	}
}
