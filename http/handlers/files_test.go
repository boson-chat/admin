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
	"github.com/boson-chat/admin/internal/services/files"
	"github.com/boson-chat/admin/internal/services/stackconfig"
)

// fakeFiles is the files service's internal listener.
type fakeFiles struct {
	srv     *httptest.Server
	deleted []string
	actors  []string
	queries []string
}

func newFakeFiles(t *testing.T) *fakeFiles {
	f := &fakeFiles{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Token") != "tok" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/internal/files/status":
			_, _ = w.Write([]byte(`{"storage":{"driver":"local","ok":true,"free_bytes":1073741824},"totals":{"files":2,"bytes":2048,"window_uploads":3,"window_bytes":4096,"uploaders":2},"config":{"enabled":true,"guest_enabled":true,"limits":{"account":{"quota_bytes":5368709120,"window_seconds":259200,"max_file_bytes":104857600},"guest":{"quota_bytes":524288000,"window_seconds":259200,"max_file_bytes":26214400}},"retention_seconds":259200},"errors":[]}`))
		case r.Method == "GET" && r.URL.Path == "/internal/files":
			f.queries = append(f.queries, r.URL.RawQuery)
			_, _ = w.Write([]byte(`{"files":[{"id":"aaaaaaaaaaaaaaaaaaaaaa","url":"https://files.example.com/f/aaaaaaaaaaaaaaaaaaaaaa/cat.png","name":"cat.png","size":1024,"content_type":"image/png","created_at":"2026-11-14T10:00:00Z","expires_at":"2026-11-17T10:00:00Z","owner":{"kind":"account","id":"usr_admin"}},{"id":"bbbbbbbbbbbbbbbbbbbbbb","url":"https://files.example.com/f/bbbbbbbbbbbbbbbbbbbbbb/x.pdf","name":"x.pdf","size":1024,"content_type":"application/pdf","created_at":"2026-11-14T10:00:00Z","expires_at":"2026-11-17T10:00:00Z","owner":{"kind":"guest","id":"g1","ip_hash":"ab12"}}],"next_cursor":"c2"}`))
		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/internal/files/"):
			id := strings.TrimPrefix(r.URL.Path, "/internal/files/")
			if id == "missing" {
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`{"error":"not_found","message":"no such file"}`))
				return
			}
			f.deleted = append(f.deleted, id)
			f.actors = append(f.actors, r.Header.Get("X-Acting-User"))
			w.WriteHeader(204)
		case r.Method == "GET" && r.URL.Path == "/internal/files/usage":
			_, _ = w.Write([]byte(`{"used":1024,"quota":5368709120,"window_seconds":259200,"max_file":104857600,"frees":[{"bytes":1024,"at":"2026-11-17T10:00:00Z"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (h *harness) withFiles(f *fakeFiles) {
	var c bosonapi.FilesClientImpl
	if f != nil {
		c = bosonapi.NewFilesClient(f.srv.URL, "tok", time.Second, nil)
	}
	svc := files.New(files.Options{Client: c})
	h.h.Files = svc
	h.h.Config.AddCheck("files.", svc.Check)
}

func TestFilesStatusListDeleteUsage(t *testing.T) {
	h := newHarness(t)
	h.withConfigFile("version: 1\n")
	f := newFakeFiles(t)
	h.withFiles(f)
	c, csrf := h.login()

	w := h.do("GET", "/api/files/status", nil, withCookie(c))
	var v files.View
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != 200 || !v.Configured || v.Status == nil ||
		v.Status.Config == nil || v.Status.Config.Limits.Account.QuotaBytes != 5<<30 || len(v.KeysEnv) != 2 {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}
	if w := h.do("GET", "/api/session", nil, withCookie(c)); !strings.Contains(w.Body.String(), `"files":true`) {
		t.Fatalf("features: %s", w.Body)
	}

	w = h.do("GET", "/api/files?user=usr_admin&q=cat&cursor=c1", nil, withCookie(c))
	var p files.Page
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || w.Code != 200 || len(p.Files) != 2 || p.NextCursor != "c2" {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if p.Files[0].Username != "root" || p.Files[1].Username != "" || p.Files[1].Owner.Kind != "guest" {
		t.Fatalf("names: %+v", p.Files)
	}
	if len(f.queries) != 1 || !strings.Contains(f.queries[0], "user=usr_admin") || !strings.Contains(f.queries[0], "q=cat") || !strings.Contains(f.queries[0], "cursor=c1") {
		t.Fatalf("query: %v", f.queries)
	}

	if w := h.do("DELETE", "/api/files/aaaaaaaaaaaaaaaaaaaaaa", nil, withCookie(c)); w.Code != http.StatusForbidden {
		t.Fatalf("no CSRF: %d", w.Code)
	}
	w = h.do("DELETE", "/api/files/aaaaaaaaaaaaaaaaaaaaaa?reason=spam", nil, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != 204 || len(f.deleted) != 1 || f.actors[0] != "root" {
		t.Fatalf("delete: %d %s %v %v", w.Code, w.Body, f.deleted, f.actors)
	}
	w = h.do("DELETE", "/api/files/missing", nil, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != 404 || errCode(t, w.Body.String()) != "file_not_found" {
		t.Fatalf("missing: %d %s", w.Code, w.Body)
	}

	w = h.do("GET", "/api/users/usr_admin/files-usage", nil, withCookie(c))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"used":1024`) || !strings.Contains(w.Body.String(), `"frees":[{`) {
		t.Fatalf("usage: %d %s", w.Code, w.Body)
	}
}

