package cli

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type PullOptions struct {
	URL               string
	Checkout          string
	Directory         string
	Inventory         string
	ModuleName        string
	ExtraVars         []string
	Sleep             int
	Force             bool
	OnlyIfChanged     bool
	Purge             bool
	Clean             bool
	Full              bool
	Check             bool
	Diff              bool
	Verbose           int
	AskPass           bool
	AskBecomePass     bool
	AskVaultPass      bool
	VaultPasswordFile string
	Tags              string
	SkipTags          string
	Playbook          string
}

func NewPullCmd(version string) *cobra.Command {
	opts := &PullOptions{
		Checkout:   "HEAD",
		ModuleName: "git",
	}

	cmd := &cobra.Command{
		Use:   "ansible-pull -U <url> [options] [<playbook.yml>]",
		Short: "Pulls playbooks from a VCS repo and executes them for the current host",
		Long: `ansible-pull is a small script that pulls a git repository containing playbooks
and runs the playbook against localhost using the local connection plugin.`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			if opts.URL == "" {
				return errors.New("missing required flag: -U / --url")
			}
			if len(args) > 0 {
				opts.Playbook = args[0]
			}
			opts.ExtraVars, _ = c.Flags().GetStringArray("extra-vars")
			opts.Verbose, _ = c.Flags().GetCount("verbose")
			return runPull(opts)
		},
	}
	cmd.SetVersionTemplate("ansible-pull {{.Version}}\n")

	flags := cmd.Flags()
	flags.StringVarP(&opts.URL, "url", "U", "", "URL of the version control repository")
	flags.StringVarP(&opts.Checkout, "checkout", "C", "HEAD", "branch/tag/commit to checkout")
	flags.StringVarP(&opts.Directory, "directory", "d", "", "directory to checkout repository to")
	flags.StringVarP(&opts.Inventory, "inventory", "i", "", "specify inventory host path(s) or comma separated host list")
	flags.StringVarP(&opts.ModuleName, "module-name", "m", "git", "repository module name")
	flags.StringArrayP("extra-vars", "e", nil, "set additional variables as key=value or YAML/JSON, if filename prepend with @")
	flags.IntVarP(&opts.Sleep, "sleep", "s", 0, "sleep for random seconds (between 0 and X) before starting")
	flags.BoolVarP(&opts.Force, "force", "f", false, "run playbook even if repository could not be updated")
	flags.BoolVarP(&opts.OnlyIfChanged, "only-if-changed", "o", false, "only run the playbook if the repository has changed")
	flags.BoolVar(&opts.Purge, "purge", false, "purge checkout directory after playbook execution")
	flags.BoolVar(&opts.Clean, "clean", false, "modified files in repo will be discarded")
	flags.BoolVar(&opts.Full, "full", false, "do a full clone instead of a shallow one")
	flags.BoolVar(&opts.Check, "check", false, "don't make any changes; instead, try to predict some of the changes that may occur")
	flags.BoolVarP(&opts.Diff, "diff", "D", false, "when changing (small) files and templates, show the differences in those files")
	flags.CountP("verbose", "v", "verbose mode (-v, -vv, -vvv)")
	flags.BoolVarP(&opts.AskPass, "ask-pass", "k", false, "ask for connection password")
	flags.BoolVarP(&opts.AskBecomePass, "ask-become-pass", "K", false, "ask for privilege escalation password")
	flags.BoolVar(&opts.AskVaultPass, "ask-vault-pass", false, "ask for vault password")
	flags.StringVar(&opts.VaultPasswordFile, "vault-password-file", "", "vault password file")
	flags.StringVarP(&opts.Tags, "tags", "t", "", "only run plays and tasks tagged with these values")
	flags.StringVar(&opts.SkipTags, "skip-tags", "", "only run plays and tasks whose tags do not match these values")

	return cmd
}

