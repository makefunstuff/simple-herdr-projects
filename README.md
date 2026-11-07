# simple-herdr-projects

Small project management for coding agents on [herdr](https://herdr.dev): spaces, coordinators, tasks, a git worktree per task, review and Accept, with nothing more on top.

## Why

It keeps the ideas that work (one coordinator per project, tasks with a brief and a status, one agent and one worktree per task, a review before merge, and real questions when an agent needs you) and drops the weight around them. It isn't an app layered over Herdr, and there are no layouts to install, no web app and no migrations.

## Principles

- **Plain files.** A task is one Markdown file with a small header. You can read it, grep it and edit it by hand.
- **No migrations, ever.** New fields are only ever added, and old files keep working.
- **herdr-core is the runtime.** Agents are ordinary `hc` terminals with labels, and each started task gets its own workspace and tabs.
- **Honest states.** A task is "needs you" only when its agent actually asks something. A message counts as delivered only once it has been sent.
- **One config file.** It sets which CLI and model each role (coordinator, worker, reviewer) runs.
- **Small.** One CLI, one small board panel and a phone client. Anything else needs a good reason.

## Status

Early design. See [`docs/design.md`](docs/design.md).

## Build

Go 1.27, standard library only.

```
go build -o shp .
go test ./...
```

## Credits

The concepts follow [Shepherdr](https://github.com/eliasstravik/shepherdr) (MIT). This is a separate, from-scratch implementation.

## License

MIT
