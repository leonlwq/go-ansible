package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"go-ansible/pkg/inventory"
)

type InventoryOptions struct {
	InventoryPath  string
	InventoryPaths []string
	List           bool
	Host           string
	YAML           bool
	Graph          bool
	Export         bool
	Vars           bool
	Output         string
}

func runInventory(args []string) {
	RunInventory(args, "0.1.0")
}

func RunInventory(args []string, version string) {
	fmt.Println("Reimplementation and reconstruction of the main features in Python version")
	cmd := NewInventoryCmd(version)
	cmd.SetArgs(normalizeAnsibleArgs(args))
	if err := cmd.Execute(); err != nil {
		if !errors.Is(err, errExecutionFailed) {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}
}

func NewInventoryCmd(version string) *cobra.Command {
	if version == "" {
		version = "0.1.0"
	}
	opts := &InventoryOptions{}
	cmd := &cobra.Command{
		Use:           "ansible-inventory [options]",
		Short:         "Display or dump an Ansible inventory host list or group tree.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				if args[0] == "list" {
					opts.List = true
				} else if opts.Host == "" {
					opts.Host = args[0]
				}
			}
			return opts.Run()
		},
	}
	cmd.SetVersionTemplate("ansible-inventory {{.Version}}\n")

	flags := cmd.Flags()
	flags.VarP(newInventoryValue(&opts.InventoryPaths), "inventory", "i", "specify inventory host path(s) or comma separated host list")
	flags.BoolVar(&opts.List, "list", false, "output all hosts info as a JSON/YAML map")
	flags.StringVar(&opts.Host, "host", "", "output specific host info as a JSON/YAML map")
	flags.BoolVarP(&opts.YAML, "yaml", "y", false, "use YAML format instead of JSON")
	flags.BoolVar(&opts.Graph, "graph", false, "create inventory graph")
	flags.BoolVar(&opts.Export, "export", false, "when doing an --list, represent in a way that is optimized for export")
	flags.BoolVar(&opts.Vars, "vars", false, "add vars to graph display")
	flags.StringVarP(&opts.Output, "output", "o", "", "when doing an --list or --graph, send output to a file instead of stdout")

	return cmd
}

func (opts *InventoryOptions) Run() error {
	cfg := loadCLIConfig()

	invPaths := opts.InventoryPaths
	if len(invPaths) == 0 && opts.InventoryPath != "" {
		invPaths = []string{opts.InventoryPath}
	}
	if len(invPaths) == 0 && cfg.Defaults.Inventory != "" {
		invPaths = []string{cfg.ResolvePath(cfg.Defaults.Inventory)}
	}

	if len(invPaths) == 0 {
		return fmt.Errorf("inventory file is required (-i)\nUsage: ansible-inventory -i <inventory> [--list|--host <name>|--graph|-y]")
	}
	opts.InventoryPath = invPaths[0]

	inv, err := inventory.ParseSources(invPaths...)
	if err != nil {
		return fmt.Errorf("parsing inventory: %w", err)
	}
	applyConfigDefaultsToInventory(inv, cfg)

	var out io.Writer = os.Stdout
	if opts.Output != "" {
		file, err := os.Create(opts.Output)
		if err != nil {
			return fmt.Errorf("create output file %q: %w", opts.Output, err)
		}
		defer file.Close()
		out = file
	}

	if opts.Graph {
		printInventoryGraphTo(out, inv, opts.Vars)
		return nil
	}

	if opts.List || opts.Host == "" {
		data := buildInventoryListData(inv)
		if opts.YAML {
			writeYAMLTo(out, data)
		} else {
			writeJSONTo(out, data)
		}
		return nil
	}

	h, err := inv.GetHost(opts.Host)
	if err != nil {
		return fmt.Errorf("host not found: %s", opts.Host)
	}

	hostData := buildInventoryHostData(inv, h)
	if opts.YAML {
		writeYAMLTo(out, hostData)
	} else {
		writeJSONTo(out, hostData)
	}
	return nil
}

func writeYAML(data interface{}) {
	writeYAMLTo(os.Stdout, data)
}

func writeYAMLTo(w io.Writer, data interface{}) {
	encoder := yaml.NewEncoder(w)
	encoder.SetIndent(2)
	if err := encoder.Encode(data); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding inventory YAML output: %v\n", err)
		os.Exit(1)
	}
}

func printInventoryGraph(inv *inventory.Inventory) {
	printInventoryGraphTo(os.Stdout, inv, false)
}

func printInventoryGraphTo(w io.Writer, inv *inventory.Inventory, showVars bool) {
	fmt.Fprintln(w, "@all:")
	for _, groupName := range sortedGroupNames(inv.Groups) {
		if groupName == "all" {
			continue
		}
		group := inv.Groups[groupName]
		if group.Parent != nil {
			continue
		}
		printGroupGraphTo(w, inv, group, "  |--", showVars)
	}
}

func printGroupGraph(inv *inventory.Inventory, group *inventory.Group, prefix string) {
	printGroupGraphTo(os.Stdout, inv, group, prefix, false)
}

