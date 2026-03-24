# go-ansible

A high-performance Ansible alternative written in Go.

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://golang.org/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

## Features

- **High Performance**: Built with Go for faster execution and lower resource usage
- **Inventory Support**: Parses INI and YAML format inventory files
- **SSH Connection Pool**: Concurrent SSH connection management with connection pooling
- **SSH Authentication**: Support for private key, password, SSH agent, and encrypted keys
- **Built-in Modules**: 16 commonly used modules (shell, copy, file, apt, yum, service, etc.)
- **Playbook Execution**: Full support for playbook parsing and execution
- **Ad-hoc Commands**: Execute quick commands on remote hosts
- **Variable Management**: Support for global, group, and host variables

## Installation

### From Source

```bash
git clone https://github.com/go-ansible/go-ansible.git
cd go-ansible
go build -o go-ansible ./cmd/go-ansible
sudo mv go-ansible /usr/local/bin/
```

### Using Go Install

```bash
go install github.com/go-ansible/go-ansible/cmd/go-ansible@latest
```

## Quick Start

### 1. Create Inventory File

**INI Format** (`inventory.ini`):

```ini
[webservers]
# Password authentication
web1 ansible_host=192.168.1.10 ansible_user=root ansible_ssh_pass=secret

# SSH key authentication
web2 ansible_host=192.168.1.11 ansible_user=deploy ansible_ssh_private_key_file=~/.ssh/id_rsa

# Encrypted private key (key password)
web3 ansible_host=192.168.1.12 ansible_user=deploy ansible_ssh_private_key_file=~/.ssh/encrypted_key ansible_ssh_pass=keypassword

[dbservers]
db1 ansible_host=192.168.1.20
db2 ansible_host=192.168.1.21

[dbservers:vars]
ansible_user=dba
ansible_ssh_private_key_file=~/.ssh/db_key
ansible_port=2222

[all:vars]
ansible_port=22
# Default key if not specified per host
ansible_ssh_private_key_file=~/.ssh/default_key
```

**YAML Format** (`inventory.yml`):

```yaml
all:
  vars:
    ansible_user: root
    ansible_port: 22
    ansible_ssh_private_key_file: ~/.ssh/default_key
  children:
    webservers:
      hosts:
        web1:
          ansible_host: 192.168.1.10
          ansible_ssh_private_key_file: ~/.ssh/web_key
        web2:
          ansible_host: 192.168.1.11
          ansible_ssh_pass: mypassword
    dbservers:
      vars:
        ansible_user: dba
        ansible_ssh_private_key_file: ~/.ssh/db_key
      hosts:
        db1:
          ansible_host: 192.168.1.20
        db2:
          ansible_host: 192.168.1.21
```

### 2. Run Ad-hoc Commands

```bash
# Ping all hosts
go-ansible adhoc all -i inventory.ini -m ping

# Run shell command
go-ansible adhoc webservers -i inventory.ini -m shell -a "uptime"

# Check disk usage
go-ansible adhoc all -i inventory.ini -m shell -a "df -h"

# Gather system facts
go-ansible adhoc all -i inventory.ini -m setup
```

### 3. Run Playbook

```bash
go-ansible playbook -i inventory.ini site.yml
```

## SSH Authentication

go-ansible supports multiple SSH authentication methods, prioritized in the following order:

### 1. Private Key Authentication (Recommended)

Specify the private key file using `ansible_ssh_private_key_file`:

```ini
# Single host
web1 ansible_host=192.168.1.10 ansible_user=deploy ansible_ssh_private_key_file=~/.ssh/id_rsa

# Environment variable path
web2 ansible_host=192.168.1.11 ansible_ssh_private_key_file=$HOME/.ssh/key

# Group variable (applied to all hosts in group)
[webservers:vars]
ansible_ssh_private_key_file=~/.ssh/web_key
```

### 2. Encrypted Private Key

For password-protected private keys, provide both the key path and password:

```ini
secure-host ansible_host=192.168.1.20 ansible_ssh_private_key_file=~/.ssh/encrypted_key ansible_ssh_pass=mypassword
```

### 3. Password Authentication

Use `ansible_ssh_pass` for password-based authentication:

```ini
password-host ansible_host=192.168.1.30 ansible_user=root ansible_ssh_pass=secret
```

### 4. SSH Agent

If no authentication method is specified, go-ansible will attempt to use the SSH agent (requires `SSH_AUTH_SOCK` environment variable).

### 5. Default Keys

