package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/boson-chat/admin/http/middleware"
	"github.com/boson-chat/admin/internal/clients/authapi/authapitest"
	"github.com/boson-chat/admin/internal/services/admin"
	"github.com/boson-chat/admin/internal/services/overview"
	"github.com/boson-chat/admin/internal/session"
)

type fakeOverview struct{}

func (fakeOverview) Overview(context.Context) *overview.Overview {
	return &overview.Overview{Services: []overview.Service{{Name: "auth", Status: "up"}}}
}

const origin = "https://admin.example.com"

type harness struct {
	t     *testing.T
	fake  *authapitest.Fake
	srv   http.Handler
	codec *session.Codec
	h     *Handlers
	mux   *http.ServeMux
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	f := authapitest.New()
	f.AddUser("usr_admin", "root", "admin", "correct horse")
	f.AddUser("usr_bob", "bob", "user", "bob password")
	codec, err := session.NewCodec(bytes.Repeat([]byte{7}, 32), "boson_admin", true, 12*time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := admin.NewAdminService(f, admin.NewRefresher(f, time.Minute, time.Second, nil), nil, nil)
	h := &Handlers{Admin: svc, Overview: fakeOverview{}, Sessions: codec, Origin: origin, Features: Features{Version: "test"}}
	mux := http.NewServeMux()
	h.Register(mux)
	return &harness{t: t, fake: f, srv: middleware.TrustedClientIP(nil)(mux), codec: codec, h: h, mux: mux}
}

type reqOpt func(*http.Request)

func withCookie(c *http.Cookie) reqOpt { return func(r *http.Request) { r.AddCookie(c) } }
func withHeader(k, v string) reqOpt    { return func(r *http.Request) { r.Header.Set(k, v) } }

func (h *harness) do(method, path string, body any, opts ...reqOpt) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, rd)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Origin", origin)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	for _, o := range opts {
		o(r)
	}
	w := httptest.NewRecorder()
	h.srv.ServeHTTP(w, r)
	return w
}

func sessionCookie(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == "__Host-boson_admin" {
			return c
		}
	}
	return nil
}

// login returns the session cookie and CSRF token.
func (h *harness) login() (*http.Cookie, string) {
	h.t.Helper()
	w := h.do("POST", "/api/login", map[string]string{"login": "root", "password": "correct horse"}, withHeader(LoginHeader, "x"))
	if w.Code != 200 {
		h.t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	var v struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	c := sessionCookie(w)
	if c == nil || v.CSRFToken == "" {
		h.t.Fatalf("login gave no cookie or csrf token: %s", w.Body)
	}
	return c, v.CSRFToken
}

func TestLoginFlow(t *testing.T) {
	h := newHarness(t)
	c, _ := h.login()
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie attrs %+v", c)
	}
	// The cookie is sealed: the tokens the fake issued must not show in it.
	// (Checking for the bare "at-"/"rt-" prefixes flaked, since random
	// base64url ciphertext can contain them.)
	d, err := h.codec.Open(c.Value)
	if err != nil || d.AccessToken == "" || d.RefreshToken == "" {
		t.Fatalf("open cookie: %v %+v", err, d)
	}
	if strings.Contains(c.Value, d.AccessToken) || strings.Contains(c.Value, d.RefreshToken) {
		t.Fatal("tokens visible in cookie")
	}
	w := h.do("GET", "/api/session", nil, withCookie(c))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"username":"root"`) {
		t.Fatalf("session: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "at-1") {
		t.Fatal("access token leaked to the browser")
	}
	w = h.do("GET", "/api/users", nil, withCookie(c))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"total":2`) {
		t.Fatalf("users: %d %s", w.Code, w.Body)
	}
	if h.fake.LastMeta.IP != "192.0.2.1" {
		t.Fatalf("client IP not forwarded to the auth service: %+v", h.fake.LastMeta)
	}
}

