// Package load contains the load / soak harness from SPEC §18. These tests are
// heavy — thousands of live WebSocket connections and tens of thousands of
// operations per second — so they do not run in the normal unit/CI path. They
// self-skip unless OPENSYNCCRDT_LOAD is set:
//
//	OPENSYNCCRDT_LOAD=1 go test ./tests/load/ -run TestLoad -v -timeout 20m
//
// Every scenario is tunable via environment variables (see envInt/envDur below)
// so the same harness can probe a laptop or a production-class host. Each test
// reports latency percentiles (p50/p95/p99), achieved throughput, memory, and
// CPU time via t.Log; they assert only on liveness (connections/ops succeed),
// not on absolute numbers, which are hardware-dependent.
package load

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/automerge/automerge-go"
	"github.com/coder/websocket"

	"github.com/shaunakrananaware/OpenSyncCRDT/pkg/engine"
	"github.com/shaunakrananaware/OpenSyncCRDT/pkg/protocol"
)

// requireLoad skips a test unless the load suite is explicitly enabled.
func requireLoad(t *testing.T) {
	t.Helper()
	if os.Getenv("OPENSYNCCRDT_LOAD") == "" {
		t.Skip("load suite disabled; set OPENSYNCCRDT_LOAD=1 to run")
	}
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDur(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// TestLoad1000Connections holds 1000 simultaneous connections on a single node
// (SPEC §18 / success criterion scale). Tunable: OPENSYNCCRDT_LOAD_CONNS.
func TestLoad1000Connections(t *testing.T) {
	requireLoad(t)
	conns := envInt("OPENSYNCCRDT_LOAD_CONNS", 1000)

	h := newHarness(t)
	before := snapshotResources()

	var (
		wg        sync.WaitGroup
		connected atomic.Int64
		failed    atomic.Int64
		latencies = make([]time.Duration, conns)
		open      = make([]*websocket.Conn, conns)
	)
	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			start := time.Now()
			c, err := h.dial()
			if err != nil {
				failed.Add(1)
				return
			}
			docID := fmt.Sprintf("doc-%d", i)
			if err := h.subscribe(c, docID, fmt.Sprintf("sess-%d", i)); err != nil {
				failed.Add(1)
				_ = c.CloseNow()
				return
			}
			latencies[i] = time.Since(start)
			open[i] = c
			connected.Add(1)
		}(i)
	}
	wg.Wait()

	after := snapshotResources()
	t.Logf("connections: %d established, %d failed (target %d)", connected.Load(), failed.Load(), conns)
	reportLatency(t, "connect+subscribe", nonZero(latencies))
	reportResources(t, before, after)

	if connected.Load() < int64(conns) {
		t.Errorf("only %d/%d connections established", connected.Load(), conns)
	}

	for _, c := range open {
		if c != nil {
			_ = c.CloseNow()
		}
	}
}

// TestLoadSustainedThroughput drives a sustained operation rate across many docs
// and reports achieved ops/sec and per-op latency percentiles (SPEC §18: target
// 10000 ops/sec). Tunable: OPENSYNCCRDT_LOAD_WORKERS, _OPS (target/sec),
// _DURATION.
func TestLoadSustainedThroughput(t *testing.T) {
	requireLoad(t)
	workers := envInt("OPENSYNCCRDT_LOAD_WORKERS", 200)
	targetRate := envInt("OPENSYNCCRDT_LOAD_OPS", 10000)
	duration := envDur("OPENSYNCCRDT_LOAD_DURATION", 10*time.Second)

	h := newHarness(t)
	before := snapshotResources()

	perWorker := targetRate / workers
	if perWorker < 1 {
		perWorker = 1
	}
	interval := time.Second / time.Duration(perWorker)

	var (
		wg        sync.WaitGroup
		totalOps  atomic.Int64
		totalErr  atomic.Int64
		latMu     sync.Mutex
		latencies []time.Duration
	)
	deadline := time.Now().Add(duration)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			c, err := h.dial()
			if err != nil {
				totalErr.Add(1)
				return
			}
			defer c.CloseNow()

			docID := fmt.Sprintf("thr-%d", w)
			sessID := fmt.Sprintf("thr-sess-%d", w)
			if err := h.subscribe(c, docID, sessID); err != nil {
				totalErr.Add(1)
				return
			}

			doc := automerge.New()
			local := make([]time.Duration, 0, 1024)
			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			for time.Now().Before(deadline) {
				<-ticker.C
				_ = doc.RootMap().Set("n", time.Now().UnixNano())
				if _, err := doc.Commit("op"); err != nil {
					totalErr.Add(1)
					continue
				}
				change := doc.SaveIncremental()

				start := time.Now()
				if err := h.write(c, protocol.Inbound{
					Type: protocol.TypeSync, DocID: docID, SessionID: sessID, Payload: change,
				}); err != nil {
					totalErr.Add(1)
					return
				}
				if err := h.readType(c, protocol.TypeAck); err != nil {
					totalErr.Add(1)
					return
				}
				local = append(local, time.Since(start))
				totalOps.Add(1)
			}

			latMu.Lock()
			latencies = append(latencies, local...)
			latMu.Unlock()
		}(w)
	}
	wg.Wait()

	after := snapshotResources()
	elapsed := duration.Seconds()
	achieved := float64(totalOps.Load()) / elapsed
	t.Logf("throughput: %d ops in %.1fs = %.0f ops/sec (target %d, %d workers, %d errors)",
		totalOps.Load(), elapsed, achieved, targetRate, workers, totalErr.Load())
	reportLatency(t, "op ack", latencies)
	reportResources(t, before, after)

	if totalOps.Load() == 0 {
		t.Fatal("no operations completed")
	}
}

