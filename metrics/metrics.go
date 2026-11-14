// Package metrics defines the panel's OpenTelemetry instruments and the
// meter provider that exports them.
package metrics

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// ScopeName is the instrumentation scope of every instrument.
const ScopeName = "github.com/boson-chat/admin"

// Outcome labels (fixed set, bounded cardinality).
const (
	OutcomeSuccess            = "success"
	OutcomeShared             = "shared" // refresh answered by an in-flight or recent refresh
	OutcomeInvalidCredentials = "invalid_credentials"
	OutcomeNotAdmin           = "not_admin"
	OutcomeExpired            = "expired"
	OutcomeRateLimited        = "rate_limited"
	OutcomeError              = "error"
)

// Metrics holds the instruments. Use New or Noop.
type Metrics struct {
	httpRequests     metric.Int64Counter
	httpDuration     metric.Float64Histogram
	upstreamRequests metric.Int64Counter
	upstreamDuration metric.Float64Histogram
	logins           metric.Int64Counter
	refreshes        metric.Int64Counter
	serviceActions   metric.Int64Counter
	settingsChanges  metric.Int64Counter
}

// New creates the instruments on mp.
func New(mp metric.MeterProvider) (*Metrics, error) {
	m := mp.Meter(ScopeName)
	var err error
	counter := func(name, unit, desc string) metric.Int64Counter {
		if err != nil {
			return nil
		}
		var c metric.Int64Counter
		c, err = m.Int64Counter(name, metric.WithUnit(unit), metric.WithDescription(desc))
		return c
	}
	hist := func(name, desc string) metric.Float64Histogram {
		if err != nil {
			return nil
		}
		var h metric.Float64Histogram
		h, err = m.Float64Histogram(name, metric.WithUnit("s"), metric.WithDescription(desc),
			metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10))
		return h
	}
	out := &Metrics{
		httpRequests:     counter("admin.http.requests", "{request}", "Panel HTTP requests by route and status class."),
		httpDuration:     hist("admin.http.duration", "Panel HTTP request duration by route."),
		upstreamRequests: counter("admin.upstream.requests", "{request}", "Requests to upstream services by upstream, operation and outcome."),
		upstreamDuration: hist("admin.upstream.duration", "Upstream request duration by upstream."),
		logins:           counter("admin.logins", "{login}", "Panel sign-ins by outcome."),
		refreshes:        counter("admin.token.refreshes", "{refresh}", "Access-token refreshes by outcome."),
		serviceActions:   counter("admin.service.actions", "{action}", "Container start/stop/restart requests by action and outcome."),
		// Prometheus: admin_settings_changes_total.
		settingsChanges: counter("admin.settings.changes", "{change}", "Saves to the stack config file by section and outcome."),
	}
	if err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}
	return out, nil
}

// Noop returns instruments that record nothing.
func Noop() *Metrics {
	m, _ := New(noop.NewMeterProvider())
	return m
}

func statusClass(status int) string {
	if status < 100 {
		return "error"
	}
	return strconv.Itoa(status/100) + "xx"
}

// HTTPRequest records one served request. route is the mux pattern.
func (m *Metrics) HTTPRequest(ctx context.Context, route string, status int, d time.Duration) {
	m.httpRequests.Add(ctx, 1, metric.WithAttributes(attribute.String("http.route", route), attribute.String("status_class", statusClass(status))))
	m.httpDuration.Record(ctx, d.Seconds(), metric.WithAttributes(attribute.String("http.route", route)))
}

// Upstream records one upstream request. op comes from a fixed set per upstream.
func (m *Metrics) Upstream(ctx context.Context, upstream, op string, status int, err error, d time.Duration) {
	outcome := statusClass(status)
	if err != nil && status == 0 {
		outcome = "error"
	}
	m.upstreamRequests.Add(ctx, 1, metric.WithAttributes(attribute.String("upstream", upstream), attribute.String("op", op), attribute.String("outcome", outcome)))
	m.upstreamDuration.Record(ctx, d.Seconds(), metric.WithAttributes(attribute.String("upstream", upstream)))
}

func (m *Metrics) Login(ctx context.Context, outcome string) {
	m.logins.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}

func (m *Metrics) Refresh(ctx context.Context, outcome string) {
	m.refreshes.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}

// ServiceAction records a container start/stop/restart. outcome is
// success, refused (typed confirmation missing) or error.
func (m *Metrics) ServiceAction(ctx context.Context, action, outcome string) {
	m.serviceActions.Add(ctx, 1, metric.WithAttributes(attribute.String("action", action), attribute.String("outcome", outcome)))
}

// SettingsChange records a save to the stack config file, per section it
// touched (auth, admin, …). outcome is success, invalid, conflict,
// invalid_file or error.
func (m *Metrics) SettingsChange(ctx context.Context, section, outcome string) {
	m.settingsChanges.Add(ctx, 1, metric.WithAttributes(attribute.String("section", section), attribute.String("outcome", outcome)))
}
