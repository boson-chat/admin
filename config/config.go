// Package config holds the panel configuration, loaded from env vars.
package config

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/jinzhu/configor"
)

type Config struct {
	AppConfig       AppConfig
	HTTPConfig      HTTPConfig
	SessionConfig   SessionConfig
	UpstreamConfig  UpstreamConfig
	DockerConfig    DockerConfig
	BosonConfig     BosonConfig
	TelemetryConfig TelemetryConfig
	StackConfig     StackConfig
}

// StackConfig is the stack's shared config file (config/boson.yaml).
type StackConfig struct {
	// Path of the file inside the container, e.g. /config/boson.yaml; its
	// directory must be writable (saves are atomic renames). Empty = no
	// file: every setting comes from the env, as before.
	Path string `env:"BOSON_CONFIG"`
	// Comma-separated name=url of each service's GET /internal/config/status
	// (names are compose services: the restart button uses them). The
	// panel reports its own status itself.
	StatusTargets string `env:"CONFIG_STATUS_TARGETS"`
	// How often the file is re-read when no change event arrives.
	Poll time.Duration `default:"10s" env:"BOSON_CONFIG_POLL"`
	// Where branding images are written (branding.images names files in
	// it). Empty = branding/ next to the config file.
	BrandingDir string `env:"BRANDING_DIR"`
	// Where the emails' MJML sources and compiled HTML are written
	// (auth.email.layout and auth.email.templates.*.{mjml,html} name
	// files in it). Empty = emails/ next to the config file.
	EmailsDir string `env:"EMAILS_DIR"`
}

// Statuses parses CONFIG_STATUS_TARGETS.
func (c StackConfig) Statuses() ([]Target, error) {
	return parseTargets("CONFIG_STATUS_TARGETS", c.StatusTargets)
}

type DockerConfig struct {
	// Docker socket proxy, e.g. http://docker-proxy:2375. Empty = services
	// control off. Never point this at a raw Docker socket.
	ProxyURL string `env:"DOCKER_PROXY_URL"`
	// Compose project whose containers are shown; nothing else is touched.
	Project string `default:"boson" env:"DOCKER_PROJECT"`
	// Compose services that need their name typed to stop or restart.
	Protected string `default:"admin,traefik" env:"DOCKER_PROTECTED_SERVICES"`
	// Per request; stats take about a second.
	Timeout time.Duration `default:"20s" env:"DOCKER_TIMEOUT"`
}

type BosonConfig struct {
	// The stack's boson API (the api service), for admin profile reads.
	// Empty = profiles off.
	APIURL string `env:"BOSON_API_URL"`
	// The api's internal listener (http://api:3001) and its
	// INTERNAL_API_TOKEN (sent as X-Internal-Token): the Nick registration
	// page's status and Test connection. Empty URL = that page says the api
	// isn't reachable.
	APIInternalURL   string `env:"BOSON_API_INTERNAL_URL"`
	APIInternalToken string `env:"BOSON_API_INTERNAL_TOKEN"`
	// The files (uploads) service's internal listener (http://files:3001)
	// and its INTERNAL_TOKEN (X-Internal-Token): the Uploads page's live
	// status, the Uploaded files moderation list and users' usage. Empty
	// URL = those say the files service isn't reachable.
	FilesInternalURL   string `env:"BOSON_FILES_INTERNAL_URL"`
	FilesInternalToken string `env:"BOSON_FILES_INTERNAL_TOKEN"`
	// Where avatars and server icons are served from (img-src in the CSP).
	CDNURL string `default:"https://cdn.boson.chat" env:"BOSON_CDN_URL"`
	// The directory this network is listed in (github.com/boson-chat/directory).
	// Empty = the directory page is off.
	DirectoryURL string `default:"https://directory.boson.chat" env:"DIRECTORY_API_URL"`
	// Register / edit this network's listing there (network key + DNS proof).
	DirectoryRegistration bool `default:"true" env:"DIRECTORY_REGISTRATION"`
	// The stack's own directory (directory profile), e.g. http://directory:8080,
	// and its operator token: the "include the main directory" switch, peers,
	// and the listings a user owns. Empty = off.
	DirectoryLocalURL      string `env:"DIRECTORY_LOCAL_URL"`
	DirectoryInternalToken string `env:"DIRECTORY_INTERNAL_TOKEN"`
	// Secret for the directory management key; defaults to SESSION_KEY.
	DirectoryKey string `env:"DIRECTORY_KEY"`
	// The IRC hostname to list; defaults to IRC_CHECK_SERVER_NAME. With
	// BOSON_CONFIG, admin.directory_hostname wins (and
	// admin.directory_registration over DIRECTORY_REGISTRATION).
	DirectoryHostname string `env:"DIRECTORY_HOSTNAME"`
}

