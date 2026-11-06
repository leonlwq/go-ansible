package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"go-ansible/pkg/module"
)

type DocOptions struct {
	List       bool
	Snippet    bool
	JSON       bool
	Type       string
	ModulePath string
	Module     string
	Modules    []string
}

func RunDoc(args []string, version string) {
	fmt.Fprintln(os.Stderr, "Reimplementation and reconstruction of the main features in Python version")
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
		Use:           "ansible-doc [-l|--list] [-s|--snippet] [-j|--json] [module...]",
		Short:         "Plugin documentation tool and module reference",
		Long:          "Displays information on modules installed in Ansible libraries. It displays a terse listing of plugins and their short descriptions, provides a printout of their DOCUMENTATION strings, and creates a short snippet of code for insertion into a playbook.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Modules = args
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
	flags.BoolVarP(&opts.JSON, "json", "j", false, "Change output into JSON format")
	flags.StringVarP(&opts.Type, "type", "t", "module", "Choose which plugin type (module, lookup, connection)")
	flags.StringVarP(&opts.ModulePath, "module-path", "M", "", "Prepend colon-separated path(s) to module library")

	return cmd
}

func (opts *DocOptions) Run() error {
	pType := strings.ToLower(strings.TrimSpace(opts.Type))
	if pType == "" {
		pType = "module"
	}

	switch pType {
	case "module":
		return opts.runModuleDocs()
	case "lookup":
		return opts.runLookupDocs()
	case "connection":
		return opts.runConnectionDocs()
	default:
		return fmt.Errorf("unknown plugin type %q, supported types are: module, lookup, connection", opts.Type)
	}
}

