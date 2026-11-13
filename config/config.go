// Package config holds the panel configuration, loaded from env vars.
package config

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
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
	TelemetryConfig TelemetryConfig
}

type AppConfig struct {
	APPName  string `default:"admin"`
	Env      string `default:"development" env:"ENV"`
	Version  string `default:"dev" env:"VERSION"`
	LogLevel string `default:"info" env:"LOG_LEVEL"`
}

type HTTPConfig struct {
	// Public listener: the UI and /api (behind Caddy).
	Addr string `default:":8080" env:"HTTP_ADDR"`
	// Internal listener: /metrics, /healthz. Never publish it. Empty disables it.
	InternalAddr string `default:":8081" env:"INTERNAL_ADDR"`
	// Externally visible URL of the panel, e.g. https://admin.example.com.
	// State-changing requests must come from this origin.
	PublicURL string `default:"http://localhost:8080" env:"PUBLIC_URL"`
	// Comma-separated CIDRs/IPs whose X-Forwarded-For is trusted (the Caddy container network).
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
	// Comma-separated host:port[@dial-host:dial-port] whose TLS certificates are checked.
	TLSCheckHosts string `env:"TLS_CHECK_HOSTS"`
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
}

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
	for name, v := range map[string]string{"AUTH_URL": c.UpstreamConfig.AuthURL, "AUTH_INTERNAL_URL": c.UpstreamConfig.AuthInternalURL,
		"METRICS_URL": c.UpstreamConfig.MetricsURL, "GRAFANA_URL": c.UpstreamConfig.GrafanaURL} {
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
	var out []TLSTarget
	for _, item := range SplitList(c.TLSCheckHosts) {
		hostPort, dial, hasDial := strings.Cut(item, "@")
		host, port, err := splitHostPort(hostPort)
		if err != nil {
			return nil, fmt.Errorf("TLS_CHECK_HOSTS: %w", err)
		}
		addr := host + ":" + port
		if hasDial {
			dh, dp, err := splitHostPort(dial)
			if err != nil {
				return nil, fmt.Errorf("TLS_CHECK_HOSTS: %w", err)
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

// SplitList splits a comma/space/newline-separated env value.
func SplitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
}
