package authapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
)

// An auth release without the new endpoints: a real ServeMux with only the
// v1.2 admin routes, so unknown paths get Go's plain-text 404 and known
// paths with another method get 405.
func TestOlderAuthIsUnsupported(t *testing.T) {
	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"users":[],"total":0}`)) }
	mux.HandleFunc("GET /v1/admin/users", ok)
	mux.HandleFunc("GET /v1/admin/users/{id}", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"user":{"id":"usr_1","username":"a","role":"user"}}`))
	})
	mux.HandleFunc("GET /v1/admin/audit", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("user_id") != "usr_1" {
			t.Errorf("legacy actor filter not sent as user_id: %v", r.URL.Query())
		}
		_, _ = w.Write([]byte(`{"entries":[]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := NewAuthClient(srv.URL, time.Second, nil)
	ctx := context.Background()

	_, err := c.GetSettings(ctx, "t")
	if !apperr.IsUnsupported(err) || !strings.Contains(err.Error(), "auth ≥ "+RequiresSettings) {
		t.Fatalf("settings: %v", err)
	}
	if _, err := c.ListSSOProviders(ctx, "t"); !apperr.IsUnsupported(err) {
		t.Fatalf("sso: %v", err)
	}
	if _, err := c.CreateUser(ctx, "t", CreateUser{Username: "x"}); !apperr.IsUnsupported(err) { // 405
		t.Fatalf("create user: %v", err)
	}
	if err := c.SendPasswordReset(ctx, "t", "usr_1"); !apperr.IsUnsupported(err) {
		t.Fatalf("reset: %v", err)
	}
	u, err := c.GetUser(ctx, "t", "usr_1")
	if err != nil || u.Identities != nil {
		t.Fatalf("identities should be unknown on old releases: %v %+v", err, u)
	}
	p, err := c.QueryAudit(ctx, "t", AuditQuery{Actor: "usr_1", Event: "login"})
	if err != nil || p.Supported {
		t.Fatalf("audit: %v %+v", err, p)
	}
}

func TestV2WireFormat(t *testing.T) {
	var bodies = map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies[r.Method+" "+r.URL.Path] = string(b)
		switch r.Method + " " + r.URL.Path {
		case "PATCH /v1/admin/settings":
			_, _ = w.Write([]byte(`{"registration":"closed","require_email_verification":true,"channel_creation":"admins","voice_channel_creation":"founders","updated_at":"2026-11-13T10:00:00Z","updated_by":"usr_1"}`))
		case "GET /v1/admin/sso/providers":
			_, _ = w.Write([]byte(`{"providers":[{"id":"idp_1","slug":"google","kind":"oidc","display_name":"Google","enabled":true,"config":{"oidc":{"issuer":"https://accounts.google.com","client_id":"cid","has_client_secret":true,"scopes":["openid","email"]}},"callback_url":"https://auth.example.com/v1/sso/google/callback"}]}`))
		case "POST /v1/admin/sso/providers/idp_1/test":
			_, _ = w.Write([]byte(`{"ok":false,"details":"issuer unreachable"}`))
		case "GET /v1/admin/users/usr_1":
			_, _ = w.Write([]byte(`{"user":{"id":"usr_1","username":"a","role":"user"},"identities":[{"id":"uid_1","provider_slug":"google","subject":"123"}]}`))
		case "GET /v1/admin/audit":
			q := r.URL.Query()
			if q.Get("actor") != "ana" || q.Get("target") != "usr_9" || q.Get("cursor") != "c1" || q.Get("since") != "2026-11-01T00:00:00Z" || q.Has("user_id") {
				t.Errorf("audit query %v", q)
			}
			_, _ = w.Write([]byte(`{"entries":[{"id":"aud_2","user_id":"usr_1","event":"login","actor_username":"ana","at":"2026-11-02T00:00:00Z"}],"next_cursor":"c2"}`))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":"not_found","message":"no such provider"}`))
		}
	}))
	defer srv.Close()
	c := NewAuthClient(srv.URL, time.Second, nil)
	ctx := context.Background()

	reg := "closed"
	s, err := c.UpdateSettings(ctx, "t", SettingsPatch{Registration: &reg})
	if err != nil || s.VoiceChannelCreation != "founders" || s.UpdatedBy == nil || *s.UpdatedBy != "usr_1" {
		t.Fatalf("settings: %v %+v", err, s)
	}
	if got := bodies["PATCH /v1/admin/settings"]; got != `{"registration":"closed"}` {
		t.Fatalf("patch body %s", got)
	}
	list, err := c.ListSSOProviders(ctx, "t")
	if err != nil || len(list) != 1 || !list[0].Config.OIDC.HasClientSecret || list[0].Config.OIDC.ClientSecret != "" {
		t.Fatalf("providers: %v %+v", err, list)
	}
	res, err := c.TestSSOProvider(ctx, "t", "idp_1")
	if err != nil || res.OK || string(res.Details) != `"issuer unreachable"` {
		t.Fatalf("test: %v %+v", err, res)
	}
	// A JSON 404 is a real "not found", not a missing endpoint.
	_, err = c.GetSSOProvider(ctx, "t", "idp_x")
	if e, ok := apperr.As(err); !ok || e.Code != "not_found" {
		t.Fatalf("json 404: %v", err)
	}
	u, err := c.GetUser(ctx, "t", "usr_1")
	if err != nil || u.Identities == nil || len(*u.Identities) != 1 {
		t.Fatalf("identities: %v %+v", err, u)
	}
	p, err := c.QueryAudit(ctx, "t", AuditQuery{Actor: "ana", Target: "usr_9", Cursor: "c1", Since: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil || !p.Supported || p.NextCursor != "c2" || p.Entries[0].ActorUsername != "ana" {
		t.Fatalf("audit: %v %+v", err, p)
	}
	var secret struct {
		Config ProviderSettings `json:"config"`
	}
	in := SSOProviderInput{Config: &ProviderSettings{OIDC: &OIDCConfig{Issuer: "i", ClientID: "c", ClientSecret: "s"}}}
	b, _ := json.Marshal(in)
	_ = json.Unmarshal(b, &secret)
	if secret.Config.OIDC.ClientSecret != "s" {
		t.Fatalf("client_secret not sent on write: %s", b)
	}
}
