package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/clients/bosonapi"
	"github.com/boson-chat/admin/internal/services/admin"
	"github.com/boson-chat/admin/internal/services/nickclaim"
	"github.com/boson-chat/admin/internal/services/stackconfig"
)

// fakeNickAPI is the api's internal listener.
type fakeNickAPI struct {
	srv    *httptest.Server
	status string
	tests  []map[string]any
}

func newFakeNickAPI(t *testing.T) *fakeNickAPI {
	f := &fakeNickAPI{status: `{"enabled":false,"email_domain":"","inbound":{"host":"","port":995,"tls":true,"user":""},"worker":{"running":false,"last_drain_at":null,"last_ok_at":null,"last_error":"","consecutive_failures":0},"pending_claims":0}`}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Token") != "tok" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /internal/nick-claims/status":
			_, _ = w.Write([]byte(f.status))
		case "POST /internal/nick-claims/test":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			f.tests = append(f.tests, b)
			_, _ = w.Write([]byte(`{"ok":false,"message":"sign-in failed: -ERR invalid login","messages":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// withNickClaims wires the Nick registration service to f (nil: no
// BOSON_API_INTERNAL_URL) and its check into the config file.
func (h *harness) withNickClaims(f *fakeNickAPI) {
	var c bosonapi.NickClaimClientImpl
	if f != nil {
		c = bosonapi.NewNickClaimClient(f.srv.URL, "tok", time.Second, nil)
	}
	svc := nickclaim.New(nickclaim.Options{Client: c})
	h.h.NickClaims = svc
	h.h.Config.AddCheck("nick_claim.", svc.Check)
}

func TestNickClaimStatusAndTest(t *testing.T) {
	h := newHarness(t)
	h.withConfigFile("version: 1\n")
	f := newFakeNickAPI(t)
	h.withNickClaims(f)
	c, csrf := h.login()

	w := h.do("GET", "/api/nick-claims", nil, withCookie(c))
	var v nickclaim.View
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != 200 || !v.Configured || v.Status == nil || v.PasswordEnv != "NICK_CLAIM_POP3_PASSWORD" {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}

	body := map[string]any{"inbound": map[string]any{"host": "pop.example.com", "port": 995, "tls": true, "user": "reg@example.com", "password": "hunter2"}}
	if w := h.do("POST", "/api/nick-claims/test", body, withCookie(c)); w.Code != http.StatusForbidden {
		t.Fatalf("no CSRF: %d", w.Code)
	}
	w = h.do("POST", "/api/nick-claims/test", body, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":false`) || !strings.Contains(w.Body.String(), "invalid login") {
		t.Fatalf("test: %d %s", w.Code, w.Body)
	}
	if len(f.tests) != 1 || f.tests[0]["inbound"].(map[string]any)["password"] != "hunter2" {
		t.Fatalf("api got %v", f.tests)
	}
	bad := map[string]any{"inbound": map[string]any{"port": 0}}
	w = h.do("POST", "/api/nick-claims/test", bad, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != 400 || errField(w.Body.String()) != "nick_claim.inbound.port" {
		t.Fatalf("bad port: %d %s", w.Code, w.Body)
	}
}

func errField(body string) string {
	var e struct {
		Field string `json:"field"`
	}
	_ = json.Unmarshal([]byte(body), &e)
	return e.Field
}

func TestNickClaimOff(t *testing.T) {
	h := newHarness(t)
	h.withConfigFile("version: 1\n")
	h.withNickClaims(nil)
	c, csrf := h.login()
	w := h.do("GET", "/api/nick-claims", nil, withCookie(c))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"configured":false`) || !strings.Contains(w.Body.String(), "isn't reachable from the admin panel") {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}
	w = h.do("POST", "/api/nick-claims/test", map[string]any{}, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != 503 || errCode(t, w.Body.String()) != "nick_claim_api_off" {
		t.Fatalf("test: %d %s", w.Code, w.Body)
	}
}

func TestNickClaimSaveValidation(t *testing.T) {
	h := newHarness(t)
	path := h.withConfigFile("version: 1\n")
	f := newFakeNickAPI(t)
	h.withNickClaims(f)
	c, csrf := h.login()
	patch := func(body map[string]any) (int, string) {
		w := h.do("PATCH", "/api/config", body, withCookie(c), withHeader(CSRFHeader, csrf))
		return w.Code, w.Body.String()
	}

	// Enabling without a domain or a host (none in the api's env either).
	code, body := patch(map[string]any{"set": map[string]any{stackconfig.KeyNickClaimEnabled: true}})
	if code != 400 || errField(body) != stackconfig.KeyNickClaimEmailDomain || !strings.Contains(body, "mail server") {
		t.Fatalf("enable alone: %d %s", code, body)
	}
	code, body = patch(map[string]any{"set": map[string]any{stackconfig.KeyNickClaimEnabled: true, stackconfig.KeyNickClaimEmailDomain: "nick.example.com"}})
	if code != 400 || errField(body) != stackconfig.KeyNickClaimHost {
		t.Fatalf("no host: %d %s", code, body)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "nick_claim") {
		t.Fatalf("a refused change was written:\n%s", b)
	}
	// Field checks.
	for key, v := range map[string]any{
		stackconfig.KeyNickClaimEmailDomain: "not a domain",
		stackconfig.KeyNickClaimRateLimit:   0,
		stackconfig.KeyNickClaimPort:        70000,
		stackconfig.KeyNickClaimProtocol:    "imap",
		stackconfig.KeyNickClaimHost:        "bad host!",
	} {
		if code, body := patch(map[string]any{"set": map[string]any{key: v}}); code != 400 || !strings.Contains(body, key) {
			t.Errorf("%s=%v: %d %s", key, v, code, body)
		}
	}

	all := map[string]any{
		stackconfig.KeyNickClaimEnabled:     true,
		stackconfig.KeyNickClaimEmailDomain: "Nick.Example.com",
		stackconfig.KeyNickClaimRateLimit:   5,
		stackconfig.KeyNickClaimProtocol:    "pop3",
		stackconfig.KeyNickClaimHost:        "pop.example.com",
		stackconfig.KeyNickClaimPort:        995,
		stackconfig.KeyNickClaimTLS:         true,
		stackconfig.KeyNickClaimUser:        "reg@nick.example.com",
	}
	if code, body := patch(map[string]any{"set": all}); code != 200 {
		t.Fatalf("save: %d %s", code, body)
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{"nick_claim:", "enabled: true", "email_domain: nick.example.com", "inbound:", "host: pop.example.com", "user: reg@nick.example.com", "by root at"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("file lacks %q:\n%s", want, b)
		}
	}
	// Removing the host while it's on is refused; switching off is fine.
	if code, body := patch(map[string]any{"unset": []string{stackconfig.KeyNickClaimHost}}); code != 400 || errField(body) != stackconfig.KeyNickClaimHost {
		t.Fatalf("unset host while on: %d %s", code, body)
	}
	if code, body := patch(map[string]any{"set": map[string]any{stackconfig.KeyNickClaimEnabled: false}, "unset": []string{stackconfig.KeyNickClaimHost}}); code != 200 {
		t.Fatalf("off + unset: %d %s", code, body)
	}
	// Other sections aren't held up by the nick_claim check.
	if code, body := patch(map[string]any{"set": map[string]any{"network.name": "Example"}}); code != 200 {
		t.Fatalf("other key: %d %s", code, body)
	}

	// With the domain and host in the api's env, enabling alone works.
	h2 := newHarness(t)
	h2.withConfigFile("version: 1\n")
	f2 := newFakeNickAPI(t)
	f2.status = `{"enabled":false,"email_domain":"env.example.com","inbound":{"host":"pop.env.example.com","port":995,"tls":true,"user":"reg@env.example.com"},"worker":{"running":false},"pending_claims":0}`
	h2.withNickClaims(f2)
	c2, csrf2 := h2.login()
	w := h2.do("PATCH", "/api/config", map[string]any{"set": map[string]any{stackconfig.KeyNickClaimEnabled: true}}, withCookie(c2), withHeader(CSRFHeader, csrf2))
	if w.Code != 200 {
		t.Fatalf("enable with env defaults: %d %s", w.Code, w.Body)
	}
}

// The api's config status lists nick_claim among what it reads.
func TestNickClaimKeysReadByAPI(t *testing.T) {
	for _, k := range stackconfig.Keys {
		if strings.HasPrefix(k.Key, "nick_claim.") && (k.Perm != admin.PermSettingsManage || len(k.Services) != 1 || k.Services[0] != "api") {
			t.Errorf("%s: %+v", k.Key, k)
		}
	}
}

// Without settings.manage the page's endpoints and keys are refused.
func TestNickClaimPermissions(t *testing.T) {
	h := newHarness(t)
	h.withConfigFile("version: 1\n")
	h.withNickClaims(newFakeNickAPI(t))
	c, csrf := h.moderator()
	for _, rq := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/nick-claims", nil},
		{"POST", "/api/nick-claims/test", map[string]any{}},
		{"PATCH", "/api/config", map[string]any{"set": map[string]any{stackconfig.KeyNickClaimEnabled: false}}},
	} {
		w := h.do(rq.method, rq.path, rq.body, withCookie(c), withHeader(CSRFHeader, csrf))
		if w.Code != 403 || errCode(t, w.Body.String()) != "missing_permission" || !strings.Contains(w.Body.String(), admin.PermSettingsManage) {
			t.Errorf("%s %s: %d %s", rq.method, rq.path, w.Code, w.Body)
		}
	}
}
