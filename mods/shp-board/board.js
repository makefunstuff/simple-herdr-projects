// The board's rules, apart from Herdr so node can test them: how tasks group, what a row
// says, when a task earns a toast, and which machines to read.

export const SECTIONS = [
  { key: "needs", label: "Needs you", color: "blocked", statuses: ["needs"] },
  { key: "working", label: "Working", color: "working", statuses: ["working", "merging"] },
  { key: "ready", label: "Ready", color: "done", statuses: ["ready"] },
  { key: "backlog", label: "Backlog", color: "muted", statuses: ["todo", "queued"] },
  { key: "done", label: "Done", color: "dim", statuses: ["done"] },
  { key: "other", label: "Other", color: "dim", statuses: [] },
];

// The design's words for the statuses that share a section with another.
const WORDS = { todo: "To do", queued: "Queued", merging: "Merging" };

// group puts each task under the one section for its status; unknown statuses stay visible
// under Other, and empty sections are left out.
export function group(tasks) {
  const sections = SECTIONS.map((section) => ({ ...section, tasks: [] }));
  const other = sections[sections.length - 1];
  for (const task of tasks) {
    const status = String(task.status ?? "todo");
    (sections.find((section) => section.statuses.includes(status)) ?? other).tasks.push(task);
  }
  return sections.filter((section) => section.tasks.length > 0);
}

// detail is a row's right-hand words: a status its section doesn't say, how far the work
// is and what is left.
export function detail(task) {
  const parts = [];
  const word = WORDS[task.status ?? "todo"];
  if (word) parts.push(word);
  if (task.progress != null) parts.push(`${task.progress}%`);
  if (task.left) parts.push(`${task.left} left`);
  return parts.join(" · ");
}

// subtitle is a row's second line: the open question, else the worker's step.
export function subtitle(task) {
  const ask = task.asks?.[0];
  if (task.status === "needs" && ask?.text) return String(ask.text);
  return task.step ? String(task.step) : "";
}

// notice is the one toast a task earns when it needs the person or is ready. A remote
// task names its machine, as the chat does.
export function notice(task) {
  const status = String(task.status ?? "");
  if (status !== "needs" && status !== "ready") return null;
  const who = task.machine ? `${task.id} on ${task.machine}` : String(task.id);
  return {
    key: `${task.machine ?? ""}/${task.id}:${status}`,
    title: `${who} ${status === "ready" ? "is ready" : "needs you"}`,
    body: subtitle(task) || String(task.title ?? ""),
    level: status === "ready" ? "success" : "warning",
  };
}

// fresh takes one machine's new listing and returns the notices not seen before, and the
// seen keys with that machine's replaced: a task keeps its key while it stays needs or
// ready, so it toasts once. Other machines' keys stay, so a machine that fails a read and
// comes back does not toast its unchanged tasks again.
export function fresh(seen, machine, tasks) {
  const prefix = `${machine ?? ""}/`;
  const now = Object.fromEntries(Object.keys(seen).filter((key) => !key.startsWith(prefix)).map((key) => [key, true]));
  const notices = [];
  for (const task of tasks) {
    const one = notice(task);
    if (!one) continue;
    now[one.key] = true;
    if (!seen[one.key]) notices.push(one);
  }
  return { notices, seen: now };
}

// linkedMachines lists every machine herdr-core knows, from `hc machine ls --json`, apart
// from the temporary sandboxes agents add for themselves.
export function linkedMachines(text) {
  const reply = JSON.parse(text);
  return (reply.machines ?? []).filter((machine) => !machine.temporary)
    .map((machine) => ({ name: machine.name, connected: machine.state === "connected" }));
}

// tasksOf adds the machine a listing came from to each task `shp list --json` returns.
export function tasksOf(machine, text) {
  const tasks = JSON.parse(text);
  if (!Array.isArray(tasks)) throw new Error("shp list --json printed no list");
  return tasks.map((task) => ({ ...task, machine: machine ?? null }));
}

// workspace is the task's Herdr space, if it has one: named "<ID> · <title>" on its machine.
export function workspace(task, spaces) {
  const machine = task.machine ?? null;
  const id = task.workspace;
  return spaces.find((space) => (space.machine ?? null) === machine
    && (space.name === `${task.id} · ${task.title}` || Boolean(id) && (space.id === id || space.id.endsWith(`/${id}`)))) ?? null;
}