func TestLoginRequiresHeaderAndOrigin(t *testing.T) {
	h := newHarness(t)
	body := map[string]string{"login": "root", "password": "correct horse"}
	if w := h.do("POST", "/api/login", body); w.Code != http.StatusForbidden {
		t.Fatalf("no X-Requested-With: %d", w.Code)
	}
	if w := h.do("POST", "/api/login", body, withHeader(LoginHeader, "x"), withHeader("Origin", "https://evil.example")); w.Code != http.StatusForbidden {
		t.Fatalf("cross origin: %d", w.Code)
	}
	if w := h.do("POST", "/api/login", body, withHeader(LoginHeader, "x"), withHeader("Sec-Fetch-Site", "cross-site")); w.Code != http.StatusForbidden {
		t.Fatalf("cross site: %d", w.Code)
	}
	w := h.do("POST", "/api/login", map[string]string{"login": "bob", "password": "bob password"}, withHeader(LoginHeader, "x"))
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "not_admin") || sessionCookie(w) != nil {
		t.Fatalf("non-admin: %d %s", w.Code, w.Body)
	}
	w = h.do("POST", "/api/login", map[string]string{"login": "root", "password": "wrong"}, withHeader(LoginHeader, "x"))
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "invalid_credentials") {
		t.Fatalf("bad password: %d %s", w.Code, w.Body)
	}
}

func TestCSRF(t *testing.T) {
	h := newHarness(t)
	c, csrf := h.login()
	if w := h.do("POST", "/api/users/usr_bob/disable", nil, withCookie(c)); w.Code != http.StatusForbidden {
		t.Fatalf("no token: %d", w.Code)
	}
	if w := h.do("POST", "/api/users/usr_bob/disable", nil, withCookie(c), withHeader(CSRFHeader, "nope")); w.Code != http.StatusForbidden {
		t.Fatalf("wrong token: %d", w.Code)
	}
	if w := h.do("POST", "/api/users/usr_bob/disable", nil, withCookie(c), withHeader(CSRFHeader, csrf), withHeader("Origin", "https://evil.example")); w.Code != http.StatusForbidden {
		t.Fatalf("right token, wrong origin: %d", w.Code)
	}
	w := h.do("POST", "/api/users/usr_bob/disable", nil, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "disabled_at") {
		t.Fatalf("disable: %d %s", w.Code, w.Body)
	}
	w = h.do("POST", "/api/users/usr_bob/enable", nil, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != 200 || strings.Contains(w.Body.String(), "disabled_at") {
		t.Fatalf("enable: %d %s", w.Code, w.Body)
	}
}

func TestUnauthenticated(t *testing.T) {
	h := newHarness(t)
	if w := h.do("GET", "/api/users", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("no cookie: %d", w.Code)
	}
	w := h.do("GET", "/api/users", nil, withCookie(&http.Cookie{Name: "__Host-boson_admin", Value: "garbage"}))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("garbage cookie: %d", w.Code)
	}
	if c := sessionCookie(w); c == nil || c.MaxAge != -1 {
		t.Fatal("garbage cookie not cleared")
	}
	if w := h.do("GET", "/api/nope", nil); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "not_found") {
		t.Fatalf("unknown api route: %d %s", w.Code, w.Body)
	}
}

func TestRefreshReissuesCookie(t *testing.T) {
	h := newHarness(t)
	c, _ := h.login()
	h.fake.ExpireAccess()
	w := h.do("GET", "/api/users", nil, withCookie(c))
	if w.Code != 200 {
		t.Fatalf("users after expiry: %d %s", w.Code, w.Body)
	}
	nc := sessionCookie(w)
	if nc == nil || nc.Value == c.Value {
		t.Fatal("refreshed tokens were not written back to the cookie")
	}
	d, err := h.codec.Open(nc.Value)
	if err != nil || d.RefreshToken == "rt-1" {
		t.Fatalf("new cookie: %v %+v", err, d)
	}
	// The new cookie works without another refresh.
	if w := h.do("GET", "/api/users", nil, withCookie(nc)); w.Code != 200 || h.fake.RefreshCalls.Load() != 1 {
		t.Fatalf("new cookie: %d, refreshes %d", w.Code, h.fake.RefreshCalls.Load())
	}
}

func TestExpiredSessionClearsCookie(t *testing.T) {
	h := newHarness(t)
	c, _ := h.login()
	h.fake.ExpireAccess()
	// Revoke upstream: use the refresh token once outside the panel so the
	// panel's copy is a reuse.
	d, _ := h.codec.Open(c.Value)
	_, _ = h.fake.Refresh(context.Background(), d.RefreshToken)
	w := h.do("GET", "/api/users", nil, withCookie(c))
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "session_expired") {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	if nc := sessionCookie(w); nc == nil || nc.MaxAge != -1 {
		t.Fatal("cookie not cleared")
	}
}

