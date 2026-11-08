# Contributing

## Build and test

- Go **1.26.0** (REQUIRED; `go.mod` requires it).
- Preferred loop: the 3-node container fleet.
  ```sh
  docker compose build
  docker compose run --rm node-a go test ./... -count=1 -race
  ```
- Host-side equivalents also work with a local Go 1.26:
  `go build ./...`, `go vet ./...`, `go test -race ./...`
- Docs: `mkdocs build --strict` (requires `docs/requirements.txt`).

## Commit style

One commit per milestone: `"<milestone id>: <summary>"`.
Stage explicit paths only — never `git add -A`. Run `gofmt -l -s` before commit.

## Issues / PRs

Use the issue templates. PRs must cite the acceptance command(s) and paste the
relevant output.
