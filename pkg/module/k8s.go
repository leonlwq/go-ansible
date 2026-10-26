package module

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go-ansible/pkg/ssh"
)

type K8sModule struct{}

type k8sSpec struct {
	State      string
	Src        string
	Definition string
	Namespace  string
	Kubeconfig string
	Context    string
}

func (m *K8sModule) Name() string { return "k8s" }

func (m *K8sModule) Validate(params map[string]interface{}) error {
	spec := buildK8sSpec(params)
	if spec.Src == "" && spec.Definition == "" {
		return fmt.Errorf("k8s module requires src or definition")
	}
	switch spec.State {
	case "present", "absent":
		return nil
	default:
		return fmt.Errorf("unsupported k8s state: %s", spec.State)
	}
}

func (m *K8sModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	spec := buildK8sSpec(params)

	localPath := spec.Src
	cleanupLocal := ""
	if spec.Definition != "" {
		tmpFile, err := os.CreateTemp("", "go-ansible-k8s-*.yml")
		if err != nil {
			return nil, fmt.Errorf("create temp manifest: %w", err)
		}
		cleanupLocal = tmpFile.Name()
		if _, err := tmpFile.WriteString(spec.Definition); err != nil {
			tmpFile.Close()
			os.Remove(cleanupLocal)
			return nil, fmt.Errorf("write temp manifest: %w", err)
		}
		if err := tmpFile.Close(); err != nil {
			os.Remove(cleanupLocal)
			return nil, fmt.Errorf("close temp manifest: %w", err)
		}
		localPath = cleanupLocal
	}
	if cleanupLocal != "" {
		defer os.Remove(cleanupLocal)
	}

	if _, err := os.Stat(localPath); err != nil {
		return nil, fmt.Errorf("stat manifest: %w", err)
	}

	remotePath := filepath.Join("/tmp", fmt.Sprintf("go-ansible-k8s-%d-%s", os.Getpid(), filepath.Base(localPath)))
	if err := client.Upload(localPath, remotePath); err != nil {
		return nil, fmt.Errorf("upload manifest: %w", err)
	}
	defer client.Execute(fmt.Sprintf("rm -f %s", shellQuote(remotePath)))

	cmd, err := buildK8sCommand(k8sSpec{
		State:      spec.State,
		Src:        remotePath,
		Namespace:  spec.Namespace,
		Kubeconfig: spec.Kubeconfig,
		Context:    spec.Context,
	})
	if err != nil {
		return nil, err
	}

	result, err := client.Execute(cmd)
	if err != nil {
		return result, err
	}
	result.Changed = k8sChanged(spec.State, result.Stdout, result.Stderr)
	return result, nil
}

func buildK8sSpec(params map[string]interface{}) k8sSpec {
	definition := GetParamString(params, "definition", "")
	if definition == "" {
		definition = GetParamString(params, "resource_definition", "")
	}
	return k8sSpec{
		State:      normalizeK8sState(GetParamString(params, "state", "present")),
		Src:        GetParamString(params, "src", ""),
		Definition: definition,
		Namespace:  GetParamString(params, "namespace", ""),
		Kubeconfig: GetParamString(params, "kubeconfig", ""),
		Context:    GetParamString(params, "context", ""),
	}
}

func normalizeK8sState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "", "present", "created":
		return "present"
	case "absent", "deleted":
		return "absent"
	default:
		return strings.ToLower(strings.TrimSpace(state))
	}
}

func buildK8sCommand(spec k8sSpec) (string, error) {
	if spec.Src == "" {
		return "", fmt.Errorf("k8s module requires src or definition")
	}
	args := []string{"kubectl"}
	if spec.Kubeconfig != "" {
		args = append(args, "--kubeconfig", spec.Kubeconfig)
	}
	if spec.Context != "" {
		args = append(args, "--context", spec.Context)
	}
	if spec.Namespace != "" {
		args = append(args, "-n", spec.Namespace)
	}

	switch spec.State {
	case "present":
		args = append(args, "apply", "-f", spec.Src)
	case "absent":
		args = append(args, "delete", "-f", spec.Src, "--ignore-not-found=true")
	default:
		return "", fmt.Errorf("unsupported k8s state: %s", spec.State)
	}

	return strings.Join([]string{
		"set -e",
		`if ! command -v kubectl >/dev/null 2>&1; then`,
		`  echo "kubectl is required for k8s module" >&2`,
		`  exit 1`,
		`fi`,
		buildDockerCLICommand(args),
	}, "\n"), nil
}

func k8sChanged(state, stdout, stderr string) bool {
	combined := strings.ToLower(strings.TrimSpace(stdout + "\n" + stderr))
	if combined == "" {
		return false
	}

	switch state {
	case "present":
		return !hasAnySubstring(combined, " unchanged", "unchanged")
	case "absent":
		return !hasAnySubstring(combined, "not found", "notfound", "no resources found")
	default:
		return true
	}
}
