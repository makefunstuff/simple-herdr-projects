package main

// These tests run the built shp against a throwaway HOME, real git repos and a
// fake hc and herdr-app. The test binary is also those fakes: the scripts on
// PATH call it back with GO_FAKE_TOOL set.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var shpBin string

func TestMain(m *testing.M) {
	if os.Getenv("GO_FAKE_TOOL") == "1" {
		os.Exit(fakeTool(os.Args[1:]))
	}
	dir, err := os.MkdirTemp("", "shp-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	shpBin = filepath.Join(dir, "shp")
	build := exec.Command("go", "build", "-o", shpBin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build:", err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// fakeTool is the fake hc, herdr-app or gh, chosen by the first argument.
func fakeTool(args []string) int {
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}
	if len(args) < 2 {
		return 2
	}
	name, rest := args[0], args[1:]
	state := os.Getenv("FAKE_STATE")
	logName := name + ".log"
	if name == "herdr-app" {
		logName = "app.log"
	}
	logCall(filepath.Join(state, logName), rest)
	logCall(filepath.Join(state, "all.log"), args) // every tool's calls, in order
	switch name {
	case "herdr-app":
		return fakeApp(state, rest)
	case "gh":
		return fakeGH(state, rest)
	}
	return fakeHC(state, rest)
}

func logCall(path string, args []string) {
	line, _ := json.Marshal(args)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	fmt.Fprintln(file, string(line))
}

func fact(state, name string) any {
	data, err := os.ReadFile(filepath.Join(state, name))
	if err != nil {
		return nil
	}
	var value any
	_ = json.Unmarshal(data, &value)
	return value
}

func fakeHC(state string, args []string) int {
	switch args[0] {
	case "get":
		reply := fact(state, "get.json")
		if reply == nil {
			reply = map[string]any{"state": "live", "first_message": map[string]any{"sent": true}}
		}
		printJSON(reply)
	case "read":
		fmt.Println("screen")
	case "wait":
		writeReport(state)
		printJSON(map[string]any{"ended": false, "reason": "timeout", "turn": 0, "children": []any{}})
	case "ls":
		wanted := map[string]string{}
		for i, arg := range args {
			if arg == "--label" && i+1 < len(args) {
				key, value, _ := strings.Cut(args[i+1], "=")
				wanted[key] = value
			}
		}
		terminals := []any{}
		if listed := fact(state, "ls.json"); listed != nil {
			all, _ := listed.(map[string]any)["terminals"].([]any)
			for _, item := range all {
				terminal, _ := item.(map[string]any)
				if matchesLabels(terminal, wanted) {
					terminals = append(terminals, terminal)
				}
			}
		}
		printJSON(map[string]any{"terminals": terminals})
	case "send":
		if exists(filepath.Join(state, "send_fails")) {
			fmt.Fprintln(os.Stderr, `{"error": {"kind": "blocked", "message": "blocked"}}`)
			return 1
		}
		fmt.Println("{}")
	default:
		fmt.Println("{}")
	}
	return 0
}

func matchesLabels(terminal map[string]any, wanted map[string]string) bool {
	labels, _ := terminal["labels"].(map[string]any)
	for key, value := range wanted {
		if got, _ := labels[key].(string); got != value {
			return false
		}
	}
	return true
}

func fakeApp(state string, args []string) int {
	if exists(filepath.Join(state, "app_fails")) {
		fmt.Println(`{"error": "no window"}`)
		return 1
	}
	if args[0] == "ls" {
		listed := fact(state, "spaces.json")
		if listed == nil {
			listed = map[string]any{"spaces": []any{}}
		}
		printJSON(listed)
		return 0
	}
	if len(args) < 3 || args[0] != "do" {
		fmt.Println("{}")
		return 0
	}
	switch {
	case args[1] == "tab.new" && strings.Contains(args[2], `"name":"Assistant"`):
		fmt.Println(`{"space": "sp-repo", "tab": "tb-assistant", "terminal": "t20"}`)
	case args[1] == "tab.new" && strings.Contains(args[2], `"name":"Review"`):
		return fakeReviewer(state, args[2])
	case args[1] == "tab.new" && exists(filepath.Join(state, "tabs_fail")):
		fmt.Println(`{"error": "no such space"}`)
		return 1
	case args[1] == "space.new" && strings.Contains(args[2], `"argv"`):
		if exists(filepath.Join(state, "app_slow")) {
			time.Sleep(2 * time.Second)
		}
		fmt.Println(`{"space": "sp-test", "tab": "tb-test", "terminal": "t9"}`)
	case args[1] == "space.new":
		fmt.Println(`{"space": "sp-repo", "tab": "tb-repo", "terminal": "t1"}`)
	case args[1] == "space.close":
		fakeClose(state, args[2])
		fmt.Println("{}")
	default:
		fmt.Println("{}")
	}
	return 0
}

// fakeClose notes a space closed while its folder was still there.
func fakeClose(state, payload string) {
	var closing struct{ ID string }
	_ = json.Unmarshal([]byte(payload), &closing)
	listed, _ := fact(state, "spaces.json").(map[string]any)
	spaces, _ := listed["spaces"].([]any)
	for _, item := range spaces {
		space, _ := item.(map[string]any)
		if folder, _ := space["folder"].(string); space["id"] == closing.ID && exists(folder) {
			_ = os.WriteFile(filepath.Join(state, "closed_on_its_folder"), nil, 0o644)
		}
	}
}

// fixture is a throwaway HOME, store, git repo and PATH for one test.
type fixture struct {
	t     *testing.T
	root  string
	home  string
	state string
	bin   string
	store string
	repo  string
	env   []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{t: t, root: root}
	f.home = filepath.Join(root, "home")
	f.state = filepath.Join(root, "state")
	f.bin = filepath.Join(root, "bin")
	for _, dir := range []string{f.home, f.state, f.bin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hc", "herdr-app", "gh"} {
		script := fmt.Sprintf("#!/bin/sh\nexec '%s' -- %s \"$@\"\n", exe, name)
		if err := os.WriteFile(filepath.Join(f.bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.store = filepath.Join(f.home, ".herdr", "simple")
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GIT_") {
			continue
		}
		f.env = append(f.env, entry)
	}
	f.env = append(f.env,
		"HOME="+f.home,
		"PATH="+f.bin+":"+os.Getenv("PATH"),
		"FAKE_STATE="+f.state,
		"GO_FAKE_TOOL=1",
		"TMPDIR="+f.state,
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	f.repo = f.makeRepo("app")
	return f
}

func (f *fixture) makeRepo(name string) string {
	f.t.Helper()
	repo := filepath.Join(f.root, name)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		f.t.Fatal(err)
	}
	f.gitAt(repo, "init", "-q")
	f.gitAt(repo, "symbolic-ref", "HEAD", "refs/heads/main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("start\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.gitAt(repo, "add", ".")
	f.gitAt(repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", "start")
	return repo
}

func (f *fixture) gitAt(dir string, args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (f *fixture) commonDir(path string) string {
	f.t.Helper()
	text := strings.TrimSpace(f.gitAt(path, "rev-parse", "--git-common-dir"))
	if !filepath.IsAbs(text) {
		text = filepath.Join(path, text)
	}
	real, err := filepath.EvalSymlinks(text)
	if err != nil {
		f.t.Fatal(err)
	}
	return real
}

type result struct {
	stdout string
	stderr string
	code   int
}

func (f *fixture) shp(stdin string, args ...string) result {
	f.t.Helper()
	cmd := exec.Command(shpBin, args...)
	cmd.Env = f.env
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		f.t.Fatalf("shp %v: %v", args, err)
	}
	return result{stdout.String(), stderr.String(), code}
}

func (f *fixture) ok(args ...string) string {
	f.t.Helper()
	return f.okIn("", args...)
}

func (f *fixture) okIn(stdin string, args ...string) string {
	f.t.Helper()
	got := f.shp(stdin, args...)
	if got.code != 0 {
		f.t.Fatalf("shp %v exited %d: %s", args, got.code, got.stderr)
	}
	return got.stdout
}

func (f *fixture) refused(args ...string) string {
	f.t.Helper()
	got := f.shp("", args...)
	if got.code != 1 {
		f.t.Fatalf("shp %v exited %d, not 1: %s%s", args, got.code, got.stdout, got.stderr)
	}
	return got.stderr
}

func (f *fixture) makeSpace(name string, args ...string) {
	f.t.Helper()
	f.ok(append([]string{"space", "add", name, "--folder", f.repo}, args...)...)
}

func (f *fixture) addTask(title string, args ...string) string {
	f.t.Helper()
	return strings.TrimSpace(f.ok(append([]string{"add", title, "--space", "work"}, args...)...))
}

func (f *fixture) taskPath(id string) string {
	return filepath.Join(f.store, "spaces", "work", id+".md")
}

func (f *fixture) header(id string) map[string]any {
	f.t.Helper()
	return f.headerIn("work", id)
}

func (f *fixture) setStatus(id, status string) {
	f.t.Helper()
	path := f.taskPath(id)
	data, err := os.ReadFile(path)
	if err != nil {
		f.t.Fatal(err)
	}
	old := fmt.Sprintf("\nstatus: %v\n", f.header(id)["status"])
	text := string(data)
	if !strings.Contains(text, old) {
		f.t.Fatalf("%s does not hold %q", path, old)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(text, old, "\nstatus: "+status+"\n", 1)), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) writeTask(id, text string) {
	f.t.Helper()
	path := f.taskPath(id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) calls(name string) [][]string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.state, name))
	if err != nil {
		return nil
	}
	var calls [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var call []string
		if json.Unmarshal([]byte(line), &call) == nil {
			calls = append(calls, call)
		}
	}
	return calls
}

func (f *fixture) fact(name string, value any) {
	f.t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.state, name), data, 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) writeConfig(text string) {
	f.t.Helper()
	if err := os.MkdirAll(f.store, 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.store, "config.md"), []byte(text), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) worker(id string) {
	f.fact("ls.json", map[string]any{"terminals": []any{map[string]any{
		"address": "t9", "state": "live", "created_ms": 1,
		"labels": map[string]any{"herdr.role": "worker", "herdr.task": id},
	}}})
}

func TestSpaceStart(t *testing.T) {
	t.Run("starts an Assistant in the space's Herdr space", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		out := f.ok("space", "start", "work")
		wantIn(t, out, "its Assistant is in t20")
		var payload map[string]any
		for _, call := range f.calls("app.log") {
			if len(call) == 3 && call[1] == "tab.new" && strings.Contains(call[2], `"name":"Assistant"`) {
				_ = json.Unmarshal([]byte(call[2]), &payload)
			}
		}
		wantField(t, payload, "space", "sp-repo")
		wantField(t, payload, "folder", f.repo)
		wantField(t, payload, "argv", []any{"pi"})
		prompt := fmt.Sprint(payload["task"])
		for _, want := range []string{"You are the Assistant for the space work", "shp add", "shp start", "shp list"} {
			wantIn(t, prompt, want)
		}
		if !hasCall(f.calls("hc.log"), "label", "t20", "herdr.role=coordinator") {
			t.Errorf("the Assistant was not labelled: %v", f.calls("hc.log"))
		}
	})

	t.Run("leaves a live Assistant alone", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		f.fact("ls.json", map[string]any{"terminals": []any{map[string]any{
			"address": "t21", "state": "live",
			"labels": map[string]any{"herdr.role": "coordinator", "herdr.space": "sp-repo"},
		}}})
		out := f.ok("space", "start", "work")
		wantIn(t, out, "already live in t21")
		for _, call := range f.calls("app.log") {
			if len(call) == 3 && call[1] == "tab.new" && strings.Contains(call[2], `"name":"Assistant"`) {
				t.Errorf("a second Assistant was started: %v", call)
			}
		}
	})

	t.Run("a folder space cannot start a task", func(t *testing.T) {
		f := newFixture(t)
		f.shepherdr(map[string]any{"home": f.homeProject()}, roleSettings)
		f.ok("import")
		id := strings.TrimSpace(f.ok("add", "a folder task", "--space", "home"))
		got := f.refused("start", id)
		wantIn(t, got, "space home is a folder, not a git repo: it has no base")
	})

	t.Run("every space, including one that is a folder", func(t *testing.T) {
		f := newFixture(t)
		app, _ := f.appProject()
		f.shepherdr(map[string]any{"app": app, "home": f.homeProject()}, roleSettings)
		f.ok("import")
		f.fact("spaces.json", map[string]any{"spaces": []any{map[string]any{
			"id": "sp-home", "folder": f.home, "machine": nil,
		}}})
		out := f.ok("space", "start")
		wantIn(t, out, "space home: its Assistant is in t20")
		wantIn(t, out, "space my-app: its Assistant is in t20")
	})
}