type AppConfig struct {
	APPName  string `default:"admin"`
	Env      string `default:"development" env:"ENV"`
	Version  string `default:"dev" env:"VERSION"`
	LogLevel string `default:"info" env:"LOG_LEVEL"`
}

type HTTPConfig struct {
	// Public listener: the UI and /api (behind the reverse proxy).
	Addr string `default:":8080" env:"HTTP_ADDR"`
	// Internal listener: /metrics, /healthz. Never publish it. Empty disables it.
	InternalAddr string `default:":8081" env:"INTERNAL_ADDR"`
	// Externally visible URL of the panel, e.g. https://admin.example.com.
	// State-changing requests must come from this origin.
	PublicURL string `default:"http://localhost:8080" env:"PUBLIC_URL"`
	// Comma-separated CIDRs/IPs whose X-Forwarded-For is trusted (the reverse proxy's container network).
	TrustedProxies string `env:"TRUSTED_PROXIES"`
}

type SessionConfig struct {
	// 32 bytes (base64 or hex) used to encrypt and authenticate the session cookie (AES-256-GCM).
	Key string `env:"SESSION_KEY"`
	// Secure flag on the session cookie. Turn off only for plain-http local dev.
	CookieSecure bool `default:"true" env:"COOKIE_SECURE"`
	// Absolute lifetime of a panel login; after it the admin signs in again.
	MaxAge time.Duration `default:"12h" env:"SESSION_MAX_AGE"`
}

type UpstreamConfig struct {
	// Auth service public API (admin API, login, refresh).
	AuthURL string `default:"http://auth:8080" env:"AUTH_URL"`
	// Auth service internal listener (/healthz, /readyz, /metrics). Empty = not checked.
	AuthInternalURL string `env:"AUTH_INTERNAL_URL"`
	// auth's INTERNAL_API_TOKEN: sent along to its internal email preview
	// and test endpoints (which authorise the admin's own access token).
	InternalAPIToken string `env:"INTERNAL_API_TOKEN"`
	// Prometheus-compatible query API (VictoriaMetrics, Prometheus). Empty = no live metrics.
	MetricsURL string `env:"METRICS_URL"`
	// Grafana base URL for links. Empty = no links.
	GrafanaURL string `env:"GRAFANA_URL"`
	// Comma-separated name=url health checks. Repeat a name to group several checks
	// under one service. Empty = derived from AUTH_INTERNAL_URL.
	HealthTargets string `env:"HEALTH_TARGETS"`
	// Comma-separated name=url version sources: a URL returning the version as text
	// or JSON {"version"}, or a Prometheus /metrics URL (target_info service_version).
	VersionTargets string `env:"VERSION_TARGETS"`
	// Comma-separated host:port[@dial-host:dial-port] whose TLS certificates
	// are checked. With BOSON_CONFIG, admin.tls_check_hosts wins. Empty =
	// derived from CERT_HOSTS.
	TLSCheckHosts string `env:"TLS_CHECK_HOSTS"`
	// The stack's certificate hostnames (its .env CERT_HOSTS). When
	// TLS_CHECK_HOSTS is empty each is checked on 443 through TLS_EDGE_ADDR,
	// and the IRC host (IRC_HOST / IRC_CHECK_SERVER_NAME) on IRC_CHECK's
	// port (6697) instead.
	CertHosts string `env:"CERT_HOSTS"`
	// Where CERT_HOSTS' 443 checks are dialled (the edge on the compose
	// network, e.g. traefik:443). Empty = each hostname itself.
	TLSEdgeAddr string `env:"TLS_EDGE_ADDR"`
	// The stack's public hostnames: the stack overview's built-in public
	// probes (with the Docker socket proxy on) for the services that run.
	AuthHost      string `env:"AUTH_HOST"`
	VoiceHost     string `env:"VOICE_HOST"`
	IRCHost       string `env:"IRC_HOST"`
	APIHost       string `env:"API_HOST"`
	DirectoryHost string `env:"DIRECTORY_HOST"`
	FilesHost     string `env:"FILES_HOST"`
	// IRC server to probe: ircs://host:port (TLS) or irc://host:port. Empty = not checked.
	IRCCheck string `env:"IRC_CHECK"`
	// SNI for the IRC TLS handshake (the public IRC host when IRC_CHECK dials
	// the container). Defaults to IRC_CHECK's host.
	IRCCheckServerName string `env:"IRC_CHECK_SERVER_NAME"`
	// Comma-separated name=url public endpoints the admin's browser times
	// (https://, http://, wss://, ws://). Their origins are added to the CSP's
	// connect-src. Names match HEALTH_TARGETS services (auth, api,
	// quicvoiced) or irc; the panel itself is always timed.
	PublicProbeTargets string `env:"PUBLIC_PROBE_TARGETS"`
	// What the stack overview calls the TLS edge (the reverse proxy).
	EdgeName string `default:"Traefik" env:"EDGE_NAME"`
	// Timeout for each upstream request.
	Timeout time.Duration `default:"5s" env:"UPSTREAM_TIMEOUT"`
}

