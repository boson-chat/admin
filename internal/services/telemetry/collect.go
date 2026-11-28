package telemetry

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/clients/bosonapi"
	"github.com/boson-chat/admin/internal/clients/dockerapi"
	"github.com/boson-chat/admin/internal/clients/ircprobe"
	"github.com/boson-chat/admin/internal/clients/ircstats"
	"github.com/boson-chat/admin/internal/clients/promql"
	"github.com/boson-chat/admin/internal/services/updates"
)

// Docker is what the report asks the socket proxy: the compose project's
// containers and each one's restart count and state.
type Docker interface {
	List(ctx context.Context, project string) ([]dockerapi.Container, error)
	Inspect(ctx context.Context, id string) (*dockerapi.Inspect, error)
}

// Prom runs instant queries against the stack's VictoriaMetrics.
type Prom interface {
	Query(ctx context.Context, q string) ([]promql.Sample, error)
}

// Files is the files service's internal API (uploads in the last 24 h).
type Files interface {
	Status(ctx context.Context) (*bosonapi.FilesStatus, error)
	List(ctx context.Context, q bosonapi.FilesQuery) (*bosonapi.FilePage, error)
}

// IRCCounter is an IRC-side API that says how big the network is now
// (Ergo's HTTP API, the bridge's managed API).
type IRCCounter interface {
	Counts(ctx context.Context) (*ircstats.Counts, error)
}

// Sources are where a report's values come from. Each is optional: nil
// means unknown (null in the report, or the schema's default).
type Sources struct {
	Docker  Docker
	Project string
	// SelfVersion is the panel's own version (VERSION).
	SelfVersion string
	// StackVersion is the installed stack release ("" = unknown).
	StackVersion func(ctx context.Context) string
	// Prom is the stack's VictoriaMetrics (METRICS_URL); nil = metrics off.
	Prom Prom
	// IRC probes IRC_CHECK and reads LUSERS (nil = no IRC check). It
	// can't register without guest access: Ergo and Bridge come first for
	// the user count.
	IRC func(ctx context.Context) *ircprobe.Result
	// AuthStats is auth's GET /internal/v1/stats (account counts with the
	// internal token). nil, or an error (an auth without it), falls back to
	// what an admin's visit read (Input.Known).
	AuthStats func(ctx context.Context) (*authapi.Stats, error)
	// Ergo is Ergo's HTTP API (irc profile): users now and registered
	// channels. Bridge is evrd's managed API (bridge profile): the
	// network's users and channels from LUSERS. nil = not configured. The
	// one for the install's mode is used.
	Ergo   IRCCounter
	Bridge IRCCounter
	// PublicConfig is auth's GET /v1/config (registration, sign-in
	// providers, email).
	PublicConfig func(ctx context.Context) (*authapi.PublicConfig, error)
	// Config reads a key of the stack config file (ok=false: unset).
	Config func(key string) (any, bool)
	// Runs is the update history, newest first.
	Runs  func(ctx context.Context) []updates.RunSummary
	Files Files
	// DirectoryListed: the network is listed in a directory.
	DirectoryListed func(ctx context.Context) bool
	// GOARCH of the panel (the host's architecture).
	GOARCH string
	// DockerInfo is Docker's GET /info through the socket proxy (host_size's
	// CPUs, memory and architecture, and where Docker keeps its data). nil,
	// or an error (a proxy that doesn't allow it), leaves those unknown.
	DockerInfo func(ctx context.Context) (*dockerapi.Info, error)
}

// Known is what the panel learned from an admin's own requests (auth's
// admin API needs a signed-in admin's token): account counts and guest
// access. Old values are dropped (Collector.MaxAge).
type Known struct {
	At          time.Time `json:"at"`
	Accounts    *int64    `json:"accounts,omitempty"`
	Active7d    *int64    `json:"active_7d,omitempty"`
	Active30d   *int64    `json:"active_30d,omitempty"`
	GuestAccess *bool     `json:"guest_access,omitempty"`
	// GuestFirst is nil on an auth without the setting.
	GuestFirst *bool `json:"guest_first,omitempty"`
}

