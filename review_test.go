package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var reportPath = regexp.MustCompile(`\S*shp-review-\S+\.md`)

// fakeReviewer opens a Review tab: it notes where the report goes and writes
// review.md there at once, or at the first wait when review_late is set.
func fakeReviewer(state, payload string) int {
	var args map[string]any
	if err := json.Unmarshal([]byte(payload), &args); err != nil {
		return 2
	}
	task, _ := args["task"].(string)
	if err := os.WriteFile(filepath.Join(state, "report_path"), []byte(reportPath.FindString(task)), 0o644); err != nil {
		return 1
	}
	if exists(filepath.Join(state, "move_head")) {
		folder, _ := args["folder"].(string)
		if err := exec.Command("git", "-C", folder, "commit", "-q", "--allow-empty", "-m", "moved").Run(); err != nil {
			return 1
		}
	}
	if !exists(filepath.Join(state, "review_late")) {
		writeReport(state)
	}
	printJSON(map[string]any{"space": args["space"], "tab": "tb-review", "terminal": "t10"})
	return 0
}

// writeReport is the fake reviewer finishing: review.md lands where the report goes.
func writeReport(state string) {
	path, err := os.ReadFile(filepath.Join(state, "report_path"))
	if err != nil || len(path) == 0 || exists(string(path)) {
		return
	}
	if report, err := os.ReadFile(filepath.Join(state, "review.md")); err == nil {
		_ = os.WriteFile(string(path), report, 0o644)
	}
}

// fakeGH answers pr view from gh_view.json (none: no pull request), creates
// pull request 7 and merges unless gh_merge_fails is set.
func fakeGH(state string, args []string) int {
	if len(args) < 2 || args[0] != "pr" {
		return 2
	}
	switch args[1] {
	case "view":
		view, err := os.ReadFile(filepath.Join(state, "gh_view.json"))
		if err != nil {
			fmt.Fprintln(os.Stderr, "no pull requests found")
			return 1
		}
		fmt.Println(string(view))
	case "create":
		fmt.Println("https://github.com/example/app/pull/7")
	case "merge":
		if exists(filepath.Join(state, "gh_merge_fails")) {
			fmt.Fprintln(os.Stderr, "auto-merge is not allowed for this repository")
			return 1
		}
	}
	return 0
}