func runPull(opts *PullOptions) error {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "localhost"
	}

	if opts.Sleep > 0 {
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		randSec := r.Intn(opts.Sleep + 1)
		if opts.Verbose > 0 {
			fmt.Printf("Sleeping for %d seconds before pull...\n", randSec)
		}
		time.Sleep(time.Duration(randSec) * time.Second)
	}

	destDir := opts.Directory
	if destDir == "" {
		home, _ := os.UserHomeDir()
		if home == "" {
			home = os.TempDir()
		}
		destDir = filepath.Join(home, ".ansible", "pull", hostname)
	} else {
		destDir = expandCLIPath(destDir)
	}

	changed, err := syncGitRepo(opts, destDir)
	if err != nil {
		return fmt.Errorf("repository sync error: %w", err)
	}

	if opts.Purge {
		defer func() {
			_ = os.RemoveAll(destDir)
		}()
	}

	if opts.OnlyIfChanged && !changed {
		fmt.Println("Repository has not changed, skipping playbook execution.")
		return nil
	}

	playbookPath, err := resolvePullPlaybook(destDir, opts.Playbook, hostname)
	if err != nil {
		return err
	}

	inventoryPath := resolvePullInventory(destDir, opts.Inventory)

	// Execute playbook locally
	pbOpts := &PlaybookOptions{
		PlaybookPath:      playbookPath,
		InventoryPaths:    []string{inventoryPath},
		ExtraVars:         opts.ExtraVars,
		Connection:        "local",
		Check:             opts.Check,
		Diff:              opts.Diff,
		Verbose:           opts.Verbose,
		Tags:              opts.Tags,
		SkipTags:          opts.SkipTags,
		AskPass:           opts.AskPass,
		AskBecomePass:     opts.AskBecomePass,
		AskVaultPass:      opts.AskVaultPass,
		VaultPasswordFile: opts.VaultPasswordFile,
	}

	if opts.Verbose > 0 {
		fmt.Printf("Executing playbook %s with inventory %s (connection=local)\n", playbookPath, inventoryPath)
	}

	return pbOpts.Run()
}

func syncGitRepo(opts *PullOptions, destDir string) (bool, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return false, fmt.Errorf("git command not found in PATH: %w", err)
	}

	gitDir := filepath.Join(destDir, ".git")
	if fi, err := os.Stat(gitDir); err == nil && fi.IsDir() {
		// Existing repository: update
		oldCommit, _ := runGitOutput(destDir, "rev-parse", "HEAD")

		if opts.Clean {
			_, _ = runGitOutput(destDir, "reset", "--hard", "HEAD")
			_, _ = runGitOutput(destDir, "clean", "-fd")
		}

		if err := runGit(destDir, "fetch", "--all"); err != nil {
			if !opts.Force {
				return false, fmt.Errorf("git fetch failed: %w", err)
			}
		}

		if opts.Checkout != "" && opts.Checkout != "HEAD" {
			if err := runGit(destDir, "checkout", opts.Checkout); err != nil {
				if !opts.Force {
					return false, fmt.Errorf("git checkout %s failed: %w", opts.Checkout, err)
				}
			}
		}

		if err := runGit(destDir, "pull"); err != nil {
			if !opts.Force {
				return false, fmt.Errorf("git pull failed: %w", err)
			}
		}

		newCommit, _ := runGitOutput(destDir, "rev-parse", "HEAD")
		return oldCommit != newCommit, nil
	}

	// New repository: clone
	if err := os.MkdirAll(filepath.Dir(destDir), 0o755); err != nil {
		return false, fmt.Errorf("creating parent directory: %w", err)
	}

	cloneArgs := []string{"clone"}
	if !opts.Full {
		cloneArgs = append(cloneArgs, "--depth", "1")
	}
	if opts.Checkout != "" && opts.Checkout != "HEAD" {
		cloneArgs = append(cloneArgs, "-b", opts.Checkout)
	}
	cloneArgs = append(cloneArgs, opts.URL, destDir)

	if err := runGit("", cloneArgs...); err != nil {
		return false, fmt.Errorf("git clone failed: %w", err)
	}

	return true, nil
}

func resolvePullPlaybook(repoDir, requestedPlaybook, hostname string) (string, error) {
	if requestedPlaybook != "" {
		p := requestedPlaybook
		if !filepath.IsAbs(p) {
			p = filepath.Join(repoDir, p)
		}
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		return "", fmt.Errorf("specified playbook not found: %s", p)
	}

	candidates := []string{
		hostname + ".yml",
		hostname + ".yaml",
		"local.yml",
		"local.yaml",
		"site.yml",
		"site.yaml",
		"main.yml",
		"main.yaml",
		"default.yml",
	}

	for _, cand := range candidates {
		p := filepath.Join(repoDir, cand)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}

	return "", fmt.Errorf("could not find a playbook to execute in %s (checked %s.yml, local.yml, site.yml, etc.)", repoDir, hostname)
}

func resolvePullInventory(repoDir, requestedInventory string) string {
	if requestedInventory != "" {
		if !strings.Contains(requestedInventory, ",") && !filepath.IsAbs(requestedInventory) {
			candidate := filepath.Join(repoDir, requestedInventory)
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
		return requestedInventory
	}

	candidates := []string{
		"hosts",
		"inventory",
		"inventory.ini",
		"inventory.yml",
		"inventory.yaml",
	}
	for _, cand := range candidates {
		p := filepath.Join(repoDir, cand)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}

	return "localhost,"
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %v: %w (output: %s)", args, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func runGitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func RunPull(args []string, version string) {
	fmt.Println("Reimplementation and reconstruction of the main features in Python version")
	cmd := NewPullCmd(version)
	cmd.SetArgs(normalizeAnsibleArgs(args))
	if err := cmd.Execute(); err != nil {
		if !errors.Is(err, errExecutionFailed) {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}
}