func TestSpace(t *testing.T) {
	t.Run("file holds the repo and a derived prefix", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work", "--checks", `[["make", "check"]]`)
		data, err := os.ReadFile(filepath.Join(f.store, "spaces", "work.md"))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, want := range []string{"prefix: WORK", "base: main", `checks: [["make", "check"]]`, f.repo, "next: 1"} {
			if !strings.Contains(text, want) {
				t.Errorf("space file lacks %q:\n%s", want, text)
			}
		}
	})

	t.Run("a taken prefix gets a digit", func(t *testing.T) {
		f := newFixture(t)
		other := f.makeRepo("api")
		f.makeSpace("my-app")
		f.ok("space", "add", "my-api", "--folder", other)
		for name, want := range map[string]string{"my-app": "prefix: MA\n", "my-api": "prefix: MA2\n"} {
			data, err := os.ReadFile(filepath.Join(f.store, "spaces", name+".md"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), want) {
				t.Errorf("%s: want %q in\n%s", name, want, data)
			}
		}
	})

	t.Run("must be a git repo root with its base branch", func(t *testing.T) {
		f := newFixture(t)
		plain := filepath.Join(f.root, "plain")
		if err := os.MkdirAll(plain, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := f.refused("space", "add", "x", "--folder", plain); !strings.Contains(got, "not a git repo") {
			t.Errorf("plain dir: %q", got)
		}
		if got := f.refused("space", "add", "x", "--folder", filepath.Join(f.repo, "README.md")); !strings.Contains(got, "not a git repo") {
			t.Errorf("file: %q", got)
		}
		if got := f.refused("space", "add", "x", "--folder", f.repo, "--base", "nope"); !strings.Contains(got, "git") {
			t.Errorf("bad base: %q", got)
		}
	})

	t.Run("an explicit prefix cannot be reused", func(t *testing.T) {
		f := newFixture(t)
		other := f.makeRepo("api")
		f.makeSpace("work")
		got := f.refused("space", "add", "other", "--folder", other, "--prefix", "WORK")
		if !strings.Contains(got, "prefix WORK is used by another space") {
			t.Errorf("%q", got)
		}
	})

	t.Run("config prints the defaults before any store exists", func(t *testing.T) {
		f := newFixture(t)
		if got := f.ok("config"); !strings.Contains(got, "max_running: 4") {
			t.Errorf("%q", got)
		}
	})
}