// commit writes a file in a checkout and commits it.
func (f *fixture) commit(dir, name, text string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.gitAt(dir, "add", name)
	f.gitAt(dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", "change "+name)
}

// report is what the fake reviewer writes next.
func (f *fixture) report(blocker, major, minor int) {
	f.t.Helper()
	text := fmt.Sprintf("---\nblocker: %d\nmajor: %d\nminor: %d\n---\nFindings.\n", blocker, major, minor)
	if err := os.WriteFile(filepath.Join(f.state, "review.md"), []byte(text), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) worktree(id string) string {
	f.t.Helper()
	return expandIn(f.home, fmt.Sprint(f.header(id)["worktree"]))
}

func expandIn(home, path string) string {
	return strings.Replace(path, "~", home, 1)
}

func (f *fixture) head(dir string) string {
	f.t.Helper()
	return strings.TrimSpace(f.gitAt(dir, "rev-parse", "HEAD"))
}

// working starts a task with one commit on its branch.
func (f *fixture) working(title string) (string, string) {
	f.t.Helper()
	id := f.addTask(title)
	f.ok("start", id)
	worktree := f.worktree(id)
	f.commit(worktree, "feature.txt", title+"\n")
	return id, worktree
}

// readyTask is a working task whose clean round made it ready.
func (f *fixture) readyTask(title string) (string, string) {
	f.t.Helper()
	id, worktree := f.working(title)
	f.report(0, 0, 1)
	f.ok("review", id)
	f.ok("ready", id, "--summary", "It works")
	return id, worktree
}

func wantIn(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("%q lacks %q", got, want)
	}
}

func wantField(t *testing.T, head map[string]any, key string, want any) {
	t.Helper()
	if got := fmt.Sprint(head[key]); got != fmt.Sprint(want) {
		t.Errorf("%s is %s, want %v", key, got, want)
	}
}

func TestReview(t *testing.T) {
	t.Run("a round launches a labelled reviewer and records its counts", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work", "--checks", `[["go", "test", "./..."]]`)
		id, worktree := f.working("Search box")
		f.report(1, 2, 3)
		out := f.ok("review", id)
		wantIn(t, out, "round 1: 1 blocker, 2 major, 3 minor; 3 to fix")
		head := f.header(id)
		wantField(t, head, "review", 1)
		wantField(t, head, "blocking", 3)
		wantField(t, head, "reviewed_commit", f.head(worktree))
		kept, err := os.ReadFile(filepath.Join(f.store, "spaces", "work", id+".review-1.md"))
		if err != nil {
			t.Fatal(err)
		}
		wantIn(t, string(kept), "Findings.")

		var payload map[string]any
		for _, call := range f.calls("app.log") {
			if len(call) == 3 && call[1] == "tab.new" && strings.Contains(call[2], `"name":"Review"`) {
				_ = json.Unmarshal([]byte(call[2]), &payload)
			}
		}
		wantField(t, payload, "name", "Review")
		wantField(t, payload, "space", "sp-test")
		wantField(t, payload, "folder", worktree)
		wantField(t, payload, "argv", []any{"pi"})
		prompt := fmt.Sprint(payload["task"])
		for _, want := range []string{"git diff main...HEAD", "go test ./...", "reviewer.md", "blocker: <n>", "shp-review-" + id + "-1.md"} {
			wantIn(t, prompt, want)
		}
		if !hasCall(f.calls("hc.log"), "label", "t10", "herdr.task="+id, "herdr.role=reviewer") {
			t.Errorf("reviewer not labelled: %v", f.calls("hc.log"))
		}
	})

	t.Run("a blocker stops ready", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, _ := f.working("Search box")
		f.report(1, 0, 0)
		f.ok("review", id)
		wantIn(t, f.refused("ready", id), "left 1 findings to fix")
		wantField(t, f.header(id), "status", "working")
	})

	t.Run("must_fix blocker lets majors through", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		f.writeConfig("---\nmust_fix: blocker\n---\n")
		id, _ := f.working("Search box")
		f.report(0, 2, 0)
		f.ok("review", id)
		f.ok("ready", id)
		wantField(t, f.header(id), "status", "ready")
	})

	t.Run("no round refuses ready", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, _ := f.working("Search box")
		wantIn(t, f.refused("ready", id), "has no review round")
	})

	t.Run("a commit after the round refuses ready", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, worktree := f.working("Search box")
		f.report(0, 0, 0)
		f.ok("review", id)
		f.commit(worktree, "later.txt", "later\n")
		wantIn(t, f.refused("ready", id), "round 1 of "+id+" is stale")
	})

	t.Run("a HEAD moved during the round records no round", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, _ := f.working("Search box")
		f.report(0, 0, 0)
		f.fact("move_head", true)
		wantIn(t, f.refused("review", id), "during round 1; no round recorded")
		if _, ok := f.header(id)["review"]; ok {
			t.Error("a round was recorded")
		}
	})

	t.Run("uncommitted changes refuse a round", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, worktree := f.working("Search box")
		if err := os.WriteFile(filepath.Join(worktree, "feature.txt"), []byte("edited\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		wantIn(t, f.refused("review", id), "uncommitted changes")
	})

	t.Run("a slow reviewer is waited for", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, _ := f.working("Search box")
		f.report(0, 0, 0)
		f.fact("review_late", true)
		f.ok("review", id)
		wantField(t, f.header(id), "review", 1)
		if !hasCall(f.calls("hc.log"), "wait", "t10") {
			t.Errorf("no wait: %v", f.calls("hc.log"))
		}
	})

	t.Run("a reviewer that ends without a report records no round", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, _ := f.working("Search box")
		f.report(0, 0, 0)
		f.fact("review_late", true)
		f.fact("get.json", map[string]any{"state": "finished", "first_message": map[string]any{"sent": true}})
		wantIn(t, f.refused("review", id), "ended without writing")
	})

	t.Run("a report without counts records no round until reviewed records it", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, worktree := f.working("Search box")
		if err := os.WriteFile(filepath.Join(f.state, "review.md"), []byte("Looks fine.\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		wantIn(t, f.refused("review", id), "no header of blocker, major and minor counts")
		if _, ok := f.header(id)["review"]; ok {
			t.Error("a round was recorded")
		}
		report := filepath.Join(f.root, "report.md")
		if err := os.WriteFile(report, []byte("---\nblocker: 0\nmajor: 0\nminor: 0\n---\nLooks fine.\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		f.ok("reviewed", id, "--report", report)
		head := f.header(id)
		wantField(t, head, "review", 1)
		wantField(t, head, "reviewed_commit", f.head(worktree))
		f.ok("ready", id)
	})

	t.Run("past the rounds limit only --again reviews", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		f.writeConfig("---\nrounds: 1\n---\n")
		id, _ := f.working("Search box")
		f.report(1, 0, 0)
		f.ok("review", id)
		wantIn(t, f.refused("review", id), "only they pass --again")
		f.report(0, 0, 0)
		f.ok("review", id, "--again")
		wantField(t, f.header(id), "review", 2)
	})

	t.Run("the next round ends the last round's reviewer", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, _ := f.working("Search box")
		f.report(1, 0, 0)
		f.ok("review", id)
		if hasCall(f.calls("hc.log"), "kill") {
			t.Fatal("the first round killed a terminal")
		}
		f.fact("ls.json", map[string]any{"terminals": []any{map[string]any{
			"address": "t10", "state": "live",
			"labels": map[string]any{"herdr.role": "reviewer", "herdr.task": id},
		}}})
		f.ok("review", id)
		if !hasCall(f.calls("hc.log"), "kill", "t10") {
			t.Errorf("no kill: %v", f.calls("hc.log"))
		}
	})

	t.Run("round 2 is ready and accepts", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, worktree := f.working("Search box")
		f.report(1, 0, 0)
		f.ok("review", id)
		f.commit(worktree, "fix.txt", "fixed\n")
		f.report(0, 0, 2)
		f.ok("review", id)
		f.ok("ready", id, "--summary", "A search box", "--proof", "art-1")
		head := f.header(id)
		wantField(t, head, "status", "ready")
		wantField(t, head, "summary", "A search box")
		wantField(t, head, "proof", "art-1")
		branchHead := f.head(worktree)
		f.ok("accept", id, "--yes")
		head = f.header(id)
		wantField(t, head, "status", "done")
		wantField(t, head, "merged", f.head(f.repo))
		f.gitAt(f.repo, "merge-base", "--is-ancestor", branchHead, "main")
		if exists(worktree) {
			t.Error("the worktree was kept")
		}
		if branches := f.gitAt(f.repo, "branch", "--list", fmt.Sprint(head["branch"])); branches != "" {
			t.Errorf("the branch was kept: %q", branches)
		}
	})
}

