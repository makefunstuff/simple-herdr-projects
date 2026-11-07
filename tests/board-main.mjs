// The panel's handlers, driven with fake Herdr modules (tests/fake): the data rules run in
// board_test.go's node snippets, and this holds what those cannot: that Accept asks first, runs
// one accept at a time, says why it was refused, closes on success, and that its click does not
// open the row's space. Run: node --import tests/fake/hooks.mjs tests/board-main.mjs [mod folder]

import { state, calls, theme } from "herdr";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const folder = process.argv[2] ?? "mods/shp-board";
const { default: Board } = await import(pathToFileURL(join(folder, "main.js")).href);

let failed = 0;
const ok = (yes, why) => {
  if (yes) return;
  console.error("FAIL:", why);
  failed++;
};
const eq = (got, want, what) =>
  ok(JSON.stringify(got) === JSON.stringify(want), `${what}: got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`);

const reset = () => {
  state.props = {};
  state.spaces = [];
  state.machines = '{"machines":[]}';
  state.exec = async () => ({ code: 0, stdout: "[]", stderr: "" });
  for (const list of Object.values(calls)) list.length = 0;
};

const fakeCx = () => ({
  pending: [],
  notified: 0,
  notify() { this.notified++; },
  spawn(fn) { this.pending.push(Promise.resolve().then(() => fn(this))); },
  timer: { every() {}, after() {} },
  focus_handle: () => ({ focus() {} }),
  stop_propagation() {},
});
const settle = async (cx) => {
  await Promise.all(cx.pending);
  cx.pending.length = 0;
};

// The panel, started as Herdr starts it.
const startBoard = async () => {
  const cx = fakeCx();
  const board = Object.create(Board.prototype);
  Board.prototype.init.call(board, {}, cx);
  await settle(cx);
  return { board, cx };
};

// A copy opened as the confirm dialog: props {accept, overlay}.
const startDialog = (task) => {
  state.props = { accept: task, overlay: "ov1" };
  const board = Object.create(Board.prototype);
  Board.prototype.init.call(board, {}, fakeCx());
  return board;
};

const walk = (el) => {
  const out = [el];
  for (const [key, list] of Object.entries(el.calls)) {
    if (key !== "child" && key !== "children") continue;
    for (const args of list) {
      for (const arg of args) {
        if (arg && typeof arg === "object" && arg.calls) out.push(...walk(arg));
        else if (Array.isArray(arg)) for (const one of arg) if (one && one.calls) out.push(...walk(one));
      }
    }
  }
  return out;
};
const find = (el, id) => walk(el).find((one) => one.calls.id?.[0]?.[0] === id);
const texts = (el) => walk(el).flatMap((one) => (one.calls.child ?? []).flat()).filter((x) => typeof x === "string");

const ready = { id: "A-1", title: "Fix login", status: "ready" };

// Every linked machine is read with --on, temporary sandboxes aside; the first read is what
// tells the person a task needs them or is ready.
reset();
state.machines = JSON.stringify({ machines: [{ name: "mac", target: "mac" }, { name: "sandbox", target: "sandbox", temporary: true }] });
state.exec = async (_command, _args, options) => options?.on === "mac"
  ? { code: 0, stdout: JSON.stringify([{ id: "M-1", title: "On the mac", status: "ready" }]), stderr: "" }
  : { code: 0, stdout: JSON.stringify([{ id: "A-1", title: "Here", status: "needs", asks: [{ id: "q", text: "Which port?", time: "t" }] }]), stderr: "" };
const first = await startBoard();
eq(calls.hc, [["machine", "ls", "--json"]], "linked machines come from hc machine ls");
eq(first.board.places.map((p) => p.name), [null, "mac"], "this computer and its linked machines");
eq(calls.exec.map((c) => c.on), [null, "mac"], "each read goes to its machine");
eq(calls.notices.length, 2, "needs you and ready toast on the first read");
eq(calls.notices.map((n) => n.title).sort(), ["A-1 needs you", "M-1 on mac is ready"], "the toast titles");
eq(calls.notices.find((n) => n.title.startsWith("A-1"))?.body, "Which port?", "a need says its question");
ok(texts(first.board.render()).includes("Needs you"), "the panel lists the Needs you section");

// Nothing new: no second toast.
calls.notices.length = 0;
first.board.refresh(first.cx);
await settle(first.cx);
eq(calls.notices.length, 0, "an unchanged task does not toast again");

