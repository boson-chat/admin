package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/boson-chat/admin/internal/services/admin"
)

// The MJML compiler runs in a worker file served from the panel: the CSP
// allows that and nothing looser (no blob: workers, no eval).
func TestCSPWorkers(t *testing.T) {
	spa, err := NewSPA(fstest.MapFS{"index.html": {Data: []byte("<!doctype html><script>boot()</script>")}})
	if err != nil {
		t.Fatal(err)
	}
	csp := spa.CSP()
	if !strings.Contains(csp, "worker-src 'self';") || strings.Contains(csp, "blob:") || strings.Contains(csp, "unsafe-eval") {
		t.Fatal(csp)
	}
}

const (
	tLayoutMJML = "<mjml><mj-body><mj-text>{{content}}</mj-text></mj-body></mjml>"
	tLayoutHTML = "<!doctype html><html><body>{{content}}</body></html>"
)

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	es, _ := os.ReadDir(dir)
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func TestEmailMJMLRoutes(t *testing.T) {
	h := newHarness(t)
	path, fe := h.withBranding("version: 1\n")
	dir := filepath.Join(filepath.Dir(path), "emails")
	c, csrf := h.login()
	opts := []reqOpt{withCookie(c), withHeader(CSRFHeader, csrf)}

	w := h.do("GET", "/api/emails/mjml/layout", nil, opts...)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"set":false`) {
		t.Fatalf("get none: %d %s", w.Code, w.Body)
	}
	if w := h.do("GET", "/api/emails/mjml/..%2Fboson.yaml", nil, opts...); w.Code != 400 {
		t.Fatalf("bad part: %d %s", w.Code, w.Body)
	}
	if w := h.do("PUT", "/api/emails/mjml/layout", map[string]any{"mjml": tLayoutMJML}, opts...); w.Code != 400 {
		t.Fatalf("no html: %d %s", w.Code, w.Body)
	}
	w = h.do("PUT", "/api/emails/mjml/layout", map[string]any{"mjml": tLayoutMJML, "html": "<p>x</p>"}, opts...)
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"field":"html"`) || !strings.Contains(w.Body.String(), "{{content}}") {
		t.Fatalf("no content: %d %s", w.Code, w.Body)
	}
	w = h.do("PUT", "/api/emails/mjml/invite", map[string]any{"mjml": "<mjml/>", "html": "<p>x</p>"}, opts...)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "{{link}}") {
		t.Fatalf("no link: %d %s", w.Code, w.Body)
	}
	big := strings.Repeat("x", 600<<10) + "{{content}}"
	if w := h.do("PUT", "/api/emails/mjml/layout", map[string]any{"mjml": tLayoutMJML, "html": big}, opts...); w.Code != 400 || !strings.Contains(w.Body.String(), "512 KB") {
		t.Fatalf("too big: %d %.200s", w.Code, w.Body)
	}
	huge := strings.Repeat("x", 4<<20)
	if w := h.do("PUT", "/api/emails/mjml/layout", map[string]any{"mjml": huge, "html": huge}, opts...); w.Code != 413 {
		t.Fatalf("huge body: %d %.200s", w.Code, w.Body)
	}
	if n := dirNames(t, dir); len(n) != 0 {
		t.Fatalf("rejected writes left %v", n)
	}

	w = h.do("PUT", "/api/emails/mjml/layout", map[string]any{"mjml": tLayoutMJML, "html": tLayoutHTML}, opts...)
	if w.Code != 200 || w.Header().Get("ETag") == "" {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	var out struct {
		Source struct {
			MJMLFile string `json:"mjml_file"`
			HTMLFile string `json:"html_file"`
		} `json:"source"`
		Config struct {
			Keys []struct {
				Key   string `json:"key"`
				Value any    `json:"value"`
			} `json:"keys"`
		} `json:"config"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	found := false
	for _, k := range out.Config.Keys {
		if k.Key == "auth.email.layout.html" && k.Value == "emails/"+out.Source.HTMLFile {
			found = true
		}
	}
	if !found || out.Source.MJMLFile == "" {
		t.Fatalf("answer: %s", w.Body)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "html: emails/"+out.Source.HTMLFile) {
		t.Fatalf("file:\n%s", b)
	}
	w = h.do("GET", "/api/emails/mjml/layout", nil, opts...)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"set":true`) || !strings.Contains(w.Body.String(), "mj-text") {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}

	// PATCH /api/config can't point the keys elsewhere.
	w = h.do("PATCH", "/api/config", map[string]any{"set": map[string]any{"auth.email.layout.html": "emails/x.html"}}, opts...)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "PUT /api/emails/mjml") {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	w = h.do("PATCH", "/api/config", map[string]any{"unset": []string{"auth.email.layout.mjml"}}, opts...)
	if w.Code != 400 {
		t.Fatalf("patch unset: %d %s", w.Code, w.Body)
	}

	// A stale If-Match changes nothing.
	if w := h.do("PUT", "/api/emails/mjml/layout", map[string]any{"mjml": tLayoutMJML + " ", "html": tLayoutHTML}, append(opts, withHeader("If-Match", `"old"`))...); w.Code != 412 {
		t.Fatalf("stale: %d %s", w.Code, w.Body)
	}
	if n := dirNames(t, dir); len(n) != 2 {
		t.Fatalf("stale write left %v", n)
	}

	w = h.do("DELETE", "/api/emails/mjml/layout", nil, opts...)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"config"`) {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if n := dirNames(t, dir); len(n) != 0 {
		t.Fatalf("delete left %v", n)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "layout") {
		t.Fatalf("file:\n%s", b)
	}

	// Boson's default through auth, with the admin's token.
	w = h.do("GET", "/api/emails/default-mjml?part=invite", nil, opts...)
	if w.Code != 200 || !strings.Contains(w.Body.String(), " invite --") || fe.actor == "" {
		t.Fatalf("default: %d %s", w.Code, w.Body)
	}
	if w := h.do("GET", "/api/emails/default-mjml?part=nope", nil, opts...); w.Code != 400 {
		t.Fatalf("default bad part: %d", w.Code)
	}

	// The preview takes the unsaved HTML (and checks it).
	w = h.do("POST", "/api/emails/preview", map[string]any{"template": "invite", "layout_html": "<p>none</p>"}, opts...)
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"field":"layout_html"`) {
		t.Fatalf("preview layout_html: %d %s", w.Code, w.Body)
	}
	w = h.do("POST", "/api/emails/preview", map[string]any{"template": "invite", "layout_html": strings.Repeat("y", 300<<10) + tLayoutHTML}, opts...)
	if w.Code != 200 || fe.last.LayoutHTML == nil {
		t.Fatalf("preview with a large layout: %d %s", w.Code, w.Body)
	}
}

