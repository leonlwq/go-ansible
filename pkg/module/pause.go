package module

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"go-ansible/pkg/ssh"
)

// PauseModule 实现 Ansible pause 模块
type PauseModule struct {
	Stdin  io.Reader
	Stdout io.Writer
}

func (m *PauseModule) Name() string {
	return "pause"
}

func (m *PauseModule) Validate(params map[string]interface{}) error {
	return nil
}

func (m *PauseModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	checkMode := false
	if cm, ok := params["check_mode"].(bool); ok {
		checkMode = cm
	}

	echo := true
	if eVal, ok := params["echo"]; ok {
		switch v := eVal.(type) {
		case bool:
			echo = v
		case string:
			echo = strings.ToLower(v) == "true" || v == "yes" || v == "1"
		}
	}

	var prompt string
	if p, ok := params["prompt"].(string); ok {
		prompt = p
	}

	var seconds float64
	if sVal, ok := params["seconds"]; ok {
		seconds = parseDurationNumber(sVal)
	}

	var minutes float64
	if mVal, ok := params["minutes"]; ok {
		minutes = parseDurationNumber(mVal)
	}

	totalSeconds := seconds + minutes*60.0

	stdin := m.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stdout := m.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}

	startTime := time.Now()

	// Check Mode 直接跳过
	if checkMode {
		return &Result{
			Changed: false,
			Message: "Paused (check mode skipped)",
			Data: map[string]interface{}{
				"user_input": "",
				"echo":       echo,
				"delta":      0,
				"start":      startTime.Format(time.RFC3339),
				"stop":       time.Now().Format(time.RFC3339),
			},
		}, nil
	}

	var userInput string

	if totalSeconds > 0 {
		// 定时休眠模式
		if prompt != "" {
			fmt.Fprintf(stdout, "%s (waiting %v seconds)...\n", prompt, totalSeconds)
		} else {
			fmt.Fprintf(stdout, "Pausing for %v seconds...\n", totalSeconds)
		}

		time.Sleep(time.Duration(totalSeconds * float64(time.Second)))
	} else {
		// 用户交互输入模式
		if prompt != "" {
			fmt.Fprintf(stdout, "%s ", prompt)
		} else {
			fmt.Fprintf(stdout, "Press enter to continue: ")
		}

		reader := bufio.NewReader(stdin)
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("reading pause input: %w", err)
		}
		userInput = strings.TrimRight(line, "\r\n")
	}

	stopTime := time.Now()
	deltaSeconds := int(stopTime.Sub(startTime).Seconds())

	return &Result{
		Changed: false,
		Stdout:  userInput,
		Message: fmt.Sprintf("Paused for %d seconds", deltaSeconds),
		Data: map[string]interface{}{
			"user_input": userInput,
			"echo":       echo,
			"delta":      deltaSeconds,
			"start":      startTime.Format(time.RFC3339),
			"stop":       stopTime.Format(time.RFC3339),
		},
	}, nil
}

func parseDurationNumber(val interface{}) float64 {
	switch v := val.(type) {
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case float64:
		return v
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
	}
	return 0
}
