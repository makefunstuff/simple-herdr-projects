package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// oldProject is one project file of Shepherdr 3.1, ~/.herdr/tasks/<slug>.json.
type oldProject struct {
	path   string
	Folder string
	Kind   string
	Name   string
	Slug   string
	Prefix string
	Next   int
	GitHub struct{ Repo string }
	Tasks  []oldTask
}

// oldTask is the part of a Shepherdr task that shp has a place for, with its
// whole record to keep beside it.
type oldTask struct {
	raw      json.RawMessage
	ID       int
	Title    string
	Prompt   string
	State    string
	Branch   string
	Base     string
	Worktree string
	Agent    string
	Agents   []struct {
		Address string
		Agent   string
		Ended   int64
	}
	After    []int
	AfterExt []struct {
		Ref     string
		Machine string
	} `json:"after_ext"`
	Reviews []struct {
		Text string
		Head string
	}
	Summary  string
	Proof    struct{ ID string }
	PR       struct{ URL string }
	Progress *struct {
		Text string
		Done int
		Left int64
	}
	Details  string
	Waits    string
	Archived int64
}

// UnmarshalJSON decodes a task and keeps its record as it was.
func (t *oldTask) UnmarshalJSON(data []byte) error {
	type plain oldTask // without this method
	if err := json.Unmarshal(data, (*plain)(t)); err != nil {
		return err
	}
	t.raw = slices.Clone(data)
	return nil
}

// role is one of Shepherdr's role settings in ~/.herdr/shepherdr.json.
type role struct {
	Agent, Model, Effort, Perm string
	Fast                       bool
}

// placed is where a Shepherdr project goes: a new space, or the one already on
// its folder; notes say how it differs from the project.
type placed struct {
	old   *oldProject
	name  string
	head  header
	body  string
	notes []string
}

// importPlan is everything an import would write, worked out before anything is.
type importPlan struct {
	writes []planned         // in write order
	adopt  map[string]string // a task id and the terminal of its live old worker
	report strings.Builder
}

// planned is a file an import writes.
type planned struct{ path, text string }

func (plan *importPlan) write(path, text string) {
	plan.writes = append(plan.writes, planned{path, text})
}

func (plan *importPlan) say(format string, args ...any) {
	fmt.Fprintf(&plan.report, format+"\n", args...)
}

// importCmd copies Shepherdr 3.1's projects, tasks and roles into shp's store
// and adopts the old workers still running. Shepherdr's files are only read.
func importCmd(raw []string) error {
	a, err := parseArgs(raw, nil, []string{"dry-run"})
	if err != nil {
		return err
	}
	if len(a.words) > 0 {
		return usage("import takes no arguments")
	}
	dryRun := a.bools["dry-run"]
	projects, unread, err := readProjects()
	if err != nil {
		return err
	}
	live, err := liveTerminals()
	if err != nil {
		return err
	}
	if !dryRun {
		unlock, err := lock()
		if err != nil {
			return err
		}
		defer unlock()
	}
	plan, err := planImport(projects, live)
	if err != nil {
		return err
	}
	for _, line := range unread {
		plan.say("%s", line)
	}
	if dryRun {
		fmt.Print("Dry run: nothing was written.\n\n" + plan.report.String())
		return nil
	}
	reportPath := filepath.Join(store(), "import.md")
	plan.write(reportPath, plan.report.String())
	for _, w := range plan.writes {
		if err := writeText(w.path, w.text); err != nil {
			return err
		}
	}
	fmt.Print(plan.report.String())
	fmt.Printf("\nWritten to %s; this report is %s.\n", tilde(store()), tilde(reportPath))
	failed := false
	for _, id := range slices.Sorted(maps.Keys(plan.adopt)) {
		terminal := plan.adopt[id]
		if _, err := hc("label", terminal, "herdr.task="+id, "herdr.role=worker"); err != nil {
			fmt.Fprintf(os.Stderr, "shp: %s's worker %s was not adopted: %v\n", id, terminal, err)
			failed = true
		}
	}
	if failed {
		return exitCode(1)
	}
	return nil
}

