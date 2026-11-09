package module

import (
	"fmt"
	"regexp"
	"strings"

	"go-ansible/pkg/ssh"
)

type GitModule struct{}

type gitSpec struct {
	Repo          string
	Dest          string
	Version       string
	State         string
	KeyFile       string
	Clone         bool
	Update        bool
	Force         bool
	AcceptHostKey bool
	Depth         int
	SingleBranch  bool
	Recursive     bool
}

func (m *GitModule) Name() string { return "git" }

func (m *GitModule) Validate(params map[string]interface{}) error {
	state := normalizeGitState(GetParamString(params, "state", "present"))
	dest := GetParamString(params, "dest", "")
	if dest == "" {
		return fmt.Errorf("git module requires dest")
	}
	if state == "absent" {
		return nil
	}
	if GetParamString(params, "repo", "") == "" {
		return fmt.Errorf("git module requires repo")
	}
	return nil
}

func (m *GitModule) Execute(client ssh.Connection, params map[string]interface{}) (*Result, error) {
	spec := gitSpec{
		Repo:          GetParamString(params, "repo", ""),
		Dest:          GetParamString(params, "dest", ""),
		Version:       GetParamString(params, "version", ""),
		State:         normalizeGitState(GetParamString(params, "state", "present")),
		KeyFile:       GetParamString(params, "key_file", ""),
		Clone:         GetParamBool(params, "clone", true),
		Update:        GetParamBool(params, "update", true),
		Force:         GetParamBool(params, "force", false),
		AcceptHostKey: GetParamBool(params, "accept_hostkey", false),
		Depth:         GetParamInt(params, "depth", 0),
		SingleBranch:  GetParamBool(params, "single_branch", false),
		Recursive:     GetParamBool(params, "recursive", true),
	}

	var (
		cmd string
		err error
	)
	if IsCheckMode(params) {
		cmd, err = buildGitCheckCommand(spec)
	} else {
		cmd, err = buildGitCommand(spec)
	}
	if err != nil {
		return nil, err
	}

	result, err := client.Execute(cmd)
	if err != nil {
		return result, err
	}

	HandleChangedMarker(result)
	parseGitRevisions(result)
	return result, nil
}

const (
	gitBeforePrefix = "__go_ansible_git_before__="
	gitAfterPrefix  = "__go_ansible_git_after__="
)

func parseGitRevisions(result *Result) {
	if result == nil {
		return
	}
	var (
		before   string
		after    string
		hasRevs  bool
		keptLine []string
	)
	for _, line := range strings.Split(result.Stdout, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, gitBeforePrefix) {
			before = strings.TrimPrefix(trimmed, gitBeforePrefix)
			hasRevs = true
			continue
		}
		if strings.HasPrefix(trimmed, gitAfterPrefix) {
			after = strings.TrimPrefix(trimmed, gitAfterPrefix)
			hasRevs = true
			continue
		}
		keptLine = append(keptLine, line)
	}
	if hasRevs {
		result.Stdout = strings.TrimSpace(strings.Join(keptLine, "\n"))
		if result.Data == nil {
			result.Data = make(map[string]interface{})
		}
		result.Data["before"] = before
		result.Data["after"] = after
	}
}

