package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/services/admin"
	"github.com/boson-chat/admin/internal/services/branding"
)

type fakeEmail struct {
	got   []string
	actor string
}

func (f *fakeEmail) Preview(_ context.Context, actor string, in authapi.EmailPreviewRequest) (*authapi.EmailPreview, error) {
	f.actor = actor
	f.got = append(f.got, "preview:"+in.Template)
	return &authapi.EmailPreview{Subject: "s", HTML: "<p>hi</p>", Text: "hi"}, nil
}

func (f *fakeEmail) SendTest(_ context.Context, actor string, in authapi.EmailTestRequest) (*authapi.EmailTestResult, error) {
	f.actor = actor
	f.got = append(f.got, "test:"+in.Template+":"+in.To)
	return &authapi.EmailTestResult{Sent: true}, nil
}

func (h *harness) withBranding(content string) (string, *fakeEmail) {
	path := h.withConfigFile(content)
	fe := &fakeEmail{}
	h.h.Branding = branding.NewBrandingService(branding.Options{Config: h.h.Config, Email: fe})
	return path, fe
}

func tinyPNG(t *testing.T) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func (h *harness) raw(method, path, ct string, body []byte, opts ...reqOpt) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if ct != "" {
		r.Header.Set("Content-Type", ct)
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

func TestBrandingImageUploadOverHTTP(t *testing.T) {
	h := newHarness(t)
	path, _ := h.withBranding("version: 1\n# hi\n")
	c, csrf := h.login()

	if w := h.raw("PUT", "/api/branding/images/logo", "image/png", tinyPNG(t), withCookie(c)); w.Code != 403 || errCode(t, w.Body.String()) != "csrf" {
		t.Fatalf("no csrf: %d %s", w.Code, w.Body)
	}
	if w := h.raw("PUT", "/api/branding/images/logo", "application/json", []byte("{}"), withCookie(c), withHeader(CSRFHeader, csrf)); w.Code != 415 {
		t.Fatalf("json body: %d %s", w.Code, w.Body)
	}
	if w := h.raw("PUT", "/api/branding/images/logo", "image/png", make([]byte, branding.MaxImageBytes+10), withCookie(c), withHeader(CSRFHeader, csrf)); w.Code != 413 {
		t.Fatalf("too big: %d %s", w.Code, w.Body)
	}
	if w := h.raw("PUT", "/api/branding/images/logo", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script/></svg>`), withCookie(c), withHeader(CSRFHeader, csrf)); w.Code != 422 {
		t.Fatalf("script svg: %d %s", w.Code, w.Body)
	}

	// The browser says JPEG; the bytes are a PNG: sniffed, re-encoded.
	w := h.raw("PUT", "/api/branding/images/login_background", "image/jpeg", tinyPNG(t), withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != 200 || w.Header().Get("ETag") == "" {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	var out struct {
		Image  branding.Image `json:"image"`
		Config struct {
			ETag string `json:"etag"`
		} `json:"config"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if !strings.HasPrefix(out.Image.File, "login_background-") || out.Image.ContentType != "image/webp" || out.Config.ETag == "" {
		t.Fatalf("%s", w.Body)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "login_background: "+out.Image.File) || !strings.Contains(string(b), "# hi") {
		t.Fatalf("file:\n%s", b)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "branding", out.Image.File)); err != nil {
		t.Fatal(err)
	}

	// Served to the panel with safe headers.
	w = h.do("GET", out.Image.URL, nil, withCookie(c))
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/webp" || w.Header().Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("asset: %d %v", w.Code, w.Header())
	}
	if w := h.do("GET", out.Image.URL, nil); w.Code != 401 {
		t.Fatalf("asset without a session: %d", w.Code)
	}
	if w := h.do("GET", "/api/branding/assets/..%2Fboson.yaml", nil, withCookie(c)); w.Code != 404 {
		t.Fatalf("traversal: %d %s", w.Code, w.Body)
	}

	// Stale If-Match.
	if w := h.raw("PUT", "/api/branding/images/logo", "image/png", tinyPNG(t), withCookie(c), withHeader(CSRFHeader, csrf), withHeader("If-Match", `"old"`)); w.Code != 412 {
		t.Fatalf("stale: %d %s", w.Code, w.Body)
	}

	w = h.do("DELETE", "/api/branding/images/login_background", nil, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if w := h.do("GET", out.Image.URL, nil, withCookie(c)); w.Code != 404 {
		t.Fatalf("removed file still served: %d", w.Code)
	}
	w = h.do("GET", "/api/session", nil, withCookie(c))
	if !strings.Contains(w.Body.String(), `"branding":true`) || !strings.Contains(w.Body.String(), `"email_preview":true`) {
		t.Fatalf("features: %s", w.Body)
	}
}

// Without settings.manage: no uploads, no theme, no email previews or tests.
func TestBrandingNeedsSettingsManage(t *testing.T) {
	h := newHarness(t)
	path, fe := h.withBranding("version: 1\n")
	h.fake.Roles = append(h.fake.Roles, rolesFixture("rol_ops", "Ops", 300, admin.PermPanel, admin.PermServicesManage, admin.PermDirectoryManage))
	h.fake.AddUser("usr_ops", "ops", "user", "ops password")
	h.fake.SetRoles("usr_ops", "rol_ops")
	c, csrf := h.loginAs("ops", "ops password")
	opts := []reqOpt{withCookie(c), withHeader(CSRFHeader, csrf)}

	for _, w := range []*httptest.ResponseRecorder{
		h.raw("PUT", "/api/branding/images/logo", "image/png", tinyPNG(t), opts...),
		h.do("DELETE", "/api/branding/images/logo", nil, opts...),
		h.do("POST", "/api/emails/preview", map[string]any{"template": "invite"}, opts...),
		h.do("POST", "/api/emails/test", map[string]any{"template": "invite", "to": "a@example.com"}, opts...),
		h.do("PATCH", "/api/config", map[string]any{"set": map[string]any{"branding.theme": map[string]any{"base": "light"}}}, opts...),
		h.do("PATCH", "/api/config", map[string]any{"set": map[string]any{"auth.email.footer": "x"}}, opts...),
	} {
		if w.Code != 403 || errCode(t, w.Body.String()) != "missing_permission" || !strings.Contains(w.Body.String(), admin.PermSettingsManage) {
			t.Errorf("%d %s", w.Code, w.Body)
		}
	}
	if len(fe.got) != 0 {
		t.Fatalf("auth called: %v", fe.got)
	}
	if b, _ := os.ReadFile(path); string(b) != "version: 1\n" {
		t.Fatalf("file changed:\n%s", b)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "branding")); err == nil {
		t.Fatal("an image was written")
	}
}

