package playbook

import "testing"

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
