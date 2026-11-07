package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// openSpaces makes herdr-app ls list these spaces, each as id, folder and tab names.
func (f *fixture) openSpaces(spaces ...map[string]any) {
	f.t.Helper()
	f.fact("spaces.json", map[string]any{"spaces": spaces})
}

func herdrSpaceAt(id, folder string, tabs ...string) map[string]any {
	listed := make([]any, len(tabs))
	for i, name := range tabs {
		listed[i] = map[string]any{"id": "tb-" + name, "name": name}
	}
	return map[string]any{"id": id, "name": id, "folder": folder, "machine": nil, "tabs": listed}
}

// appCalls are the herdr-app do calls of one command, with their payloads.
func (f *fixture) appCalls(command string) []map[string]any {
	f.t.Helper()
	var found []map[string]any
	for _, call := range f.calls("app.log") {
		if len(call) == 3 && call[0] == "do" && call[1] == command {
			var payload map[string]any
			if err := json.Unmarshal([]byte(call[2]), &payload); err != nil {
				f.t.Fatal(err)
			}
			found = append(found, payload)
		}
	}
	return found
}

// tabNames are the names of the tabs opened with tab.new, in order.
func (f *fixture) tabNames() []string {
	var names []string
	for _, payload := range f.appCalls("tab.new") {
		names = append(names, payload["name"].(string))
	}
	return names
}

// order is the position of a call in every tool's log, or -1.
func (f *fixture) order(want ...string) int {
	f.t.Helper()
	return slices.IndexFunc(f.calls("all.log"), func(call []string) bool {
		return hasCall([][]string{call}, want...)
	})
}

func (f *fixture) installMod(name string) {
	f.t.Helper()
	dir := filepath.Join(f.home, ".config", "herdr-app", "mods", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.js"), []byte("export default {};\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func TestRepoSpace(t *testing.T) {
	t.Run("space add makes the repo's Herdr space when there is none", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		made := f.appCalls("space.new")
		if len(made) != 1 {
			t.Fatalf("space.new calls %v", made)
		}
		wantField(t, made[0], "folder", f.repo)
		wantField(t, made[0], "name", "work")
		wantField(t, f.spaceHeader("work"), "herdr_space", "sp-repo")
	})

	t.Run("space add keeps a space already at the repo", func(t *testing.T) {
		f := newFixture(t)
		link := filepath.Join(f.root, "link")
		if err := os.Symlink(f.repo, link); err != nil {
			t.Fatal(err)
		}
		remote := herdrSpaceAt("sp-remote", f.repo)
		remote["machine"] = "machine-a"
		f.openSpaces(remote, herdrSpaceAt("sp-other", f.root), herdrSpaceAt("sp-hand", link))
		f.makeSpace("work")
		if made := f.appCalls("space.new"); len(made) != 0 {
			t.Errorf("made %v", made)
		}
		wantField(t, f.spaceHeader("work"), "herdr_space", "sp-hand")
	})

	t.Run("space add works while Herdr is out of reach", func(t *testing.T) {
		f := newFixture(t)
		f.fact("app_fails", true)
		got := f.shp("", "space", "add", "work", "--folder", f.repo)
		if got.code != 0 {
			t.Fatalf("exit %d: %s", got.code, got.stderr)
		}
		wantIn(t, got.stderr, "space work has no Herdr space yet")
		if _, ok := f.spaceHeader("work")["herdr_space"]; ok {
			t.Error("herdr_space was written")
		}
	})
}

func TestTaskTabs(t *testing.T) {
	t.Run("a started task's space and tabs follow its worktree, after the repo's space", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		f.installMod("px-files")
		f.installMod("px-changes")
		id := f.addTask("Search box")
		f.ok("start", id)
		worktree := f.worktree(id)
		launch := f.launch()
		wantField(t, launch, "folder", worktree)
		wantField(t, launch, "tab", "Chat")
		calls := f.calls("app.log")
		if repo := f.appCalls("space.new")[0]; repo["folder"] != f.repo || !isLaunch(calls[len(calls)-4]) {
			t.Errorf("the repo's space is not made before the launch: %v", calls)
		}
		tabs := f.appCalls("tab.new")
		if got := f.tabNames(); !slices.Equal(got, []string{"Terminal", "Files", "Changes"}) {
			t.Fatalf("tabs %v", got)
		}
		wantField(t, tabs[0], "folder", worktree)
		for i, mod := range []any{nil, "px-files", "px-changes"} {
			wantField(t, tabs[i], "space", "sp-test")
			wantField(t, tabs[i], "mod", mod)
		}
	})

	t.Run("a missing mod is a shell tab", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id := f.addTask("Search box")
		f.ok("start", id)
		for _, tab := range f.appCalls("tab.new") {
			if _, ok := tab["mod"]; ok {
				t.Errorf("tab with a mod: %v", tab)
			}
		}
	})

	t.Run("a tab that will not open leaves the worker running", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id := f.addTask("Search box")
		f.fact("tabs_fail", true)
		got := f.shp("", "start", id)
		if got.code != 0 {
			t.Fatalf("exit %d: %s", got.code, got.stderr)
		}
		wantIn(t, got.stderr, "has no Terminal tab")
		wantField(t, f.header(id), "status", "working")
		wantField(t, f.header(id), "workspace", "sp-test")
	})

	t.Run("a pull request gets one PR tab", func(t *testing.T) {
		f := newFixture(t)
		f.prSpace()
		id, worktree := f.readyTask("Search box")
		f.openSpaces(herdrSpaceAt("sp-test", worktree, "Chat", "Terminal"))
		f.installMod("px-pr")
		f.ok("accept", id, "--yes")
		pr := f.appCalls("tab.new")
		pr = slices.DeleteFunc(pr, func(tab map[string]any) bool { return tab["name"] != "PR" })
		if len(pr) != 1 {
			t.Fatalf("PR tabs %v", pr)
		}
		wantField(t, pr[0], "space", "sp-test")
		wantField(t, pr[0], "mod", "px-pr")

		f.openSpaces(herdrSpaceAt("sp-test", worktree, "Chat", "Terminal", "PR"))
		f.fact("gh_view.json", map[string]any{"state": "OPEN", "mergeable": "CONFLICTING", "mergeStateStatus": "DIRTY"})
		f.ok("tick")
		f.fact("gh_view.json", map[string]any{"url": "https://example.com/pr/7", "state": "OPEN"})
		f.ok("accept", id, "--yes")
		if got := strings.Count(strings.Join(f.tabNames(), " "), "PR"); got != 1 {
			t.Errorf("%d PR tabs: %v", got, f.tabNames())
		}
	})
}

