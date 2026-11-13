package tlsprobe

import (
	"context"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProbe(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "https://")
	leaf := srv.Certificate()

	p := &TLSProber{Now: time.Now}
	c, err := p.Probe(context.Background(), "example.com", addr)
	if err != nil {
		t.Fatal(err)
	}
	if c.Trusted || !strings.Contains(c.VerifyError, "unknown authority") {
		t.Fatalf("self-signed cert reported trusted: %+v", c)
	}
	if !c.NotAfter.Equal(leaf.NotAfter) {
		t.Fatalf("not_after %v, want %v", c.NotAfter, leaf.NotAfter)
	}

	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	p.Roots = roots
	if c, err = p.Probe(context.Background(), "example.com", addr); err != nil || !c.Trusted {
		t.Fatalf("with its root: trusted=%v err=%v verify=%q", c != nil && c.Trusted, err, c.VerifyError)
	}
	if c, _ = p.Probe(context.Background(), "wrong.invalid", addr); c.Trusted || !strings.Contains(c.VerifyError, "hostname") {
		t.Fatalf("wrong name: %+v", c)
	}
	// Expired by the clock.
	p.Now = func() time.Time { return leaf.NotAfter.Add(time.Hour) }
	if c, _ = p.Probe(context.Background(), "example.com", addr); c.Trusted || !strings.Contains(c.VerifyError, "expired") {
		t.Fatalf("expired: %+v", c)
	}
}

func TestProbeUnreachable(t *testing.T) {
	p := NewTLSProber(200 * time.Millisecond)
	if _, err := p.Probe(context.Background(), "localhost", "127.0.0.1:1"); err == nil {
		t.Fatal("want error")
	}
}