type TelemetryConfig struct {
	OTLPEndpoint        string `env:"OTEL_EXPORTER_OTLP_ENDPOINT"`
	OTLPMetricsEndpoint string `env:"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"`
	OTLPProtocol        string `env:"OTEL_EXPORTER_OTLP_PROTOCOL"`
	OTLPMetricsProtocol string `env:"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL"`
	MetricsExporter     string `env:"OTEL_METRICS_EXPORTER"`
}

// LoadConfig reads the configuration from the environment.
func LoadConfig() (Config, error) {
	var cfg Config
	if err := configor.New(&configor.Config{ENVPrefix: "-", Silent: true}).Load(&cfg); err != nil {
		return cfg, err
	}
	cfg.ApplyDefaults()
	return cfg, cfg.Validate()
}

// ApplyDefaults normalises URLs and fills derived values.
func (c *Config) ApplyDefaults() {
	trim := func(s string) string { return strings.TrimRight(strings.TrimSpace(s), "/") }
	c.HTTPConfig.PublicURL = trim(c.HTTPConfig.PublicURL)
	u := &c.UpstreamConfig
	u.AuthURL, u.AuthInternalURL = trim(u.AuthURL), trim(u.AuthInternalURL)
	u.MetricsURL, u.GrafanaURL = trim(u.MetricsURL), trim(u.GrafanaURL)
	if strings.TrimSpace(u.HealthTargets) == "" && u.AuthInternalURL != "" {
		u.HealthTargets = "auth=" + u.AuthInternalURL + "/healthz,auth=" + u.AuthInternalURL + "/readyz"
	}
	if strings.TrimSpace(u.VersionTargets) == "" && u.AuthInternalURL != "" {
		u.VersionTargets = "auth=" + u.AuthInternalURL + "/metrics"
	}
	if strings.TrimSpace(u.TLSCheckHosts) == "" {
		u.TLSCheckHosts = strings.Join(u.CertTLSHosts(), ",")
	}
	d := &c.DockerConfig
	d.ProxyURL = trim(d.ProxyURL)
	b := &c.BosonConfig
	b.APIURL, b.CDNURL, b.DirectoryURL = trim(b.APIURL), trim(b.CDNURL), trim(b.DirectoryURL)
	b.APIInternalURL, b.APIInternalToken = trim(b.APIInternalURL), strings.TrimSpace(b.APIInternalToken)
	b.FilesInternalURL, b.FilesInternalToken = trim(b.FilesInternalURL), strings.TrimSpace(b.FilesInternalToken)
	b.DirectoryLocalURL, b.DirectoryInternalToken = trim(b.DirectoryLocalURL), strings.TrimSpace(b.DirectoryInternalToken)
	if b.DirectoryHostname == "" {
		b.DirectoryHostname = strings.TrimSpace(u.IRCCheckServerName)
	}
}

