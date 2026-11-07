package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// task is one Markdown file in a space: its state and its brief.
type task struct {
	space string
	id    string
	path  string
	head  header
	body  string
}

var (
	taskID  = regexp.MustCompile(`^[A-Za-z]+\d*-\d+$`)
	nonSlug = regexp.MustCompile(`[^a-z0-9]+`)
)

func taskPath(space, id string) string {
	return filepath.Join(store(), "spaces", space, id+".md")
}

// readTask checks a task file's fields and fills in its defaults.
func readTask(path string) (header, string, error) {
	head, body, err := parseFile(path)
	if err != nil {
		return header{}, "", err
	}
	if err := checkFields(path, head, taskRules); err != nil {
		return header{}, "", err
	}
	id := strings.TrimSuffix(filepath.Base(path), ".md")
	if given := head.val("id"); given != nil && given != "" && given != id {
		return header{}, "", refuse("%s says id %v, not %s", tilde(path), given, id)
	}
	if !head.has("id") {
		head.set("id", id)
	}
	if !head.has("status") {
		head.set("status", "todo")
	}
	if !head.has("asks") {
		head.set("asks", []any{})
	}
	return head, body, nil
}

// loadTask finds a task by id in any space.
func loadTask(id string) (task, error) {
	if !taskID.MatchString(id) {
		return task{}, refuse("%s is not a task id", id)
	}
	paths, _ := filepath.Glob(filepath.Join(store(), "spaces", "*", id+".md"))
	found := make([]string, 0, len(paths))
	for _, path := range paths {
		if isFile(path) {
			found = append(found, path)
		}
	}
	if len(found) == 0 {
		return task{}, refuse("there is no task %s", id)
	}
	if len(found) > 1 {
		names := make([]string, len(found))
		for i, path := range found {
			names[i] = filepath.Base(filepath.Dir(path))
		}
		return task{}, refuse("%s is in %d spaces: %s", id, len(found), strings.Join(names, ", "))
	}
	path := found[0]
	head, body, err := readTask(path)
	if err != nil {
		return task{}, err
	}
	return task{space: filepath.Base(filepath.Dir(path)), id: id, path: path, head: head, body: body}, nil
}

// update changes a task under the store's lock and saves it; when change
// refuses, nothing is written.
func update(id string, change func(t *task) error) (task, error) {
	unlock, err := lock()
	if err != nil {
		return task{}, err
	}
	defer unlock()
	t, err := loadTask(id)
	if err != nil {
		return task{}, err
	}
	if err := change(&t); err != nil {
		return task{}, err
	}
	return t, writeText(t.path, render(t.head, t.body))
}

// allTasks reads every task file, with the files that would not read.
func allTasks() ([]task, []error) {
	paths, _ := filepath.Glob(filepath.Join(store(), "spaces", "*", "*.md"))
	var tasks []task
	var problems []error
	for _, path := range paths {
		id := strings.TrimSuffix(filepath.Base(path), ".md")
		if !isFile(path) || !taskID.MatchString(id) {
			continue
		}
		head, body, err := readTask(path)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		tasks = append(tasks, task{
			space: filepath.Base(filepath.Dir(path)),
			id:    id,
			path:  path,
			head:  head,
			body:  body,
		})
	}
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].space != tasks[j].space {
			return tasks[i].space < tasks[j].space
		}
		return taskNumber(tasks[i].id) < taskNumber(tasks[j].id)
	})
	return tasks, problems
}

// taskNumber is the number after a task id's last dash.
func taskNumber(id string) int {
	n, _ := strconv.Atoi(id[strings.LastIndex(id, "-")+1:])
	return n
}

// taskLine is one row of shp list.
func taskLine(h header) string {
	text := fmt.Sprintf("%s  %s  %s", h.text("id"), h.text("status"), h.text("title"))
	var tail []string
	progress, hasProgress := h.number("progress")
	step := h.text("step")
	if hasProgress || step != "" {
		part := ""
		if hasProgress {
			part = strconv.Itoa(progress) + "%"
		}
		if step != "" {
			if part != "" {
				part += " "
			}
			part += step
		}
		tail = append(tail, part)
	}
	if asks := h.list("asks"); len(asks) > 0 {
		quoted := make([]string, len(asks))
		for i, item := range asks {
			ask, _ := item.(map[string]any)
			text, _ := ask["text"].(string)
			quoted[i] = `"` + text + `"`
		}
		tail = append(tail, "asks: "+strings.Join(quoted, " / "))
	}
	if left := h.text("left"); left != "" {
		tail = append(tail, left+" left")
	}
	if len(tail) > 0 {
		text += "  ·  " + strings.Join(tail, "  ·  ")
	}
	return text
}