func TestHelp(t *testing.T) {
	f := newFixture(t)
	commands := []string{"config", "space", "add", "list", "show", "start", "tick", "status", "ask", "say",
		"review", "reviewed", "ready", "accept", "reopen", "done"}
	help := f.ok("--help")
	for _, command := range commands {
		if !strings.Contains(help, "\n    "+command+" ") {
			t.Errorf("main help lacks %s: %q", command, help)
		}
		if got := f.ok(command, "--help"); !strings.Contains(got, "usage: shp "+command) {
			t.Errorf("%s --help: %q", command, got)
		}
	}
	if got := f.ok("space", "add", "--help"); !strings.Contains(got, "usage: shp space add") {
		t.Errorf("space add --help: %q", got)
	}
	if got := f.shp(""); got.code != 2 || !strings.Contains(got.stderr, "usage: shp") {
		t.Errorf("no command: exit %d, %q", got.code, got.stderr)
	}
	if got := f.refused("ask", "WORK-1", "--", "--help"); !strings.Contains(got, "no task WORK-1") {
		t.Errorf("-- --help: %q", got)
	}
}

func TestAdd(t *testing.T) {
	t.Run("ids are never reused", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		if id := f.addTask("First"); id != "WORK-1" {
			t.Fatalf("first id %q", id)
		}
		if id := f.addTask("Second"); id != "WORK-2" {
			t.Fatalf("second id %q", id)
		}
		if err := os.Remove(f.taskPath("WORK-1")); err != nil {
			t.Fatal(err)
		}
		if id := f.addTask("Third"); id != "WORK-3" {
			t.Fatalf("third id %q", id)
		}
	})

	t.Run("a task is a file with its branch, worktree and brief", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id := strings.TrimSpace(f.okIn("Put a box on top.\n", "add", "Add a search box", "--space", "work", "--brief", "-"))
		head := f.header(id)
		if head["status"] != "todo" {
			t.Errorf("status %v", head["status"])
		}
		if head["branch"] != "task/1-add-a-search-box" {
			t.Errorf("branch %v", head["branch"])
		}
		if head["worktree"] != "~/.herdr/worktrees/work/task-1-add-a-search-box" {
			t.Errorf("worktree %v", head["worktree"])
		}
		if asks, _ := head["asks"].([]any); len(asks) != 0 {
			t.Errorf("asks %v", head["asks"])
		}
		if shown := f.ok("show", id); !strings.Contains(shown, "Put a box on top.") {
			t.Errorf("show: %q", shown)
		}
		var listed []map[string]any
		if err := json.Unmarshal([]byte(f.ok("list", "--json")), &listed); err != nil {
			t.Fatal(err)
		}
		if len(listed) != 1 || listed[0]["id"] != id {
			t.Errorf("list: %v", listed)
		}
	})

	t.Run("ids skip a file that is already there", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		if id := f.addTask("First"); id != "WORK-1" {
			t.Fatalf("first id %q", id)
		}
		path := filepath.Join(f.store, "spaces", "work.md")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.Replace(string(data), "next: 2", "next: 1", 1)), 0o644); err != nil {
			t.Fatal(err)
		}
		if id := f.addTask("Second"); id != "WORK-2" {
			t.Fatalf("second id %q", id)
		}
	})

	t.Run("an invalid agent is refused", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		if got := f.refused("add", "X", "--space", "work", "--agent", "claude"); !strings.Contains(got, "JSON like") {
			t.Errorf("%q", got)
		}
	})

	t.Run("adding a task keeps the space brief", func(t *testing.T) {
		f := newFixture(t)
		f.okIn("Space brief must survive.\n", "space", "add", "work", "--folder", f.repo, "--brief", "-")
		spaceFile := filepath.Join(f.store, "spaces", "work.md")
		spaceBody := func() string {
			data, err := os.ReadFile(spaceFile)
			if err != nil {
				t.Fatal(err)
			}
			_, body, _ := strings.Cut(string(data), "\n---\n")
			return body
		}
		before := spaceBody()
		f.okIn("Task brief.\n", "add", "With a brief", "--space", "work", "--brief", "-")
		f.addTask("Without a brief")
		if after := spaceBody(); after != before {
			t.Errorf("space brief changed from %q to %q", before, after)
		}
	})

	t.Run("a task may wait for a parent and after", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		parent := f.addTask("Parent")
		sibling := f.addTask("Sibling")
		child := f.addTask("Child", "--status", "queued", "--parent", parent, "--after", sibling)
		head := f.header(child)
		if head["parent"] != parent {
			t.Errorf("parent %v", head["parent"])
		}
		if after, _ := head["after"].([]any); len(after) != 1 || after[0] != sibling {
			t.Errorf("after %v", head["after"])
		}
		if got := f.refused("add", "X", "--space", "work", "--parent", "WORK-9"); !strings.Contains(got, "no task WORK-9") {
			t.Errorf("%q", got)
		}
	})
}