// IRCHostname is IRC_HOST, else IRC_CHECK_SERVER_NAME.
func (u UpstreamConfig) IRCHostname() string {
	if h := strings.TrimSpace(u.IRCHost); h != "" {
		return h
	}
	return strings.TrimSpace(u.IRCCheckServerName)
}

// CertTLSHosts turns CERT_HOSTS into TLS_CHECK_HOSTS entries: host:443
// (@TLS_EDGE_ADDR), the IRC host on IRC_CHECK's port. Hostnames that
// aren't plain DNS names are skipped.
func (u UpstreamConfig) CertTLSHosts() []string {
	ircPort := "6697"
	if t, err := u.IRC(); err == nil && t != nil && t.TLS {
		if _, p, err := net.SplitHostPort(t.Address); err == nil {
			ircPort = p
		}
	}
	irc := strings.ToLower(u.IRCHostname())
	edge := strings.TrimSpace(u.TLSEdgeAddr)
	var out []string
	seen := map[string]bool{}
	for _, h := range SplitList(u.CertHosts) {
		h = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), "."))
		if h == "" || seen[h] || strings.ContainsAny(h, ":@/*") {
			continue
		}
		seen[h] = true
		switch {
		case h == irc:
			out = append(out, h+":"+ircPort)
		case edge != "":
			out = append(out, h+":443@"+edge)
		default:
			out = append(out, h+":443")
		}
	}
	return out
}

// ProtectedServices parses DOCKER_PROTECTED_SERVICES.
func (c DockerConfig) ProtectedServices() []string { return SplitList(c.Protected) }

// Validate checks settings that would otherwise fail confusingly later.
func (c Config) Validate() error {
	if _, err := c.HTTPConfig.Origin(); err != nil {
		return err
	}
	if _, err := c.HTTPConfig.TrustedProxyPrefixes(); err != nil {
		return err
	}
	if _, err := c.UpstreamConfig.Health(); err != nil {
		return err
	}
	if _, err := c.UpstreamConfig.Versions(); err != nil {
		return err
	}
	if _, err := c.UpstreamConfig.TLSTargets(); err != nil {
		return err
	}
	if _, err := c.UpstreamConfig.IRC(); err != nil {
		return err
	}
	if _, err := c.UpstreamConfig.PublicProbes(); err != nil {
		return err
	}
	if _, err := c.StackConfig.Statuses(); err != nil {
		return err
	}
	for name, v := range map[string]string{"AUTH_URL": c.UpstreamConfig.AuthURL, "AUTH_INTERNAL_URL": c.UpstreamConfig.AuthInternalURL,
		"METRICS_URL": c.UpstreamConfig.MetricsURL, "GRAFANA_URL": c.UpstreamConfig.GrafanaURL,
		"DOCKER_PROXY_URL": c.DockerConfig.ProxyURL, "BOSON_API_URL": c.BosonConfig.APIURL,
		"BOSON_CDN_URL": c.BosonConfig.CDNURL, "DIRECTORY_API_URL": c.BosonConfig.DirectoryURL,
		"DIRECTORY_LOCAL_URL": c.BosonConfig.DirectoryLocalURL, "BOSON_API_INTERNAL_URL": c.BosonConfig.APIInternalURL,
		"BOSON_FILES_INTERNAL_URL": c.BosonConfig.FilesInternalURL} {
		if v == "" {
			continue
		}
		if err := checkHTTPURL(v); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if c.UpstreamConfig.AuthURL == "" {
		return errors.New("AUTH_URL is required")
	}
	if strings.HasPrefix(c.DockerConfig.ProxyURL, "unix:") || strings.Contains(c.DockerConfig.ProxyURL, "docker.sock") {
		return errors.New("DOCKER_PROXY_URL must be a socket proxy's http:// URL, never the Docker socket")
	}
	if c.BosonConfig.DirectoryRegistration && c.BosonConfig.DirectoryURL == "" {
		return errors.New("DIRECTORY_REGISTRATION needs DIRECTORY_API_URL")
	}
	if c.BosonConfig.DirectoryLocalURL != "" && c.BosonConfig.DirectoryInternalToken == "" {
		return errors.New("DIRECTORY_LOCAL_URL needs DIRECTORY_INTERNAL_TOKEN (the directory's operator token)")
	}
	if c.SessionConfig.MaxAge <= 0 {
		return errors.New("SESSION_MAX_AGE must be positive")
	}
	if c.SessionConfig.Key != "" {
		if _, err := c.SessionConfig.DecodeKey(); err != nil {
			return err
		}
	} else if c.AppConfig.Env != "development" {
		return errors.New("SESSION_KEY is required outside development (32 random bytes, base64: openssl rand -base64 32)")
	}
	return nil
}

func checkHTTPURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%q is not an http(s) URL", s)
	}
	return nil
}

