// Package handlers implements the panel's JSON API under /api and serves
// the embedded UI. Handlers only talk to services.
package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"

	"github.com/boson-chat/admin/http/middleware"
	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/clients/bosonapi"
	"github.com/boson-chat/admin/internal/logger"
	"github.com/boson-chat/admin/internal/services/admin"
	"github.com/boson-chat/admin/internal/services/containers"
	"github.com/boson-chat/admin/internal/services/directory"
	"github.com/boson-chat/admin/internal/services/overview"
	"github.com/boson-chat/admin/internal/session"
)

const (
	// CSRFHeader carries the session's CSRF token on state-changing requests.
	CSRFHeader = "X-CSRF-Token"
	// LoginHeader must be present on POST /api/login (no session yet, so no
	// token): cross-site forms can't set custom headers.
	LoginHeader = "X-Requested-With"
	maxBody     = 64 << 10
)

// Features tells the UI what is configured.
type Features struct {
	Version    string `json:"version"`
	GrafanaURL string `json:"grafana_url,omitempty"`
	Metrics    bool   `json:"metrics"`
	// Services: DOCKER_PROXY_URL is set.
	Services bool `json:"services"`
	// Profiles: BOSON_API_URL is set.
	Profiles bool `json:"profiles"`
	// Directory: DIRECTORY_API_URL is set; DirectoryRegistration: the
	// proposed self-hosted registration is switched on.
	Directory             bool `json:"directory"`
	DirectoryRegistration bool `json:"directory_registration"`
	// IRCHost is the public IRC hostname (for previews of how roles look on IRC).
	IRCHost string `json:"irc_host,omitempty"`
}

// Handlers is the DI container for the HTTP layer.
type Handlers struct {
	Admin    admin.AdminServiceImpl
	Overview overview.OverviewServiceImpl
	// Optional: nil when not configured.
	Containers containers.ContainersServiceImpl
	Directory  directory.DirectoryServiceImpl
	Profiles   bosonapi.ProfileClientImpl
	Sessions   *session.Codec
	// Origin is PUBLIC_URL's origin; unsafe requests must come from it.
	Origin   string
	Features Features
}

// Register mounts the API on mux.
func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/session", h.authed(h.getSession))
	mux.HandleFunc("POST /api/login", h.login)
	mux.HandleFunc("POST /api/logout", h.authed(h.logout))

	// Every other route needs admin.panel plus its permission (routes.go).
	for _, rt := range h.routes() {
		mux.HandleFunc(rt.Pattern, h.gate(rt.Perm, rt.Fresh, rt.fn))
	}

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, apperr.ErrNotFound)
	})
}

type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// writeError renders apperr errors and hides everything else. Errors that
// end the panel session also clear the cookie.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, apperr.ErrSessionExpired) || errors.Is(err, apperr.ErrNotAdmin) || errors.Is(err, apperr.ErrUnauthenticated) {
		if sw, ok := w.(*sessionWriter); ok {
			sw.end()
		}
	}
	if e, ok := apperr.As(err); ok {
		// 501 (upstream lacks a feature) and 503 (feature not configured)
		// are expected states, not failures.
		if e.Status >= 500 && e.Status != http.StatusNotImplemented && e.Status != http.StatusServiceUnavailable {
			logger.FromCtx(r.Context()).Warn().Err(err).Str("path", r.URL.Path).Msg("upstream error")
		}
		writeJSON(w, e.Status, errorBody{Error: e.Code, Message: e.Message})
		return
	}
	if errors.Is(err, context.Canceled) {
		return // client went away
	}
	if errors.Is(err, context.DeadlineExceeded) {
		writeJSON(w, http.StatusGatewayTimeout, errorBody{Error: "upstream_timeout", Message: "the auth service took too long to answer"})
		return
	}
	logger.FromCtx(r.Context()).Error().Err(err).Str("path", r.URL.Path).Msg("internal error")
	writeJSON(w, http.StatusInternalServerError, errorBody{Error: "internal_error", Message: "internal error"})
}

var errUnsupportedMedia = apperr.New(http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")

func decode(w http.ResponseWriter, r *http.Request, dst any) error {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/json" {
		return errUnsupportedMedia
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		return apperr.Invalid("malformed JSON body")
	}
	return nil
}

func unsafeMethod(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

var errOrigin = apperr.New(http.StatusForbidden, "bad_origin", "request did not come from the admin panel")

// checkOrigin rejects cross-site state-changing requests. Browsers send
// Origin on every cross-origin and every non-GET same-origin fetch, and
// Sec-Fetch-Site everywhere modern.
func (h *Handlers) checkOrigin(r *http.Request) error {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return errOrigin
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || u.Scheme+"://"+u.Host != h.Origin {
			return errOrigin
		}
	}
	return nil
}

// withMeta forwards the admin's IP and browser to the auth service.
func withMeta(r *http.Request) context.Context {
	return authapi.WithMeta(r.Context(), authapi.ClientMeta{IP: middleware.ClientIP(r.Context()), UserAgent: r.UserAgent()})
}

type authedFunc func(w http.ResponseWriter, r *http.Request, s *session.Session)

// authed loads the session cookie and, for state-changing requests, checks
// the origin and the CSRF token. Token refreshes made while handling the
// request are written back to the cookie before the response goes out.
func (h *Handlers) authed(next authedFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, noCookie := r.Cookie(h.Sessions.Name())
		d, err := h.Sessions.Read(r)
		if err != nil {
			if noCookie == nil { // a cookie we can't use (expired, old key): drop it
				h.Sessions.Clear(w)
			}
			writeError(w, r, apperr.ErrUnauthenticated)
			return
		}
		if unsafeMethod(r.Method) {
			if err := h.checkOrigin(r); err != nil {
				writeError(w, r, err)
				return
			}
			got := r.Header.Get(CSRFHeader)
			if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(d.CSRF)) != 1 {
				writeError(w, r, apperr.ErrCSRF)
				return
			}
		}
		sess := session.New(d)
		sw := &sessionWriter{ResponseWriter: w, sess: sess, codec: h.Sessions, r: r}
		r = r.WithContext(withMeta(r))
		next(sw, r, sess)
	}
}

// sessionWriter re-issues the session cookie (after a token refresh) or
// clears it (session ended) just before the headers are sent.
type sessionWriter struct {
	http.ResponseWriter
	sess   *session.Session
	codec  *session.Codec
	r      *http.Request
	ended  bool
	headed bool
}

func (w *sessionWriter) end() { w.ended = true }

func (w *sessionWriter) flushCookie() {
	if w.headed {
		return
	}
	w.headed = true
	switch {
	case w.ended:
		w.codec.Clear(w.ResponseWriter)
	case w.sess.Dirty():
		if err := w.codec.Write(w.ResponseWriter, w.sess.Snapshot()); err != nil {
			logger.FromCtx(w.r.Context()).Error().Err(err).Msg("re-issuing session cookie")
		}
	}
}

func (w *sessionWriter) WriteHeader(code int) {
	w.flushCookie()
	w.ResponseWriter.WriteHeader(code)
}

func (w *sessionWriter) Write(b []byte) (int, error) {
	w.flushCookie()
	return w.ResponseWriter.Write(b)
}

func (w *sessionWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
