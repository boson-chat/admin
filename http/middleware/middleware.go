package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/boson-chat/admin/internal/logger"
	"github.com/boson-chat/admin/metrics"
)

// Chain applies middleware so the first one is outermost.
func Chain(h http.Handler, mw ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status, w.wrote = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.status, w.wrote = http.StatusOK, true
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Observe adds a request-scoped logger, recovers panics, and logs and
// counts every request by its route pattern.
func Observe(m *metrics.Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			reqID := newID()
			l := logger.FromCtx(r.Context()).With().Str("request_id", reqID).Logger()
			r = r.WithContext(logger.WithLogger(r.Context(), l))
			sw := &statusWriter{ResponseWriter: w}
			defer func() {
				if rec := recover(); rec != nil {
					if rec == http.ErrAbortHandler {
						panic(rec)
					}
					l.Error().Interface("panic", rec).Bytes("stack", debug.Stack()).Msg("handler panic")
					if !sw.wrote {
						sw.Header().Set("Content-Type", "application/json; charset=utf-8")
						sw.WriteHeader(http.StatusInternalServerError)
						_, _ = sw.Write([]byte(`{"error":"internal_error","message":"internal error"}`))
					}
				}
				route := r.Pattern
				if route == "" {
					route = "unmatched"
				}
				d := time.Since(start)
				m.HTTPRequest(r.Context(), route, sw.status, d)
				ev := l.Debug()
				if sw.status >= 500 {
					ev = l.Warn()
				} else if strings.HasPrefix(r.URL.Path, "/api/") {
					ev = l.Info()
				}
				ev.Str("method", r.Method).Str("route", route).Int("status", sw.status).
					Dur("duration", d).Str("ip", ClientIP(r.Context())).Msg("request")
			}()
			w.Header().Set("X-Request-Id", reqID)
			next.ServeHTTP(sw, r)
		})
	}
}

// SecurityHeaders sets browser hardening headers. csp is the full
// Content-Security-Policy (computed from the embedded UI).
func SecurityHeaders(csp string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", csp)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "same-origin")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Cross-Origin-Resource-Policy", "same-origin")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), interest-cohort=()")
			if strings.HasPrefix(r.URL.Path, "/api/") {
				h.Set("Cache-Control", "no-store")
			}
			next.ServeHTTP(w, r)
		})
	}
}
