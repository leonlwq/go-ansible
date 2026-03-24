package inventory

// Host 表示单个主机
type Host struct {
	Name         string            `yaml:"name" json:"name"`
	Address      string            `yaml:"address" json:"address"`
	Port         int               `yaml:"port" json:"port"`
	User         string            `yaml:"user" json:"user"`
	Password     string            `yaml:"password" json:"password"`
	PrivateKey   string            `yaml:"private_key" json:"private_key"`
	Become       bool              `yaml:"become" json:"become"`
	BecomeUser   string            `yaml:"become_user" json:"become_user"`
	BecomeMethod string            `yaml:"become_method" json:"become_method"`
	BecomePass   string            `yaml:"become_pass" json:"become_pass"`
	BecomeExe    string            `yaml:"become_exe" json:"become_exe"`
	BecomeFlags  string            `yaml:"become_flags" json:"become_flags"`
	Variables    map[string]string `yaml:"vars" json:"vars"`
}

// Group 表示主机组
type Group struct {
	Name      string            `yaml:"name" json:"name"`
	Hosts     []*Host           `yaml:"hosts" json:"hosts"`
	Children  []*Group          `yaml:"children" json:"children"`
	Variables map[string]string `yaml:"vars" json:"vars"`
	Parent    *Group            `yaml:"-" json:"-"`
}

// Inventory 表示主机清单
type Inventory struct {
	Groups    map[string]*Group `yaml:"groups" json:"groups"`
	AllHosts  map[string]*Host  `yaml:"-" json:"-"`
	Variables map[string]string `yaml:"vars" json:"vars"`
}

// NewInventory 创建新的主机清单
func NewInventory() *Inventory {
	return &Inventory{
		Groups:    make(map[string]*Group),
		AllHosts:  make(map[string]*Host),
		Variables: make(map[string]string),
	}
}