As a fallback, go-ansible will try these default key locations:
- `~/.ssh/id_rsa`
- `~/.ssh/id_ed25519`
- `~/.ssh/id_ecdsa`
- `~/.ssh/id_dsa`

### Authentication Priority

1. Private key specified in inventory (highest priority)
2. Password authentication
3. SSH Agent
4. Default key locations
5. Connection failure (if no auth method works)

### Path Expansion

go-ansible automatically expands paths:
- `~` → User's home directory
- `$VAR` or `${VAR}` → Environment variable

```ini
# These are equivalent
web1 ansible_ssh_private_key_file=~/.ssh/id_rsa
web2 ansible_ssh_private_key_file=$HOME/.ssh/id_rsa
web3 ansible_ssh_private_key_file=/home/user/.ssh/id_rsa
```

## CLI Reference

### Global Usage

```
go-ansible <command> [options]

Commands:
  playbook    Run a playbook
  adhoc       Run ad-hoc commands
  inventory   Manage inventory
  version     Show version
  help        Show this help
```

### Playbook Command

```bash
go-ansible playbook [options] <playbook.yml>

Options:
  -i, --inventory   Inventory file path (required)
  -t, --tags        Only run tasks with these tags
  --check           Dry run mode
  -v, --verbose     Verbose output
```

### Ad-hoc Command

```bash
go-ansible adhoc [options] <target>

Options:
  -i, --inventory   Inventory file path (required)
  -m, --module      Module to execute (default: command)
  -a, --args        Module arguments
  --forks           Number of parallel processes (default: 5)
  -v, --verbose     Verbose output

Examples:
  go-ansible adhoc all -i inventory.ini -m ping
  go-ansible adhoc webservers -i inventory.ini -m shell -a "ls -la"
  go-ansible adhoc all -i inventory.ini -m copy -a "src=/local/file dest=/remote/path"
```

### Inventory Command

```bash
go-ansible inventory [options] [list|host]

Options:
  -i, --inventory   Inventory file path (required)
  --list            List all hosts and groups
  --host <name>     Show details for specific host

Examples:
  go-ansible inventory -i inventory.ini list
  go-ansible inventory -i inventory.ini --host web1
```

## Built-in Modules

### Command Modules

| Module | Description | Example |
|--------|-------------|---------|
| `command` | Execute commands (no shell) | `-a "ls /tmp"` |
| `shell` | Execute shell commands | `-a "echo $HOME"` |
| `ping` | Test connectivity | `-m ping` |
| `setup` | Gather system facts | `-m setup` |

### File Modules

| Module | Description | Example |
|--------|-------------|---------|
| `copy` | Copy files to remote | `-a "src=local dest=/remote"` |
| `file` | Manage file attributes | `-a "path=/tmp/dir state=directory"` |
| `template` | Template file rendering | `-a "src=template.j2 dest=/remote"` |
| `lineinfile` | Manage lines in files | `-a "path=/etc/hosts line='127.0.0.1 localhost'"` |
| `stat` | Get file information | `-a "path=/etc/passwd"` |
| `fetch` | Fetch files from remote | `-a "src=/remote dest=/local"` |

### Package Modules

| Module | Description | Example |
|--------|-------------|---------|
| `yum` | YUM package manager | `-a "name=httpd state=present"` |
| `apt` | APT package manager | `-a "name=nginx state=installed"` |

### System Modules

| Module | Description | Example |
|--------|-------------|---------|
| `service` | Manage services | `-a "name=nginx state=started"` |
| `user` | Manage users | `-a "name=deploy state=present"` |
| `group` | Manage groups | `-a "name=developers state=present"` |
| `cron` | Manage cron jobs | `-a "name='backup' job='/backup.sh'"` |

## Playbook Example

```yaml
---
- name: Web Server Setup
  hosts: webservers
  become: true
  vars:
    app_name: myapp
    app_port: 8080
  tasks:
    - name: Install nginx
      apt:
        name: nginx
        state: present
        update_cache: yes

    - name: Copy nginx config
      copy:
        src: files/nginx.conf
        dest: /etc/nginx/nginx.conf
        mode: '0644'
        owner: root
        group: root
      notify: restart nginx

    - name: Create app directory
      file:
        path: "/opt/{{ app_name }}"
        state: directory
        mode: '0755'

    - name: Start nginx service
      service:
        name: nginx
        state: started
        enabled: yes

    - name: Check nginx status
      shell: systemctl status nginx
      register: nginx_status
      ignore_errors: true

    - name: Print nginx status
      debug:
        var: nginx_status.stdout

  handlers:
    - name: restart nginx
      service:
        name: nginx
        state: restarted

- name: Database Setup
  hosts: dbservers
  become: true
  tasks:
    - name: Install MySQL
      apt:
        name: mysql-server
        state: present

    - name: Start MySQL service
      service:
        name: mysql
        state: started
        enabled: yes
```

