package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/clients/promql"
	"github.com/boson-chat/admin/internal/services/admin"
	"github.com/boson-chat/admin/internal/services/hostmetrics"
)

// promStub answers every query with the same samples (or error).
type promStub struct {
	samples []promql.Sample
	err     error
}

func (p promStub) Query(context.Context, string) ([]promql.Sample, error) { return p.samples, p.err }
func (p promStub) QueryRangeSeries(context.Context, string, time.Time, time.Time, time.Duration) ([]promql.Series, error) {
	return []promql.Series{{Points: []promql.Point{{T: time.Unix(1_800_000_000, 0), V: 42}}}}, p.err
}

func decodeView(t *testing.T, body string) hostmetrics.View {
	t.Helper()
	var v hostmetrics.View
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return v
}

func TestHostMetrics(t *testing.T) {
	h := newHarness(t)
	c, _ := h.login()

	// Without METRICS_URL: an explicit "not configured", not an error.
	w := h.do("GET", "/api/metrics/host", nil, withCookie(c))
	if v := decodeView(t, w.Body.String()); w.Code != 200 || v.Configured || v.Range != "1h" {
		t.Fatalf("unconfigured: %d %s", w.Code, w.Body)
	}

	// Data: every query answers for the one node target.
	h.h.HostMetrics = hostmetrics.New(hostmetrics.Options{TTL: -1, Prom: promStub{samples: []promql.Sample{
		{Labels: map[string]string{"job": "node", "instance": "node-exporter:9100", "mountpoint": "/"}, Value: 1},
	}}})
	w = h.do("GET", "/api/metrics/host?range=6h&instance=node-exporter:9100", nil, withCookie(c))
	v := decodeView(t, w.Body.String())
	if w.Code != 200 || !v.Configured || !v.NodeExporter || v.Host == nil || v.Range != "6h" || v.Step != 90 || len(v.Host.Charts) != 6 {
		t.Fatalf("data: %d %s", w.Code, w.Body)
	}
	if len(v.Targets) != 1 || v.Targets[0].Job != "node" || v.Host.Stats.Cores == nil {
		t.Fatalf("targets/stats: %+v", v)
	}

	// No node-exporter yet: configured, no host.
	h.h.HostMetrics = hostmetrics.New(hostmetrics.Options{TTL: -1, Prom: promStub{}})
	w = h.do("GET", "/api/metrics/host?range=7d", nil, withCookie(c))
	if v := decodeView(t, w.Body.String()); w.Code != 200 || !v.Configured || v.NodeExporter || v.Host != nil || !strings.Contains(w.Body.String(), `"targets":[]`) {
		t.Fatalf("no data: %d %s", w.Code, w.Body)
	}

	// The backend is down: 200 with the error, so the page can say so.
	h.h.HostMetrics = hostmetrics.New(hostmetrics.Options{TTL: -1, Prom: promStub{err: errors.New("metrics backend unreachable")}})
	w = h.do("GET", "/api/metrics/host", nil, withCookie(c))
	if v := decodeView(t, w.Body.String()); w.Code != 200 || v.Error != "metrics backend unreachable" {
		t.Fatalf("error: %d %s", w.Code, w.Body)
	}

	// Bad input is a 400, never a query.
	for _, p := range []string{"/api/metrics/host?range=1y", "/api/metrics/host?range=1h&instance=evil%22})"} {
		h.h.HostMetrics = hostmetrics.New(hostmetrics.Options{TTL: -1, Prom: promStub{samples: []promql.Sample{
			{Labels: map[string]string{"job": "node", "instance": "node-exporter:9100"}, Value: 1},
		}}})
		if w := h.do("GET", p, nil, withCookie(c)); w.Code != 400 || errCode(t, w.Body.String()) != "invalid_request" {
			t.Fatalf("%s: %d %s", p, w.Code, w.Body)
		}
	}
}

func TestHostMetricsPermission(t *testing.T) {
	h := newHarness(t)
	c, _ := h.moderator()
	w := h.do("GET", "/api/metrics/host", nil, withCookie(c))
	if w.Code != 403 || errCode(t, w.Body.String()) != "missing_permission" || !strings.Contains(w.Body.String(), admin.PermServicesView) {
		t.Fatalf("moderator: %d %s", w.Code, w.Body)
	}
	if w := h.do("GET", "/api/metrics/host", nil); w.Code != 401 {
		t.Fatalf("anonymous: %d", w.Code)
	}
}
