package overview

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/boson-chat/admin/config"
	"github.com/boson-chat/admin/internal/clients/health"
	"github.com/boson-chat/admin/internal/clients/ircprobe"
	"github.com/boson-chat/admin/internal/clients/promql"
	"github.com/boson-chat/admin/internal/clients/tlsprobe"
)

type fakeHealth struct{ down map[string]bool }

func (f fakeHealth) Check(_ context.Context, url string) health.Result {
	return health.Result{URL: url, OK: !f.down[url], StatusCode: map[bool]int{true: 503, false: 200}[f.down[url]]}
}
func (f fakeHealth) Version(_ context.Context, url string) (string, error) {
	if url == "http://broken/version" {
		return "", errors.New("HTTP 404")
	}
	return "1.2.3", nil
}

type fakeTLS struct {
	calls atomic.Int32
	certs map[string]*tlsprobe.Cert
}

func (f *fakeTLS) Probe(_ context.Context, name, _ string) (*tlsprobe.Cert, error) {
	f.calls.Add(1)
	if c, ok := f.certs[name]; ok {
		return c, nil
	}
	return nil, errors.New("connection refused")
}

type fakeProm struct{ fail bool }

func (f fakeProm) Query(_ context.Context, q string) ([]promql.Sample, error) {
	if f.fail {
		return nil, errors.New("metrics backend unreachable")
	}
	switch q {
	case `sum by (outcome) (increase(auth_login_attempts_total[1h]))`:
		return []promql.Sample{{Labels: map[string]string{"outcome": "invalid_credentials"}, Value: 2}, {Labels: map[string]string{"outcome": "success"}, Value: 40.004}}, nil
	case `sum(quicvoice_rooms)`:
		return []promql.Sample{{Value: 3}}, nil
	}
	return nil, nil // no data
}
func (f fakeProm) QueryRange(context.Context, string, time.Time, time.Time, time.Duration) ([]promql.Point, error) {
	return []promql.Point{{T: time.Unix(1, 0), V: 1}, {T: time.Unix(61, 0), V: 2}}, nil
}

func TestServicesRollup(t *testing.T) {
	s := NewOverviewService(Options{
		Health: fakeHealth{down: map[string]bool{"http://auth/readyz": true, "http://irc/healthz": true}},
		TLS:    &fakeTLS{},
		Targets: []config.Target{
			{Name: "auth", URL: "http://auth/healthz"}, {Name: "auth", URL: "http://auth/readyz"},
			{Name: "voice", URL: "http://voice/healthz"}, {Name: "irc", URL: "http://irc/healthz"},
		},
		Versions: []config.Target{{Name: "voice", URL: "http://voice/version"}, {Name: "web", URL: "http://broken/version"}},
	})
	o := s.Overview(context.Background())
	want := map[string]string{"auth": StatusDegraded, "voice": StatusUp, "irc": StatusDown, "web": ""}
	if len(o.Services) != 4 {
		t.Fatalf("services: %+v", o.Services)
	}
	for _, sv := range o.Services {
		if sv.Status != want[sv.Name] {
			t.Errorf("%s: status %q, want %q", sv.Name, sv.Status, want[sv.Name])
		}
	}
	if o.Services[1].Version != "1.2.3" || o.Services[3].VersionError == "" {
		t.Fatalf("versions: %+v", o.Services)
	}
	if o.Metrics.Configured {
		t.Fatal("metrics configured without a Prometheus client")
	}
}

func TestCertificatesGradedAndCached(t *testing.T) {
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	clock := now
	ft := &fakeTLS{certs: map[string]*tlsprobe.Cert{
		"ok.example":      {NotAfter: now.Add(60 * 24 * time.Hour), Trusted: true},
		"soon.example":    {NotAfter: now.Add(10 * 24 * time.Hour), Trusted: true},
		"selfsig.example": {NotAfter: now.Add(60 * 24 * time.Hour), Trusted: false, VerifyError: "unknown authority"},
		"old.example":     {NotAfter: now.Add(-time.Hour), Trusted: false},
	}}
	s := NewOverviewService(Options{Health: fakeHealth{}, TLS: ft, Now: func() time.Time { return clock },
		Certs: []config.TLSTarget{
			{ServerName: "ok.example", Label: "ok"}, {ServerName: "soon.example", Label: "soon"},
			{ServerName: "selfsig.example", Label: "selfsig"}, {ServerName: "old.example", Label: "old"},
			{ServerName: "gone.example", Label: "gone"},
		}})
	o := s.Overview(context.Background())
	want := []string{CertOK, CertWarning, CertError, CertExpired, CertError}
	for i, c := range o.Certificates {
		if c.Status != want[i] {
			t.Errorf("%s: %q, want %q", c.Target, c.Status, want[i])
		}
	}
	if o.Certificates[2].Error != "unknown authority" || o.Certificates[4].Error == "" {
		t.Fatalf("errors not reported: %+v", o.Certificates)
	}
	if d := *o.Certificates[0].DaysLeft; d != 60 {
		t.Fatalf("days left %v", d)
	}
	// Cached within the TTL, but days left move with the clock.
	clock = now.Add(4 * 24 * time.Hour)
	s.CertTTL = 30 * 24 * time.Hour
	o = s.Overview(context.Background())
	if ft.calls.Load() != 5 {
		t.Fatalf("probed again: %d calls", ft.calls.Load())
	}
	if o.Certificates[1].Status != CertCritical {
		t.Fatalf("status not regraded from cache: %q", o.Certificates[1].Status)
	}
}

