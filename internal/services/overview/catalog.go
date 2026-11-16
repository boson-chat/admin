package overview

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/boson-chat/admin/config"
	"github.com/boson-chat/admin/internal/clients/dockerapi"
)

// Known is a compose service of the boson stack the overview knows how to
// probe. With the Docker socket proxy on, it is shown when its compose
// project has a container for it (running or not), so an optional profile
// that is off doesn't show up as down.
type Known struct {
	// Name is the overview's name (and PUBLIC_PROBE_TARGETS / HEALTH_TARGETS name).
	Name string
	// Service is the compose service whose container must exist.
	Service string
	// Health URLs on the compose network; empty = no HTTP checks (IRC is
	// probed by IRC_CHECK instead).
	Health []string
	// Version source (see health.Version); empty = the container image's
	// version only.
	Version string
	// Public is the endpoint the browser times; empty = none.
	Public string
}

// Hosts are the stack's public hostnames (the stack .env's *_HOST).
type Hosts struct {
	Auth, Voice, IRC, API, Directory, Files, Video string
}

// Builtins is the stack's probe map. A public probe is only listed when its
// hostname is known.
func Builtins(h Hosts) []Known {
	pub := func(scheme, host, path string) string {
		if host = strings.TrimSpace(host); host == "" {
			return ""
		}
		return scheme + "://" + host + path
	}
	return []Known{
		{Name: "auth", Service: "auth", Health: []string{"http://auth:8081/healthz", "http://auth:8081/readyz"},
			Version: "http://auth:8081/metrics", Public: pub("https", h.Auth, "/healthz")},
		{Name: "quicvoiced", Service: "quicvoiced", Health: []string{"http://quicvoiced:8080/healthz"},
			Version: "http://quicvoiced:8080/version", Public: pub("https", h.Voice, "/healthz")},
		{Name: "quicvideod", Service: "quicvideod", Health: []string{"http://quicvideod:8080/healthz"},
			Version: "http://quicvideod:8080/version", Public: pub("https", h.Video, "/healthz")},
		{Name: IRCServiceName, Service: "ergo", Public: pub("wss", h.IRC, "/webirc")},
		// The api has no version endpoint on the compose network: the image tag.
		{Name: "api", Service: "api", Health: []string{"http://api:3000/health"}, Public: pub("https", h.API, "/health")},
		{Name: "directory", Service: "directory", Health: []string{"http://directory:8081/healthz"},
			Version: "http://directory:8081/metrics", Public: pub("https", h.Directory, "/healthz")},
		{Name: "files", Service: "files", Health: []string{"http://files:3001/healthz"},
			Version: "http://files:3001/metrics", Public: pub("https", h.Files, "/healthz")},
		{Name: "victoriametrics", Service: "victoriametrics", Health: []string{"http://victoriametrics:8428/health"}},
		{Name: "minio", Service: "minio", Health: []string{"http://minio:9000/minio/health/live"}},
	}
}

// Containers lists a compose project's containers (the socket proxy).
type Containers interface {
	List(ctx context.Context, project string) ([]dockerapi.Container, error)
}

// present is one compose service that has a container.
type present struct {
	State   string
	Version string // the image's version, "" if unknown
}

// discovery is what the socket proxy says exists right now.
type discovery struct {
	services map[string]present
	err      string
}

// discoverStale: how long the last good listing stands in when the proxy
// fails, before the overview falls back to the env lists.
const discoverStale = 5 * time.Minute

// discover lists the project's containers. ok=false means use the env
// lists (proxy off, or failing with no recent listing).
func (s *OverviewService) discover(ctx context.Context) (d discovery, ok bool) {
	if s.Docker == nil {
		return discovery{}, false
	}
	list, err := s.Docker.List(ctx, s.Project)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		if s.discCache != nil && s.Now().Sub(s.discAt) < discoverStale {
			return discovery{services: s.discCache, err: err.Error()}, true
		}
		return discovery{err: err.Error()}, false
	}
	m := map[string]present{}
	for _, c := range list {
		name := c.Labels[dockerapi.LabelService]
		if name == "" {
			continue
		}
		if prev, seen := m[name]; seen && prev.State == "running" {
			continue // scaled: any running replica speaks for the service
		}
		m[name] = present{State: c.State, Version: imageVersion(c)}
	}
	s.discCache, s.discAt = m, s.Now()
	return discovery{services: m}, true
}

// imageVersion: the OCI version label, else the image tag (not "latest").
func imageVersion(c dockerapi.Container) string {
	v := c.Labels[dockerapi.LabelVersion]
	if v == "" {
		img := c.Image
		if i := strings.LastIndex(img, "/"); i >= 0 {
			img = img[i+1:]
		}
		img, _, _ = strings.Cut(img, "@")
		if _, tag, ok := strings.Cut(img, ":"); ok && tag != "latest" {
			v = tag
		}
	}
	// v1.2.3 → 1.2.3, like the services' own version endpoints.
	if len(v) > 1 && v[0] == 'v' && v[1] >= '0' && v[1] <= '9' {
		v = v[1:]
	}
	return v
}

