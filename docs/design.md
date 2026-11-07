# Design: simple-herdr-projects v1

Plain files, herdr-core for the agents, one program, `shp`. A space is a git repo; a task is one Markdown file with its state in the header.

## 1. Files

Files in `~/.herdr/simple/`: `config.md`, `spaces/work.md`, `spaces/work/APP-1.md`, `APP-1.review-1.md`.

Config: role argv, `must_fix: major`, `rounds: 3`, `max_running: 4`.

Space `spaces/<name>.md`: `folder`, `prefix`, `base`, `merge` (local/pr), `checks` (argv), `next`; optional `herdr_space`, `coordinator` (e.g. `{"argv": ["pi"], "tab": "Assistant"}`). Prefix (2–5 initials) stored once, digit if taken; import keeps or appends. The body is the brief.

A task's header: `id`, `title`, `status`, `agent` (worker argv, empty = config), `branch`, `worktree`; `parent`/`after`, `review`/`reviewed_commit`/`blocking`, `asks`, optional `summary`/`proof` (ready; proof is a Herdr artifact id), `pr`/`merged` (accept), `progress`, `step`, `left`, `workspace`, `imported_key`; an ask is id, text, time. The Markdown below is the brief.

- Header: `key: value` in the first `---` pair, lists JSON; missing keys default (`status: todo`, `asks: []`), unknown statuses stay, a bad file names its path.
- Ids: prefix + `next`, never reused, unique per machine; writers refuse existing paths. Paths use `~`; no machine names.
- Writes: one `flock` + rename; never empty.

## 2. States

`todo` → `queued`/`working` (person, coordinator); `queued` → `working` when a slot, parent or `after` frees (tick); `working` → `needs` (ask) or `ready`; `needs` → `working` on delivery; `ready` → `merging`/`done` (accept) or `working` (reopen); `merging` → `done` or `ready` (tick); `done` → `working` (reopen). Words: To do, Queued, Working, Needs you, Ready, Merging, Done.

Needs you only on an explicit ask; silence, idle, blocked or merge trouble never move a task. Only `shp say` and a successful `hc send` close it; else it stays open and says why.

Worker: `status`, `ask`, `ready`, `review`; coordinator: `add`, `start`, `status`; person: `say`, `review`, `reviewed`, `ready`, `start`, `accept`, `reopen`, `done`, `--again`. `start` starts now; `tick` starts queued work.

`done` drops an open task (not `merging`), kills terminals, keeps branch and worktree. `reopen` from ready keeps them, refuses a dirty tree, clears `reviewed_commit`; from done it reuses worktree/branch if present, else `<branch>-2` (or the next free number) from base, passing through `todo` to start its worker.

## 3. CLI

`shp`: one Go binary, standard library only, over `hc` and `herdr-app`. Commands are the role lists above plus `space add`, `list`, `show`, `tick`, `ready --summary/--proof`, `import`, `config`. `list` shows id, status, title, progress and step or asks.

`--json` uses header keys. Exit 0 done, 1 refused (why on stderr), 2 bad usage; refusals say what would move them. `accept` confirms unless `--yes`; board and phone confirm first. `config` prints the file.

## 4. Agents

- **Worker.** `start` makes the worktree and workspace with `space.new --folder <worktree> --tab Chat --argv <agent> --task "<rules and task path>"`, takes the terminal id, labels `herdr.task=APP-1`, `herdr.role=worker`. A false `first_message.sent` is read and retried, never blind-resent. `say` finds it by those labels or `imported_key`.
- **Coordinator.** One per space: tab "Assistant", label `herdr.role=coordinator`, argv from space else config; first message points at the space file; `space add --coordinator` or `start` runs it.
- **Reviewer.** Only `review`: `tab.new --name Review --space <workspace> --folder <worktree> --argv <reviewer> --task <prompt>`, labelled `herdr.role=reviewer`, `herdr.task=APP-1`; checks `first_message.sent`, retries after reading, waits as long as needed, keeps its tab, never fails it. Never headless.
- **Queue.** `max_running` workers per machine (default 4), reviewers don't count; a child waits for `parent` and every `after`. Only `start`, `tick` and the board's 10 s tick start work; reads never do.

## 5. Review and merge

Worker commits, then `review`, refused at `rounds` unless a person passes `--again`. Reviewer gets the task, `git diff <base>...HEAD`, checks, the read-only `~/.config/herdr-core/templates/reviewer.md` and a prompt to write `/tmp/shp-review-<id>-<n>.md` with `blocker`/`major`/`minor` counts and findings. `review` waits, copies the report beside the task, sets `review`, `reviewed_commit` (its launch HEAD) and `blocking` (findings at or above `must_fix`); later commits make the round stale. No header, no round; `reviewed --report` records one.