func TestList(t *testing.T) {
	t.Run("show --json keeps the file's key order", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id := f.addTask("Order")
		got := f.ok("show", id, "--json")
		var keys []string
		for _, line := range strings.Split(got, "\n") {
			if key, _, ok := strings.Cut(strings.TrimSpace(line), `": `); ok && strings.HasPrefix(line, `  "`) {
				keys = append(keys, strings.TrimPrefix(key, `"`))
			}
		}
		want := "id title status agent branch worktree asks"
		if strings.Join(keys, " ") != want {
			t.Errorf("keys %v, want %s:\n%s", keys, want, got)
		}
	})

	t.Run("todo, queued, working and needs show", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		todo := f.addTask("A todo task")
		queued := f.addTask("A queued task", "--status", "queued")
		working := f.addTask("A working task")
		waiting := f.addTask("A asking task")
		f.ok("start", working)
		f.ok("start", waiting)
		f.ok("ask", waiting, "Which colour?")
		lines := map[string]string{}
		for _, text := range strings.Split(f.ok("list"), "\n") {
			if fields := strings.Fields(text); len(fields) > 0 {
				lines[fields[0]] = text
			}
		}
		for id, want := range map[string]string{
			todo: " todo ", queued: " queued ", working: " working ", waiting: " needs ",
		} {
			if !strings.Contains(lines[id], want) {
				t.Errorf("%s: %q lacks %q", id, lines[id], want)
			}
		}
		if !strings.Contains(lines[waiting], "Which colour?") {
			t.Errorf("ask missing: %q", lines[waiting])
		}
	})

	t.Run("a bad file names its path and the rest still list", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		good := f.addTask("Good")
		f.writeTask("WORK-9", "no header here\n")
		got := f.shp("", "list")
		if got.code != 1 {
			t.Fatalf("exited %d", got.code)
		}
		if !strings.Contains(got.stderr, "WORK-9.md") {
			t.Errorf("stderr %q", got.stderr)
		}
		if !strings.Contains(got.stdout, good) {
			t.Errorf("stdout %q", got.stdout)
		}
	})

	t.Run("unknown keys and statuses survive reads", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		f.writeTask("WORK-1", `---
id: WORK-1
title: Odd
status: parked
custom: 7
branch: task/1-odd
worktree: ~/.herdr/worktrees/app/task-1-odd
---
Brief.
`)
		if got := f.ok("list"); !strings.Contains(got, "parked") {
			t.Errorf("list %q", got)
		}
		if got := f.refused("start", "WORK-1"); !strings.Contains(got, "parked") {
			t.Errorf("start %q", got)
		}
		f.ok("status", "WORK-1", "--progress", "10")
		data, err := os.ReadFile(f.taskPath("WORK-1"))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"status: parked", "custom: 7"} {
			if !strings.Contains(string(data), want) {
				t.Errorf("file lacks %q:\n%s", want, data)
			}
		}
	})

	t.Run("a task without a branch names its file", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		f.writeTask("WORK-1", `---
id: WORK-1
title: Broken
worktree: ~/.herdr/worktrees/app/task-1-broken
---
`)
		got := f.refused("start", "WORK-1")
		for _, want := range []string{"WORK-1.md", "branch"} {
			if !strings.Contains(got, want) {
				t.Errorf("%q lacks %q", got, want)
			}
		}
	})

	t.Run("an ask without its words names its file", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		f.writeTask("WORK-1", `---
id: WORK-1
title: Broken
status: needs
asks: [{"text": "Which colour?"}]
---
`)
		got := f.refused("list")
		for _, want := range []string{"WORK-1.md", "asks is a list of id, text and time"} {
			if !strings.Contains(got, want) {
				t.Errorf("%q lacks %q", got, want)
			}
		}
	})

	t.Run("a malformed asks names its file", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		f.writeTask("WORK-1", `---
id: WORK-1
title: Broken
status: todo
asks: 5
---
`)
		got := f.refused("list")
		for _, want := range []string{"WORK-1.md", "asks"} {
			if !strings.Contains(got, want) {
				t.Errorf("%q lacks %q", got, want)
			}
		}
	})

	t.Run("a numeric id names its file", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		f.writeTask("WORK-1", `---
id: 7
title: Broken
status: todo
---
`)
		got := f.refused("list")
		for _, want := range []string{"WORK-1.md", "says id 7, not WORK-1"} {
			if !strings.Contains(got, want) {
				t.Errorf("%q lacks %q", got, want)
			}
		}
	})

	t.Run("a number worktree names its file", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		f.writeTask("WORK-1", `---
id: WORK-1
title: Broken
status: todo
branch: task/1-broken
worktree: 7
---
`)
		got := f.refused("start", "WORK-1")
		for _, want := range []string{"WORK-1.md", "worktree"} {
			if !strings.Contains(got, want) {
				t.Errorf("%q lacks %q", got, want)
			}
		}
	})

	t.Run("a failed space.new leaves the task queued", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id := f.addTask("No window", "--status", "queued")
		f.fact("app_fails", true)
		if got := f.refused("start", id); !strings.Contains(got, "no window") {
			t.Errorf("%q", got)
		}
		if status := f.header(id)["status"]; status != "queued" {
			t.Errorf("status %v", status)
		}
	})

	t.Run("a bad config names its file", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		f.writeConfig("---\nmax_running: plenty\n---\n")
		got := f.refused("tick")
		for _, want := range []string{"config.md", "max_running"} {
			if !strings.Contains(got, want) {
				t.Errorf("%q lacks %q", got, want)
			}
		}
	})

	t.Run("a file missing status is todo and runs", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		f.writeTask("WORK-7", `---
id: WORK-7
title: No status
branch: task/7-no-status
worktree: ~/.herdr/worktrees/app/task-7-no-status
---
Do the thing.
`)
		if got := f.ok("list"); !strings.Contains(got, "WORK-7  todo  No status") {
			t.Errorf("list %q", got)
		}
		f.ok("start", "WORK-7")
		if status := f.header("WORK-7")["status"]; status != "working" {
			t.Errorf("status %v", status)
		}
		if !exists(filepath.Join(f.home, ".herdr", "worktrees", "app", "task-7-no-status")) {
			t.Error("worktree missing")
		}
	})
}

