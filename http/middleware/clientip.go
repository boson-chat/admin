// Package middleware holds the panel's HTTP middleware.
package middleware

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type clientIPKey struct{}

// ClientIP returns the client address computed by TrustedClientIP.
func ClientIP(ctx context.Context) string {
	s, _ := ctx.Value(clientIPKey{}).(string)
	return s
}

// TrustedClientIP stores the client's IP in the context. X-Forwarded-For is
// honoured only when the peer is a trusted proxy, and then the rightmost
// address that isn't a trusted proxy wins (the leftmost is client-supplied).
func TrustedClientIP(trusted []netip.Prefix) func(http.Handler) http.Handler {
	isTrusted := func(a netip.Addr) bool {
		for _, p := range trusted {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := resolve(r, isTrusted)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientIPKey{}, ip)))
		})
	}
}

func resolve(r *http.Request, isTrusted func(netip.Addr) bool) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	peer = peer.Unmap()
	if !isTrusted(peer) {
		return peer.String()
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		a = a.Unmap()
		if !isTrusted(a) {
			return a.String()
		}
	}
	return peer.String()
}
