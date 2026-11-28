package federation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Home is this server as a home: whether its boson.json publishes the
// federation block (federation spec §1), so its users can sign in on other
// Boson servers.
type Home struct {
	// Domain whose boson.json is checked; URL is what was fetched and
	// PublicURL the address other servers use (the page's test link).
	Domain    string `json:"domain,omitempty"`
	URL       string `json:"url,omitempty"`
	PublicURL string `json:"public_url,omitempty"`
	// State: published, missing (boson.json has no federation block),
	// off (accepts: off), mismatch (it doesn't match auth), unreachable,
	// unknown (no domain to check).
	State      string      `json:"state"`
	Federation *Discovery  `json:"federation,omitempty"`
	Checks     []HomeCheck `json:"checks"`
	Error      string      `json:"error,omitempty"`
	CheckedAt  time.Time   `json:"checked_at"`
}

// Discovery is boson.json's federation block.
type Discovery struct {
	Version           int    `json:"version"`
	Issuer            string `json:"issuer"`
	JWKSURI           string `json:"jwks_uri"`
	AssertionEndpoint string `json:"assertion_endpoint"`
	LoginEndpoint     string `json:"login_endpoint"`
	Accepts           string `json:"accepts"`
}

// HomeCheck is one line of the status: ok, or a problem in words.
type HomeCheck struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	OK     bool   `json:"ok"`
	Warn   bool   `json:"warn,omitempty"`
	Detail string `json:"detail,omitempty"`
}

const (
	HomePublished   = "published"
	HomeMissing     = "missing"
	HomeOff         = "off"
	HomeMismatch    = "mismatch"
	HomeUnreachable = "unreachable"
	HomeUnknown     = "unknown"
)

// HomeChecker fetches boson.json (and the JWKS it names), cached a minute.
type HomeChecker struct {
	// Domain is the stack's DOMAIN (BOSON_DOMAIN), used when auth doesn't
	// say which domain it is.
	Domain string
	// URL overrides where boson.json is fetched (WELLKNOWN_URL, e.g. the
	// wellknown container when the panel can't reach its own public name).
	URL string
	// WebHost is the stack's WEB_HOST: when set, boson.json should name
	// that web client (web.url), or other installs' voice, video and files
	// refuse its calls (directory-mode origins).
	WebHost string
	HTTP    *http.Client
	TTL     time.Duration
	Now     func() time.Time

	mu    sync.Mutex
	key   string
	cache *Home
}