func TestWorker(t *testing.T) {
	t.Run("a worktree on the repo itself is refused", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		f.writeTask("WORK-1", fmt.Sprintf(`---
id: WORK-1
title: Main checkout
branch: main
worktree: %s
---
`, f.repo))
		if got := f.refused("start", "WORK-1"); !strings.Contains(got, "worktree is the repo itself") {
			t.Errorf("%q", got)
		}
	})

	t.Run("a worktree from another repo is refused", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		other := f.makeRepo("other")
		elsewhere := filepath.Join(f.root, "shared-wt")
		f.gitAt(other, "worktree", "add", "-b", "task/1-other", elsewhere)
		f.writeTask("WORK-1", fmt.Sprintf(`---
id: WORK-1
title: Elsewhere
branch: task/1-other
worktree: %s
---
`, elsewhere))
		if got := f.refused("start", "WORK-1"); !strings.Contains(got, "belongs to another repo") {
			t.Errorf("%q", got)
		}
	})

	t.Run("a worker that exits right after launch leaves the task queued", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id := f.addTask("Dies at once", "--status", "queued")
		f.fact("get.json", map[string]any{"state": "finished", "first_message": map[string]any{"sent": true}})
		if got := f.refused("start", id); !strings.Contains(got, "exited right after launch") {
			t.Errorf("%q", got)
		}
		if status := f.header(id)["status"]; status != "queued" {
			t.Errorf("status %v", status)
		}
		if _, ok := f.header(id)["workspace"]; ok {
			t.Error("workspace was kept")
		}
		if !hasCall(f.calls("app.log"), "do", "space.close", `{"confirmed":true,"id":"sp-test"}`) {
			t.Errorf("no space.close in %v", f.calls("app.log"))
		}
	})

	t.Run("two spaces with the same repo basename do not share a worktree", func(t *testing.T) {
		f := newFixture(t)
		one := f.makeRepo("one/app")
		two := f.makeRepo("two/app")
		f.ok("space", "add", "one", "--folder", one)
		f.ok("space", "add", "two", "--folder", two)
		first := strings.TrimSpace(f.ok("add", "Same title", "--space", "one"))
		second := strings.TrimSpace(f.ok("add", "Same title", "--space", "two"))
		f.ok("start", first)
		f.ok("start", second)
		firstTree := filepath.Join(f.home, ".herdr", "worktrees", "one", "task-1-same-title")
		secondTree := filepath.Join(f.home, ".herdr", "worktrees", "two", "task-1-same-title")
		if got := f.headerIn("one", first)["worktree"]; got != "~/.herdr/worktrees/one/task-1-same-title" {
			t.Errorf("first worktree %v", got)
		}
		if got := f.headerIn("two", second)["worktree"]; got != "~/.herdr/worktrees/two/task-1-same-title" {
			t.Errorf("second worktree %v", got)
		}
		if got, want := f.commonDir(firstTree), f.commonDir(one); got != want {
			t.Errorf("first common dir %s, want %s", got, want)
		}
		if got, want := f.commonDir(secondTree), f.commonDir(two); got != want {
			t.Errorf("second common dir %s, want %s", got, want)
		}
	})

	t.Run("two ticks do not start the same task twice", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		f.writeConfig("---\nworker: [\"pi\"]\nmax_running: 1\n---\n")
		id := f.addTask("Once", "--status", "queued")
		f.fact("app_slow", true)
		first := f.startBackground("tick")
		time.Sleep(400 * time.Millisecond)
		second := f.shp("", "tick")
		firstResult := f.waitBackground(first)
		if firstResult.code != 0 {
			t.Fatalf("first tick: %s", firstResult.stderr)
		}
		if second.code != 0 {
			t.Fatalf("second tick: %s", second.stderr)
		}
		if launches := countLaunches(f.calls("app.log")); launches != 1 {
			t.Errorf("launches %d", launches)
		}
		if status := f.header(id)["status"]; status != "working" {
			t.Errorf("status %v", status)
		}
	})

	t.Run("two overlapping ticks stay within max_running", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		f.writeConfig("---\nworker: [\"pi\"]\nmax_running: 2\n---\n")
		var tasks []string
		for number := 0; number < 3; number++ {
			tasks = append(tasks, f.addTask(fmt.Sprintf("Queued %d", number), "--status", "queued"))
		}
		f.fact("app_slow", true)
		first := f.startBackground("tick")
		time.Sleep(400 * time.Millisecond)
		second := f.shp("", "tick")
		firstResult := f.waitBackground(first)
		if firstResult.code != 0 {
			t.Fatalf("first tick: %s", firstResult.stderr)
		}
		if second.code != 0 {
			t.Fatalf("second tick: %s", second.stderr)
		}
		if launches := countLaunches(f.calls("app.log")); launches != 2 {
			t.Errorf("launches %d", launches)
		}
		statuses := map[any]int{}
		for _, id := range tasks {
			statuses[f.header(id)["status"]]++
		}
		if statuses["working"] != 2 || statuses["queued"] != 1 {
			t.Errorf("statuses %v", statuses)
		}
	})

	t.Run("an interrupt during a launch puts the task back", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id := f.addTask("Interrupted")
		f.fact("app_slow", true)
		starting := f.startBackground("start", id)
		for range 60 {
			if f.header(id)["status"] == "working" {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err := starting.cmd.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		got := f.waitBackground(starting)
		if got.code != 130 || !strings.Contains(got.stderr, "interrupted while starting") {
			t.Errorf("exit %d, %q", got.code, got.stderr)
		}
		if status := f.header(id)["status"]; status != "todo" {
			t.Errorf("status %v", status)
		}
		if !hasCall(f.calls("app.log"), "do", "space.close", `{"confirmed":true,"id":"sp-test"}`) {
			t.Errorf("no space.close in %v", f.calls("app.log"))
		}
	})

	t.Run("an interrupt stops tick before it starts the next task", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		first := f.addTask("First", "--status", "queued")
		second := f.addTask("Second", "--status", "queued")
		f.fact("app_slow", true)
		ticking := f.startBackground("tick")
		for range 60 {
			if f.header(first)["status"] == "working" {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err := ticking.cmd.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		if got := f.waitBackground(ticking); got.code != 130 {
			t.Errorf("exit %d, %q", got.code, got.stderr)
		}
		if launches := countLaunches(f.calls("app.log")); launches != 1 {
			t.Errorf("launches %d", launches)
		}
		for _, id := range []string{first, second} {
			if status := f.header(id)["status"]; status != "queued" {
				t.Errorf("%s status %v", id, status)
			}
		}
	})

	t.Run("an ask during a launch stays open", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id := f.addTask("Slow start")
		f.fact("app_slow", true)
		starting := f.startBackground("start", id)
		for range 60 {
			if f.header(id)["status"] == "working" {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if got := f.ok("ask", id, "Which colour?"); !strings.Contains(got, "needs you") {
			t.Errorf("ask %q", got)
		}
		started := f.waitBackground(starting)
		if started.code != 0 {
			t.Fatalf("start: %s", started.stderr)
		}
		head := f.header(id)
		if head["status"] != "needs" {
			t.Errorf("status %v", head["status"])
		}
		if asks, _ := head["asks"].([]any); len(asks) != 1 {
			t.Errorf("asks %v", head["asks"])
		}
		if head["workspace"] != "sp-test" {
			t.Errorf("workspace %v", head["workspace"])
		}
	})

	t.Run("a worker is launched labelled and reports", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id := f.addTask("Add a search box", "--agent", `["claude"]`)
		f.ok("start", id)
		args := f.launch()
		if args["name"] != id+" · Add a search box" {
			t.Errorf("name %v", args["name"])
		}
		if args["tab"] != "Chat" {
			t.Errorf("tab %v", args["tab"])
		}
		if argv, _ := args["argv"].([]any); len(argv) != 1 || argv[0] != "claude" {
			t.Errorf("argv %v", args["argv"])
		}
		wantFolder := filepath.Join(f.home, ".herdr", "worktrees", "work", "task-1-add-a-search-box")
		if args["folder"] != wantFolder {
			t.Errorf("folder %v, want %s", args["folder"], wantFolder)
		}
		taskFile := strings.Replace(f.taskPath(id), f.home, "~", 1)
		if task, _ := args["task"].(string); !strings.Contains(task, taskFile) {
			t.Errorf("task %q lacks %q", task, taskFile)
		}
		for _, call := range f.calls("hc.log") {
			if call[0] == "send" {
				t.Errorf("unexpected send %v", call)
			}
		}
		if !hasCall(f.calls("hc.log"), "label", "t9", "herdr.task="+id, "herdr.role=worker") {
			t.Errorf("no label in %v", f.calls("hc.log"))
		}
		head := f.header(id)
		if head["status"] != "working" {
			t.Errorf("status %v", head["status"])
		}
		if head["workspace"] != "sp-test" {
			t.Errorf("workspace %v", head["workspace"])
		}
		worktree := wantFolder
		if branch := strings.TrimSpace(f.gitAt(worktree, "rev-parse", "--abbrev-ref", "HEAD")); branch != "task/1-add-a-search-box" {
			t.Errorf("branch %q", branch)
		}
		f.ok("status", id, "--progress", "40", "--step", "Writing the box", "--left", "1h")
		listed := f.ok("list")
		for _, want := range []string{"40% Writing the box", "1h left"} {
			if !strings.Contains(listed, want) {
				t.Errorf("list %q lacks %q", listed, want)
			}
		}
	})

	t.Run("a first message that did not go in is read then retried", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id := f.addTask("Add a search box")
		f.fact("get.json", map[string]any{"state": "live", "first_message": map[string]any{"sent": false, "reason": "timeout"}})
		f.ok("start", id)
		calls := f.calls("hc.log")
		var sendIndex = -1
		for i, call := range calls {
			if call[0] == "send" {
				if sendIndex >= 0 {
					t.Fatalf("more than one send")
				}
				sendIndex = i
				if call[1] != "--" || call[2] != "t9" {
					t.Errorf("send %v", call)
				}
				taskFile := strings.Replace(f.taskPath(id), f.home, "~", 1)
				if !strings.Contains(call[3], taskFile) {
					t.Errorf("send text %q lacks %q", call[3], taskFile)
				}
			}
		}
		if sendIndex < 0 {
			t.Fatalf("no send in %v", calls)
		}
		if !hasCall(calls[:sendIndex], "read") {
			t.Error("no read before the send")
		}
		if !hasCall(calls[:sendIndex], "get") {
			t.Error("no get before the send")
		}
		if status := f.header(id)["status"]; status != "working" {
			t.Errorf("status %v", status)
		}
	})

	t.Run("a null first message is not resent", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id := f.addTask("Add a search box")
		f.fact("get.json", map[string]any{"state": "live", "first_message": nil})
		f.ok("start", id)
		for _, call := range f.calls("hc.log") {
			if call[0] == "send" {
				t.Errorf("unexpected send %v", call)
			}
		}
	})

	t.Run("a worker that never takes its task leaves the task queued", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id := f.addTask("Add a search box", "--status", "queued")
		f.fact("get.json", map[string]any{"state": "live", "first_message": map[string]any{"sent": false, "reason": "timeout"}})
		f.fact("send_fails", true)
		if got := f.refused("start", id); !strings.Contains(got, "first message was not sent") {
			t.Errorf("%q", got)
		}
		if !hasCall(f.calls("app.log"), "do", "space.close", `{"confirmed":true,"id":"sp-test"}`) {
			t.Errorf("no space.close in %v", f.calls("app.log"))
		}
		if status := f.header(id)["status"]; status != "queued" {
			t.Errorf("status %v", status)
		}
	})

	t.Run("start refuses a task that already has a live worker", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id := f.addTask("Already running")
		f.worker(id)
		if got := f.refused("start", id); !strings.Contains(got, "already has a live worker") {
			t.Errorf("%q", got)
		}
	})

	t.Run("only todo and queued start", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id := f.addTask("Once")
		f.ok("start", id)
		if got := f.refused("start", id); !strings.Contains(got, "only todo or queued starts") {
			t.Errorf("%q", got)
		}
	})
}