func TestLogout(t *testing.T) {
	h := newHarness(t)
	c, csrf := h.login()
	w := h.do("POST", "/api/logout", nil, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != http.StatusNoContent || h.fake.LogoutCalls.Load() != 1 {
		t.Fatalf("logout: %d calls=%d", w.Code, h.fake.LogoutCalls.Load())
	}
	if nc := sessionCookie(w); nc == nil || nc.MaxAge != -1 {
		t.Fatal("cookie not cleared")
	}
}

func TestInvitesAndAudit(t *testing.T) {
	h := newHarness(t)
	c, csrf := h.login()
	w := h.do("POST", "/api/invites", map[string]any{"max_uses": 3, "ttl": "168h", "note": "ana"}, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"code":"code-inv_1"`) {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	w = h.do("GET", "/api/invites", nil, withCookie(c))
	if w.Code != 200 || strings.Contains(w.Body.String(), "code-") || !strings.Contains(w.Body.String(), `"registration":"invite"`) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if w := h.do("POST", "/api/invites", map[string]any{"ttl": "7d"}, withCookie(c), withHeader(CSRFHeader, csrf)); w.Code != 400 {
		t.Fatalf("bad ttl: %d", w.Code)
	}
	if w := h.do("GET", "/api/audit?since=nonsense", nil, withCookie(c)); w.Code != 400 {
		t.Fatalf("bad since: %d", w.Code)
	}
	if w := h.do("GET", "/api/audit?since=24h&event=admin_*", nil, withCookie(c)); w.Code != 200 || !strings.Contains(w.Body.String(), `"events"`) {
		t.Fatalf("audit: %d %s", w.Code, w.Body)
	}
}

func TestCannotRevokeOwnPanelSession(t *testing.T) {
	h := newHarness(t)
	c, csrf := h.login()
	d, _ := h.codec.Open(c.Value)
	w := h.do("DELETE", "/api/sessions/"+d.SessionID, nil, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != 400 {
		t.Fatalf("got %d", w.Code)
	}
	w = h.do("GET", "/api/users/usr_admin/sessions", nil, withCookie(c))
	if !strings.Contains(w.Body.String(), `"current":true`) {
		t.Fatalf("current session not marked: %s", w.Body)
	}
}

func TestOverviewAndSSO(t *testing.T) {
	h := newHarness(t)
	c, _ := h.login()
	if w := h.do("GET", "/api/overview", nil, withCookie(c)); w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"auth"`) {
		t.Fatalf("overview: %d %s", w.Code, w.Body)
	}
	if w := h.do("GET", "/api/sso/providers", nil, withCookie(c)); w.Code != 200 || !strings.Contains(w.Body.String(), `"providers":[]`) {
		t.Fatalf("sso: %d %s", w.Code, w.Body)
	}
}

func TestSPA(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":            {Data: []byte(`<html><script src="/x.js"></script><script>boot()</script></html>`)},
		"_app/immutable/app.js": {Data: []byte("x")},
		"favicon.svg":           {Data: []byte("<svg/>")},
	}
	spa, err := NewSPA(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(spa.ScriptHashes) != 1 || !strings.Contains(spa.CSP(), spa.ScriptHashes[0]) || !strings.Contains(spa.CSP(), "frame-ancestors 'none'") {
		t.Fatalf("csp: %s", spa.CSP())
	}
	if !strings.Contains(spa.CSP(), "connect-src 'self';") {
		t.Fatalf("connect-src: %s", spa.CSP())
	}
	csp := spa.CSP("https://auth.example.com", "wss://irc.example.com", "https://auth.example.com", "*", "x;y")
	if !strings.Contains(csp, "connect-src 'self' https://auth.example.com wss://irc.example.com;") {
		t.Fatalf("connect-src with probes: %s", csp)
	}
	get := func(p string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		spa.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		return w
	}
	if w := get("/users/usr_1"); w.Code != 200 || !strings.Contains(w.Body.String(), "boot()") || w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("client route: %d %q", w.Code, w.Header().Get("Cache-Control"))
	}
	if w := get("/_app/immutable/app.js"); w.Code != 200 || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("asset: %d", w.Code)
	}
	if w := get("/_app/immutable/missing.js"); w.Code != 404 {
		t.Fatalf("missing asset: %d", w.Code)
	}
	if w := get("/../../etc/passwd"); w.Code != 200 || !strings.Contains(w.Body.String(), "boot()") {
		t.Fatalf("traversal: %d", w.Code)
	}
}
