package playbook

type rawPlay struct {
	Name           string                 `yaml:"name"`
	Hosts          string                 `yaml:"hosts"`
	RemoteUser     string                 `yaml:"remote_user"`
	Become         interface{}            `yaml:"become"`
	BecomeUser     string                 `yaml:"become_user"`
	BecomeMethod   string                 `yaml:"become_method"`
	BecomePass     string                 `yaml:"become_pass"`
	BecomePasswd   string                 `yaml:"become_password"`
	GatherFacts    interface{}            `yaml:"gather_facts"`
	Vars           map[string]interface{} `yaml:"vars"`
	VarsFiles      interface{}            `yaml:"vars_files"`
	Tasks          []rawTask              `yaml:"tasks"`
	Handlers       []rawTask              `yaml:"handlers"`
	PreTasks       []rawTask              `yaml:"pre_tasks"`
	PostTasks      []rawTask              `yaml:"post_tasks"`
	Roles          []interface{}          `yaml:"roles"`
	Tags           interface{}            `yaml:"tags"`
	Environment    map[string]interface{} `yaml:"environment"`
	Serial         interface{}            `yaml:"serial"`
	MaxFailPct     int                    `yaml:"max_fail_percentage"`
	AnyErrorsFatal bool                   `yaml:"any_errors_fatal"`
	ForceHandlers  interface{}            `yaml:"force_handlers"`
	Strategy       string                 `yaml:"strategy"`
	Throttle       interface{}            `yaml:"throttle"`
	VarsPrompt     interface{}            `yaml:"vars_prompt"`
	Debugger       string                 `yaml:"debugger"`
}

type rawTask map[string]interface{}
