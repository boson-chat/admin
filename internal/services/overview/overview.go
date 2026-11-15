// Package overview gathers the stack's health: service checks, versions,
// TLS certificate expiry and a few live numbers from the metrics backend.
package overview

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/boson-chat/admin/config"
	"github.com/boson-chat/admin/internal/clients/health"
	"github.com/boson-chat/admin/internal/clients/ircprobe"
	"github.com/boson-chat/admin/internal/clients/promql"
	"github.com/boson-chat/admin/internal/clients/tlsprobe"
)

const (
	StatusUp       = "up"
	StatusDegraded = "degraded"
	StatusDown     = "down"

	CertOK       = "ok"
	CertWarning  = "warning"  // expires within 21 days
	CertCritical = "critical" // expires within 7 days
	CertExpired  = "expired"
	CertError    = "error" // unreachable, untrusted or wrong name
)

// Service is one named service and its checks.
type Service struct {
	Name         string          `json:"name"`
	Status       string          `json:"status"`
	Version      string          `json:"version,omitempty"`
	VersionError string          `json:"version_error,omitempty"`
	Checks       []health.Result `json:"checks"`
}

// Certificate is one TLS_CHECK_HOSTS entry.
type Certificate struct {
	Target     string         `json:"target"`
	ServerName string         `json:"server_name"`
	Address    string         `json:"address"`
	Status     string         `json:"status"`
	DaysLeft   *float64       `json:"days_left,omitempty"`
	Error      string         `json:"error,omitempty"`
	Cert       *tlsprobe.Cert `json:"cert,omitempty"`
	CheckedAt  time.Time      `json:"checked_at"`
}

// Overview is GET /api/overview.
type Overview struct {
	CheckedAt    time.Time     `json:"checked_at"`
	Panel        PanelInfo     `json:"panel"`
	Services     []Service     `json:"services"`
	Certificates []Certificate `json:"certificates"`
	Metrics      MetricsView   `json:"metrics"`
	GrafanaURL   string        `json:"grafana_url,omitempty"`
	// Edge names the reverse proxy that terminates TLS (EDGE_NAME).
	Edge EdgeInfo `json:"edge"`
	// IRC is the IRC_CHECK probe; it is also listed in Services as "irc".
	IRC IRCInfo `json:"irc"`
	// PublicProbes are endpoints the browser times itself
	// (PUBLIC_PROBE_TARGETS), plus the panel's own /healthz as "admin".
	PublicProbes []config.PublicProbe `json:"public_probes"`
	// Discovery says whether the services come from the compose project's
	// containers (the Docker socket proxy) or only the env lists.
	Discovery DiscoveryInfo `json:"discovery"`
}

// DiscoveryInfo: Enabled = the services shown are the ones that have a
// container; Error = the last listing failed (an older one, or the env
// lists, stand in).
type DiscoveryInfo struct {
	Enabled bool   `json:"enabled"`
	Error   string `json:"error,omitempty"`
}

type EdgeInfo struct {
	Name string `json:"name"`
}

// IRCInfo says whether IRC is probed and where.
type IRCInfo struct {
	Configured bool   `json:"configured"`
	Target     string `json:"target,omitempty"`
}

// IRCServiceName is the Services entry the IRC probe reports under.
const IRCServiceName = "irc"

type PanelInfo struct {
	Version string `json:"version"`
}

type OverviewServiceImpl interface {
	Overview(ctx context.Context) *Overview
}

type OverviewService struct {
	Health   health.HealthCheckerImpl
	TLS      tlsprobe.TLSProberImpl
	Prom     promql.PromClientImpl // nil when METRICS_URL is empty
	Targets  []config.Target
	Versions []config.Target
	Certs    []config.TLSTarget
	// CertsFn, when set, gives the current list instead of Certs
	// (admin.tls_check_hosts in the stack config file can change it).
	CertsFn  func() []config.TLSTarget
	Grafana  string
	Version  string
	IRCProbe ircprobe.IRCProberImpl
	IRC      *config.IRCTarget // nil = IRC not checked
	Edge     string
	Public   []config.PublicProbe
	// Docker lists the compose project's containers; nil = the env lists
	// alone say what to probe.
	Docker  Containers
	Project string
	// Known is the probe map for compose services (Builtins).
	Known []Known
	Now   func() time.Time
	// IRCTTL caches the IRC probe: each probe is a connection the IRC
	// server logs, and several open panels poll every 15 s.
	IRCTTL time.Duration
	// CertTTL caches certificate probes; they change rarely and dialing
	// public hostnames on every poll is wasteful.
	CertTTL time.Duration

	mu        sync.Mutex
	certCache []Certificate
	certAt    time.Time
	certKey   string
	ircCache  *Service
	ircAt     time.Time
	discCache map[string]present
	discAt    time.Time
}

