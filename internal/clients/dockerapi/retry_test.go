package dockerapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
)

// flakyDial fails the first n dials with err, then dials for real.
func flakyDial(n int32, fail func() error) (*http.Transport, *atomic.Int32) {
	var dials atomic.Int32
	d := &net.Dialer{Timeout: time.Second}
	tr := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		if dials.Add(1) <= n {
			return nil, &net.OpError{Op: "dial", Net: network, Err: fail()}
		}
		return d.DialContext(ctx, network, addr)
	}}
	return tr, &dials
}

// The DNS failure of 2026-11-28 (boson.chat): Docker's embedded DNS has no
// record for docker-proxy while compose replaces it.
func dnsMisbehaving() error {
	return &net.DNSError{Err: "server misbehaving", Name: "docker-proxy", Server: "127.0.0.11:53"}
}

func refused() error { return os.NewSyscallError("connect", syscall.ECONNREFUSED) }

func retrying(url string, tr http.RoundTripper, window time.Duration) *Client {
	return &Client{BaseURL: url, HTTP: &http.Client{Timeout: 2 * time.Second, Transport: tr}, MaxLogBytes: 1 << 20,
		RetryWindow: window, RetryBackoff: 5 * time.Millisecond}
}

func listServer(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/containers/json":
			_, _ = w.Write([]byte(`[{"Id":"a","Names":["/boson-auth-1"],"Labels":{"com.docker.compose.project":"boson"}}]`))
		case "/_ping":
			_, _ = w.Write([]byte("OK"))
		case "/containers/create":
			_, _ = w.Write([]byte(`{"Id":"job1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRetryDNSAndRefusedThenWorks(t *testing.T) {
	for name, fail := range map[string]func() error{"dns": dnsMisbehaving, "refused": refused} {
		t.Run(name, func(t *testing.T) {
			var hits atomic.Int32
			srv := listServer(t, &hits)
			tr, dials := flakyDial(4, fail)
			c := retrying(srv.URL, tr, 5*time.Second)
			cs, err := c.List(context.Background(), "boson")
			if err != nil || len(cs) != 1 {
				t.Fatalf("list: %v %+v", err, cs)
			}
			if dials.Load() != 5 || hits.Load() != 1 {
				t.Fatalf("dials %d hits %d, want 5 tries and 1 answer", dials.Load(), hits.Load())
			}
			if err := c.Ping(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A create that never reached the proxy (resolving or dialling failed) is
// safe to send again.
func TestRetryCreateOnlyWhenNotSent(t *testing.T) {
	var hits atomic.Int32
	srv := listServer(t, &hits)
	tr, _ := flakyDial(2, dnsMisbehaving)
	c := retrying(srv.URL, tr, 5*time.Second)
	if id, err := c.Create(context.Background(), JobSpec{Name: "j", Image: "docker:cli"}); err != nil || id != "job1" {
		t.Fatalf("create after a DNS failure: %q %v", id, err)
	}

	// The connection drops after the request went out: the proxy may have
	// created it, so the client doesn't send it twice (the helper cleans up
	// and tries again itself).
	var creates atomic.Int32
	eof := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers/create" {
			creates.Add(1)
		}
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
	}))
	defer eof.Close()
	c = retrying(eof.URL, &http.Transport{DisableKeepAlives: true}, 300*time.Millisecond)
	_, err := c.Create(context.Background(), JobSpec{Name: "j", Image: "docker:cli"})
	if !errors.Is(err, ErrUnavailable) || !IsTransient(err) || creates.Load() != 1 {
		t.Fatalf("create on EOF: %v (transient %v), %d sent", err, IsTransient(err), creates.Load())
	}
	// A GET on the same server is retried until the window closes.
	start := time.Now()
	_, err = c.List(context.Background(), "boson")
	if !errors.Is(err, ErrUnavailable) || time.Since(start) < 250*time.Millisecond {
		t.Fatalf("list on EOF: %v after %s", err, time.Since(start))
	}
}

func TestRetryEOFThenWorks(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) <= 3 {
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
			return
		}
		_, _ = w.Write([]byte(`{"Id":"x","State":{"Status":"running","Running":true}}`))
	}))
	defer srv.Close()
	c := retrying(srv.URL, &http.Transport{DisableKeepAlives: true}, 5*time.Second)
	in, err := c.Inspect(context.Background(), "x")
	if err != nil || !in.State.Running || n.Load() != 4 {
		t.Fatalf("inspect: %+v %v after %d requests", in, err, n.Load())
	}
}

// An answer is an answer: a 403 (the proxy's allowlist), a 404 or a 500
// is never sent again.
func TestRetryNeverOnAnAnswer(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError} {
		var n atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n.Add(1)
			w.WriteHeader(status)
		}))
		c := retrying(srv.URL, nil, 5*time.Second)
		_, err := c.List(context.Background(), "boson")
		_, cerr := c.Create(context.Background(), JobSpec{Name: "j"})
		srv.Close()
		if err == nil || cerr == nil || n.Load() != 2 {
			t.Fatalf("%d: %v / %v after %d requests, want one each", status, err, cerr, n.Load())
		}
		if errors.Is(err, ErrUnavailable) || IsTransient(err) {
			t.Fatalf("%d is an answer, not unavailable: %v", status, err)
		}
		if status == http.StatusForbidden {
			if e, ok := apperr.As(err); !ok || e.Code != "docker_forbidden" {
				t.Fatalf("403: %v", err)
			}
		}
	}
}

// Down past the window: unavailable, with the cause, after about the window.
func TestRetryGivesUpAfterTheWindow(t *testing.T) {
	tr, dials := flakyDial(1<<30, dnsMisbehaving)
	c := retrying("http://docker-proxy:2375", tr, 200*time.Millisecond)
	start := time.Now()
	_, err := c.List(context.Background(), "boson")
	took := time.Since(start)
	if !errors.Is(err, ErrUnavailable) || !IsTransient(err) || !strings.Contains(err.Error(), "server misbehaving") {
		t.Fatalf("err %v", err)
	}
	if took < 200*time.Millisecond || took > 2*time.Second || dials.Load() < 3 {
		t.Fatalf("took %s, %d tries", took, dials.Load())
	}
	if e, ok := apperr.As(err); !ok || e.Code != "docker_unavailable" {
		t.Fatalf("apperr: %v", err)
	}

	// Without a window: one try.
	tr, dials = flakyDial(1<<30, refused)
	c = retrying("http://docker-proxy:2375", tr, 0)
	if _, err := c.List(context.Background(), "boson"); !errors.Is(err, ErrUnavailable) || dials.Load() != 1 {
		t.Fatalf("no retry: %v, %d tries", err, dials.Load())
	}

	// A cancelled context stops the retries.
	tr, _ = flakyDial(1<<30, refused)
	c = retrying("http://docker-proxy:2375", tr, time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start = time.Now()
	if _, err := c.List(ctx, "boson"); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("cancelled: %v after %s", err, time.Since(start))
	}
}

func TestIsTransient(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&net.OpError{Op: "dial", Err: dnsMisbehaving()}, true},
		{&net.OpError{Op: "dial", Err: refused()}, true},
		{&net.OpError{Op: "read", Err: os.NewSyscallError("read", syscall.ECONNRESET)}, true},
		{errors.New("x: " + "EOF"), false},
		{context.DeadlineExceeded, false},
		{apperr.New(502, "docker_forbidden", "refused"), false},
		{nil, false},
	} {
		if got := IsTransient(tc.err); got != tc.want {
			t.Errorf("IsTransient(%v) = %v", tc.err, got)
		}
	}
}