func buildGitCheckCommand(spec gitSpec) (string, error) {
	if spec.Dest == "" {
		return "", fmt.Errorf("git module requires dest")
	}
	if spec.State != "absent" && spec.Repo == "" {
		return "", fmt.Errorf("git module requires repo")
	}
	lines := []string{"set -e"}
	if envLine := buildGitSSHEnv(spec); envLine != "" {
		lines = append(lines, envLine)
	}
	quotedDest := shellQuote(spec.Dest)
	if spec.State == "absent" {
		lines = append(lines, fmt.Sprintf(`if [ ! -e %[1]s ]; then
  echo %[2]s
else
  before_rev="$(git -C %[1]s rev-parse HEAD 2>/dev/null || true)"
  echo "%[4]s$before_rev"
  echo "%[5]s"
  echo %[3]s
fi`, quotedDest, changeMarkerFalse, changeMarkerTrue, gitBeforePrefix, gitAfterPrefix))
		return strings.Join(lines, "\n"), nil
	}

	quotedRepo := shellQuote(spec.Repo)
	targetVer := spec.Version
	if targetVer == "" {
		targetVer = "HEAD"
	}
	quotedVer := shellQuote(targetVer)

	lines = append(lines, fmt.Sprintf(`if [ ! -d %[1]s/.git ]; then
  if [ "%[5]t" != "true" ]; then
    echo %[2]s
    exit 0
  fi
  remote_rev="$(git ls-remote %[4]s %[6]s 2>/dev/null | head -n 1 | awk '{print $1}' || true)"
  echo "%[7]s"
  echo "%[8]s$remote_rev"
  echo %[3]s
  exit 0
fi
before_rev="$(git -C %[1]s rev-parse HEAD 2>/dev/null || true)"
if [ "%[9]t" != "true" ]; then
  echo "%[7]s$before_rev"
  echo "%[8]s$before_rev"
  echo %[2]s
  exit 0
fi
remote_rev="$(git ls-remote %[4]s %[6]s 2>/dev/null | head -n 1 | awk '{print $1}' || true)"
if [ -z "$remote_rev" ]; then
  remote_rev="$(git -C %[1]s rev-parse %[6]s 2>/dev/null || echo "$before_rev")"
fi
echo "%[7]s$before_rev"
echo "%[8]s$remote_rev"
if [ -n "$remote_rev" ] && [ "$before_rev" != "$remote_rev" ]; then
  echo %[3]s
else
  echo %[2]s
fi`, quotedDest, changeMarkerFalse, changeMarkerTrue, quotedRepo, spec.Clone, quotedVer, gitBeforePrefix, gitAfterPrefix, spec.Update))

	return strings.Join(lines, "\n"), nil
}

func normalizeGitState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "", "present", "latest":
		return "present"
	case "absent":
		return "absent"
	default:
		return strings.ToLower(strings.TrimSpace(state))
	}
}

func buildGitCommand(spec gitSpec) (string, error) {
	if spec.Dest == "" {
		return "", fmt.Errorf("git module requires dest")
	}
	if spec.State != "absent" && spec.Repo == "" {
		return "", fmt.Errorf("git module requires repo")
	}
	if spec.Depth < 0 {
		return "", fmt.Errorf("git module depth must be >= 0")
	}

	lines := []string{"set -e"}
	if envLine := buildGitSSHEnv(spec); envLine != "" {
		lines = append(lines, envLine)
	}

	switch spec.State {
	case "absent":
		lines = append(lines, buildGitAbsentScript(spec))
	case "present":
		lines = append(lines, buildGitPresentScript(spec))
	default:
		return "", fmt.Errorf("unsupported git state: %s", spec.State)
	}

	return strings.Join(lines, "\n"), nil
}

func buildGitAbsentScript(spec gitSpec) string {
	quotedDest := shellQuote(spec.Dest)
	return fmt.Sprintf(`if [ ! -e %[1]s ]; then
  echo %[2]s
else
  rm -rf %[1]s
  echo %[3]s
fi`, quotedDest, changeMarkerFalse, changeMarkerTrue)
}