func TestMetrics(t *testing.T) {
	s := NewOverviewService(Options{Health: fakeHealth{}, TLS: &fakeTLS{}, Prom: fakeProm{}})
	m := s.Overview(context.Background()).Metrics
	if !m.Configured || m.Error != "" || len(m.Stats) != len(statDefs) {
		t.Fatalf("metrics: %+v", m)
	}
	by := map[string]Stat{}
	for _, st := range m.Stats {
		by[st.Key] = st
	}
	if v := by["voice_rooms"].Value; v == nil || *v != 3 {
		t.Fatalf("rooms: %v", v)
	}
	if by["voice_participants"].Value != nil {
		t.Fatal("no data should be null, not 0")
	}
	logins := by["auth_logins"]
	if *logins.Value != 42 || len(logins.Breakdown) != 2 || logins.Breakdown[0].Label != "success" || logins.Breakdown[0].Value != 40 {
		t.Fatalf("logins: %+v", logins)
	}
	if len(by["voice_rooms"].Trend) != 2 {
		t.Fatal("no trend")
	}

	s.Prom = fakeProm{fail: true}
	m = s.Overview(context.Background()).Metrics
	if m.Error != "metrics backend unreachable" {
		t.Fatalf("shared error not surfaced: %q", m.Error)
	}
}

type fakeIRC struct {
	calls atomic.Int32
	res   ircprobe.Result
}

func (f *fakeIRC) Probe(_ context.Context, _, _ string, _ bool) ircprobe.Result {
	f.calls.Add(1)
	return f.res
}

func TestIRCProbe(t *testing.T) {
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	clock := now
	fi := &fakeIRC{res: ircprobe.Result{OK: true, Latency: 12 * time.Millisecond, Version: "ergo-2.19.1"}}
	target := &config.IRCTarget{Address: "ergo:6697", ServerName: "irc.example.com", TLS: true, Label: "ircs://ergo:6697"}
	s := NewOverviewService(Options{Health: fakeHealth{}, TLS: &fakeTLS{}, IRCProbe: fi, IRC: target,
		Targets: []config.Target{{Name: "auth", URL: "http://auth/healthz"}}, Now: func() time.Time { return clock }})
	o := s.Overview(context.Background())
	if !o.IRC.Configured || o.IRC.Target != "ircs://ergo:6697" || o.Edge.Name != "Traefik" {
		t.Fatalf("overview: %+v %+v", o.IRC, o.Edge)
	}
	if len(o.Services) != 2 || o.Services[1].Name != "irc" || o.Services[1].Status != StatusUp ||
		o.Services[1].Version != "ergo-2.19.1" || o.Services[1].Checks[0].LatencyMS != 12 {
		t.Fatalf("services: %+v", o.Services)
	}

	// Cached within the TTL.
	fi.res = ircprobe.Result{Detail: "connection refused"}
	clock = now.Add(10 * time.Second)
	if o = s.Overview(context.Background()); fi.calls.Load() != 1 || o.Services[1].Status != StatusUp {
		t.Fatalf("not cached: %d calls, %+v", fi.calls.Load(), o.Services[1])
	}
	clock = now.Add(time.Minute)
	o = s.Overview(context.Background())
	if o.Services[1].Status != StatusDown || o.Services[1].Checks[0].Detail != "connection refused" {
		t.Fatalf("down: %+v", o.Services[1])
	}
}

func TestIRCMergedWithHealthTarget(t *testing.T) {
	fi := &fakeIRC{res: ircprobe.Result{OK: false, Detail: "timed out"}}
	s := NewOverviewService(Options{Health: fakeHealth{}, TLS: &fakeTLS{}, IRCProbe: fi, Edge: "Caddy",
		IRC:     &config.IRCTarget{Address: "ergo:6697", Label: "ircs://ergo:6697"},
		Targets: []config.Target{{Name: "irc", URL: "http://irc/healthz"}}})
	o := s.Overview(context.Background())
	if len(o.Services) != 1 || len(o.Services[0].Checks) != 2 || o.Services[0].Status != StatusDegraded || o.Edge.Name != "Caddy" {
		t.Fatalf("services: %+v", o.Services)
	}
}

func TestIRCNotConfigured(t *testing.T) {
	o := NewOverviewService(Options{Health: fakeHealth{}, TLS: &fakeTLS{}}).Overview(context.Background())
	if o.IRC.Configured || len(o.Services) != 0 {
		t.Fatalf("%+v %+v", o.IRC, o.Services)
	}
}

func TestPublicProbesIncludeThePanel(t *testing.T) {
	o := NewOverviewService(Options{Health: fakeHealth{}, TLS: &fakeTLS{},
		Public: []config.PublicProbe{{Name: "auth", URL: "https://auth.example.com/healthz", Kind: "http"}}}).Overview(context.Background())
	if len(o.PublicProbes) != 2 || o.PublicProbes[0].Name != "admin" || o.PublicProbes[0].URL != "/healthz" || o.PublicProbes[1].Name != "auth" {
		t.Fatalf("%+v", o.PublicProbes)
	}
	o = NewOverviewService(Options{Health: fakeHealth{}, TLS: &fakeTLS{},
		Public: []config.PublicProbe{{Name: "admin", URL: "https://admin.example.com/healthz", Kind: "http"}}}).Overview(context.Background())
	if len(o.PublicProbes) != 1 {
		t.Fatalf("%+v", o.PublicProbes)
	}
}
