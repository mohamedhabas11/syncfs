# Getting started

The dev loop is the 3-node container fleet.

```sh
docker compose build
docker compose run --rm node-a go test ./... -count=1 -race
docker compose run --rm node-a go test ./internal/fusefs/ -count=1 -race -v -run TestFUSE
```

`node-a`, `node-b`, `node-c` each own a private writable volume
(`/var/lib/syncfs`) — independence is the point. Shared read-only code is at
`/src`. `/dev/fuse` is routed to every node for M3+ tests.
