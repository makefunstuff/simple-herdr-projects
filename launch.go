package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

// holdingStatuses are the statuses whose task takes a running slot.
var holdingStatuses = map[string]bool{
	"working": true,
	"needs":   true,
	"ready":   true,
	"merging": true,
}

// noSlot is a queued task that has to wait for a free slot.
type noSlot string

func (n noSlot) Error() string { return string(n) }

// workerTerminal is the live terminal of a task's worker, if any.
func workerTerminal(id string) (string, error) {
	alive, err := liveTerminals("herdr.role=worker", "herdr.task="+id)
	if err != nil {
		return "", err
	}
	if len(alive) > 1 {
		return "", refuse("%s has %d live workers: %s", id, len(alive), strings.Join(alive, ", "))
	}
	if len(alive) == 0 {
		return "", nil
	}
	return alive[0], nil
}

// liveTerminals are the addresses of the live terminals with all these labels.
func liveTerminals(labels ...string) ([]string, error) {
	argv := []string{"ls"}
	for _, label := range labels {
		argv = append(argv, "--label", label)
	}
	found, err := hcJSON(argv...)
	if err != nil {
		return nil, err
	}
	terminals, _ := found["terminals"].([]any)
	var alive []string
	for _, item := range terminals {
		terminal, _ := item.(map[string]any)
		if address, _ := terminal["address"].(string); terminal["state"] == "live" && address != "" {
			alive = append(alive, address)
		}
	}
	return alive, nil
}

