package module

import (
	"strings"
	"testing"
)

func TestK8sValidate(t *testing.T) {
	mod := &K8sModule{}

	if err := mod.Validate(map[string]interface{}{}); err == nil {
		t.Fatal("expected missing src/definition validation error")
	}
	if err := mod.Validate(map[string]interface{}{
		"src":   "deployment.yml",
		"state": "present",
	}); err != nil {
		t.Fatalf("Validate(src) error = %v", err)
	}
	if err := mod.Validate(map[string]interface{}{
		"definition": "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: app\n",
		"state":      "absent",
	}); err != nil {
		t.Fatalf("Validate(definition) error = %v", err)
	}
	if err := mod.Validate(map[string]interface{}{
		"src":   "deployment.yml",
		"state": "patched",
	}); err == nil {
		t.Fatal("expected unsupported state validation error")
	}
}

func TestBuildK8sSpec(t *testing.T) {
	spec := buildK8sSpec(map[string]interface{}{
		"resource_definition": "kind: ConfigMap",
		"namespace":           "demo",
		"kubeconfig":          "/tmp/kubeconfig",
		"context":             "test",
	})

	if spec.Definition != "kind: ConfigMap" {
		t.Fatalf("expected definition from resource_definition, got %q", spec.Definition)
	}
	if spec.State != "present" {
		t.Fatalf("expected default state present, got %q", spec.State)
	}
	if spec.Namespace != "demo" || spec.Kubeconfig != "/tmp/kubeconfig" || spec.Context != "test" {
		t.Fatalf("unexpected spec: %#v", spec)
	}
}

func TestBuildK8sCommandApply(t *testing.T) {
	cmd, err := buildK8sCommand(k8sSpec{
		State:      "present",
		Src:        "/tmp/deployment.yml",
		Namespace:  "demo",
		Kubeconfig: "/tmp/kubeconfig",
		Context:    "test",
	})
	if err != nil {
		t.Fatalf("buildK8sCommand() error = %v", err)
	}
	if !strings.Contains(cmd, "command -v kubectl") {
		t.Fatalf("expected kubectl presence check, got %q", cmd)
	}
	if !strings.Contains(cmd, "'kubectl' '--kubeconfig' '/tmp/kubeconfig' '--context' 'test' '-n' 'demo' 'apply' '-f' '/tmp/deployment.yml'") {
		t.Fatalf("unexpected apply command: %q", cmd)
	}
}

func TestBuildK8sCommandDelete(t *testing.T) {
	cmd, err := buildK8sCommand(k8sSpec{
		State: "absent",
		Src:   "/tmp/deployment.yml",
	})
	if err != nil {
		t.Fatalf("buildK8sCommand(delete) error = %v", err)
	}
	if !strings.Contains(cmd, "'kubectl' 'delete' '-f' '/tmp/deployment.yml' '--ignore-not-found=true'") {
		t.Fatalf("unexpected delete command: %q", cmd)
	}
}

func TestK8sChanged(t *testing.T) {
	if k8sChanged("present", "deployment.apps/web unchanged", "") {
		t.Fatal("expected unchanged apply output to be unchanged")
	}
	if !k8sChanged("present", "deployment.apps/web configured", "") {
		t.Fatal("expected configured apply output to be changed")
	}
	if k8sChanged("absent", "", "Error from server (NotFound)") {
		t.Fatal("expected not found delete output to be unchanged")
	}
	if !k8sChanged("absent", "deployment.apps \"web\" deleted", "") {
		t.Fatal("expected deleted output to be changed")
	}
}

func TestRegistryIncludesK8sModule(t *testing.T) {
	registry := NewRegistry()
	if _, err := registry.Get("k8s"); err != nil {
		t.Fatalf("expected k8s module to be registered: %v", err)
	}
}