func TestAsk(t *testing.T) {
	t.Run("an ask moves to needs and an answer closes it", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id := f.addTask("Add a search box")
		f.ok("start", id)
		f.worker(id)
		f.ok("ask", id, "Which colour?")
		head := f.header(id)
		if head["status"] != "needs" {
			t.Errorf("status %v", head["status"])
		}
		if asks, _ := head["asks"].([]any); len(asks) != 1 {
			t.Errorf("asks %v", head["asks"])
		}
		if got := f.ok("list"); !strings.Contains(got, "needs") {
			t.Errorf("list %q", got)
		}
		f.ok("say", id, "Use blue")
		head = f.header(id)
		if head["status"] != "working" {
			t.Errorf("status %v", head["status"])
		}
		if asks, _ := head["asks"].([]any); len(asks) != 0 {
			t.Errorf("asks %v", head["asks"])
		}
		if !hasCall(f.calls("hc.log"), "send", "--", "t9", "Use blue") {
			t.Errorf("no send in %v", f.calls("hc.log"))
		}
	})

	t.Run("a send that fails keeps the ask open", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id := f.addTask("Add a search box")
		f.ok("start", id)
		f.worker(id)
		f.ok("ask", id, "Which colour?")
		f.fact("send_fails", true)
		if got := f.refused("say", id, "Use blue"); !strings.Contains(got, "still needs you") {
			t.Errorf("%q", got)
		}
		head := f.header(id)
		if head["status"] != "needs" {
			t.Errorf("status %v", head["status"])
		}
		if asks, _ := head["asks"].([]any); len(asks) != 1 {
			t.Errorf("asks %v", head["asks"])
		}
	})

	t.Run("say without a live worker keeps the ask", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id := f.addTask("Add a search box")
		f.ok("start", id)
		f.ok("ask", id, "Which colour?")
		if got := f.refused("say", id, "Use blue"); !strings.Contains(got, "no live worker") {
			t.Errorf("%q", got)
		}
		if status := f.header(id)["status"]; status != "needs" {
			t.Errorf("status %v", status)
		}
	})

	t.Run("several asks need asking for one", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id := f.addTask("Add a search box")
		f.ok("start", id)
		f.worker(id)
		f.ok("ask", id, "First?")
		f.ok("ask", id, "Second?")
		asks, _ := f.header(id)["asks"].([]any)
		if got := f.refused("say", id, "Both"); !strings.Contains(got, "--ask") {
			t.Errorf("%q", got)
		}
		first, _ := asks[0].(map[string]any)
		second, _ := asks[1].(map[string]any)
		f.ok("say", id, "One", "--ask", first["id"].(string))
		head := f.header(id)
		if head["status"] != "needs" {
			t.Errorf("status %v", head["status"])
		}
		left, _ := head["asks"].([]any)
		if len(left) != 1 {
			t.Fatalf("asks %v", left)
		}
		if ask, _ := left[0].(map[string]any); ask["id"] != second["id"] {
			t.Errorf("kept %v, want %v", left[0], second["id"])
		}
		f.ok("say", id, "Two", "--ask", second["id"].(string))
		if status := f.header(id)["status"]; status != "working" {
			t.Errorf("status %v", status)
		}
	})
}

