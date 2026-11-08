GO ?= go
GOFLAGS ?=
BUF_BASIS := api/baseline

.PHONY: proto-lint proto-breaking proto-gen test vet lint build clean mod-verify fleet-test help

help:
	@echo "targets: proto-lint proto-breaking proto-gen test vet lint build clean mod-verify fleet-test"

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

clean:
	$(GO) clean -cache -testcache 2>/dev/null || true
	rm -rf /bin
