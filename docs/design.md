# Design: simple-herdr-projects v1

Plain files, herdr-core running the agents, and one program, `shp`. A space is a git repo; a task is one Markdown file with its state in the header.

## 1. Files

Files live in `~/.herdr/simple/`: `config.md`, `spaces/work.md`, `spaces/work/APP-1.md`, `APP-1.review-1.md`.

**Config**, argv per role:

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

**A space** is `spaces/<name>.md`:

```
---
folder: ~/work/app
prefix: APP
base: main
merge: local
checks: [["make", "check"]]
next: 2
herdr_space: sp-abc123
coordinator: {"argv": ["pi"], "tab": "Assistant"}
---
```

`merge` is local or pr; `checks` is argv lists. The prefix (initials, 2–5) is derived once and stored, with a digit if another space has it; import keeps it or appends one. A space's `coordinator` argv beats config; the body is its brief. Spaces are git repos.

**A task**:

```
---
id: APP-1
title: Add a search box
status: working
agent: ["pi"]
branch: task/1-add-a-search-box
worktree: ~/.herdr/worktrees/app/task-1-add-a-search-box
parent: null
after: []
review: 0
reviewed_commit: null
blocking: 0
progress: 40
step: Writing the box
left: 1h
asks: []
workspace: sp-def456
imported_key: null
---
The brief in Markdown.
```

`agent` is the worker argv, empty for config; `ready` sets `summary` and `proof` (a Herdr artifact id); accept sets `pr` and `merged`; all optional. An ask holds id, text and time.

- Header: `key: value` between the first `---` pair, lists as JSON; missing keys default (`status: todo`, `asks: []`), unknown statuses are never rewritten, and a bad file names its path.
- Ids: stored prefix plus `next`, never reused, unique per machine; every writer refuses an existing path. Paths use `~`; no machine names.
- Writes use one `flock` and rename; an unreadable file keeps its last good copy, never an empty one.

## 2. States

| Key | Word | Moves to | Who |
|---|---|---|---|
| `todo` | To do | `queued` or `working` | person, coordinator: `shp start` |
| `queued` | Queued | `working` when a slot, parent or `after` frees | `shp tick` |
| `working` | Working | `needs` by ask; `ready` by ready | worker |
| `needs` | Needs you | `working` once delivered | `shp` |
| `ready` | Ready | `merging`/`done` by accept; `working` by reopen | person |
| `merging` | Merging | `done`; `ready` if it can't merge | `shp tick` |
| `done` | Done | `working` by reopen | person |

- **Needs you only on an explicit ask.** Silence, idle, blocked or a merge problem never moves a task. An answer closes it only through `shp say` and a successful `hc send`; otherwise it stays open and says why.
- Worker: `status`, `ask`, `ready`, `review`; coordinator: `add`, `start`, `status`; person: `say`, `review`, `reviewed`, `ready`, `start`, `accept`, `reopen`, `done`, `--again`.
- `shp done` drops an open task (not `merging`), kills its terminals and keeps its branch and worktree. `shp reopen` from `ready` keeps them, refuses a dirty tree and clears `reviewed_commit`; from `done` it starts `<branch>-2` from base.

## 3. CLI

`shp`: one Python 3 file, stdlib only, using `hc` and `herdr-app`. One line per task: id, status, title, progress and step or asks.

Commands: `space add`, `add`, `list`, `show`, `start`, `tick`, `status`, `ask`, `say`, `ready --summary/--proof`, `review`, `reviewed --report`, `accept`, `reopen`, `done`, `import`, `config`.

`--json` uses the header's keys. Exit 0 done, 1 refused (why on stderr), 2 bad usage; a refusal says what would move it. `shp say` sends to the worker and closes an ask only on a successful `hc send`. `shp accept` asks for confirmation unless `--yes`; board and phone confirm first. `shp config` prints the file.

## 4. Agents

Agents are ordinary labelled `hc` terminals.

- **Worker.** `shp start` makes the worktree and workspace with `space.new --folder <worktree> --tab Chat --argv <task agent, else config worker> --task "<rules and task path>"`, takes the terminal id from the reply and labels it `herdr.task=APP-1`, `herdr.role=worker`. If `first_message.sent` is false it reads and retries, never blind-resends. `say` finds it by `herdr.role=worker` and that label, or by `imported_key` for an adopted one.
- **Coordinator.** One per space, in its Herdr space, tab "Assistant", label `herdr.role=coordinator`, argv from the space else config; its first message points at the space file, and `shp space add --coordinator` or `shp start` runs it.
- **Reviewer.** Only `shp review` starts it: `tab.new --name Review --space <workspace> --folder <worktree> --argv <reviewer> --task <prompt>`, labelled `herdr.role=reviewer`, `herdr.task=APP-1`. It checks `first_message.sent`, retries after reading, waits on the terminal id from the reply, and closes the tab on failure or after the round. Never headless.
- **Queue.** `max_running` workers per machine (default 4); reviewers don't count. A child waits for its `parent` and every `after`. Only `shp start`, `shp tick` and the board's 10 s tick start work; reads never do, so the queue waits when Herdr is closed.

## 5. Review and merge

