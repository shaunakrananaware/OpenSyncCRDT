package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// Metrics is the subset of the metrics registry the dispatcher reports to. It
// is optional; a nil Metrics disables reporting.
type Metrics interface {
	WebhookCall(event string)
	WebhookError(event string)
}

// Dispatcher makes outbound HTTP POST calls to developer-configured URLs when
// events occur. All webhooks are optional: an event with no configured URL is
// silently skipped. Delivery is fire-and-forget (non-blocking) and retried with
// exponential backoff; retry failures are logged but never crash the server.
//
// The blocking webhooks (auth and conflict resolver) are handled elsewhere;
// this dispatcher is only for the fire-and-forget event webhooks.
type Dispatcher struct {
	events     map[string]string // event name -> URL
	secret     string
	maxRetries int
	client     *http.Client
	logger     *slog.Logger
	metrics    Metrics
}

// Config configures a Dispatcher.
type Config struct {
	Events     map[string]string
	Secret     string
	Timeout    time.Duration
	MaxRetries int
	Logger     *slog.Logger
	Metrics    Metrics
}

// NewDispatcher builds a Dispatcher from configuration.
func NewDispatcher(cfg Config) *Dispatcher {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	events := make(map[string]string, len(cfg.Events))
	for k, v := range cfg.Events {
		if v != "" {
			events[k] = v
		}
	}
	return &Dispatcher{
		events:     events,
		secret:     cfg.Secret,
		maxRetries: cfg.MaxRetries,
		client:     &http.Client{Timeout: timeout},
		logger:     logger,
		metrics:    cfg.Metrics,
	}
}

// Emit dispatches an event with the given event-specific fields. The event name
// and an ISO-8601 timestamp are added to the body automatically. If no URL is
// configured for the event, Emit returns immediately. Delivery happens in the
// background.
func (d *Dispatcher) Emit(event string, fields map[string]any) {
	url, ok := d.events[event]
	if !ok {
		return // event not configured: silently skipped
	}

	body := make(map[string]any, len(fields)+2)
	for k, v := range fields {
		body[k] = v
	}
	body["event"] = event
	now := time.Now().UTC()
	body["timestamp"] = now.Format(time.RFC3339)

	payload, err := json.Marshal(body)
	if err != nil {
		d.logger.Error("marshal webhook payload", "event", event, "error", err)
		return
	}

	go d.deliver(event, url, payload, now.Unix())
}

func (d *Dispatcher) deliver(event, url string, payload []byte, ts int64) {
	if d.metrics != nil {
		d.metrics.WebhookCall(event)
	}
	sig := Sign(d.secret, payload)
	tsStr := strconv.FormatInt(ts, 10)

	backoff := 200 * time.Millisecond
	attempts := d.maxRetries + 1 // initial try + retries
	for attempt := 1; attempt <= attempts; attempt++ {
		err := d.post(url, event, payload, sig, tsStr)
		if err == nil {
			return
		}
		if attempt == attempts {
			d.logger.Warn("webhook delivery failed after retries",
				"event", event, "url", url, "attempts", attempts, "error", err)
			if d.metrics != nil {
				d.metrics.WebhookError(event)
			}
			return
		}
		time.Sleep(backoff)
		backoff *= 2
	}
}

func (d *Dispatcher) post(url, event string, payload []byte, sig, ts string) error {
	ctx, cancel := context.WithTimeout(context.Background(), d.client.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-OpenSyncCRDT-Event", event)
	req.Header.Set("X-OpenSyncCRDT-Signature", sig)
	req.Header.Set("X-OpenSyncCRDT-Timestamp", ts)

	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &statusError{code: resp.StatusCode}
	}
	return nil
}

type statusError struct{ code int }

func (e *statusError) Error() string { return "webhook returned status " + strconv.Itoa(e.code) }
