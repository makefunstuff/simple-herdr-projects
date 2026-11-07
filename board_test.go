package main

// The board's tests: its data rules run under node, and herdr-app checks the mod loads.
// The panel's clicks need a pointer, so the rules behind them are what these tests hold.

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// boardJS runs a snippet with the board's rules as `board`; the snippet asserts with ok/eq.
func boardJS(t *testing.T, body string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; the board's rules are JavaScript")
	}
	rules, err := filepath.Abs("mods/shp-board/board.js")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`import * as board from %q;
const ok = (yes, why) => { if (!yes) { console.error(why); process.exit(1); } };
const eq = (got, want, what) => ok(JSON.stringify(got) === JSON.stringify(want),
  what + ": got " + JSON.stringify(got) + ", want " + JSON.stringify(want));
%s`, "file://"+filepath.ToSlash(rules), body)
	out, err := exec.Command(node, "--input-type=module", "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("board rules: %v\n%s", err, out)
	}
}

func TestBoardGroupsTasks(t *testing.T) {
	boardJS(t, `
const tasks = [
  { id: "A-1", status: "needs" },
  { id: "A-2" },
  { id: "A-3", status: "paused" },
  { id: "A-4", status: "working" },
  { id: "A-5", status: "merging" },
  { id: "A-6", status: "ready" },
  { id: "A-7", status: "todo" },
  { id: "A-8", status: "queued" },
  { id: "A-9", status: "done" },
];
const sections = board.group(tasks);
eq(sections.map((s) => s.key), ["needs", "working", "ready", "backlog", "done", "other"], "section order");
eq(sections.map((s) => s.label), ["Needs you", "Working", "Ready", "Backlog", "Done", "Other"], "section labels");
eq(sections.map((s) => s.tasks.map((t) => t.id)),
  [["A-1"], ["A-4", "A-5"], ["A-6"], ["A-2", "A-7", "A-8"], ["A-9"], ["A-3"]], "tasks by status");
eq(board.group([]), [], "no tasks, no sections");
`)
}

func TestBoardRowWords(t *testing.T) {
	boardJS(t, `
eq(board.detail({ progress: 40, left: "20m" }), "40% · 20m left", "progress and left");
eq(board.detail({ progress: 0 }), "0%", "zero progress shows");
eq(board.detail({}), "", "nothing to say");
eq(board.subtitle({ status: "needs", asks: [{ text: "Which port?" }], step: "Running tests" }), "Which port?", "the open question");
eq(board.subtitle({ status: "working", asks: [{ text: "Which port?" }], step: "Running tests" }), "Running tests", "else the step");
eq(board.subtitle({ status: "needs", asks: [] }), "", "no question, nothing");
`)
}

func TestBoardNoticesOnce(t *testing.T) {
	boardJS(t, `
const need = board.notice({ id: "A-1", status: "needs", title: "Fix login", asks: [{ text: "Which port?" }], step: "Waiting" });
eq(need.key, "/A-1:needs", "the key");
eq(need.title, "A-1 needs you", "the title");
eq(need.body, "Which port?", "the question");
eq(need.level, "warning", "needs you is a warning");
const ready = board.notice({ id: "B-2", status: "ready", machine: "mac", title: "Ship it", step: "Done" });
eq(ready.key, "mac/B-2:ready", "a remote key names its machine");
eq(ready.title, "B-2 on mac is ready", "a remote title names its machine");
eq(ready.body, "Done", "a ready task says its step");
eq(ready.level, "success", "ready is a success");
eq(board.notice({ id: "A-3", status: "working" }), null, "working does not toast");

const first = board.fresh({}, [{ id: "A-1", status: "ready", title: "Fix" }]);
eq(first.notices.length, 1, "first sighting toasts");
eq(first.seen, { "/A-1:ready": true }, "the key is now seen");
const again = board.fresh(first.seen, [{ id: "A-1", status: "ready", title: "Fix" }]);
eq(again.notices.length, 0, "staying ready does not toast again");
const left = board.fresh(again.seen, [{ id: "A-1", status: "working" }]);
eq(left.seen, {}, "leaving ready forgets the key");
const back = board.fresh(left.seen, [{ id: "A-1", status: "ready", title: "Fix" }]);
eq(back.notices.length, 1, "becoming ready again toasts");

const both = board.fresh({}, [
  { id: "A-1", status: "ready", machine: "mac", title: "One" },
  { id: "A-1", status: "ready", title: "Two" },
]);
eq(both.notices.length, 2, "the same id on two machines toasts twice");

const remote = [{ id: "R-1", status: "needs", machine: "mac", title: "Remote", asks: [{ text: "Which?" }] }];
const seenRemote = board.fresh({}, remote).seen;
const failed = board.fresh(seenRemote, [], ["mac"]);
eq(failed.notices.length, 0, "a failed read toasts nothing");
eq(failed.seen, seenRemote, "a failed machine keeps its seen keys");
const recovered = board.fresh(failed.seen, remote, []);
eq(recovered.notices.length, 0, "recovery does not toast an unchanged task again");
const gone = board.fresh(recovered.seen, [], []);
eq(gone.seen, {}, "a machine that answers drops the tasks that are gone");
eq(board.fresh({ "/A-1:needs": true, "mac/B-2:ready": true }, [], [null]).seen,
  { "/A-1:needs": true }, "a failed local read keeps only local keys");
`)
}

