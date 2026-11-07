// The board: every task of every machine linked to herdr-core, grouped by machine and status.
// It reads `shp list --json` every 2 s and runs `shp tick` every 10 s, on each machine apart,
// so a slow one holds up only its own rows. A row opens the task's Herdr space; Accept asks
// first and then runs `shp accept --yes`; a task that needs the person or is ready toasts once.
// A machine without shp, out of reach or not connected gets one quiet line.

import { View, div } from "gpui-kit";
import { v_flex, h_flex } from "gpui-base";
import { hc, spaces, exec, run, notify, openOverlay, closeOverlay, props, theme, shared } from "herdr";
import { group, detail, subtitle, fresh, workspace, linkedMachines, tasksOf } from "./board.js";

const POLL = 2000;  // how often the tasks are read
const TICK = 10000; // how often queued work is started
const ID_FONT = "JetBrains Mono";
const caps = (x) => (shared("layout") === "workbench" ? String(x).toUpperCase() : String(x));
const lastLine = (text) => String(text ?? "").trim().split("\n").pop();
const errorLine = (error) => lastLine(error?.stderr || error?.message || error) || "No shp here, or this machine can't be reached.";

// A small boxed word, in its colour: "2 need you", "1 ready", ACCEPT.
function chip(id, text, color) {
  return h_flex().id(id).h(20).flex_none().px(6).gap(5).items_center().whitespace_nowrap().border_1().border_color(color)
    .text_size(10.5).font_semibold().text_color(color).child(div().size(5).flex_none().bg(color)).child(caps(text));
}

// A section bar as the workbench draws them: the label in a raised block, the count beside it.
function bar(t, label, n, color) {
  return h_flex().w_full().h(22).mt(10).flex_none().items_center().border_b(1).border_color(t.border)
    .child(div().h_full().px(8).flex().items_center().bg(t.raised).text_size(10).font_semibold().text_color(color).child(caps(label)))
    .child(div().px(8).text_size(10).font_semibold().text_color(t.dim).child(String(n)))
    .child(div().flex_1());
}

// A place is one machine's share of the board; name null is this computer.
const newPlace = (name, connected) => ({ name, connected, tasks: [], error: null, read: false, loading: false });

export default class Board extends View {
  init(_props, cx) {
    this.busy = false; // accepting
    this.error = null; // why the accept was refused
    if (props()?.accept) { // this copy is the confirm dialog, not the board
      this.handle = cx.focus_handle();
      this.handle.focus();
      return;
    }
    this.places = [newPlace(null, true)];
    this.seen = {}; // the toast keys of the tasks that need the person or are ready
    this.refresh(cx);
    cx.timer.every(POLL, (cx) => this.refresh(cx));
    cx.timer.every(TICK, () => {
      for (const place of this.places) {
        if (place.read && !place.error) exec("shp", ["tick"], { on: place.name }).catch(() => null);
      }
    });
  }

  // Find the linked machines, then read each one that isn't still answering the last read.
  refresh(cx) {
    cx.spawn(async (cx) => {
      let linked = [];
      try {
        const reply = await hc(["machine", "ls", "--json"]);
        if (reply.code === 0) linked = linkedMachines(reply.stdout);
      } catch {
        // no herdr-core answer: this computer's tasks are still worth showing
      }
      this.places = [{ name: null, connected: true }, ...linked].map(({ name, connected }) =>
        Object.assign(this.places.find((place) => place.name === name) ?? newPlace(name, connected), { connected }));
      for (const place of this.places) this.load(place, cx);
      cx.notify();
    });
  }

  load(place, cx) {
    if (place.loading) return;
    if (!place.connected) {
      Object.assign(place, { tasks: [], error: "Not connected.", read: true });
      return;
    }
    place.loading = true;
    cx.spawn(async (cx) => {
      try {
        const reply = await exec("shp", ["list", "--json"], { on: place.name });
        // shp still prints the tasks it could read when another task file is bad.
        place.tasks = reply.stdout.trim() ? tasksOf(place.name, reply.stdout) : [];
        place.error = reply.code === 0 ? null : lastLine(reply.stderr) || `shp list exited ${reply.code}`;
        if (reply.stdout.trim()) this.toast(place);
      } catch (error) {
        place.tasks = [];
        place.error = errorLine(error);
      } finally {
        place.loading = false;
        place.read = true;
        cx.notify();
      }
    });
  }

  // Toast what newly needs the person or is ready, on this machine; the first read toasts too,
  // so work that got there while the board was closed still says so.
  toast(place) {
    const news = fresh(this.seen, place.name, place.tasks);
    for (const one of news.notices) notify({ title: one.title, body: one.body, level: one.level });
    this.seen = news.seen;
  }

  // The row's Accept: confirm first, and say why if shp refuses.
  ask(task) {
    openOverlay({ mod: "shp-board", kind: "dialog", modal: true, width: 440, height: 200, props: { accept: task } });
  }

  accept(task, cx) {
    if (this.busy) return; // one accept per dialog, however fast the clicks
    this.busy = true;
    this.error = null;
    cx.notify();
    cx.spawn(async (cx) => {
      let refused = null;
      try {
        const reply = await exec("shp", ["accept", task.id, "--yes"], { on: task.machine });
        if (reply.code !== 0) refused = lastLine(reply.stderr) || `shp accept exited ${reply.code}`;
      } catch (error) {
        refused = errorLine(error);
      }
      if (refused) {
        this.busy = false;
        this.error = refused;
        cx.notify();
        return;
      }
      closeOverlay(props().overlay);
    });
  }