func printGroupGraphTo(w io.Writer, inv *inventory.Inventory, group *inventory.Group, prefix string, showVars bool) {
	varsInfo := ""
	if showVars && len(group.Variables) > 0 {
		var parts []string
		for k, v := range group.Variables {
			parts = append(parts, fmt.Sprintf("%s=%s", k, v))
		}
		sort.Strings(parts)
		varsInfo = fmt.Sprintf(" {%s}", strings.Join(parts, ", "))
	}
	fmt.Fprintf(w, "%s@%s:%s\n", prefix, group.Name, varsInfo)
	childPrefix := strings.Replace(prefix, "|--", "|  ", 1) + "|--"
	for _, child := range group.Children {
		printGroupGraphTo(w, inv, child, childPrefix, showVars)
	}
	for _, host := range group.Hosts {
		hVarsInfo := ""
		if showVars {
			resolved := inv.ResolveHostVars(host)
			if len(resolved) > 0 {
				var parts []string
				for k, v := range resolved {
					parts = append(parts, fmt.Sprintf("%s=%s", k, v))
				}
				sort.Strings(parts)
				hVarsInfo = fmt.Sprintf(" {%s}", strings.Join(parts, ", "))
			}
		}
		fmt.Fprintf(w, "%s%s%s\n", childPrefix, host.Name, hVarsInfo)
	}
}

func writeJSON(data interface{}) {
	writeJSONTo(os.Stdout, data)
}

func writeJSONTo(w io.Writer, data interface{}) {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(data); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding inventory output: %v\n", err)
		os.Exit(1)
	}
}

func buildInventoryListData(inv *inventory.Inventory) map[string]interface{} {
	data := make(map[string]interface{}, len(inv.Groups)+3)
	hostVars := make(map[string]interface{}, len(inv.AllHosts))
	groupHostMembership := make(map[string]bool)
	topLevelGroups := make([]string, 0)

	for _, host := range sortedHosts(inv.AllHosts) {
		hostVars[host.Name] = buildInventoryHostData(inv, host)
	}

	for _, groupName := range sortedGroupNames(inv.Groups) {
		group := inv.Groups[groupName]
		hosts := make([]string, 0, len(group.Hosts))
		for _, host := range group.Hosts {
			hosts = append(hosts, host.Name)
			if groupName != "all" {
				groupHostMembership[host.Name] = true
			}
		}
		sort.Strings(hosts)

		children := make([]string, 0, len(group.Children))
		for _, child := range group.Children {
			children = append(children, child.Name)
		}
		sort.Strings(children)

		groupData := map[string]interface{}{
			"hosts": hosts,
		}
		if len(children) > 0 {
			groupData["children"] = children
		}
		if len(group.Variables) > 0 {
			groupData["vars"] = cloneStringMap(group.Variables)
		}
		data[groupName] = groupData

		if groupName != "all" && group.Parent == nil {
			topLevelGroups = append(topLevelGroups, groupName)
		}
	}

	sort.Strings(topLevelGroups)

	allGroup, ok := data["all"].(map[string]interface{})
	if !ok {
		allGroup = map[string]interface{}{
			"hosts": []string{},
		}
		data["all"] = allGroup
	}
	if len(topLevelGroups) > 0 {
		existingChildren, _ := allGroup["children"].([]string)
		allGroup["children"] = appendUniqueStrings(existingChildren, topLevelGroups...)
	}

	ungroupedHosts := make([]string, 0)
	for _, host := range sortedHosts(inv.AllHosts) {
		if !groupHostMembership[host.Name] {
			ungroupedHosts = append(ungroupedHosts, host.Name)
		}
	}
	data["ungrouped"] = map[string]interface{}{
		"hosts": ungroupedHosts,
	}
	data["_meta"] = map[string]interface{}{
		"hostvars": hostVars,
	}

	return data
}

func buildInventoryHostData(inv *inventory.Inventory, host *inventory.Host) map[string]interface{} {
	data := make(map[string]interface{})
	for key, value := range inv.ResolveHostVars(host) {
		data[key] = value
	}
	if host.Address != "" && host.Address != host.Name {
		data["ansible_host"] = host.Address
	}
	if host.Port > 0 && host.Port != 22 {
		data["ansible_port"] = host.Port
	}
	if host.User != "" {
		data["ansible_user"] = host.User
	}
	if host.Password != "" {
		data["ansible_ssh_pass"] = host.Password
	}
	if host.PrivateKey != "" {
		data["ansible_private_key_file"] = host.PrivateKey
	}
	if host.SSHCommonArgs != "" {
		data["ansible_ssh_common_args"] = host.SSHCommonArgs
	}
	if host.Become {
		data["ansible_become"] = true
	}
	if host.BecomeUser != "" {
		data["ansible_become_user"] = host.BecomeUser
	}
	if host.BecomeMethod != "" {
		data["ansible_become_method"] = host.BecomeMethod
	}
	if host.BecomePass != "" {
		data["ansible_become_pass"] = host.BecomePass
	}
	if host.BecomeExe != "" {
		data["ansible_become_exe"] = host.BecomeExe
	}
	if host.BecomeFlags != "" {
		data["ansible_become_flags"] = host.BecomeFlags
	}
	return data
}

func sortedGroupNames(groups map[string]*inventory.Group) []string {
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedHosts(hosts map[string]*inventory.Host) []*inventory.Host {
	names := make([]string, 0, len(hosts))
	for name := range hosts {
		names = append(names, name)
	}
	sort.Strings(names)

	result := make([]*inventory.Host, 0, len(names))
	for _, name := range names {
		result = append(result, hosts[name])
	}
	return result
}

func cloneStringMap(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func appendUniqueStrings(existing []string, values ...string) []string {
	set := make(map[string]struct{}, len(existing))
	result := append([]string{}, existing...)
	for _, value := range existing {
		set[value] = struct{}{}
	}
	for _, value := range values {
		if _, ok := set[value]; ok {
			continue
		}
		set[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
