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
