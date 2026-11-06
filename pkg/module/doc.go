package module

import (
	"fmt"
	"sort"
	"strings"
)

// ParamDoc 模块参数元数据
type ParamDoc struct {
	Name        string `json:"name" yaml:"name"`
	Type        string `json:"type" yaml:"type"`
	Required    bool   `json:"required" yaml:"required"`
	Default     string `json:"default" yaml:"default"`
	Description string `json:"description" yaml:"description"`
}

// ModuleDoc 模块文档元数据
type ModuleDoc struct {
	Name        string     `json:"name" yaml:"name"`
	ShortDesc   string     `json:"short_description" yaml:"short_description"`
	Description string     `json:"description" yaml:"description"`
	Options     []ParamDoc `json:"options" yaml:"options"`
	Examples    string     `json:"examples" yaml:"examples"`
}

var moduleDocs = map[string]*ModuleDoc{
	"command": {
		Name:        "command",
		ShortDesc:   "Execute commands on targets without shell processing",
		Description: "The command module takes the command name followed by a list of space-delimited arguments. The given command will not be processed through the shell, so variables like $HOME and operations like \"<\" or \">\" will not work.",
		Options: []ParamDoc{
			{Name: "cmd", Type: "string", Required: false, Default: "", Description: "The command to run."},
			{Name: "_raw_params", Type: "string", Required: false, Default: "", Description: "Command string arguments."},
			{Name: "chdir", Type: "string", Required: false, Default: "", Description: "Change into this directory before running the command."},
			{Name: "creates", Type: "string", Required: false, Default: "", Description: "A filename; when it already exists, this step will not be run."},
			{Name: "removes", Type: "string", Required: false, Default: "", Description: "A filename; when it does not exist, this step will not be run."},
		},
		Examples: `- name: Run reboot command
  command: /sbin/shutdown -t now

- name: Run command in specific directory
  command:
    cmd: make install
    chdir: /src/app
    creates: /usr/local/bin/app`,
	},
	"shell": {
		Name:        "shell",
		ShortDesc:   "Execute shell commands on targets",
		Description: "The shell module takes the command name followed by a list of space-delimited arguments. It is almost exactly like the command module but runs the command through a system shell (/bin/sh).",
		Options: []ParamDoc{
			{Name: "cmd", Type: "string", Required: false, Default: "", Description: "The command to run."},
			{Name: "_raw_params", Type: "string", Required: false, Default: "", Description: "Command string with pipes and redirects."},
			{Name: "chdir", Type: "string", Required: false, Default: "", Description: "Change into this directory before running the command."},
			{Name: "creates", Type: "string", Required: false, Default: "", Description: "A filename; when it already exists, this step will not be run."},
			{Name: "removes", Type: "string", Required: false, Default: "", Description: "A filename; when it does not exist, this step will not be run."},
		},
		Examples: `- name: Run shell command with pipe
  shell: cat /etc/hosts | grep localhost

- name: Redirect output to file
  shell: echo "hello world" > /tmp/hello.txt`,
	},
	"ping": {
		Name:        "ping",
		ShortDesc:   "Try to connect to host, verify a usable connection and return pong",
		Description: "A trivial test module, this module always returns pong on successful contact. It does not make sense in playbooks, but it is useful from /usr/bin/ansible to verify the ability to connect and execute.",
		Options: []ParamDoc{
			{Name: "data", Type: "string", Required: false, Default: "pong", Description: "Data to return for the ping return value."},
		},
		Examples: `- name: Ping target host
  ping:`,
	},
	"setup": {
		Name:        "setup",
		ShortDesc:   "Gathers facts about remote hosts",
		Description: "This module is automatically called by playbooks to gather useful variables about remote hosts that can be used in playbooks.",
		Options: []ParamDoc{
			{Name: "filter", Type: "string", Required: false, Default: "*", Description: "If supplied, only return facts that match one of the shell wildcards."},
		},
		Examples: `- name: Collect facts
  setup:

- name: Only gather network facts
  setup:
    filter: "ansible_all_ipv4_addresses"`,
	},
	"copy": {
		Name:        "copy",
		ShortDesc:   "Copy files to remote locations",
		Description: "The copy module copies a file from the local or remote machine to a location on the remote machine via SFTP or local copy.",
		Options: []ParamDoc{
			{Name: "src", Type: "string", Required: false, Default: "", Description: "Local path to a file to copy to the remote server."},
			{Name: "dest", Type: "string", Required: true, Default: "", Description: "Remote absolute path where the file should be copied to."},
			{Name: "content", Type: "string", Required: false, Default: "", Description: "When used instead of src, sets the contents of a file directly to the specified value."},
			{Name: "backup", Type: "bool", Required: false, Default: "false", Description: "Create a backup file including timestamp if dest already exists."},
			{Name: "mode", Type: "string", Required: false, Default: "0644", Description: "The permissions of the destination file (e.g. '0644')."},
			{Name: "owner", Type: "string", Required: false, Default: "", Description: "Name of the user that should own the file."},
			{Name: "group", Type: "string", Required: false, Default: "", Description: "Name of the group that should own the file."},
		},
		Examples: `- name: Copy file with owner and permissions
  copy:
    src: /srv/myfiles/foo.conf
    dest: /etc/foo.conf
    owner: root
    group: root
    mode: '0644'
    backup: yes

- name: Write string directly to file
  copy:
    content: "# Generated by go-ansible\nenabled=1\n"
    dest: /etc/app.conf`,
	},
	"file": {
		Name:        "file",
		ShortDesc:   "Manage files and file properties",
		Description: "Set attributes of files, symlinks, and directories, or remove files/symlinks/directories.",
		Options: []ParamDoc{
			{Name: "path", Type: "string", Required: true, Default: "", Description: "Path to the file being managed."},
			{Name: "state", Type: "string", Required: false, Default: "file", Description: "Directory, file, link, absent, touch."},
			{Name: "src", Type: "string", Required: false, Default: "", Description: "Target of the link when state=link."},
			{Name: "mode", Type: "string", Required: false, Default: "", Description: "File permissions (e.g. '0755')."},
			{Name: "owner", Type: "string", Required: false, Default: "", Description: "File owner user."},
			{Name: "group", Type: "string", Required: false, Default: "", Description: "File owner group."},
			{Name: "recurse", Type: "bool", Required: false, Default: "false", Description: "Recursively set the specified file attributes on directory contents."},
		},
		Examples: `- name: Create a directory if not exists
  file:
    path: /etc/myapp
    state: directory
    mode: '0755'

- name: Create a symbolic link
  file:
    src: /etc/myapp/app.conf
    dest: /etc/app.conf
    state: link

- name: Remove a file
  file:
    path: /tmp/unneeded.txt
    state: absent`,
	},
	"template": {
		Name:        "template",
		ShortDesc:   "Template a file out to a target host",
		Description: "Template takes a markup specification of a file, renders it on the controller using variables, and copies the result to the target host.",
		Options: []ParamDoc{
			{Name: "src", Type: "string", Required: true, Default: "", Description: "Path of a Jinja2 formatted template on the local server."},
			{Name: "dest", Type: "string", Required: true, Default: "", Description: "Location to render the template to on the remote machine."},
			{Name: "mode", Type: "string", Required: false, Default: "0644", Description: "The permissions of the destination file."},
			{Name: "owner", Type: "string", Required: false, Default: "", Description: "Name of the user that should own the file."},
			{Name: "group", Type: "string", Required: false, Default: "", Description: "Name of the group that should own the file."},
			{Name: "backup", Type: "bool", Required: false, Default: "false", Description: "Create a backup file if destination exists."},
		},
		Examples: `- name: Render configuration template
  template:
    src: templates/app.conf.j2
    dest: /etc/app/app.conf
    owner: app
    group: app
    mode: '0640'`,
	},
	"lineinfile": {
		Name:        "lineinfile",
		ShortDesc:   "Manage lines in text files",
		Description: "This module ensures a particular line is in a file, or replace an existing line using a back-referenced regular expression.",
		Options: []ParamDoc{
			{Name: "path", Type: "string", Required: true, Default: "", Description: "The file to modify."},
			{Name: "line", Type: "string", Required: false, Default: "", Description: "The line to insert/replace into the file."},
			{Name: "regexp", Type: "string", Required: false, Default: "", Description: "The regular expression to look for in every line of the file."},
			{Name: "state", Type: "string", Required: false, Default: "present", Description: "Whether the line should be present or absent."},
			{Name: "create", Type: "bool", Required: false, Default: "false", Description: "Create the file if it does not already exist."},
			{Name: "backup", Type: "bool", Required: false, Default: "false", Description: "Create a backup file before editing."},
			{Name: "insertafter", Type: "string", Required: false, Default: "EOF", Description: "Used with state=present to place the line after matching regexp or EOF."},
			{Name: "insertbefore", Type: "string", Required: false, Default: "", Description: "Used with state=present to place the line before matching regexp or BOF."},
		},
		Examples: `- name: Ensure SELinux is permissive
  lineinfile:
    path: /etc/selinux/config
    regexp: '^SELINUX='
    line: SELINUX=permissive

- name: Add port setting to sshd_config
  lineinfile:
    path: /etc/ssh/sshd_config
    line: 'Port 2222'
    insertafter: '^#Port 22'`,
	},
	"replace": {
		Name:        "replace",
		ShortDesc:   "Replace all instances of a particular string in a file using a regexp",
		Description: "This module will replace all instances of a pattern within a file.",
		Options: []ParamDoc{
			{Name: "path", Type: "string", Required: true, Default: "", Description: "The file to modify."},
			{Name: "regexp", Type: "string", Required: true, Default: "", Description: "The regular expression to look for in the contents."},
			{Name: "replace", Type: "string", Required: false, Default: "", Description: "The text to replace the pattern with."},
			{Name: "backup", Type: "bool", Required: false, Default: "false", Description: "Create a backup file before modifying."},
		},
		Examples: `- name: Replace hostname in file
  replace:
    path: /etc/hosts
    regexp: 'old\.example\.com'
    replace: 'new.example.com'`,
	},
	"stat": {
		Name:        "stat",
		ShortDesc:   "Retrieve file or file system status",
		Description: "Retrieves facts for a file similar to the linux stat command (exists, size, mode, checksum).",
		Options: []ParamDoc{
			{Name: "path", Type: "string", Required: true, Default: "", Description: "The full path of the file/object to get the facts of."},
			{Name: "checksum_algorithm", Type: "string", Required: false, Default: "sha1", Description: "Algorithm to determine checksum of file (md5, sha1, sha256)."},
		},
		Examples: `- name: Check if file exists
  stat:
    path: /etc/foo.conf
  register: st

- name: Print size
  debug:
    msg: "File size is {{ st.stat.size }}"
  when: st.stat.exists`,
	},
	"slurp": {
		Name:        "slurp",
		ShortDesc:   "Slurps a file from remote nodes",
		Description: "This module retrieves a file from a remote node and returns its contents base64 encoded.",
		Options: []ParamDoc{
			{Name: "src", Type: "string", Required: true, Default: "", Description: "The file on the remote machine to retrieve."},
		},
		Examples: `- name: Fetch remote file content
  slurp:
    src: /etc/issue
  register: remote_issue

- name: Print decoded content
  debug:
    msg: "{{ remote_issue.content | b64decode }}"`,
	},
	"fetch": {
		Name:        "fetch",
		ShortDesc:   "Fetch files from remote nodes",
		Description: "This module works like copy, but in reverse. It is used for fetching files from remote machines up to the local machine.",
		Options: []ParamDoc{
			{Name: "src", Type: "string", Required: true, Default: "", Description: "The file on the remote system to fetch."},
			{Name: "dest", Type: "string", Required: true, Default: "", Description: "A directory to save the file into."},
			{Name: "flat", Type: "bool", Required: false, Default: "false", Description: "If true, overrides default behavior of prepending hostname/path."},
		},
		Examples: `- name: Fetch remote log to controller
  fetch:
    src: /var/log/app.log
    dest: /tmp/fetched_logs/
    flat: yes`,
	},
	"find": {
		Name:        "find",
		ShortDesc:   "Return a list of files based on specific criteria",
		Description: "Return a list of files based on path, age, pattern, size and recursion depth.",
		Options: []ParamDoc{
			{Name: "paths", Type: "list/string", Required: true, Default: "", Description: "List of paths of directories to search for files."},
			{Name: "patterns", Type: "list/string", Required: false, Default: "*", Description: "File patterns to match against."},
			{Name: "recurse", Type: "bool", Required: false, Default: "false", Description: "If target is a directory, recursively descend into it."},
			{Name: "file_type", Type: "string", Required: false, Default: "file", Description: "Type of file to search (file, directory, any)."},
			{Name: "age", Type: "string", Required: false, Default: "", Description: "Match files based on mtime (e.g. 2d, 1w)."},
		},
		Examples: `- name: Find old log files
  find:
    paths: /var/log/app
    patterns: "*.log"
    age: 7d
    recurse: yes`,
	},
	"get_url": {
		Name:        "get_url",
		ShortDesc:   "Downloads files from HTTP, HTTPS, or FTP to remote locations",
		Description: "Downloads files from HTTP, HTTPS, or FTP to the remote host directly.",
		Options: []ParamDoc{
			{Name: "url", Type: "string", Required: true, Default: "", Description: "HTTP, HTTPS, or FTP URL in the form (http|https|ftp)://..."},
			{Name: "dest", Type: "string", Required: true, Default: "", Description: "Absolute path of where to download the file to."},
			{Name: "mode", Type: "string", Required: false, Default: "", Description: "The permissions of the destination file."},
			{Name: "checksum", Type: "string", Required: false, Default: "", Description: "Validate file checksum (e.g. sha256:hash)."},
			{Name: "timeout", Type: "int", Required: false, Default: "10", Description: "Timeout in seconds for URL request."},
		},
		Examples: `- name: Download binary
  get_url:
    url: https://example.com/software-1.0.tar.gz
    dest: /tmp/software.tar.gz
    mode: '0644'`,
	},
	"archive": {
		Name:        "archive",
		ShortDesc:   "Creates a compressed archive of one or more files or trees",
		Description: "Packs an archive into target format (tar.gz, zip, bz2).",
		Options: []ParamDoc{
			{Name: "path", Type: "string/list", Required: true, Default: "", Description: "Remote path to file or directory to archive."},
			{Name: "dest", Type: "string", Required: true, Default: "", Description: "The file name of the destination archive."},
			{Name: "format", Type: "string", Required: false, Default: "gz", Description: "The type of compression to use (gz, bz2, xz, zip, tar)."},
		},
		Examples: `- name: Compress directory
  archive:
    path: /var/log
    dest: /backup/log.tar.gz
    format: gz`,
	},
	"unarchive": {
		Name:        "unarchive",
		ShortDesc:   "Unpacks an archive from remote or local storage",
		Description: "The unarchive module unpacks an archive. It can copy the archive from local or unpack an existing archive on target.",
		Options: []ParamDoc{
			{Name: "src", Type: "string", Required: true, Default: "", Description: "Path to the archive file on local controller or remote node."},
			{Name: "dest", Type: "string", Required: true, Default: "", Description: "Remote absolute path where the archive should be unpacked."},
			{Name: "remote_src", Type: "bool", Required: false, Default: "false", Description: "Set to true to indicate the archive is already on the remote target."},
			{Name: "creates", Type: "string", Required: false, Default: "", Description: "If the specified file exists, unpacking is skipped."},
		},
		Examples: `- name: Unpack remote archive
  unarchive:
    src: /tmp/software.tar.gz
    dest: /opt/software
    remote_src: yes`,
	},
	"service": {
		Name:        "service",
		ShortDesc:   "Manage services",
		Description: "Controls services on remote hosts (SysV, systemd, upstart).",
		Options: []ParamDoc{
			{Name: "name", Type: "string", Required: true, Default: "", Description: "Name of the service."},
			{Name: "state", Type: "string", Required: false, Default: "", Description: "started, stopped, restarted, reloaded."},
			{Name: "enabled", Type: "bool", Required: false, Default: "", Description: "Whether the service should start on boot."},
		},
		Examples: `- name: Start and enable nginx
  service:
    name: nginx
    state: started
    enabled: yes`,
	},
	"systemd": {
		Name:        "systemd",
		ShortDesc:   "Manage systemd units",
		Description: "Controls systemd services or units on remote hosts.",
		Options: []ParamDoc{
			{Name: "name", Type: "string", Required: true, Default: "", Description: "Name of the unit."},
			{Name: "state", Type: "string", Required: false, Default: "", Description: "started, stopped, restarted, reloaded."},
			{Name: "enabled", Type: "bool", Required: false, Default: "", Description: "Whether the unit should start on boot."},
			{Name: "daemon_reload", Type: "bool", Required: false, Default: "false", Description: "Run daemon-reload before applying states."},
			{Name: "masked", Type: "bool", Required: false, Default: "false", Description: "Whether unit should be masked."},
		},
		Examples: `- name: Reload systemd daemon and restart app
  systemd:
    name: myapp
    state: restarted
    daemon_reload: yes`,
	},
	"apt": {
		Name:        "apt",
		ShortDesc:   "Manages apt-packages on Debian/Ubuntu systems",
		Description: "Manages apt packages (install, update, remove).",
		Options: []ParamDoc{
			{Name: "name", Type: "string/list", Required: false, Default: "", Description: "Package name or list of packages to install."},
			{Name: "state", Type: "string", Required: false, Default: "present", Description: "present, absent, latest."},
			{Name: "update_cache", Type: "bool", Required: false, Default: "false", Description: "Run apt-get update before operation."},
		},
		Examples: `- name: Install curl and git
  apt:
    name:
      - curl
      - git
    state: present
    update_cache: yes`,
	},
	"yum": {
		Name:        "yum",
		ShortDesc:   "Manages packages with the yum package manager",
		Description: "Manages yum packages on RHEL/CentOS systems.",
		Options: []ParamDoc{
			{Name: "name", Type: "string/list", Required: false, Default: "", Description: "Package name or list of packages to install."},
			{Name: "state", Type: "string", Required: false, Default: "present", Description: "present, absent, latest."},
			{Name: "update_cache", Type: "bool", Required: false, Default: "false", Description: "Force update of repository cache."},
		},
		Examples: `- name: Install nginx on CentOS
  yum:
    name: nginx
    state: present`,
	},
	"user": {
		Name:        "user",
		ShortDesc:   "Manage user accounts",
		Description: "Manage user accounts and user attributes (uid, shell, home, groups).",
		Options: []ParamDoc{
			{Name: "name", Type: "string", Required: true, Default: "", Description: "Name of the user to create, remove or modify."},
			{Name: "state", Type: "string", Required: false, Default: "present", Description: "present, absent."},
			{Name: "uid", Type: "int", Required: false, Default: "", Description: "Optionally sets the UID of the user."},
			{Name: "group", Type: "string", Required: false, Default: "", Description: "Primary group of the user."},
			{Name: "groups", Type: "string/list", Required: false, Default: "", Description: "List of secondary groups."},
			{Name: "shell", Type: "string", Required: false, Default: "/bin/bash", Description: "Login shell."},
			{Name: "home", Type: "string", Required: false, Default: "", Description: "User home directory path."},
			{Name: "create_home", Type: "bool", Required: false, Default: "true", Description: "Create home directory if state=present."},
			{Name: "remove", Type: "bool", Required: false, Default: "false", Description: "When state=absent, remove home dir and mail spool."},
		},
		Examples: `- name: Create deploy user
  user:
    name: deploy
    shell: /bin/bash
    groups: sudo,docker
    state: present`,
	},
	"group": {
		Name:        "group",
		ShortDesc:   "Manage user groups",
		Description: "Manage group presence and GID on the target host.",
		Options: []ParamDoc{
			{Name: "name", Type: "string", Required: true, Default: "", Description: "Name of the group to manage."},
			{Name: "state", Type: "string", Required: false, Default: "present", Description: "present, absent."},
			{Name: "gid", Type: "int", Required: false, Default: "", Description: "Optional GID to assign to the group."},
		},
		Examples: `- name: Create devops group
  group:
    name: devops
    state: present`,
	},
	"cron": {
		Name:        "cron",
		ShortDesc:   "Manage cron.d and crontab entries",
		Description: "Manage crontab lines and cron directories with unique name comments.",
		Options: []ParamDoc{
			{Name: "name", Type: "string", Required: true, Default: "", Description: "Description of a crontab entry or unique identifier."},
			{Name: "job", Type: "string", Required: false, Default: "", Description: "The command to execute in the cron job."},
			{Name: "state", Type: "string", Required: false, Default: "present", Description: "present, absent."},
			{Name: "minute", Type: "string", Required: false, Default: "*", Description: "Minute when the job should run."},
			{Name: "hour", Type: "string", Required: false, Default: "*", Description: "Hour when the job should run."},
			{Name: "day", Type: "string", Required: false, Default: "*", Description: "Day of the month the job should run."},
			{Name: "user", Type: "string", Required: false, Default: "root", Description: "The user whose crontab should be modified."},
		},
		Examples: `- name: Run cleanup every midnight
  cron:
    name: "daily disk cleanup"
    minute: "0"
    hour: "0"
    job: "/usr/local/bin/cleanup.sh"`,
	},
	"wait_for": {
		Name:        "wait_for",
		ShortDesc:   "Waits for a condition before continuing",
		Description: "Wait for a port to be open/drained, a file to exist, or regex to match.",
		Options: []ParamDoc{
			{Name: "host", Type: "string", Required: false, Default: "127.0.0.1", Description: "A resolvable hostname or IP address to wait on."},
			{Name: "port", Type: "int", Required: false, Default: "", Description: "Port number to poll."},
			{Name: "path", Type: "string", Required: false, Default: "", Description: "Path to a file on the filesystem that must exist."},
			{Name: "state", Type: "string", Required: false, Default: "started", Description: "started, stopped, present, absent."},
			{Name: "timeout", Type: "int", Required: false, Default: "300", Description: "Maximum number of seconds to wait."},
			{Name: "delay", Type: "int", Required: false, Default: "0", Description: "Number of seconds to wait before starting to poll."},
		},
		Examples: `- name: Wait for port 8080 to become available
  wait_for:
    port: 8080
    timeout: 60`,
	},
	"git": {
		Name:        "git",
		ShortDesc:   "Deploy software (or files) from git checkouts",
		Description: "Manage git checkouts of repositories to deploy software or configuration.",
		Options: []ParamDoc{
			{Name: "repo", Type: "string", Required: true, Default: "", Description: "Git URL of the remote repository."},
			{Name: "dest", Type: "string", Required: true, Default: "", Description: "The path of where the repository should be checked out to."},
			{Name: "version", Type: "string", Required: false, Default: "HEAD", Description: "Branch, tag, or commit SHA to checkout."},
			{Name: "force", Type: "bool", Required: false, Default: "false", Description: "If true, any modified files in working repository will be discarded."},
		},
		Examples: `- name: Clone repository
  git:
    repo: 'https://github.com/example/app.git'
    dest: /var/www/app
    version: master`,
	},
	"uri": {
		Name:        "uri",
		ShortDesc:   "Interacts with HTTP and HTTPS web services",
		Description: "Interacts with HTTP and HTTPS web services, supporting digest, basic and WSSE HTTP authentication mechanisms.",
		Options: []ParamDoc{
			{Name: "url", Type: "string", Required: true, Default: "", Description: "HTTP or HTTPS URL of the service."},
			{Name: "method", Type: "string", Required: false, Default: "GET", Description: "The HTTP method of the request (GET, POST, PUT, DELETE)."},
			{Name: "body", Type: "string/dict", Required: false, Default: "", Description: "The body of the HTTP request."},
			{Name: "status_code", Type: "list/int", Required: false, Default: "200", Description: "A valid, numeric, HTTP status code or list."},
			{Name: "headers", Type: "dict", Required: false, Default: "", Description: "Custom HTTP headers dictionary."},
		},
		Examples: `- name: Health check API
  uri:
    url: http://localhost:8080/health
    status_code: 200`,
	},
	"docker_container": {
		Name:        "docker_container",
		ShortDesc:   "Manage Docker containers",
		Description: "Manage the life cycle of Docker containers using native Docker CLI.",
		Options: []ParamDoc{
			{Name: "name", Type: "string", Required: true, Default: "", Description: "Name of the container."},
			{Name: "image", Type: "string", Required: false, Default: "", Description: "Repository path and tag of image."},
			{Name: "state", Type: "string", Required: false, Default: "started", Description: "started, stopped, restarted, absent, present."},
			{Name: "ports", Type: "list", Required: false, Default: "", Description: "List of port mappings (e.g. ['80:80'])."},
			{Name: "env", Type: "dict", Required: false, Default: "", Description: "Dictionary of environment variables."},
		},
		Examples: `- name: Run Redis container
  docker_container:
    name: my-redis
    image: redis:alpine
    state: started
    ports:
      - "6379:6379"`,
	},
	"k8s": {
		Name:        "k8s",
		ShortDesc:   "Manage Kubernetes (K8s) objects",
		Description: "Apply, delete, or modify Kubernetes objects via kubectl.",
		Options: []ParamDoc{
			{Name: "definition", Type: "dict/string", Required: false, Default: "", Description: "YAML definition of Kubernetes manifest."},
			{Name: "src", Type: "string", Required: false, Default: "", Description: "Path to a file containing Kubernetes manifest specification."},
			{Name: "state", Type: "string", Required: false, Default: "present", Description: "present, absent."},
			{Name: "namespace", Type: "string", Required: false, Default: "default", Description: "Target namespace."},
		},
		Examples: `- name: Apply Kubernetes deployment
  k8s:
    src: /path/to/deployment.yaml
    state: present`,
	},
	"debug": {
		Name:        "debug",
		ShortDesc:   "Print statements during execution",
		Description: "This module allows you to print expressions or string messages during playbook run.",
		Options: []ParamDoc{
			{Name: "msg", Type: "string", Required: false, Default: "", Description: "The customized message that is printed. If not supplied, 'Hello world!' will be used."},
			{Name: "var", Type: "string", Required: false, Default: "", Description: "A variable name to debug and dump."},
		},
		Examples: `- name: Print greeting
  debug:
    msg: "System architecture is {{ ansible_architecture }}"

- name: Dump variable
  debug:
    var: hostvars[inventory_hostname]`,
	},
	"fail": {
		Name:        "fail",
		ShortDesc:   "Fail with custom message",
		Description: "This module fails the progress with a custom message. Can be useful for boundary checks.",
		Options: []ParamDoc{
			{Name: "msg", Type: "string", Required: false, Default: "Failed as requested from task", Description: "The customized message used for failing execution."},
		},
		Examples: `- name: Fail when memory is too low
  fail:
    msg: "Insufficient RAM detected"
  when: ansible_memtotal_mb < 2048`,
	},
	"set_fact": {
		Name:        "set_fact",
		ShortDesc:   "Set host facts from a task",
		Description: "This module allows setting new variables. Variables are set on a host-by-host basis just like facts discovered by the setup module.",
		Options: []ParamDoc{
			{Name: "key_value", Type: "any", Required: true, Default: "", Description: "Variables specified in key=value pairs or YAML dict."},
		},
		Examples: `- name: Set custom host variable
  set_fact:
    cluster_role: primary
    app_port: 8080`,
	},
	"meta": {
		Name:        "meta",
		ShortDesc:   "Execute Ansible meta actions",
		Description: "Meta tasks are a special kind of task that influences Ansible execution flow.",
		Options: []ParamDoc{
			{Name: "_raw_params", Type: "string", Required: true, Default: "", Description: "flush_handlers, refresh_inventory, clear_facts, clear_host_errors, end_host, end_play."},
		},
		Examples: `- name: Flush handlers immediately
  meta: flush_handlers

- name: End host execution early
  meta: end_host`,
	},
	"pause": {
		Name:        "pause",
		ShortDesc:   "Pause playbook execution",
		Description: "Pauses and waits for user interaction or sleeps for a specified amount of time.",
		Options: []ParamDoc{
			{Name: "seconds", Type: "int/float", Required: false, Default: "", Description: "A number of seconds to pause for."},
			{Name: "minutes", Type: "int/float", Required: false, Default: "", Description: "A number of minutes to pause for."},
			{Name: "prompt", Type: "string", Required: false, Default: "", Description: "Optional text to prompt the user with."},
			{Name: "echo", Type: "bool", Required: false, Default: "true", Description: "Controls whether the user's input is displayed on the screen."},
		},
		Examples: `- name: Pause for 5 seconds
  pause:
    seconds: 5

- name: Pause until user confirms
  pause:
    prompt: "Ready to deploy? Press enter to continue"`,
	},
	"async_status": {
		Name:        "async_status",
		ShortDesc:   "Obtain status of asynchronous task or clean up",
		Description: "This module can be used to poll the status of an asynchronous task started with async > 0 and poll == 0, or clean up its temporary files.",
		Options: []ParamDoc{
			{Name: "jid", Type: "string", Required: true, Default: "", Description: "Job or task identifier generated by the async task."},
			{Name: "mode", Type: "string", Required: false, Default: "status", Description: "If status, query job status. If cleanup, remove temporary files."},
		},
		Examples: `- name: Check status of async task
  async_status:
    jid: "{{ job_result.ansible_job_id }}"
  register: job_poll
  until: job_poll.finished
  retries: 30
  delay: 2

- name: Clean up async job files
  async_status:
    jid: "{{ job_result.ansible_job_id }}"
    mode: cleanup`,
	},
	"add_host": {
		Name:        "add_host",
		ShortDesc:   "Add a host (and alternative group) to the in-memory inventory",
		Description: "Use this module during a playbook run to add a host to the in-memory inventory for subsequent plays.",
		Options: []ParamDoc{
			{Name: "name", Type: "string", Required: true, Default: "", Description: "The hostname or IP address of the host to add."},
			{Name: "groups", Type: "list/string", Required: false, Default: "", Description: "The groups to add the host to."},
		},
		Examples: `- name: Add newly provisioned host
  add_host:
    name: 192.168.1.100
    groups: kubernetes_nodes
    ansible_port: 2222`,
	},
	"group_by": {
		Name:        "group_by",
		ShortDesc:   "Create Ansible groups based on facts or variables",
		Description: "Use this module during a playbook run to dynamically group hosts based on facts or variables.",
		Options: []ParamDoc{
			{Name: "key", Type: "string", Required: true, Default: "", Description: "The group name expression."},
			{Name: "parents", Type: "list/string", Required: false, Default: "", Description: "Parent groups for the newly created dynamic group."},
		},
		Examples: `- name: Group by operating system
  group_by:
    key: "os_{{ ansible_os_family }}"`,
	},
	"sysctl": {
		Name:        "sysctl",
		ShortDesc:   "Manage kernel parameters in /etc/sysctl.conf and live runtime",
		Description: "This module manipulates sysctl entries and optionally reloads the sysctl configuration file.",
		Options: []ParamDoc{
			{Name: "name", Type: "string", Required: true, Default: "", Description: "The sysctl parameter key, e.g. net.ipv4.ip_forward."},
			{Name: "value", Type: "string/int", Required: true, Default: "", Description: "Desired value of the sysctl parameter."},
			{Name: "state", Type: "string", Required: false, Default: "present", Description: "Whether the entry should be present or absent."},
			{Name: "reload", Type: "bool", Required: false, Default: "true", Description: "Whether to reload sysctl after updating file."},
			{Name: "sysctl_set", Type: "bool", Required: false, Default: "false", Description: "Verify and enforce runtime value even if file is up to date."},
			{Name: "sysctl_file", Type: "string", Required: false, Default: "/etc/sysctl.conf", Description: "Path to the sysctl configuration file."},
			{Name: "ignoreerrors", Type: "bool", Required: false, Default: "false", Description: "Ignore errors when setting runtime or reloading."},
		},
		Examples: `- name: Enable IP forwarding for Kubernetes
  sysctl:
    name: net.ipv4.ip_forward
    value: 1
    state: present
    reload: yes

- name: Set bridge netfilter and vm swappiness
  sysctl:
    name: net.bridge.bridge-nf-call-iptables
    value: 1
    sysctl_set: yes
    state: present`,
	},
	"hostname": {
		Name:        "hostname",
		ShortDesc:   "Manage system hostname",
		Description: "Set the system's hostname using hostnamectl or generic hostname commands and update /etc/hostname.",
		Options: []ParamDoc{
			{Name: "name", Type: "string", Required: true, Default: "", Description: "Name of the host to set."},
			{Name: "use", Type: "string", Required: false, Default: "generic", Description: "Which strategy to use (generic or systemd)."},
		},
		Examples: `- name: Set hostname to k8s-master-01
  hostname:
    name: k8s-master-01`,
	},
	"mount": {
		Name:        "mount",
		ShortDesc:   "Control active and configured mount points in /etc/fstab",
		Description: "This module controls active and configured mount points in /etc/fstab and mounts/unmounts filesystems.",
		Options: []ParamDoc{
			{Name: "path", Type: "string", Required: true, Default: "", Description: "Mount point path on remote system (e.g. /data)."},
			{Name: "src", Type: "string", Required: false, Default: "", Description: "Device or network share to mount (required for mounted/present)."},
			{Name: "fstype", Type: "string", Required: false, Default: "", Description: "Filesystem type (e.g. ext4, xfs, nfs)."},
			{Name: "opts", Type: "string", Required: false, Default: "defaults", Description: "Mount options."},
			{Name: "dump", Type: "int", Required: false, Default: "0", Description: "Dump frequency."},
			{Name: "passno", Type: "int", Required: false, Default: "0", Description: "FSCK pass number."},
			{Name: "state", Type: "string", Required: true, Default: "", Description: "mounted, unmounted, present, absent, remounted."},
			{Name: "fstab", Type: "string", Required: false, Default: "/etc/fstab", Description: "Path to fstab file."},
			{Name: "boot", Type: "bool", Required: false, Default: "true", Description: "Whether to configure in fstab file."},
			{Name: "backup", Type: "bool", Required: false, Default: "false", Description: "Create a backup file including timestamp before modifying fstab."},
		},
		Examples: `- name: Mount data disk to /data
  mount:
    path: /data
    src: /dev/vdb1
    fstype: ext4
    opts: defaults,noatime
    state: mounted

- name: Unmount /mnt/share
  mount:
    path: /mnt/share
    state: unmounted`,
	},
	"package": {
		Name:        "package",
		ShortDesc:   "Generic OS package manager module",
		Description: "Installs, upgrades, and removes packages using the target system's package manager (apt, dnf, yum, or apk).",
		Options: []ParamDoc{
			{Name: "name", Type: "string/list", Required: false, Default: "", Description: "Package name or list of package names."},
			{Name: "state", Type: "string", Required: false, Default: "present", Description: "Desired package state (present, latest, absent)."},
			{Name: "update_cache", Type: "bool", Required: false, Default: "false", Description: "Whether to update repository cache prior to action."},
		},
		Examples: `- name: Install git via generic package module
  package:
    name: git
    state: present

- name: Update repository cache only
  package:
    update_cache: yes`,
	},
	"blockinfile": {
		Name:        "blockinfile",
		ShortDesc:   "Insert/update/remove a text block surrounded by marker lines",
		Description: "This module will insert/update/remove a block of multi-line text surrounded by customizable marker lines.",
		Options: []ParamDoc{
			{Name: "path", Type: "string", Required: true, Default: "", Description: "The file to modify."},
			{Name: "block", Type: "string", Required: false, Default: "", Description: "The text block to insert."},
			{Name: "marker", Type: "string", Required: false, Default: "# {mark} ANSIBLE MANAGED BLOCK", Description: "The marker line template."},
			{Name: "state", Type: "string", Required: false, Default: "present", Description: "Whether the block should be present or absent."},
			{Name: "insertafter", Type: "string", Required: false, Default: "EOF", Description: "Insert after regex pattern or EOF."},
			{Name: "insertbefore", Type: "string", Required: false, Default: "", Description: "Insert before regex pattern or BOF."},
			{Name: "backup", Type: "bool", Required: false, Default: "false", Description: "Create backup file before changing."},
			{Name: "create", Type: "bool", Required: false, Default: "false", Description: "Create file if it does not exist."},
		},
		Examples: `- name: Add custom environment exports
  blockinfile:
    path: /etc/profile
    block: |
      export JAVA_HOME=/opt/jdk
      export PATH=$JAVA_HOME/bin:$PATH`,
	},
	"dnf": {
		Name:        "dnf",
		ShortDesc:   "Manages packages with the dnf package manager",
		Description: "Installs, upgrades, and removes packages using the dnf package manager on RHEL 8/9, Fedora, and compatible systems.",
		Options: []ParamDoc{
			{Name: "name", Type: "string/list", Required: false, Default: "", Description: "Package name or list of packages to install/remove."},
			{Name: "state", Type: "string", Required: false, Default: "present", Description: "Desired package state (present, latest, absent)."},
			{Name: "update_cache", Type: "bool", Required: false, Default: "false", Description: "Whether to run dnf makecache."},
		},
		Examples: `- name: Install nginx with dnf
  dnf:
    name: nginx
    state: latest`,
	},
	"reboot": {
		Name:        "reboot",
		ShortDesc:   "Reboot a machine and wait for it to recover",
		Description: "Reboot a machine, wait for it to go down, come back up, and respond to commands.",
		Options: []ParamDoc{
			{Name: "reboot_timeout", Type: "int", Required: false, Default: "600", Description: "Maximum seconds to wait for machine to reboot and respond to test command."},
			{Name: "connect_timeout", Type: "int", Required: false, Default: "5", Description: "Maximum seconds to wait for a single connection attempt to the rebooted machine."},
			{Name: "post_reboot_delay", Type: "int", Required: false, Default: "0", Description: "Seconds to wait after the reboot was successful and the test command succeeded."},
			{Name: "pre_reboot_delay", Type: "int", Required: false, Default: "0", Description: "Seconds to wait before the reboot command is issued."},
			{Name: "msg", Type: "string", Required: false, Default: "Reboot initiated by Ansible", Description: "Message to display to users before reboot."},
			{Name: "test_command", Type: "string", Required: false, Default: "whoami", Description: "Command to run on the rebooted host to verify it is back online."},
			{Name: "search_regex", Type: "string", Required: false, Default: "", Description: "Regex pattern to match in the output of test_command."},
			{Name: "boot_time_command", Type: "string", Required: false, Default: "cat /proc/sys/kernel/random/boot_id", Description: "Command to get system boot time or boot ID."},
			{Name: "reboot_command", Type: "string", Required: false, Default: "", Description: "Command to run to reboot the system."},
		},
		Examples: `- name: Unconditionally reboot the machine with a 600s timeout
  reboot:

- name: Reboot with custom delays and test command
  reboot:
    reboot_timeout: 300
    pre_reboot_delay: 10
    post_reboot_delay: 15
    test_command: uptime`,
	},
	"wait_for_connection": {
		Name:        "wait_for_connection",
		ShortDesc:   "Waits until remote host becomes reachable/usable",
		Description: "Waits for a remote host to become reachable and usable over SSH or local connection, useful after reboots or provisioning.",
		Options: []ParamDoc{
			{Name: "connect_timeout", Type: "int", Required: false, Default: "5", Description: "Maximum number of seconds to wait for a connection to happen at each attempt."},
			{Name: "delay", Type: "int", Required: false, Default: "0", Description: "Number of seconds to wait before starting to poll."},
			{Name: "sleep", Type: "int", Required: false, Default: "1", Description: "Number of seconds to sleep between checks."},
			{Name: "timeout", Type: "int", Required: false, Default: "600", Description: "Maximum number of seconds to wait."},
		},
		Examples: `- name: Wait 600 seconds for target connection to become reachable
  wait_for_connection:

- name: Wait with custom delay and sleep interval
  wait_for_connection:
    delay: 10
    timeout: 300
    sleep: 5`,
	},
	"authorized_key": {
		Name:        "authorized_key",
		ShortDesc:   "Adds or removes authorized keys for particular user accounts",
		Description: "Adds or removes SSH authorized keys for specific user accounts. Supports fetching public keys from URLs, exclusive key management, and custom key options/comments.",
		Options: []ParamDoc{
			{Name: "user", Type: "string", Required: true, Default: "", Description: "The username on the remote host whose authorized_keys file will be modified."},
			{Name: "key", Type: "string", Required: true, Default: "", Description: "The SSH public key(s), as a string or an http/https URL."},
			{Name: "state", Type: "string", Required: false, Default: "present", Description: "Whether the given key should or should not be in the file (present or absent)."},
			{Name: "path", Type: "string", Required: false, Default: "~/.ssh/authorized_keys", Description: "Alternate path to the authorized_keys file."},
			{Name: "exclusive", Type: "bool", Required: false, Default: "false", Description: "Whether to remove all other non-specified keys from the authorized_keys file."},
			{Name: "manage_dir", Type: "bool", Required: false, Default: "true", Description: "Whether to manage the directory containing the authorized_keys file with mode 0700."},
			{Name: "key_options", Type: "string", Required: false, Default: "", Description: "A string of options to prepend to the key line in authorized_keys."},
			{Name: "comment", Type: "string", Required: false, Default: "", Description: "Change the comment on the public key."},
			{Name: "validate_certs", Type: "bool", Required: false, Default: "true", Description: "Whether to validate SSL certificates when downloading keys from URLs."},
		},
		Examples: `- name: Set authorized key taken from file
  authorized_key:
    user: charlie
    state: present
    key: "{{ lookup('file', '/home/charlie/.ssh/id_rsa.pub') }}"

- name: Set authorized keys exclusively from GitHub URL
  authorized_key:
    user: deploy
    state: present
    key: https://github.com/alice.keys
    exclusive: true`,
	},
	"pip": {
		Name:        "pip",
		ShortDesc:   "Manages Python library dependencies",
		Description: "Manage Python library dependencies. To use this module, pip must be installed on the remote host, or a virtualenv must be created.",
		Options: []ParamDoc{
			{Name: "name", Type: "string/list", Required: false, Default: "", Description: "The name of a Python library to install or the url(s) of the remote package."},
			{Name: "version", Type: "string", Required: false, Default: "", Description: "The version number to install of the Python library specified in name."},
			{Name: "requirements", Type: "string", Required: false, Default: "", Description: "The path to a pip requirements file, which should be local to the remote system."},
			{Name: "virtualenv", Type: "string", Required: false, Default: "", Description: "An optional path to a virtualenv directory to install into."},
			{Name: "virtualenv_command", Type: "string", Required: false, Default: "python3 -m venv", Description: "The command to create the virtualenv with."},
			{Name: "virtualenv_python", Type: "string", Required: false, Default: "", Description: "The Python executable used for virtualenv creation."},
			{Name: "state", Type: "string", Required: false, Default: "present", Description: "The desired state: present, absent, latest, forcereinstall."},
			{Name: "extra_args", Type: "string", Required: false, Default: "", Description: "Extra arguments passed to pip."},
			{Name: "chdir", Type: "string", Required: false, Default: "", Description: "cd into this directory before running pip."},
			{Name: "executable", Type: "string", Required: false, Default: "pip3", Description: "The explicit executable or pathname for the pip executable."},
		},
		Examples: `- name: Install bottle python package
  pip:
    name: bottle

- name: Install bottle with version specifier
  pip:
    name: bottle
    version: 0.12.25

- name: Install dependencies into a virtualenv
  pip:
    requirements: /my_app/requirements.txt
    virtualenv: /opt/my_app/venv`,
	},
	"synchronize": {
		Name:        "synchronize",
		ShortDesc:   "A wrapper around rsync to make common tasks in ansible easier",
		Description: "synchronize is a wrapper around rsync. It is commonly used to transfer files and directories between the control machine and managed nodes, or between two managed nodes.",
		Options: []ParamDoc{
			{Name: "src", Type: "string", Required: true, Default: "", Description: "Path on the source host that will be synchronized to the destination."},
			{Name: "dest", Type: "string", Required: true, Default: "", Description: "Path on the destination host that will be synchronized from the source."},
			{Name: "archive", Type: "bool", Required: false, Default: "true", Description: "Mirrors the rsync -a option, which enables recursive copy, links, perms, times, owner, group."},
			{Name: "delete", Type: "bool", Required: false, Default: "false", Description: "Delete files in dest that do not exist (or no longer exist) in src."},
			{Name: "checksum", Type: "bool", Required: false, Default: "false", Description: "Skip based on checksum, rather than mod-time & size."},
			{Name: "compress", Type: "bool", Required: false, Default: "true", Description: "Compress file data during the transfer."},
			{Name: "mode", Type: "string", Required: false, Default: "push", Description: "Specify the direction of the synchronization: push or pull."},
			{Name: "rsync_opts", Type: "string/list", Required: false, Default: "", Description: "Specify additional rsync options by passing them in as a string or array."},
			{Name: "rsync_path", Type: "string", Required: false, Default: "", Description: "Specify the rsync command to run on the remote machine (e.g. sudo rsync)."},
			{Name: "private_key", Type: "string", Required: false, Default: "", Description: "Specify the SSH private key path for rsync connection."},
			{Name: "dest_port", Type: "int", Required: false, Default: "22", Description: "Port number for ssh on the destination host."},
		},
		Examples: `- name: Synchronization of src on the control machine to dest on the remote hosts
  synchronize:
    src: some/relative/path
    dest: /var/www/site

- name: Synchronization using rsync delete option
  synchronize:
    src: /data/static/
    dest: /var/www/static/
    delete: yes
    archive: yes`,
	},
	"known_hosts": {
		Name:        "known_hosts",
		ShortDesc:   "Add or remove a host from the known_hosts file",
		Description: "The known_hosts module lets you add or remove a host keys from the known_hosts file.",
		Options: []ParamDoc{
			{Name: "name", Type: "string", Required: true, Default: "", Description: "The host to query and add/remove into the known_hosts file."},
			{Name: "key", Type: "string", Required: false, Default: "", Description: "The host key line(s) for the entry in the known_hosts file. Required if state=present."},
			{Name: "path", Type: "string", Required: false, Default: "~/.ssh/known_hosts", Description: "The known_hosts file to modify."},
			{Name: "hash_host", Type: "bool", Required: false, Default: "false", Description: "Hash the host and name in the known_hosts file."},
			{Name: "state", Type: "string", Required: false, Default: "present", Description: "Whether to ensure the host and key are present or absent."},
		},
		Examples: `- name: Tell known_hosts that github.com has this key
  known_hosts:
    name: github.com
    key: "github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"
    path: /etc/ssh/ssh_known_hosts
    state: present

- name: Add a hashed host entry
  known_hosts:
    name: 192.168.1.100
    key: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"
    hash_host: yes

- name: Remove a host from known_hosts
  known_hosts:
    name: old-server.example.com
    state: absent`,
	},
	"service_facts": {
		Name:        "service_facts",
		ShortDesc:   "Return service state information as fact data",
		Description: "The service_facts module returns information about system services (systemd, sysv, launchd) and populates ansible_facts.services.",
		Options:     []ParamDoc{},
		Examples: `- name: Populate service facts
  service_facts:

- name: Print service status
  debug:
    var: ansible_facts.services['ssh.service'].state`,
	},
	"package_facts": {
		Name:        "package_facts",
		ShortDesc:   "Package information as facts",
		Description: "The package_facts module fetches information about installed packages on the system (supporting dpkg/apt, rpm/yum, apk, pacman, brew) and populates ansible_facts.packages.",
		Options: []ParamDoc{
			{Name: "manager", Type: "string", Required: false, Default: "auto", Description: "The package manager used to query packages (auto, apt, rpm, apk, pacman, brew)."},
		},
		Examples: `- name: Gather package facts
  package_facts:
    manager: auto

- name: Check if nginx is installed
  debug:
    msg: "Nginx version: {{ ansible_facts.packages['nginx'][0].version }}"
  when: "'nginx' in ansible_facts.packages"`,
	},
	"tempfile": {
		Name:        "tempfile",
		ShortDesc:   "Creates temporary files and directories",
		Description: "The tempfile module creates temporary files or directories on remote hosts with restricted permissions (0600 for files, 0700 for directories).",
		Options: []ParamDoc{
			{Name: "state", Type: "string", Required: false, Default: "file", Description: "Whether to create file or directory (file, directory)."},
			{Name: "path", Type: "string", Required: false, Default: "/tmp", Description: "Location where temporary file or directory should be created."},
			{Name: "prefix", Type: "string", Required: false, Default: "ansible.", Description: "Prefix of temporary file or directory name."},
			{Name: "suffix", Type: "string", Required: false, Default: "", Description: "Suffix of temporary file or directory name."},
		},
		Examples: `- name: Create temporary build file
  tempfile:
    state: file
    suffix: .build
  register: temp_file

- name: Create temporary workspace directory
  tempfile:
    state: directory
    prefix: deploy_
  register: temp_dir`,
	},
}

