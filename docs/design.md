# Design: simple-herdr-projects v1

Plain files, herdr-core running the agents, and one program, `shp`. A space is a folder; a task is one Markdown file whose header holds its state.

## 1. Files

Files live in `~/.herdr/simple/`:

```
config.md
spaces/work.md
spaces/work/APP-1.md
spaces/work/APP-1.review-1.md
```

**Config**, the argv per role and defaults:

```
---
coordinator: ["pi"]
worker: ["pi"]
reviewer: ["pi"]
must_fix: major
rounds: 3
max_running: 4
---
```

**A space** is `spaces/<name>.md`; the file's name is the space's:

```
---
folder: ~/work/app
prefix: APP
base: main
merge: local
checks: ["make", "check"]
next: 2
coordinator: {"argv": ["pi"], "terminal": "t12", "tab": "Assistant"}
---
Standing notes for the coordinator, if any.
```

`merge` is `local` or `pr`; `checks` may be empty; the body is the coordinator's brief. A space that isn't a repo has no worktree: its workers work in the folder.

**A task** is one Markdown file with a small header:

```
---
id: APP-1
title: Add a search box
status: working
agent: ["pi"]
branch: task/1-add-a-search-box
worktree: ~/.herdr/worktrees/app/task-1-add-a-search-box
parent: null
review: 0
must_fix: 0
progress: 40
step: Writing the box
left: 1h
asks: []
workspace: sp-abc123
---
The brief, in Markdown. The worker reads the whole file.
```