// Input is what a report needs besides the sources.
type Input struct {
	InstallID string
	Reason    string
	Now       time.Time
	Known     *Known
	// IRCPeak is the most IRC users seen at once in the last 24 h (nil = unknown).
	IRCPeak *int
}

// Collector builds reports from Sources.
type Collector struct {
	S Sources
	// MaxAge: Known values older than this are left out (default 7 days).
	MaxAge time.Duration
}

type svcInfo struct {
	container dockerapi.Container
	inspect   *dockerapi.Inspect
}

// Build assembles a report. It never fails: what can't be read is null or
// the schema's "unknown". It also returns how many IRC users are connected
// now (nil = unknown), a sample for the 24 h peak.
func (c *Collector) Build(ctx context.Context, in Input) (*Report, *int) {
	s := c.S
	r := &Report{
		Schema: 1, InstallID: in.InstallID, Reason: in.Reason,
		Stack:    Stack{Version: Unknown},
		Services: map[string]string{}, Profiles: []string{},
		Install:  Install{Mode: Unknown, IRCd: Unknown, Arch: archOf(s.GOARCH)},
		Settings: Settings{Registration: "open", GuestAccess: true, SSO: []string{}, FederationPeers: "0"},
		Health:   Health{Updates: Updates{Errors: []string{}}, Services: map[string]ServiceHealth{}},
	}
	var (
		wg      sync.WaitGroup
		svcs    map[string]svcInfo
		irc     *ircprobe.Result
		pub     *authapi.PublicConfig
		runs    []updates.RunSummary
		listed  bool
		stackV  string
		authSt  *authapi.Stats
		ergoC   *ircstats.Counts
		bridgeC *ircstats.Counts
		hostH   *Host
		uptime  *int
		info    *dockerapi.Info
		feature Features
	)
	if s.Docker != nil {
		safeGo(&wg, func() { svcs = c.services(ctx) })
	}
	if s.IRC != nil {
		safeGo(&wg, func() { irc = s.IRC(ctx) })
	}
	if s.PublicConfig != nil {
		safeGo(&wg, func() { pub, _ = s.PublicConfig(ctx) })
	}
	if s.AuthStats != nil {
		safeGo(&wg, func() { authSt, _ = s.AuthStats(ctx) })
	}
	if s.Ergo != nil {
		safeGo(&wg, func() { ergoC, _ = s.Ergo.Counts(ctx) })
	}
	if s.Bridge != nil {
		safeGo(&wg, func() { bridgeC, _ = s.Bridge.Counts(ctx) })
	}
	if s.Runs != nil {
		safeGo(&wg, func() { runs = s.Runs(ctx) })
	}
	if s.DirectoryListed != nil {
		safeGo(&wg, func() { listed = s.DirectoryListed(ctx) })
	}
	if s.StackVersion != nil {
		safeGo(&wg, func() { stackV = s.StackVersion(ctx) })
	}
	if s.Prom != nil {
		safeGo(&wg, func() { hostH, uptime = c.host(ctx) })
	}
	if s.DockerInfo != nil {
		safeGo(&wg, func() { info, _ = s.DockerInfo(ctx) })
	}
	wg.Wait()
	r.HostSize = c.hostSize(ctx, info)

	r.Stack.Version = NormaliseVersion(stackV)

	// Services, profiles and their health.
	profiles := map[string]bool{"admin": true} // the report comes from the panel
	for name, si := range svcs {
		v := NormaliseVersion(updates.ImageTag(si.container.Image))
		if v == Unknown {
			if lv := si.container.Labels[dockerapi.LabelVersion]; lv != "" {
				v = NormaliseVersion(lv)
			}
		}
		if name == "admin" && v == Unknown {
			v = NormaliseVersion(s.SelfVersion)
		}
		r.Services[name] = v
		if p := serviceProfile[name]; p != "" {
			profiles[p] = true
		}
		if si.inspect != nil {
			r.Health.Services[name] = ServiceHealth{Restarts: min(max(si.inspect.RestartCount, 0), 1000), State: stateOf(si.inspect)}
		}
	}
	if _, ok := r.Services["admin"]; !ok && s.SelfVersion != "" {
		r.Services["admin"] = NormaliseVersion(s.SelfVersion)
	}
	for p := range profiles {
		r.Profiles = append(r.Profiles, p)
	}
	slices.Sort(r.Profiles)

	// Install mode: the bridge (evrd) puts Boson in front of another IRCd.
	var ircVersion string
	if irc != nil {
		ircVersion = irc.Version
	}
	switch {
	case svcs["evrd"].container.ID != "":
		r.Install.Mode = "addon"
		r.Install.IRCd = ircdFrom(ircVersion)
		if r.Install.IRCd == "ergo" { // IRC_CHECK at an Ergo of the stack's? can't tell
			r.Install.IRCd = Unknown
		}
	case svcs["ergo"].container.ID != "":
		r.Install.Mode, r.Install.IRCd = "fresh", "ergo"
	case ircVersion != "":
		r.Install.IRCd = ircdFrom(ircVersion)
	}

	// Counts: auth's internal stats, else what an admin's visit read.
	if authSt != nil {
		r.Counts.Accounts = bucketPtr(authSt.Accounts)
		r.Counts.Active7d = bucketPtr(authSt.Active7d)
		r.Counts.Active30d = bucketPtr(authSt.Active30d)
	} else if k := in.Known; k != nil && in.Now.Sub(k.At) <= c.maxAge() {
		r.Counts.Accounts = bucketOf(k.Accounts)
		r.Counts.Active7d = bucketOf(k.Active7d)
		r.Counts.Active30d = bucketOf(k.Active30d)
	}
	// IRC users now and channels: from the API of the install's mode
	// (Ergo's registered channels; the bridge's channels formed), the IRC
	// probe's LUSERS for users when that has none.
	var ircC *ircstats.Counts
	switch r.Install.Mode {
	case "addon":
		ircC = bridgeC
	case "fresh":
		ircC = ergoC
	default:
		ircC = cmp.Or(ergoC, bridgeC)
	}
	var usersNow *int
	if ircC != nil {
		usersNow = ircC.Users
		if ircC.Channels != nil {
			r.Counts.Channels = bucketPtr(int64(*ircC.Channels))
		}
	}
	if usersNow == nil && irc != nil {
		usersNow = irc.Users
	}
	peak := in.IRCPeak
	if usersNow != nil && (peak == nil || *usersNow > *peak) {
		peak = usersNow
	}
	if peak != nil {
		r.Counts.IRCPeak24h = bucketPtr(int64(*peak))
	}

	// Features (24 h), from VictoriaMetrics and the files service.
	if s.Prom != nil {
		var fw sync.WaitGroup
		has := func(svc string) bool { _, ok := svcs[svc]; return s.Docker == nil || ok }
		if has("quicvoiced") {
			safeGo(&fw, func() {
				feature.VoiceMinutes24h = c.promBucket(ctx, `sum(sum_over_time(quicvoice_participants[24h:1m]))`)
			})
		}
		if has("quicvideod") {
			safeGo(&fw, func() { feature.VideoMinutes24h = c.promBucket(ctx, `sum(sum_over_time(quicvideo_sessions[24h:1m]))`) })
		}
		if has("pushrelay") {
			safeGo(&fw, func() {
				feature.PushSent24h = c.promBucket(ctx, `sum(increase(pushrelay_upstream_requests_total{result="ok"}[24h]))`)
			})
		}
		fw.Wait()
	}
	if s.Files != nil {
		if _, ok := svcs["files"]; ok || s.Docker == nil {
			feature.FilesUploaded = c.uploads(ctx, in.Now)
		}
	}
	r.Features = feature

	// Settings.
	cfg := func(key string) (any, bool) {
		if s.Config == nil {
			return nil, false
		}
		return s.Config(key)
	}
	if pub != nil && validRegistration(pub.Registration) {
		r.Settings.Registration = pub.Registration
	} else if v, ok := cfg("auth.registration"); ok {
		if str, _ := v.(string); validRegistration(str) {
			r.Settings.Registration = str
		}
	}
	if v, ok := cfg("auth.guest_access"); ok {
		if b, ok := v.(bool); ok {
			r.Settings.GuestAccess = b
		}
	} else if k := in.Known; k != nil && k.GuestAccess != nil && in.Now.Sub(k.At) <= c.maxAge() {
		r.Settings.GuestAccess = *k.GuestAccess
	}
	// guest_first: the file's auth.guest_first, else what auth said (nil
	// on an auth without the setting: null). It needs guest access.
	fresh := in.Known != nil && in.Now.Sub(in.Known.At) <= c.maxAge()
	if v, ok := cfg("auth.guest_first"); ok {
		if b, ok := v.(bool); ok {
			r.Settings.GuestFirst = ptrBool(b && r.Settings.GuestAccess)
		}
	} else if fresh && in.Known.GuestFirst != nil {
		r.Settings.GuestFirst = ptrBool(*in.Known.GuestFirst && r.Settings.GuestAccess)
	}
	switch {
	case pub != nil && pub.Email != nil:
		r.Settings.SMTP = pub.Email.Configured
	default:
		if v, ok := cfg("auth.smtp.host"); ok {
			str, _ := v.(string)
			r.Settings.SMTP = strings.TrimSpace(str) != ""
		}
	}
	if pub != nil {
		var kinds []string
		for _, p := range pub.SSOProviders {
			kinds = append(kinds, ssoKind(p.Kind))
		}
		r.Settings.SSO = sortedUnique(kinds)
		if r.Settings.SSO == nil {
			r.Settings.SSO = []string{}
		}
	}
	r.Settings.FederationPeers = Bucket(int64(federationPeers(cfg)))
	r.Settings.DirectoryListed = listed

	// Health.
	r.Health.Updates, _ = updateHealth(runs, in.Now)
	unresolved := len(runs) > 0 && (runs[0].State == updates.StateFailed || runs[0].State == updates.StateRollbackFailed)
	r.Health.Host = hostH
	r.UptimeDays = uptime
	healthy := !unresolved
	for _, h := range r.Health.Services {
		if h.State == "restarting" || h.State == "unhealthy" {
			healthy = false
		}
	}
	r.Health.Healthy = healthy
	return r, usersNow
}