// GetModuleDoc 获取指定模块的文档
func GetModuleDoc(name string) (*ModuleDoc, bool) {
	doc, ok := moduleDocs[name]
	return doc, ok
}

// ListModuleDocs 列出所有模块文档
func ListModuleDocs() []*ModuleDoc {
	docs := make([]*ModuleDoc, 0, len(moduleDocs))
	for _, doc := range moduleDocs {
		docs = append(docs, doc)
	}
	sort.Slice(docs, func(i, j int) bool {
		return docs[i].Name < docs[j].Name
	})
	return docs
}

// FormatDoc 以文本形式格式化输出模块详细文档
func FormatDoc(doc *ModuleDoc) string {
	if doc == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("> %s    (%s)\n\n", strings.ToUpper(doc.Name), doc.ShortDesc))
	sb.WriteString("DESCRIPTION:\n")
	sb.WriteString(fmt.Sprintf("    %s\n\n", doc.Description))

	if len(doc.Options) > 0 {
		sb.WriteString("OPTIONS (= is mandatory):\n\n")
		for _, opt := range doc.Options {
			prefix := " "
			if opt.Required {
				prefix = "="
			}
			defStr := ""
			if opt.Default != "" {
				defStr = fmt.Sprintf(" [Default: %s]", opt.Default)
			}
			sb.WriteString(fmt.Sprintf("  %s %s (%s)%s\n", prefix, opt.Name, opt.Type, defStr))
			sb.WriteString(fmt.Sprintf("        %s\n\n", opt.Description))
		}
	}

	if doc.Examples != "" {
		sb.WriteString("EXAMPLES:\n\n")
		sb.WriteString(doc.Examples + "\n")
	}

	return sb.String()
}

// FormatSnippet 生成 YAML 任务片段模板
func FormatSnippet(doc *ModuleDoc) string {
	if doc == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("- name: %s\n", doc.ShortDesc))
	sb.WriteString(fmt.Sprintf("  %s:\n", doc.Name))

	for _, opt := range doc.Options {
		comment := ""
		if opt.Required {
			comment = "  # required"
		} else if opt.Default != "" {
			comment = fmt.Sprintf("  # default: %s", opt.Default)
		}
		sb.WriteString(fmt.Sprintf("    %s: %s\n", opt.Name, comment))
	}

	return sb.String()
}