func addCmd(raw []string) error {
	a, err := parseArgs(raw, []string{"space", "brief", "status", "parent", "after", "agent"}, nil)
	if err != nil {
		return err
	}
	if len(a.words) != 1 {
		return usage("add takes one title")
	}
	title := strings.TrimSpace(a.words[0])
	if title == "" {
		return refuse("a task needs a title")
	}
	status := "todo"
	if given, ok := a.value("status"); ok {
		if given != "todo" && given != "queued" {
			return usage("--status is todo or queued")
		}
		status = given
	}
	agent, err := agentFlag(a)
	if err != nil {
		return err
	}
	brief, err := readBrief(a.optional("brief"))
	if err != nil {
		return err
	}
	spaceName, _ := a.value("space")
	if spaceName == "" {
		spaceName, err = oneSpace()
		if err != nil {
			return err
		}
	}
	parent := a.optional("parent")
	after := a.values["after"]

	unlock, err := lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := ensureStore(); err != nil {
		return err
	}
	path, head, body, err := loadSpace(spaceName)
	if err != nil {
		return err
	}
	if parent != "" {
		if _, err := loadTask(parent); err != nil {
			return err
		}
	}
	for _, reference := range after {
		if _, err := loadTask(reference); err != nil {
			return err
		}
	}
	number, ok := head.number("next")
	if !ok {
		return refuse("%s has no usable next", tilde(path))
	}
	id, following := nextID(spaceName, head.text("prefix"), number)
	branch, worktree := taskPlace(spaceName, id, title)
	fields := []field{
		{"id", id},
		{"title", title},
		{"status", status},
		{"agent", agent},
		{"branch", branch},
		{"worktree", tilde(worktree)},
		{"asks", []any{}},
	}
	if parent != "" {
		fields = append(fields, field{"parent", parent})
	}
	if len(after) > 0 {
		references := make([]any, len(after))
		for i, reference := range after {
			references[i] = reference
		}
		fields = append(fields, field{"after", references})
	}
	newTask := header{fields: fields}
	taskBody := ""
	if brief != "" {
		taskBody = "\n" + brief
	}
	if err := writeText(taskPath(spaceName, id), render(newTask, taskBody)); err != nil {
		return err
	}
	head.set("next", json.Number(strconv.Itoa(following)))
	if err := writeText(path, render(head, body)); err != nil {
		return err
	}
	fmt.Println(id)
	return nil
}

func agentFlag(a *args) ([]any, error) {
	text, ok := a.value("agent")
	if !ok {
		return []any{}, nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return nil, refuse(`--agent is JSON like ["claude"]`)
	}
	words, ok := parsed.([]any)
	if !ok || !isStrings(words) {
		return nil, refuse(`--agent is JSON like ["claude"]`)
	}
	return words, nil
}

// taskPlace is the branch and worktree a task gets in its space.
func taskPlace(spaceName, id, title string) (string, string) {
	branch := "task/" + id[strings.LastIndex(id, "-")+1:] + "-" + slug(title)
	return branch, filepath.Join(homeDir(), ".herdr", "worktrees", spaceName, strings.ReplaceAll(branch, "/", "-"))
}

// nextID finds the first free id at or after number.
func nextID(spaceName, prefix string, number int) (string, int) {
	for {
		id := fmt.Sprintf("%s-%d", prefix, number)
		if !exists(taskPath(spaceName, id)) {
			return id, number + 1
		}
		number++
	}
}

// slug is a title as a branch word: lowercase, dashes, at most 48 letters.
func slug(title string) string {
	text := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(text) > 48 {
		text = text[:48]
	}
	text = strings.Trim(text, "-")
	if text == "" {
		return "task"
	}
	return text
}

func listCmd(raw []string) error {
	a, err := parseArgs(raw, []string{"space"}, []string{"json"})
	if err != nil {
		return err
	}
	if len(a.words) > 0 {
		return usage("list takes no arguments")
	}
	tasks, problems := allTasks()
	for _, problem := range problems {
		fmt.Fprintf(os.Stderr, "shp: %v\n", problem)
	}
	only, bySpace := a.value("space")
	var shown []task
	for _, t := range tasks {
		if !bySpace || t.space == only {
			shown = append(shown, t)
		}
	}
	if a.bools["json"] {
		heads := make([]header, len(shown))
		for i, t := range shown {
			heads[i] = t.head
		}
		if err := printJSON(heads); err != nil {
			return err
		}
	} else {
		for _, t := range shown {
			fmt.Println(taskLine(t.head))
		}
	}
	if len(problems) > 0 {
		return exitCode(1)
	}
	return nil
}

func showCmd(raw []string) error {
	a, err := parseArgs(raw, nil, []string{"json"})
	if err != nil {
		return err
	}
	if len(a.words) != 1 {
		return usage("show takes one task id")
	}
	t, err := loadTask(a.words[0])
	if err != nil {
		return err
	}
	if a.bools["json"] {
		return printJSON(t.head)
	}
	text, err := readText(t.path)
	if err != nil {
		return err
	}
	fmt.Print(text)
	return nil
}