// UsersNow is how many IRC users are connected now: Ergo's API, else the
// bridge's, else the IRC probe's LUSERS (which needs guest access). nil =
// unknown.
func (c *Collector) UsersNow(ctx context.Context) *int {
	for _, api := range []IRCCounter{c.S.Ergo, c.S.Bridge} {
		if api == nil {
			continue
		}
		if n, err := api.Counts(ctx); err == nil && n.Users != nil {
			return n.Users
		}
	}
	if c.S.IRC != nil {
		if r := c.S.IRC(ctx); r != nil {
			return r.Users
		}
	}
	return nil
}

func (c *Collector) maxAge() time.Duration {
	if c.MaxAge > 0 {
		return c.MaxAge
	}
	return 7 * 24 * time.Hour
}

func bucketOf(n *int64) *string {
	if n == nil {
		return nil
	}
	return bucketPtr(*n)
}

func ptrBool(b bool) *bool { return &b }

func validRegistration(s string) bool { return s == "open" || s == "invite" || s == "closed" }

// federationPeers is the allowlist of home servers trusted for
// federation (auth.federation.allow), 0 with federation off.
func federationPeers(cfg func(string) (any, bool)) int {
	if v, ok := cfg("auth.federation.mode"); ok {
		if m, _ := v.(string); m == "off" {
			return 0
		}
	}
	v, ok := cfg("auth.federation.allow")
	if !ok {
		return 0
	}
	switch l := v.(type) {
	case []string:
		return len(l)
	case []any:
		return len(l)
	}
	return 0
}

