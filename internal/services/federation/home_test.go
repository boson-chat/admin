package federation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type wk struct {
	fed     map[string]any // nil = no federation block
	keys    string
	status  int
	fetches atomic.Int32
}

func (s *wk) serve(t *testing.T) *httptest.Server {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/boson.json":
			s.fetches.Add(1)
			if s.status != 0 {
				w.WriteHeader(s.status)
				return
			}
			doc := map[string]any{"version": 1}
			if s.fed != nil {
				f := map[string]any{"jwks_uri": srv.URL + "/jwks.json"}
				for k, v := range s.fed {
					f[k] = v
				}
				doc["federation"] = f
			}
			_ = json.NewEncoder(w).Encode(doc)
		case "/jwks.json":
			_, _ = w.Write([]byte(s.keys))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func good() map[string]any {
	return map[string]any{"version": 1, "issuer": "https://auth.example.org", "accepts": "directory",
		"assertion_endpoint": "https://auth.example.org/v1/federation/assertions", "login_endpoint": "https://auth.example.org/v1/federation/login"}
}

func checkState(t *testing.T, h *Home, want string) {
	t.Helper()
	if h.State != want {
		t.Fatalf("state %q, want %q: %+v", h.State, want, h)
	}
}

func failing(h *Home) []string {
	var out []string
	for _, c := range h.Checks {
		if !c.OK {
			out = append(out, c.ID)
		}
	}
	return out
}

func TestHomePublished(t *testing.T) {
	s := &wk{fed: good(), keys: `{"keys":[{"kid":"a"}]}`}
	srv := s.serve(t)
	c := &HomeChecker{Domain: "example.org", URL: srv.URL + "/.well-known/boson.json"}
	h := c.Check(context.Background(), "", "https://auth.example.org/", "directory", false)
	checkState(t, h, HomePublished)
	if h.PublicURL != "https://example.org/.well-known/boson.json" || len(failing(h)) != 0 || h.Federation.Accepts != "directory" {
		t.Fatalf("%+v", h)
	}
	// Cached for a minute, unless fresh.
	c.Check(context.Background(), "", "https://auth.example.org/", "directory", false)
	if s.fetches.Load() != 1 {
		t.Fatalf("fetched %d times", s.fetches.Load())
	}
	c.Check(context.Background(), "", "https://auth.example.org/", "directory", true)
	if s.fetches.Load() != 2 {
		t.Fatalf("fresh: fetched %d times", s.fetches.Load())
	}
}

func TestHomeProblems(t *testing.T) {
	ctx := context.Background()
	check := func(s *wk, issuer, mode string) *Home {
		srv := s.serve(t)
		return (&HomeChecker{URL: srv.URL + "/.well-known/boson.json", TTL: time.Nanosecond}).Check(ctx, "example.org", issuer, mode, true)
	}

	checkState(t, check(&wk{}, "", ""), HomeMissing)
	checkState(t, check(&wk{status: 502}, "", ""), HomeUnreachable)

	f := good()
	f["issuer"] = "https://login.example.org"
	h := check(&wk{fed: f, keys: `{"keys":[{}]}`}, "https://auth.example.org", "directory")
	checkState(t, h, HomeMismatch)
	if got := failing(h); len(got) != 1 || got[0] != "issuer" || !strings.Contains(h.Checks[2].Detail, "refuse") {
		t.Fatalf("issuer: %v %+v", got, h.Checks)
	}

	h = check(&wk{fed: good(), keys: `{"keys":[]}`}, "", "")
	checkState(t, h, HomeMismatch)
	if got := failing(h); len(got) != 1 || got[0] != "jwks" {
		t.Fatalf("empty jwks: %v", got)
	}

	// A mode boson.json doesn't advertise is a warning, not a broken home.
	h = check(&wk{fed: good(), keys: `{"keys":[{}]}`}, "", "open")
	checkState(t, h, HomePublished)
	if got := failing(h); len(got) != 1 || got[0] != "accepts" || !h.Checks[len(h.Checks)-1].Warn {
		t.Fatalf("accepts: %v", h.Checks)
	}

	f = good()
	f["accepts"] = "off"
	checkState(t, check(&wk{fed: f, keys: `{"keys":[{}]}`}, "", "off"), HomeOff)

	f = good()
	delete(f, "login_endpoint")
	checkState(t, check(&wk{fed: f, keys: `{"keys":[{}]}`}, "", ""), HomeMismatch)

	h = (&HomeChecker{}).Check(ctx, "", "", "", true)
	checkState(t, h, HomeUnknown)
	if !strings.Contains(h.Error, "BOSON_DOMAIN") {
		t.Fatalf("unknown: %+v", h)
	}
}
