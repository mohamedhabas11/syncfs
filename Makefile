GO ?= go
GOFLAGS ?=

.PHONY: test vet lint build clean mod-verify help

help:
	@echo "targets: test vet lint build clean mod-verify"

mod-verify:
	$(GO) mod verify

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

## Lint via go vet + gofmt (no external linter pinned yet).
## Swap in golangci-lint once M1-02 adds the first real packages.
lint:
	$(GO) vet ./...
	$(GO) fmt ./... > /dev/null
	gofmt -l -s .

build:
	$(GO) build ./...

clean:
	$(GO) clean -cache -testcache 2>/dev/null || true
	rm -rf /bin