func (opts *DocOptions) runModuleDocs() error {
	if opts.List {
		docs := module.ListModuleDocs()
		if opts.JSON {
			listMap := make(map[string]string, len(docs))
			for _, d := range docs {
				listMap[d.Name] = d.ShortDesc
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(listMap)
		}
		for _, d := range docs {
			fmt.Printf("%-24s %s\n", d.Name, d.ShortDesc)
		}
		return nil
	}

	targetModules := opts.Modules
	if len(targetModules) == 0 && strings.TrimSpace(opts.Module) != "" {
		targetModules = []string{strings.TrimSpace(opts.Module)}
	}
	if len(targetModules) == 0 {
		return fmt.Errorf("missing module name\nUsage: ansible-doc [-t module|lookup|connection] [-l] [-s] [-j] <name>")
	}

	if opts.JSON {
		jsonDocs := make(map[string]*module.ModuleDoc, len(targetModules))
		for _, mName := range targetModules {
			mName = strings.TrimSpace(mName)
			if doc, ok := module.GetModuleDoc(mName); ok {
				jsonDocs[mName] = doc
			} else {
				return fmt.Errorf("module %q not found", mName)
			}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(jsonDocs)
	}

	for _, mName := range targetModules {
		mName = strings.TrimSpace(mName)
		doc, ok := module.GetModuleDoc(mName)
		if !ok {
			return fmt.Errorf("module %q not found", mName)
		}

		if opts.Snippet {
			fmt.Print(module.FormatSnippet(doc))
		} else {
			fmt.Print(module.FormatDoc(doc))
		}
	}
	return nil
}

type PluginDocInfo struct {
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	ShortDesc   string            `json:"short_description"`
	Description string            `json:"description"`
	Options     map[string]string `json:"options,omitempty"`
	Snippet     string            `json:"snippet,omitempty"`
}

var lookupPluginDocs = map[string]PluginDocInfo{
	"env": {
		Name:        "env",
		Type:        "lookup",
		ShortDesc:   "Read environment variables",
		Description: "Allows retrieving the value of environment variables available on the Ansible control node.",
		Options: map[string]string{
			"_terms": "The name of the environment variable(s) to fetch.",
		},
		Snippet: "{{ lookup('env', 'HOME') }}",
	},
	"file": {
		Name:        "file",
		Type:        "lookup",
		ShortDesc:   "Read file contents",
		Description: "Reads and returns the content of one or more files from the local filesystem on the control node.",
		Options: map[string]string{
			"_terms": "Path to the file to read.",
		},
		Snippet: "{{ lookup('file', '/path/to/file') }}",
	},
	"pipe": {
		Name:        "pipe",
		Type:        "lookup",
		ShortDesc:   "Read output from a command",
		Description: "Executes a command locally on the control node and returns standard output as a string.",
		Options: map[string]string{
			"_terms": "The command line to run.",
		},
		Snippet: "{{ lookup('pipe', 'date +%Y-%m-%d') }}",
	},
	"template": {
		Name:        "template",
		Type:        "lookup",
		ShortDesc:   "Retrieve contents of file after templating with Jinja2",
		Description: "Templates a file locally using current variables and returns the rendered result string.",
		Options: map[string]string{
			"_terms": "Path to Jinja2 template file.",
		},
		Snippet: "{{ lookup('template', './config.j2') }}",
	},
	"password": {
		Name:        "password",
		Type:        "lookup",
		ShortDesc:   "Retrieve or generate a random password",
		Description: "Generates a random password and stores it in a file on the control node for idempotent retrieval.",
		Options: map[string]string{
			"_terms": "File path followed by optional parameters like 'chars=...' and 'length=...'",
		},
		Snippet: "{{ lookup('password', '/tmp/pass chars=ascii_letters,digits length=16') }}",
	},
	"url": {
		Name:        "url",
		Type:        "lookup",
		ShortDesc:   "Return contents from URL",
		Description: "Fetches data from HTTP, HTTPS, or FTP URLs and returns content as string.",
		Options: map[string]string{
			"_terms": "HTTP/HTTPS URL to fetch.",
		},
		Snippet: "{{ lookup('url', 'https://example.com/api/data.json') }}",
	},
	"lines": {
		Name:        "lines",
		Type:        "lookup",
		ShortDesc:   "Split output of command into lines",
		Description: "Runs a command locally and splits the output into a list of lines.",
		Options: map[string]string{
			"_terms": "Command line to execute.",
		},
		Snippet: "{{ lookup('lines', 'cat /etc/shells') }}",
	},
	"random_choice": {
		Name:        "random_choice",
		Type:        "lookup",
		ShortDesc:   "Return random element from list",
		Description: "Selects and returns an element at random from a provided list.",
		Options: map[string]string{
			"_terms": "List of choices.",
		},
		Snippet: "{{ lookup('random_choice', ['node1', 'node2', 'node3']) }}",
	},
}

var connectionPluginDocs = map[string]PluginDocInfo{
	"ssh": {
		Name:        "ssh",
		Type:        "connection",
		ShortDesc:   "Pure Go SSH and SFTP connection with long connection pooling and JumpHost support",
		Description: "Connects to target hosts via SSH protocol using native Go cryptography, providing SFTP transfer, multiplexing, and bastion jumps.",
		Options: map[string]string{
			"ansible_host":                 "Target hostname or IP address.",
			"ansible_port":                 "SSH port on target host (default: 22).",
			"ansible_user":                 "Remote login username.",
			"ansible_password":             "SSH login password.",
			"ansible_ssh_private_key_file": "Path to SSH private key file.",
			"ansible_ssh_common_args":      "SSH jump host proxy and common CLI arguments.",
		},
		Snippet: "ansible_connection: ssh\nansible_user: root\nansible_port: 22",
	},
	"local": {
		Name:        "local",
		Type:        "connection",
		ShortDesc:   "Execute tasks directly on local control node without SSH overhead",
		Description: "Executes commands locally on the control host without network serialization or SSH handshakes.",
		Options: map[string]string{
			"ansible_connection": "Set to 'local' to execute on localhost.",
		},
		Snippet: "ansible_connection: local",
	},
	"docker": {
		Name:        "docker",
		Type:        "connection",
		ShortDesc:   "Execute commands and copy files inside Docker containers",
		Description: "Manages Docker containers directly via docker exec and docker cp commands.",
		Options: map[string]string{
			"ansible_host":              "Docker container name or container ID.",
			"ansible_docker_extra_args": "Extra flags passed to docker exec.",
		},
		Snippet: "ansible_connection: docker\nansible_host: my-web-container",
	},
}

func (opts *DocOptions) runLookupDocs() error {
	if opts.List {
		if opts.JSON {
			listMap := make(map[string]string, len(lookupPluginDocs))
			for name, d := range lookupPluginDocs {
				listMap[name] = d.ShortDesc
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(listMap)
		}
		for name, d := range lookupPluginDocs {
			fmt.Printf("%-24s %s\n", name, d.ShortDesc)
		}
		return nil
	}

	targetPlugins := opts.Modules
	if len(targetPlugins) == 0 && strings.TrimSpace(opts.Module) != "" {
		targetPlugins = []string{strings.TrimSpace(opts.Module)}
	}
	if len(targetPlugins) == 0 {
		return fmt.Errorf("missing plugin name\nUsage: ansible-doc -t lookup [-l] [-s] [-j] <name>")
	}

	if opts.JSON {
		jsonDocs := make(map[string]PluginDocInfo, len(targetPlugins))
		for _, name := range targetPlugins {
			name = strings.TrimSpace(name)
			if doc, ok := lookupPluginDocs[name]; ok {
				jsonDocs[name] = doc
			} else {
				return fmt.Errorf("lookup plugin %q not found", name)
			}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(jsonDocs)
	}

	for _, name := range targetPlugins {
		name = strings.TrimSpace(name)
		doc, ok := lookupPluginDocs[name]
		if !ok {
			return fmt.Errorf("lookup plugin %q not found", name)
		}

		if opts.Snippet {
			fmt.Println(doc.Snippet)
		} else {
			fmt.Printf("> LOOKUP [%s]\n\n", doc.Name)
			fmt.Printf("  %s\n\n", doc.Description)
			if len(doc.Options) > 0 {
				fmt.Println("OPTIONS (= is mandatory):")
				for k, v := range doc.Options {
					fmt.Printf("  %s:\n    %s\n", k, v)
				}
				fmt.Println()
			}
			fmt.Printf("EXAMPLE:\n  %s\n\n", doc.Snippet)
		}
	}
	return nil
}

func (opts *DocOptions) runConnectionDocs() error {
	if opts.List {
		if opts.JSON {
			listMap := make(map[string]string, len(connectionPluginDocs))
			for name, d := range connectionPluginDocs {
				listMap[name] = d.ShortDesc
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(listMap)
		}
		for name, d := range connectionPluginDocs {
			fmt.Printf("%-24s %s\n", name, d.ShortDesc)
		}
		return nil
	}

	targetPlugins := opts.Modules
	if len(targetPlugins) == 0 && strings.TrimSpace(opts.Module) != "" {
		targetPlugins = []string{strings.TrimSpace(opts.Module)}
	}
	if len(targetPlugins) == 0 {
		return fmt.Errorf("missing plugin name\nUsage: ansible-doc -t connection [-l] [-s] [-j] <name>")
	}

	if opts.JSON {
		jsonDocs := make(map[string]PluginDocInfo, len(targetPlugins))
		for _, name := range targetPlugins {
			name = strings.TrimSpace(name)
			if doc, ok := connectionPluginDocs[name]; ok {
				jsonDocs[name] = doc
			} else {
				return fmt.Errorf("connection plugin %q not found", name)
			}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(jsonDocs)
	}

	for _, name := range targetPlugins {
		name = strings.TrimSpace(name)
		doc, ok := connectionPluginDocs[name]
		if !ok {
			return fmt.Errorf("connection plugin %q not found", name)
		}

		if opts.Snippet {
			fmt.Println(doc.Snippet)
		} else {
			fmt.Printf("> CONNECTION [%s]\n\n", doc.Name)
			fmt.Printf("  %s\n\n", doc.Description)
			if len(doc.Options) > 0 {
				fmt.Println("OPTIONS (= is mandatory):")
				for k, v := range doc.Options {
					fmt.Printf("  %s:\n    %s\n", k, v)
				}
				fmt.Println()
			}
			fmt.Printf("EXAMPLE:\n  %s\n\n", doc.Snippet)
		}
	}
	return nil
}
