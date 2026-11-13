// Package overview gathers the stack's health: service checks, versions,
// TLS certificate expiry and a few live numbers from the metrics backend.
package overview

import (
	"context"
	"sync"
	"time"

	"github.com/boson-chat/admin/config"
	"github.com/boson-chat/admin/internal/clients/health"
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
}

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
	Grafana  string
	Version  string
	Now      func() time.Time
	// CertTTL caches certificate probes; they change rarely and dialing
	// public hostnames on every poll is wasteful.
	CertTTL time.Duration

	mu        sync.Mutex
	certCache []Certificate
	certAt    time.Time
}

type Options struct {
	Health   health.HealthCheckerImpl
	TLS      tlsprobe.TLSProberImpl
	Prom     promql.PromClientImpl
	Targets  []config.Target
	Versions []config.Target
	Certs    []config.TLSTarget
	Grafana  string
	Version  string
	Now      func() time.Time
}

func NewOverviewService(o Options) *OverviewService {
	if o.Now == nil {
		o.Now = time.Now
	}
	return &OverviewService{Health: o.Health, TLS: o.TLS, Prom: o.Prom, Targets: o.Targets, Versions: o.Versions,
		Certs: o.Certs, Grafana: o.Grafana, Version: o.Version, Now: o.Now, CertTTL: 5 * time.Minute}
}

func (s *OverviewService) Overview(ctx context.Context) *Overview {
	out := &Overview{CheckedAt: s.Now().UTC(), Panel: PanelInfo{Version: s.Version}, GrafanaURL: s.Grafana}
	var wg sync.WaitGroup
	wg.Go(func() { out.Services = s.services(ctx) })
	wg.Go(func() { out.Certificates = s.certificates(ctx) })
	wg.Go(func() { out.Metrics = s.metrics(ctx) })
	wg.Wait()
	return out
}

func (s *OverviewService) services(ctx context.Context) []Service {
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
	results := make([]health.Result, len(s.Targets))
	versions := make([]string, len(s.Versions))
	verrs := make([]string, len(s.Versions))
	var wg sync.WaitGroup
	for i, t := range s.Targets {
		wg.Go(func() { results[i] = s.Health.Check(ctx, t.URL) })
	}
	for i, t := range s.Versions {
		wg.Go(func() {
			v, err := s.Health.Version(ctx, t.URL)
			versions[i] = v
			if err != nil {
				verrs[i] = err.Error()
			}
		})
	}
	wg.Wait()
	for i, t := range s.Targets {
		sv := get(t.Name)
		sv.Checks = append(sv.Checks, results[i])
	}
	for i, t := range s.Versions {
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

func (s *OverviewService) certificates(ctx context.Context) []Certificate {
	s.mu.Lock()
	if s.certCache != nil && s.Now().Sub(s.certAt) < s.CertTTL {
		c := s.certCache
		s.mu.Unlock()
		return s.withDaysLeft(c)
	}
	s.mu.Unlock()

	out := make([]Certificate, len(s.Certs))
	var wg sync.WaitGroup
	for i, t := range s.Certs {
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
	s.certCache, s.certAt = out, s.Now()
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
