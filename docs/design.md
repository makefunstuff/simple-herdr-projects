# Design: simple-herdr-projects v1

Plain files, herdr-core running the agents, and one program, `shp`. A space is a git repo; a task is one Markdown file with its state in the header.

## 1. Files

Files live in `~/.herdr/simple/`: `config.md`, `spaces/work.md`, `spaces/work/APP-1.md`, and a report beside it as `APP-1.review-1.md`.

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
Standing notes for the coordinator, if any.
```

`merge` is `local` or `pr`; `checks` is a list of argv. The prefix is derived once from the name (initials, 2–5 letters) and stored; import keeps the old one, or appends a digit if taken. A space's `coordinator` argv beats config; the body is its brief. Spaces are git repos; non-repos are not v1.

**A task** is one Markdown file:

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
reviewed_commit: null
blocking: 0
progress: 40
step: Writing the box
left: 1h
asks: []
workspace: sp-def456
---
The brief, in Markdown. The worker reads the whole file.
```

`ready` adds `summary` and `proof`; accept adds `pr` and `merged`; all optional. An ask is `{"id": 1, "text": "Which columns?", "at": "2026-11-06T10:00"}`.

- Header: `key: value` lines between the first `---` pair, lists and objects as JSON. Missing keys default (`status: todo`, `asks: []`); an unknown status is shown, never rewritten; a bad file is an error naming its path.
- Ids: stored prefix plus `next`, never reused, unique per machine. Paths use `~`; no machine names.
- Writes use one `flock` and rename; an unreadable file keeps its last good copy, never an empty one.

## 2. States

| Key | Word | Moves to | Who |
|---|---|---|---|
| `todo` | To do | `queued` or `working` | person, coordinator: `shp start` |
| `queued` | Queued | `working` when a slot or parent frees | `shp tick` |
| `working` | Working | `needs` by ask; `ready` by ready | worker |
| `needs` | Needs you | `working` once delivered | `shp` |
| `ready` | Ready | `merging`/`done` by accept; `working` by reopen | person |
| `merging` | Merging | `done`; `ready` if it can't merge | `shp tick` |
| `done` | Done | `working` by reopen | person |

- **Needs you only on an explicit ask.** Silence, idle, blocked or a merge problem never moves a task; Herdr's blocked dot stays Herdr's. Only `shp say` closes it; typing in the terminal does not.
- **Delivered means herdr-core said so.** `hc send` must exit 0 for a live terminal; a failure leaves the ask open and says why.
- Worker: `status`, `ask`, `ready`, `review`; coordinator: `add`, `start`, `status`; person: `say`, `start`, `accept`, `reopen`, `done`, `--again`, and `done` from any open state. Nothing else moves a task. Reopen keeps the id and starts `<branch>-2` from base.

## 3. CLI

`shp`: one Python 3 file, stdlib only, using `hc` and `herdr-app`. One line per task:

```
APP-1  Working    Add a search box  40%  Writing the box  1h
```

- `shp space add`, `shp add`, `shp list`, `shp show`
- `shp start`, `shp tick`, `shp status`, `shp ask`, `shp say`
- `shp ready`, `shp review`, `shp reviewed`
- `shp accept`, `shp reopen`, `shp done`
- `shp import`, `shp config`

`--json` uses the header's keys. Exit 0 is done, 1 refused (why on stderr), 2 bad usage; a refusal prints the state and what would move it. `shp say <id> "<text>"` sends to the worker with `hc send` and closes an open ask only on success. `shp accept` asks for confirmation unless `--yes`; board and phone confirm first.

## 4. Agents

Workers, coordinator and reviewers are ordinary labelled `hc` terminals.

