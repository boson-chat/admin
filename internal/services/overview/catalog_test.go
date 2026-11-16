package overview

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/boson-chat/admin/config"
	"github.com/boson-chat/admin/internal/clients/dockerapi"
	"github.com/boson-chat/admin/internal/clients/health"
	"github.com/boson-chat/admin/internal/clients/ircprobe"
)

type fakeDocker struct {
	list []dockerapi.Container
	err  error
}

func (f *fakeDocker) List(_ context.Context, project string) ([]dockerapi.Container, error) {
	if project != "boson" {
		return nil, errors.New("wrong project " + project)
	}
	return f.list, f.err
}

func ctr(service, state, image string, labels ...string) dockerapi.Container {
	l := map[string]string{dockerapi.LabelProject: "boson", dockerapi.LabelService: service}
	for i := 0; i+1 < len(labels); i += 2 {
		l[labels[i]] = labels[i+1]
	}
	return dockerapi.Container{ID: service, Image: image, State: state, Labels: l}
}

// versionHealth: /metrics and /version answer, anything else 404s.
type versionHealth struct{ down map[string]bool }

func (f versionHealth) Check(_ context.Context, url string) health.Result {
	return health.Result{URL: url, OK: !f.down[url]}
}
func (f versionHealth) Version(_ context.Context, url string) (string, error) {
	switch url {
	case "http://directory:8081/metrics":
		return "1.8.0", nil
	case "http://files:3001/metrics":
		return "1.0.1", nil
	case "http://auth:8081/metrics":
		return "1.15.0", nil
	}
	return "", errors.New("HTTP 404")
}

var testHosts = Hosts{Auth: "auth.example.com", Voice: "voice.example.com", IRC: "irc.example.com",
	API: "api.example.com", Directory: "directory.example.com", Files: "files.example.com", Video: "video.example.com"}

func names(svcs []Service) string {
	var n []string
	for _, s := range svcs {
		n = append(n, s.Name)
	}
	return strings.Join(n, ",")
}

func probeNames(p []config.PublicProbe) string {
	var n []string
	for _, x := range p {
		n = append(n, x.Name)
	}
	return strings.Join(n, ",")
}

func TestDiscoveryShowsWhatRuns(t *testing.T) {
	dk := &fakeDocker{list: []dockerapi.Container{
		ctr("auth", "running", "harbor.example/boson/auth:1.15.0"),
		ctr("ergo", "running", "harbor.example/boson/stack-ergo:2.19.1", dockerapi.LabelVersion, "v2.19.1"),
		ctr("api", "running", "harbor.example/boson/backend:0.55.1"),
		ctr("quicvideod", "running", "harbor.example/boson/quicvideo:1.8.1"),
		ctr("directory", "running", "harbor.example/boson/directory:1.8.0"),
		ctr("files", "running", "harbor.example/boson/files:1.0.1"),
		ctr("minio", "exited", "bitnamilegacy/minio:2025.7.23-debian-12-r5", dockerapi.LabelVersion, "2025.7.23"),
		ctr("victoriametrics", "running", "victoriametrics/victoria-metrics:v1.131.0", dockerapi.LabelVersion, "v1.131.0"),
		ctr("postgres", "running", "postgres:18-alpine"),
		ctr("admin", "running", "harbor.example/boson/admin:latest"),
	}}
	fi := &fakeIRC{res: ircprobe.Result{OK: true, Version: "ergo-2.19.1"}}
	s := NewOverviewService(Options{
		Health: versionHealth{down: map[string]bool{"http://minio:9000/minio/health/live": true}}, TLS: &fakeTLS{},
		Docker: dk, Known: Builtins(testHosts), IRCProbe: fi,
		IRC: &config.IRCTarget{Address: "ergo:6697", Label: "ircs://ergo:6697", TLS: true},
		// HEALTH_TARGETS as derived from AUTH_INTERNAL_URL, plus a custom service.
		Targets:  []config.Target{{Name: "auth", URL: "http://auth:8081/healthz"}, {Name: "grafana", URL: "http://grafana:3000/api/health"}},
		Versions: []config.Target{{Name: "auth", URL: "http://auth:8081/metrics"}},
		Public: []config.PublicProbe{{Name: "api", URL: "https://api.other.example/health", Kind: "http"},
			{Name: "quicvoiced", URL: "https://voice.example.com/healthz", Kind: "http"}},
	})
	o := s.Overview(context.Background())
	if !o.Discovery.Enabled || o.Discovery.Error != "" {
		t.Fatalf("discovery: %+v", o.Discovery)
	}
	// quicvoiced has no container: hidden even though PUBLIC_PROBE_TARGETS names it.
	if got := names(o.Services); got != "auth,quicvideod,irc,api,directory,files,victoriametrics,minio,grafana" {
		t.Fatalf("services: %s", got)
	}
	by := map[string]Service{}
	for _, sv := range o.Services {
		by[sv.Name] = sv
	}
	if a := by["auth"]; len(a.Checks) != 1 || a.Version != "1.15.0" {
		t.Fatalf("auth (env replaces the map's checks): %+v", a)
	}
	for name, want := range map[string]string{"directory": "1.8.0", "files": "1.0.1", "api": "0.55.1", "quicvideod": "1.8.1",
		"victoriametrics": "1.131.0", "minio": "2025.7.23", "irc": "ergo-2.19.1"} {
		if v := by[name].Version; v != want || by[name].VersionError != "" {
			t.Errorf("%s version %q (%q), want %q", name, v, by[name].VersionError, want)
		}
	}
	if by["minio"].Status != StatusDown || by["directory"].Status != StatusUp || by["irc"].Status != StatusUp {
		t.Fatalf("statuses: %+v", o.Services)
	}
	if got := probeNames(o.PublicProbes); got != "admin,auth,quicvideod,irc,directory,files,api" {
		t.Fatalf("public probes: %s", got)
	}
	for _, p := range o.PublicProbes {
		if p.Name == "api" && p.URL != "https://api.other.example/health" {
			t.Fatalf("env probe should replace the map's: %+v", p)
		}
		if p.Name == "irc" && (p.Kind != "ws" || p.URL != "wss://irc.example.com/webirc") {
			t.Fatalf("irc probe: %+v", p)
		}
	}
}