func TestAccept(t *testing.T) {
	t.Run("it asks first unless --yes", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, _ := f.readyTask("Search box")
		got := f.shp("n\n", "accept", id)
		if got.code != 1 {
			t.Fatalf("exit %d: %s", got.code, got.stderr)
		}
		wantIn(t, got.stdout, "Merge "+id)
		wantIn(t, got.stderr, "was not accepted")
		wantField(t, f.header(id), "status", "ready")
		f.okIn("y\n", "accept", id)
		wantField(t, f.header(id), "status", "done")
	})

	t.Run("uncommitted changes in the worktree keep it ready", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, worktree := f.readyTask("Search box")
		if err := os.WriteFile(filepath.Join(worktree, "feature.txt"), []byte("edited\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		wantIn(t, f.refused("accept", id, "--yes"), "uncommitted changes")
		wantField(t, f.header(id), "status", "ready")
	})

	t.Run("an untracked file in the worktree keeps it ready", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, worktree := f.readyTask("Search box")
		if err := os.WriteFile(filepath.Join(worktree, "notes.txt"), []byte("stray\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		wantIn(t, f.refused("accept", id, "--yes"), "uncommitted changes")
		wantField(t, f.header(id), "status", "ready")
	})

	t.Run("a main checkout on another branch refuses", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, _ := f.readyTask("Search box")
		f.gitAt(f.repo, "checkout", "-q", "-b", "elsewhere")
		wantIn(t, f.refused("accept", id, "--yes"), "is on elsewhere, not main")
		wantField(t, f.header(id), "status", "ready")
	})

	t.Run("a dirty main checkout refuses", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, _ := f.readyTask("Search box")
		if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("edited\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		wantIn(t, f.refused("accept", id, "--yes"), "commit or stash them")
		wantField(t, f.header(id), "status", "ready")
	})

	t.Run("a conflict stays ready and names its files", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, worktree := f.working("Search box")
		f.commit(worktree, "README.md", "from the branch\n")
		f.report(0, 0, 0)
		f.ok("review", id)
		f.ok("ready", id)
		f.commit(f.repo, "README.md", "from main\n")
		wantIn(t, f.refused("accept", id, "--yes"), "conflicts with main in README.md")
		wantField(t, f.header(id), "status", "ready")
		if status := f.gitAt(f.repo, "status", "--porcelain"); status != "" {
			t.Errorf("the merge was not aborted: %q", status)
		}
		if !exists(worktree) {
			t.Error("the worktree went")
		}
	})

	t.Run("failing checks keep it ready with their output", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work", "--checks", `[["sh", "-c", "echo tests failed; exit 1"]]`)
		id, _ := f.readyTask("Search box")
		got := f.refused("accept", id, "--yes")
		wantIn(t, got, "check sh -c")
		wantIn(t, got, "tests failed")
		wantField(t, f.header(id), "status", "ready")
	})

	t.Run("a commit after the round returns it to working", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, worktree := f.readyTask("Search box")
		f.commit(worktree, "later.txt", "later\n")
		wantIn(t, f.refused("accept", id, "--yes"), "it is working again")
		wantField(t, f.header(id), "status", "working")
	})

	t.Run("a commit made during accept stops it", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work", "--checks", `[["sh", "-c", "echo late > late.txt && git add late.txt && git commit -qm late"]]`)
		id, _ := f.readyTask("Search box")
		main := f.head(f.repo)
		wantIn(t, f.refused("accept", id, "--yes"), "it is working again")
		wantField(t, f.header(id), "status", "working")
		if f.head(f.repo) != main {
			t.Error("main moved")
		}
	})

	t.Run("a task dropped during accept is not merged", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work", "--checks", fmt.Sprintf(`[[%q, "done", "WORK-1"]]`, shpBin))
		id, worktree := f.readyTask("Search box")
		main := f.head(f.repo)
		wantIn(t, f.refused("accept", id, "--yes"), "changed while it was being accepted (now done)")
		if f.head(f.repo) != main {
			t.Error("main moved")
		}
		if !exists(worktree) {
			t.Error("the worktree went")
		}
	})

	t.Run("a merge kills the task's terminals", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, _ := f.readyTask("Search box")
		f.worker(id)
		f.ok("accept", id, "--yes")
		if !hasCall(f.calls("hc.log"), "kill", "t9") {
			t.Errorf("no kill: %v", f.calls("hc.log"))
		}
	})
}