// Origin is the scheme://host[:port] of PUBLIC_URL.
func (c HTTPConfig) Origin() (string, error) {
	u, err := url.Parse(c.PublicURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("PUBLIC_URL: %q is not an http(s) URL", c.PublicURL)
	}
	return u.Scheme + "://" + u.Host, nil
}

// DecodeKey parses SESSION_KEY: 32 bytes as base64 (any variant) or hex.
func (c SessionConfig) DecodeKey() ([]byte, error) {
	s := strings.TrimSpace(c.Key)
	if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			if len(b) != 32 {
				return nil, fmt.Errorf("SESSION_KEY must decode to 32 bytes, got %d", len(b))
			}
			return b, nil
		}
	}
	return nil, errors.New("SESSION_KEY must be 32 bytes, base64 or hex (openssl rand -base64 32)")
}

// TrustedProxyPrefixes parses TRUSTED_PROXIES.
func (c HTTPConfig) TrustedProxyPrefixes() ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, s := range SplitList(c.TrustedProxies) {
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("TRUSTED_PROXIES: %q is not an IP or CIDR", s)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// Target is a named URL from HEALTH_TARGETS / VERSION_TARGETS.
type Target struct {
	Name string
	URL  string
}

func parseTargets(env, raw string) ([]Target, error) {
	var out []Target
	for _, item := range SplitList(raw) {
		name, u, ok := strings.Cut(item, "=")
		name, u = strings.TrimSpace(name), strings.TrimSpace(u)
		if !ok || name == "" || u == "" {
			return nil, fmt.Errorf("%s: %q must be name=url", env, item)
		}
		if err := checkHTTPURL(u); err != nil {
			return nil, fmt.Errorf("%s: %s: %w", env, name, err)
		}
		out = append(out, Target{Name: name, URL: u})
	}
	return out, nil
}

// Health parses HEALTH_TARGETS.
func (c UpstreamConfig) Health() ([]Target, error) {
	return parseTargets("HEALTH_TARGETS", c.HealthTargets)
}

// Versions parses VERSION_TARGETS.
func (c UpstreamConfig) Versions() ([]Target, error) {
	return parseTargets("VERSION_TARGETS", c.VersionTargets)
}

// TLSTarget is a certificate to check: ServerName is sent as SNI and
// verified; Address is dialled (defaults to ServerName:port).
type TLSTarget struct {
	ServerName string
	Address    string
	Label      string
}

// TLSTargets parses TLS_CHECK_HOSTS (host:port[@dial-host:dial-port]).
func (c UpstreamConfig) TLSTargets() ([]TLSTarget, error) {
	out, err := ParseTLSTargets(SplitList(c.TLSCheckHosts))
	if err != nil {
		return nil, fmt.Errorf("TLS_CHECK_HOSTS: %w", err)
	}
	return out, nil
}

// ParseTLSTargets parses host:port[@dial-host:dial-port] entries.
func ParseTLSTargets(items []string) ([]TLSTarget, error) {
	var out []TLSTarget
	for _, item := range items {
		hostPort, dial, hasDial := strings.Cut(item, "@")
		host, port, err := splitHostPort(hostPort)
		if err != nil {
			return nil, err
		}
		addr := host + ":" + port
		if hasDial {
			dh, dp, err := splitHostPort(dial)
			if err != nil {
				return nil, err
			}
			addr = dh + ":" + dp
		}
		out = append(out, TLSTarget{ServerName: host, Address: addr, Label: host + ":" + port})
	}
	return out, nil
}

func splitHostPort(s string) (string, string, error) {
	i := strings.LastIndex(s, ":")
	if i <= 0 || i == len(s)-1 {
		return "", "", fmt.Errorf("%q must be host:port", s)
	}
	host, port := s[:i], s[i+1:]
	for _, r := range port {
		if r < '0' || r > '9' {
			return "", "", fmt.Errorf("%q: bad port", s)
		}
	}
	return strings.Trim(host, "[]"), port, nil
}

// PublicProbe is an endpoint the browser times (PUBLIC_PROBE_TARGETS).
type PublicProbe struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	// Kind is "http" (a no-cors GET) or "ws" (a WebSocket open).
	Kind string `json:"kind"`
	// Origin is scheme://host[:port], for the CSP.
	Origin string `json:"-"`
}

