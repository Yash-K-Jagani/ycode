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
# exists - Linux and macOS, and Windows with a 64-bit gcc. It cannot run
# against a 32-bit gcc, which fails with "cc1.exe: sorry, unimplemented:
# 64-bit mode not compiled in".
#
# On Windows, install a 64-bit mingw-w64 (no admin needed, installs per-user
# and side by side with any existing MinGW):
#
#   winget install --id BrechtSanders.WinLibs.POSIX.UCRT \
#     --accept-package-agreements --accept-source-agreements
#
# then point the build at it, since a 32-bit gcc may come first on PATH:
#
#   set PATH=<winlibs>\mingw64\bin;%PATH%
#   set CC=x86_64-w64-mingw32-gcc
#   make race
#
# CI runs this in its own job rather than the main matrix, for the same reason.
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