func TestBoardReadsList(t *testing.T) {
	boardJS(t, `
const tasks = board.tasksOf("mac", '[{"id":"APP-1","title":"Fix login","status":"working","progress":40,"step":"Running tests","left":"20m","asks":[]}]');
eq(tasks.length, 1, "one task");
eq(tasks[0].machine, "mac", "the machine it came from is added");
eq(tasks[0].progress, 40, "the header's values stay");
eq(board.tasksOf(null, "[]"), [], "an empty store is an empty list");
eq(board.tasksOf(null, '[{"id":"A-1"}]')[0].machine, null, "this computer is null");
let threw = false;
try { board.tasksOf(null, "{}"); } catch { threw = true; }
ok(threw, "a listing that is not a list is refused");
`)
}

func TestBoardCommands(t *testing.T) {
	boardJS(t, `
eq(board.listCall(null), { command: "shp", args: ["list", "--json"], on: null }, "list here");
eq(board.listCall("mac"), { command: "shp", args: ["list", "--json"], on: "mac" }, "list on a machine");
eq(board.tickCall("mac"), { command: "shp", args: ["tick"], on: "mac" }, "tick on a machine");
eq(board.acceptCall({ id: "APP-1" }), { command: "shp", args: ["accept", "APP-1", "--yes"], on: null }, "accept here");
eq(board.acceptCall({ id: "APP-1", machine: "mac" }), { command: "shp", args: ["accept", "APP-1", "--yes"], on: "mac" }, "accept on a machine");
`)
}

func TestBoardOpensWorkspace(t *testing.T) {
	boardJS(t, `
const spaces = [
  { id: "s1", name: "APP-1 · Fix login", machine: null },
  { id: "mac/s2", name: "APP-2 · Ship it", machine: "mac" },
  { id: "s3", name: "APP-1 · Fix login", machine: "mac" },
];
eq(board.workspace({ id: "APP-1", title: "Fix login" }, spaces).id, "s1", "the space on this computer");
eq(board.workspace({ id: "APP-2", title: "Ship it", machine: "mac" }, spaces).id, "mac/s2", "the space on its machine");
eq(board.workspace({ id: "APP-2", title: "Ship it" }, spaces), null, "not another machine's space");
eq(board.workspace({ id: "APP-1", title: "Renamed", workspace: "s1" }, spaces).id, "s1", "the recorded workspace id");
eq(board.workspace({ id: "APP-2", title: "Renamed", machine: "mac", workspace: "s2" }, spaces).id, "mac/s2", "a recorded id on a machine");
eq(board.workspace({ id: "APP-9", title: "Not started" }, spaces), null, "no space, nothing to open");
`)
}

func TestBoardModChecks(t *testing.T) {
	herdrApp, err := exec.LookPath("herdr-app")
	if err != nil {
		t.Skip("herdr-app is not installed")
	}
	folder, err := filepath.Abs("mods/shp-board")
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(herdrApp, "check", folder).CombinedOutput()
	if err != nil {
		t.Fatalf("herdr-app check: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "check passed") {
		t.Fatalf("herdr-app check: %s", out)
	}
}
