package module

import (
	"fmt"
	"sort"
	"strings"

	"go-ansible/pkg/ssh"
	"go-ansible/pkg/util"
)

type DockerContainerModule struct{}

type dockerContainerSpec struct {
	Name          string
	Image         string
	State         string
	Command       string
	Detach        bool
	Pull          bool
	Recreate      bool
	RemoveVolumes bool
	Restart       bool
	Ports         []string
	Volumes       []string
	Env           map[string]string
}

func (m *DockerContainerModule) Name() string { return "docker_container" }

func (m *DockerContainerModule) Validate(params map[string]interface{}) error {
	spec := buildDockerContainerSpec(params)
	if spec.Name == "" {
		return fmt.Errorf("docker_container module requires name")
	}
	switch spec.State {
	case "present", "started", "stopped", "restarted", "absent":
	default:
		return fmt.Errorf("unsupported docker_container state: %s", spec.State)
	}
	return nil
}

func (m *DockerContainerModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	cmd, err := buildDockerContainerCommand(buildDockerContainerSpec(params))
	if err != nil {
		return nil, err
	}

	result, err := client.Execute(cmd)
	if err != nil {
		return result, err
	}
	if cleaned, changed, found := parseChangedMarker(result.Stdout); found {
		result.Stdout = cleaned
		result.Changed = changed
	}
	if cleaned, _, found := parseChangedMarker(result.Stderr); found {
		result.Stderr = cleaned
	}
	return result, nil
}

func buildDockerContainerSpec(params map[string]interface{}) dockerContainerSpec {
	spec := dockerContainerSpec{
		Name:          GetParamString(params, "name", ""),
		Image:         GetParamString(params, "image", ""),
		State:         normalizeDockerContainerState(GetParamString(params, "state", "started")),
		Command:       GetParamString(params, "command", ""),
		Detach:        getOptionalBool(params, "detach", true),
		Pull:          GetParamBool(params, "pull", false),
		Recreate:      GetParamBool(params, "recreate", false),
		RemoveVolumes: GetParamBool(params, "keep_volumes", false) == false,
		Restart:       GetParamBool(params, "restart", false),
		Ports:         dockerContainerListParam(params, "published_ports", "ports"),
		Volumes:       dockerContainerListParam(params, "volumes"),
		Env:           dockerContainerEnvParam(params, "env"),
	}

	if spec.Restart && spec.State != "absent" {
		spec.State = "restarted"
	}
	return spec
}

func normalizeDockerContainerState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "", "started", "running":
		return "started"
	case "present":
		return "present"
	case "stopped":
		return "stopped"
	case "restarted":
		return "restarted"
	case "absent":
		return "absent"
	default:
		return strings.ToLower(strings.TrimSpace(state))
	}
}