// PublicProbes parses PUBLIC_PROBE_TARGETS.
func (c UpstreamConfig) PublicProbes() ([]PublicProbe, error) {
	var out []PublicProbe
	for _, item := range SplitList(c.PublicProbeTargets) {
		name, raw, ok := strings.Cut(item, "=")
		name, raw = strings.TrimSpace(name), strings.TrimSpace(raw)
		if !ok || name == "" || raw == "" {
			return nil, fmt.Errorf("PUBLIC_PROBE_TARGETS: %q must be name=url", item)
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
			return nil, fmt.Errorf("PUBLIC_PROBE_TARGETS: %s: %q is not a URL", name, raw)
		}
		kind := "http"
		switch u.Scheme {
		case "https", "http":
		case "wss", "ws":
			kind = "ws"
		default:
			return nil, fmt.Errorf("PUBLIC_PROBE_TARGETS: %s: %q must be https://, http://, wss:// or ws://", name, raw)
		}
		out = append(out, PublicProbe{Name: name, URL: raw, Kind: kind, Origin: u.Scheme + "://" + u.Host})
	}
	return out, nil
}

// IRCTarget is the IRC server the overview probes.
type IRCTarget struct {
	Address    string // host:port to dial
	ServerName string // SNI
	TLS        bool
	Label      string // ircs://host:port as configured
}

// IRC parses IRC_CHECK: ircs://host[:6697] (TLS), irc://host[:6667]
// (plaintext) or a bare host:port (TLS). nil when unset.
func (c UpstreamConfig) IRC() (*IRCTarget, error) {
	raw := strings.TrimSpace(c.IRCCheck)
	if raw == "" {
		return nil, nil
	}
	useTLS, port, rest := true, "6697", raw
	switch {
	case strings.HasPrefix(raw, "ircs://"):
		rest = strings.TrimPrefix(raw, "ircs://")
	case strings.HasPrefix(raw, "irc://"):
		useTLS, port, rest = false, "6667", strings.TrimPrefix(raw, "irc://")
	case strings.Contains(raw, "://"):
		return nil, fmt.Errorf("IRC_CHECK: %q must be ircs://host:port or irc://host:port", raw)
	}
	rest = strings.TrimRight(rest, "/")
	if rest == "" || strings.ContainsAny(rest, "/?#@ ") {
		return nil, fmt.Errorf("IRC_CHECK: %q must be ircs://host:port or irc://host:port", raw)
	}
	host := strings.Trim(rest, "[]")
	if !strings.HasSuffix(rest, "]") && strings.Contains(rest, ":") {
		h, p, err := splitHostPort(rest)
		if err != nil {
			return nil, fmt.Errorf("IRC_CHECK: %w", err)
		}
		host, port = h, p
	}
	scheme := "ircs"
	if !useTLS {
		scheme = "irc"
	}
	sni := strings.TrimSpace(c.IRCCheckServerName)
	if sni == "" {
		sni = host
	}
	return &IRCTarget{
		Address: net.JoinHostPort(host, port), ServerName: sni, TLS: useTLS,
		Label: scheme + "://" + net.JoinHostPort(host, port),
	}, nil
}

// SplitList splits a comma/space/newline-separated env value.
func SplitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
}
