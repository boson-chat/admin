package ircstats

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func serve(t *testing.T, method, path, token string, status int, body any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method || r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func eq(p *int, want int) bool { return p != nil && *p == want }

func TestErgo(t *testing.T) {
	srv := serve(t, "POST", "/v1/status", "ergo-tok", 200, map[string]any{
		"success": true, "users": map[string]any{"total": 17, "max": 40}, "channels": 9, "registered_channels": 6})
	c, err := NewErgo(srv.URL, "ergo-tok", time.Second).Counts(context.Background())
	if err != nil || !eq(c.Users, 17) || !eq(c.Channels, 6) {
		t.Fatalf("counts %+v %v", c, err)
	}
	// An Ergo without the stack's patch: no registered channel count (the
	// active channels aren't registered channels).
	old := serve(t, "POST", "/v1/status", "ergo-tok", 200, map[string]any{"success": true, "users": map[string]any{"total": 3}, "channels": 9})
	if c, err := NewErgo(old.URL, "ergo-tok", time.Second).Counts(context.Background()); err != nil || !eq(c.Users, 3) || c.Channels != nil {
		t.Fatalf("unpatched %+v %v", c, err)
	}
	if _, err := NewErgo(srv.URL, "wrong", time.Second).Counts(context.Background()); err == nil {
		t.Fatal("wrong token accepted")
	}
	bad := serve(t, "POST", "/v1/status", "ergo-tok", 200, map[string]any{"success": false})
	if _, err := NewErgo(bad.URL, "ergo-tok", time.Second).Counts(context.Background()); err == nil {
		t.Fatal("success: false accepted")
	}
}

func TestBridge(t *testing.T) {
	srv := serve(t, "GET", "/api/managed/stats", "bridge-tok", 200, map[string]any{"users": 120, "channels": 33, "at": "2026-11-27T10:00:00Z"})
	c, err := NewBridge(srv.URL, "bridge-tok", time.Second).Counts(context.Background())
	if err != nil || !eq(c.Users, 120) || !eq(c.Channels, 33) {
		t.Fatalf("counts %+v %v", c, err)
	}
	unheard := serve(t, "GET", "/api/managed/stats", "bridge-tok", 200, map[string]any{"users": nil, "channels": -1, "at": nil})
	if c, err := NewBridge(unheard.URL, "bridge-tok", time.Second).Counts(context.Background()); err != nil || c.Users != nil || c.Channels != nil {
		t.Fatalf("unheard %+v %v", c, err)
	}
	// An evrd without the endpoint.
	old := serve(t, "GET", "/api/managed/health", "bridge-tok", 200, map[string]any{})
	if _, err := NewBridge(old.URL, "bridge-tok", time.Second).Counts(context.Background()); err == nil {
		t.Fatal("404 accepted")
	}
	if _, err := NewBridge("http://127.0.0.1:1", "", time.Second).Counts(context.Background()); err == nil {
		t.Fatal("unreachable accepted")
	}
}
