GO ?= go
GOFLAGS ?=
BUF_BASIS := api/baseline
BENCH_COMMIT := $(shell git rev-parse --short HEAD)

.PHONY: proto-lint proto-breaking proto-gen test vet lint build clean mod-verify fleet-test docs-gen docs-build bench-floor bench-sweep help

help:
	@echo "targets: test vet lint build fleet-test docs-gen docs-build bench-floor bench-sweep"

mod-verify:
	$(GO) mod verify

proto-lint:
	buf lint

proto-breaking:
	buf breaking --against $(BUF_BASIS)

proto-gen:
	buf generate

test:
	$(GO) test ./...

fleet-test:
	docker compose run --rm node-a go test ./... -count=1 -race

vet:
	$(GO) vet ./...

## Lint via go vet + gofmt (no external linter pinned yet).
## Swap in golangci-lint once M1-02 adds the first real packages.
lint:
	$(GO) vet ./...
	$(GO) fmt ./... > /dev/null
	gofmt -l -s .
	buf lint

build:
	$(GO) build ./...

docs-gen:
	# Reference docs are hand-written from the code (see docs/reference/api.md).
	# This target is the single regeneration entry point for future generators.
	@echo "docs-gen: reference/api.md table regenerated manually from api/v1/*.proto (buf generate)"

docs-build:
	mkdocs build --strict

clean:
	$(GO) clean -cache -testcache 2>/dev/null || true
	rm -rf /bin

## Benchmark policy (laptop / Docker Desktop): every result states Docker
## Desktop Linux VM, volume type (tmpfs or named volume, never a macOS bind
## mount for data), commit, and exact command. Absolute numbers here are
## NEVER compared against spec §11 targets (UNVERIFIED: needs 3x NVMe
## honoring flushes + 10 GbE). Fresh payloads only; dedup state recorded.
bench-floor:
	@echo "bench-floor: write+fsync loop + RTT on Docker Desktop Linux VM"
	@echo "commit $(BENCH_COMMIT)"
	docker compose build bench >/dev/null
	docker compose up -d node-b node-c >/dev/null
	docker compose run --rm -e COMMIT_SHA=$(BENCH_COMMIT) bench go run ./cmd/bench-floor -dir /bench/volume/floor -volume-type named_volume -n 500 -peer node-b 2>/tmp/bench-floor-volume.log >docs/benchmarks/floor-volume.json || (cat /tmp/bench-floor-volume.log; docker compose stop node-b node-c; exit 1)
	@echo "volume run done (see docs/benchmarks/floor-volume.json)"
	docker compose run --rm -e COMMIT_SHA=$(BENCH_COMMIT) bench go run ./cmd/bench-floor -dir /bench/tmpfs/floor -volume-type tmpfs -n 500 -peer node-b 2>/tmp/bench-floor-tmpfs.log >docs/benchmarks/floor-tmpfs.json || (cat /tmp/bench-floor-tmpfs.log; docker compose stop node-b node-c; exit 1)
	@echo "tmpfs run done (see docs/benchmarks/floor-tmpfs.json)"
	docker compose stop node-b node-c
	python3 scripts/bench-render.py floor docs/benchmarks/floor-volume.json docs/benchmarks/floor-tmpfs.json

bench-sweep:
	@echo "bench-sweep: clients 1,8,32,128 at 4KiB fresh payloads, tmpfs + named volume"
	@echo "commit $(BENCH_COMMIT)"
	docker compose build bench >/dev/null
	docker compose run --rm -e COMMIT_SHA=$(BENCH_COMMIT) bench go run ./cmd/bench-sweep -dir /bench/volume/sweep -clients 1,8,32,128 -n 100 -volume-type named_volume >docs/benchmarks/sweep-volume.json
	@echo "volume sweep done"
	docker compose run --rm -e COMMIT_SHA=$(BENCH_COMMIT) bench go run ./cmd/bench-sweep -dir /bench/tmpfs/sweep -clients 1,8,32,128 -n 100 -volume-type tmpfs >docs/benchmarks/sweep-tmpfs.json
	@echo "tmpfs sweep done"
	python3 scripts/bench-render.py sweep docs/benchmarks/sweep-volume.json docs/benchmarks/sweep-tmpfs.json docs/benchmarks/floor-volume.json docs/benchmarks/floor-tmpfs.json
