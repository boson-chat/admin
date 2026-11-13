package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/down":
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"no signing key"}`))
		case "/redirect":
			http.Redirect(w, r, "https://elsewhere.example/", http.StatusFound)
		case "/slow":
			time.Sleep(300 * time.Millisecond)
		}
	}))
	defer srv.Close()
	h := NewHealthChecker(100 * time.Millisecond)
	ctx := context.Background()
	if r := h.Check(ctx, srv.URL+"/ok"); !r.OK || r.StatusCode != 200 {
		t.Fatalf("ok: %+v", r)
	}
	if r := h.Check(ctx, srv.URL+"/down"); r.OK || r.StatusCode != 503 || r.Detail != `{"status":"no signing key"}` {
		t.Fatalf("down: %+v", r)
	}
	if r := h.Check(ctx, srv.URL+"/redirect"); !r.OK || r.StatusCode != 302 {
		t.Fatalf("redirects are not followed and count as up: %+v", r)
	}
	if r := h.Check(ctx, srv.URL+"/slow"); r.OK || r.Detail != "timed out" {
		t.Fatalf("slow: %+v", r)
	}
	if r := h.Check(ctx, "http://127.0.0.1:1/healthz"); r.OK || r.Detail == "" {
		t.Fatalf("refused: %+v", r)
	}
}

func TestVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			_, _ = w.Write([]byte("1.1.0\n"))
		case "/json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"2.3.4"}`))
		case "/metrics":
			w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
			_, _ = w.Write([]byte("# HELP target_info Target metadata\n# TYPE target_info gauge\n" +
				`target_info{deployment_environment="production",service_name="auth",service_version="1.4.2"} 1` + "\n" +
				"go_goroutines 12\n"))
		case "/nometa":
			_, _ = w.Write([]byte("go_goroutines 12\n"))
		}
	}))
	defer srv.Close()
	h := NewHealthChecker(time.Second)
	ctx := context.Background()
	for path, want := range map[string]string{"/version": "1.1.0", "/json": "2.3.4", "/metrics": "1.4.2"} {
		got, err := h.Version(ctx, srv.URL+path)
		if err != nil || got != want {
			t.Fatalf("%s: got %q %v, want %q", path, got, err, want)
		}
	}
	if _, err := h.Version(ctx, srv.URL+"/nometa/metrics"); err == nil {
		t.Fatal("want an error without target_info")
	}
}