func buildDockerContainerCommand(spec dockerContainerSpec) (string, error) {
	if spec.Name == "" {
		return "", fmt.Errorf("docker_container module requires name")
	}
	if spec.State != "absent" && spec.Image == "" && spec.State != "stopped" && spec.State != "present" && spec.State != "restarted" {
		// started is the default state and needs an image when the container does not already exist.
		// Validation is lenient for existing containers; runtime script handles the missing-image case.
	}

	runArgs, err := buildDockerContainerCreateArgs(spec)
	if err != nil {
		return "", err
	}

	quotedName := shellQuote(spec.Name)
	quotedImage := shellQuote(spec.Image)
	rmFlags := ""
	if spec.RemoveVolumes {
		rmFlags = " -v"
	}

	lines := []string{
		"set -e",
		`if ! command -v docker >/dev/null 2>&1; then`,
		`  echo "docker is required for docker_container module" >&2`,
		`  exit 1`,
		`fi`,
		"changed=0",
	}

	if spec.Pull && spec.Image != "" {
		lines = append(lines,
			fmt.Sprintf(`before_image="$(docker image inspect %s --format '{{.Id}}' 2>/dev/null || true)"`, quotedImage),
			fmt.Sprintf(`docker pull %s >/dev/null 2>&1 || docker pull %s`, quotedImage, quotedImage),
			fmt.Sprintf(`after_image="$(docker image inspect %s --format '{{.Id}}' 2>/dev/null || true)"`, quotedImage),
			`if [ "$before_image" != "$after_image" ]; then`,
			`  changed=1`,
			`fi`,
		)
	}

	lines = append(lines,
		fmt.Sprintf(`if docker inspect %s >/dev/null 2>&1; then`, quotedName),
		`  exists=1`,
		fmt.Sprintf(`  current_image="$(docker inspect -f '{{.Config.Image}}' %s 2>/dev/null || true)"`, quotedName),
		fmt.Sprintf(`  running="$(docker inspect -f '{{.State.Running}}' %s 2>/dev/null || echo false)"`, quotedName),
		`else`,
		`  exists=0`,
		`  current_image=""`,
		`  running="false"`,
		`fi`,
	)

	switch spec.State {
	case "absent":
		lines = append(lines,
			`if [ "$exists" = "1" ]; then`,
			fmt.Sprintf(`  docker rm -f%s %s >/dev/null 2>&1 || docker rm -f%s %s`, rmFlags, quotedName, rmFlags, quotedName),
			`  changed=1`,
			`fi`,
		)
	default:
		lines = append(lines,
			`recreate_needed=0`,
			fmt.Sprintf(`if [ "$exists" = "1" ] && [ -n %s ] && [ "$current_image" != %s ]; then`, quotedImage, quotedImage),
			`  recreate_needed=1`,
			`fi`,
		)
		if spec.Recreate {
			lines = append(lines, `recreate_needed=1`)
		}
		lines = append(lines,
			`if [ "$exists" = "0" ] || [ "$recreate_needed" = "1" ]; then`,
			fmt.Sprintf(`  if [ "$exists" = "1" ]; then docker rm -f%s %s >/dev/null 2>&1 || docker rm -f%s %s; fi`, rmFlags, quotedName, rmFlags, quotedName),
			fmt.Sprintf(`  if [ -z %s ]; then`, quotedImage),
			fmt.Sprintf(`    echo "container %s does not exist and image is required" >&2`, spec.Name),
			`    exit 1`,
			`  fi`,
		)
		switch spec.State {
		case "present", "stopped":
			lines = append(lines, fmt.Sprintf("  %s", buildDockerCLICommand(runArgs.create)))
		case "started", "restarted":
			lines = append(lines, fmt.Sprintf("  %s", buildDockerCLICommand(runArgs.run)))
		}
		lines = append(lines,
			`  changed=1`,
			`  exists=1`,
			fmt.Sprintf(`  current_image=%s`, quotedImage),
			`  if [ "`+spec.State+`" = "present" ] || [ "`+spec.State+`" = "stopped" ]; then`,
			`    running="false"`,
			`  else`,
			`    running="true"`,
			`  fi`,
			`fi`,
		)

		switch spec.State {
		case "started":
			lines = append(lines,
				`if [ "$running" != "true" ]; then`,
				fmt.Sprintf(`  docker start %s >/dev/null 2>&1 || docker start %s`, quotedName, quotedName),
				`  changed=1`,
				`fi`,
			)
		case "stopped":
			lines = append(lines,
				`if [ "$running" = "true" ]; then`,
				fmt.Sprintf(`  docker stop %s >/dev/null 2>&1 || docker stop %s`, quotedName, quotedName),
				`  changed=1`,
				`fi`,
			)
		case "restarted":
			lines = append(lines,
				`if [ "$running" = "true" ]; then`,
				fmt.Sprintf(`  docker restart %s >/dev/null 2>&1 || docker restart %s`, quotedName, quotedName),
				`else`,
				fmt.Sprintf(`  docker start %s >/dev/null 2>&1 || docker start %s`, quotedName, quotedName),
				`fi`,
				`changed=1`,
			)
		}
	}

	lines = append(lines,
		`if [ "$changed" = "1" ]; then`,
		fmt.Sprintf("  echo %s", changeMarkerTrue),
		`else`,
		fmt.Sprintf("  echo %s", changeMarkerFalse),
		`fi`,
	)

	return strings.Join(lines, "\n"), nil
}

