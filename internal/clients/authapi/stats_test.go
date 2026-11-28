package authapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
)

func TestStatsClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/stats" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Internal-Token") != "tok" || r.Header.Get("Authorization") != "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"accounts":12,"active_7d":3,"active_30d":7}`))
	}))
	defer srv.Close()
	st, err := NewStatsClient(srv.URL, "tok", time.Second).Stats(context.Background())
	if err != nil || *st != (Stats{Accounts: 12, Active7d: 3, Active30d: 7}) {
		t.Fatalf("stats %+v %v", st, err)
	}
	if _, err := NewStatsClient(srv.URL, "nope", time.Second).Stats(context.Background()); err == nil || apperr.IsUnsupported(err) {
		t.Fatalf("wrong token: %v", err)
	}
	// An auth without the endpoint.
	old := httptest.NewServer(http.NotFoundHandler())
	defer old.Close()
	if _, err := NewStatsClient(old.URL, "tok", time.Second).Stats(context.Background()); !apperr.IsUnsupported(err) {
		t.Fatalf("older auth: %v", err)
	}
}
