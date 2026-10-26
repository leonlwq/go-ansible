package module

import (
	"strings"
	"testing"
)

func TestDockerContainerValidate(t *testing.T) {
	mod := &DockerContainerModule{}

	if err := mod.Validate(map[string]interface{}{}); err == nil {
		t.Fatal("expected missing name validation error")
	}
	if err := mod.Validate(map[string]interface{}{
		"name":  "app",
		"image": "nginx:latest",
		"state": "started",
	}); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if err := mod.Validate(map[string]interface{}{
		"name":  "app",
		"state": "unknown",
	}); err == nil {
		t.Fatal("expected unsupported state validation error")
	}
}

func TestBuildDockerContainerSpec(t *testing.T) {
	spec := buildDockerContainerSpec(map[string]interface{}{
		"name":            "app",
		"image":           "nginx:latest",
		"restart":         true,
		"keep_volumes":    true,
		"published_ports": []interface{}{"8080:80", "8443:443"},
		"volumes":         "/srv/app:/app,/srv/log:/log",
		"env": map[string]interface{}{
			"APP_ENV": "prod",
			"DEBUG":   false,
		},
	})

	if spec.State != "restarted" {
		t.Fatalf("expected restart=true to normalize state restarted, got %q", spec.State)
	}
	if spec.RemoveVolumes {
		t.Fatal("expected keep_volumes=true to disable volume removal")
	}
	if len(spec.Ports) != 2 || spec.Ports[0] != "8080:80" || spec.Ports[1] != "8443:443" {
		t.Fatalf("unexpected ports: %#v", spec.Ports)
	}
	if len(spec.Volumes) != 2 || spec.Volumes[0] != "/srv/app:/app" || spec.Volumes[1] != "/srv/log:/log" {
		t.Fatalf("unexpected volumes: %#v", spec.Volumes)
	}
	if spec.Env["APP_ENV"] != "prod" || spec.Env["DEBUG"] != "false" {
		t.Fatalf("unexpected env: %#v", spec.Env)
	}
}

func TestBuildDockerContainerCreateArgs(t *testing.T) {
	args, err := buildDockerContainerCreateArgs(dockerContainerSpec{
		Name:    "app",
		Image:   "nginx:latest",
		Command: `nginx -g "daemon off;"`,
		Detach:  true,
		Ports:   []string{"8080:80"},
		Volumes: []string{"/srv/app:/app"},
		Env: map[string]string{
			"B": "2",
			"A": "1",
		},
	})
	if err != nil {
		t.Fatalf("buildDockerContainerCreateArgs() error = %v", err)
	}

	runCmd := buildDockerCLICommand(args.run)
	if !strings.Contains(runCmd, "'docker' 'run' '-d' '--name' 'app'") {
		t.Fatalf("unexpected docker run command: %q", runCmd)
	}
	if !strings.Contains(runCmd, "'-p' '8080:80'") || !strings.Contains(runCmd, "'-v' '/srv/app:/app'") {
		t.Fatalf("expected port and volume args, got %q", runCmd)
	}
	if strings.Index(runCmd, "'-e' 'A=1'") > strings.Index(runCmd, "'-e' 'B=2'") {
		t.Fatalf("expected env args sorted by key, got %q", runCmd)
	}
	if !strings.Contains(runCmd, "'nginx:latest' 'nginx' '-g' 'daemon off;'") {
		t.Fatalf("expected image and command args, got %q", runCmd)
	}

	createCmd := buildDockerCLICommand(args.create)
	if strings.Contains(createCmd, "'-d'") {
		t.Fatalf("docker create should not include detach flag, got %q", createCmd)
	}
}

func TestBuildDockerContainerCommandStarted(t *testing.T) {
	cmd, err := buildDockerContainerCommand(dockerContainerSpec{
		Name:          "app",
		Image:         "nginx:latest",
		State:         "started",
		Detach:        true,
		Pull:          true,
		RemoveVolumes: true,
		Ports:         []string{"8080:80"},
	})
	if err != nil {
		t.Fatalf("buildDockerContainerCommand() error = %v", err)
	}
	if !strings.Contains(cmd, "docker pull 'nginx:latest'") {
		t.Fatalf("expected pull command, got %q", cmd)
	}
	if !strings.Contains(cmd, "'docker' 'run' '-d' '--name' 'app' '-p' '8080:80' 'nginx:latest'") {
		t.Fatalf("expected docker run command, got %q", cmd)
	}
	if !strings.Contains(cmd, "docker start 'app'") {
		t.Fatalf("expected start idempotence command, got %q", cmd)
	}
	if !strings.Contains(cmd, changeMarkerTrue) || !strings.Contains(cmd, changeMarkerFalse) {
		t.Fatalf("expected change markers, got %q", cmd)
	}
}

func TestBuildDockerContainerCommandAbsent(t *testing.T) {
	cmd, err := buildDockerContainerCommand(dockerContainerSpec{
		Name:          "app",
		State:         "absent",
		RemoveVolumes: true,
	})
	if err != nil {
		t.Fatalf("buildDockerContainerCommand(absent) error = %v", err)
	}
	if !strings.Contains(cmd, "docker rm -f -v 'app'") {
		t.Fatalf("expected docker rm -f -v, got %q", cmd)
	}
}

func TestBuildDockerContainerCommandStoppedUsesCreate(t *testing.T) {
	cmd, err := buildDockerContainerCommand(dockerContainerSpec{
		Name:          "app",
		Image:         "nginx:latest",
		State:         "stopped",
		Detach:        true,
		RemoveVolumes: true,
	})
	if err != nil {
		t.Fatalf("buildDockerContainerCommand(stopped) error = %v", err)
	}
	if !strings.Contains(cmd, "'docker' 'create' '--name' 'app' 'nginx:latest'") {
		t.Fatalf("expected docker create for stopped missing container, got %q", cmd)
	}
	if !strings.Contains(cmd, "docker stop 'app'") {
		t.Fatalf("expected stop command for running container, got %q", cmd)
	}
}

func TestRegistryIncludesDockerContainerModule(t *testing.T) {
	registry := NewRegistry()
	if _, err := registry.Get("docker_container"); err != nil {
		t.Fatalf("expected docker_container module to be registered: %v", err)
	}
}