func (c *HomeChecker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Check reports on domain's boson.json (domain "" = c.Domain), comparing it
// with auth's issuer and mode when known.
func (c *HomeChecker) Check(ctx context.Context, domain, issuer, mode string, fresh bool) *Home {
	if domain == "" {
		domain = c.Domain
	}
	key := domain + "|" + issuer + "|" + mode
	ttl := c.TTL
	if ttl == 0 {
		ttl = time.Minute
	}
	c.mu.Lock()
	if !fresh && c.cache != nil && c.key == key && c.now().Sub(c.cache.CheckedAt) < ttl {
		h := *c.cache
		c.mu.Unlock()
		return &h
	}
	c.mu.Unlock()
	h := c.check(ctx, domain, issuer, mode)
	c.mu.Lock()
	c.key, c.cache = key, h
	c.mu.Unlock()
	out := *h
	return &out
}

func (c *HomeChecker) get(ctx context.Context, url string, out any) error {
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("couldn't reach it: %v", unwrapURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("it answered HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(out); err != nil {
		return fmt.Errorf("it isn't valid JSON: %v", err)
	}
	return nil
}

func unwrapURLError(err error) error {
	type unwrapper interface{ Unwrap() error }
	if u, ok := err.(unwrapper); ok && u.Unwrap() != nil {
		return u.Unwrap()
	}
	return err
}

func (c *HomeChecker) check(ctx context.Context, domain, issuer, mode string) *Home {
	h := &Home{Domain: domain, CheckedAt: c.now().UTC(), Checks: []HomeCheck{}}
	if domain != "" {
		h.PublicURL = "https://" + domain + "/.well-known/boson.json"
	}
	h.URL = c.URL
	if h.URL == "" {
		h.URL = h.PublicURL
	}
	if h.URL == "" {
		h.State = HomeUnknown
		h.Error = "the panel doesn't know this server's domain: set BOSON_DOMAIN (the stack's DOMAIN) on the admin service"
		return h
	}
	add := func(id, label string, ok bool, detail string) {
		h.Checks = append(h.Checks, HomeCheck{ID: id, Label: label, OK: ok, Detail: detail})
	}
	var doc struct {
		Federation *Discovery `json:"federation"`
		Web        *struct {
			URL string `json:"url"`
		} `json:"web"`
	}
	if err := c.get(ctx, h.URL, &doc); err != nil {
		h.State, h.Error = HomeUnreachable, "boson.json: "+err.Error()
		add("discovery", "boson.json is served", false, err.Error())
		return h
	}
	add("discovery", "boson.json is served", true, "")
	if c.WebHost != "" {
		want := "https://" + c.WebHost
		hc := HomeCheck{ID: "web", Label: "It names this server’s web client", OK: doc.Web != nil && strings.TrimRight(doc.Web.URL, "/") == want}
		if !hc.OK {
			hc.Warn = true
			got := "no web section"
			if doc.Web != nil {
				got = fmt.Sprintf("web.url %q", doc.Web.URL)
			}
			hc.Detail = fmt.Sprintf("boson.json has %s, not %s: other servers’ voice, video and files refuse calls from your web client. Add web to WELLKNOWN_SECTIONS in .env (or remove the line) and restart the wellknown service.", got, want)
		}
		h.Checks = append(h.Checks, hc)
	}
	h.Federation = doc.Federation
	f := doc.Federation
	if f == nil {
		h.State = HomeMissing
		add("block", "It has a federation block", false,
			"Other servers can’t verify your users. Update the stack: its wellknown service adds the block when auth supports federation.")
		return h
	}
	add("block", "It has a federation block", f.Version == 1, versionDetail(f.Version))
	state := HomePublished
	if issuer != "" {
		ok := strings.TrimRight(f.Issuer, "/") == strings.TrimRight(issuer, "/")
		detail := ""
		if !ok {
			detail = fmt.Sprintf("boson.json says %q, auth signs as %q: other servers will refuse your users", f.Issuer, issuer)
			state = HomeMismatch
		}
		add("issuer", "Its issuer is this auth service", ok, detail)
	}
	if f.JWKSURI == "" {
		add("jwks", "Signing keys are published", false, "jwks_uri is missing")
		state = HomeMismatch
	} else if !strings.HasPrefix(f.JWKSURI, "https://") && c.URL == "" {
		add("jwks", "Signing keys are published", false, "jwks_uri must be https://")
		state = HomeMismatch
	} else {
		var jwks struct {
			Keys []json.RawMessage `json:"keys"`
		}
		if err := c.get(ctx, f.JWKSURI, &jwks); err != nil {
			add("jwks", "Signing keys are published", false, err.Error())
			state = HomeMismatch
		} else if len(jwks.Keys) == 0 {
			add("jwks", "Signing keys are published", false, "the key set is empty")
			state = HomeMismatch
		} else {
			add("jwks", "Signing keys are published", true, fmt.Sprintf("%d key(s)", len(jwks.Keys)))
		}
	}
	if f.AssertionEndpoint == "" || f.LoginEndpoint == "" {
		add("endpoints", "Assertion and login endpoints are listed", false, "assertion_endpoint or login_endpoint is missing")
		state = HomeMismatch
	} else {
		add("endpoints", "Assertion and login endpoints are listed", true, "")
	}
	if mode != "" {
		ok := f.Accepts == mode
		hc := HomeCheck{ID: "accepts", Label: "It advertises this server’s mode", OK: ok}
		if !ok {
			hc.Warn = true
			hc.Detail = fmt.Sprintf("boson.json says accepts: %q but the mode is %q. Apps read this to decide whether to offer “Continue as…”. Restart the wellknown service to republish it.", f.Accepts, mode)
		}
		h.Checks = append(h.Checks, hc)
	}
	if f.Accepts == "off" && state == HomePublished {
		state = HomeOff
	}
	h.State = state
	return h
}

func versionDetail(v int) string {
	if v == 1 {
		return ""
	}
	return fmt.Sprintf("version %d: this panel knows version 1", v)
}