// services lists the compose project's containers that are in the
// service enum (one per service, the running one first) and inspects each.
func (c *Collector) services(ctx context.Context) map[string]svcInfo {
	out := map[string]svcInfo{}
	cs, err := c.S.Docker.List(ctx, c.S.Project)
	if err != nil {
		return out
	}
	for _, ct := range cs {
		name := ct.Labels[dockerapi.LabelService]
		if !slices.Contains(ServiceNames, name) {
			continue // one-shot init containers, services the install added itself
		}
		if cur, dup := out[name]; dup && cur.container.State == "running" {
			continue
		}
		out[name] = svcInfo{container: ct}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	inspected := map[string]*dockerapi.Inspect{}
	for name, si := range out {
		safeGo(&wg, func() {
			in, err := c.S.Docker.Inspect(ctx, si.container.ID)
			if err != nil {
				return
			}
			mu.Lock()
			inspected[name] = in
			mu.Unlock()
		})
	}
	wg.Wait()
	for name, in := range inspected {
		si := out[name]
		si.inspect = in
		out[name] = si
	}
	return out
}

// safeGo runs fn in the group; a panic in a source leaves its value unknown.
func safeGo(wg *sync.WaitGroup, fn func()) {
	wg.Go(func() {
		defer func() { _ = recover() }()
		fn()
	})
}

func stateOf(in *dockerapi.Inspect) string {
	st := in.State
	switch {
	case st.Restarting || st.Status == "restarting":
		return "restarting"
	case st.Running || st.Status == "running":
		if st.Health != nil && st.Health.Status == "unhealthy" {
			return "unhealthy"
		}
		return "running"
	case st.Status == "created":
		return "created"
	}
	return "exited" // exited, dead, paused, removing
}

// updateHealth counts the updates and rollbacks started in the last 24 h.
func updateHealth(runs []updates.RunSummary, now time.Time) (Updates, bool) {
	u := Updates{Errors: []string{}}
	for _, run := range runs {
		if now.Sub(run.StartedAt) > 24*time.Hour || run.StartedAt.After(now.Add(time.Minute)) {
			continue
		}
		switch run.Kind {
		case "update":
			u.Attempts++
			if run.FailedStep != "" || run.Error != "" || run.State == updates.StateFailed {
				u.Failures++
				u.Errors = append(u.Errors, errorClass(run.Kind, run.FailedStep, run.Error))
			}
		case "rollback":
			u.Rollbacks++
			if run.State == updates.StateRollbackFailed {
				u.Errors = append(u.Errors, "rollback")
			}
		}
	}
	u.Attempts, u.Failures, u.Rollbacks = min(u.Attempts, 100), min(u.Failures, 100), min(u.Rollbacks, 100)
	if len(u.Errors) > 10 {
		u.Errors = u.Errors[:10]
	}
	return u, u.Failures > 0
}

// promValue is a query's single value (nil: no data, or an error).
func (c *Collector) promValue(ctx context.Context, q string) *float64 {
	smp, err := c.S.Prom.Query(ctx, q)
	if err != nil || len(smp) == 0 || math.IsNaN(smp[0].Value) || math.IsInf(smp[0].Value, 0) {
		return nil
	}
	v := smp[0].Value
	return &v
}

func (c *Collector) promBucket(ctx context.Context, q string) *string {
	v := c.promValue(ctx, q)
	if v == nil {
		return nil
	}
	return bucketPtr(int64(math.Round(*v)))
}

// Host queries: node-exporter (scrape job "node"), 24 h at a 5 min step.
const nodeSel = `job="node"`

var (
	cpuExpr  = `(100 * (1 - avg(rate(node_cpu_seconds_total{` + nodeSel + `,mode="idle"}[5m]))))`
	memExpr  = `(100 * max(1 - node_memory_MemAvailable_bytes{` + nodeSel + `} / node_memory_MemTotal_bytes{` + nodeSel + `}))`
	loadExpr = `(100 * max(node_load1{` + nodeSel + `}) / count(node_cpu_seconds_total{` + nodeSel + `,mode="idle"}))`
	fsFilter = `fstype!~"tmpfs|overlay|squashfs|ramfs|devtmpfs|nsfs|autofs|proc|sysfs|fuse.*",mountpoint!~"/(run|var/lib/docker|snap)(/.*)?|/boot/efi"`
	diskExpr = `100 * max(1 - node_filesystem_avail_bytes{` + nodeSel + `,` + fsFilter + `} / node_filesystem_size_bytes{` + nodeSel + `,` + fsFilter + `})`
)

func (c *Collector) host(ctx context.Context) (*Host, *int) {
	up, err := c.S.Prom.Query(ctx, `up{`+nodeSel+`}`)
	if err != nil || len(up) == 0 {
		return nil, nil // no node-exporter: no host numbers
	}
	h := &Host{}
	var uptime *int
	var wg sync.WaitGroup
	pct := func(dst **float64, q string) {
		safeGo(&wg, func() {
			if v := c.promValue(ctx, q); v != nil {
				x := math.Round(min(max(*v, 0), 100)*10) / 10
				*dst = &x
			}
		})
	}
	pct(&h.CPUP95, fmt.Sprintf(`quantile_over_time(0.95, %s[24h:5m])`, cpuExpr))
	pct(&h.CPUMax, fmt.Sprintf(`max_over_time(%s[24h:5m])`, cpuExpr))
	pct(&h.MemP95, fmt.Sprintf(`quantile_over_time(0.95, %s[24h:5m])`, memExpr))
	pct(&h.DiskUsed, diskExpr)
	pct(&h.LoadPerCPU, fmt.Sprintf(`quantile_over_time(0.95, %s[24h:5m])`, loadExpr))
	safeGo(&wg, func() {
		if v := c.promValue(ctx, `max(node_time_seconds{`+nodeSel+`} - node_boot_time_seconds{`+nodeSel+`})`); v != nil && *v >= 0 {
			d := min(int(*v/86400), 3650)
			uptime = &d
		}
	})
	wg.Wait()
	return h, uptime
}

// diskFS: real filesystems, Docker's own mount included (its data root may
// be on one).
const diskFS = `fstype!~"tmpfs|overlay|squashfs|ramfs|devtmpfs|nsfs|autofs|proc|sysfs|fuse.*"`

// hostSize is host_size: CPUs, memory and architecture from Docker's
// /info, the disk from node-exporter (the filesystem holding Docker's data
// root, else /). Only buckets; null when nothing is known.
func (c *Collector) hostSize(ctx context.Context, info *dockerapi.Info) *HostSize {
	hs := &HostSize{}
	root := "/"
	if info != nil {
		hs.CPUCores = CPUBucket(info.NCPU)
		hs.RAMGB = RAMBucket(info.MemTotal)
		hs.Arch = dockerArch(info.Architecture)
		if strings.HasPrefix(info.DockerRootDir, "/") {
			root = info.DockerRootDir
		}
	}
	if c.S.Prom != nil {
		if smp, err := c.S.Prom.Query(ctx, `node_filesystem_size_bytes{`+nodeSel+`,`+diskFS+`}`); err == nil {
			if size, ok := mountFor(smp, root); ok {
				hs.DiskGB = DiskBucket(size)
			}
		}
	}
	if hs.CPUCores == nil && hs.RAMGB == nil && hs.DiskGB == nil && hs.Arch == nil {
		return nil
	}
	return hs
}

// mountFor is the size of the filesystem holding path: the longest mount
// point that contains it.
func mountFor(smp []promql.Sample, path string) (float64, bool) {
	best, size := "", 0.0
	for _, s := range smp {
		m := s.Labels["mountpoint"]
		if m == "" || math.IsNaN(s.Value) || s.Value <= 0 {
			continue
		}
		in := m == "/" || path == m || strings.HasPrefix(path, strings.TrimSuffix(m, "/")+"/")
		if !in || len(m) < len(best) {
			continue
		}
		if len(m) > len(best) || s.Value > size {
			best, size = m, s.Value
		}
	}
	return size, best != ""
}

// uploads counts the files uploaded in the last 24 h: the files service's
// window when it is 24 h, else its newest-first list.
func (c *Collector) uploads(ctx context.Context, now time.Time) *string {
	st, err := c.S.Files.Status(ctx)
	if err != nil {
		return nil
	}
	if st.Config != nil && st.Config.WindowSeconds == 86400 {
		return bucketPtr(st.Totals.WindowUploads)
	}
	since := now.Add(-24 * time.Hour)
	var n int64
	cursor := ""
	for page := 0; page < 60; page++ {
		p, err := c.S.Files.List(ctx, bosonapi.FilesQuery{Status: "all", Limit: 100, Cursor: cursor})
		if err != nil {
			return nil
		}
		for _, f := range p.Files {
			if f.CreatedAt.Before(since) {
				return bucketPtr(n)
			}
			n++
			if n >= bucketCap {
				return bucketPtr(n)
			}
		}
		if p.NextCursor == "" || len(p.Files) == 0 {
			return bucketPtr(n)
		}
		cursor = p.NextCursor
	}
	return bucketPtr(n)
}
