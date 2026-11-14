package authapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
)

// The real auth service's wire format, checked against a stub.
func TestClientWireFormat(t *testing.T) {
	var gotAuth, gotXFF, gotUA, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotXFF, gotUA, gotCT = r.Header.Get("Authorization"), r.Header.Get("X-Forwarded-For"), r.UserAgent(), r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/login":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["login"] != "root" || body["cookie"] != nil {
				t.Errorf("login body %v", body)
			}
			_, _ = w.Write([]byte(`{"token_type":"Bearer","access_token":"a","expires_in":900,"refresh_token":"r","refresh_expires_in":100,"session_id":"ses_1","user":{"id":"usr_1","username":"root","role":"admin","email":null,"created_at":"2026-01-01T00:00:00Z"}}`))
		case "GET /v1/admin/users":
			if r.URL.Query().Get("q") != "bo b" || r.URL.Query().Get("limit") != "5" {
				t.Errorf("query %v", r.URL.Query())
			}
			_, _ = w.Write([]byte(`{"users":[{"id":"usr_2","username":"bob","role":"user","email":"b@x","email_verified":true,"created_at":"2026-01-01T00:00:00Z","disabled_at":"2026-02-01T00:00:00Z"}],"total":1}`))
		case "POST /v1/admin/users/usr_2/disable":
			_, _ = w.Write([]byte(`{"user":{"id":"usr_2","username":"bob","role":"user"}}`))
		case "POST /v1/admin/invites":
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"invite":{"id":"inv_1","code":"abc","max_uses":1,"uses":0,"note":"","created_by":"usr_1","expires_at":null,"created_at":"2026-01-01T00:00:00Z"}}`))
		case "DELETE /v1/admin/invites/inv_1":
			w.WriteHeader(204)
		case "GET /v1/admin/audit":
			_, _ = w.Write([]byte(`{"entries":[{"id":"aud_1","user_id":null,"event":"signing_key_created","ip":"","meta":{"kid":"key_1"},"at":"2026-01-01T00:00:00Z"}]}`))
		case "GET /v1/admin/users/missing":
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":"not_found","message":"not found"}`))
		case "POST /v1/token/refresh":
			w.WriteHeader(429)
			w.Header().Set("Retry-After", "30")
			_, _ = w.Write([]byte(`{"error":"rate_limited","message":"slow down"}`))
		default:
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"error":"internal_error","message":"db password is hunter2"}`))
		}
	}))
	defer srv.Close()

	var ops []string
	c := NewAuthClient(srv.URL, time.Second, func(op string, _ int, _ error, _ time.Duration) { ops = append(ops, op) })
	ctx := WithMeta(context.Background(), ClientMeta{IP: "203.0.113.7", UserAgent: "Firefox"})

	tok, err := c.Login(ctx, "root", "pw")
	if err != nil || tok.AccessToken != "a" || tok.User.Role != "admin" {
		t.Fatalf("login: %v %+v", err, tok)
	}
	if gotXFF != "203.0.113.7" || gotUA != "Firefox" || gotCT != "application/json" || gotAuth != "" {
		t.Fatalf("headers xff=%q ua=%q ct=%q auth=%q", gotXFF, gotUA, gotCT, gotAuth)
	}
	list, err := c.ListUsers(ctx, "tok", "bo b", 5, 0)
	if err != nil || list.Total != 1 || list.Users[0].DisabledAt == nil || *list.Users[0].Email != "b@x" {
		t.Fatalf("list: %v %+v", err, list)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("auth header %q", gotAuth)
	}
	if _, err := c.SetDisabled(ctx, "tok", "usr_2", true); err != nil {
		t.Fatal(err)
	}
	inv, err := c.CreateInvite(ctx, "tok", CreateInvite{MaxUses: 1})
	if err != nil || inv.Code != "abc" {
		t.Fatalf("invite: %v %+v", err, inv)
	}
	if err := c.RevokeInvite(ctx, "tok", "inv_1"); err != nil {
		t.Fatal(err)
	}
	page, err := c.QueryAudit(ctx, "tok", AuditQuery{Limit: 10})
	if err != nil || page.Supported || len(page.Entries) != 1 || page.Entries[0].UserID != nil || string(page.Entries[0].Meta) != `{"kid":"key_1"}` {
		t.Fatalf("audit: %v %+v", err, page)
	}
	_, err = c.GetUser(ctx, "tok", "missing")
	if e, ok := apperr.As(err); !ok || e.Status != 404 || e.Code != "not_found" {
		t.Fatalf("404: %v", err)
	}
	_, err = c.Refresh(ctx, "r")
	if e, ok := apperr.As(err); !ok || e.Status != 429 {
		t.Fatalf("429: %v", err)
	}
	_, err = c.Me(ctx, "tok") // default branch: 500
	if e, ok := apperr.As(err); !ok || e.Status != 502 || e.Message == "db password is hunter2" {
		t.Fatalf("5xx must be hidden: %v", err)
	}
	if len(ops) != 9 || ops[0] != "login" {
		t.Fatalf("observer ops: %v", ops)
	}
}

func TestClientUnreachable(t *testing.T) {
	c := NewAuthClient("http://127.0.0.1:1", 200*time.Millisecond, nil)
	_, err := c.Me(context.Background(), "x")
	if e, ok := apperr.As(err); !ok || e != apperr.ErrUpstream {
		t.Fatalf("got %v", err)
	}
	if IsUnauthorized(err) {
		t.Fatal("transport error treated as 401")
	}
}