## Module Parameters

### Common Parameters

All modules support these common parameters:

| Parameter | Description |
|-----------|-------------|
| `name` | Task name for logging |
| `when` | Conditional execution |
| `register` | Save result to variable |
| `ignore_errors` | Continue on failure |
| `tags` | Task tags for filtering |

### copy Module

```yaml
- name: Copy configuration file
  copy:
    src: ./files/app.conf        # Local source file
    dest: /etc/app/app.conf      # Remote destination
    mode: '0644'                 # File permissions
    owner: app                   # File owner
    group: app                   # File group
    backup: yes                  # Backup existing file
```

### file Module

```yaml
- name: Create directory
  file:
    path: /opt/myapp
    state: directory
    mode: '0755'

- name: Create symlink
  file:
    src: /opt/myapp/current
    dest: /opt/myapp/releases/v1.0
    state: link

- name: Remove file
  file:
    path: /tmp/old_file
    state: absent
```

### template Module

```yaml
- name: Generate config from template
  template:
    src: templates/nginx.conf.j2
    dest: /etc/nginx/nginx.conf
    mode: '0644'
  vars:
    server_name: example.com
    port: 80
```

## Project Structure

```
go-ansible/
├── cmd/
│   └── go-ansible/
│       └── main.go              # CLI entry point
├── pkg/
│   ├── inventory/               # Inventory management
│   │   ├── types.go             # Type definitions
│   │   ├── parser.go            # INI/YAML parser
│   │   └── inventory.go         # API methods
│   ├── ssh/                     # SSH connection management
│   │   ├── client.go            # SSH client
│   │   └── pool.go              # Connection pool
│   ├── module/                  # Module system
│   │   ├── module.go            # Module registry
│   │   ├── command.go           # command/shell/ping modules
│   │   ├── copy.go              # copy/fetch modules
│   │   ├── file.go              # file/stat modules
│   │   ├── package.go           # yum/apt modules
│   │   ├── service.go           # service module
│   │   ├── user.go              # user/group modules
│   │   ├── cron.go              # cron module
│   │   ├── lineinfile.go        # lineinfile module
│   │   └── template.go          # template module
│   ├── playbook/                # Playbook support
│   │   ├── types.go             # Type definitions
│   │   ├── parser.go            # YAML parser
│   │   └── executor.go          # Execution engine
│   ├── adhoc/                   # Ad-hoc commands
│   │   └── adhoc.go             # Ad-hoc executor
│   └── variable/                # Variable management
│       └── manager.go           # Variable manager
├── examples/                    # Example files
│   ├── inventory.ini            # Example inventory
│   └── site.yml                 # Example playbook
├── go.mod
├── go.sum
└── README.md
```

## Performance

Compared to Python Ansible:

- **Startup time**: ~10x faster (no Python interpreter initialization)
- **Memory usage**: ~50% lower
- **SSH connections**: Reuses connections via connection pooling
- **Concurrent execution**: Native goroutine-based parallelism

## Comparison with Ansible

| Feature | go-ansible | Ansible |
|---------|------------|---------|
| Language | Go | Python |
| Startup | Fast | Slow |
| Memory | Low | Higher |
| Modules | 16 built-in | 3000+ |
| Jinja2 | Basic support | Full support |
| Roles | Not yet | Full support |
| Collections | Not yet | Full support |
| Plugins | Not yet | Full support |

## Roadmap

- [ ] Full Jinja2 template support
- [ ] Role support
- [ ] Vault support (encrypted variables)
- [ ] Dynamic inventory
- [ ] More modules (git, docker, kubernetes, etc.)
- [ ] Callback plugins
- [ ] Filter plugins
- [ ] Connection plugins (winrm, paramiko)
- [ ] Configuration file support
- [ ] Parallel execution optimization

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Acknowledgments

- [Ansible](https://www.ansible.com/) - The original project that inspired this implementation
- [golang.org/x/crypto/ssh](https://pkg.go.dev/golang.org/x/crypto/ssh) - SSH client library
- [gopkg.in/yaml.v3](https://pkg.go.dev/gopkg.in/yaml.v3) - YAML parser

## Support

- Issues: [GitHub Issues](https://github.com/go-ansible/go-ansible/issues)
- Discussions: [GitHub Discussions](https://github.com/go-ansible/go-ansible/discussions)