// readProjects reads Shepherdr's project files in name order, with why each
// file that would not read is left out.
func readProjects() ([]oldProject, []string, error) {
	dir := filepath.Join(homeDir(), ".herdr", "tasks")
	paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var projects []oldProject
	var unread []string
	for _, path := range paths {
		if strings.HasPrefix(filepath.Base(path), ".") {
			continue // Shepherdr's own bookkeeping
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, wrap(path, err)
		}
		p := oldProject{path: path}
		if err := json.Unmarshal(data, &p); err != nil {
			unread = append(unread, fmt.Sprintf("Not imported: %s: %v.", tilde(path), err))
			continue
		}
		projects = append(projects, p)
	}
	if len(projects)+len(unread) == 0 {
		return nil, nil, refuse("%s holds no Shepherdr projects", tilde(dir))
	}
	return projects, unread, nil
}

// planImport works out the files an import writes and the report it gives.
func planImport(projects []oldProject, live []string) (*importPlan, error) {
	plan := &importPlan{adopt: map[string]string{}}
	if err := plan.roles(); err != nil {
		return nil, err
	}
	existing, err := spaces()
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	for _, s := range existing {
		used[s.head.text("prefix")] = true
	}
	// Every space first, so an after can name a task of another space.
	var places []*placed
	refs := map[string]string{}     // an old task ref, like HERD-31, and its shp id
	onFolder := map[string]string{} // a folder and the project placed on it
	for i := range projects {
		p := &projects[i]
		resolved, err := resolveDir(p.Folder)
		if err != nil {
			return nil, err
		}
		folder := tilde(resolved)
		if other := onFolder[folder]; other != "" {
			plan.say("Not imported: %s: %s is on the same folder.\n", tilde(p.path), tilde(other))
			continue
		}
		place, why, err := placeSpace(p, folder, existing)
		if err != nil {
			return nil, err
		}
		if why != "" {
			plan.say("Not imported: %s: %s.\n", tilde(p.path), why)
			continue
		}
		prefix := place.head.text("prefix")
		switch {
		case prefix != "":
			if prefix != p.Prefix {
				place.notes = append(place.notes, fmt.Sprintf("It joined the space on its folder, so its tasks are %s-<n>, not %s-<n>.", prefix, p.Prefix))
			}
		case !prefixForm.MatchString(p.Prefix):
			prefix = freshPrefix(initials(place.name), used)
			place.notes = append(place.notes, fmt.Sprintf("%q is not 2 to 5 capitals, so its tasks are %s-<n>.", p.Prefix, prefix))
		default:
			prefix = freshPrefix(p.Prefix, used)
			if prefix != p.Prefix {
				place.notes = append(place.notes, fmt.Sprintf("%s was taken, so its tasks are %s-<n>, not %s-<n>.", p.Prefix, prefix, p.Prefix))
			}
		}
		place.head.set("prefix", prefix)
		used[prefix] = true
		onFolder[folder] = p.path
		existing = append(existing, space{name: place.name, head: place.head})
		places = append(places, place)
		for _, t := range p.Tasks {
			oldRef, id := fmt.Sprintf("%s-%d", p.Prefix, t.ID), fmt.Sprintf("%s-%d", prefix, t.ID)
			other, err := isOtherTask(taskPath(place.name, id), oldRef)
			if err != nil {
				return nil, err
			}
			if !other { // a reference to a task that shp has as another stays unresolved
				refs[oldRef] = id
			}
		}
	}
	isLive := map[string]bool{}
	for _, address := range live {
		isLive[address] = true
	}
	for _, place := range places {
		if err := plan.space(place, refs, isLive); err != nil {
			return nil, err
		}
	}
	return plan, nil
}

// isOtherTask reports whether a task file is there already for another task than oldRef.
func isOtherTask(path, oldRef string) (bool, error) {
	if !exists(path) {
		return false, nil
	}
	head, _, err := parseFile(path)
	if err != nil {
		return false, err
	}
	return head.text("imported_key") != oldRef, nil
}

