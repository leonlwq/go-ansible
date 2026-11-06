package module

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"go-ansible/pkg/ssh"
)

type URIModule struct{}

type uriSpec struct {
	URL           string
	Method        string
	Headers       map[string]string
	Body          string
	StatusCodes   []int
	Timeout       int
	ReturnContent bool
	ValidateCerts bool
}

func (m *URIModule) Name() string { return "uri" }

func (m *URIModule) Validate(params map[string]interface{}) error {
	if strings.TrimSpace(GetParamString(params, "url", "")) == "" {
		return fmt.Errorf("uri module requires url")
	}
	if _, err := parseURIStatusCodes(params["status_code"]); err != nil {
		return err
	}
	return nil
}

func (m *URIModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	spec, err := newURISpec(params)
	if err != nil {
		return nil, err
	}
	cmd := buildURICommand(spec)

	result, err := client.Execute(cmd)
	if err != nil {
		return result, err
	}
	if result == nil {
		return result, nil
	}

	stdout := strings.TrimRight(result.Stdout, "\n")
	body, status, ok := splitURIOutput(stdout)
	if !ok {
		if result.Message == "" && result.Stderr != "" {
			result.Message = strings.TrimSpace(result.Stderr)
		}
		return result, nil
	}

	result.Stdout = ""
	result.Changed = false
	result.Data = map[string]interface{}{
		"status": status,
		"url":    spec.URL,
	}
	if spec.ReturnContent {
		result.Stdout = body
		result.Data["content"] = body
		var jsonData interface{}
		if err := json.Unmarshal([]byte(body), &jsonData); err == nil && jsonData != nil {
			result.Data["json"] = jsonData
		}
	}
	if statusAllowed(status, spec.StatusCodes) {
		result.Failed = false
		result.ExitCode = 0
		if result.Message == "" {
			result.Message = fmt.Sprintf("HTTP %d", status)
		}
		return result, nil
	}

	result.Failed = true
	result.ExitCode = 1
	result.Message = fmt.Sprintf("status code %d not in expected %s", status, formatStatusCodes(spec.StatusCodes))
	return result, nil
}

func newURISpec(params map[string]interface{}) (uriSpec, error) {
	statusCodes, err := parseURIStatusCodes(params["status_code"])
	if err != nil {
		return uriSpec{}, err
	}
	if len(statusCodes) == 0 {
		statusCodes = []int{200}
	}

	timeout := GetParamInt(params, "timeout", 30)
	if timeout <= 0 {
		timeout = 30
	}

	method := strings.ToUpper(strings.TrimSpace(GetParamString(params, "method", "GET")))
	if method == "" {
		method = "GET"
	}

	var body string
	if bVal, ok := params["body"]; ok && bVal != nil {
		switch bv := bVal.(type) {
		case string:
			body = bv
		case []byte:
			body = string(bv)
		case map[string]interface{}, []interface{}:
			if jsonBytes, err := json.Marshal(bv); err == nil {
				body = string(jsonBytes)
			} else {
				body = fmt.Sprintf("%v", bv)
			}
		default:
			body = fmt.Sprintf("%v", bv)
		}
	}
	if method == "GET" && body != "" {
		method = "POST"
	}

	return uriSpec{
		URL:           strings.TrimSpace(GetParamString(params, "url", "")),
		Method:        method,
		Headers:       parseURIHeaders(params["headers"]),
		Body:          body,
		StatusCodes:   statusCodes,
		Timeout:       timeout,
		ReturnContent: GetParamBool(params, "return_content", false),
		ValidateCerts: getOptionalBool(params, "validate_certs", true),
	}, nil
}

func parseURIHeaders(value interface{}) map[string]string {
	headers := map[string]string{}
	switch v := value.(type) {
	case map[string]string:
		for key, val := range v {
			key = strings.TrimSpace(key)
			if key != "" {
				headers[key] = val
			}
		}
	case map[string]interface{}:
		for key, val := range v {
			key = strings.TrimSpace(key)
			if key != "" {
				headers[key] = fmt.Sprintf("%v", val)
			}
		}
	}
	return headers
}

func parseURIStatusCodes(value interface{}) ([]int, error) {
	switch v := value.(type) {
	case nil:
		return nil, nil
	case int:
		return []int{v}, nil
	case int64:
		return []int{int(v)}, nil
	case float64:
		return []int{int(v)}, nil
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, nil
		}
		fields := strings.FieldsFunc(v, func(r rune) bool {
			return r == ',' || r == ' '
		})
		result := make([]int, 0, len(fields))
		for _, field := range fields {
			code, err := strconv.Atoi(strings.TrimSpace(field))
			if err != nil {
				return nil, fmt.Errorf("invalid uri status_code: %s", field)
			}
			result = append(result, code)
		}
		return result, nil
	case []int:
		return append([]int(nil), v...), nil
	case []interface{}:
		result := make([]int, 0, len(v))
		for _, item := range v {
			codes, err := parseURIStatusCodes(item)
			if err != nil {
				return nil, err
			}
			result = append(result, codes...)
		}
		return result, nil
	default:
		code, err := strconv.Atoi(strings.TrimSpace(fmt.Sprintf("%v", value)))
		if err != nil {
			return nil, fmt.Errorf("invalid uri status_code: %v", value)
		}
		return []int{code}, nil
	}
}

func buildURICommand(spec uriSpec) string {
	lines := []string{"set -e"}
	lines = append(lines, "body_file=")
	if spec.Body != "" {
		lines = append(lines,
			`body_file="$(mktemp /tmp/go-ansible-uri-body.XXXXXX)"`,
			`cleanup() { rm -f "$body_file"; }`,
			`trap cleanup EXIT`,
			fmt.Sprintf("cat > \"$body_file\" << '%s'\n%s\n%s", uriBodyMarker(spec.Body), spec.Body, uriBodyMarker(spec.Body)),
		)
	}

	args := []string{
		"curl",
		"-sS",
		"-L",
		"-X", shellQuote(spec.Method),
		"--connect-timeout", strconv.Itoa(spec.Timeout),
		"--max-time", strconv.Itoa(spec.Timeout),
	}
	if !spec.ValidateCerts {
		args = append(args, "-k")
	}
	for key, value := range spec.Headers {
		args = append(args, "-H", shellQuote(fmt.Sprintf("%s: %s", key, value)))
	}
	if spec.Body != "" {
		args = append(args, "--data-binary", "@$body_file")
	}
	args = append(args, "-w", shellQuote("\n__go_ansible_status__=%{http_code}"), shellQuote(spec.URL))
	lines = append(lines, strings.Join(args, " "))
	return strings.Join(lines, "\n")
}

func uriBodyMarker(body string) string {
	return "GOANSIBLE_URI_BODY_" + stringChecksum(body)[:12]
}

func splitURIOutput(output string) (string, int, bool) {
	marker := "\n__go_ansible_status__="
	index := strings.LastIndex(output, marker)
	if index < 0 {
		return "", 0, false
	}
	body := output[:index]
	statusText := strings.TrimSpace(output[index+len(marker):])
	status, err := strconv.Atoi(statusText)
	if err != nil {
		return "", 0, false
	}
	return body, status, true
}

func statusAllowed(status int, allowed []int) bool {
	for _, code := range allowed {
		if status == code {
			return true
		}
	}
	return false
}

func formatStatusCodes(codes []int) string {
	parts := make([]string, 0, len(codes))
	for _, code := range codes {
		parts = append(parts, strconv.Itoa(code))
	}
	return strings.Join(parts, ",")
}
