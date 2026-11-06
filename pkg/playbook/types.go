package playbook

import "path/filepath"

// Playbook 表示一个 playbook 文件
type Playbook struct {
	Plays []*Play `yaml:"plays" json:"plays"`
	Path  string  `yaml:"-" json:"-"`
}

func (p *Playbook) BaseDir() string {
	if p == nil || p.Path == "" {
		return ""
	}
	return filepath.Dir(p.Path)
}

// VarsPrompt 表示一个交互式变量录入提示项
type VarsPrompt struct {
	Name    string      `yaml:"name" json:"name"`
	Prompt  string      `yaml:"prompt" json:"prompt"`
	Default interface{} `yaml:"default" json:"default"`
	Private bool        `yaml:"private" json:"private"`
	Confirm bool        `yaml:"confirm" json:"confirm"`
	Encrypt string      `yaml:"encrypt" json:"encrypt"`
}

// Play 表示一个 play
type Play struct {
	Name           string                 `yaml:"name" json:"name"`
	Hosts          string                 `yaml:"hosts" json:"hosts"`
	RemoteUser     string                 `yaml:"remote_user" json:"remote_user"`
	Become         bool                   `yaml:"become" json:"become"`
	BecomeUser     string                 `yaml:"become_user" json:"become_user"`
	BecomeMethod   string                 `yaml:"become_method" json:"become_method"`
	BecomePass     string                 `yaml:"become_pass" json:"become_pass"`
	GatherFacts    *bool                  `yaml:"gather_facts" json:"gather_facts"`
	GatherSubset   []string               `yaml:"gather_subset" json:"gather_subset"`
	GatherTimeout  int                    `yaml:"gather_timeout" json:"gather_timeout"`
	Vars           map[string]interface{} `yaml:"vars" json:"vars"`
	VarsFiles      []string               `yaml:"vars_files" json:"vars_files"`
	VarsPrompt     []*VarsPrompt          `yaml:"vars_prompt" json:"vars_prompt"`
	Tasks          []*Task                `yaml:"tasks" json:"tasks"`
	Handlers       []*Handler             `yaml:"handlers" json:"handlers"`
	PreTasks       []*Task                `yaml:"pre_tasks" json:"pre_tasks"`
	PostTasks      []*Task                `yaml:"post_tasks" json:"post_tasks"`
	Roles          []interface{}          `yaml:"roles" json:"roles"`
	Tags           []string               `yaml:"tags" json:"tags"`
	Environment    map[string]string      `yaml:"environment" json:"environment"`
	Serial         interface{}            `yaml:"serial" json:"serial"`
	MaxFailPct     int                    `yaml:"max_fail_percentage" json:"max_fail_percentage"`
	AnyErrorsFatal bool                   `yaml:"any_errors_fatal" json:"any_errors_fatal"`
	ForceHandlers  bool                   `yaml:"force_handlers" json:"force_handlers"`
	Strategy       string                 `yaml:"strategy" json:"strategy"`
	Throttle       int                    `yaml:"throttle" json:"throttle"`
	Debugger       string                 `yaml:"debugger" json:"debugger"`
	CheckMode      *bool                  `yaml:"check_mode" json:"check_mode"`
	Diff           *bool                  `yaml:"diff" json:"diff"`
}