// TestLoadReconnectStorm connects a fleet, drops every connection at once, then
// reconnects them all simultaneously — the classic thundering-herd reconnect
// (SPEC §18: 500 clients). Tunable: OPENSYNCCRDT_LOAD_STORM.
func TestLoadReconnectStorm(t *testing.T) {
	requireLoad(t)
	clients := envInt("OPENSYNCCRDT_LOAD_STORM", 500)

	h := newHarness(t)

	// Prime the fleet.
	conns := make([]*websocket.Conn, clients)
	for i := 0; i < clients; i++ {
		c, err := h.dial()
		if err != nil {
			t.Fatalf("initial dial %d: %v", i, err)
		}
		if err := h.subscribe(c, fmt.Sprintf("storm-%d", i), fmt.Sprintf("storm-sess-%d", i)); err != nil {
			t.Fatalf("initial subscribe %d: %v", i, err)
		}
		conns[i] = c
	}

	// Drop everyone at once.
	for _, c := range conns {
		_ = c.CloseNow()
	}

	before := snapshotResources()

	// Reconnect storm: all clients race to re-establish simultaneously.
	var (
		wg        sync.WaitGroup
		reconnect = make([]time.Duration, clients)
		failed    atomic.Int64
		open      = make([]*websocket.Conn, clients)
	)
	gate := make(chan struct{})
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-gate // release all at once
			start := time.Now()
			c, err := h.dial()
			if err != nil {
				failed.Add(1)
				return
			}
			if err := h.subscribe(c, fmt.Sprintf("storm-%d", i), fmt.Sprintf("storm-sess-%d", i)); err != nil {
				failed.Add(1)
				_ = c.CloseNow()
				return
			}
			reconnect[i] = time.Since(start)
			open[i] = c
		}(i)
	}
	close(gate)
	wg.Wait()

	after := snapshotResources()
	t.Logf("reconnect storm: %d clients, %d failed", clients, failed.Load())
	reportLatency(t, "reconnect", nonZero(reconnect))
	reportResources(t, before, after)

	if failed.Load() > 0 {
		t.Errorf("%d/%d reconnections failed", failed.Load(), clients)
	}

	for _, c := range open {
		if c != nil {
			_ = c.CloseNow()
		}
	}
}

// --- harness ---------------------------------------------------------------

type harness struct {
	t   *testing.T
	url string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	eng, err := engine.New(engine.Config{
		Storage: engine.SQLiteStorage(t.TempDir()),
		Auth:    engine.NoAuth(),
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	ts := httptest.NewServer(eng.Handler())
	t.Cleanup(func() {
		ts.Close()
		eng.Close()
	})
	return &harness{t: t, url: "ws" + strings.TrimPrefix(ts.URL, "http") + "/sync"}
}

func (h *harness) dial() (*websocket.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, h.url, &websocket.DialOptions{
		Subprotocols: []string{protocol.SubprotocolJSON},
	})
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(4 << 20)
	return c, nil
}

func (h *harness) write(c *websocket.Conn, v any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	data, _ := json.Marshal(v)
	return c.Write(ctx, websocket.MessageText, data)
}

func (h *harness) subscribe(c *websocket.Conn, docID, sessID string) error {
	if err := h.write(c, protocol.Inbound{Type: protocol.TypeSubscribe, DocID: docID, SessionID: sessID}); err != nil {
		return err
	}
	return h.readType(c, protocol.TypeReplay)
}

// readType reads until a message of the wanted type arrives (skipping others).
func (h *harness) readType(c *websocket.Conn, want protocol.Type) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return err
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		var typ protocol.Type
		_ = json.Unmarshal(m["type"], &typ)
		if typ == want {
			return nil
		}
	}
}

// --- reporting -------------------------------------------------------------

func reportLatency(t *testing.T, label string, samples []time.Duration) {
	t.Helper()
	if len(samples) == 0 {
		t.Logf("%s latency: no samples", label)
		return
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	t.Logf("%s latency: p50=%s p95=%s p99=%s min=%s max=%s (n=%d)",
		label,
		pct(samples, 0.50), pct(samples, 0.95), pct(samples, 0.99),
		samples[0], samples[len(samples)-1], len(samples))
}

func pct(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := int(p * float64(len(sorted)))
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}

func nonZero(ds []time.Duration) []time.Duration {
	out := ds[:0:0]
	for _, d := range ds {
		if d > 0 {
			out = append(out, d)
		}
	}
	return out
}

type resources struct {
	heapAlloc uint64
	goroutine int
	cpu       time.Duration
}

func snapshotResources() resources {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	cpu := time.Duration(ru.Utime.Nano()) + time.Duration(ru.Stime.Nano())
	return resources{heapAlloc: ms.HeapAlloc, goroutine: runtime.NumGoroutine(), cpu: cpu}
}

func reportResources(t *testing.T, before, after resources) {
	t.Helper()
	t.Logf("resources: heap %s -> %s, goroutines %d -> %d, cpu +%s",
		humanBytes(before.heapAlloc), humanBytes(after.heapAlloc),
		before.goroutine, after.goroutine, (after.cpu - before.cpu).Round(time.Millisecond))
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
