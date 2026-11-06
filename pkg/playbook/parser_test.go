package playbook

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParserParsesBasicPlaybook(t *testing.T) {
	data := []byte(`
- name: Example
  hosts: web
  tasks:
    - name: Run command
      shell: echo hello
`)

	parser := NewParser("")
	pb, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if len(pb.Plays) != 1 {
		t.Fatalf("expected 1 play, got %d", len(pb.Plays))
	}

	task := pb.Plays[0].Tasks[0]
	if task.ModuleName != "shell" {
		t.Fatalf("expected shell module, got %q", task.ModuleName)
	}
	if task.Params["_raw_params"] != "echo hello" {
		t.Fatalf("expected raw params to be preserved, got %#v", task.Params["_raw_params"])
	}
}

func TestParserNormalizesTagsAndLoop(t *testing.T) {
	data := []byte(`
- name: Example
  hosts: web
  tasks:
    - name: Copy file
      tags: deploy
      with_items:
        - a
        - b
      copy:
        src: foo
        dest: /tmp/foo
`)

	parser := NewParser("")
	pb, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	task := pb.Plays[0].Tasks[0]
	if len(task.Tags) != 1 || task.Tags[0] != "deploy" {
		t.Fatalf("unexpected tags: %#v", task.Tags)
	}

	items, ok := task.Loop.([]interface{})
	if !ok || len(items) != 2 {
		t.Fatalf("expected with_items to map to loop, got %#v", task.Loop)
	}
}

func TestParserParsesLoopControl(t *testing.T) {
	data := []byte(`
- name: Example
  hosts: web
  tasks:
    - name: Show service
      loop:
        - api
      loop_control:
        loop_var: service
        index_var: service_index
        label: "{{ service }}"
      debug:
        msg: "{{ service_index }}:{{ service }}"
`)

	parser := NewParser("")
	pb, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	task := pb.Plays[0].Tasks[0]
	if task.LoopControl.LoopVar != "service" {
		t.Fatalf("expected loop_var service, got %q", task.LoopControl.LoopVar)
	}
	if task.LoopControl.IndexVar != "service_index" {
		t.Fatalf("expected index_var service_index, got %q", task.LoopControl.IndexVar)
	}
	if task.LoopControl.Label != "{{ service }}" {
		t.Fatalf("expected label to be preserved, got %q", task.LoopControl.Label)
	}
}

func TestParserNormalizesWithDictToLoop(t *testing.T) {
	data := []byte(`
- name: Example
  hosts: web
  tasks:
    - name: Render entries
      with_dict:
        app: 8080
        admin: 9090
      debug:
        msg: "{{ item.key }}={{ item.value }}"
`)

	parser := NewParser("")
	pb, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	task := pb.Plays[0].Tasks[0]
	switch items := task.Loop.(type) {
	case map[string]interface{}:
		if len(items) != 2 || items["app"] != 8080 || items["admin"] != 9090 {
			t.Fatalf("unexpected with_dict content: %#v", items)
		}
	case rawTask:
		if len(items) != 2 || items["app"] != 8080 || items["admin"] != 9090 {
			t.Fatalf("unexpected with_dict content: %#v", items)
		}
	default:
		t.Fatalf("expected with_dict to map to loop, got %#v", task.Loop)
	}
}

func TestParserNormalizesWithFileglobToLoop(t *testing.T) {
	data := []byte(`
- name: Example
  hosts: web
  tasks:
    - name: Copy templates
      with_fileglob: templates/*.j2
      debug:
        msg: "{{ item }}"
`)

	parser := NewParser("")
	pb, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	task := pb.Plays[0].Tasks[0]
	items, ok := task.Loop.(map[string]interface{})
	if !ok {
		t.Fatalf("expected with_fileglob to map to internal loop marker, got %#v", task.Loop)
	}
	if items["__with_fileglob__"] != "templates/*.j2" {
		t.Fatalf("unexpected with_fileglob content: %#v", items)
	}
}

func TestParserNormalizesWithSequenceToLoop(t *testing.T) {
	data := []byte(`
- name: Example
  hosts: web
  tasks:
    - name: Create users
      with_sequence: start=1 end=3 format=user%02d
      debug:
        msg: "{{ item }}"
`)

	parser := NewParser("")
	pb, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	task := pb.Plays[0].Tasks[0]
	items, ok := task.Loop.(map[string]interface{})
	if !ok {
		t.Fatalf("expected with_sequence to map to internal loop marker, got %#v", task.Loop)
	}
	if items["__with_sequence__"] != "start=1 end=3 format=user%02d" {
		t.Fatalf("unexpected with_sequence content: %#v", items)
	}
}

