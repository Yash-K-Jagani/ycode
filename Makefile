# Verification commands.
#
# These mirror .github/workflows/ci.yml exactly. They exist because the two
# used to drift: local linting was run as `./internal/... ./cmd/...`, which
# silently skipped ./pkg, so six lint failures in pkg/ycodeclient reached CI
# unnoticed. Anything added to this repo is linted by `./...` here, as in CI.

GO ?= go

.PHONY: all
all: fmt-check vet lint test

.PHONY: build
build:
	$(GO) build ./...

.PHONY: fmt
fmt:
	gofmt -w cmd internal pkg store

# `gofmt -l .` also walks .git on some shells; CI scopes it to source dirs.
.PHONY: fmt-check
fmt-check:
	@unformatted=$$(gofmt -l cmd internal pkg store); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed on:"; echo "$$unformatted"; exit 1; \
	fi

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: lint
lint:
	golangci-lint run ./...

.PHONY: test
test:
	$(GO) test -short ./...

# Full run without -short, for local checks that want the slower tests.
.PHONY: test-all
test-all:
	$(GO) test ./...

# The race detector needs a 64-bit C toolchain, so this only works where one
# exists - Linux and macOS. It cannot run on 32-bit Windows, which is why CI
# runs it in its own job rather than in the main matrix.
.PHONY: race
race:
	$(GO) test -short -race -p 2 ./...

.PHONY: cover
cover:
	$(GO) test ./... -coverprofile=coverage.out -covermode=atomic
	$(GO) tool cover -func=coverage.out | tail -1

.PHONY: clean
clean:
	rm -f coverage.out