func TestRemoval(t *testing.T) {
	t.Run("done kills the task's terminals, then closes only its space", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, worktree := f.working("Search box")
		f.worker(id)
		f.openSpaces(herdrSpaceAt("sp-repo", f.repo), herdrSpaceAt("sp-hand", worktree), herdrSpaceAt("sp-test", worktree))
		f.ok("done", id)
		killed := f.order("hc", "kill", "t9")
		closed := f.order("herdr-app", "do", "space.close", `{"confirmed":true,"id":"sp-test"}`)
		if killed < 0 || closed < killed {
			t.Errorf("kill at %d, close at %d: %v", killed, closed, f.calls("all.log"))
		}
		if closes := f.appCalls("space.close"); len(closes) != 1 {
			t.Errorf("closed %v", closes)
		}
	})

	t.Run("done leaves a space already closed by hand alone", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, _ := f.working("Search box")
		got := f.shp("", "done", id)
		if got.code != 0 || got.stderr != "" {
			t.Errorf("exit %d: %s", got.code, got.stderr)
		}
		if closes := f.appCalls("space.close"); len(closes) != 0 {
			t.Errorf("closed %v", closes)
		}
	})

	t.Run("accept closes the space before the worktree goes", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, worktree := f.readyTask("Search box")
		f.worker(id)
		f.openSpaces(herdrSpaceAt("sp-test", worktree))
		f.ok("accept", id, "--yes")
		killed := f.order("hc", "kill", "t9")
		closed := f.order("herdr-app", "do", "space.close", `{"confirmed":true,"id":"sp-test"}`)
		if killed < 0 || closed < killed {
			t.Errorf("kill at %d, close at %d: %v", killed, closed, f.calls("all.log"))
		}
		if exists(worktree) {
			t.Error("the worktree is still there")
		}
	})

	t.Run("a restart closes the task's old space, leaving no orphan", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, worktree := f.working("Search box")
		f.setStatus(id, "todo")
		f.openSpaces(herdrSpaceAt("sp-test", worktree, "Chat", "Terminal"))
		f.ok("start", id)
		var launches []int
		for i, call := range f.calls("all.log") {
			if isLaunch(call[1:]) {
				launches = append(launches, i)
			}
		}
		closed := f.order("herdr-app", "do", "space.close", `{"confirmed":true,"id":"sp-test"}`)
		if len(launches) != 2 || closed < launches[0] || closed > launches[1] {
			t.Errorf("launches at %v, close at %d: %v", launches, closed, f.calls("all.log"))
		}
	})

	t.Run("no layout is written", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, worktree := f.readyTask("Search box")
		f.openSpaces(herdrSpaceAt("sp-test", worktree))
		f.ok("accept", id, "--yes")
		for _, call := range f.calls("app.log") {
			if len(call) > 1 && strings.HasPrefix(call[1], "layout.") {
				t.Errorf("layout call %v", call)
			}
		}
		if exists(filepath.Join(f.home, ".config", "herdr-app")) {
			t.Error("shp wrote into herdr-app's folder")
		}
	})
}
