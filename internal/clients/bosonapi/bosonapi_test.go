package bosonapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
)

func TestProfiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte("invalid token"))
			return
		}
		switch r.URL.Path {
		case "/v1/admin/profiles/usr_1":
			_, _ = w.Write([]byte(`{"profile":{"user_id":"u-1","auth_user_id":"usr_1","handle":"ana","avatar_url":"https://cdn.boson.chat/avatars/u-1-abc.png"},"servers":[{"id":"s1","hostname":"irc.ana.org","port":6697,"tls":true,"name":"Ana","verification_status":"verified"}]}`))
		case "/v1/admin/profiles/usr_2":
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":"profile_not_found"}`))
		default:
			http.NotFound(w, r) // plain text: endpoint missing
		}
	}))
	defer srv.Close()
	c := NewProfileClient(srv.URL, time.Second, nil)
	ctx := context.Background()
	p, err := c.AdminProfile(ctx, "at", "usr_1")
	if err != nil || p.Profile.Handle != "ana" || len(p.Servers) != 1 {
		t.Fatalf("profile: %v %+v", err, p)
	}
	p, err = c.AdminProfile(ctx, "at", "usr_2")
	if err != nil || p.Profile != nil {
		t.Fatalf("no profile yet: %v %+v", err, p)
	}
	if _, err := c.AdminProfile(ctx, "at", "usr_3"); !apperr.IsUnsupported(err) {
		t.Fatalf("missing endpoint: %v", err)
	}
	_, err = c.AdminProfile(ctx, "nope", "usr_1")
	if e, ok := apperr.As(err); !ok || e.Status != 401 || e.Message != "the boson API: invalid token" {
		t.Fatalf("401: %v", err)
	}
}
