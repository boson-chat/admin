// Package hostmetrics is the Metrics page: the VPS's health from
// node-exporter (scraped as job="node") and every scrape target's up/down,
// read from the metrics backend (METRICS_URL).
//
// The browser never sends PromQL: the page asks for a range and an instance
// and gets the answers to a fixed set of queries.
package hostmetrics

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/promql"
)

// NodeJob is the scrape job node-exporter runs under.
const NodeJob = "node"

// Target states.
const (
	StatusUp    = "up"
	StatusDown  = "down"
	StatusStale = "stale" // no scrape for StaleAfter
)

// StaleAfter: a target whose last scrape is older than this is "stale"
// (the backend still answers with its last sample for a few minutes).
const StaleAfter = 2 * time.Minute

// Window is one selectable time range.
type Window struct {
	Key  string
	Span time.Duration
	// Step between chart points (about 240 per chart).
	Step time.Duration
}

// Windows are the ranges the page offers; the first is the default.
var Windows = []Window{
	{"1h", time.Hour, 15 * time.Second},
	{"6h", 6 * time.Hour, 90 * time.Second},
	{"24h", 24 * time.Hour, 6 * time.Minute},
	{"7d", 7 * 24 * time.Hour, 42 * time.Minute},
}

// RateWindow is the rate() window for a step: two steps, at least a minute
// (four 15 s scrapes), so every point averages over the gap since the last.
func (w Window) RateWindow() time.Duration { return max(2*w.Step, time.Minute) }

// WindowFor parses ?range= ("" = the default).
func WindowFor(key string) (Window, error) {
	if key == "" {
		return Windows[0], nil
	}
	for _, w := range Windows {
		if w.Key == key {
			return w, nil
		}
	}
	keys := make([]string, len(Windows))
	for i, w := range Windows {
		keys[i] = w.Key
	}
	return Window{}, apperr.InvalidField("range", "range must be one of "+strings.Join(keys, ", "))
}