func TestFilesOff(t *testing.T) {
	h := newHarness(t)
	h.withConfigFile("version: 1\n")
	h.withFiles(nil)
	c, csrf := h.login()
	w := h.do("GET", "/api/files/status", nil, withCookie(c))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"configured":false`) || !strings.Contains(w.Body.String(), "BOSON_FILES_INTERNAL_URL") {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}
	for _, rq := range []struct{ method, path string }{{"GET", "/api/files"}, {"DELETE", "/api/files/x"}, {"GET", "/api/users/usr_admin/files-usage"}} {
		w := h.do(rq.method, rq.path, nil, withCookie(c), withHeader(CSRFHeader, csrf))
		if w.Code != 503 || errCode(t, w.Body.String()) != "files_off" {
			t.Errorf("%s %s: %d %s", rq.method, rq.path, w.Code, w.Body)
		}
	}
}

func TestFilesBadToken(t *testing.T) {
	h := newHarness(t)
	h.withConfigFile("version: 1\n")
	f := newFakeFiles(t)
	svc := files.New(files.Options{Client: bosonapi.NewFilesClient(f.srv.URL, "wrong", time.Second, nil)})
	h.h.Files = svc
	c, _ := h.login()
	w := h.do("GET", "/api/files/status", nil, withCookie(c))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "BOSON_FILES_INTERNAL_TOKEN") {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}
}

func TestFilesSaveValidation(t *testing.T) {
	h := newHarness(t)
	path := h.withConfigFile("version: 1\n")
	h.withFiles(newFakeFiles(t))
	c, csrf := h.login()
	patch := func(body map[string]any) (int, string) {
		w := h.do("PATCH", "/api/config", body, withCookie(c), withHeader(CSRFHeader, csrf))
		return w.Code, w.Body.String()
	}
	set := func(kv map[string]any) (int, string) { return patch(map[string]any{"set": kv}) }

	// s3 without endpoint / bucket.
	if code, body := set(map[string]any{stackconfig.KeyFilesDriver: "s3"}); code != 400 || errField(body) != stackconfig.KeyFilesS3Endpoint {
		t.Fatalf("s3 alone: %d %s", code, body)
	}
	if code, body := set(map[string]any{stackconfig.KeyFilesDriver: "s3", stackconfig.KeyFilesS3Endpoint: "http://minio:9000"}); code != 400 || errField(body) != stackconfig.KeyFilesS3Bucket {
		t.Fatalf("no bucket: %d %s", code, body)
	}
	// A max file above the quota (default 5GB / 500MB).
	if code, body := set(map[string]any{stackconfig.KeyFilesGuestMaxFile: "1GB"}); code != 400 || errField(body) != stackconfig.KeyFilesGuestMaxFile {
		t.Fatalf("guest max > quota: %d %s", code, body)
	}
	for key, v := range map[string]any{
		stackconfig.KeyFilesAccountQuota:    "lots",
		stackconfig.KeyFilesAccountMaxFile:  "10",
		stackconfig.KeyFilesRetention:       "3 days",
		stackconfig.KeyFilesWindow:          "1s",
		stackconfig.KeyFilesDriver:          "ftp",
		stackconfig.KeyFilesS3Endpoint:      "minio:9000",
		stackconfig.KeyFilesS3Bucket:        "Bad_Bucket",
		stackconfig.KeyFilesS3PublicBaseURL: "http://cdn.example.com",
	} {
		if code, body := set(map[string]any{key: v}); code != 400 || !strings.Contains(body, key) {
			t.Errorf("%s=%v: %d %s", key, v, code, body)
		}
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "files:") {
		t.Fatalf("a refused change was written:\n%s", b)
	}
	// Human units are written in the file's form.
	code, body := set(map[string]any{
		stackconfig.KeyFilesEnabled:        true,
		stackconfig.KeyFilesRetention:      "48h",
		stackconfig.KeyFilesWindow:         "72h",
		stackconfig.KeyFilesAccountQuota:   "10 GB",
		stackconfig.KeyFilesAccountMaxFile: "1.5gb",
		stackconfig.KeyFilesGuestEnabled:   false,
		stackconfig.KeyFilesDriver:         "s3",
		stackconfig.KeyFilesS3Endpoint:     "http://minio:9000/",
		stackconfig.KeyFilesS3Bucket:       "boson-files",
		stackconfig.KeyFilesS3Region:       "auto",
		stackconfig.KeyFilesS3PathStyle:    true,
	})
	if code != 200 {
		t.Fatalf("save: %d %s", code, body)
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{"files:", "enabled: true", "retention: 48h", "quota: 10GB", "max_file: 1536MB", "guest:", "driver: s3", "endpoint: http://minio:9000", "bucket: boson-files", "path_style: true"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("file lacks %q:\n%s", want, b)
		}
	}
}

// Moderation needs users.moderate (or users.manage); the Uploads page
// settings.manage.
func TestFilesPermissions(t *testing.T) {
	h := newHarness(t)
	h.withConfigFile("version: 1\n")
	f := newFakeFiles(t)
	h.withFiles(f)
	c, csrf := h.moderator()
	opts := []reqOpt{withCookie(c), withHeader(CSRFHeader, csrf)}
	if w := h.do("GET", "/api/files", nil, opts...); w.Code != 200 {
		t.Fatalf("moderator list: %d %s", w.Code, w.Body)
	}
	if w := h.do("DELETE", "/api/files/aaaaaaaaaaaaaaaaaaaaaa", nil, opts...); w.Code != 204 || f.actors[0] != "mod" {
		t.Fatalf("moderator delete: %d %s", w.Code, w.Body)
	}
	if w := h.do("GET", "/api/users/usr_mod/files-usage", nil, opts...); w.Code != 200 {
		t.Fatalf("usage (users.view): %d %s", w.Code, w.Body)
	}
	for _, w := range []*httptest.ResponseRecorder{
		h.do("GET", "/api/files/status", nil, opts...),
		h.do("PATCH", "/api/config", map[string]any{"set": map[string]any{stackconfig.KeyFilesEnabled: true}}, opts...),
	} {
		if w.Code != 403 || !strings.Contains(w.Body.String(), admin.PermSettingsManage) {
			t.Errorf("%d %s", w.Code, w.Body)
		}
	}

	h2 := newHarness(t)
	h2.withConfigFile("version: 1\n")
	h2.withFiles(newFakeFiles(t))
	h2.fake.Roles = append(h2.fake.Roles, rolesFixture("rol_ops", "Ops", 300, admin.PermPanel, admin.PermSettingsManage))
	h2.fake.AddUser("usr_ops", "ops", "user", "ops password")
	h2.fake.SetRoles("usr_ops", "rol_ops")
	c2, csrf2 := h2.loginAs("ops", "ops password")
	for _, rq := range []struct{ method, path string }{{"GET", "/api/files"}, {"DELETE", "/api/files/x"}} {
		w := h2.do(rq.method, rq.path, nil, withCookie(c2), withHeader(CSRFHeader, csrf2))
		if w.Code != 403 || !strings.Contains(w.Body.String(), admin.PermUsersModerate) {
			t.Errorf("%s %s: %d %s", rq.method, rq.path, w.Code, w.Body)
		}
	}
}