func TestQueue(t *testing.T) {
	t.Run("slots cap running workers and tick only starts queued", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		f.writeConfig("---\nworker: [\"pi\"]\nmax_running: 1\n---\n")
		first := f.addTask("First", "--status", "queued")
		second := f.addTask("Second", "--status", "queued")
		todo := f.addTask("Never by tick")
		if got := strings.TrimSpace(f.ok("tick")); got != first {
			t.Errorf("tick started %q, want %s", got, first)
		}
		if status := f.header(first)["status"]; status != "working" {
			t.Errorf("first status %v", status)
		}
		if status := f.header(second)["status"]; status != "queued" {
			t.Errorf("second status %v", status)
		}
		if got := strings.TrimSpace(f.ok("tick")); got != "" {
			t.Errorf("second tick started %q", got)
		}
		f.setStatus(first, "done")
		if got := strings.TrimSpace(f.ok("tick")); got != second {
			t.Errorf("third tick started %q, want %s", got, second)
		}
		if status := f.header(todo)["status"]; status != "todo" {
			t.Errorf("todo status %v", status)
		}
	})

	t.Run("a child waits for its parent and after", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		parent := f.addTask("Parent")
		sibling := f.addTask("Sibling")
		child := f.addTask("Child", "--status", "queued", "--parent", parent, "--after", sibling)
		if got := strings.TrimSpace(f.ok("tick")); got != "" {
			t.Errorf("tick started %q", got)
		}
		f.setStatus(parent, "done")
		if got := strings.TrimSpace(f.ok("tick")); got != "" {
			t.Errorf("tick started %q", got)
		}
		f.setStatus(sibling, "done")
		if got := strings.TrimSpace(f.ok("tick")); got != child {
			t.Errorf("tick started %q, want %s", got, child)
		}
		if status := f.header(child)["status"]; status != "working" {
			t.Errorf("status %v", status)
		}
	})

	t.Run("a needs worker still holds its slot", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		f.writeConfig("---\nworker: [\"pi\"]\nmax_running: 1\n---\n")
		wait := f.addTask("Waiting")
		other := f.addTask("Other", "--status", "queued")
		f.ok("start", wait)
		f.ok("ask", wait, "Well?")
		if got := strings.TrimSpace(f.ok("tick")); got != "" {
			t.Errorf("tick started %q", got)
		}
		if status := f.header(other)["status"]; status != "queued" {
			t.Errorf("status %v", status)
		}
	})
}

