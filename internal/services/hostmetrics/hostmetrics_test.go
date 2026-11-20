package hostmetrics

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/promql"
)

var now = time.Unix(1_800_000_000, 0)

// fakeProm answers by the first rule whose substring the query contains.
type fakeProm struct {
	mu      sync.Mutex
	instant []rule
	ranges  []rangeRule
	err     error
	calls   atomic.Int64
	seen    []string
}

type rule struct {
	has string
	out []promql.Sample
}

type rangeRule struct {
	has string
	out []promql.Series
}

func (f *fakeProm) Query(_ context.Context, q string) ([]promql.Sample, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.seen = append(f.seen, q)
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	for _, r := range f.instant {
		if strings.Contains(q, r.has) {
			return r.out, nil
		}
	}
	return nil, nil
}

func (f *fakeProm) QueryRangeSeries(_ context.Context, q string, start, end time.Time, step time.Duration) ([]promql.Series, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.seen = append(f.seen, q)
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	for _, r := range f.ranges {
		if strings.Contains(q, r.has) {
			return r.out, nil
		}
	}
	return nil, nil
}

func one(v float64) []promql.Sample { return []promql.Sample{{Value: v}} }

func labels(kv ...string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func line(lbl map[string]string, vals ...float64) promql.Series {
	s := promql.Series{Labels: lbl}
	for i, v := range vals {
		s.Points = append(s.Points, promql.Point{T: now.Add(time.Duration(i-len(vals)) * 15 * time.Second), V: v})
	}
	return s
}

func populated() *fakeProm {
	return &fakeProm{
		instant: []rule{
			{`up{job="node"}`, []promql.Sample{{Labels: labels("job", "node", "instance", "node-exporter:9100"), Value: 1}}},
			{`node_uname_info`, []promql.Sample{{Labels: labels("nodename", "boson", "release", "6.8.0-49-generic")}}},
			{`node_time_seconds`, one(86400 * 3)},
			{`count(node_cpu_seconds_total`, one(2)},
			{`avg(rate(node_cpu_seconds_total`, one(37.123456)},
			{`node_memory_MemTotal_bytes`, one(2e9)},
			{`node_memory_MemAvailable_bytes`, one(1.5e8)},
			{`node_memory_SwapTotal_bytes`, one(1e9)},
			{`node_memory_SwapFree_bytes`, one(6e8)},
			{`node_load15`, one(0.8)},
			{`node_load5`, one(1.1)},
			{`node_load1`, one(2.5)},
			{`node_disk_io_time_seconds_total`, one(12)},
			{`node_pressure_memory`, one(math.NaN())},
			{`node_filesystem_size_bytes`, []promql.Sample{
				{Labels: labels("mountpoint", "/data", "device", "/dev/sdb", "fstype", "ext4"), Value: 100e9},
				{Labels: labels("mountpoint", "/", "device", "/dev/sda1", "fstype", "ext4"), Value: 40e9},
			}},
			{`node_filesystem_avail_bytes`, []promql.Sample{
				{Labels: labels("mountpoint", "/data"), Value: 50e9},
				{Labels: labels("mountpoint", "/"), Value: 4e9},
			}},
			{`timestamp(up == 1)`, []promql.Sample{{Labels: labels("job", "quicvideod", "instance", "quicvideod:8080"), Value: float64(now.Add(-time.Hour).Unix())}}},
			{`timestamp(up)`, []promql.Sample{
				{Labels: labels("job", "auth", "instance", "auth:8081"), Value: float64(now.Add(-10 * time.Second).Unix())},
				{Labels: labels("job", "traefik", "instance", "traefik:8080"), Value: float64(now.Add(-4 * time.Minute).Unix())},
				{Labels: labels("job", "quicvideod", "instance", "quicvideod:8080"), Value: float64(now.Add(-5 * time.Second).Unix())},
			}},
			{`avg_over_time(up[`, []promql.Sample{
				{Labels: labels("job", "auth", "instance", "auth:8081"), Value: 100},
				{Labels: labels("job", "quicvideod", "instance", "quicvideod:8080"), Value: 12.5},
			}},
			{`up`, []promql.Sample{
				{Labels: labels("job", "auth", "instance", "auth:8081"), Value: 1},
				{Labels: labels("job", "traefik", "instance", "traefik:8080"), Value: 1},
				{Labels: labels("job", "quicvideod", "instance", "quicvideod:8080"), Value: 0},
				{Labels: labels("job", "node", "instance", "node-exporter:9100"), Value: 1},
			}},
		},
		ranges: []rangeRule{
			{`sum by (mode)`, []promql.Series{
				line(labels("mode", "user"), 10, 20),
				line(labels("mode", "nice"), 1, 1),
				line(labels("mode", "system"), 5, 5),
				line(labels("mode", "softirq"), 1, 2),
				line(labels("mode", "iowait"), 3, 0),
				line(labels("mode", "steal"), 0, 0.1),
			}},
			{`node_memory_MemAvailable_bytes`, []promql.Series{line(nil, 90, 92.5)}},
			{`node_memory_SwapFree_bytes`, []promql.Series{line(nil, 40, math.NaN(), 41)}},
			{`node_load1{`, []promql.Series{line(nil, 2, 2.5)}},
			{`node_load5{`, []promql.Series{line(nil, 1, 1.1)}},
			{`node_load15{`, []promql.Series{line(nil, 0.7, 0.8)}},
			{`node_filesystem_avail_bytes`, []promql.Series{
				line(labels("mountpoint", "/data"), 50, 50),
				line(labels("mountpoint", "/"), 89, 90),
			}},
			{`node_disk_read_bytes_total`, []promql.Series{line(nil, 1e6, 2e6)}},
			{`node_disk_written_bytes_total`, []promql.Series{line(nil, 3e6, 4e6)}},
			{`node_network_receive_bytes_total`, []promql.Series{
				line(labels("device", "eth0"), 1e5, 2e5),
				line(labels("device", "ens4"), 1, 1),
				line(labels("device", "wg0"), 1e4, 1e4),
			}},
			{`node_network_transmit_bytes_total`, []promql.Series{
				line(labels("device", "eth0"), 3e5, 4e5),
				line(labels("device", "ens4"), 1, 1),
				line(labels("device", "wg0"), 1e4, 1e4),
			}},
		},
	}
}

func chart(t *testing.T, h *Host, key string) Chart {
	t.Helper()
	for _, c := range h.Charts {
		if c.Key == key {
			return c
		}
	}
	t.Fatalf("no chart %q", key)
	return Chart{}
}

func names(c Chart) string {
	var n []string
	for _, s := range c.Series {
		n = append(n, s.Name)
	}
	return strings.Join(n, ",")
}

func TestHostData(t *testing.T) {
	f := populated()
	s := New(Options{Prom: f, Now: func() time.Time { return now }})
	v, err := s.Host(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !v.Configured || !v.NodeExporter || v.Error != "" || v.Range != "1h" || v.Step != 15 || v.Instance != "node-exporter:9100" {
		t.Fatalf("view: %+v", v)
	}
	if !v.End.Equal(now) || !v.Start.Equal(now.Add(-time.Hour)) {
		t.Fatalf("range %v–%v", v.Start, v.End)
	}
	h := v.Host
	if h.Hostname != "boson" || h.Kernel != "6.8.0-49-generic" {
		t.Fatalf("uname: %+v", h)
	}
	st := h.Stats
	if *st.UptimeSeconds != 259200 || *st.Cores != 2 || *st.CPUPercent != 37.123 || *st.MemTotalBytes != 2e9 ||
		*st.SwapFreeBytes != 6e8 || *st.Load1 != 2.5 || *st.Load15 != 0.8 || *st.DiskBusyPercent != 12 {
		t.Fatalf("stats: %+v", st)
	}
	if st.PressureMemory != nil || st.PressureCPU != nil {
		t.Fatal("NaN or missing pressure should be null")
	}
	if len(h.Filesystems) != 2 || h.Filesystems[0].Mountpoint != "/" || h.Filesystems[0].AvailBytes != 4e9 || h.Filesystems[1].FSType != "ext4" {
		t.Fatalf("filesystems: %+v", h.Filesystems)
	}

	cpu := chart(t, h, "cpu")
	if !cpu.Stacked || cpu.Max == nil || *cpu.Max != 100 || names(cpu) != "user,system,iowait" {
		t.Fatalf("cpu: %+v", cpu)
	}
	if cpu.Series[0].Points[1][1] != 21 || cpu.Series[1].Points[1][1] != 7 {
		t.Fatalf("cpu folding: %+v", cpu.Series)
	}
	if m := chart(t, h, "memory"); names(m) != "Memory,Swap" || len(m.Series[1].Points) != 2 {
		t.Fatalf("memory (NaN dropped): %+v", m)
	}
	if l := chart(t, h, "load"); names(l) != "1 min,5 min,15 min" || l.Reference == nil || l.Reference.Value != 2 {
		t.Fatalf("load: %+v", l)
	}
	if d := chart(t, h, "disk_used"); names(d) != "/,/data" {
		t.Fatalf("disk used: %s", names(d))
	}
	if d := chart(t, h, "disk_io"); names(d) != "Read,Write" {
		t.Fatalf("disk io: %s", names(d))
	}
	// The two busiest interfaces, in name order.
	if n := chart(t, h, "network"); names(n) != "eth0 in,eth0 out,wg0 in,wg0 out" {
		t.Fatalf("network: %s", names(n))
	}

	// Targets: down first, then stale, then up; last-up only while down.
	var got []string
	for _, tg := range v.Targets {
		got = append(got, tg.Job+":"+tg.Status)
	}
	if strings.Join(got, " ") != "quicvideod:down traefik:stale auth:up node:up" {
		t.Fatalf("targets: %v", got)
	}
	if v.Targets[0].LastUp == nil || *v.Targets[0].Availability != 12.5 || v.Targets[3].LastUp != nil || v.Targets[2].LastScrape == nil {
		t.Fatalf("target details: %+v", v.Targets)
	}

	// Every query is fixed and scoped to the instance.
	for _, q := range f.seen {
		if strings.HasPrefix(q, "node_") || strings.Contains(q, "(node_") {
			if !strings.Contains(q, `instance="node-exporter:9100"`) {
				t.Errorf("unscoped query: %s", q)
			}
		}
	}
}

func TestNoNodeExporter(t *testing.T) {
	f := populated()
	f.instant[0].out = nil // up{job="node"}: nothing
	s := New(Options{Prom: f, Now: func() time.Time { return now }})
	v, err := s.Host(context.Background(), "24h", "")
	if err != nil {
		t.Fatal(err)
	}
	if !v.Configured || v.NodeExporter || v.Host != nil || v.Error != "" || len(v.Instances) != 0 || v.Step != 360 {
		t.Fatalf("view: %+v", v)
	}
	if len(v.Targets) != 4 {
		t.Fatalf("targets should still show: %+v", v.Targets)
	}
}

func TestUnconfigured(t *testing.T) {
	s := New(Options{})
	v, err := s.Host(context.Background(), "7d", "")
	if err != nil || v.Configured || s.Configured() || v.Targets == nil || v.Instances == nil {
		t.Fatalf("%v %+v", err, v)
	}
}

func TestBackendError(t *testing.T) {
	f := &fakeProm{err: errors.New("metrics backend unreachable: connection refused")}
	s := New(Options{Prom: f, Now: func() time.Time { return now }})
	v, err := s.Host(context.Background(), "1h", "")
	if err != nil {
		t.Fatal(err)
	}
	if v.Error == "" || v.TargetsError == "" || v.Host != nil || v.NodeExporter {
		t.Fatalf("view: %+v", v)
	}
	// Failures aren't cached.
	n := f.calls.Load()
	_, _ = s.Host(context.Background(), "1h", "")
	if f.calls.Load() == n {
		t.Fatal("an error was cached")
	}
}

func TestBadInput(t *testing.T) {
	s := New(Options{Prom: populated(), Now: func() time.Time { return now }})
	_, err := s.Host(context.Background(), "5m", "")
	if e, ok := apperr.As(err); !ok || e.Status != 400 {
		t.Fatalf("range: %v", err)
	}
	if _, err := s.Host(context.Background(), "1h", "elsewhere:9100"); err != ErrUnknownInstance {
		t.Fatalf("instance: %v", err)
	}
}

func TestCache(t *testing.T) {
	f := populated()
	at := now
	s := New(Options{Prom: f, Now: func() time.Time { return at }})
	if _, err := s.Host(context.Background(), "6h", ""); err != nil {
		t.Fatal(err)
	}
	n := f.calls.Load()
	if _, err := s.Host(context.Background(), "6h", ""); err != nil || f.calls.Load() != n {
		t.Fatalf("not cached: %v", err)
	}
	at = at.Add(11 * time.Second)
	if _, _ = s.Host(context.Background(), "6h", ""); f.calls.Load() == n {
		t.Fatal("cache never expires")
	}
}

func TestWindows(t *testing.T) {
	for _, w := range Windows {
		if pts := w.Span / w.Step; pts < 200 || pts > 300 {
			t.Errorf("%s: %d points", w.Key, pts)
		}
		if w.RateWindow() < time.Minute || w.RateWindow() < w.Step {
			t.Errorf("%s: rate window %v", w.Key, w.RateWindow())
		}
	}
	for _, q := range Queries() {
		if strings.Contains(q, "{sel}") || strings.Contains(q, "{w}") {
			t.Errorf("unfilled: %s", q)
		}
	}
}