// placeSpace finds the space a project goes to: the one already on its
// folder, else a new one named after it, or why there is none.
func placeSpace(p *oldProject, folder string, existing []space) (*placed, string, error) {
	for _, s := range existing {
		if s.head.text("folder") != folder {
			continue
		}
		head, body, err := parseFile(spacePath(s.name))
		if err != nil {
			return nil, "", err
		}
		return &placed{old: p, name: s.name, head: head, body: body}, "", nil
	}
	name := importedName(p, existing)
	if name == "" {
		return nil, "its name and slug make no space name", nil
	}
	var notes []string
	if name != p.Name {
		notes = append(notes, "Shepherdr called it "+p.Name+".")
	}
	head := header{fields: []field{{"folder", folder}}}
	if p.Kind == "repo" {
		base := "main"
		for _, t := range p.Tasks {
			if t.Base != "" {
				base = t.Base // the newest task's base
			}
		}
		merge := "local"
		if p.GitHub.Repo != "" {
			merge = "pr"
		}
		head.set("base", base)
		head.set("merge", merge)
		head.set("checks", []any{})
	}
	return &placed{old: p, name: name, head: head, notes: notes}, "", nil
}

// importedName is a project's name as a free space name, else its slug's;
// when both are taken, the first with a number added.
func importedName(p *oldProject, existing []space) string {
	taken := func(name string) bool {
		return slices.ContainsFunc(existing, func(s space) bool { return s.name == name })
	}
	first := ""
	for _, candidate := range []string{p.Name, p.Slug} {
		name := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(candidate), "-"), "-")
		if !spaceName.MatchString(name) {
			continue
		}
		if !taken(name) {
			return name
		}
		if first == "" {
			first = name
		}
	}
	if first == "" {
		return ""
	}
	for n := 2; ; n++ {
		if name := fmt.Sprintf("%s-%d", first, n); !taken(name) {
			return name
		}
	}
}

// space plans one project's tasks and its space file, with next past them all.
func (plan *importPlan) space(place *placed, refs map[string]string, isLive map[string]bool) error {
	p := place.old
	prefix := place.head.text("prefix")
	plan.say("## %s: %s, %s", place.name, prefix, place.head.text("folder"))
	for _, note := range place.notes {
		plan.say("%s", note)
	}
	if p.Kind != "repo" {
		plan.say("It is a folder, not a git repo: its tasks are listed, but none can start here.")
	} else if !exists(expand(place.head.text("folder"))) {
		plan.say("Its folder is gone.")
	}
	next := max(p.Next, 1)
	if n, ok := place.head.number("next"); ok {
		next = max(next, n)
	}
	open, done, archived, skipped := 0, 0, 0, 0
	for _, t := range p.Tasks {
		next = max(next, t.ID+1)
		head, imported, err := plan.task(place, t, refs, isLive)
		if err != nil {
			return err
		}
		switch {
		case !imported:
			skipped++
		case head.text("status") != "done":
			open++
			line := "  " + taskLine(head)
			if terminal := plan.adopt[head.text("id")]; terminal != "" {
				line += "  ·  adopts worker " + terminal
			}
			plan.say("%s", line)
		default:
			done++
			if t.Archived != 0 {
				archived++
			}
		}
	}
	counts := fmt.Sprintf("%d open, %d done (%d of them archived)", open, done, archived)
	if skipped > 0 {
		counts += fmt.Sprintf(", %d not written (above)", skipped)
	}
	plan.say("%s. Next is %s-%d.\n", counts, prefix, next)
	place.head.set("next", json.Number(strconv.Itoa(next)))
	plan.write(spacePath(place.name), render(place.head, place.body))
	return nil
}

