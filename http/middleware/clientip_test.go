package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestTrustedClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("172.16.0.0/12")}
	var got string
	h := TrustedClientIP(trusted)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = ClientIP(r.Context()) }))
	cases := []struct{ peer, xff, want string }{
		{"203.0.113.9:5000", "1.2.3.4", "203.0.113.9"},             // untrusted peer: XFF ignored
		{"172.18.0.2:5000", "1.2.3.4", "1.2.3.4"},                  // via Caddy
		{"172.18.0.2:5000", "6.6.6.6, 1.2.3.4", "1.2.3.4"},         // spoofed left hop ignored
		{"172.18.0.2:5000", "1.2.3.4, 172.18.0.5", "1.2.3.4"},      // trusted hops skipped
		{"172.18.0.2:5000", "", "172.18.0.2"},                      // no header
		{"[::ffff:172.18.0.2]:5000", "2001:db8::1", "2001:db8::1"}, // mapped peer
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.peer
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
		if got != c.want {
			t.Errorf("peer %s xff %q: got %s, want %s", c.peer, c.xff, got, c.want)
		}
	}
}
