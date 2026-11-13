package handlers

import (
	"net/http"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/session"
)

type sessionView struct {
	User      *authapi.User `json:"user"`
	CSRFToken string        `json:"csrf_token"`
	ExpiresAt time.Time     `json:"expires_at"`
	Features  Features      `json:"features"`
}

func (h *Handlers) view(d session.Data, u *authapi.User) sessionView {
	if u == nil {
		u = &authapi.User{ID: d.UserID, Username: d.Username, Role: d.Role}
	}
	return sessionView{User: u, CSRFToken: d.CSRF, ExpiresAt: h.Sessions.Expiry(d), Features: h.Features}
}

// getSession re-checks the admin against the auth service (and refreshes
// the tokens if needed), so a disabled or demoted admin is signed out on
// the next page load.
func (h *Handlers) getSession(w http.ResponseWriter, r *http.Request, s *session.Session) {
	u, err := h.Admin.Me(r.Context(), s)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, h.view(s.Snapshot(), u))
}

type loginReq struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

func (h *Handlers) login(w http.ResponseWriter, r *http.Request) {
	if err := h.checkOrigin(r); err != nil {
		writeError(w, r, err)
		return
	}
	if r.Header.Get(LoginHeader) == "" {
		writeError(w, r, apperr.ErrCSRF)
		return
	}
	var req loginReq
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	d, err := h.Admin.Login(withMeta(r), req.Login, req.Password)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.Sessions.Write(w, d); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, h.view(d, nil))
}

func (h *Handlers) logout(w http.ResponseWriter, r *http.Request, s *session.Session) {
	err := h.Admin.Logout(r.Context(), s)
	if sw, ok := w.(*sessionWriter); ok {
		sw.end()
	}
	if err != nil {
		// The cookie is gone either way; report that the upstream session may linger.
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