- Header: `key: value` lines between the first `---` pair, lists and objects as JSON. Missing keys default (`status: todo`, `asks: []`, `review: 0`), an unknown status reads `todo`, unknown keys survive, and a bad file is never rewritten.
- Ids are `PREFIX-N`: the prefix from the space name (initials up to 5, else a word's first 4, at least 2), the number from `next`, never reused. Ids are unique per machine, so a taken prefix gets a digit.
- Paths use `~`; no machine names. Writes take one `flock` and rename into place; a reader that can't parse keeps its last good copy, never an empty one.

## 2. States

| Key | Word | Moves to | Who moves it |
|---|---|---|---|
| `todo` | To do | `queued` or `working` | person or coordinator, via `shp start` |
| `queued` | Queued | `working` when a slot or its parent frees | `shp tick` |
| `working` | Working | `needs` by ask; `ready` by ready; `done` by done | worker, person |
| `needs` | Needs you | `working` when the answer is delivered | `shp`, on hc's confirmation |
| `ready` | Ready | `merging`/`done` by accept; `working` by reopen | person |
| `merging` | Merging | `done`; back to `ready` if it can't merge | `shp tick` |
| `done` | Done | `working` by reopen | person |

- **Needs you only on an explicit ask.** Silence, idle or blocked never moves a task; Herdr's blocked dot stays Herdr's.
- **Delivered means herdr-core said so.** A message counts only when `hc send` exits 0 for a live terminal; a failed send leaves the ask open and prints why. The ask closes on delivery, not on the click.
- Only `shp` writes a status. Each agent gets one verb; the person gets accept, reopen and done. Reopening a merged task starts `<old>-2` from base.

## 3. CLI

`shp`: one Python 3 file, stdlib only, using `hc` and `herdr-app`. One line per task; `--json` uses the header's keys.

```
APP-1  Working    Add a search box  40%  Writing the box  1h
```

- `shp space add`, `shp add`
- `shp list`, `shp show`
- `shp start`, `shp tick`
- `shp status`, `shp ask`, `shp say`
- `shp ready`, `shp review`, `shp reviewed`
- `shp accept`, `shp reopen`, `shp done`
- `shp import`, `shp config`

Exit 0 is done, 1 refused (why on stderr), 2 bad usage; a refusal prints the state and what would move it.

## 4. Agents

Workers, coordinator and reviewers are ordinary labelled `hc` terminals.

- **Worker.** `shp start` makes the worktree, the workspace and the Chat tab running the worker argv (a task's `agent` wins), labels it `herdr.task=APP-1`, `herdr.role=worker`, `herdr.space=<space>`, then sends the task path and the three verbs. The send decides whether it started.
- **Coordinator.** One per space, tab "Assistant", label `herdr.role=coordinator`; `shp space add --coordinator` starts it, `shp start` if there is none.
- **Reviewer.** Only `shp review` starts it: its own tab "Review" in the worktree, label `herdr.role=reviewer`, name `review-<id>-<round>`. Never headless; the worker waits with `hc wait` and reads the findings.
- **Queue.** `max_running` workers per machine (default 4); reviewers and coordinators don't count. A child waits for its `parent`. `shp tick` starts the next queued in id order, at every command and every 10 s from the board while Herdr is open; no daemon, so the queue waits when Herdr is closed.

## 5. Review and merge

A worker commits, then runs `shp review`, refused at `rounds` unless a person passes `--again`. The reviewer gets the task file, `git diff <base>...HEAD`, the checks and the shared template (`~/.config/herdr-core/templates/reviewer.md`), and writes `APP-1.review-<n>.md` beside the task:

```
---
blocker: 0
major: 2
minor: 3
---
```

then findings with file, line, proof and level. `shp review` reads the header, sets `review: n` and `must_fix: blocker + major`, and appends the report under `## Review n`. Levels: blocker (loses work or unsafe), major (someone hits it), minor (a nit). No header, no round. `shp ready` refuses while `must_fix` is above 0. Only a person adds a round past the limit; the worker's brief forbids `--again`, though the CLI can't tell who typed it.

Accept works only from ready with `must_fix: 0`:

1. Run `checks`; a failure leaves it ready and prints the output.
2. **Local:** a clean main checkout, then `git merge --no-ff`; a conflict aborts, stays ready and names the files; success is done, `merged`, worktree and branch removed.
3. **PR:** push and create or reuse the PR (summary, proof id), set merging, `gh pr merge --auto --squash` where allowed, else tick merges on green. Merged → done; closed or conflicted → ready.
4. One accept per space; a second says which task is merging.

## 6. Herdr

- One workspace per started task: folder is the worktree, name `<ID> · <title>`. Herdr shows a worktree under the first space at its checkout, so there is no parent field and no workspace service. It goes when the task is done, after its terminals are unlabelled: Herdr rebuilds a space from a labelled terminal.
- Tabs: Chat (worker), Terminal (shell), Review (once a round starts), Files, Changes, PR (once there is one). The last three are `herdr-app do tab.new --mod px-files|px-changes|px-pr`; without them a shell tab stands in.
- Board: one mod, `shp-board`. It reads `shp list --json` every 2 s, shows counts and rows, opens a workspace, accepts a ready task, and toasts once when a task turns needs or ready. Herdr's blocked notification stays Herdr's.
- No layouts, no sidebar replacement, no installer: the mod is copied in, the layout stays the person's, and `shp` writes only its store plus spaces and tabs through `herdr-app`.

## 7. Phone

The machine link the app already has is simplest. `shp list --json`, `shp show`, `shp accept`, `shp ask` and `shp say` run over the existing SSH connection (`hc exec --on`, or the app's own exec), and chat is `hc read`/`hc send`; an answered ask is delivered as on the desktop. No server, port, token or service; the cost is a round trip per action, fine for a board. `shp serve` is not v1.

## 8. Import and the switch

`shp import` reads Shepherdr's `~/.herdr/tasks/*.json` without writing to it. For each list it finds or makes the space (from its folder and prefix) and writes files for open tasks: `todo→todo`, `queued→queued`, `running→working` (or `needs` when attention is set, its reason as the ask), `ready→ready`, `done→done` only with `--all`. It keeps id, title, prompt, agent argv, branch, worktree, parent, review rounds and progress, and records `imported: shepherdr`. An existing id is skipped; a live Shepherdr worker is adopted by its `herdr.task` label, never started twice.

Side by side, Shepherdr keeps working untouched and new tasks go to `shp`: import once, early, then add only through `shp`, never copy back. When the old tasks are done, the person removes the Shepherdr mods and `task-workspaces.py`.

## 9. Not in v1

Web app, voice, add-ons, a settings UI, a proof store, multi-machine sync, an installer, archive and pinning, automations. Autopilot may later be a plain schedule.

## 10. Build

Six tasks, in order. Each is done when:

1. **Core.** Files, ids, spaces, config, worktrees, every command but import, the lock. Done: a repo task gets a worktree, a labelled worker and its workspace; it reports, asks and reaches ready; all seven states show; old files run.
2. **Review and merge.** Rounds, the reviewer tab and report, the must-fix gate, local and PR merges, conflicts, tick. Done: a blocker stops ready, round 2 clears it and accepts; a PR merges on green; a conflict stays ready, files named.
3. **Herdr.** Workspace, tabs, labels, nesting, removal, toasts. Done: a started task has its workspace and tabs under its repo; done removes it and leaves hand-made spaces alone; no layout is written.
4. **Board.** `shp-board`. Done: `herdr-app check` passes; it lists tasks, opens workspaces, accepts and toasts once per need or ready.
5. **Phone.** JSON calls over the link. Done: the app lists and accepts over SSH with no new service; an ask clears on hc's confirmation.
6. **Import.** `shp import`. Done: a dry run names the open tasks; a real run writes files `shp` reads; live old workers are adopted; the old store is unchanged.

## What we drop from Shepherdr and why

| Dropped | Why |
|---|---|
| Web app | Board and phone cover it; a second UI is the same work twice. |
| Service, HTTP API, token, port | Files, `shp` and herdr-core's link suffice. |
| Versioned store and migrations | Headers only gain keys; old files keep working. |
| Settings window, layouts, installer, sidebar mods | One `config.md`; the person's Herdr and layout; one panel added. |
| Artifact/proof store | Herdr's artifacts exist; the task keeps the id. |
| Inferred needs, need kinds, inbox | One explicit ask, never a guess from idle or blocked. |
| Automations and Autopilot | A cron line later, if ever. |
| Archive, pinning, Done folding, guessed progress | A status and `shp list --all`; only what `shp status` says. |
| Multi-machine sync and `--on` | Herdr runs terminals elsewhere; one machine's files. |
| Voice, add-ons, browser relay | Out of scope; Herdr has a browser. |