// task plans one task's file, its review rounds and its whole old record; a
// task already in shp is left as it is, and reports false.
func (plan *importPlan) task(place *placed, t oldTask, refs map[string]string, isLive map[string]bool) (header, bool, error) {
	oldRef := fmt.Sprintf("%s-%d", place.old.Prefix, t.ID)
	id, ok := refs[oldRef]
	if !ok {
		plan.say("  Not imported: %s, as %s-%d is another task.", oldRef, place.head.text("prefix"), t.ID)
		return header{}, false, nil
	}
	path := taskPath(place.name, id)
	if exists(path) {
		plan.say("  %s is in shp already; kept as it is.", id)
		return header{}, false, nil
	}
	status := t.State
	if status == "running" {
		status = "working"
	}
	var attention []string // what the person should know, in the step
	after, missing := resolveAfter(t, place.old.Prefix, refs)
	if len(missing) > 0 {
		plan.say("  %s: its after %s did not import.", id, strings.Join(missing, ", "))
		if status == "todo" || status == "queued" {
			status = "todo"
			attention = append(attention, "it waited on "+strings.Join(missing, ", ")+", which shp does not have; start it when that is done")
		}
	}
	if status == "queued" && place.old.Kind != "repo" {
		status = "todo"
		attention = append(attention, "its space is a folder, not a git repo, so it cannot start")
	}
	// Shepherdr gives a task its branch and worktree when it starts it, and
	// its oldest tasks kept no worktree; shp gives both when it adds one.
	if place.old.Kind == "repo" {
		if t.Branch == "" && (status == "todo" || status == "queued") {
			t.Branch = taskBranch(id, t.Title)
		}
		if t.Branch != "" && t.Worktree == "" {
			t.Worktree = worktreeFor(place.name, t.Branch)
		}
	}
	reviewed := ""
	if n := len(t.Reviews); n > 0 {
		reviewed = t.Reviews[n-1].Head
	}
	if status == "ready" && !onCommit(t.Worktree, reviewed) {
		status = "working"
		attention = append(attention, "it was ready, but not on its reviewed commit: review it again")
	}
	if status == "working" || status == "ready" {
		if terminal := liveWorker(t, isLive); terminal != "" {
			plan.adopt[id] = terminal
		} else if status == "working" {
			attention = append(attention, "no live worker: set its status to todo and start it")
		}
	}

	head := header{fields: []field{{"id", id}, {"title", t.Title}, {"status", status}}}
	agent := []any{}
	if name := t.Agent; name != "" {
		agent = []any{name}
	} else if n := len(t.Agents); n > 0 && t.Agents[n-1].Agent != "" {
		agent = []any{t.Agents[n-1].Agent}
	}
	head.set("agent", agent)
	setText(&head, "branch", t.Branch)
	if t.Worktree != "" {
		head.set("worktree", tilde(t.Worktree))
	}
	head.set("asks", []any{})
	if len(after) > 0 {
		head.set("after", after)
	}
	if len(t.Reviews) > 0 {
		head.set("review", json.Number(strconv.Itoa(len(t.Reviews))))
	}
	// A round shp can trust: the one a ready task is on, or a done task's last.
	if status == "ready" || status == "done" {
		setText(&head, "reviewed_commit", reviewed)
	}
	if status == "ready" {
		head.set("blocking", json.Number("0"))
	}
	setText(&head, "summary", t.Summary)
	setText(&head, "proof", t.Proof.ID)
	setText(&head, "pr", t.PR.URL)
	if t.Progress != nil {
		head.set("progress", json.Number(strconv.Itoa(min(max(t.Progress.Done, 0), 100))))
		if t.Progress.Left > 0 {
			head.set("left", roughly(t.Progress.Left))
		}
	}
	step := t.Waits
	if step == "" && t.Progress != nil {
		step = t.Progress.Text
	}
	if len(attention) > 0 {
		step = "imported: " + strings.Join(attention, "; ")
	}
	setText(&head, "step", step)
	head.set("imported_key", oldRef)

	body := ""
	if prompt := strings.Trim(t.Prompt, "\n"); strings.TrimSpace(prompt) != "" {
		body = "\n" + prompt + "\n"
	}
	if details := strings.TrimSpace(t.Details); details != "" {
		body += "\n## Details from Shepherdr\n\n" + details + "\n"
	}
	plan.write(path, render(head, body))
	for i, r := range t.Reviews {
		round := header{}
		setText(&round, "reviewed_commit", r.Head)
		plan.write(filepath.Join(filepath.Dir(path), fmt.Sprintf("%s.review-%d.md", id, i+1)), render(round, "\n"+r.Text+"\n"))
	}
	var kept bytes.Buffer
	if err := json.Indent(&kept, t.raw, "", "  "); err != nil {
		return header{}, false, fmt.Errorf("%s: %w", oldRef, err)
	}
	plan.write(filepath.Join(filepath.Dir(path), id+".shepherdr.json"), kept.String()+"\n")
	return head, true, nil
}