// prSpace is a pr-merge space whose repo pushes to a bare origin.
func (f *fixture) prSpace() string {
	f.t.Helper()
	origin := filepath.Join(f.root, "origin.git")
	f.gitAt(f.root, "init", "-q", "--bare", origin)
	f.gitAt(f.repo, "remote", "add", "origin", origin)
	f.makeSpace("work", "--merge", "pr")
	return origin
}

func TestPullRequest(t *testing.T) {
	t.Run("accept pushes, opens a pull request and sets merging", func(t *testing.T) {
		f := newFixture(t)
		origin := f.prSpace()
		id, worktree := f.readyTask("Search box")
		f.ok("accept", id, "--yes")
		head := f.header(id)
		wantField(t, head, "status", "merging")
		wantField(t, head, "pr", "https://github.com/example/app/pull/7")
		if got := strings.TrimSpace(f.gitAt(origin, "rev-parse", fmt.Sprint(head["branch"]))); got != f.head(worktree) {
			t.Errorf("origin has %s", got)
		}
		calls := f.calls("gh.log")
		var create []string
		for _, call := range calls {
			if call[1] == "create" {
				create = call
			}
		}
		wantIn(t, strings.Join(create, " "), "--base main")
		wantIn(t, strings.Join(create, " "), "It works")
		wantIn(t, strings.Join(create, " "), "Task: "+id)
		if !hasCall(calls, "pr", "merge", "--auto", "--squash") {
			t.Errorf("no auto merge: %v", calls)
		}
	})

	t.Run("an open pull request is reused", func(t *testing.T) {
		f := newFixture(t)
		f.prSpace()
		id, _ := f.readyTask("Search box")
		f.fact("gh_view.json", map[string]any{"url": "https://github.com/example/app/pull/3", "state": "OPEN"})
		f.ok("accept", id, "--yes")
		wantField(t, f.header(id), "pr", "https://github.com/example/app/pull/3")
		if hasCall(f.calls("gh.log"), "pr", "create") {
			t.Error("a second pull request was opened")
		}
	})

	t.Run("one accept per space", func(t *testing.T) {
		f := newFixture(t)
		f.prSpace()
		first, _ := f.readyTask("First")
		second, _ := f.readyTask("Second")
		f.ok("accept", first, "--yes")
		wantIn(t, f.refused("accept", second, "--yes"), first+" is merging")
	})

	tick := []struct {
		name   string
		view   map[string]any
		status string
		step   string
	}{
		{"a merged pull request is done", map[string]any{"state": "MERGED", "mergeCommit": map[string]any{"oid": "abc1234"}}, "done", ""},
		{"a closed pull request is ready again", map[string]any{"state": "CLOSED"}, "ready", "its pull request was closed; accept again for a new one"},
		{"a conflicting pull request is ready again", map[string]any{"state": "OPEN", "mergeable": "CONFLICTING"}, "ready", "its pull request conflicts with main; reopen, merge main into its branch, commit and review again"},
		{"a pending pull request stays merging", map[string]any{"state": "OPEN", "mergeable": "MERGEABLE", "mergeStateStatus": "BLOCKED"}, "merging", ""},
	}
	for _, c := range tick {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.prSpace()
			id, worktree := f.readyTask("Search box")
			f.ok("accept", id, "--yes")
			f.fact("gh_view.json", c.view)
			f.ok("tick")
			head := f.header(id)
			wantField(t, head, "status", c.status)
			if c.step != "" {
				wantField(t, head, "step", c.step)
			}
			if c.status == "done" {
				wantField(t, head, "merged", "abc1234")
				if exists(worktree) {
					t.Error("the worktree was kept")
				}
			}
		})
	}

	t.Run("tick merges a green pull request GitHub would not auto-merge", func(t *testing.T) {
		f := newFixture(t)
		f.prSpace()
		f.fact("gh_merge_fails", true)
		id, _ := f.readyTask("Search box")
		f.ok("accept", id, "--yes")
		if err := os.Remove(filepath.Join(f.state, "gh_merge_fails")); err != nil {
			t.Fatal(err)
		}
		f.fact("gh_view.json", map[string]any{"state": "OPEN", "mergeable": "MERGEABLE", "mergeStateStatus": "CLEAN"})
		f.ok("tick")
		calls := f.calls("gh.log")
		if !hasCall(calls[len(calls)-1:], "pr", "merge", "--squash", "https://github.com/example/app/pull/7") {
			t.Errorf("no merge: %v", calls)
		}
	})
}

