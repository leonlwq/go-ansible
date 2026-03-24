package playbook

// Playbook 表示一个 playbook 文件
type Playbook struct {
	Plays []*Play `yaml:"plays" json:"plays"`
	Path  string  `yaml:"-" json:"-"`
}

// Play 表示一个 play
type Play struct {
	Name         string                 `yaml:"name" json:"name"`
	Hosts        string                 `yaml:"hosts" json:"hosts"`
	RemoteUser   string                 `yaml:"remote_user" json:"remote_user"`
	Become       bool                   `yaml:"become" json:"become"`
	BecomeUser   string                 `yaml:"become_user" json:"become_user"`
	BecomeMethod string                 `yaml:"become_method" json:"become_method"`
	GatherFacts  *bool                  `yaml:"gather_facts" json:"gather_facts"`
	Vars         map[string]interface{} `yaml:"vars" json:"vars"`
	VarsFiles    []string               `yaml:"vars_files" json:"vars_files"`
	Tasks        []*Task                `yaml:"tasks" json:"tasks"`
	Handlers     []*Handler             `yaml:"handlers" json:"handlers"`
	PreTasks     []*Task                `yaml:"pre_tasks" json:"pre_tasks"`
	PostTasks    []*Task                `yaml:"post_tasks" json:"post_tasks"`
	Roles        []interface{}          `yaml:"roles" json:"roles"`
	Tags         []string               `yaml:"tags" json:"tags"`
	Environment  map[string]string      `yaml:"environment" json:"environment"`
	Serial       interface{}            `yaml:"serial" json:"serial"`
	MaxFailPct   int                    `yaml:"max_fail_percentage" json:"max_fail_percentage"`
}

// Task 表示一个任务
type Task struct {
	Name        string                 `yaml:"name" json:"name"`
	ModuleName  string                 `yaml:"-" json:"-"`
	Params      map[string]interface{} `yaml:"-" json:"-"`
	Module      map[string]interface{} `yaml:",inline" json:"module"`
	When        interface{}            `yaml:"when" json:"when"`
	Loop        interface{}            `yaml:"loop" json:"loop"`
	Register    string                 `yaml:"register" json:"register"`
	Until       interface{}            `yaml:"until" json:"until"`
	Retries     int                    `yaml:"retries" json:"retries"`
	Delay       int                    `yaml:"delay" json:"delay"`
	IgnoreError bool                   `yaml:"ignore_errors" json:"ignore_errors"`
	NoLog       bool                   `yaml:"no_log" json:"no_log"`
	DelegateTo  string                 `yaml:"delegate_to" json:"delegate_to"`
	LocalAction string                 `yaml:"local_action" json:"local_action"`
	RunOnce     bool                   `yaml:"run_once" json:"run_once"`
	Tags        []string               `yaml:"tags" json:"tags"`
	Notify      interface{}            `yaml:"notify" json:"notify"`
	Become      bool                   `yaml:"become" json:"become"`
	BecomeUser  string                 `yaml:"become_user" json:"become_user"`
}

// Handler 表示一个 handler
type Handler struct {
	Name       string                 `yaml:"name" json:"name"`
	ModuleName string                 `yaml:"-" json:"-"`
	Params     map[string]interface{} `yaml:"-" json:"-"`
	Module     map[string]interface{} `yaml:",inline" json:"module"`
	Listen     string                 `yaml:"listen" json:"listen"`
}

// Role 表示一个 role
type Role struct {
	Name     string                 `yaml:"name" json:"name"`
	Tasks    []*Task                `yaml:"tasks" json:"tasks"`
	Handlers []*Handler             `yaml:"handlers" json:"handlers"`
	Vars     map[string]interface{} `yaml:"vars" json:"vars"`
	Defaults map[string]interface{} `yaml:"defaults" json:"defaults"`
}