func TestDiscoveryWithoutOptionalProfiles(t *testing.T) {
	dk := &fakeDocker{list: []dockerapi.Container{ctr("auth", "running", "auth:1.0.0")}}
	fi := &fakeIRC{res: ircprobe.Result{OK: true}}
	s := NewOverviewService(Options{Health: versionHealth{}, TLS: &fakeTLS{}, Docker: dk, Known: Builtins(Hosts{}),
		IRCProbe: fi, IRC: &config.IRCTarget{Address: "ergo:6697", Label: "ircs://ergo:6697"}})
	o := s.Overview(context.Background())
	if names(o.Services) != "auth" || o.IRC.Configured || fi.calls.Load() != 0 {
		t.Fatalf("services %s, irc %+v, %d probes", names(o.Services), o.IRC, fi.calls.Load())
	}
	if probeNames(o.PublicProbes) != "admin" {
		t.Fatalf("no hostnames, no public probes: %s", probeNames(o.PublicProbes))
	}
}

func TestDiscoveryFailureFallsBack(t *testing.T) {
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	clock := now
	dk := &fakeDocker{list: []dockerapi.Container{ctr("auth", "running", "auth:1.0.0"), ctr("directory", "running", "d:1")}}
	s := NewOverviewService(Options{Health: versionHealth{}, TLS: &fakeTLS{}, Docker: dk, Known: Builtins(testHosts),
		Targets: []config.Target{{Name: "auth", URL: "http://auth:8081/healthz"}}, Now: func() time.Time { return clock }})
	if got := names(s.Overview(context.Background()).Services); got != "auth,directory" {
		t.Fatalf("services: %s", got)
	}
	dk.err = errors.New("the Docker socket proxy is unreachable")
	clock = now.Add(time.Minute)
	o := s.Overview(context.Background())
	if names(o.Services) != "auth,directory" || !o.Discovery.Enabled || o.Discovery.Error == "" {
		t.Fatalf("recent listing should stand in: %s %+v", names(o.Services), o.Discovery)
	}
	clock = now.Add(time.Hour)
	o = s.Overview(context.Background())
	if names(o.Services) != "auth" || o.Discovery.Enabled || o.Discovery.Error == "" {
		t.Fatalf("stale listing: env lists only: %s %+v", names(o.Services), o.Discovery)
	}
}

func TestNoDockerUsesEnvLists(t *testing.T) {
	s := NewOverviewService(Options{Health: versionHealth{}, TLS: &fakeTLS{}, Known: Builtins(testHosts),
		Targets: []config.Target{{Name: "auth", URL: "http://auth:8081/healthz"}, {Name: "api", URL: "http://api:3000/health"}}})
	o := s.Overview(context.Background())
	if names(o.Services) != "auth,api" || o.Discovery.Enabled || probeNames(o.PublicProbes) != "admin" {
		t.Fatalf("%s %+v %s", names(o.Services), o.Discovery, probeNames(o.PublicProbes))
	}
	if len(s.PublicOrigins()) != 0 {
		t.Fatalf("CSP without the proxy: only PUBLIC_PROBE_TARGETS: %v", s.PublicOrigins())
	}
	s.Docker = &fakeDocker{}
	if got := strings.Join(s.PublicOrigins(), " "); !strings.Contains(got, "https://files.example.com") || !strings.Contains(got, "wss://irc.example.com") {
		t.Fatalf("CSP origins: %s", got)
	}
}

func TestImageVersion(t *testing.T) {
	for img, want := range map[string]string{
		"harbor.example/boson/backend:0.55.1": "0.55.1",
		"traefik:v3.7.19":                     "3.7.19",
		"x/certsync:latest":                   "",
		"postgres":                            "",
		"localhost:5000/app:2.0@sha256:abc":   "2.0",
		"localhost:5000/app":                  "",
	} {
		if got := imageVersion(dockerapi.Container{Image: img}); got != want {
			t.Errorf("%s: %q, want %q", img, got, want)
		}
	}
}