type Options struct {
	Health   health.HealthCheckerImpl
	TLS      tlsprobe.TLSProberImpl
	Prom     promql.PromClientImpl
	Targets  []config.Target
	Versions []config.Target
	Certs    []config.TLSTarget
	CertsFn  func() []config.TLSTarget
	Grafana  string
	Version  string
	IRCProbe ircprobe.IRCProberImpl
	IRC      *config.IRCTarget
	Edge     string
	Public   []config.PublicProbe
	Docker   Containers
	Project  string
	Known    []Known
	Now      func() time.Time
}

func NewOverviewService(o Options) *OverviewService {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Edge == "" {
		o.Edge = "Traefik"
	}
	if o.Project == "" {
		o.Project = "boson"
	}
	return &OverviewService{Health: o.Health, TLS: o.TLS, Prom: o.Prom, Targets: o.Targets, Versions: o.Versions,
		Certs: o.Certs, CertsFn: o.CertsFn, Grafana: o.Grafana, Version: o.Version, IRCProbe: o.IRCProbe, IRC: o.IRC, Edge: o.Edge,
		Public: withSelfProbe(o.Public), Docker: o.Docker, Project: o.Project, Known: o.Known,
		Now: o.Now, CertTTL: 5 * time.Minute, IRCTTL: 30 * time.Second}
}

// withSelfProbe adds the panel's own /healthz (same origin, through the
// edge) as "admin" unless PUBLIC_PROBE_TARGETS already names one.
func withSelfProbe(in []config.PublicProbe) []config.PublicProbe {
	for _, p := range in {
		if p.Name == "admin" {
			return in
		}
	}
	return append([]config.PublicProbe{{Name: "admin", URL: "/healthz", Kind: "http"}}, in...)
}

func (s *OverviewService) Overview(ctx context.Context) *Overview {
	out := &Overview{CheckedAt: s.Now().UTC(), Panel: PanelInfo{Version: s.Version}, GrafanaURL: s.Grafana,
		Edge: EdgeInfo{Name: s.Edge}}
	var wg sync.WaitGroup
	wg.Go(func() { out.Certificates = s.certificates(ctx) })
	wg.Go(func() { out.Metrics = s.metrics(ctx) })

	d, ok := s.discover(ctx)
	out.Discovery = DiscoveryInfo{Enabled: ok, Error: d.err}
	p := s.planFor(d, ok)
	out.PublicProbes = s.publicFor(d, ok)
	var irc *Service
	wg.Go(func() { out.Services = s.services(ctx, p) })
	if p.irc {
		out.IRC = IRCInfo{Configured: true, Target: s.IRC.Label}
		wg.Go(func() { irc = s.irc(ctx) })
	}
	wg.Wait()
	if irc != nil {
		out.Services = mergeIRC(out.Services, *irc)
	}
	for i := range out.Services {
		sv := &out.Services[i]
		if v := p.images[sv.Name]; v != "" && sv.Version == "" {
			sv.Version, sv.VersionError = v, ""
		}
	}
	return out
}

// irc probes IRC_CHECK (cached for IRCTTL).
func (s *OverviewService) irc(ctx context.Context) *Service {
	s.mu.Lock()
	if s.ircCache != nil && s.Now().Sub(s.ircAt) < s.IRCTTL {
		c := *s.ircCache
		s.mu.Unlock()
		return &c
	}
	s.mu.Unlock()

	t := s.IRC
	r := s.IRCProbe.Probe(ctx, t.Address, t.ServerName, t.TLS)
	check := health.Result{URL: t.Label, OK: r.OK, Latency: r.Latency, Detail: r.Detail}
	check.LatencyMS = float64(r.Latency.Microseconds()) / 1000
	sv := &Service{Name: IRCServiceName, Version: r.Version, Checks: []health.Result{check}}
	sv.Status = rollup(sv.Checks)

	s.mu.Lock()
	s.ircCache, s.ircAt = sv, s.Now()
	s.mu.Unlock()
	c := *sv
	return &c
}