// Without settings.manage: no reading, writing or removing MJML.
func TestEmailMJMLNeedsSettingsManage(t *testing.T) {
	h := newHarness(t)
	path, fe := h.withBranding("version: 1\n")
	h.fake.Roles = append(h.fake.Roles, rolesFixture("rol_ops", "Ops", 300, admin.PermPanel, admin.PermServicesManage))
	h.fake.AddUser("usr_ops", "ops", "user", "ops password")
	h.fake.SetRoles("usr_ops", "rol_ops")
	c, csrf := h.loginAs("ops", "ops password")
	opts := []reqOpt{withCookie(c), withHeader(CSRFHeader, csrf)}
	for _, w := range []*httptest.ResponseRecorder{
		h.do("GET", "/api/emails/mjml/layout", nil, opts...),
		h.do("PUT", "/api/emails/mjml/layout", map[string]any{"mjml": tLayoutMJML, "html": tLayoutHTML}, opts...),
		h.do("DELETE", "/api/emails/mjml/layout", nil, opts...),
		h.do("GET", "/api/emails/default-mjml?part=layout", nil, opts...),
	} {
		if w.Code != 403 || errCode(t, w.Body.String()) != "missing_permission" {
			t.Errorf("%d %s", w.Code, w.Body)
		}
	}
	if len(fe.got) != 0 {
		t.Fatalf("auth called: %v", fe.got)
	}
	if b, _ := os.ReadFile(path); string(b) != "version: 1\n" {
		t.Fatalf("file changed:\n%s", b)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "emails")); err == nil {
		t.Fatal("emails/ was written")
	}
	// No CSRF token: refused before anything else.
	if w := h.do("PUT", "/api/emails/mjml/layout", map[string]any{"mjml": tLayoutMJML, "html": tLayoutHTML}, withCookie(c)); w.Code != 403 || errCode(t, w.Body.String()) != "csrf" {
		t.Fatalf("csrf: %d %s", w.Code, w.Body)
	}
}
