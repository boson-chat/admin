package authapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
)

func TestFederationWireFormat(t *testing.T) {
	var got []string
	mux := http.NewServeMux()
	record := func(r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.RequestURI()+" "+strings.TrimSpace(string(b)))
	}
	mux.HandleFunc("GET /v1/admin/federation", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_, _ = w.Write([]byte(`{"mode":"directory","allow":[],"block":["spam.example"],"directory_url":"https://directory.boson.chat",
			"remote_upload_quota":"","remote_voice":true,"sources":{"mode":"env"},"editable":true,"issuer":"https://auth.example.org","domain":"example.org","remote_users":4,"remote_role":"Remote","updated_at":null,"updated_by":null}`))
	})
	mux.HandleFunc("PATCH /v1/admin/federation", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_, _ = w.Write([]byte(`{"mode":"open"}`))
	})
	mux.HandleFunc("GET /v1/admin/federation/users", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_, _ = w.Write([]byte(`{"users":[{"id":"usr_1","identity":"alice@boson.chat","handle":"alice","home_domain":"boson.chat","home_username":"alice","first_seen_at":"2026-11-20T00:00:00Z","last_login_at":null,"disabled":false}],"next_cursor":"c2","total":9}`))
	})
	mux.HandleFunc("POST /v1/admin/federation/users/{id}/ban", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_, _ = w.Write([]byte(`{"id":"usr_1","identity":"alice@boson.chat","banned":true}`))
	})
	mux.HandleFunc("DELETE /v1/admin/federation/users/{id}/ban", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_, _ = w.Write([]byte(`{"id":"usr_1","banned":false}`))
	})
	mux.HandleFunc("POST /v1/admin/federation/domains/{domain}/revoke-sessions", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_, _ = w.Write([]byte(`{"revoked":3}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := NewAuthClient(srv.URL, time.Second, nil).(*AuthClient)
	ctx := context.Background()

	st, err := c.FederationStatus(ctx, "t")
	if err != nil || st.Mode != FederationDirectory || st.Block[0] != "spam.example" || !st.Editable || st.RemoteUsers != 4 || st.Sources["mode"] != "env" || !st.RemoteVoice || st.RemoteRole != "Remote" || st.UpdatedAt != nil {
		t.Fatalf("status: %+v %v", st, err)
	}
	mode := FederationOpen
	if st, err := c.UpdateFederation(ctx, "t", FederationPatch{Mode: &mode, Reset: []string{"remote_voice", "mode"}}); err != nil || st.Mode != "open" {
		t.Fatalf("update: %+v %v", st, err)
	}
	p, err := c.RemoteUsers(ctx, "t", RemoteUserQuery{Q: "ali", Domain: "boson.chat", Cursor: "c1", Limit: 20})
	if err != nil || len(p.Users) != 1 || p.Users[0].Identity != "alice@boson.chat" || p.NextCursor != "c2" || p.Total != 9 {
		t.Fatalf("users: %+v %v", p, err)
	}
	if u, err := c.BanRemoteUser(ctx, "t", "usr_1", "spam"); err != nil || !u.Banned {
		t.Fatalf("ban: %+v %v", u, err)
	}
	if u, err := c.UnbanRemoteUser(ctx, "t", "usr_1"); err != nil || u.Banned {
		t.Fatalf("unban: %+v %v", u, err)
	}
	if r, err := c.RevokeDomainSessions(ctx, "t", "boson.chat"); err != nil || r.Revoked != 3 || r.Domain != "boson.chat" {
		t.Fatalf("revoke: %+v %v", r, err)
	}
	want := []string{
		"GET /v1/admin/federation ",
		`PATCH /v1/admin/federation {"mode":"open","remote_voice":null}`,
		"GET /v1/admin/federation/users?cursor=c1&domain=boson.chat&limit=20&q=ali ",
		`POST /v1/admin/federation/users/usr_1/ban {"reason":"spam"}`,
		"DELETE /v1/admin/federation/users/usr_1/ban ",
		"POST /v1/admin/federation/domains/boson.chat/revoke-sessions ",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// An auth without the federation API: every call says which release it needs.
func TestFederationOnOlderAuth(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	c := NewAuthClient(srv.URL, time.Second, nil).(*AuthClient)
	_, err := c.FederationStatus(context.Background(), "t")
	if !apperr.IsUnsupported(err) || !strings.Contains(err.Error(), RequiresFederation) {
		t.Fatalf("want unsupported: %v", err)
	}
	if _, err := c.RemoteUsers(context.Background(), "t", RemoteUserQuery{}); !apperr.IsUnsupported(err) {
		t.Fatalf("users: %v", err)
	}
}

func TestRemoteUserPageNullCursor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"users":[],"next_cursor":null,"total":0}`))
	}))
	defer srv.Close()
	p, err := NewAuthClient(srv.URL, time.Second, nil).(*AuthClient).RemoteUsers(context.Background(), "t", RemoteUserQuery{})
	if err != nil || p.NextCursor != "" || p.Users == nil {
		t.Fatalf("%+v %v", p, err)
	}
}
