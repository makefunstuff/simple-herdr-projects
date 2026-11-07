package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shepherdr writes a Shepherdr 3.1 store: one file per project and the role settings.
func (f *fixture) shepherdr(projects map[string]any, settings any) {
	f.t.Helper()
	dir := filepath.Join(f.home, ".herdr", "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	files := map[string]any{filepath.Join(f.home, ".herdr", "shepherdr.json"): settings}
	for name, project := range projects {
		files[filepath.Join(dir, name+".json")] = project
	}
	for path, value := range files {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
	// Shepherdr's own bookkeeping, which import skips.
	if err := os.WriteFile(filepath.Join(dir, ".autopilot.json"), []byte("{}"), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// oldStore is every file of the Shepherdr store with its bytes.
func (f *fixture) oldStore() map[string]string {
	f.t.Helper()
	files := map[string]string{}
	root := filepath.Join(f.home, ".herdr")
	paths, _ := filepath.Glob(filepath.Join(root, "tasks", "*"))
	for _, path := range append(paths, filepath.Join(root, "shepherdr.json")) {
		data, err := os.ReadFile(path)
		if err != nil {
			f.t.Fatal(err)
		}
		files[path] = string(data)
	}
	return files
}

func (f *fixture) sameOldStore(before map[string]string) {
	f.t.Helper()
	after := f.oldStore()
	if len(after) != len(before) {
		f.t.Fatalf("the old store has %d files, not %d", len(after), len(before))
	}
	for path, text := range before {
		if after[path] != text {
			f.t.Errorf("%s changed", path)
		}
	}
}

// oldWorktree makes a task's worktree with one commit and returns it and its HEAD.
func (f *fixture) oldWorktree(name, branch string) (string, string) {
	f.t.Helper()
	path := filepath.Join(f.root, name)
	f.gitAt(f.repo, "worktree", "add", "-q", "-b", branch, path, "main")
	f.commit(path, "feature.txt", name+"\n")
	return path, f.head(path)
}

// appProject is a Shepherdr project on the fixture's repo with a task in every state.
func (f *fixture) appProject() (map[string]any, string) {
	f.t.Helper()
	runningPath, _ := f.oldWorktree("running", "task/2-running")
	readyPath, readyHead := f.oldWorktree("ready", "task/5-ready")
	return map[string]any{
		"version": 1, "folder": f.repo, "kind": "repo", "name": "My App", "slug": "app",
		"prefix": "APP", "next": 3, "github": map[string]any{"repo": nil},
		"tasks": []any{
			map[string]any{"id": 1, "title": "Old work", "prompt": "Did it.", "mode": "worktree",
				"state": "done", "archived": 1, "branch": "task/1-old", "base": "main",
				"worktree": filepath.Join(f.home, "gone"), "merged": true,
				"summary": "It was done", "proof": map[string]any{"id": "proof-1"},
				"pr":      map[string]any{"url": "https://github.com/example/app/pull/1"},
				"reviews": []any{map[string]any{"round": 1, "text": "Round 1: fine", "head": "abc123"}},
				"agents":  []any{map[string]any{"address": "t1", "agent": "pi", "ended": 5}}},
			map[string]any{"id": 2, "title": "Running work", "prompt": "Keep going.", "mode": "worktree",
				"state": "running", "branch": "task/2-running", "base": "main", "worktree": runningPath,
				"agent":    "claude",
				"progress": map[string]any{"text": "Testing", "done": 60, "left": 1_200_000},
				"agents":   []any{map[string]any{"address": "t5", "agent": "claude"}}},
			map[string]any{"id": 3, "title": "Next work", "prompt": "Then this.", "mode": "worktree",
				"state": "todo", "after": []any{2}, "agents": []any{}},
			map[string]any{"id": 4, "title": "Waiting work", "prompt": "Later.", "mode": "worktree",
				"state": "queued", "after": []any{9}, "agents": []any{}},
			map[string]any{"id": 5, "title": "Ready work", "prompt": "Done soon.", "mode": "worktree",
				"state": "ready", "branch": "task/5-ready", "base": "main", "worktree": readyPath,
				"summary": "Ready to go",
				"reviews": []any{map[string]any{"round": 1, "text": "Round 1: no findings", "head": readyHead}},
				"agents":  []any{map[string]any{"address": "t6", "agent": "pi", "ended": 9}}},
			map[string]any{"id": 6, "title": "Queued work", "prompt": "When there is room.", "mode": "worktree",
				"state": "queued", "agents": []any{}},
		},
	}, runningPath
}

// otherProject is a second project, a pull-request repo whose task waits on one of APP's.
func (f *fixture) otherProject() map[string]any {
	return map[string]any{
		"version": 1, "folder": f.makeRepo("other"), "kind": "repo", "name": "other.lib", "slug": "other-lib",
		"prefix": "OTH", "next": 2, "github": map[string]any{"repo": "example/other"},
		"tasks": []any{
			map[string]any{"id": 1, "title": "Follow up", "prompt": "After APP-3.", "mode": "worktree",
				"state": "todo", "after_ext": []any{map[string]any{"ref": "APP-3", "machine": nil}}, "agents": []any{}},
		},
	}
}

func (f *fixture) homeProject() map[string]any {
	return map[string]any{
		"version": 1, "folder": f.home, "kind": "folder", "name": "~", "slug": "home", "prefix": "JPL", "next": 2,
		"tasks": []any{
			map[string]any{"id": 1, "title": "Desktop tweak", "prompt": "Done here.", "mode": "here",
				"state": "done", "agents": []any{}},
			map[string]any{"id": 2, "title": "Queued tweak", "prompt": "Later here.", "mode": "here",
				"state": "queued", "agents": []any{}},
		},
	}
}

var roleSettings = map[string]any{"version": 1, "everywhere": map[string]any{
	"worker":   map[string]any{"agent": "claude"},
	"reviewer": map[string]any{"agent": "codex", "model": "gpt-6"},
	"tasks":    map[string]any{"max_running": 6},
}}

func (f *fixture) liveOldWorker() {
	f.fact("ls.json", map[string]any{"terminals": []any{
		map[string]any{"address": "t5", "state": "live"},
		map[string]any{"address": "t6", "state": "live"},
	}})
}

func TestImport(t *testing.T) {
	t.Run("a dry run names the open tasks and writes nothing", func(t *testing.T) {
		f := newFixture(t)
		app, _ := f.appProject()
		f.shepherdr(map[string]any{"app": app, "other-lib": f.otherProject(), "broken": []any{"not a project"}}, roleSettings)
		f.liveOldWorker()
		before := f.oldStore()
		out := f.ok("import", "--dry-run")
		wantIn(t, out, "Not imported: ~/.herdr/tasks/broken.json")
		wantIn(t, out, "Dry run: nothing was written.")
		wantIn(t, out, "## my-app: APP")
		wantIn(t, out, "APP-2  working  Running work")
		wantIn(t, out, "adopts worker t5")
		wantIn(t, out, "APP-3  todo  Next work")
		wantIn(t, out, "APP-5  ready  Ready work")
		wantIn(t, out, "OTH-1  todo  Follow up")
		wantIn(t, out, "1 done (1 of them archived). Next is APP-7.")
		if strings.Contains(out, "APP-1  done") {
			t.Errorf("a dry run lists done tasks one by one:\n%s", out)
		}
		if exists(f.store) {
			t.Errorf("a dry run made %s", f.store)
		}
		if calls := f.calls("hc.log"); hasCall(calls, "label") {
			t.Errorf("a dry run labelled a terminal: %v", calls)
		}
		f.sameOldStore(before)
	})

	t.Run("a real run writes every space and task for shp to read", func(t *testing.T) {
		f := newFixture(t)
		app, _ := f.appProject()
		f.shepherdr(map[string]any{"app": app, "other-lib": f.otherProject(), "home": f.homeProject()}, roleSettings)
		f.liveOldWorker()
		before := f.oldStore()
		out := f.ok("import")
		f.sameOldStore(before)
		wantIn(t, out, `worker: ["claude"] (was ["pi"])`)

		list := f.ok("list")
		for _, want := range []string{"APP-1  done  Old work", "APP-2  working  Running work",
			"APP-3  todo", "APP-4  todo", "APP-5  ready", "OTH-1  todo", "JPL-1  done  Desktop tweak"} {
			wantIn(t, list, want)
		}
		f.space("my-app", "prefix: APP", "base: main", "merge: local", "next: 7")
		f.space("other-lib", "prefix: OTH", "merge: pr", "next: 2")
		home := f.space("home", "prefix: JPL", "folder: ~")
		if strings.Contains(home, "base:") {
			t.Errorf("a folder space got a base:\n%s", home)
		}
		folderTask := f.headerIn("home", "JPL-2")
		wantField(t, folderTask, "status", "todo")
		wantIn(t, fmtValue(folderTask["step"]), "its space is a folder")
		wantIn(t, f.ok("tick"), "APP-6")
		queued := f.headerIn("my-app", "APP-6")
		wantField(t, queued, "status", "working")
		wantField(t, queued, "branch", "task/6-queued-work")

		done := f.headerIn("my-app", "APP-1")
		wantField(t, done, "summary", "It was done")
		wantField(t, done, "proof", "proof-1")
		wantField(t, done, "pr", "https://github.com/example/app/pull/1")
		wantField(t, done, "review", 1)
		wantField(t, done, "reviewed_commit", "abc123")
		wantField(t, done, "imported_key", "APP-1")
		wantIn(t, f.read("spaces/my-app/APP-1.md"), "\nDid it.\n")
		wantIn(t, f.read("spaces/my-app/APP-1.review-1.md"), "Round 1: fine")
		wantIn(t, f.read("spaces/my-app/APP-1.shepherdr.json"), `"archived": 1`)

		running := f.headerIn("my-app", "APP-2")
		wantField(t, running, "agent", "[claude]")
		wantField(t, running, "progress", 60)
		wantField(t, running, "step", "Testing")
		wantField(t, running, "left", "20m")
		wantField(t, f.headerIn("my-app", "APP-5"), "agent", "[pi]")
		wantField(t, f.headerIn("my-app", "APP-3"), "after", "[APP-2]")
		wantField(t, f.headerIn("other-lib", "OTH-1"), "after", "[APP-3]")
		waiting := f.headerIn("my-app", "APP-4")
		wantIn(t, fmtValue(waiting["step"]), "it waited on APP-9")

		config := f.ok("config")
		wantIn(t, config, `worker: ["claude"]`)
		wantIn(t, config, `reviewer: ["codex", "--model", "gpt-6"]`)
		wantIn(t, config, "max_running: 6")
		wantIn(t, f.read("import.md"), "## my-app: APP")

		if !hasCall(f.calls("hc.log"), "label", "t5", "herdr.task=APP-2", "herdr.role=worker") {
			t.Errorf("the live old worker was not labelled: %v", f.calls("hc.log"))
		}
		if got := strings.TrimSpace(f.ok("add", "New work", "--space", "my-app")); got != "APP-7" {
			t.Errorf("the next task is %s, not APP-7", got)
		}
	})

	t.Run("an imported ready task accepts", func(t *testing.T) {
		f := newFixture(t)
		app, _ := f.appProject()
		f.shepherdr(map[string]any{"app": app}, roleSettings)
		f.ok("import")
		ready := f.headerIn("my-app", "APP-5")
		wantField(t, ready, "status", "ready")
		wantField(t, ready, "blocking", 0)
		f.ok("accept", "APP-5", "--yes")
		wantField(t, f.headerIn("my-app", "APP-5"), "status", "done")
		if !strings.Contains(f.gitAt(f.repo, "log", "--oneline", "main"), "change feature.txt") {
			t.Error("main lacks the ready task's commit")
		}
	})

	t.Run("a ready task off its reviewed commit imports as working", func(t *testing.T) {
		f := newFixture(t)
		app, _ := f.appProject()
		f.commit(filepath.Join(f.root, "ready"), "later.txt", "later\n")
		f.shepherdr(map[string]any{"app": app}, roleSettings)
		f.ok("import")
		moved := f.headerIn("my-app", "APP-5")
		wantField(t, moved, "status", "working")
		wantIn(t, fmtValue(moved["step"]), "review it again")
	})

	t.Run("an adopted worker can finish", func(t *testing.T) {
		f := newFixture(t)
		app, worktree := f.appProject()
		f.shepherdr(map[string]any{"app": app}, roleSettings)
		f.liveOldWorker()
		f.ok("import")
		f.fact("ls.json", map[string]any{"terminals": []any{map[string]any{"address": "t5", "state": "live",
			"labels": map[string]any{"herdr.role": "worker", "herdr.task": "APP-2"}}}})
		f.ok("say", "APP-2", "carry on")
		if !hasCall(f.calls("hc.log"), "send", "--", "t5", "carry on") {
			t.Errorf("say did not reach the adopted worker: %v", f.calls("hc.log"))
		}
		f.commit(worktree, "more.txt", "more\n")
		f.report(0, 0, 0)
		f.ok("review", "APP-2")
		f.ok("ready", "APP-2", "--summary", "Finished")
		wantField(t, f.headerIn("my-app", "APP-2"), "status", "ready")
	})

	t.Run("a worker that is gone says so in its step", func(t *testing.T) {
		f := newFixture(t)
		app, _ := f.appProject()
		f.shepherdr(map[string]any{"app": app}, roleSettings)
		f.ok("import")
		wantIn(t, fmtValue(f.headerIn("my-app", "APP-2")["step"]), "no live worker")
		if hasCall(f.calls("hc.log"), "label") {
			t.Errorf("a gone worker was labelled: %v", f.calls("hc.log"))
		}
	})

	t.Run("a second run keeps what is there", func(t *testing.T) {
		f := newFixture(t)
		app, _ := f.appProject()
		f.shepherdr(map[string]any{"app": app}, roleSettings)
		f.ok("import")
		f.ok("status", "APP-3", "--step", "edited in shp")
		out := f.ok("import")
		wantIn(t, out, "APP-3 is in shp already; kept as it is.")
		wantField(t, f.headerIn("my-app", "APP-3"), "step", "edited in shp")
		if spaces, _ := filepath.Glob(filepath.Join(f.store, "spaces", "*.md")); len(spaces) != 1 {
			t.Errorf("a second run made %d space files, not 1", len(spaces))
		}
	})

	t.Run("a taken prefix gets a digit and references follow", func(t *testing.T) {
		f := newFixture(t)
		app, _ := f.appProject()
		app["folder"] = f.makeRepo("app-two")
		f.makeSpace("work", "--prefix", "APP")
		f.shepherdr(map[string]any{"app": app}, roleSettings)
		out := f.ok("import")
		wantIn(t, out, "APP was taken, so its tasks are APP2-<n>")
		next := f.headerIn("my-app", "APP2-3")
		wantField(t, next, "after", "[APP2-2]")
		wantField(t, next, "imported_key", "APP-3")
	})

	t.Run("a space already on the folder takes the tasks", func(t *testing.T) {
		f := newFixture(t)
		app, _ := f.appProject()
		f.makeSpace("work", "--prefix", "WRK")
		f.addTask("Made in shp")
		link := filepath.Join(f.root, "link")
		if err := os.Symlink(f.repo, link); err != nil {
			t.Fatal(err)
		}
		app["folder"] = link
		f.shepherdr(map[string]any{"app": app}, roleSettings)
		out := f.ok("import")
		wantIn(t, out, "Not imported: APP-1, as WRK-1 is another task.")
		wantField(t, f.header("WRK-1"), "title", "Made in shp")
		wantField(t, f.header("WRK-2"), "imported_key", "APP-2")
		f.space("work", "next: 7")
	})

	t.Run("a second project on the same folder is reported, not imported", func(t *testing.T) {
		f := newFixture(t)
		app, _ := f.appProject()
		twin := map[string]any{"version": 1, "folder": f.repo, "kind": "repo", "name": "twin", "slug": "twin",
			"prefix": "TWIN", "next": 1, "tasks": []any{}}
		f.shepherdr(map[string]any{"app": app, "twin": twin}, roleSettings)
		wantIn(t, f.ok("import", "--dry-run"), "Not imported: ~/.herdr/tasks/twin.json: ~/.herdr/tasks/app.json is on the same folder.")
	})
}

// space reads a space file and checks it holds each line.
func (f *fixture) space(name string, lines ...string) string {
	f.t.Helper()
	text := f.read(filepath.Join("spaces", name+".md"))
	for _, line := range lines {
		if !strings.Contains(text, "\n"+line+"\n") {
			f.t.Errorf("space %s lacks %q:\n%s", name, line, text)
		}
	}
	return text
}

// read reads a file of the store.
func (f *fixture) read(name string) string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.store, name))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(data)
}

func fmtValue(value any) string {
	text, _ := value.(string)
	return text
}
