package playbook

import (
	"path/filepath"
	"sort"

	"go-ansible/pkg/inventory"
)

type ListedPlay struct {
	Name  string
	Hosts string
	Tasks []ListedTask
	Tags  []string
}

type ListedTask struct {
	Name       string
	ModuleName string
	Tags       []string
}

type ListedPlayHosts struct {
	Name  string
	Hosts string
	Items []string
}

func ListPlaybook(playbook *Playbook, rolePaths []string) ([]ListedPlay, error) {
	if playbook == nil {
		return nil, nil
	}

	baseDir := playbook.BaseDir()
	executor := NewExecutor(nil)
	executor.baseDir = baseDir
	executor.SetRolePaths(rolePaths)

	result := make([]ListedPlay, 0, len(playbook.Plays))
	for _, play := range playbook.Plays {
		roleTasks, _, _, err := executor.loadPlayRoles(play)
		if err != nil {
			return nil, err
		}

		preTasks := cloneTasks(play.PreTasks)
		mainTasks := cloneTasks(play.Tasks)
		postTasks := cloneTasks(play.PostTasks)
		roleTasks = cloneTasks(roleTasks)

		mergePlayTags(play.Tags, preTasks)
		mergePlayTags(play.Tags, roleTasks)
		mergePlayTags(play.Tags, mainTasks)
		mergePlayTags(play.Tags, postTasks)

		listed := ListedPlay{
			Name:  play.Name,
			Hosts: play.Hosts,
			Tasks: make([]ListedTask, 0, len(preTasks)+len(roleTasks)+len(mainTasks)+len(postTasks)),
		}
		for _, tasks := range [][]*Task{preTasks, roleTasks, mainTasks, postTasks} {
			for _, task := range tasks {
				listed.Tasks = append(listed.Tasks, listTasks(task, baseDir)...)
			}
		}
		listed.Tags = collectListedTags(listed.Tasks)
		result = append(result, listed)
	}

	return result, nil
}

func listTasks(task *Task, baseDir string) []ListedTask {
	if task == nil {
		return nil
	}

	if isBlockTask(task) {
		result := make([]ListedTask, 0)
		for _, child := range cloneTasks(task.Block) {
			applyParentTaskAttributes(task, []*Task{child})
			result = append(result, listTasks(child, baseDir)...)
		}
		for _, child := range cloneTasks(task.Rescue) {
			applyParentTaskAttributes(task, []*Task{child})
			result = append(result, listTasks(child, baseDir)...)
		}
		for _, child := range cloneTasks(task.Always) {
			applyParentTaskAttributes(task, []*Task{child})
			result = append(result, listTasks(child, baseDir)...)
		}
		return result
	}

	if task.ModuleName == "import_tasks" {
		result := []ListedTask{listedTask(task)}
		for _, child := range loadStaticListedInclude(task, baseDir) {
			applyParentTaskAttributes(task, []*Task{child})
			result = append(result, listTasks(child, baseDir)...)
		}
		return result
	}

	return []ListedTask{listedTask(task)}
}

func loadStaticListedInclude(task *Task, baseDir string) []*Task {
	if task == nil {
		return nil
	}
	raw, ok := task.Params["_raw_params"].(string)
	if !ok || raw == "" || contains(raw, "{{") {
		return nil
	}
	path := raw
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	parser := NewParser(baseDir)
	tasks, err := parser.ResolveInclude(path)
	if err != nil {
		return nil
	}
	return tasks
}

func listedTask(task *Task) ListedTask {
	return ListedTask{
		Name:       task.Name,
		ModuleName: task.ModuleName,
		Tags:       normalizeTagList(task.Tags),
	}
}

func collectListedTags(tasks []ListedTask) []string {
	seen := make(map[string]struct{})
	for _, task := range tasks {
		for _, tag := range task.Tags {
			seen[tag] = struct{}{}
		}
	}

	result := make([]string, 0, len(seen))
	for tag := range seen {
		result = append(result, tag)
	}
	sort.Strings(result)
	return result
}

func ListPlayHosts(playbook *Playbook, inv *inventory.Inventory, limit string, extraVars map[string]interface{}) ([]ListedPlayHosts, error) {
	if playbook == nil {
		return nil, nil
	}

	executor := NewExecutor(inv)
	executor.SetLimit(limit)
	if len(extraVars) > 0 {
		executor.SetExtraVars(extraVars)
	}

	result := make([]ListedPlayHosts, 0, len(playbook.Plays))
	for _, play := range playbook.Plays {
		hosts, err := executor.getPlayHosts(play)
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(hosts))
		for _, host := range hosts {
			names = append(names, host.Name)
		}
		sort.Strings(names)
		result = append(result, ListedPlayHosts{
			Name:  play.Name,
			Hosts: play.Hosts,
			Items: names,
		})
	}
	return result, nil
}
