package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/clients/authapi/authapitest"
	"github.com/boson-chat/admin/internal/services/admin"
	"github.com/boson-chat/admin/internal/services/federation"
	"github.com/boson-chat/admin/internal/services/stackconfig"
)

// wellKnown serves a boson.json with a federation block (or without one)
// and the JWKS it names.
func wellKnown(t *testing.T, accepts string, block bool) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/boson.json":
			doc := map[string]any{"version": 1, "auth": map[string]string{"url": "https://auth.example.org"}}
			if block {
				doc["federation"] = map[string]any{"version": 1, "issuer": "https://auth.example.org",
					"jwks_uri": srv.URL + "/jwks.json", "assertion_endpoint": "https://auth.example.org/v1/federation/assertions",
					"login_endpoint": "https://auth.example.org/v1/federation/login", "accepts": accepts}
			}
			_ = json.NewEncoder(w).Encode(doc)
		case "/jwks.json":
			_, _ = w.Write([]byte(`{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"k1","x":"AAAA"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// withFederation rebuilds the federation service on the harness's config
// file and fake, checking boson.json at wk.
func (h *harness) withFederation(wk string) {
	svc := federation.New(federation.Options{Client: h.fake, Tokens: h.h.Admin, Config: h.h.Config,
		Home: &federation.HomeChecker{Domain: "example.org", URL: wk}})
	h.h.Federation = svc
	h.h.Config.AddCheck("auth.federation.", svc.Check)
}

func fedView(t *testing.T, w *httptest.ResponseRecorder) federation.View {
	t.Helper()
	var v federation.View
	if w.Code != 200 {
		t.Fatalf("federation: %d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestFederationOlderAuth(t *testing.T) {
	h := newHarness(t)
	c, csrf := h.login()
	v := fedView(t, h.do("GET", "/api/federation", nil, withCookie(c)))
	if v.Supported || v.Requires != authapi.RequiresFederation || v.Store != federation.StoreEnv || v.Editable {
		t.Fatalf("older auth: %+v", v)
	}
	if v.Settings.Mode != authapi.FederationDirectory || v.Settings.DirectoryURL != federation.DefaultDirectoryURL {
		t.Fatalf("defaults: %+v", v.Settings)
	}
	if w := h.do("GET", "/api/federation/users", nil, withCookie(c)); w.Code != http.StatusNotImplemented {
		t.Fatalf("users on older auth: %d %s", w.Code, w.Body)
	}
	// Nowhere to save: read-only, with what to set instead.
	w := h.do("PATCH", "/api/federation", map[string]any{"mode": "open"}, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "FEDERATION_") {
		t.Fatalf("read-only patch: %d %s", w.Code, w.Body)
	}
}

func TestFederationSettingsInConfigFile(t *testing.T) {
	h := newHarness(t)
	path := h.withConfigFile("version: 1\n")
	h.fake.EnableFederation(false)
	wk := wellKnown(t, "directory", true)
	h.withFederation(wk.URL + "/.well-known/boson.json")
	c, csrf := h.login()

	v := fedView(t, h.do("GET", "/api/federation", nil, withCookie(c)))
	if !v.Supported || v.Store != federation.StoreFile || !v.Editable || v.Domain != "example.org" || v.RemoteUsers != 3 {
		t.Fatalf("view: %+v", v)
	}
	if v.Home == nil || v.Home.State != federation.HomePublished {
		t.Fatalf("home: %+v", v.Home)
	}

	body := map[string]any{"mode": "allowlist", "allow": []string{"Boson.Chat", "*.Example.NET"}, "remote_upload_quota": "1 gb", "remote_voice": false}
	v = fedView(t, h.do("PATCH", "/api/federation", body, withCookie(c), withHeader(CSRFHeader, csrf)))
	if v.Settings.Mode != "allowlist" || !slices.Equal(v.Settings.Allow, []string{"boson.chat", "*.example.net"}) ||
		v.Settings.RemoteUploadQuota != "1GB" || v.Settings.RemoteVoice || v.Sources["mode"] != "file" {
		t.Fatalf("after save: %+v %v", v.Settings, v.Sources)
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{"federation:", "mode: allowlist", "- boson.chat", "remote_upload_quota: 1GB", "by root at"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("file lacks %q:\n%s", want, b)
		}
	}
	// The home check now sees a mode the published boson.json doesn't.
	if i := slices.IndexFunc(v.Home.Checks, func(c federation.HomeCheck) bool { return c.ID == "accepts" }); i < 0 || v.Home.Checks[i].OK {
		t.Fatalf("accepts check: %+v", v.Home.Checks)
	}

	// Bad input never reaches the file.
	for _, bad := range []map[string]any{
		{"allow": []string{"https://boson.chat/"}},
		{"allow": []string{"*.*.example.org"}},
		{"allow": []string{}},             // allowlist mode with nobody on it
		{"block": []string{"boson.chat"}}, // on both lists
		{"directory_url": "http://directory.example"},
		{"mode": "everyone"},
		{"remote_upload_quota": "lots"},
	} {
		if w := h.do("PATCH", "/api/federation", bad, withCookie(c), withHeader(CSRFHeader, csrf)); w.Code != 400 {
			t.Fatalf("%v: %d %s", bad, w.Code, w.Body)
		}
	}
	// The same rules hold for PATCH /api/config (the Stack config page).
	w := h.do("PATCH", "/api/config", map[string]any{"set": map[string]any{stackconfig.KeyFederationBlock: []string{"boson.chat"}}}, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "both") {
		t.Fatalf("config patch on both lists: %d %s", w.Code, w.Body)
	}

	// Reset to the env default removes the key.
	v = fedView(t, h.do("PATCH", "/api/federation", map[string]any{"unset": []string{"remote_voice", "remote_upload_quota"}}, withCookie(c), withHeader(CSRFHeader, csrf)))
	if v.Sources["remote_voice"] == "file" || v.Sources["remote_upload_quota"] == "file" {
		t.Fatalf("after reset: %v", v.Sources)
	}
}

func TestFederationSettingsInAuth(t *testing.T) {
	h := newHarness(t)
	h.fake.EnableFederation(true)
	h.withFederation("")
	c, csrf := h.login()
	v := fedView(t, h.do("GET", "/api/federation", nil, withCookie(c)))
	if v.Store != federation.StoreAuth || !v.Editable {
		t.Fatalf("view: %+v", v)
	}
	v = fedView(t, h.do("PATCH", "/api/federation", map[string]any{"mode": "open", "block": []string{"spam.example"}}, withCookie(c), withHeader(CSRFHeader, csrf)))
	if v.Settings.Mode != "open" || v.Sources["mode"] != "database" || !slices.Equal(v.Settings.Block, []string{"spam.example"}) {
		t.Fatalf("after save: %+v %v", v.Settings, v.Sources)
	}
	if !slices.ContainsFunc(h.fake.Audit, func(e authapi.AuditEntry) bool { return e.Event == authapitest.EventFederationSettings }) {
		t.Fatal("no audit entry")
	}
	// Reset goes to auth as null.
	v = fedView(t, h.do("PATCH", "/api/federation", map[string]any{"unset": []string{"mode"}}, withCookie(c), withHeader(CSRFHeader, csrf)))
	if v.Settings.Mode != "directory" || v.Sources["mode"] != "default" || v.UpdatedBy != "admin" || v.RemoteRole != "Remote" {
		t.Fatalf("after reset: %+v %v", v.Settings, v.Sources)
	}
	// auth switched to the config file meanwhile: a clear 409.
	h.fake.Fed.Managed = true
	w := h.do("PATCH", "/api/federation", map[string]any{"mode": "open"}, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != http.StatusConflict || errCode(t, w.Body.String()) != "settings_managed" || !strings.Contains(w.Body.String(), "BOSON_CONFIG") {
		t.Fatalf("managed: %d %s", w.Code, w.Body)
	}
}

func TestFederationRemoteUsers(t *testing.T) {
	h := newHarness(t)
	h.withConfigFile("version: 1\n")
	h.fake.EnableFederation(false)
	h.withFederation("")
	c, csrf := h.login()

	var p authapi.RemoteUserPage
	w := h.do("GET", "/api/federation/users?domain=chat.example.net", nil, withCookie(c))
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || w.Code != 200 || len(p.Users) != 2 {
		t.Fatalf("users: %d %s", w.Code, w.Body)
	}

	// Ban and unban an identity.
	w = h.do("POST", "/api/federation/users/usr_fed_alice/ban", map[string]string{"reason": "spam"}, withCookie(c), withHeader(CSRFHeader, csrf))
	var u authapi.RemoteUser
	if err := json.Unmarshal(w.Body.Bytes(), &u); err != nil || w.Code != 200 || !u.Banned || u.BanReason != "spam" {
		t.Fatalf("ban: %d %s", w.Code, w.Body)
	}
	if w := h.do("POST", "/api/federation/users/usr_fed_alice/ban", map[string]string{"reason": "a\nb"}, withCookie(c), withHeader(CSRFHeader, csrf)); w.Code != 400 {
		t.Fatalf("multi-line reason: %d", w.Code)
	}
	w = h.do("DELETE", "/api/federation/users/usr_fed_alice/ban", nil, withCookie(c), withHeader(CSRFHeader, csrf))
	if err := json.Unmarshal(w.Body.Bytes(), &u); err != nil || w.Code != 200 || u.Banned {
		t.Fatalf("unban: %d %s", w.Code, w.Body)
	}
	if w := h.do("POST", "/api/federation/users/usr_nobody/ban", nil, withCookie(c), withHeader(CSRFHeader, csrf)); w.Code != 404 {
		t.Fatalf("unknown user: %d", w.Code)
	}

	// Block a whole home domain: on the blocklist, sessions ended.
	w = h.do("POST", "/api/federation/blocked-domains", map[string]string{"domain": "Chat.Example.Net"}, withCookie(c), withHeader(CSRFHeader, csrf))
	var res federation.BlockResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || w.Code != 200 || res.Domain != "chat.example.net" || res.Revoked != 2 || res.RevokeError != "" {
		t.Fatalf("block: %d %s", w.Code, w.Body)
	}
	if !slices.Equal(res.View.Settings.Block, []string{"chat.example.net"}) || h.fake.Fed.Revoked["chat.example.net"] != 1 {
		t.Fatalf("block list: %v %v", res.View.Settings.Block, h.fake.Fed.Revoked)
	}
	w = h.do("GET", "/api/federation/users", nil, withCookie(c))
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	for _, u := range p.Users {
		if u.DomainBlocked != (u.HomeDomain == "chat.example.net") {
			t.Fatalf("domain_blocked: %+v", u)
		}
	}
	if w := h.do("POST", "/api/federation/blocked-domains", map[string]string{"domain": "example.org"}, withCookie(c), withHeader(CSRFHeader, csrf)); w.Code != 400 {
		t.Fatalf("own domain: %d %s", w.Code, w.Body)
	}
	if w := h.do("POST", "/api/federation/blocked-domains", map[string]string{"domain": "not a domain"}, withCookie(c), withHeader(CSRFHeader, csrf)); w.Code != 400 {
		t.Fatalf("bad domain: %d %s", w.Code, w.Body)
	}
	v := fedView(t, h.do("DELETE", "/api/federation/blocked-domains/chat.example.net", nil, withCookie(c), withHeader(CSRFHeader, csrf)))
	if len(v.Settings.Block) != 0 {
		t.Fatalf("unblock: %v", v.Settings.Block)
	}

	events := map[string]bool{}
	for _, e := range h.fake.Audit {
		events[e.Event] = true
	}
	for _, e := range []string{authapitest.EventFederationBan, authapitest.EventFederationUnban, authapitest.EventFederationRevoke} {
		if !events[e] {
			t.Fatalf("no %s in the audit log: %v", e, events)
		}
	}
}

func TestFederationBlockingTheLastAllowedDomain(t *testing.T) {
	h := newHarness(t)
	h.withConfigFile("version: 1\nauth:\n  federation:\n    mode: allowlist\n    allow: [boson.chat]\n")
	h.fake.EnableFederation(false)
	h.withFederation("")
	c, csrf := h.login()
	w := h.do("POST", "/api/federation/blocked-domains", map[string]string{"domain": "boson.chat"}, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "only domain") {
		t.Fatalf("block the only allowed domain: %d %s", w.Code, w.Body)
	}
}

// Without settings.manage: no settings, no domain blocks; config keys
// refused too.
func TestFederationNeedsSettingsManage(t *testing.T) {
	h := newHarness(t)
	h.withConfigFile("version: 1\n")
	h.fake.EnableFederation(false)
	h.withFederation("")
	h.fake.Roles = append(h.fake.Roles, rolesFixture("rol_ops", "Ops", 300, admin.PermPanel, admin.PermServicesManage))
	h.fake.AddUser("usr_ops", "ops", "user", "ops password")
	h.fake.SetRoles("usr_ops", "rol_ops")
	c, csrf := h.loginAs("ops", "ops password")
	opts := []reqOpt{withCookie(c), withHeader(CSRFHeader, csrf)}
	// Reading is fine (admin.panel), not saving.
	if v := fedView(t, h.do("GET", "/api/federation", nil, opts...)); v.Editable {
		t.Fatal("editable without settings.manage")
	}
	for _, w := range []*httptest.ResponseRecorder{
		h.do("PATCH", "/api/federation", map[string]any{"mode": "open"}, opts...),
		h.do("POST", "/api/federation/blocked-domains", map[string]any{"domain": "boson.chat"}, opts...),
		h.do("PATCH", "/api/config", map[string]any{"set": map[string]any{stackconfig.KeyFederationMode: "open"}}, opts...),
	} {
		if w.Code != 403 || errCode(t, w.Body.String()) != "missing_permission" || !strings.Contains(w.Body.String(), admin.PermSettingsManage) {
			t.Errorf("%d %s", w.Code, w.Body)
		}
	}
	if len(h.fake.Fed.Revoked) != 0 {
		t.Fatal("a refused block ended sessions")
	}
}

// The block wording follows auth's release: before 1.26.0 a domain block
// doesn't drop users already on IRC; unknown = the current behaviour.
func TestFederationBlockWordingFollowsAuthVersion(t *testing.T) {
	h := newHarness(t)
	h.fake.EnableFederation(true)
	c, _ := h.login()
	for _, tc := range []struct {
		version string
		keeps   bool
	}{{"1.25.0", true}, {"v1.26.0", false}, {"", false}} {
		version := tc.version
		h.h.Federation = federation.New(federation.Options{Client: h.fake, Tokens: h.h.Admin, Config: h.h.Config,
			AuthVersion: func(context.Context) (string, error) { return version, nil }})
		v := fedView(t, h.do("GET", "/api/federation", nil, withCookie(c)))
		if v.BlockKeepsIRC != tc.keeps || v.AuthVersion != strings.TrimPrefix(version, "v") {
			t.Errorf("%q: keeps=%v version=%q", version, v.BlockKeepsIRC, v.AuthVersion)
		}
	}
}

// Right after the panel saves the block list, auth still answers from the
// old one: the list shows the saved state, marked applying, until auth
// agrees.
func TestFederationBlockApplying(t *testing.T) {
	h := newHarness(t)
	h.withConfigFile("version: 1\nauth:\n  federation:\n    block: [chat.example.net]\n")
	h.fake.EnableFederation(false)
	h.withFederation("")
	c, csrf := h.login()
	users := func() map[string]authapi.RemoteUser {
		var p authapi.RemoteUserPage
		w := h.do("GET", "/api/federation/users", nil, withCookie(c))
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || w.Code != 200 {
			t.Fatalf("users: %d %s", w.Code, w.Body)
		}
		out := map[string]authapi.RemoteUser{}
		for _, u := range p.Users {
			out[u.ID] = u
		}
		return out
	}
	// auth (the fake) hasn't applied the file's block yet.
	if u := users()["usr_fed_bob"]; !u.DomainBlocked || !u.Applying {
		t.Fatalf("block not applied yet: %+v", u)
	}
	// It has now.
	for i := range h.fake.Fed.Remotes {
		if h.fake.Fed.Remotes[i].HomeDomain == "chat.example.net" {
			h.fake.Fed.Remotes[i].DomainBlocked = true
		}
	}
	if u := users()["usr_fed_bob"]; !u.DomainBlocked || u.Applying {
		t.Fatalf("applied: %+v", u)
	}
	// Unblock: shown unblocked at once, applying until auth re-reads.
	fedView(t, h.do("DELETE", "/api/federation/blocked-domains/chat.example.net", nil, withCookie(c), withHeader(CSRFHeader, csrf)))
	if u := users()["usr_fed_bob"]; u.DomainBlocked || !u.Applying {
		t.Fatalf("unblock not applied yet: %+v", u)
	}
	if u := users()["usr_fed_alice"]; u.DomainBlocked || u.Applying {
		t.Fatalf("other domain: %+v", u)
	}
}