`ready` refuses with no round, a moved HEAD or `blocking` above 0. At the limit the worker asks the person; its first message says only a person passes `--again`. The Review tab keeps that report until the next round.

Accept needs ready, `blocking: 0` and `reviewed_commit` = HEAD; a newer commit returns it to working. Then:

1. `checks` pass and worktree clean, else it stays ready with output.
2. **Local:** main clean and on `base`, else refuse. `git merge --no-ff`; a conflict aborts, stays ready, names files. Success: done, `merged`, kill terminals, remove worktree/branch (a failed removal keeps them).
3. **PR:** push, create/reuse the PR (`summary`, `proof`, task ref), set merging; `gh pr merge --auto --squash` where allowed, else tick merges on green. Merged → done; closed/conflicted → ready. One accept per space; tick merges only an accepted task.

## 6. Herdr

- `space add` ensures the repo's Herdr space, records `herdr_space`. A task's workspace: worktree folder, name `<ID> · <title>`; Herdr nests it under the first space at the checkout. Done removes it after unlabelling terminals.
- Tabs: Chat, Terminal, Review (after a round, kept with its report), Files, Changes, PR (with one); the last three are `px-files|px-changes|px-pr` mods or shells.
- Board: `shp-board`, reading `shp list --json` every 2 s, calling `shp tick` every 10 s: counts, rows, open a workspace, confirm before `accept --yes`, one toast per need or ready. It reads and acts on every machine `hc machine ls` lists through Herdr's `--on`, grouped by machine; one without shp or out of reach shows one line. No layouts, sidebar or installer; `shp` writes only its store, spaces and tabs.

## 7. Phone

The app confirms, then runs `list --json`, `show`, `accept --yes`, `say` over SSH; chat is `hc read`/`hc send`. No server, token or push; it polls when opened.

## 8. Import and the switch

Stop Shepherdr first. `import` (`--dry-run` writes nothing) reads `~/.herdr/tasks/*.json` and `~/.herdr/shepherdr.json` without writing, and copies everything: each project becomes a space (or joins the space already on its folder; a `kind: folder` one gets no base, so nothing starts there), its prefix kept or given a digit; every task in every state: `todo`/`queued`/`done` as is, `running→working`, `ready→ready`. Keeps id, title, prompt, branch, worktree, `agent` or config, summary, proof, PR, progress, rounds from `reviews` (each as `<ID>.review-<n>.md`), and the whole old record as `<ID>.shepherdr.json`. Rewrites `after`/`after_ext` to imported ids; unresolved ones import as `todo` with why in `step`. Records `imported_key`, raises `next`, puts attention in `step`, not a need. A ready task takes `reviewed_commit` from the last `reviews[].head`; without one or with another HEAD it imports as `working`. The everywhere roles and task settings go into `config.md`. A task already in shp is kept as it is; the report goes to `import.md`.

A live old worker (the last `agents[]` entry, not ended, still live) is adopted: labelled `herdr.task`/`herdr.role=worker`, so `say`, `review`, `ready` and `done` find it; a person can `review`/`ready` for it. If Shepherdr restarts, `start` refuses a key with a live terminal. Old agents keep running; the old store stays read-only; mods and hooks go after the last task.

## 9. Not in v1

Web app, voice, add-ons, settings, proof store, sync, remote `--on` in `shp` (the board reaches machines through Herdr), non-repo spaces, installer, archive, automations, push.

## 10. Build

Six tasks, in order; done is:

1. **Core** (store, spaces, worktrees, worker launch, labels, queue): a worker runs and reports; an ask closes on a confirmed send; todo, queued, working and needs show; a file missing `status` runs.
2. **Review and merge** (`review`/`reviewed`, `ready`, `accept`, merging, PR, conflicts, tick): a blocker stops ready; no round or a moved HEAD refuses; round 2 accepts; a dirty tree or wrong main branch refuses; a conflict stays ready.
3. **Herdr** (Files, Changes and PR tabs, nesting, removal, toasts): the tabs sit under the repo; done kills terminals first; hand-made spaces stay; no layout is written.
4. **Board:** `herdr-app check ~/.config/herdr-app/mods/shp-board` passes; it lists, opens, confirms, accepts and toasts once per need or ready.
5. **Phone:** the app confirms, lists and accepts over SSH with no service; an ask clears on hc's confirmation.
6. **Import:** a dry run names open tasks; a real run writes files `shp` reads; `next` is past them; an imported ready task accepts; an adopted worker can finish; the old store is unchanged.

## Decisions

- The Review tab keeps its round's report until the next; a slow reviewer is waited for and kept, never failed.
- A round records its launch HEAD; a later commit makes it stale and `ready` refuses.
- Reopening a done task reuses its worktree and branch if they exist, else `<branch>-2` (or the next free number) from base.
