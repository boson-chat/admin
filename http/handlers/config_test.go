package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/boson-chat/admin/internal/configfile"
	"github.com/boson-chat/admin/internal/services/admin"
	"github.com/boson-chat/admin/internal/services/stackconfig"
)

// withConfigFile gives the harness a stack config file.
func (h *harness) withConfigFile(content string) string {
	h.t.Helper()
	path := filepath.Join(h.t.TempDir(), "boson.yaml")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			h.t.Fatal(err)
		}
	}
	h.h.Config = stackconfig.New(stackconfig.Options{
		Store:    configfile.Open(path, configfile.Options{Header: stackconfig.FileHeader}),
		Defaults: stackconfig.Defaults{DirectoryRegistration: true, DirectoryHostname: "irc.env.example"},
	})
	return path
}

func TestConfigReadAndWrite(t *testing.T) {
	h := newHarness(t)
	path := h.withConfigFile("version: 1\n# keep me\nfuture: {x: 1}\n")
	c, csrf := h.login()

	w := h.do("GET", "/api/config", nil, withCookie(c))
	var v stackconfig.View
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != 200 || !v.Enabled || w.Header().Get("ETag") == "" {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}

	body := map[string]any{"set": map[string]any{
		stackconfig.KeyTLSCheckHosts:         []string{"irc.example.org:6697"},
		stackconfig.KeyDirectoryRegistration: false,
	}}
	if w := h.do("PATCH", "/api/config", body, withCookie(c)); w.Code != http.StatusForbidden {
		t.Fatalf("no CSRF token: %d", w.Code)
	}
	if w := h.do("PATCH", "/api/config", body, withCookie(c), withHeader(CSRFHeader, csrf), withHeader("If-Match", `"nope"`)); w.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale If-Match: %d %s", w.Code, w.Body)
	}
	w = h.do("PATCH", "/api/config", body, withCookie(c), withHeader(CSRFHeader, csrf), withHeader("If-Match", v.ETag))
	if w.Code != 200 {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "tls_check_hosts:") || !strings.Contains(string(b), "# keep me") || !strings.Contains(string(b), "by root at") {
		t.Fatalf("file:\n%s", b)
	}
	// The session's features follow the file.
	w = h.do("GET", "/api/session", nil, withCookie(c))
	if !strings.Contains(w.Body.String(), `"config_file":true`) || strings.Contains(w.Body.String(), `"directory_registration":true`) {
		t.Fatalf("session: %s", w.Body)
	}

	bad := map[string]any{"set": map[string]any{stackconfig.KeyTLSCheckHosts: []string{"irc.example.org:99999"}}}
	if w := h.do("PATCH", "/api/config", bad, withCookie(c), withHeader(CSRFHeader, csrf)); w.Code != 400 || !strings.Contains(w.Body.String(), "65535") {
		t.Fatalf("bad port: %d %s", w.Code, w.Body)
	}
	reset := map[string]any{"unset": []string{stackconfig.KeyTLSCheckHosts}}
	if w := h.do("PATCH", "/api/config", reset, withCookie(c), withHeader(CSRFHeader, csrf)); w.Code != 200 {
		t.Fatalf("reset: %d %s", w.Code, w.Body)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "tls_check_hosts") {
		t.Fatalf("reset kept the key:\n%s", b)
	}

	w = h.do("GET", "/api/config/raw", nil, withCookie(c))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "# keep me") {
		t.Fatalf("raw: %d %s", w.Code, w.Body)
	}
	w = h.do("GET", "/api/config/status", nil, withCookie(c))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"admin"`) {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}
}

// Each key needs its own permission on top of the route's.
func TestConfigPerKeyPermission(t *testing.T) {
	h := newHarness(t)
	h.withConfigFile("version: 1\n")
	h.fake.Roles = append(h.fake.Roles, rolesFixture("rol_dir", "Directory", 300, admin.PermPanel, admin.PermDirectoryManage))
	h.fake.AddUser("usr_dir", "dir", "user", "dir password")
	h.fake.SetRoles("usr_dir", "rol_dir")
	c, csrf := h.loginAs("dir", "dir password")

	ok := map[string]any{"set": map[string]any{stackconfig.KeyDirectoryHostname: "irc.example.org"}}
	if w := h.do("PATCH", "/api/config", ok, withCookie(c), withHeader(CSRFHeader, csrf)); w.Code != 200 {
		t.Fatalf("directory key: %d %s", w.Code, w.Body)
	}
	no := map[string]any{"set": map[string]any{stackconfig.KeyTLSCheckHosts: []string{"a.example.org:443"}}}
	w := h.do("PATCH", "/api/config", no, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != 403 || errCode(t, w.Body.String()) != "missing_permission" || !strings.Contains(w.Body.String(), admin.PermServicesManage) {
		t.Fatalf("tls hosts: %d %s", w.Code, w.Body)
	}
	var v stackconfig.View
	_ = json.Unmarshal(h.do("GET", "/api/config", nil, withCookie(c)).Body.Bytes(), &v)
	for _, k := range v.Keys {
		if want := k.Perm == admin.PermDirectoryManage; k.Editable != want {
			t.Errorf("%s editable=%v", k.Key.Key, k.Editable)
		}
	}
}

// With the file, Network settings write auth's section instead of PATCHing auth.
func TestNetworkSettingsWriteTheFile(t *testing.T) {
	h := newHarness(t)
	path := h.withConfigFile("version: 1\n")
	c, csrf := h.login()
	w := h.do("PATCH", "/api/settings", map[string]any{"registration": "closed", "channel_expiry_days": 30}, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != 200 {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "auth:\n  ") || !strings.Contains(string(b), "registration: closed") || !strings.Contains(string(b), "channel_expiry_days: 30") {
		t.Fatalf("file:\n%s", b)
	}
	var out struct {
		Settings map[string]any `json:"settings"`
		Config   struct {
			Managed bool              `json:"managed"`
			Sources map[string]string `json:"sources"`
		} `json:"config"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Settings["registration"] != "closed" || !out.Config.Managed || out.Config.Sources["registration"] != "file" || out.Config.Sources["channel_creation"] != "default" {
		t.Fatalf("response: %s", w.Body)
	}
	if st, _ := h.fake.GetSettings(t.Context(), ""); st != nil && st.Registration == "closed" {
		t.Fatal("auth was PATCHed too")
	}
	if w := h.do("PATCH", "/api/settings", map[string]any{"registration": "sometimes"}, withCookie(c), withHeader(CSRFHeader, csrf)); w.Code != 400 {
		t.Fatalf("bad value: %d %s", w.Code, w.Body)
	}
}

// guest_voice goes to auth.guest_voice in the file like the other settings.
func TestNetworkSettingsGuestVoiceInFile(t *testing.T) {
	h := newHarness(t)
	path := h.withConfigFile("version: 1\n")
	c, csrf := h.login()
	w := h.do("PATCH", "/api/settings", map[string]any{"guest_voice": true}, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != 200 {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "guest_voice: true") {
		t.Fatalf("file:\n%s", b)
	}
	var out struct {
		Settings map[string]any `json:"settings"`
		Config   struct {
			Sources map[string]string `json:"sources"`
		} `json:"config"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Settings["guest_voice"] != true || out.Config.Sources["guest_voice"] != "file" || out.Config.Sources["guest_access"] != "default" {
		t.Fatalf("response: %s", w.Body)
	}
}

func TestConfigOffWithoutFile(t *testing.T) {
	h := newHarness(t)
	c, csrf := h.login()
	w := h.do("GET", "/api/config", nil, withCookie(c))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}
	w = h.do("PATCH", "/api/config", map[string]any{"set": map[string]any{"network.name": "x"}}, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != 503 || errCode(t, w.Body.String()) != "config_file_off" {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
}
