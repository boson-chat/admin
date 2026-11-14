package bosonapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
)

func TestNickClaimClient(t *testing.T) {
	var got struct {
		actor string
		body  map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Token") != "tok" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /internal/nick-claims/status":
			_, _ = w.Write([]byte(`{"enabled":true,"email_domain":"nick.example.com","inbound":{"host":"pop.example.com","port":995,"tls":true,"user":"reg@nick.example.com","password_set":true},"worker":{"running":true,"last_drain_at":"2026-11-14T10:00:00Z","last_ok_at":"2026-11-14T10:00:00Z","last_error":"","consecutive_failures":0},"pending_claims":3}`))
		case "POST /internal/nick-claims/test":
			got.actor = r.Header.Get("X-Acting-User")
			_ = json.NewDecoder(r.Body).Decode(&got.body)
			_, _ = w.Write([]byte(`{"ok":true,"message":"Connected","messages":2}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	c := NewNickClaimClient(srv.URL, "tok", time.Second, nil)
	st, err := c.Status(ctx)
	if err != nil || !st.Enabled || st.Inbound.Host != "pop.example.com" || st.PendingClaims != 3 || !st.Worker.Running ||
		st.Worker.LastOKAt == nil || st.Inbound.PasswordSet == nil || !*st.Inbound.PasswordSet {
		t.Fatalf("status: %v %+v", err, st)
	}
	host, port := "other.example.com", 110
	res, err := c.Test(ctx, "root", NickClaimTestRequest{Inbound: &NickClaimTestInbound{Host: &host, Port: &port}})
	if err != nil || !res.OK || res.Messages != 2 {
		t.Fatalf("test: %v %+v", err, res)
	}
	in, _ := got.body["inbound"].(map[string]any)
	if got.actor != "root" || in["host"] != "other.example.com" || in["port"] != float64(110) {
		t.Fatalf("test request: %+v", got)
	}
	if _, has := in["tls"]; has {
		t.Fatalf("unset fields must be left out: %v", in)
	}

	// A wrong token says which variables to match.
	_, err = NewNickClaimClient(srv.URL, "wrong", time.Second, nil).Status(ctx)
	if e, ok := apperr.As(err); !ok || e.Code != "nick_claim_api_token" || !strings.Contains(e.Message, "BOSON_API_INTERNAL_TOKEN") {
		t.Fatalf("wrong token: %v", err)
	}
}

func TestNickClaimClientErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/nick-claims/status":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"error":"internal_api_disabled"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	ctx := context.Background()
	c := NewNickClaimClient(srv.URL, "", time.Second, nil)
	_, err := c.Status(ctx)
	if e, ok := apperr.As(err); !ok || e.Code != "nick_claim_api_token" || e.Status != 503 || !strings.Contains(e.Message, "API_INTERNAL_TOKEN") {
		t.Fatalf("token off: %v", err)
	}
	// An older api without the endpoint.
	if _, err := c.Test(ctx, "", NickClaimTestRequest{}); !apperr.IsUnsupported(err) {
		t.Fatalf("missing endpoint: %v", err)
	}
	srv.Close()
	_, err = c.Status(ctx)
	if e, ok := apperr.As(err); !ok || e.Code != "nick_claim_api_unreachable" {
		t.Fatalf("unreachable: %v", err)
	}
}
