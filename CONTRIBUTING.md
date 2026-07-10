# Contributing to OpenSyncCRDT

Thanks for your interest in improving OpenSyncCRDT! This guide covers setting up
a development environment, running the tests, and the pull-request process. By
participating you agree to abide by our [Code of Conduct](CODE_OF_CONDUCT.md).

## Scope

OpenSyncCRDT handles **sync infrastructure only**. Before proposing a feature,
check that it isn't explicitly out of scope: user management, JWT
issuance/validation, access-control lists or document permissions, presence or
cursor sharing, client SDKs, an admin dashboard UI, email/push notifications,
file/binary attachment sync, end-to-end encryption (TLS is the transport
security), per-user rate limiting (connection limits only), content
indexing/search, or application business logic. These belong in the developer's
own application, not in the sync engine.

Every configurable behaviour must be reachable via an environment variable or
the YAML config file — never a code change. New knobs go through
`internal/config` and the [configuration reference](docs/configuration.md).

## Development environment setup

1. **Install Go 1.23 or newer.** Verify with `go version`.

2. **Install a C compiler.** CGO is required — SQLite (`mattn/go-sqlite3`) and
   the Automerge CRDT core are both cgo dependencies. On Debian/Ubuntu:
   `sudo apt-get install build-essential`. On macOS: `xcode-select --install`.

   > Linux release binaries link against **glibc**, not musl: the prebuilt
   > Automerge archives reference glibc LFS64 symbols (`stat64`/`open64`) that
   > musl lacks. For local development the host toolchain is fine; only the
   > cross-compilation `dist-*` targets need the glibc note (see the `Makefile`
   > header and [architecture docs](docs/contributing/architecture.md)).

3. **Clone and build:**

   ```bash
   git clone https://github.com/opensynccrdt/opensynccrdt.git
   cd opensynccrdt
   make build          # produces ./opensynccrdt (CGO_ENABLED=1)
   ```

4. **(Optional) install `staticcheck`** for local linting — `make lint` will
   otherwise fetch it on demand via `go run`.

5. **(Optional) Docker + Docker Compose** if you want to run the Postgres/MySQL
   or cluster stacks, or exercise the integration tests against real backends.

## Running the server locally

```bash
make run             # runs cmd/opensynccrdt with default config (SQLite)
```

Or run a built binary directly, optionally with a config file:

```bash
./opensynccrdt --config config.example.yaml
```

The only command-line flag is `--config`; everything else is set via
environment variables or the YAML file. See the
[configuration reference](docs/configuration.md) and
[getting-started guide](docs/getting-started.md).

## Running the tests

```bash
make test              # unit tests
make test-race         # unit tests with the race detector
make test-integration  # integration + storage + cluster tests
make lint              # go vet + staticcheck
```

The storage and cluster integration tests **self-skip** unless the
corresponding backend URL is exported, so `make test` stays hermetic (SQLite
only). To exercise Postgres, MySQL, and Redis, start them (e.g. via the compose
files) and export:

```bash
export TEST_POSTGRES_URL="postgres://opensync:opensync@localhost:5432/opensynccrdt?sslmode=disable"
export TEST_MYSQL_URL="mysql://opensync:opensync@localhost:3306/opensynccrdt"
export TEST_REDIS_URL="redis://localhost:6379"
make test-integration
```

CI runs exactly this matrix — `go vet`, `staticcheck`, race-enabled unit tests,
and integration tests against all three storage backends plus Redis. Please make
sure `make lint` and `make test-race` pass before opening a PR.

### Testing expectations

- Every exported function in `internal/` should have a unit test.
- CRDT merge logic, auth webhook caching, and webhook signing/verification have
  extensive tests — extend them when you touch those areas.
- New storage backends (or changes to existing ones) must pass the shared
  conformance suite in `internal/storage/conformance_test.go` identically.

## Pull-request process

1. **Open an issue first** for anything non-trivial so we can agree on the
   approach before you invest time.
2. **Branch** from `main` and keep your change focused — one logical change per
   PR.
3. **Follow the existing style.** Run `make fmt` (gofmt) and match the
   surrounding code's naming and comment density.
4. **Add tests** covering the new behaviour and update the docs (including
   `docs/` and `config.example.yaml`) when behaviour or configuration changes.
5. **Update `CHANGELOG.md`** under the `[Unreleased]` heading.
6. **Fill in the PR template** — it includes a checklist for lint/tests,
   config-not-code, changelog, and scope.
7. **Green CI is required.** A maintainer will review; expect a first response
   within a few days. Reviews focus on correctness, scope, test coverage, and
   whether new behaviour is configurable rather than hard-coded. Address
   feedback by pushing follow-up commits (we squash on merge).

## Good first issues

New here? Look for issues labelled [`good first issue`][gfi] and
[`help wanted`][hw]. Documentation fixes, additional test cases (especially CRDT
edge cases), and improvements to the example apps are all excellent starting
points and are always appreciated. If a `good first issue` is unclear, ask in
the issue thread — we're happy to help you get started.

[gfi]: https://github.com/opensynccrdt/opensynccrdt/labels/good%20first%20issue
[hw]: https://github.com/opensynccrdt/opensynccrdt/labels/help%20wanted