type dockerCreateArgs struct {
	create []string
	run    []string
}

func buildDockerContainerCreateArgs(spec dockerContainerSpec) (dockerCreateArgs, error) {
	if spec.Image == "" {
		return dockerCreateArgs{}, nil
	}

	common := []string{"docker"}
	create := append(common, "create")
	run := append(common, "run")
	if spec.Detach {
		run = append(run, "-d")
	}

	flags := []string{"--name", spec.Name}
	for _, port := range spec.Ports {
		flags = append(flags, "-p", port)
	}
	for _, volume := range spec.Volumes {
		flags = append(flags, "-v", volume)
	}

	envKeys := make([]string, 0, len(spec.Env))
	for key := range spec.Env {
		envKeys = append(envKeys, key)
	}
	sort.Strings(envKeys)
	for _, key := range envKeys {
		flags = append(flags, "-e", key+"="+spec.Env[key])
	}

	create = append(create, flags...)
	run = append(run, flags...)
	create = append(create, spec.Image)
	run = append(run, spec.Image)

	if strings.TrimSpace(spec.Command) != "" {
		parts, err := util.SplitArgs(spec.Command)
		if err != nil {
			return dockerCreateArgs{}, fmt.Errorf("parse docker command: %w", err)
		}
		create = append(create, parts...)
		run = append(run, parts...)
	}

	return dockerCreateArgs{create: create, run: run}, nil
}

func dockerContainerListParam(params map[string]interface{}, keys ...string) []string {
	for _, key := range keys {
		raw, ok := params[key]
		if !ok {
			continue
		}
		switch v := raw.(type) {
		case []interface{}:
			items := make([]string, 0, len(v))
			for _, item := range v {
				value := strings.TrimSpace(fmt.Sprintf("%v", item))
				if value != "" {
					items = append(items, value)
				}
			}
			return items
		case []string:
			items := make([]string, 0, len(v))
			for _, item := range v {
				item = strings.TrimSpace(item)
				if item != "" {
					items = append(items, item)
				}
			}
			return items
		default:
			value := strings.TrimSpace(fmt.Sprintf("%v", raw))
			if value == "" {
				return nil
			}
			value = strings.ReplaceAll(value, ",", " ")
			return strings.Fields(value)
		}
	}
	return nil
}

func dockerContainerEnvParam(params map[string]interface{}, key string) map[string]string {
	raw, ok := params[key]
	if !ok {
		return map[string]string{}
	}

	result := make(map[string]string)
	switch v := raw.(type) {
	case map[string]interface{}:
		for envKey, value := range v {
			result[envKey] = fmt.Sprintf("%v", value)
		}
	case map[string]string:
		for envKey, value := range v {
			result[envKey] = value
		}
	case []interface{}:
		for _, item := range v {
			applyDockerEnvItem(result, fmt.Sprintf("%v", item))
		}
	case []string:
		for _, item := range v {
			applyDockerEnvItem(result, item)
		}
	default:
		value := strings.TrimSpace(fmt.Sprintf("%v", raw))
		if value != "" {
			value = strings.ReplaceAll(value, ",", " ")
			for _, item := range strings.Fields(value) {
				applyDockerEnvItem(result, item)
			}
		}
	}

	return result
}

func applyDockerEnvItem(env map[string]string, item string) {
	key, value, ok := strings.Cut(strings.TrimSpace(item), "=")
	if !ok || strings.TrimSpace(key) == "" {
		return
	}
	env[strings.TrimSpace(key)] = value
}

func buildDockerCLICommand(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}