// A machine out of reach keeps the keys of what it had: its tasks do not toast again when
// it answers.
state.exec = async () => ({ code: 1, stdout: "", stderr: "no shp on mac\n" });
first.board.refresh(first.cx);
await settle(first.cx);
eq(first.board.places[1].error, "no shp on mac", "the failed machine says one line");
eq(calls.notices.length, 0, "a failed read toasts nothing");
state.exec = async (_command, _args, options) => options?.on === "mac"
  ? { code: 0, stdout: JSON.stringify([{ id: "M-1", title: "On the mac", status: "ready" }]), stderr: "" }
  : { code: 0, stdout: JSON.stringify([{ id: "A-1", title: "Here", status: "needs", asks: [{ id: "q", text: "Which port?", time: "t" }] }]), stderr: "" };
first.board.refresh(first.cx);
await settle(first.cx);
eq(calls.notices.length, 0, "recovery does not toast the same tasks again");

// Accept asks first and runs nothing until the person says yes.
reset();
const { board } = await startBoard();
calls.exec.length = 0;
board.ask(ready);
eq(calls.overlays.length, 1, "Accept opens the confirm dialog");
eq(calls.overlays[0].kind, "dialog", "as a dialog");
eq(calls.overlays[0].props.accept, ready, "about that task");
eq(calls.exec.length, 0, "and runs no shp before the person confirms");

// One accept at a time, closed once it succeeds.
const dialog = startDialog(ready);
const releases = [];
state.exec = () => new Promise((done) => { releases.push(done); });
const accepting = fakeCx();
calls.exec.length = 0;
dialog.accept(ready, accepting);
dialog.accept(ready, accepting);
await new Promise((done) => setTimeout(done, 0));
eq(calls.exec.length, 1, "a second click runs no second accept");
eq(dialog.busy, true, "accepting while it runs");
for (const release of releases) release({ code: 0, stdout: "", stderr: "" });
await settle(accepting);
eq(calls.closed, ["ov1"], "closes its dialog on success");

// A refusal frees the button and says why, and leaves the dialog open.
reset();
state.exec = async () => ({ code: 1, stdout: "", stderr: "needs a review round\n" });
const refused = startDialog(ready);
const refusedCx = fakeCx();
refused.accept(ready, refusedCx);
await settle(refusedCx);
eq(refused.busy, false, "a refusal frees the button");
eq(refused.error, "needs a review round", "and shows the reason");
eq(calls.closed.length, 0, "the dialog stays open to say it");

// A machine that cannot be reached says so instead of throwing.
reset();
state.exec = async () => { throw new Error("mac is unreachable"); };
const unreachable = startDialog(ready);
const unreachableCx = fakeCx();
unreachable.accept(ready, unreachableCx);
await settle(unreachableCx);
eq(unreachable.busy, false, "an unreachable machine frees the button");
eq(unreachable.error, "mac is unreachable", "and shows the reason");

// A started task's row opens its space; Accept stops the click first.
reset();
state.spaces = [{ id: "s1", name: "A-1 · Fix login", machine: null }];
const { board: rows } = await startBoard();
const row = rows.row(ready, theme());
const open = row.calls.on_click?.[0]?.[0];
ok(Boolean(open), "a started task's row opens its space");
open({}, fakeCx());
eq(calls.runs, [{ name: "space.select", args: { id: "s1" } }], "selects that space");
const accept = find(row, "accept-here-A-1");
ok(Boolean(accept), "a ready row has Accept");
let stopped = false;
accept.calls.on_click[0][0]({}, { stop_propagation() { stopped = true; } });
ok(stopped, "Accept stops the click before the row opens its space");
eq(calls.runs.length, 1, "so Accept only opens the dialog");
eq(calls.overlays.at(-1).props.accept, ready, "the dialog is about that task");

// Nothing to open: the row is not clickable.
state.spaces = [];
const bare = rows.row({ id: "Z-9", title: "Not started", status: "ready" }, theme());
ok(!bare.calls.on_click, "a row with no space has no click");

// The dialog's button works while idle and takes no click while accepting.
const idle = startDialog(ready);
ok(Boolean(find(idle.render(), "yes").calls.on_click?.[0]?.[0]), "the dialog's Accept works while idle");
idle.busy = true;
ok(!find(idle.render(), "yes").calls.on_click, "and takes no click while accepting");

// The keyboard works as in px-confirm: Esc cancels, Enter accepts.
reset();
const keyed = startDialog(ready);
const keys = keyed.render().calls.on_key_down[0][0];
const keyCx = fakeCx();
keys({ key: "escape" }, keyCx);
eq(calls.closed, ["ov1"], "Esc closes the dialog");
eq(calls.exec.length, 0, "and accepts nothing");
keys({ key: "enter" }, keyCx);
await settle(keyCx);
eq(calls.exec.map((c) => c.args), [["accept", "A-1", "--yes"]], "Enter accepts");

if (failed) process.exit(1);
console.log("board panel handlers hold");