// plan is what to probe: health and version targets by service, in order.
type plan struct {
	health   []config.Target
	versions []config.Target
	order    []string
	// images are container image versions, the fallback when a service has
	// no version source or it fails.
	images map[string]string
	// irc: probe IRC_CHECK.
	irc bool
}

// knownBy indexes the probe map by overview name.
func (s *OverviewService) knownBy() map[string]Known {
	m := make(map[string]Known, len(s.Known))
	for _, k := range s.Known {
		m[k.Name] = k
	}
	return m
}

// gated: a name from the probe map whose container doesn't exist.
func gated(known map[string]Known, d discovery, name string) bool {
	k, ok := known[name]
	if !ok {
		return false
	}
	_, there := d.services[k.Service]
	return !there
}

// planFor merges the probe map (for services that have a container) with
// the env lists: an env name replaces the map's checks (or version
// source) for that service, a new name is added. Without discovery only
// the env lists are probed.
func (s *OverviewService) planFor(d discovery, ok bool) plan {
	p := plan{images: map[string]string{}, irc: s.IRC != nil && s.IRCProbe != nil}
	seen := map[string]bool{}
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			p.order = append(p.order, name)
		}
	}
	if !ok {
		for _, t := range s.Targets {
			add(t.Name)
		}
		for _, t := range s.Versions {
			add(t.Name)
		}
		p.health, p.versions = s.Targets, s.Versions
		return p
	}
	known := s.knownBy()
	envHealth, envVersion := map[string]bool{}, map[string]bool{}
	for _, t := range s.Targets {
		envHealth[t.Name] = true
	}
	for _, t := range s.Versions {
		envVersion[t.Name] = true
	}
	for _, k := range s.Known {
		c, there := d.services[k.Service]
		if !there {
			continue
		}
		if k.Name == IRCServiceName {
			if p.irc {
				add(k.Name)
			}
		} else {
			add(k.Name)
		}
		if c.Version != "" {
			p.images[k.Name] = c.Version
		}
		if !envHealth[k.Name] {
			for _, u := range k.Health {
				p.health = append(p.health, config.Target{Name: k.Name, URL: u})
			}
		}
		if !envVersion[k.Name] && k.Version != "" {
			p.versions = append(p.versions, config.Target{Name: k.Name, URL: k.Version})
		}
	}
	if gated(known, d, IRCServiceName) {
		p.irc = false // no ergo container: the irc profile is off
	}
	for _, t := range s.Targets {
		if gated(known, d, t.Name) {
			continue
		}
		add(t.Name)
		p.health = append(p.health, t)
	}
	for _, t := range s.Versions {
		if gated(known, d, t.Name) {
			continue
		}
		add(t.Name)
		p.versions = append(p.versions, t)
	}
	return p
}

// publicFor is the browser's probe list: the panel itself, then the probe
// map's public endpoints for services that have a container, with
// PUBLIC_PROBE_TARGETS replacing (same name) or adding to them.
func (s *OverviewService) publicFor(d discovery, ok bool) []config.PublicProbe {
	if !ok {
		return s.Public
	}
	known := s.knownBy()
	env := map[string]bool{}
	for _, p := range s.Public {
		env[p.Name] = true
	}
	var out []config.PublicProbe
	for _, p := range s.Public {
		if p.Name == "admin" {
			out = append(out, p)
		}
	}
	for _, k := range s.Known {
		if k.Public == "" || env[k.Name] || gated(known, d, k.Name) {
			continue
		}
		if pp, err := publicProbe(k.Name, k.Public); err == nil {
			out = append(out, pp)
		}
	}
	for _, p := range s.Public {
		if p.Name == "admin" || gated(known, d, p.Name) {
			continue
		}
		out = append(out, p)
	}
	return out
}

func publicProbe(name, raw string) (config.PublicProbe, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return config.PublicProbe{}, err
	}
	kind := "http"
	if u.Scheme == "wss" || u.Scheme == "ws" {
		kind = "ws"
	}
	return config.PublicProbe{Name: name, URL: raw, Kind: kind, Origin: u.Scheme + "://" + u.Host}, nil
}

// PublicOrigins is every origin the browser may be asked to time (the
// CSP's connect-src): the probe map's public endpoints and
// PUBLIC_PROBE_TARGETS, whichever services turn out to be running.
func (s *OverviewService) PublicOrigins() []string {
	var out []string
	for _, p := range s.Public {
		if p.Origin != "" {
			out = append(out, p.Origin)
		}
	}
	if s.Docker == nil {
		return out
	}
	for _, k := range s.Known {
		if pp, err := publicProbe(k.Name, k.Public); k.Public != "" && err == nil {
			out = append(out, pp.Origin)
		}
	}
	return out
}
