# Load / soak tests

The load harness for the SPEC §18 testing requirements. These
tests spin up an embedded engine (SQLite, no auth) behind a real HTTP server and
drive it over real WebSocket connections.

They are **heavy** and do not run in the normal unit/CI path — they self-skip
unless `OPENSYNCCRDT_LOAD` is set:

```bash
OPENSYNCCRDT_LOAD=1 go test ./tests/load/ -run TestLoad -v -timeout 20m
```

## Scenarios

| Test | SPEC §18 requirement | What it does |
| ---- | -------------------- | ------------ |
| `TestLoad1000Connections` | 1000 simultaneous connections on one node | Opens N connections concurrently, each subscribing to its own document; reports connect+subscribe latency percentiles. |
| `TestLoadSustainedThroughput` | sustained 10 000 ops/sec | Many workers, each committing Automerge changes to its own document at a paced rate for a fixed duration; reports achieved ops/sec and per-op ack latency. |
| `TestLoadReconnectStorm` | reconnect storm (500 clients) | Primes a fleet, drops every connection at once, then releases all reconnections simultaneously behind a gate; reports reconnect latency. |

Each test reports **p50 / p95 / p99** latency, throughput, heap, goroutine
count, and CPU time via `t.Log` (visible with `-v`). Assertions cover liveness
only — that connections and operations succeed — because absolute numbers are
hardware-dependent.

## Tuning

Every scenario reads its scale from the environment, so the same harness probes
a laptop or a production-class host:

| Variable | Default | Applies to |
| -------- | ------- | ---------- |
| `OPENSYNCCRDT_LOAD` | _(unset → skip)_ | all — must be set to run |
| `OPENSYNCCRDT_LOAD_CONNS` | `1000` | connections test |
| `OPENSYNCCRDT_LOAD_WORKERS` | `200` | throughput test (concurrent writers) |
| `OPENSYNCCRDT_LOAD_OPS` | `10000` | throughput test (target ops/sec) |
| `OPENSYNCCRDT_LOAD_DURATION` | `10s` | throughput test |
| `OPENSYNCCRDT_LOAD_STORM` | `500` | reconnect-storm test |

Example — the full spec targets:

```bash
OPENSYNCCRDT_LOAD=1 \
OPENSYNCCRDT_LOAD_CONNS=1000 \
OPENSYNCCRDT_LOAD_WORKERS=400 \
OPENSYNCCRDT_LOAD_OPS=10000 \
OPENSYNCCRDT_LOAD_DURATION=30s \
OPENSYNCCRDT_LOAD_STORM=500 \
  go test ./tests/load/ -run TestLoad -v -timeout 20m
```

## Interpreting results

- **Throughput** below the target usually means the client (op generation,
  ack round-trips) or the host is the bottleneck, not the server — raise
  `_WORKERS` to add concurrency, or shard across more documents.
- **CPU** is process user+sys time over the measured window (`getrusage`), so
  it is comparable across runs on the same host; watch it alongside external
  tooling (`top`, `docker stats`) for a full picture.
- **Goroutines** should return toward baseline after a scenario; a monotonic
  climb across repeated runs is worth investigating.