func reportCmd(raw []string) error {
	a, err := parseArgs(raw, []string{"progress", "step", "left"}, nil)
	if err != nil {
		return err
	}
	if len(a.words) != 1 {
		return usage("status takes one task id")
	}
	progressText, hasProgress := a.value("progress")
	step, hasStep := a.value("step")
	left, hasLeft := a.value("left")
	if !hasProgress && !hasStep && !hasLeft {
		return refuse("nothing to set: use --progress, --step or --left")
	}
	progress := 0
	if hasProgress {
		n, err := strconv.Atoi(progressText)
		if err != nil {
			return usage("--progress is a number")
		}
		if n < 0 || n > 100 {
			return refuse("--progress is 0 to 100")
		}
		progress = n
	}
	unlock, err := lock()
	if err != nil {
		return err
	}
	defer unlock()
	t, err := loadTask(a.words[0])
	if err != nil {
		return err
	}
	if hasProgress {
		t.head.set("progress", json.Number(strconv.Itoa(progress)))
	}
	if hasStep {
		t.head.set("step", step)
	}
	if hasLeft {
		t.head.set("left", left)
	}
	if err := writeText(t.path, render(t.head, t.body)); err != nil {
		return err
	}
	fmt.Println(taskLine(t.head))
	return nil
}

func askCmd(raw []string) error {
	a, err := parseArgs(raw, nil, nil)
	if err != nil {
		return err
	}
	if len(a.words) != 2 {
		return usage("ask takes a task id and a question")
	}
	id := a.words[0]
	text := strings.TrimSpace(a.words[1])
	if text == "" {
		return refuse("an ask needs a question")
	}
	askID, err := newAskID()
	if err != nil {
		return err
	}
	unlock, err := lock()
	if err != nil {
		return err
	}
	defer unlock()
	t, err := loadTask(id)
	if err != nil {
		return err
	}
	if status := t.head.text("status"); status != "working" && status != "needs" {
		return refuse("%s is %s; only a working worker asks", id, status)
	}
	ask := map[string]any{"id": askID, "text": text, "time": askTime()}
	t.head.set("asks", append(t.head.list("asks"), ask))
	t.head.set("status", "needs")
	if err := writeText(t.path, render(t.head, t.body)); err != nil {
		return err
	}
	fmt.Printf("%s needs you: %s\n", id, text)
	return nil
}

// newAskID is a short random id for an ask.
func newAskID() (string, error) {
	bytes := make([]byte, 3)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("making an ask id: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

// askTime is when an ask was made, in UTC.
func askTime() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05-07:00")
}

func sayCmd(raw []string) error {
	a, err := parseArgs(raw, []string{"ask"}, nil)
	if err != nil {
		return err
	}
	if len(a.words) != 2 {
		return usage("say takes a task id and a message")
	}
	id, text := a.words[0], a.words[1]
	t, err := loadTask(id)
	if err != nil {
		return err
	}
	asks := t.head.list("asks")
	var answer map[string]any
	if chosen, ok := a.value("ask"); ok {
		for _, item := range asks {
			if ask, ok := item.(map[string]any); ok && ask["id"] == chosen {
				answer = ask
				break
			}
		}
		if answer == nil {
			return refuse("%s has no ask %s", id, chosen)
		}
	} else if len(asks) > 1 {
		ids := make([]string, len(asks))
		for i, item := range asks {
			ask, _ := item.(map[string]any)
			ids[i] = fmt.Sprint(ask["id"])
		}
		return refuse("%s has %d asks; pass --ask one of %s", id, len(asks), strings.Join(ids, ", "))
	} else if len(asks) == 1 {
		answer, _ = asks[0].(map[string]any)
	}
	if status := t.head.text("status"); status != "working" && status != "needs" {
		return refuse("%s is %s; a message goes to a working or waiting worker", id, status)
	}
	terminal, err := workerTerminal(id)
	if err != nil {
		return err
	}
	if terminal == "" {
		return refuse("%s has no live worker; edit its status to todo or queued and start it", id)
	}
	stdout, stderr, err := run("hc", "send", "--", terminal, text)
	if err != nil {
		return refuse("the message did not go in: %s; %s still needs you", reason(stderr, stdout), id)
	}

	unlock, err := lock()
	if err != nil {
		return err
	}
	defer unlock()
	current, err := loadTask(id)
	if err != nil {
		return err
	}
	if answer != nil {
		kept := []any{}
		for _, item := range current.head.list("asks") {
			if ask, ok := item.(map[string]any); ok && ask["id"] == answer["id"] {
				continue
			}
			kept = append(kept, item)
		}
		current.head.set("asks", kept)
	}
	if current.head.text("status") == "needs" && len(current.head.list("asks")) == 0 {
		current.head.set("status", "working")
	}
	if err := writeText(current.path, render(current.head, current.body)); err != nil {
		return err
	}
	fmt.Printf("%s: message sent", id)
	if answer != nil {
		fmt.Printf(", ask %v closed", answer["id"])
	}
	fmt.Println()
	return nil
}