func buildGitPresentScript(spec gitSpec) string {
	quotedDest := shellQuote(spec.Dest)
	quotedRepo := shellQuote(spec.Repo)
	quotedVersion := shellQuote(spec.Version)
	quotedLocalRef := shellQuote("refs/heads/" + spec.Version)
	quotedRemoteRef := shellQuote("refs/remotes/origin/" + spec.Version)
	quotedOriginBranch := shellQuote("origin/" + spec.Version)

	cloneArgs := []string{"git", "clone"}
	if spec.Depth > 0 {
		cloneArgs = append(cloneArgs, "--depth", fmt.Sprintf("%d", spec.Depth))
	}
	if spec.SingleBranch {
		cloneArgs = append(cloneArgs, "--single-branch")
	}
	if spec.Recursive {
		cloneArgs = append(cloneArgs, "--recursive")
	}
	if spec.Version != "" && spec.Version != "HEAD" && !looksLikeCommit(spec.Version) {
		cloneArgs = append(cloneArgs, "--branch", spec.Version)
	}
	cloneArgs = append(cloneArgs, spec.Repo, spec.Dest)

	script := fmt.Sprintf(`if [ ! -d %[1]s/.git ]; then
  if [ -e %[1]s ]; then
    echo "destination exists but is not a git repository: %[4]s" >&2
    exit 1
  fi
  if [ "%[5]t" != "true" ]; then
    echo "repository not present and clone=false"
    echo %[2]s
    exit 0
  fi
  mkdir -p "$(dirname %[1]s)"
  %[6]s
  if [ -n %[3]s ] && [ %[3]s != 'HEAD' ] && ! git -C %[1]s symbolic-ref -q HEAD >/dev/null 2>&1; then
    git -C %[1]s fetch --all --tags --prune >/dev/null 2>&1 || git -C %[1]s fetch --all --tags --prune
    git -C %[1]s checkout -q %[3]s
  fi
  if [ "%[13]t" = "true" ] && [ -f %[1]s/.gitmodules ]; then
    git -C %[1]s submodule update --init --recursive >/dev/null 2>&1 || git -C %[1]s submodule update --init --recursive
  fi
  after_rev="$(git -C %[1]s rev-parse HEAD 2>/dev/null || true)"
  echo "__go_ansible_git_before__="
  echo "__go_ansible_git_after__=$after_rev"
  echo %[7]s
  exit 0
fi

origin_url="$(git -C %[1]s config --get remote.origin.url || true)"
if [ -n "$origin_url" ] && [ "$origin_url" != %[4]s ]; then
  if [ "%[8]t" = "true" ]; then
    before_rev="$(git -C %[1]s rev-parse HEAD 2>/dev/null || true)"
    rm -rf %[1]s
    mkdir -p "$(dirname %[1]s)"
    %[6]s
    after_rev="$(git -C %[1]s rev-parse HEAD 2>/dev/null || true)"
    echo "__go_ansible_git_before__=$before_rev"
    echo "__go_ansible_git_after__=$after_rev"
    echo %[7]s
    exit 0
  fi
  echo "remote origin mismatch: expected %[4]s got $origin_url" >&2
  exit 1
fi

before_rev="$(git -C %[1]s rev-parse HEAD 2>/dev/null || true)"
if [ "%[8]t" = "true" ]; then
  git -C %[1]s reset --hard HEAD >/dev/null 2>&1 || true
  git -C %[1]s clean -fd >/dev/null 2>&1 || true
fi
if [ "%[9]t" = "true" ]; then
  git -C %[1]s fetch --all --tags --prune >/dev/null 2>&1 || git -C %[1]s fetch --all --tags --prune
fi

if [ -n %[3]s ] && [ %[3]s != 'HEAD' ]; then
  if git -C %[1]s show-ref --verify --quiet %[10]s; then
    git -C %[1]s checkout -q %[3]s
  elif git -C %[1]s show-ref --verify --quiet %[11]s; then
    git -C %[1]s checkout -q -b %[3]s %[12]s
  else
    git -C %[1]s checkout -q %[3]s
  fi
  if [ "%[9]t" = "true" ] && git -C %[1]s show-ref --verify --quiet %[11]s; then
    git -C %[1]s pull --ff-only origin %[3]s >/dev/null 2>&1 || git -C %[1]s pull --ff-only origin %[3]s
  fi
elif [ "%[9]t" = "true" ]; then
  current_branch="$(git -C %[1]s branch --show-current 2>/dev/null || true)"
  if [ -n "$current_branch" ]; then
    git -C %[1]s pull --ff-only >/dev/null 2>&1 || git -C %[1]s pull --ff-only
  fi
fi

if [ "%[13]t" = "true" ] && [ -f %[1]s/.gitmodules ]; then
  git -C %[1]s submodule update --init --recursive >/dev/null 2>&1 || git -C %[1]s submodule update --init --recursive
fi

after_rev="$(git -C %[1]s rev-parse HEAD 2>/dev/null || true)"
echo "__go_ansible_git_before__=$before_rev"
echo "__go_ansible_git_after__=$after_rev"
if [ "$before_rev" = "$after_rev" ]; then
  echo %[2]s
else
  echo %[7]s
fi`, quotedDest, changeMarkerFalse, quotedVersion, quotedRepo, spec.Clone, buildShellCommand(cloneArgs), changeMarkerTrue, spec.Force, spec.Update, quotedLocalRef, quotedRemoteRef, quotedOriginBranch, spec.Recursive)

	return script
}

func buildGitSSHEnv(spec gitSpec) string {
	parts := []string{"ssh"}
	if spec.KeyFile != "" {
		parts = append(parts, "-i", spec.KeyFile)
	}
	if spec.AcceptHostKey {
		parts = append(parts, "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null")
	}
	if len(parts) == 1 {
		return ""
	}
	return fmt.Sprintf("export GIT_SSH_COMMAND=%s", shellQuote(buildShellCommand(parts)))
}

func buildShellCommand(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

var commitRefPattern = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

func looksLikeCommit(ref string) bool {
	return commitRefPattern.MatchString(strings.TrimSpace(ref))
}
