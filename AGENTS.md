# Agent notes

- This repo is public. Never commit LAN IPs, hostnames, personal paths, keys or tokens, not even in tests, docs or commit messages. Use placeholders such as `~/work/app`, `machine-a` and `192.0.2.10`.
- Keep it small. Before adding a dependency, a file format change or a new concept, check it against the README's principles.
- File formats only ever gain optional fields. Never write a migration.
- Never commit with GPG signing.

## Code style

The goal is minimal, elegant code with no unnecessary abstractions.
- Prefer plain functions and data (dicts, lists, files) to classes, frameworks, plugin systems, registries or config layers.
- No abstraction until it is needed in a second real place. Duplication beats the wrong abstraction.
- Every line earns its place: delete before you add. A smaller diff that does the job wins.
- Go's standard library only, unless the design doc says otherwise.
- Read the code as a whole before adding to it, and fit what's there rather than layering on top.
- Reviewers flag unneeded abstraction, indirection or code as a major finding.

## Quality bar

The code has to be good, not just working: idiomatic Go that a senior Go developer would be glad to maintain. Use clear names, early returns and errors wrapped with context. No dead code, no clever tricks, no interfaces without two real implementations. Review with a different model from the one that wrote the code, judging quality as hard as correctness.

## Build and test

- `gofmt -l .`, `go vet ./...` and `go test ./...` all pass before a commit.
- `go build -o shp .` builds the binary; the tests build it themselves and run it against a throwaway HOME with a fake `hc` and `herdr-app` on PATH.
