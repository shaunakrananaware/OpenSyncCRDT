# OpenSyncCRDT — build automation
#
# The default build targets the host platform. SQLite (mattn/go-sqlite3) is the
# one dependency that requires CGO; every other dependency is pure Go.
#
# Cross-compilation therefore needs a C cross-toolchain. The cross-* targets
# below use `zig cc` because it bundles cross toolchains for every target in a
# single install (https://ziglang.org). Install zig, then `make cross-all`.

BINARY      := opensynccrdt
PKG         := ./cmd/opensynccrdt
DIST        := dist
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)

.DEFAULT_GOAL := build

.PHONY: build
build: ## Build the binary for the host platform
	CGO_ENABLED=1 go build -ldflags '$(LDFLAGS)' -o $(BINARY) $(PKG)

.PHONY: run
run: ## Build and run with local defaults
	CGO_ENABLED=1 go run $(PKG)

.PHONY: test
test: ## Run all tests (race detector on)
	CGO_ENABLED=1 go test -race ./cmd/... ./internal/...

.PHONY: vet
vet: ## Run go vet
	go vet ./cmd/... ./internal/...

.PHONY: fmt
fmt: ## Format all Go source
	gofmt -w cmd internal

.PHONY: tidy
tidy: ## Sync go.mod/go.sum
	go mod tidy

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BINARY) $(DIST) *.db *.db-wal *.db-shm

# --- cross compilation ------------------------------------------------------
# Each target links a static binary. Windows produces a .exe. zig cc supplies
# the C compiler for the SQLite CGO dependency.

.PHONY: cross-all
cross-all: cross-linux-amd64 cross-linux-arm64 cross-darwin-amd64 cross-darwin-arm64 cross-windows-amd64 ## Build every release target

.PHONY: cross-linux-amd64
cross-linux-amd64:
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 CC="zig cc -target x86_64-linux-musl" \
		go build -ldflags '$(LDFLAGS) -linkmode external -extldflags "-static"' \
		-o $(DIST)/$(BINARY)-linux-amd64 $(PKG)

.PHONY: cross-linux-arm64
cross-linux-arm64:
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC="zig cc -target aarch64-linux-musl" \
		go build -ldflags '$(LDFLAGS) -linkmode external -extldflags "-static"' \
		-o $(DIST)/$(BINARY)-linux-arm64 $(PKG)

.PHONY: cross-darwin-amd64
cross-darwin-amd64:
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 CC="zig cc -target x86_64-macos" \
		go build -ldflags '$(LDFLAGS)' -o $(DIST)/$(BINARY)-darwin-amd64 $(PKG)

.PHONY: cross-darwin-arm64
cross-darwin-arm64:
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 CC="zig cc -target aarch64-macos" \
		go build -ldflags '$(LDFLAGS)' -o $(DIST)/$(BINARY)-darwin-arm64 $(PKG)

.PHONY: cross-windows-amd64
cross-windows-amd64:
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC="zig cc -target x86_64-windows-gnu" \
		go build -ldflags '$(LDFLAGS)' -o $(DIST)/$(BINARY)-windows-amd64.exe $(PKG)

.PHONY: help
help: ## List available targets
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'