func TestParserNormalizesWithTogetherAndNestedToLoop(t *testing.T) {
	data := []byte(`
- name: Example
  hosts: web
  tasks:
    - name: Zip lists
      with_together:
        - "{{ users }}"
        - "{{ groups }}"
      debug:
        msg: "{{ item.0 }}={{ item.1 }}"
    - name: Product lists
      with_nested:
        - [a, b]
        - [1, 2]
      debug:
        msg: "{{ item.0 }}={{ item.1 }}"
`)

	parser := NewParser("")
	pb, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	together, ok := pb.Plays[0].Tasks[0].Loop.(map[string]interface{})
	if !ok || together["__with_together__"] == nil {
		t.Fatalf("expected with_together marker, got %#v", pb.Plays[0].Tasks[0].Loop)
	}

	nested, ok := pb.Plays[0].Tasks[1].Loop.(map[string]interface{})
	if !ok || nested["__with_nested__"] == nil {
		t.Fatalf("expected with_nested marker, got %#v", pb.Plays[0].Tasks[1].Loop)
	}
}

func TestParserParsesTaskVars(t *testing.T) {
	data := []byte(`
- name: Example
  hosts: web
  tasks:
    - name: Render template
      vars:
        app_port: 8080
      template:
        src: app.conf.j2
        dest: /etc/app.conf
`)

	parser := NewParser("")
	pb, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	task := pb.Plays[0].Tasks[0]
	if task.Vars["app_port"] != 8080 {
		t.Fatalf("unexpected task vars: %#v", task.Vars)
	}
}

func TestParserParsesChangedWhenAndFailedWhen(t *testing.T) {
	data := []byte(`
- name: Example
  hosts: web
  tasks:
    - name: Probe app
      changed_when: result.rc != 0
      failed_when: result.stdout == "panic"
      shell: echo hello
`)

	parser := NewParser("")
	pb, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	task := pb.Plays[0].Tasks[0]
	if task.ChangedWhen != "result.rc != 0" {
		t.Fatalf("unexpected changed_when: %#v", task.ChangedWhen)
	}
	if task.FailedWhen != `result.stdout == "panic"` {
		t.Fatalf("unexpected failed_when: %#v", task.FailedWhen)
	}
}

func TestParserParsesPlayFailureControls(t *testing.T) {
	data := []byte(`
- name: Example
  hosts: web
  any_errors_fatal: true
  force_handlers: yes
  tasks:
    - name: noop
      debug:
        msg: ok
`)

	parser := NewParser("")
	pb, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	play := pb.Plays[0]
	if !play.AnyErrorsFatal {
		t.Fatal("expected any_errors_fatal to be true")
	}
	if !play.ForceHandlers {
		t.Fatal("expected force_handlers to be true")
	}
}

func TestParserParsesAsyncAndPoll(t *testing.T) {
	data := []byte(`
- name: Example
  hosts: web
  tasks:
    - name: background restart
      async: 120
      poll: 5
      shell: sleep 1
`)

	parser := NewParser("")
	pb, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	task := pb.Plays[0].Tasks[0]
	if task.Async != 120 {
		t.Fatalf("expected async 120, got %d", task.Async)
	}
	if task.Poll == nil || *task.Poll != 5 {
		t.Fatalf("expected poll 5, got %#v", task.Poll)
	}
}

func TestParserParsesIncludeTasks(t *testing.T) {
	data := []byte(`
- name: Example
  hosts: web
  tasks:
    - name: Include deploy steps
      include_tasks: tasks/deploy.yml
`)

	parser := NewParser("")
	pb, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	task := pb.Plays[0].Tasks[0]
	if task.ModuleName != "include_tasks" {
		t.Fatalf("expected include_tasks module, got %q", task.ModuleName)
	}
	if task.Params["_raw_params"] != "tasks/deploy.yml" {
		t.Fatalf("unexpected include params: %#v", task.Params)
	}
}

func TestParserParsesBlockRescueAlways(t *testing.T) {
	data := []byte(`
- name: Example
  hosts: web
  tasks:
    - name: guarded block
      block:
        - name: first step
          debug:
            msg: hello
      rescue:
        - name: recover
          debug:
            msg: rescued
      always:
        - name: cleanup
          debug:
            msg: cleanup
`)

	parser := NewParser("")
	pb, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	task := pb.Plays[0].Tasks[0]
	if task.ModuleName != "block" {
		t.Fatalf("expected block task, got %q", task.ModuleName)
	}
	if len(task.Block) != 1 || len(task.Rescue) != 1 || len(task.Always) != 1 {
		t.Fatalf("unexpected block structure: %#v", task)
	}
}