func TestEmailRoutes(t *testing.T) {
	h := newHarness(t)
	_, fe := h.withBranding("version: 1\n")
	c, csrf := h.login()
	opts := []reqOpt{withCookie(c), withHeader(CSRFHeader, csrf)}
	w := h.do("POST", "/api/emails/preview", map[string]any{"template": "verification", "subject": "x"}, opts...)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"subject":"s"`) || !strings.Contains(w.Body.String(), `"text":"hi"`) {
		t.Fatalf("preview: %d %s", w.Code, w.Body)
	}
	if w := h.do("POST", "/api/emails/preview", map[string]any{"template": "nope"}, opts...); w.Code != 400 {
		t.Fatalf("bad template: %d", w.Code)
	}
	w = h.do("POST", "/api/emails/test", map[string]any{"template": "invite", "to": "me@example.com"}, opts...)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"sent":true`) || fe.actor == "" {
		t.Fatalf("test: %d %s %s", w.Code, w.Body, fe.actor)
	}
	if strings.Join(fe.got, ",") != "preview:verification,test:invite:me@example.com" {
		t.Fatalf("%v", fe.got)
	}
	if w := h.do("POST", "/api/emails/test", map[string]any{"template": "invite", "to": "x"}, opts...); w.Code != 400 {
		t.Fatalf("bad address: %d", w.Code)
	}
	h.h.Branding = nil
	if w := h.do("POST", "/api/emails/test", map[string]any{"template": "invite", "to": "me@example.com"}, opts...); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("off: %d", w.Code)
	}
}
