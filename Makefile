# OpenSyncCRDT — build automation
#
# The default build targets the host platform. SQLite (mattn/go-sqlite3) and
# the automerge-go CRDT core are the two dependencies that require CGO; every
# other dependency is pure Go.
#
# Cross-compilation needs a C cross-toolchain. The dist-* targets below use
# `zig cc` because it bundles cross toolchains for every target in a single
# install (https://ziglang.org). Install zig, then `make build-all`.
#
# IMPORTANT — linux targets link against glibc, NOT musl. automerge-go ships a
# prebuilt Rust static archive (deps/libautomerge_core_linux_*.a) that
# references glibc LFS64 symbols (stat64/open64/fstat64) which musl does not
# provide; a musl link fails with "undefined reference to stat64". Building
# against glibc resolves those symbols, and the result is still a fully static
# binary. `-tags netgo` supplies a pure-Go DNS resolver so the static binary
# resolves webhook hosts without glibc's NSS dlopen path.

BINARY      := opensynccrdt
PKG         := ./cmd/opensynccrdt
DIST        := dist
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)

# Static link flags for the linux release binaries (see header note).
# -lunwind resolves the Itanium unwind symbols (_Unwind_Backtrace, _Unwind_GetIP)
# that automerge-go's prebuilt Rust archive references for its std backtraces; a
# fully static link pulls in no unwinder otherwise and ld.lld fails on those
# undefined symbols. It follows -lautomerge_core in the link line (Go appends
# extldflags last), which is the order static resolution requires.
STATIC_LD   := $(LDFLAGS) -linkmode external -extldflags "-static -lunwind"

# Docker image variants shipped by the project.
IMAGE       := opensynccrdt/server
DOCKER_DIR  := deploy/docker

.DEFAULT_GOAL := build

# --- local development ------------------------------------------------------

.PHONY: build
build: ## Build the binary for the host platform
	CGO_ENABLED=1 go build -ldflags '$(LDFLAGS)' -o $(BINARY) $(PKG)

.PHONY: run
run: ## Run locally with default config
	CGO_ENABLED=1 go run $(PKG)

.PHONY: test
test: ## Run unit tests
	CGO_ENABLED=1 go test ./cmd/... ./internal/... ./pkg/...

.PHONY: test-race
test-race: ## Run unit tests with the race detector
	CGO_ENABLED=1 go test -race ./cmd/... ./internal/... ./pkg/...

.PHONY: test-integration
test-integration: ## Run integration tests (SQLite hermetic; set TEST_*_URL for Postgres/MySQL/Redis)
	CGO_ENABLED=1 go test -race ./tests/integration/... ./internal/storage/... ./internal/cluster/...

.PHONY: lint
lint: ## Run all linters (go vet + staticcheck)
	go vet ./cmd/... ./internal/... ./pkg/...
	go run honnef.co/go/tools/cmd/staticcheck@latest ./cmd/... ./internal/... ./pkg/...

.PHONY: fmt
fmt: ## Format all Go source
	gofmt -w cmd internal pkg tests

.PHONY: tidy
tidy: ## Sync go.mod/go.sum
	go mod tidy

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BINARY) $(DIST) *.db *.db-wal *.db-shm

.PHONY: docs
docs: ## Serve documentation locally on :8000
	@echo "Serving docs/ at http://localhost:8000 (Ctrl-C to stop)"
	cd docs && python3 -m http.server 8000

# --- docker -----------------------------------------------------------------

.PHONY: docker-build
docker-build: ## Build all three Docker image variants
	docker build -f $(DOCKER_DIR)/Dockerfile            --build-arg VERSION=$(VERSION) -t $(IMAGE):latest .
	docker build -f $(DOCKER_DIR)/Dockerfile.alpine     --build-arg VERSION=$(VERSION) -t $(IMAGE):alpine .
	docker build -f $(DOCKER_DIR)/Dockerfile.distroless --build-arg VERSION=$(VERSION) -t $(IMAGE):distroless .

.PHONY: docker-push
docker-push: ## Push all three Docker image variants
	docker push $(IMAGE):latest
	docker push $(IMAGE):alpine
	docker push $(IMAGE):distroless

# --- cross compilation ------------------------------------------------------
# Release binaries. Requires `zig` on PATH for the C cross-toolchain.
#
# Supported platforms are linux/{amd64,arm64} and darwin/{amd64,arm64}.
# windows is out of scope: the mandated CRDT dependency (automerge-go) ships
# prebuilt archives and cgo LDFLAGS only for those four platforms, so a windows
# build cannot link the automerge core, and building a custom CRDT is itself
# out of scope.

.PHONY: build-all
build-all: dist-linux-amd64 dist-linux-arm64 dist-darwin-amd64 dist-darwin-arm64 ## Build release binaries for all supported platforms

.PHONY: dist-linux-amd64
dist-linux-amd64: ## Build static linux/amd64 release binary
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 CC="zig cc -target x86_64-linux-gnu" \
		go build -tags netgo -ldflags '$(STATIC_LD)' \
		-o $(DIST)/$(BINARY)-linux-amd64 $(PKG)

.PHONY: dist-linux-arm64
dist-linux-arm64: ## Build static linux/arm64 release binary
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC="zig cc -target aarch64-linux-gnu" \
		go build -tags netgo -ldflags '$(STATIC_LD)' \
		-o $(DIST)/$(BINARY)-linux-arm64 $(PKG)

.PHONY: dist-darwin-amd64
dist-darwin-amd64: ## Build darwin/amd64 release binary
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 CC="zig cc -target x86_64-macos" \
		go build -ldflags '$(LDFLAGS)' -o $(DIST)/$(BINARY)-darwin-amd64 $(PKG)

.PHONY: dist-darwin-arm64
dist-darwin-arm64: ## Build darwin/arm64 release binary
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 CC="zig cc -target aarch64-macos" \
		go build -ldflags '$(LDFLAGS)' -o $(DIST)/$(BINARY)-darwin-arm64 $(PKG)

.PHONY: help
help: ## List available targets
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'
