package config

import (
	"strings"
	"testing"
)

func valid() Config {
	c := Config{
		AppConfig:      AppConfig{Env: "production"},
		HTTPConfig:     HTTPConfig{PublicURL: "https://admin.example.com/"},
		SessionConfig:  SessionConfig{Key: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", MaxAge: 1},
		UpstreamConfig: UpstreamConfig{AuthURL: "http://auth:8080", AuthInternalURL: "http://auth:8081/"},
	}
	c.ApplyDefaults()
	return c
}

func TestDefaultsFromAuthInternalURL(t *testing.T) {
	c := valid()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	h, _ := c.UpstreamConfig.Health()
	if len(h) != 2 || h[1].URL != "http://auth:8081/readyz" || h[0].Name != "auth" {
		t.Fatalf("health: %+v", h)
	}
	v, _ := c.UpstreamConfig.Versions()
	if len(v) != 1 || v[0].URL != "http://auth:8081/metrics" {
		t.Fatalf("versions: %+v", v)
	}
	if o, _ := c.HTTPConfig.Origin(); o != "https://admin.example.com" {
		t.Fatalf("origin %q", o)
	}
}

func TestTLSTargets(t *testing.T) {
	u := UpstreamConfig{TLSCheckHosts: "irc.example.com:6697, voice.example.com:443@caddy:443\n[::1]:8443"}
	got, err := u.TLSTargets()
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Address != "irc.example.com:6697" || got[1].Address != "caddy:443" || got[1].ServerName != "voice.example.com" || got[2].ServerName != "::1" {
		t.Fatalf("%+v", got)
	}
	for _, bad := range []string{"irc.example.com", "host:", "host:abc", "a:1@b"} {
		if _, err := (UpstreamConfig{TLSCheckHosts: bad}).TLSTargets(); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestValidation(t *testing.T) {
	cases := map[string]func(*Config){
		"SESSION_KEY is required": func(c *Config) { c.SessionConfig.Key = "" },
		"32 bytes":                func(c *Config) { c.SessionConfig.Key = "c2hvcnQ=" },
		"HEALTH_TARGETS":          func(c *Config) { c.UpstreamConfig.HealthTargets = "auth" },
		"PUBLIC_URL":              func(c *Config) { c.HTTPConfig.PublicURL = "admin.example.com" },
		"TRUSTED_PROXIES":         func(c *Config) { c.HTTPConfig.TrustedProxies = "caddy" },
		"METRICS_URL":             func(c *Config) { c.UpstreamConfig.MetricsURL = "victoriametrics:8428" },
	}
	for want, mutate := range cases {
		c := valid()
		mutate(&c)
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
	c := valid()
	c.AppConfig.Env, c.SessionConfig.Key = "development", ""
	if err := c.Validate(); err != nil {
		t.Fatalf("dev without SESSION_KEY should be allowed: %v", err)
	}
	c.SessionConfig.Key = strings.Repeat("ab", 32) // hex
	if _, err := c.SessionConfig.DecodeKey(); err != nil {
		t.Fatal(err)
	}
}

func TestIRCTarget(t *testing.T) {
	cases := []struct {
		in, sni, addr, serverName, label string
		tls                              bool
	}{
		{"ircs://ergo:6697", "irc.example.com", "ergo:6697", "irc.example.com", "ircs://ergo:6697", true},
		{"ircs://irc.example.com", "", "irc.example.com:6697", "irc.example.com", "ircs://irc.example.com:6697", true},
		{"irc://ergo", "", "ergo:6667", "ergo", "irc://ergo:6667", false},
		{"ergo:7000", "", "ergo:7000", "ergo", "ircs://ergo:7000", true},
		{"ircs://[::1]:6697/", "", "[::1]:6697", "::1", "ircs://[::1]:6697", true},
		{"ircs://[::1]", "", "[::1]:6697", "::1", "ircs://[::1]:6697", true},
	}
	for _, c := range cases {
		got, err := UpstreamConfig{IRCCheck: c.in, IRCCheckServerName: c.sni}.IRC()
		if err != nil {
			t.Errorf("%s: %v", c.in, err)
			continue
		}
		if got.Address != c.addr || got.ServerName != c.serverName || got.TLS != c.tls || got.Label != c.label {
			t.Errorf("%s: %+v", c.in, got)
		}
	}
	if got, err := (UpstreamConfig{}).IRC(); got != nil || err != nil {
		t.Fatalf("empty: %+v %v", got, err)
	}
	for _, bad := range []string{"http://ergo:6697", "ircs://", "ergo:", "ergo:abc", "ircs://ergo:6697/#chan", "ircs://user@ergo"} {
		if _, err := (UpstreamConfig{IRCCheck: bad}).IRC(); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	c := valid()
	c.UpstreamConfig.IRCCheck = "http://ergo"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "IRC_CHECK") {
		t.Fatalf("validate: %v", err)
	}
}

func TestPublicProbes(t *testing.T) {
	got, err := UpstreamConfig{PublicProbeTargets: "auth=https://auth.example.com/healthz, irc=wss://irc.example.com/webirc,dev=http://localhost:8080/healthz"}.PublicProbes()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Kind != "http" || got[0].Origin != "https://auth.example.com" ||
		got[1].Kind != "ws" || got[1].Origin != "wss://irc.example.com" || got[2].Origin != "http://localhost:8080" {
		t.Fatalf("%+v", got)
	}
	for _, bad := range []string{"auth", "auth=ftp://x/y", "auth=https://", "auth=https://u:p@x/", "auth=/healthz"} {
		if _, err := (UpstreamConfig{PublicProbeTargets: bad}).PublicProbes(); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestDockerAndBoson(t *testing.T) {
	c := valid()
	c.UpstreamConfig.IRCCheckServerName = "irc.example.org"
	c.DockerConfig = DockerConfig{ProxyURL: "http://docker-proxy:2375/", Protected: "admin, traefik"}
	c.BosonConfig.DirectoryURL = "https://api.boson.chat/"
	c.ApplyDefaults()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.DockerConfig.ProxyURL != "http://docker-proxy:2375" || c.BosonConfig.DirectoryHostname != "irc.example.org" {
		t.Fatalf("defaults: %+v %+v", c.DockerConfig, c.BosonConfig)
	}
	if p := c.DockerConfig.ProtectedServices(); len(p) != 2 || p[1] != "traefik" {
		t.Fatalf("protected: %v", p)
	}
	for _, bad := range []string{"unix:///var/run/docker.sock", "http://localhost/var/run/docker.sock", "tcp://docker:2375"} {
		c := valid()
		c.DockerConfig.ProxyURL = bad
		if err := c.Validate(); err == nil {
			t.Fatalf("accepted DOCKER_PROXY_URL=%s", bad)
		}
	}
	c = valid()
	c.BosonConfig.DirectoryRegistration = true
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "DIRECTORY_API_URL") {
		t.Fatalf("registration without a URL: %v", err)
	}
}

func TestTLSHostsFromCertHosts(t *testing.T) {
	c := Config{UpstreamConfig: UpstreamConfig{
		CertHosts:   "irc.example.com, voice.example.com,auth.example.com auth.example.com,*.bad.example,FILES.example.com.",
		TLSEdgeAddr: "traefik:443", IRCHost: "irc.example.com", IRCCheck: "ircs://ergo:6697",
	}}
	c.ApplyDefaults()
	want := "irc.example.com:6697,voice.example.com:443@traefik:443,auth.example.com:443@traefik:443,files.example.com:443@traefik:443"
	if c.UpstreamConfig.TLSCheckHosts != want {
		t.Fatalf("got %q", c.UpstreamConfig.TLSCheckHosts)
	}
	if _, err := c.UpstreamConfig.TLSTargets(); err != nil {
		t.Fatal(err)
	}
	// TLS_CHECK_HOSTS wins; the IRC host falls back to IRC_CHECK_SERVER_NAME.
	c = Config{UpstreamConfig: UpstreamConfig{CertHosts: "a.example", TLSCheckHosts: "b.example:443"}}
	c.ApplyDefaults()
	if c.UpstreamConfig.TLSCheckHosts != "b.example:443" {
		t.Fatalf("got %q", c.UpstreamConfig.TLSCheckHosts)
	}
	u := UpstreamConfig{CertHosts: "irc.example.com,a.example", IRCCheckServerName: "irc.example.com"}
	if got := strings.Join(u.CertTLSHosts(), ","); got != "irc.example.com:6697,a.example:443" {
		t.Fatalf("got %q", got)
	}
}