// branchExists reports whether a repo has a local branch.
func branchExists(repo, branch string) bool {
	_, _, err := run("git", "-C", repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// workerPrompt is the first message a worker gets.
func workerPrompt(t task) string {
	return fmt.Sprintf(
		"You are the worker for task %s in space %s.\n"+
			"Work only in %s on branch %s; do not start other agents.\n"+
			"The task file is %s: its header is the state, the Markdown below is the brief.\n"+
			"Report with: shp status %s --progress <0-100> --step \"<what you are doing>\" --left <e.g. 20m>\n"+
			"If you need the person, run: shp ask %s \"<your question>\" and wait for the answer here.\n"+
			"When your work is committed, run: shp review %s. Fix what a round finds, commit and review again.\n"+
			"Once a round leaves nothing to fix, run: shp ready %s --summary \"<what the person can now do>\".\n"+
			"Review refuses past the rounds limit; then ask the person: only a person passes --again.",
		t.id, t.space, expand(t.head.text("worktree")), t.head.text("branch"),
		tilde(t.path), t.id, t.id, t.id, t.id)
}

// commonDir is where a checkout's shared git objects live.
func commonDir(path string) (string, error) {
	text, err := git("-C", path, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	text = strings.TrimSpace(text)
	if !filepath.IsAbs(text) {
		text = filepath.Join(path, text)
	}
	return resolveDir(text)
}

// makeWorktree creates a task's worktree, or checks the one it has.
func makeWorktree(spaceHead header, t task) error {
	repo, err := resolveDir(expand(spaceHead.text("folder")))
	if err != nil {
		return err
	}
	worktree := expand(t.head.text("worktree"))
	branch := t.head.text("branch")
	if exists(worktree) {
		where, err := resolveDir(worktree)
		if err != nil {
			return err
		}
		if repo == where {
			return refuse("%s: its worktree is the repo itself", t.id)
		}
		current, err := git("-C", worktree, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil {
			return err
		}
		if current = strings.TrimSpace(current); current != branch {
			return refuse("%s exists on %s, not %s", tilde(worktree), current, branch)
		}
		here, err := commonDir(worktree)
		if err != nil {
			return err
		}
		there, err := commonDir(repo)
		if err != nil {
			return err
		}
		if here != there {
			return refuse("%s belongs to another repo, not %s", tilde(worktree), tilde(repo))
		}
		return nil
	}
	if branchExists(repo, branch) {
		return refuse("branch %s already exists", branch)
	}
	if err := os.MkdirAll(filepath.Dir(worktree), 0o755); err != nil {
		return wrap(filepath.Dir(worktree), err)
	}
	_, err = git("-C", repo, "worktree", "add", "-b", branch, worktree, spaceHead.text("base"))
	return err
}

// deliver checks a launch task went in, and retries after reading; it never resends blind.
func deliver(terminal, text, id string) error {
	var first map[string]any
	for range 3 {
		reply, err := hcJSON("get", terminal)
		if err != nil {
			return err
		}
		first, _ = reply["first_message"].(map[string]any)
		if sent, _ := first["sent"].(bool); first == nil || sent {
			return nil
		}
		if _, err := hc("read", terminal); err != nil {
			return err
		}
		if _, err := hcJSON("wait", terminal, "--timeout", "1"); err != nil {
			return err
		}
		reply, err = hcJSON("get", terminal)
		if err != nil {
			return err
		}
		if reply["state"] != "live" {
			break
		}
		if _, _, err := run("hc", "send", "--", terminal, text); err == nil {
			return nil
		}
	}
	why := ""
	if reason, _ := first["reason"].(string); reason != "" {
		why = " (" + reason + ")"
	}
	return refuse("%s: the agent's first message was not sent%s", id, why)
}

// start makes the worktree and launches the worker; a queued start also takes a slot.
func start(id string, queued bool) (task, error) {
	t, err := loadTask(id)
	if err != nil {
		return task{}, err
	}
	for _, key := range []string{"title", "branch", "worktree"} {
		if t.head.text(key) == "" {
			return task{}, refuse("%s has no %s", tilde(t.path), key)
		}
	}
	if err := startable(t); err != nil {
		return task{}, err
	}
	live, err := workerTerminal(id)
	if err != nil {
		return task{}, err
	}
	if live != "" {
		return task{}, refuse("%s already has a live worker (%s); say to it, or kill it first", id, live)
	}
	_, spaceHead, _, err := loadSpace(t.space)
	if err != nil {
		return task{}, err
	}
	// An interrupt, as from Ctrl-C, must not leave the task working with no
	// worker: hold it until the launch ends, put the task back, then stop shp.
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupt)
	t, previous, err := claim(id, queued)
	if err != nil {
		return task{}, err
	}
	workspace, err := launch(spaceHead, t)
	interrupted := len(interrupt) > 0
	if interrupted {
		err = refuse("%s: interrupted while starting", id)
	}
	if err != nil {
		if undoErr := release(id, previous, workspace); undoErr != nil {
			fmt.Fprintf(os.Stderr, "shp: %s was not put back to %s: %v\n", id, previous, undoErr)
		}
		if interrupted {
			fmt.Fprintf(os.Stderr, "shp: %v\n", err)
			os.Exit(130) // a stopped tick starts nothing more
		}
		return task{}, err
	}

	unlock, err := lock()
	if err != nil {
		return task{}, err
	}
	defer unlock()
	t, err = loadTask(id)
	if err != nil {
		return task{}, err
	}
	var recorded any // null when herdr-app named no space
	if workspace != "" {
		recorded = workspace
	}
	t.head.set("workspace", recorded)
	if err := writeText(t.path, render(t.head, t.body)); err != nil {
		return task{}, err
	}
	return t, nil
}

// startable refuses a task that is not todo or queued.
func startable(t task) error {
	if status := t.head.text("status"); status != "todo" && status != "queued" {
		return refuse("%s is %s; only todo or queued starts (edit %s)", t.id, status, tilde(t.path))
	}
	return nil
}

// claim marks a task working under the lock, so no other start takes it, and
// returns the status it had. A queued task also needs a free slot.
func claim(id string, queued bool) (task, string, error) {
	unlock, err := lock()
	if err != nil {
		return task{}, "", err
	}
	defer unlock()
	t, err := loadTask(id)
	if err != nil {
		return task{}, "", err
	}
	if err := startable(t); err != nil {
		return task{}, "", err
	}
	if queued {
		cfg, err := config()
		if err != nil {
			return task{}, "", err
		}
		maxRunning, _ := cfg.number("max_running")
		running := 0
		tasks, _ := allTasks()
		for _, held := range tasks {
			if holdingStatuses[held.head.text("status")] {
				running++
			}
		}
		if running >= maxRunning {
			return task{}, "", noSlot(fmt.Sprintf("%s waits: max_running is %d", id, maxRunning))
		}
	}
	previous := t.head.text("status")
	t.head.set("status", "working")
	if err := writeText(t.path, render(t.head, t.body)); err != nil {
		return task{}, "", err
	}
	return t, previous, nil
}

// launch makes the worktree and opens the worker in its own Herdr space,
// returning that space's id; on an error the id is whatever was made so far.
func launch(spaceHead header, t task) (string, error) {
	if err := makeWorktree(spaceHead, t); err != nil {
		return "", err
	}
	argv := t.head.words("agent")
	if len(argv) == 0 {
		cfg, err := config()
		if err != nil {
			return "", err
		}
		argv = cfg.words("worker") // config refuses an empty worker
	}
	prompt := workerPrompt(t)
	reply, err := herdrApp("space.new", map[string]any{
		"name":   t.id + " · " + t.head.text("title"),
		"folder": expand(t.head.text("worktree")),
		"tab":    "Chat",
		"argv":   argv,
		"task":   prompt,
	})
	if err != nil {
		return "", err
	}
	terminal, _ := reply["terminal"].(string)
	workspace, _ := reply["space"].(string)
	if terminal == "" {
		return workspace, refuse("herdr-app made no terminal for %s", t.id)
	}
	if _, err := hc("label", terminal, "herdr.task="+t.id, "herdr.role=worker"); err != nil {
		return workspace, err
	}
	if err := deliver(terminal, prompt, t.id); err != nil {
		return workspace, err
	}
	reply, err = hcJSON("get", terminal)
	if err != nil {
		return workspace, err
	}
	if reply["state"] != "live" {
		return workspace, refuse("%s: the worker exited right after launch", t.id)
	}
	return workspace, nil
}

// release undoes a failed launch: it closes the space and puts the task back,
// unless the worker got far enough to ask something. Closing is best effort:
// the space may already be gone.
func release(id, previous, workspace string) error {
	if workspace != "" {
		_, _ = herdrApp("space.close", map[string]any{"id": workspace, "confirmed": true})
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
	if t.head.text("status") != "working" || len(t.head.list("asks")) > 0 {
		return nil
	}
	t.head.set("status", previous)
	return writeText(t.path, render(t.head, t.body))
}

func startCmd(raw []string) error {
	a, err := parseArgs(raw, nil, nil)
	if err != nil {
		return err
	}
	if len(a.words) != 1 {
		return usage("start takes one task id")
	}
	t, err := start(a.words[0], false)
	if err != nil {
		return err
	}
	fmt.Println(taskLine(t.head))
	return nil
}

// gatesClear says a task's parent and every after task are done.
func gatesClear(t task, byID map[string]header) bool {
	waiting := append([]string(nil), t.head.words("after")...)
	if parent := t.head.text("parent"); parent != "" {
		waiting = append(waiting, parent)
	}
	for _, reference := range waiting {
		if byID[reference].text("status") != "done" {
			return false
		}
	}
	return true
}

func tickCmd(raw []string) error {
	if len(raw) > 0 {
		return usage("tick takes no arguments")
	}
	if _, err := config(); err != nil { // a bad config names its file before tick does anything
		return err
	}
	tasks, problems := allTasks()
	for _, problem := range problems {
		fmt.Fprintf(os.Stderr, "shp: %v\n", problem)
	}
	problems = append(problems, followMerges(tasks)...)
	tasks, _ = allTasks() // a merge may have cleared a gate
	byID := map[string]header{}
	for _, t := range tasks {
		byID[t.id] = t.head
	}
	var started []string
	for _, t := range tasks {
		if t.head.text("status") != "queued" || !gatesClear(t, byID) {
			continue
		}
		if _, err := start(t.id, true); err != nil {
			if errors.As(err, new(noSlot)) {
				break
			}
			if current, readErr := loadTask(t.id); readErr == nil &&
				current.head.text("status") == "queued" {
				fmt.Fprintf(os.Stderr, "shp: %v\n", err)
				problems = append(problems, err)
			}
			continue
		}
		started = append(started, t.id)
	}
	for _, id := range started {
		fmt.Println(id)
	}
	if len(problems) > 0 {
		return exitCode(1)
	}
	return nil
}