// View is GET /api/metrics/host.
type View struct {
	// Configured: METRICS_URL is set.
	Configured bool `json:"configured"`
	// Error: the metrics backend didn't answer (every part failed).
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
	Range     string    `json:"range"`
	// Step is the seconds between chart points.
	Step  float64   `json:"step"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// NodeExporter: the backend has job="node" targets; without them Host
	// is nil and the page explains how to turn it on.
	NodeExporter bool `json:"node_exporter"`
	// Instances are the node targets; Instance is the one shown.
	Instances []string `json:"instances"`
	Instance  string   `json:"instance,omitempty"`
	Host      *Host    `json:"host,omitempty"`
	// Targets are every scrape target's health (the up metric).
	Targets []Target `json:"targets"`
	// TargetsError: the up queries failed (the host part may still work).
	TargetsError string `json:"targets_error,omitempty"`
}

// Host is one node-exporter target.
type Host struct {
	Hostname    string       `json:"hostname,omitempty"`
	Kernel      string       `json:"kernel,omitempty"`
	Stats       Stats        `json:"stats"`
	Filesystems []Filesystem `json:"filesystems"`
	Charts      []Chart      `json:"charts"`
}

// Stats are the instant numbers (nil = no data).
type Stats struct {
	UptimeSeconds     *float64 `json:"uptime_seconds"`
	Cores             *float64 `json:"cores"`
	CPUPercent        *float64 `json:"cpu_percent"`
	MemTotalBytes     *float64 `json:"mem_total_bytes"`
	MemAvailableBytes *float64 `json:"mem_available_bytes"`
	SwapTotalBytes    *float64 `json:"swap_total_bytes"`
	SwapFreeBytes     *float64 `json:"swap_free_bytes"`
	Load1             *float64 `json:"load1"`
	Load5             *float64 `json:"load5"`
	Load15            *float64 `json:"load15"`
	DiskBusyPercent   *float64 `json:"disk_busy_percent"`
	// Pressure (PSI): the share of time some tasks waited, in percent.
	PressureCPU    *float64 `json:"pressure_cpu"`
	PressureMemory *float64 `json:"pressure_memory"`
	PressureIO     *float64 `json:"pressure_io"`
}

// Filesystem is one real mount (tmpfs, overlay and Docker's own excluded).
type Filesystem struct {
	Mountpoint string  `json:"mountpoint"`
	Device     string  `json:"device,omitempty"`
	FSType     string  `json:"fstype,omitempty"`
	SizeBytes  float64 `json:"size_bytes"`
	AvailBytes float64 `json:"avail_bytes"`
}

// Chart units.
const (
	UnitPercent     = "percent"
	UnitBytesPerSec = "bytes_per_sec"
	UnitLoad        = "load"
)

// Chart is one time-series chart.
type Chart struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Unit  string `json:"unit"`
	// Stacked: the series are parts of a whole (CPU by mode).
	Stacked bool `json:"stacked,omitempty"`
	// Max fixes the y-axis top (100 for percentages).
	Max *float64 `json:"max,omitempty"`
	// Reference is a labelled horizontal line (the core count on load).
	Reference *Reference `json:"reference,omitempty"`
	Series    []Series   `json:"series"`
	Error     string     `json:"error,omitempty"`
}

type Reference struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
}

// Series is one line: Points are [unix seconds, value].
type Series struct {
	Name   string       `json:"name"`
	Points [][2]float64 `json:"points"`
}

// Target is one scrape target.
type Target struct {
	Job      string `json:"job"`
	Instance string `json:"instance"`
	Status   string `json:"status"`
	// LastScrape is the newest sample of up.
	LastScrape *time.Time `json:"last_scrape,omitempty"`
	// LastUp is the last time it was up within the range (when it's down).
	LastUp *time.Time `json:"last_up,omitempty"`
	// Availability is the share of scrapes that succeeded over the range, 0–100.
	Availability *float64 `json:"availability,omitempty"`
}

type ServiceImpl interface {
	Configured() bool
	// Host answers GET /api/metrics/host (instance "" = the first).
	Host(ctx context.Context, rangeKey, instance string) (*View, error)
}

type Options struct {
	// Prom is nil when METRICS_URL is empty.
	Prom promql.RangeClient
	Now  func() time.Time
	// TTL caches answers per range and instance: several open tabs poll
	// every 30 s and each answer is ~25 queries. 0 = 10 s; <0 = off.
	TTL time.Duration
}

type Service struct {
	o     Options
	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	at   time.Time
	view *View
}

func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.TTL == 0 {
		o.TTL = 10 * time.Second
	}
	return &Service{o: o, cache: map[string]cached{}}
}

func (s *Service) Configured() bool { return s.o.Prom != nil }

// ErrUnknownInstance: ?instance= isn't a node target.
var ErrUnknownInstance = apperr.New(http.StatusBadRequest, "invalid_request", "no node-exporter target has that instance")

func (s *Service) Host(ctx context.Context, rangeKey, instance string) (*View, error) {
	w, err := WindowFor(rangeKey)
	if err != nil {
		return nil, err
	}
	now := s.o.Now()
	if s.o.Prom == nil {
		return &View{CheckedAt: now.UTC(), Range: w.Key, Step: w.Step.Seconds(), Instances: []string{}, Targets: []Target{}}, nil
	}
	key := w.Key + "|" + instance
	if s.o.TTL > 0 {
		s.mu.Lock()
		c, ok := s.cache[key]
		s.mu.Unlock()
		if ok && now.Sub(c.at) < s.o.TTL {
			return c.view, nil
		}
	}
	v, err := s.build(ctx, w, instance, now)
	if err != nil {
		return nil, err
	}
	if s.o.TTL > 0 && v.Error == "" {
		s.mu.Lock()
		for k, c := range s.cache {
			if now.Sub(c.at) >= s.o.TTL {
				delete(s.cache, k)
			}
		}
		s.cache[key] = cached{at: now, view: v}
		s.mu.Unlock()
	}
	return v, nil
}

func (s *Service) build(ctx context.Context, w Window, instance string, now time.Time) (*View, error) {
	// Align the range to the step so successive polls draw the same points.
	end := now.Truncate(w.Step)
	v := &View{Configured: true, CheckedAt: now.UTC(), Range: w.Key, Step: w.Step.Seconds(),
		Start: end.Add(-w.Span).UTC(), End: end.UTC(), Instances: []string{}, Targets: []Target{}}

	var wg sync.WaitGroup
	wg.Go(func() { v.Targets, v.TargetsError = s.targets(ctx, w, now) })

	nodes, err := s.o.Prom.Query(ctx, fmt.Sprintf(`up{job=%q}`, NodeJob))
	if err != nil {
		wg.Wait()
		v.Error = err.Error()
		return v, nil
	}
	for _, n := range nodes {
		if in := n.Labels["instance"]; in != "" && !slices.Contains(v.Instances, in) {
			v.Instances = append(v.Instances, in)
		}
	}
	slices.Sort(v.Instances)
	if len(v.Instances) == 0 {
		wg.Wait()
		return v, nil
	}
	v.NodeExporter = true
	switch {
	case instance == "":
		instance = v.Instances[0]
	case !slices.Contains(v.Instances, instance):
		wg.Wait()
		return nil, ErrUnknownInstance
	}
	v.Instance = instance
	v.Host = s.host(ctx, w, instance, end)
	wg.Wait()
	return v, nil
}

// Filters for the series that describe the machine, not containers.
const (
	fsFilter   = `fstype!~"tmpfs|overlay|squashfs|ramfs|devtmpfs|nsfs|autofs|proc|sysfs|fuse.*",mountpoint!~"/(run|var/lib/docker|snap)(/.*)?|/boot/efi"`
	diskFilter = `device!~"(dm-|loop|ram|zram|sr|fd).*"`
	netFilter  = `device!~"lo|veth.*|docker.*|br-.*|virbr.*|cni.*|flannel.*|cali.*|vxlan.*"`
)

// MaxFilesystems and MaxInterfaces cap the lines per chart (largest
// filesystems, busiest interfaces) so colours stay tellable apart.
const (
	MaxFilesystems = 4
	MaxInterfaces  = 2
)

// q fills a query template: {sel} is the instance's selector body, {w} the rate window.
func q(tmpl, sel string, rate time.Duration) string {
	return strings.NewReplacer("{sel}", sel, "{w}", fmt.Sprintf("%ds", int(rate.Seconds()))).Replace(tmpl)
}

// Instant queries ({w} = 1m: the "now" numbers).
var instantDefs = []struct {
	query string
	set   func(*Stats, *float64)
}{
	{`node_time_seconds{{sel}} - node_boot_time_seconds{{sel}}`, func(s *Stats, v *float64) { s.UptimeSeconds = v }},
	{`count(node_cpu_seconds_total{{sel},mode="idle"})`, func(s *Stats, v *float64) { s.Cores = v }},
	{`100 * (1 - avg(rate(node_cpu_seconds_total{{sel},mode="idle"}[{w}])))`, func(s *Stats, v *float64) { s.CPUPercent = v }},
	{`node_memory_MemTotal_bytes{{sel}}`, func(s *Stats, v *float64) { s.MemTotalBytes = v }},
	{`node_memory_MemAvailable_bytes{{sel}}`, func(s *Stats, v *float64) { s.MemAvailableBytes = v }},
	{`node_memory_SwapTotal_bytes{{sel}}`, func(s *Stats, v *float64) { s.SwapTotalBytes = v }},
	{`node_memory_SwapFree_bytes{{sel}}`, func(s *Stats, v *float64) { s.SwapFreeBytes = v }},
	{`node_load1{{sel}}`, func(s *Stats, v *float64) { s.Load1 = v }},
	{`node_load5{{sel}}`, func(s *Stats, v *float64) { s.Load5 = v }},
	{`node_load15{{sel}}`, func(s *Stats, v *float64) { s.Load15 = v }},
	{`100 * max(rate(node_disk_io_time_seconds_total{{sel},` + diskFilter + `}[{w}]))`, func(s *Stats, v *float64) { s.DiskBusyPercent = v }},
	{`100 * rate(node_pressure_cpu_waiting_seconds_total{{sel}}[{w}])`, func(s *Stats, v *float64) { s.PressureCPU = v }},
	{`100 * rate(node_pressure_memory_waiting_seconds_total{{sel}}[{w}])`, func(s *Stats, v *float64) { s.PressureMemory = v }},
	{`100 * rate(node_pressure_io_waiting_seconds_total{{sel}}[{w}])`, func(s *Stats, v *float64) { s.PressureIO = v }},
}

// Queries is every PromQL the page runs, for the README and tests.
func Queries() []string {
	sel := fmt.Sprintf(`job=%q,instance="<instance>"`, NodeJob)
	var out []string
	for _, d := range instantDefs {
		out = append(out, q(d.query, sel, time.Minute))
	}
	for _, c := range chartDefs {
		for _, cq := range c.queries {
			out = append(out, q(cq.query, sel, Windows[0].RateWindow()))
		}
	}
	return out
}

type chartQuery struct {
	query string
	// name labels the one series; by names each series by that label.
	name, by string
}

type chartDef struct {
	key, title, unit string
	stacked          bool
	max              float64
	queries          []chartQuery
	// fold regroups series (CPU modes) or picks the ones shown.
	fold func(in []Series, h *Host) []Series
}

var hundred = 100.0

// CPU modes folded into four lines (guest time is already in user).
var cpuModes = []struct{ name, modes string }{
	{"user", "user nice"},
	{"system", "system irq softirq"},
	{"iowait", "iowait"},
	{"steal", "steal"},
}

var chartDefs = []chartDef{
	{key: "cpu", title: "CPU", unit: UnitPercent, stacked: true, max: 100,
		queries: []chartQuery{{query: `100 * sum by (mode) (rate(node_cpu_seconds_total{{sel},mode!="idle"}[{w}])) / scalar(count(node_cpu_seconds_total{{sel},mode="idle"}))`, by: "mode"}},
		fold:    foldCPU},
	{key: "memory", title: "Memory and swap used", unit: UnitPercent, max: 100,
		queries: []chartQuery{
			{query: `100 * (1 - node_memory_MemAvailable_bytes{{sel}} / node_memory_MemTotal_bytes{{sel}})`, name: "Memory"},
			{query: `100 * (1 - node_memory_SwapFree_bytes{{sel}} / (node_memory_SwapTotal_bytes{{sel}} > 0))`, name: "Swap"},
		}},
	{key: "load", title: "Load average", unit: UnitLoad,
		queries: []chartQuery{
			{query: `node_load1{{sel}}`, name: "1 min"},
			{query: `node_load5{{sel}}`, name: "5 min"},
			{query: `node_load15{{sel}}`, name: "15 min"},
		}},
	{key: "disk_used", title: "Disk used", unit: UnitPercent, max: 100,
		queries: []chartQuery{{query: `100 * (1 - node_filesystem_avail_bytes{{sel},` + fsFilter + `} / node_filesystem_size_bytes{{sel},` + fsFilter + `})`, by: "mountpoint"}},
		fold:    pickFilesystems},
	{key: "disk_io", title: "Disk throughput", unit: UnitBytesPerSec,
		queries: []chartQuery{
			{query: `sum(rate(node_disk_read_bytes_total{{sel},` + diskFilter + `}[{w}]))`, name: "Read"},
			{query: `sum(rate(node_disk_written_bytes_total{{sel},` + diskFilter + `}[{w}]))`, name: "Write"},
		}},
	{key: "network", title: "Network", unit: UnitBytesPerSec,
		queries: []chartQuery{
			{query: `sum by (device) (rate(node_network_receive_bytes_total{{sel},` + netFilter + `}[{w}]))`, by: "device", name: "in"},
			{query: `sum by (device) (rate(node_network_transmit_bytes_total{{sel},` + netFilter + `}[{w}]))`, by: "device", name: "out"},
		},
		fold: pickInterfaces},
}

func (s *Service) host(ctx context.Context, w Window, instance string, end time.Time) *Host {
	sel := fmt.Sprintf(`job=%q,instance=%q`, NodeJob, instance)
	h := &Host{Filesystems: []Filesystem{}, Charts: make([]Chart, len(chartDefs))}
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, d := range instantDefs {
		wg.Go(func() {
			v := s.scalar(ctx, q(d.query, sel, time.Minute))
			mu.Lock()
			d.set(&h.Stats, v)
			mu.Unlock()
		})
	}
	wg.Go(func() {
		smp, err := s.o.Prom.Query(ctx, fmt.Sprintf(`node_uname_info{%s}`, sel))
		if err == nil && len(smp) > 0 {
			mu.Lock()
			h.Hostname, h.Kernel = smp[0].Labels["nodename"], smp[0].Labels["release"]
			mu.Unlock()
		}
	})
	wg.Go(func() {
		fs := s.filesystems(ctx, sel)
		mu.Lock()
		h.Filesystems = fs
		mu.Unlock()
	})

	start := end.Add(-w.Span)
	raw := make([][]Series, len(chartDefs))
	for i, d := range chartDefs {
		h.Charts[i] = Chart{Key: d.key, Title: d.title, Unit: d.unit, Stacked: d.stacked, Series: []Series{}}
		if d.max > 0 {
			h.Charts[i].Max = &hundred
		}
		wg.Go(func() {
			var all []Series
			for _, cq := range d.queries {
				res, err := s.o.Prom.QueryRangeSeries(ctx, q(cq.query, sel, w.RateWindow()), start, end, w.Step)
				if err != nil {
					h.Charts[i].Error = err.Error()
					return
				}
				for _, r := range res {
					name := cq.name
					if cq.by != "" {
						name = strings.TrimSpace(cmp.Or(r.Labels[cq.by], "unknown") + " " + cq.name)
					}
					all = append(all, Series{Name: name, Points: points(r.Points)})
				}
			}
			raw[i] = all
		})
	}
	wg.Wait()

	for i, d := range chartDefs {
		ser := raw[i]
		if d.fold != nil {
			ser = d.fold(ser, h)
		}
		ser = slices.DeleteFunc(ser, func(x Series) bool { return len(x.Points) == 0 })
		if ser == nil {
			ser = []Series{}
		}
		h.Charts[i].Series = ser
	}
	if c := h.Stats.Cores; c != nil {
		for i := range h.Charts {
			if h.Charts[i].Key == "load" {
				h.Charts[i].Reference = &Reference{Label: "cores", Value: *c}
			}
		}
	}
	return h
}

// scalar runs an instant query that should give one number (nil = no data
// or failed: the stat tile shows "—").
func (s *Service) scalar(ctx context.Context, query string) *float64 {
	smp, err := s.o.Prom.Query(ctx, query)
	if err != nil || len(smp) == 0 {
		return nil
	}
	v := smp[0].Value
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	v = round(v)
	return &v
}

func (s *Service) filesystems(ctx context.Context, sel string) []Filesystem {
	byKey := map[string]*Filesystem{}
	var order []string
	for _, metric := range []string{"size", "avail"} {
		smp, err := s.o.Prom.Query(ctx, fmt.Sprintf(`node_filesystem_%s_bytes{%s,%s}`, metric, sel, fsFilter))
		if err != nil {
			continue
		}
		for _, x := range smp {
			mp := x.Labels["mountpoint"]
			if mp == "" {
				continue
			}
			f, ok := byKey[mp]
			if !ok {
				f = &Filesystem{Mountpoint: mp, Device: x.Labels["device"], FSType: x.Labels["fstype"]}
				byKey[mp] = f
				order = append(order, mp)
			}
			if metric == "size" {
				f.SizeBytes = finite(x.Value)
			} else {
				f.AvailBytes = finite(x.Value)
			}
		}
	}
	out := make([]Filesystem, 0, len(order))
	for _, mp := range order {
		if f := byKey[mp]; f.SizeBytes > 0 {
			out = append(out, *f)
		}
	}
	// "/" first, then the largest.
	slices.SortStableFunc(out, func(a, b Filesystem) int {
		if (a.Mountpoint == "/") != (b.Mountpoint == "/") {
			if a.Mountpoint == "/" {
				return -1
			}
			return 1
		}
		return cmp.Compare(b.SizeBytes, a.SizeBytes)
	})
	return out
}

func foldCPU(in []Series, _ *Host) []Series {
	out := make([]Series, len(cpuModes))
	for i, m := range cpuModes {
		out[i].Name = m.name
		var parts []Series
		for _, s := range in {
			if slices.Contains(strings.Fields(m.modes), s.Name) {
				parts = append(parts, s)
			}
		}
		out[i].Points = sumSeries(parts)
	}
	// Steal is only worth a line when the hypervisor takes time.
	if peak(out[3].Points) < 0.5 {
		out = out[:3]
	}
	return out
}

// pickFilesystems keeps the mounts Filesystems lists, in its order.
func pickFilesystems(in []Series, h *Host) []Series {
	var out []Series
	for _, f := range h.Filesystems {
		for _, s := range in {
			if s.Name == f.Mountpoint {
				out = append(out, s)
				break
			}
		}
		if len(out) == MaxFilesystems {
			break
		}
	}
	return out
}

// pickInterfaces keeps the busiest MaxInterfaces devices, "in" then "out" each.
func pickInterfaces(in []Series, _ *Host) []Series {
	total := map[string]float64{}
	for _, s := range in {
		dev := strings.TrimSuffix(strings.TrimSuffix(s.Name, " in"), " out")
		for _, p := range s.Points {
			total[dev] += p[1]
		}
	}
	devs := make([]string, 0, len(total))
	for d := range total {
		devs = append(devs, d)
	}
	slices.SortFunc(devs, func(a, b string) int {
		if c := cmp.Compare(total[b], total[a]); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	if len(devs) > MaxInterfaces {
		devs = devs[:MaxInterfaces]
	}
	slices.Sort(devs) // stable colours: an interface keeps its slot as traffic shifts
	var out []Series
	for _, d := range devs {
		for _, dir := range []string{" in", " out"} {
			for _, s := range in {
				if s.Name == d+dir {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

func sumSeries(parts []Series) [][2]float64 {
	if len(parts) == 0 {
		return nil
	}
	acc := map[float64]float64{}
	for _, s := range parts {
		for _, p := range s.Points {
			acc[p[0]] += p[1]
		}
	}
	out := make([][2]float64, 0, len(acc))
	for t, v := range acc {
		out = append(out, [2]float64{t, round(v)})
	}
	slices.SortFunc(out, func(a, b [2]float64) int { return cmp.Compare(a[0], b[0]) })
	return out
}

func peak(pts [][2]float64) float64 {
	m := 0.0
	for _, p := range pts {
		m = max(m, p[1])
	}
	return m
}

// points drops NaN/Inf samples (a gap in the line).
func points(in []promql.Point) [][2]float64 {
	out := make([][2]float64, 0, len(in))
	for _, p := range in {
		if math.IsNaN(p.V) || math.IsInf(p.V, 0) {
			continue
		}
		out = append(out, [2]float64{float64(p.T.Unix()), round(p.V)})
	}
	return out
}

func (s *Service) targets(ctx context.Context, w Window, now time.Time) ([]Target, string) {
	type key struct{ job, instance string }
	byKey := map[key]*Target{}
	var order []key
	up, err := s.o.Prom.Query(ctx, `up`)
	if err != nil {
		return []Target{}, err.Error()
	}
	for _, x := range up {
		k := key{x.Labels["job"], x.Labels["instance"]}
		if _, ok := byKey[k]; ok {
			continue
		}
		st := StatusDown
		if x.Value == 1 {
			st = StatusUp
		}
		byKey[k] = &Target{Job: k.job, Instance: k.instance, Status: st}
		order = append(order, k)
	}
	span := fmt.Sprintf("%ds", int(w.Span.Seconds()))
	extra := []struct {
		query string
		set   func(*Target, float64)
	}{
		{`timestamp(up)`, func(t *Target, v float64) {
			ts := time.Unix(int64(v), 0).UTC()
			t.LastScrape = &ts
			if t.Status == StatusUp && now.Sub(ts) > StaleAfter {
				t.Status = StatusStale
			}
		}},
		{`100 * avg_over_time(up[` + span + `])`, func(t *Target, v float64) { a := round(v); t.Availability = &a }},
		{`max_over_time(timestamp(up == 1)[` + span + `:` + fmt.Sprintf("%ds", int(w.Step.Seconds())) + `])`, func(t *Target, v float64) {
			ts := time.Unix(int64(v), 0).UTC()
			t.LastUp = &ts
		}},
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, e := range extra {
		wg.Go(func() {
			smp, err := s.o.Prom.Query(ctx, e.query)
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, x := range smp {
				if t := byKey[key{x.Labels["job"], x.Labels["instance"]}]; t != nil && !math.IsNaN(x.Value) && !math.IsInf(x.Value, 0) {
					e.set(t, x.Value)
				}
			}
		})
	}
	wg.Wait()
	out := make([]Target, 0, len(order))
	for _, k := range order {
		t := byKey[k]
		if t.Status == StatusUp {
			t.LastUp = nil // only interesting while it's down
		}
		out = append(out, *t)
	}
	// Problems first, then by job.
	rank := map[string]int{StatusDown: 0, StatusStale: 1, StatusUp: 2}
	slices.SortStableFunc(out, func(a, b Target) int {
		return cmp.Or(cmp.Compare(rank[a.Status], rank[b.Status]), cmp.Compare(a.Job, b.Job), cmp.Compare(a.Instance, b.Instance))
	})
	return out, ""
}

func finite(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

func round(v float64) float64 { return math.Round(v*1000) / 1000 }
