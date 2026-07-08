// Package metrics collects the Prometheus-compatible metrics exposed at
// GET /api/v1/metrics. It is a tiny, dependency-free registry: the spec forbids
// non-essential dependencies, so we render the Prometheus text exposition
// format by hand rather than pulling in the client library.
package metrics

import (
	"fmt"
	"sort"
	"strings"
	stdsync "sync"
	"sync/atomic"
	"time"
)

// Registry holds all engine metrics. The zero value is not usable; call New.
type Registry struct {
	connectionsTotal  atomic.Int64
	connectionsActive atomic.Int64
	operationsErrors  atomic.Int64
	replayOperations  atomic.Int64

	storageQueryCount atomic.Int64
	storageQuerySum   atomic.Uint64 // seconds * 1e9 accumulated as nanoseconds

	mu            stdsync.Mutex
	operations    map[string]int64 // by doc_id
	webhookCalls  map[string]int64 // by event
	webhookErrors map[string]int64 // by event
}

// New returns an initialized Registry.
func New() *Registry {
	return &Registry{
		operations:    make(map[string]int64),
		webhookCalls:  make(map[string]int64),
		webhookErrors: make(map[string]int64),
	}
}

// ConnectionOpened records a new WebSocket connection.
func (r *Registry) ConnectionOpened() {
	r.connectionsTotal.Add(1)
	r.connectionsActive.Add(1)
}

// ConnectionClosed records a closed WebSocket connection.
func (r *Registry) ConnectionClosed() { r.connectionsActive.Add(-1) }

// Operation records a committed operation on a document.
func (r *Registry) Operation(docID string) {
	r.mu.Lock()
	r.operations[docID]++
	r.mu.Unlock()
}

// OperationError records a failed operation.
func (r *Registry) OperationError() { r.operationsErrors.Add(1) }

// ReplayOps records operations delivered during replay.
func (r *Registry) ReplayOps(n int) { r.replayOperations.Add(int64(n)) }

// WebhookCall records a webhook delivery attempt for an event.
func (r *Registry) WebhookCall(event string) {
	r.mu.Lock()
	r.webhookCalls[event]++
	r.mu.Unlock()
}

// WebhookError records a webhook delivery failure for an event.
func (r *Registry) WebhookError(event string) {
	r.mu.Lock()
	r.webhookErrors[event]++
	r.mu.Unlock()
}

// StorageQuery records the duration of a storage query.
func (r *Registry) StorageQuery(d time.Duration) {
	r.storageQueryCount.Add(1)
	r.storageQuerySum.Add(uint64(d.Nanoseconds()))
}

// Render returns the metrics in Prometheus text exposition format.
func (r *Registry) Render() string {
	var b strings.Builder

	writeCounter(&b, "opensynccrdt_connections_total",
		"Total WebSocket connections opened.", r.connectionsTotal.Load())
	writeGauge(&b, "opensynccrdt_connections_active",
		"WebSocket connections currently open.", r.connectionsActive.Load())

	r.mu.Lock()
	ops := snapshot(r.operations)
	calls := snapshot(r.webhookCalls)
	errs := snapshot(r.webhookErrors)
	r.mu.Unlock()

	writeLabeledCounter(&b, "opensynccrdt_operations_total",
		"Total committed operations.", "doc_id", ops)
	writeCounter(&b, "opensynccrdt_operations_errors_total",
		"Total operations that failed to apply.", r.operationsErrors.Load())
	writeLabeledCounter(&b, "opensynccrdt_webhook_calls_total",
		"Total webhook delivery attempts.", "event", calls)
	writeLabeledCounter(&b, "opensynccrdt_webhook_errors_total",
		"Total webhook deliveries that failed after retries.", "event", errs)

	// storage_query_duration_seconds as a summary (_count/_sum).
	count := r.storageQueryCount.Load()
	sumSeconds := float64(r.storageQuerySum.Load()) / 1e9
	fmt.Fprintf(&b, "# HELP opensynccrdt_storage_query_duration_seconds Storage query duration.\n")
	fmt.Fprintf(&b, "# TYPE opensynccrdt_storage_query_duration_seconds summary\n")
	fmt.Fprintf(&b, "opensynccrdt_storage_query_duration_seconds_count %d\n", count)
	fmt.Fprintf(&b, "opensynccrdt_storage_query_duration_seconds_sum %g\n", sumSeconds)

	writeCounter(&b, "opensynccrdt_replay_operations_total",
		"Total operations delivered during replay.", r.replayOperations.Load())

	return b.String()
}

func snapshot(m map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func writeCounter(b *strings.Builder, name, help string, v int64) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, help, name, name, v)
}

func writeGauge(b *strings.Builder, name, help string, v int64) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s gauge\n%s %d\n", name, help, name, name, v)
}

func writeLabeledCounter(b *strings.Builder, name, help, label string, values map[string]int64) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s counter\n", name, help, name)
	if len(values) == 0 {
		return
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(b, "%s{%s=%q} %d\n", name, label, k, values[k])
	}
}