func TestParserParsesHandler(t *testing.T) {
	data := []byte(`
- name: Example
  hosts: web
  handlers:
    - name: restart nginx
      listen: restart web
      service:
        name: nginx
        state: restarted
`)

	parser := NewParser("")
	pb, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	handler := pb.Plays[0].Handlers[0]
	if handler.ModuleName != "service" {
		t.Fatalf("expected service handler, got %q", handler.ModuleName)
	}
	if handler.Listen != "restart web" {
		t.Fatalf("expected listen to be preserved, got %q", handler.Listen)
	}
}

func TestParserParsesHandlerTags(t *testing.T) {
	data := []byte(`
- name: Example
  hosts: web
  handlers:
    - name: restart nginx
      tags: reload
      service:
        name: nginx
        state: restarted
`)

	parser := NewParser("")
	pb, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	handler := pb.Plays[0].Handlers[0]
	if len(handler.Tags) != 1 || handler.Tags[0] != "reload" {
		t.Fatalf("unexpected handler tags: %#v", handler.Tags)
	}
}

func TestParserParsesTaskAndHandlerBecomeFields(t *testing.T) {
	data := []byte(`
- name: Example
  hosts: web
  tasks:
    - name: privileged command
      become: true
      become_user: app
      become_method: su
      become_password: secret
      shell: id
  handlers:
    - name: restart app
      become: true
      become_user: root
      become_method: sudo
      become_pass: handler-secret
      shell: systemctl restart app
`)

	parser := NewParser("")
	pb, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	task := pb.Plays[0].Tasks[0]
	if task.Become == nil || !*task.Become || task.BecomeUser != "app" || task.BecomeMethod != "su" || task.BecomePass != "secret" {
		t.Fatalf("unexpected task become fields: %#v", task)
	}

	handler := pb.Plays[0].Handlers[0]
	if handler.Become == nil || !*handler.Become || handler.BecomeUser != "root" || handler.BecomeMethod != "sudo" || handler.BecomePass != "handler-secret" {
		t.Fatalf("unexpected handler become fields: %#v", handler)
	}
}

func TestParserRejectsTaskWithoutModule(t *testing.T) {
	data := []byte(`
- name: Example
  hosts: web
  tasks:
    - name: Missing module
      tags: test
`)

	parser := NewParser("")
	if _, err := parser.Parse(data); err == nil {
		t.Fatal("expected parser error for task without module")
	}
}

func TestParserRejectsTaskWithMultipleModules(t *testing.T) {
	data := []byte(`
- name: Example
  hosts: web
  tasks:
    - name: Invalid task
      shell: echo hi
      command: echo hi
`)

	parser := NewParser("")
	if _, err := parser.Parse(data); err == nil {
		t.Fatal("expected parser error for task with multiple modules")
	}
}

func TestParserParsesExampleDeployPlaybook(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "deploy.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	parser := NewParser("")
	pb, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if len(pb.Plays) != 1 {
		t.Fatalf("expected 1 play, got %d", len(pb.Plays))
	}

	play := pb.Plays[0]
	if play.Hosts != "{{ Hosts }}" {
		t.Fatalf("unexpected hosts expression: %q", play.Hosts)
	}
	if len(play.Tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(play.Tasks))
	}
	if play.Tasks[0].ModuleName != "copy" {
		t.Fatalf("expected first task to be copy, got %q", play.Tasks[0].ModuleName)
	}
	if play.Tasks[1].ModuleName != "shell" {
		t.Fatalf("expected second task to be shell, got %q", play.Tasks[1].ModuleName)
	}
	if play.Tasks[1].Params["cmd"] != "{{ item.cmd }}" {
		t.Fatalf("expected second task cmd param to be preserved, got %#v", play.Tasks[1].Params)
	}
	if play.Tasks[2].ModuleName != "wait_for" {
		t.Fatalf("expected third task to be wait_for, got %q", play.Tasks[2].ModuleName)
	}
	if len(play.Handlers) != 1 || play.Handlers[0].ModuleName != "shell" {
		t.Fatalf("unexpected handlers: %#v", play.Handlers)
	}
}