// headerIn reads a task header in a named space.
func (f *fixture) headerIn(space, id string) map[string]any {
	f.t.Helper()
	return f.fileHeader(filepath.Join(f.store, "spaces", space, id+".md"))
}

// spaceHeader reads a space file's header.
func (f *fixture) spaceHeader(space string) map[string]any {
	f.t.Helper()
	return f.fileHeader(filepath.Join(f.store, "spaces", space+".md"))
}

func (f *fixture) fileHeader(path string) map[string]any {
	f.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		f.t.Fatal(err)
	}
	head := map[string]any{}
	lines := strings.Split(string(data), "\n")
	end := 1
	for ; end < len(lines); end++ {
		if lines[end] == "---" {
			break
		}
	}
	for _, line := range lines[1:end] {
		key, value, _ := strings.Cut(line, ":")
		var parsed any
		if json.Unmarshal([]byte(strings.TrimSpace(value)), &parsed) == nil {
			head[strings.TrimSpace(key)] = parsed
		} else {
			head[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return head
}

// background is a shp command still running, as two overlapping commands would be.
type background struct {
	cmd    *exec.Cmd
	stdout strings.Builder
	stderr strings.Builder
}

func (f *fixture) startBackground(args ...string) *background {
	f.t.Helper()
	b := &background{cmd: exec.Command(shpBin, args...)}
	b.cmd.Env = f.env
	b.cmd.Stdout = &b.stdout
	b.cmd.Stderr = &b.stderr
	if err := b.cmd.Start(); err != nil {
		f.t.Fatal(err)
	}
	return b
}

func (f *fixture) waitBackground(b *background) result {
	f.t.Helper()
	err := b.cmd.Wait()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		f.t.Fatalf("wait: %v", err)
	}
	return result{b.stdout.String(), b.stderr.String(), code}
}

func hasCall(calls [][]string, want ...string) bool {
	for _, call := range calls {
		if len(call) >= len(want) && strings.Join(call[:len(want)], "\x00") == strings.Join(want, "\x00") {
			return true
		}
	}
	return false
}

// isLaunch says a herdr-app call is a worker's space.new, not a repo's.
func isLaunch(call []string) bool {
	return len(call) == 3 && call[0] == "do" && call[1] == "space.new" && strings.Contains(call[2], `"argv"`)
}

func countLaunches(calls [][]string) int {
	launches := 0
	for _, call := range calls {
		if isLaunch(call) {
			launches++
		}
	}
	return launches
}

// launch is the payload of the first worker launch.
func (f *fixture) launch() map[string]any {
	f.t.Helper()
	for _, call := range f.calls("app.log") {
		if isLaunch(call) {
			var payload map[string]any
			if err := json.Unmarshal([]byte(call[2]), &payload); err != nil {
				f.t.Fatal(err)
			}
			return payload
		}
	}
	f.t.Fatalf("no launch in %v", f.calls("app.log"))
	return nil
}