A worker commits, then `shp review`, refused at `rounds` unless a person passes `--again`. The reviewer gets the task, `git diff <base>...HEAD`, the checks, the read-only template (`~/.config/herdr-core/templates/reviewer.md`) and a prompt to write `/tmp/shp-review-<id>-<n>.md` with `blocker`/`major`/`minor` counts and findings. `shp review` waits, copies the report beside the task, and sets `review`, `reviewed_commit: HEAD` and `blocking` (findings at or above `must_fix`). No header, no round; `shp reviewed --report` records a hand-started one.

`shp ready` refuses with no round, a moved HEAD, or `blocking` above 0. At the limit the worker asks the person; its first message says only a person passes `--again`.

Accept works only from ready with `blocking: 0`, and re-checks `reviewed_commit` against HEAD; a newer commit sends it back to working for a round. Then:

1. `checks` must pass and the task worktree must be clean; a failure leaves it ready with its output.
2. **Local:** the main checkout must be clean and on `base`, else refuse. `git merge --no-ff`. A conflict aborts and stays ready, naming the files. Success sets done and `merged`, kills the terminals, then removes the worktree and branch; a failed removal keeps them.
3. **PR:** push and create or reuse the PR (`summary`, `proof`, task ref), set merging, `gh pr merge --auto --squash` where allowed, else tick merges on green. Merged → done; closed or conflicted → ready. One accept per space; tick merges only an accepted task.

## 6. Herdr

- `shp space add` ensures the repo's Herdr space and records `herdr_space`. A task's workspace has folder = worktree and name `<ID> · <title>`; Herdr nests a worktree under the first space at its checkout. It goes when done, after unlabelling its terminals.
- Tabs: Chat, Terminal, Review (after a round), Files, Changes, PR (once there is one); the last three are `px-files|px-changes|px-pr` mods, or shells.
- Board: one mod, `shp-board`, reading `shp list --json` every 2 s and calling `shp tick` every 10 s: counts, rows, open a workspace, a confirm before `shp accept --yes`, and one toast per need or ready. No layouts, no sidebar replacement, no installer; `shp` writes only its store, spaces and tabs.

## 7. Phone

The app confirms, then runs `shp list --json`, `show`, `accept --yes` and `say` over its existing SSH connection; chat is `hc read`/`hc send`. No server, port, token or push in v1: the app polls when opened or foregrounded. A round trip per action is fine for a board; `shp serve` is not v1.

## 8. Import and the switch

First stop Shepherdr's scheduler, service and systemd units. `shp import` reads `~/.herdr/tasks/*.json` without writing to it, skips `kind: folder` stores and writes open tasks: `todo`/`queued` as they are, `running→working`, `ready→ready`, `done` only with `--all`. It keeps id, title, prompt, branch, worktree, `agent` from `agents[].agent` (mapped to argv) or empty for config, and review rounds from `reviews` else 0. It rewrites every `after` number and `after_ext` ref to an imported id; if any is unresolved it imports the task as `todo` and notes why in `step`. It records `imported_key` (Shepherdr's `<slug>#<n>`), raises the space's `next` past the highest imported id, and puts attention in `step`, not a need. A ready task takes `reviewed_commit` from the last `reviews[].head`; without one, or with a different HEAD, it imports as `working`.

A live old worker is adopted by its `herdr.task` label and found through `imported_key`; the person can run `review` and `ready` for it if its worker won't use shp. If Shepherdr restarts, `shp start` refuses a key with a live terminal. Old agents keep running; new tasks go to `shp`, the old store stays read-only, and the mods and hooks go after the last task.

## 9. Not in v1

Web app, voice, add-ons, settings UI, proof store, multi-machine sync, non-repo spaces, installer, archive, automations, push; Autopilot later as a plain schedule.

## 10. Build

Six tasks, in order; done is:

1. **Core.** The store, spaces, worktrees, worker launch, labels and queue. Done: a worker runs and reports; an ask closes on a confirmed send; todo, queued, working and needs show; a file missing `status` runs.
2. **Review and merge.** `review/reviewed`, `ready`, `accept`, merging, PR, conflicts, tick. Done: a blocker stops ready; no round or a moved HEAD refuses; round 2 accepts; a dirty tree or wrong main branch refuses; a conflict stays ready.
3. **Herdr.** Files, Changes and PR tabs, nesting, removal, toasts. Done: the tabs sit under the repo; done kills terminals first; hand-made spaces stay; no layout is written.
4. **Board.** Done: `herdr-app check ~/.config/herdr-app/mods/shp-board` passes; it lists, opens, confirms, accepts and toasts once per need or ready.
5. **Phone.** Done: the app confirms, lists and accepts over SSH with no service; an ask clears on hc's confirmation.
6. **Import.** Done: a dry run names open tasks; a real run writes files `shp` reads; `next` is past them; an imported ready task accepts; an adopted worker can finish; the old store is unchanged.

## What we drop from Shepherdr and why

| Dropped | Why |
|---|---|
| Web app, service, API, token, port, push | Board, phone and herdr-core's link suffice. |
| Versioned store and migrations | Headers only gain keys. |
| Settings, layouts, installer, sidebar mods | One config; the person's layout. |
| Artifact/proof store | Herdr artifacts exist. |
| Inferred needs, need kinds, inbox | One explicit ask. |
| Automations and Autopilot | A cron line later. |
| Archive, pinning, done folding, guessed progress | A status and a flag. |
| Multi-machine sync, Shepherdr's remote `--on`, voice, add-ons, browser relay | Herdr runs terminals and a browser there. |