// mergeIRC adds the IRC probe to a HEALTH_TARGETS service of the same name
// (if someone configured one) or appends it.
func mergeIRC(services []Service, irc Service) []Service {
	for i := range services {
		sv := &services[i]
		if sv.Name != IRCServiceName {
			continue
		}
		sv.Checks = append(append([]health.Result{}, sv.Checks...), irc.Checks...)
		if sv.Version == "" {
			sv.Version = irc.Version
		}
		sv.Status = rollup(sv.Checks)
		return services
	}
	return append(services, irc)
}

func (s *OverviewService) services(ctx context.Context, p plan) []Service {
	byName := map[string]*Service{}
	var order []string
	get := func(name string) *Service {
		if sv, ok := byName[name]; ok {
			return sv
		}
		sv := &Service{Name: name, Checks: []health.Result{}}
		byName[name] = sv
		order = append(order, name)
		return sv
	}
	for _, name := range p.order {
		get(name)
	}
	results := make([]health.Result, len(p.health))
	versions := make([]string, len(p.versions))
	verrs := make([]string, len(p.versions))
	var wg sync.WaitGroup
	for i, t := range p.health {
		wg.Go(func() { results[i] = s.Health.Check(ctx, t.URL) })
	}
	for i, t := range p.versions {
		wg.Go(func() {
			v, err := s.Health.Version(ctx, t.URL)
			versions[i] = v
			if err != nil {
				verrs[i] = err.Error()
			}
		})
	}
	wg.Wait()
	for i, t := range p.health {
		sv := get(t.Name)
		sv.Checks = append(sv.Checks, results[i])
	}
	for i, t := range p.versions {
		sv := get(t.Name)
		sv.Version, sv.VersionError = versions[i], verrs[i]
	}
	out := make([]Service, 0, len(order))
	for _, name := range order {
		sv := byName[name]
		sv.Status = rollup(sv.Checks)
		out = append(out, *sv)
	}
	return out
}

// rollup: all checks pass = up, some = degraded, none = down. A service
// that only has a version source is "up" if the version was readable.
func rollup(checks []health.Result) string {
	if len(checks) == 0 {
		return ""
	}
	ok := 0
	for _, c := range checks {
		if c.OK {
			ok++
		}
	}
	switch ok {
	case len(checks):
		return StatusUp
	case 0:
		return StatusDown
	default:
		return StatusDegraded
	}
}

func (s *OverviewService) certTargets() []config.TLSTarget {
	if s.CertsFn != nil {
		return s.CertsFn()
	}
	return s.Certs
}

func (s *OverviewService) certificates(ctx context.Context) []Certificate {
	targets := s.certTargets()
	var kb strings.Builder
	for _, t := range targets {
		kb.WriteString(t.Label + "@" + t.Address + ",")
	}
	key := kb.String()
	s.mu.Lock()
	if s.certCache != nil && s.certKey == key && s.Now().Sub(s.certAt) < s.CertTTL {
		c := s.certCache
		s.mu.Unlock()
		return s.withDaysLeft(c)
	}
	s.mu.Unlock()

	out := make([]Certificate, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Go(func() {
			c := Certificate{Target: t.Label, ServerName: t.ServerName, Address: t.Address, CheckedAt: s.Now().UTC()}
			cert, err := s.TLS.Probe(ctx, t.ServerName, t.Address)
			if err != nil {
				c.Status, c.Error = CertError, err.Error()
			} else {
				c.Cert = cert
			}
			out[i] = c
		})
	}
	wg.Wait()
	s.mu.Lock()
	s.certCache, s.certAt, s.certKey = out, s.Now(), key
	s.mu.Unlock()
	return s.withDaysLeft(out)
}

// withDaysLeft computes status and days left against the current time
// (the cache may be a few minutes old).
func (s *OverviewService) withDaysLeft(in []Certificate) []Certificate {
	now := s.Now()
	out := make([]Certificate, len(in))
	copy(out, in)
	for i := range out {
		c := &out[i]
		if c.Cert == nil {
			continue
		}
		days := c.Cert.NotAfter.Sub(now).Hours() / 24
		c.DaysLeft = &days
		c.Status = CertStatus(c.Cert, now)
		if c.Status == CertError && c.Error == "" {
			c.Error = c.Cert.VerifyError
		}
	}
	return out
}

// CertStatus grades a certificate. Expiry wins over trust problems so an
// expired cert reads "expired", not "untrusted".
func CertStatus(c *tlsprobe.Cert, now time.Time) string {
	left := c.NotAfter.Sub(now)
	switch {
	case left <= 0:
		return CertExpired
	case !c.Trusted:
		return CertError
	case left < 7*24*time.Hour:
		return CertCritical
	case left < 21*24*time.Hour:
		return CertWarning
	default:
		return CertOK
	}
}