  // One machine: its name when there are several (or it has a reason), then a bar per status.
  place(body, entry, t) {
    if (this.places.length > 1 || entry.error) {
      body.child(h_flex().h(26).flex_none().mt(6).px(8).gap(8).items_center()
        .child(div().text_size(10.5).font_semibold().text_color(t.muted).child(caps(entry.name ?? "This computer")))
        .child(div().flex_1())
        .when(!entry.error, (e) => e.child(div().text_size(10.5).text_color(t.dim).child(String(entry.tasks.length)))));
    }
    if (entry.error) body.child(div().px(9).py(6).truncate().text_size(11).text_color(t.dim).child(entry.error));
    for (const section of group(entry.tasks)) {
      const color = t[section.color] ?? t.dim;
      body.child(bar(t, section.label, section.tasks.length, color));
      for (const task of section.tasks) body.child(this.row(task, t));
    }
  }

  row(task, t) {
    const space = workspace(task, spaces());
    const info = detail(task);
    const under = subtitle(task);
    const open = () => run("space.select", { id: space.id });
    return h_flex().id(`task-${task.machine ?? "here"}-${task.id}`).w_full().flex_none().px(8).py(5).gap(10).items_center()
      .when(Boolean(space), (e) => e.cursor_pointer().hover((s) => s.bg(t.hover)).on_click(open))
      .child(v_flex().flex_1().min_w(0).gap(2)
        .child(h_flex().min_w(0).gap(8).items_center()
          .child(div().flex_none().w(64).font_family(ID_FONT).text_size(11.5).text_color(task.status === "needs" ? t.blocked : t.dim).child(String(task.id)))
          .child(div().flex_1().min_w(0).truncate().child(String(task.title ?? ""))))
        .when(Boolean(under), (e) => e.child(div().pl(72).min_w(0).truncate().text_size(11).text_color(t.muted).child(under))))
      .when(Boolean(info), (e) => e.child(div().flex_none().text_size(11).text_color(t.dim).child(info)))
      .when(task.status === "ready", (e) => e.child(chip(`accept-${task.machine ?? "here"}-${task.id}`, "Accept", t.accent)
        .cursor_pointer().hover((s) => s.bg(t.accent).text_color(t.accent_foreground)).on_mouse_down("left", (ev, cx) => cx.stop_propagation())
        .on_click((ev, cx) => { cx.stop_propagation(); this.ask(task); })));
  }

  // The confirm dialog before accepting, as px-confirm draws it: Enter accepts, Esc cancels.
  confirm(task, t) {
    const cancel = () => closeOverlay(props().overlay);
    const button = (id, label, bg, fg, border, onClick) => h_flex().id(id).h(32).px(14).items_center().border_1()
      .border_color(border).bg(bg).text_color(fg).cursor_pointer().hover((s) => s.opacity(0.88)).on_click(onClick).child(caps(label));
    return v_flex().id("shp-board-confirm").track_focus(this.handle).size_full().min_h(0).p(18).gap(12).bg(t.popover)
      .text_color(t.foreground).text_size(13)
      .on_key_down((ev, cx) => {
        if (ev.key === "enter") this.accept(task, cx);
        else if (ev.key === "escape") cancel();
        else return;
        cx.stop_propagation();
      })
      .child(h_flex().items_center()
        .child(div().flex_1().text_size(15).font_semibold().child(`Accept ${task.id}?`))
        .child(div().text_size(11).text_color(t.dim).child("Esc")))
      .child(div().flex_1().min_h(0).text_color(t.muted).child(`${task.title} — runs its space's checks, then merges its branch or pull request.`))
      .when(Boolean(this.error), (e) => e.child(div().text_size(11).text_color(t.blocked).child(this.error)))
      .child(h_flex().justify_end().gap(8).text_size(12)
        .child(button("no", "Cancel", t.raised, t.foreground, t.input, cancel))
        .child(button("yes", this.busy ? "Accepting…" : "Accept", t.solid, t.solid_foreground, t.solid, (ev, cx) => this.accept(task, cx))));
  }

  render() {
    const t = theme();
    const accepting = props()?.accept;
    if (accepting) return this.confirm(accepting, t);
    const tasks = this.places.flatMap((place) => place.tasks);
    const needs = tasks.filter((task) => task.status === "needs").length;
    const ready = tasks.filter((task) => task.status === "ready").length;
    const body = v_flex().id("shp-board-list").flex_1().min_h(0).overflow_y_scrollbar().px(8).pb(10);
    for (const place of this.places) this.place(body, place, t);
    if (!tasks.length && this.places.every((place) => !place.error)) {
      const read = this.places.every((place) => place.read);
      body.child(div().px(2).py(10).text_size(11).text_color(t.dim).child(read ? "No tasks yet." : "Reading tasks…"));
    }
    return v_flex().id("shp-board").size_full().min_h(0).rounded(0).overflow_hidden().bg(t.surface).border_1().border_color(t.border)
      .text_color(t.foreground).text_size(13)
      .child(h_flex().h(42).flex_none().px(14).gap(8).items_center().border_b(1).border_color(t.border)
        .child(div().font_semibold().child(caps("Board")))
        .child(div().flex_1())
        .when(needs > 0, (e) => e.child(chip("shp-needs", `${needs} need you`, t.blocked)))
        .when(ready > 0, (e) => e.child(chip("shp-ready", `${ready} ready`, t.done)))
        .child(div().text_size(11).text_color(t.dim).child(`${tasks.length} ${tasks.length === 1 ? "task" : "tasks"}`)))
      .child(body);
  }
}