// Task 表示一个任务
type Task struct {
	Name              string                 `yaml:"name" json:"name"`
	ModuleName        string                 `yaml:"-" json:"-"`
	Params            map[string]interface{} `yaml:"-" json:"-"`
	Module            map[string]interface{} `yaml:",inline" json:"module"`
	Block             []*Task                `yaml:"block" json:"block"`
	Rescue            []*Task                `yaml:"rescue" json:"rescue"`
	Always            []*Task                `yaml:"always" json:"always"`
	Vars              map[string]interface{} `yaml:"vars" json:"vars"`
	When              interface{}            `yaml:"when" json:"when"`
	ChangedWhen       interface{}            `yaml:"changed_when" json:"changed_when"`
	FailedWhen        interface{}            `yaml:"failed_when" json:"failed_when"`
	Loop              interface{}            `yaml:"loop" json:"loop"`
	LoopControl       LoopControl            `yaml:"loop_control" json:"loop_control"`
	Register          string                 `yaml:"register" json:"register"`
	Until             interface{}            `yaml:"until" json:"until"`
	Retries           int                    `yaml:"retries" json:"retries"`
	Delay             int                    `yaml:"delay" json:"delay"`
	Async             int                    `yaml:"async" json:"async"`
	Poll              *int                   `yaml:"poll" json:"poll"`
	IgnoreError       bool                   `yaml:"ignore_errors" json:"ignore_errors"`
	IgnoreUnreachable bool                   `yaml:"ignore_unreachable" json:"ignore_unreachable"`
	NoLog             bool                   `yaml:"no_log" json:"no_log"`
	DelegateTo        string                 `yaml:"delegate_to" json:"delegate_to"`
	DelegateFacts     bool                   `yaml:"delegate_facts" json:"delegate_facts"`
	LocalAction       string                 `yaml:"local_action" json:"local_action"`
	RunOnce           bool                   `yaml:"run_once" json:"run_once"`
	Tags              []string               `yaml:"tags" json:"tags"`
	Notify            interface{}            `yaml:"notify" json:"notify"`
	Become            *bool                  `yaml:"become" json:"become"`
	BecomeUser        string                 `yaml:"become_user" json:"become_user"`
	BecomeMethod      string                 `yaml:"become_method" json:"become_method"`
	BecomePass        string                 `yaml:"become_pass" json:"become_pass"`
	Throttle          int                    `yaml:"throttle" json:"throttle"`
	Timeout           int                    `yaml:"timeout" json:"timeout"`
	Debugger          string                 `yaml:"debugger" json:"debugger"`
	Environment       map[string]string      `yaml:"environment" json:"environment"`
	CheckMode         *bool                  `yaml:"check_mode" json:"check_mode"`
	Diff              *bool                  `yaml:"diff" json:"diff"`
}

type LoopControl struct {
	LoopVar  string  `yaml:"loop_var" json:"loop_var"`
	IndexVar string  `yaml:"index_var" json:"index_var"`
	Label    string  `yaml:"label" json:"label"`
	Extended bool    `yaml:"extended" json:"extended"`
	Pause    float64 `yaml:"pause" json:"pause"`
}

// Handler 表示一个 handler
type Handler struct {
	Name         string                 `yaml:"name" json:"name"`
	ModuleName   string                 `yaml:"-" json:"-"`
	Params       map[string]interface{} `yaml:"-" json:"-"`
	Module       map[string]interface{} `yaml:",inline" json:"module"`
	When         interface{}            `yaml:"when" json:"when"`
	DelegateTo   string                 `yaml:"delegate_to" json:"delegate_to"`
	Loop         interface{}            `yaml:"loop" json:"loop"`
	WithItems    interface{}            `yaml:"with_items" json:"with_items"`
	Listen       string                 `yaml:"listen" json:"listen"`
	Listens      []string               `yaml:"-" json:"-"`
	Notify       interface{}            `yaml:"notify" json:"notify"`
	Tags         []string               `yaml:"tags" json:"tags"`
	Become       *bool                  `yaml:"become" json:"become"`
	BecomeUser   string                 `yaml:"become_user" json:"become_user"`
	BecomeMethod string                 `yaml:"become_method" json:"become_method"`
	BecomePass   string                 `yaml:"become_pass" json:"become_pass"`
	NoLog        bool                   `yaml:"no_log" json:"no_log"`
}

// Role 表示一个 role
type Role struct {
	Name         string                 `yaml:"name" json:"name"`
	Tasks        []*Task                `yaml:"tasks" json:"tasks"`
	Handlers     []*Handler             `yaml:"handlers" json:"handlers"`
	Vars         map[string]interface{} `yaml:"vars" json:"vars"`
	Defaults     map[string]interface{} `yaml:"defaults" json:"defaults"`
	Dependencies []interface{}          `yaml:"dependencies" json:"dependencies"`
}