// resolveAfter turns a task's after and after_ext into shp ids, with the
// references that have none.
func resolveAfter(t oldTask, prefix string, refs map[string]string) ([]any, []string) {
	var after []any
	var missing []string
	wanted := make([]string, 0, len(t.After)+len(t.AfterExt))
	for _, n := range t.After {
		wanted = append(wanted, fmt.Sprintf("%s-%d", prefix, n))
	}
	for _, ext := range t.AfterExt {
		if ext.Machine != "" {
			missing = append(missing, ext.Ref+" on "+ext.Machine)
			continue
		}
		wanted = append(wanted, ext.Ref)
	}
	for _, ref := range wanted {
		if id, ok := refs[ref]; ok {
			after = append(after, id)
		} else {
			missing = append(missing, ref)
		}
	}
	return after, missing
}

// onCommit reports whether a worktree is checked out on a commit.
func onCommit(worktree, commit string) bool {
	if worktree == "" || commit == "" {
		return false
	}
	head, err := gitHead(worktree)
	return err == nil && head == commit
}

// liveWorker is the terminal of a task's last agent, if it is still running.
func liveWorker(t oldTask, isLive map[string]bool) string {
	if len(t.Agents) == 0 {
		return ""
	}
	last := t.Agents[len(t.Agents)-1]
	if last.Ended != 0 || !isLive[last.Address] {
		return ""
	}
	return last.Address
}

// setText sets a header field when there is something to set.
func setText(h *header, key, value string) {
	if value != "" {
		h.set(key, value)
	}
}

// roughly writes milliseconds the way a worker reports time left: 20m, 3h.
func roughly(ms int64) string {
	minutes := (ms + 30_000) / 60_000
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dh", (minutes+30)/60)
}

// roles plans config.md with Shepherdr's everywhere roles and task settings.
func (plan *importPlan) roles() error {
	path := filepath.Join(homeDir(), ".herdr", "shepherdr.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		plan.say("No %s: config.md keeps its roles.\n", tilde(path))
		return nil
	}
	if err != nil {
		return wrap(path, err)
	}
	var settings struct {
		Everywhere struct {
			Coordinator, Worker, Reviewer *role
			Tasks                         struct {
				MustFix      string `json:"must_fix"`
				MaxRunning   int    `json:"max_running"`
				ReviewRounds int    `json:"review_rounds"`
			}
		}
		Spaces   map[string]json.RawMessage
		Machines map[string]json.RawMessage
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return refuse("%s: %v", tilde(path), err)
	}
	before, err := config()
	if err != nil {
		return err
	}
	cfg := header{fields: slices.Clone(before.fields)}
	plan.say("## Config, from %s", tilde(path))
	every := settings.Everywhere
	roles := []struct {
		name     string
		settings *role
	}{{"coordinator", every.Coordinator}, {"worker", every.Worker}, {"reviewer", every.Reviewer}}
	for _, r := range roles {
		if r.settings == nil || r.settings.Agent == "" {
			continue
		}
		argv := []any{r.settings.Agent}
		if r.settings.Model != "" {
			argv = append(argv, "--model", r.settings.Model)
		}
		cfg.set(r.name, argv)
		if r.settings.Effort != "" || r.settings.Perm != "" || r.settings.Fast {
			plan.say("The %s's effort, permissions and fast mode did not import: add their flags to its argv.", r.name)
		}
	}
	if slices.Contains(levels, every.Tasks.MustFix) {
		cfg.set("must_fix", every.Tasks.MustFix)
	}
	if every.Tasks.MaxRunning > 0 {
		cfg.set("max_running", json.Number(strconv.Itoa(every.Tasks.MaxRunning)))
	}
	if every.Tasks.ReviewRounds > 0 {
		cfg.set("rounds", json.Number(strconv.Itoa(every.Tasks.ReviewRounds)))
	}
	if len(settings.Spaces)+len(settings.Machines) > 0 {
		plan.say("Settings for one space or machine did not import: shp has one config.")
	}
	for _, key := range []string{"coordinator", "worker", "reviewer", "must_fix", "rounds", "max_running"} {
		now, was := quote(cfg.val(key)), quote(before.val(key))
		if now == was {
			plan.say("  %s: %s", key, now)
		} else {
			plan.say("  %s: %s (was %s)", key, now, was)
		}
	}
	plan.say("")
	plan.write(filepath.Join(store(), "config.md"), render(cfg, ""))
	return nil
}