- **Worker.** `shp start` makes the worktree and workspace, then the Chat tab with `space.new --tab Chat --argv <worker>` (a task's `agent` wins) and `--task "<rules and task path>"`. It checks `first_message.sent` from `hc get`; else it reads and retries, never blind-resends. Labels: `herdr.task=APP-1`, `herdr.role=worker`, `herdr.space=<workspace>`.
- **Coordinator.** One per space, in its Herdr space, tab "Assistant", label `herdr.role=coordinator`, argv from the space else config; its first message points at the space file. `shp space add --coordinator` starts it; `shp start` if none.
- **Reviewer.** Only `shp review` starts it: tab "Review" in the worktree, label `herdr.role=reviewer`, name `review-<id>-<round>` lowercased, cut to 32. Never headless; `shp review` waits.
- **Queue.** `max_running` workers per machine (default 4); reviewers don't count. A child waits for its `parent`. Only `shp start`, `shp tick` and the board's 10 s tick start work; reads never do. No daemon, so the queue waits when Herdr is closed.

## 5. Review and merge

A worker commits and runs `shp review`, refused at `rounds` unless a person passes `--again`. The reviewer gets the task, `git diff <base>...HEAD`, the checks, the read-only template (`~/.config/herdr-core/templates/reviewer.md`) and a prompt to write `/tmp/shp-review-<id>-<n>.md` with `blocker`/`major`/`minor` counts and findings. `shp review` waits, copies the report beside the task and sets `review`, `reviewed_commit: HEAD` and `blocking` (findings at or above the config's `must_fix`). No header, no round. `shp reviewed --report` records a hand-started round.

`shp ready` refuses with no round, with `reviewed_commit` not HEAD, or while `blocking` is above 0. At the limit the worker asks the person; its first message says only a person passes `--again`.

Accept works only from ready with `blocking: 0`:

1. `checks` must pass, the task worktree must be clean, and a failed run leaves it ready with its output. A dirty tree refuses Accept.
2. **Local:** a clean main checkout, then `git merge --no-ff`. A conflict aborts and stays ready, naming the files. Success sets done and `merged`, kills the terminals, then removes the worktree and branch; a failed removal keeps them and says so.
3. **PR:** push and create or reuse the PR (`summary`, `proof`, task ref), set merging, `gh pr merge --auto --squash` where allowed, else tick merges on green. Merged → done; closed or conflicted → ready. One accept per space; tick merges only an accepted task.

## 6. Herdr

- `shp space add` ensures the repo's Herdr space and records `herdr_space`. A task's workspace has folder = worktree and name `<ID> · <title>`; Herdr nests a worktree under the first space at its checkout, with no parent field. It goes when done, after its terminals are unlabelled.
- Tabs: Chat, Terminal, Review (after a round), Files, Changes, PR (once there is one), each `tab.new --space <workspace>`; the last three `--mod px-files|px-changes|px-pr`, or a shell tab without them.
- Board: one mod, `shp-board`, reading `shp list --json` every 2 s: counts, rows, open a workspace, a confirm before `shp accept --yes`, and one toast per need or ready. Herdr's blocked notification stays Herdr's. No layouts, no sidebar replacement, no installer; `shp` writes only its store, spaces and tabs.

## 7. Phone

The app runs `shp list --json`, `show`, `accept --yes` and `say` over its existing SSH connection to the machine; chat is `hc read`/`hc send`. No server, port, token or push in v1: the app polls when opened or foregrounded, and an answer is delivered as on the desktop. The cost is a round trip per action, fine for a board. `shp serve` is not v1, and "remote `--on`" in the drop table means Shepherdr's, not hc's.

## 8. Import and the switch

First stop Shepherdr's scheduler, service and their systemd units. `shp import` reads `~/.herdr/tasks/*.json` without writing to it: `todo`/`queued` as they are, `running→working`, `ready→ready`, `done` only with `--all`. It keeps id, title, prompt, agent argv, branch, worktree, `after` (first as `parent`), review and progress, and records `imported_key` (Shepherdr's `<slug>#<n>`). Attention becomes a note in `step`, not a need; a cross-space `after` imports as `todo`.

A live old worker is adopted by its `herdr.task` label (`imported_key`); if Shepherdr restarts, `shp start` refuses a key with a live terminal. Old agents keep running while new tasks go to `shp`; keep the old store read-only, and remove the mods, hooks and links after the last task.

## 9. Not in v1

Web app, voice, add-ons, settings UI, proof store, multi-machine sync, non-repo spaces, installer, archive, automations, push; Autopilot later as a plain schedule.

## 10. Build

Six tasks, in order. Each is done when:

1. **Core.** Files, ids, spaces, config, worktrees, Chat and Terminal, `add/list/show/start/status/ask/say/reopen/done`, queue and lock. Done: a worker runs and reports; an ask closes on a confirmed send; todo, queued, working and needs show; an old file missing `status` runs.
2. **Review and merge.** `review/reviewed`, `ready`, `accept`, merging, PR, conflicts, tick. Done: a blocker stops ready; an unreviewed HEAD or later commit refuses; round 2 accepts; a dirty tree refuses; a conflict stays ready.
3. **Herdr.** Repo space, tabs, labels, nesting, safe removal, toasts. Done: a workspace and tabs sit under the repo; done kills terminals first; hand-made spaces stay; no layout is written.
4. **Board.** Done: `herdr-app check` passes; it lists, opens, confirms, accepts and toasts once per need or ready.
5. **Phone.** Done: the app lists and accepts over SSH with no new service; an ask clears on hc's confirmation.
6. **Import.** Done: a dry run names open tasks; a real run writes files `shp` reads; old workers are adopted; the old store is unchanged.

## What we drop from Shepherdr and why

| Dropped | Why |
|---|---|
| Web app | Board and phone cover it. |
| Service, HTTP API, token, port, push | Files and herdr-core's link suffice. |
| Versioned store and migrations | Headers only gain keys. |
| Settings window, layouts, installer, sidebar mods | One config; the person's layout. |
| Artifact/proof store | Herdr's artifacts exist. |
| Inferred needs, need kinds, inbox | One explicit ask. |
| Automations and Autopilot | A cron line later. |
| Archive, pinning, Done folding, guessed progress | A status and a list flag. |
| Multi-machine sync, remote `--on`, voice, add-ons, browser relay | Herdr runs terminals and a browser there. |
