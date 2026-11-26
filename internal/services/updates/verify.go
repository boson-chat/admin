package updates

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/boson-chat/admin/internal/clients/health"
	"github.com/boson-chat/admin/internal/clients/ircprobe"
)

// VerifyInput is what the checks need to know.
type VerifyInput struct {
	// Services are the compose services that have a container: only those are checked.
	Services map[string]bool
	// Token is the applying admin's access token (may be empty or expired).
	Token string
}

// Verifier runs the post-update checks.
type Verifier interface {
	Verify(ctx context.Context, in VerifyInput) []Check
}

// HTTPCheck is a GET that must answer 2xx (and contain Contains, when set).
type HTTPCheck struct {
	ID, Label, Service, URL, Contains string
}

// StackVerifier checks the stack the way its users use it: IRC connects
// over TLS and registers, auth issues a token, the api, voice, video,
// files and directory answer, the web client's page loads.
type StackVerifier struct {
	Health health.HealthCheckerImpl
	HTTP   *http.Client
	// IRC: address (host:port), TLS server name, TLS; empty Addr = no IRC check.
	IRC       ircprobe.IRCProberImpl
	IRCAddr   string
	IRCServer string
	IRCTLS    bool
	// AuthURL is auth's public API on the compose network (http://auth:8080).
	AuthURL string
	Checks  []HTTPCheck
}

// DefaultHTTPChecks are the stack's internal endpoints.
func DefaultHTTPChecks() []HTTPCheck {
	return []HTTPCheck{
		{ID: "auth", Label: "Auth is ready", Service: "auth", URL: "http://auth:8081/readyz"},
		{ID: "api", Label: "API answers", Service: "api", URL: "http://api:3000/health"},
		{ID: "web", Label: "Web client loads", Service: "web", URL: "http://web:8080/", Contains: "<"},
		{ID: "web-config", Label: "Web client config", Service: "web", URL: "http://web:8080/boson-config.js"},
		{ID: "voice", Label: "Voice answers", Service: "quicvoiced", URL: "http://quicvoiced:8080/healthz"},
		{ID: "video", Label: "Video answers", Service: "quicvideod", URL: "http://quicvideod:8080/healthz"},
		{ID: "files", Label: "Files answers", Service: "files", URL: "http://files:3001/readyz"},
		{ID: "directory", Label: "Directory answers", Service: "directory", URL: "http://directory:8081/healthz"},
		{ID: "webircgateway", Label: "Web IRC gateway answers", Service: "webircgateway", URL: "http://webircgateway:8080/webirc/info"},
	}
}

func (v *StackVerifier) client() *http.Client {
	if v.HTTP != nil {
		return v.HTTP
	}
	return &http.Client{Timeout: 5 * time.Second}
}

func (v *StackVerifier) Verify(ctx context.Context, in VerifyInput) []Check {
	var mu sync.Mutex
	var out []Check
	add := func(c Check) {
		mu.Lock()
		out = append(out, c)
		mu.Unlock()
	}
	var wg sync.WaitGroup
	if v.IRC != nil && v.IRCAddr != "" && in.Services["ergo"] {
		wg.Go(func() {
			r := v.IRC.Probe(ctx, v.IRCAddr, v.IRCServer, v.IRCTLS)
			d := r.Detail
			if r.OK {
				d = "connected over TLS in " + r.Latency.Round(time.Millisecond).String()
				if !v.IRCTLS {
					d = "connected in " + r.Latency.Round(time.Millisecond).String()
				}
				if r.Version != "" {
					d += ", registered (" + r.Version + ")"
				}
			}
			add(Check{ID: "irc", Label: "IRC connects and registers", OK: r.OK, Detail: d})
		})
	}
	if v.AuthURL != "" && in.Services["auth"] {
		wg.Go(func() { add(v.authToken(ctx, in.Token)) })
	}
	for _, hc := range v.Checks {
		if !in.Services[hc.Service] {
			continue
		}
		wg.Go(func() { add(v.get(ctx, hc)) })
	}
	wg.Wait()
	order := map[string]int{"irc": 0, "auth-token": 1}
	for i, hc := range v.Checks {
		order[hc.ID] = i + 2
	}
	sortChecks(out, order)
	return out
}

func sortChecks(cs []Check, order map[string]int) {
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0 && order[cs[j].ID] < order[cs[j-1].ID]; j-- {
			cs[j], cs[j-1] = cs[j-1], cs[j]
		}
	}
}

func (v *StackVerifier) get(ctx context.Context, hc HTTPCheck) Check {
	c := Check{ID: hc.ID, Label: hc.Label}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hc.URL, nil)
	if err != nil {
		c.Detail = err.Error()
		return c
	}
	start := time.Now()
	resp, err := v.client().Do(req)
	if err != nil {
		c.Detail = shortErr(err)
		return c
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	switch {
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		c.Detail = "answered " + strconv.Itoa(resp.StatusCode)
	case hc.Contains != "" && !bytes.Contains(body, []byte(hc.Contains)):
		c.Detail = "answered without the expected content"
	default:
		c.OK = true
		c.Detail = "answered in " + time.Since(start).Round(time.Millisecond).String()
	}
	return c
}

// authToken: auth issues a token. With the applying admin's access token it
// asks for a voice token (auth signs a fresh JWT); without one, or when
// that token has expired, it checks that auth serves its signing keys.
func (v *StackVerifier) authToken(ctx context.Context, token string) Check {
	c := Check{ID: "auth-token", Label: "Auth issues a token"}
	if token != "" {
		st, body, err := v.do(ctx, http.MethodPost, v.AuthURL+"/v1/voice/token", token, []byte(`{"rooms":[]}`))
		if err == nil && st == http.StatusOK {
			var t struct {
				Token string `json:"token"`
			}
			if json.Unmarshal(body, &t) == nil && strings.Count(t.Token, ".") == 2 {
				c.OK, c.Detail = true, "issued a signed token"
				return c
			}
		}
		if err == nil && (st == http.StatusNotFound || st == http.StatusNotImplemented || st == http.StatusServiceUnavailable) {
			if st2, _, err := v.do(ctx, http.MethodGet, v.AuthURL+"/v1/me", token, nil); err == nil && st2 == http.StatusOK {
				c.OK, c.Detail = true, "accepted the panel's token (no voice tokens on this auth)"
				return c
			}
		}
		if err != nil {
			c.Detail = shortErr(err)
			return c
		}
		if st != http.StatusUnauthorized {
			c.Detail = "token request answered " + strconv.Itoa(st)
			return c
		}
	}
	st, body, err := v.do(ctx, http.MethodGet, v.AuthURL+"/.well-known/jwks.json", "", nil)
	if err != nil {
		c.Detail = shortErr(err)
		return c
	}
	var jwks struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if st != http.StatusOK || json.Unmarshal(body, &jwks) != nil || len(jwks.Keys) == 0 {
		c.Detail = "no signing keys (" + strconv.Itoa(st) + ")"
		return c
	}
	c.OK, c.Detail = true, "serves "+strconv.Itoa(len(jwks.Keys))+" signing key(s); token issue not tried (no fresh admin token)"
	return c
}

func (v *StackVerifier) do(ctx context.Context, method, url, token string, body []byte) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := v.client().Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	return resp.StatusCode, b, nil
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && len(s) > 80 {
		s = s[i+2:]
	}
	return s
}
