# Development setup

A step-by-step guide to a working local development environment. For the
contribution workflow (PRs, scope, review), see the top-level
[CONTRIBUTING.md](../../CONTRIBUTING.md).

## Prerequisites

| Tool | Version | Notes |
| ---- | ------- | ----- |
| Go | 1.23+ | `go version` |
| C compiler | any | CGO is required (SQLite + Automerge core). |
| Docker (optional) | — | For Postgres/MySQL/Redis and the compose stacks. |
| staticcheck (optional) | latest | `make lint` fetches it on demand otherwise. |

CGO is mandatory:

- **Debian/Ubuntu:** `sudo apt-get install build-essential`
- **macOS:** `xcode-select --install`

## Clone and build

```bash
git clone https://github.com/opensynccrdt/opensynccrdt.git
cd opensynccrdt
make build        # -> ./opensynccrdt (CGO_ENABLED=1)
```

## Run it

```bash
make run                                  # default config, SQLite
./opensynccrdt --config config.example.yaml
```

The only flag is `--config`; everything else is env vars or the YAML file. Try
the endpoints:

```bash
curl localhost:8080/health
curl -X POST localhost:8080/api/v1/docs -H 'Content-Type: application/json' -d '{"doc_id":"demo"}'
```

## Make targets

| Target | Does |
| ------ | ---- |
| `make build` | Build the host binary. |
| `make build-all` | Cross-build release binaries for the four supported platforms (needs `zig`). |
| `make run` | Run with default config. |
| `make test` | Unit tests. |
| `make test-race` | Unit tests with the race detector. |
| `make test-integration` | Integration + storage + cluster tests. |
| `make lint` | `go vet` + `staticcheck`. |
| `make fmt` | `gofmt -w`. |
| `make tidy` | `go mod tidy`. |
| `make docker-build` / `docker-push` | Build/push the three image variants. |
| `make docs` | Serve `docs/` locally on `:8000`. |
| `make clean` | Remove build artifacts. |

## Running the full test matrix

Unit tests are hermetic (SQLite only). The storage and cluster tests self-skip
unless the matching backend URL is set. To run everything against real backends,
start them and export the URLs:

```bash
# Start dependencies (any method); the Postgres compose file is handy:
docker compose -f docker-compose.postgres.yml up -d

export TEST_POSTGRES_URL="postgres://opensync:opensync@localhost:5432/opensynccrdt?sslmode=disable"
export TEST_MYSQL_URL="mysql://opensync:opensync@localhost:3306/opensynccrdt"
export TEST_REDIS_URL="redis://localhost:6379"

make test-integration
```

This mirrors what CI does (see [`.github/workflows/ci.yml`](../../.github/workflows/ci.yml)),
which spins up Postgres, MySQL, and Redis service containers and sets those same
variables.

## Cross-compilation

`make build-all` uses [`zig`](https://ziglang.org) as the C cross-toolchain to
build the four supported platforms. Install zig and run:

```bash
make build-all      # dist/opensynccrdt-{linux,darwin}-{amd64,arm64}
```

The Linux targets link against glibc (not musl) — see
[architecture › build and platforms](architecture.md#build-and-platforms) for
why, and why Windows is not a target.

## Before you open a PR

```bash
make fmt
make lint
make test-race
```

Then update `CHANGELOG.md` under `[Unreleased]` and fill in the PR template.
