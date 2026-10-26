package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"go-ansible/pkg/module"
)

type DocOptions struct {
	List    bool
	Snippet bool
	Module  string
}

func RunDoc(args []string, version string) {
	fmt.Println("Reimplementation and reconstruction of the main features in Python version")
	cmd := NewDocCmd(version)
	cmd.SetArgs(normalizeAnsibleArgs(args))
	if err := cmd.Execute(); err != nil {
		if !errors.Is(err, errExecutionFailed) {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}
}

func NewDocCmd(version string) *cobra.Command {
	if version == "" {
		version = "0.1.0"
	}
	opts := &DocOptions{}
	cmd := &cobra.Command{
		Use:           "ansible-doc [-l|--list] [-s|--snippet] [module...]",
		Short:         "Plugin documentation tool and module reference",
		Long:          "Displays information on modules installed in Ansible libraries. It displays a terse listing of plugins and their short descriptions, provides a printout of their DOCUMENTATION strings, and creates a short snippet of code for insertion into a playbook.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				opts.Module = args[0]
			}
			return opts.Run()
		},
	}
	cmd.SetVersionTemplate("ansible-doc {{.Version}}\n")

	flags := cmd.Flags()
	flags.BoolVarP(&opts.List, "list", "l", false, "List available plugins or modules")
	flags.BoolVarP(&opts.Snippet, "snippet", "s", false, "Show playbook snippet for specified module")

	return cmd
}

func (opts *DocOptions) Run() error {
	if opts.List {
		docs := module.ListModuleDocs()
		for _, d := range docs {
			fmt.Printf("%-24s %s\n", d.Name, d.ShortDesc)
		}
		return nil
	}

	targetModule := strings.TrimSpace(opts.Module)
	if targetModule == "" {
		return fmt.Errorf("missing module name\nUsage: ansible-doc [-l] [-s] <module>")
	}

	doc, ok := module.GetModuleDoc(targetModule)
	if !ok {
		return fmt.Errorf("module %q not found", targetModule)
	}

	if opts.Snippet {
		fmt.Print(module.FormatSnippet(doc))
		return nil
	}

	fmt.Print(module.FormatDoc(doc))
	return nil
}