func TestDoneAndReopen(t *testing.T) {
	t.Run("done keeps the branch and worktree and kills terminals", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, worktree := f.working("Search box")
		f.worker(id)
		f.ok("done", id)
		wantField(t, f.header(id), "status", "done")
		if !exists(worktree) {
			t.Error("the worktree went")
		}
		if !hasCall(f.calls("hc.log"), "kill", "t9") {
			t.Errorf("no kill: %v", f.calls("hc.log"))
		}
		wantIn(t, f.refused("done", id), "done already")
	})

	t.Run("a merging task is not dropped", func(t *testing.T) {
		f := newFixture(t)
		f.prSpace()
		id, _ := f.readyTask("Search box")
		f.ok("accept", id, "--yes")
		wantIn(t, f.refused("done", id), "is merging")
	})

	t.Run("reopen sends a ready task back to work", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, _ := f.readyTask("Search box")
		f.ok("reopen", id)
		head := f.header(id)
		wantField(t, head, "status", "working")
		wantField(t, head, "reviewed_commit", nil)
		wantIn(t, f.refused("ready", id), "round 1 of "+id+" is stale")
	})

	t.Run("reopen refuses a ready task with uncommitted changes", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, worktree := f.readyTask("Search box")
		if err := os.WriteFile(filepath.Join(worktree, "feature.txt"), []byte("edited\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		wantIn(t, f.refused("reopen", id), "uncommitted changes")
		wantField(t, f.header(id), "status", "ready")
	})

	t.Run("reopen reuses a done task's worktree", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, worktree := f.working("Search box")
		branch := fmt.Sprint(f.header(id)["branch"])
		f.ok("done", id)
		f.ok("reopen", id)
		head := f.header(id)
		wantField(t, head, "status", "working")
		wantField(t, head, "branch", branch)
		if got := f.worktree(id); got != worktree {
			t.Errorf("worktree %s", got)
		}
		if countLaunches(f.calls("app.log")) != 2 {
			t.Errorf("launches: %v", f.calls("app.log"))
		}
	})

	t.Run("reopen puts a done task's branch back in a worktree", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, worktree := f.working("Search box")
		f.ok("done", id)
		f.gitAt(f.repo, "worktree", "remove", worktree)
		f.ok("reopen", id)
		if strings.TrimSpace(f.gitAt(worktree, "log", "-1", "--format=%s")) != "change feature.txt" {
			t.Error("the branch's commit is missing")
		}
	})

	t.Run("reopen recovers a done task's worktree deleted by hand", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, worktree := f.working("Search box")
		f.ok("done", id)
		if err := os.RemoveAll(worktree); err != nil {
			t.Fatal(err)
		}
		f.ok("reopen", id)
		if !exists(filepath.Join(worktree, "feature.txt")) {
			t.Error("the branch's work is missing")
		}
	})

	t.Run("reopen starts a merged task fresh from the base", func(t *testing.T) {
		f := newFixture(t)
		f.makeSpace("work")
		id, worktree := f.readyTask("Search box")
		branch := fmt.Sprint(f.header(id)["branch"])
		f.ok("accept", id, "--yes")
		f.ok("reopen", id)
		head := f.header(id)
		wantField(t, head, "status", "working")
		wantField(t, head, "branch", branch+"-2")
		if got := f.worktree(id); got != worktree+"-2" {
			t.Errorf("worktree %s", got)
		}
		if got, want := f.head(worktree+"-2"), f.head(f.repo); got != want {
			t.Errorf("fresh worktree on %s, want %s", got, want)
		}

		f.commit(worktree+"-2", "again.txt", "again\n")
		f.ok("review", id)
		f.ok("ready", id)
		f.ok("accept", id, "--yes")
		f.ok("reopen", id)
		wantField(t, f.header(id), "branch", branch+"-2")
	})
}
