package handlers

import (
	"net/http"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/services/admin"
	"github.com/boson-chat/admin/internal/session"
)

type sessionView struct {
	User      *authapi.User `json:"user"`
	CSRFToken string        `json:"csrf_token"`
	ExpiresAt time.Time     `json:"expires_at"`
	Features  Features      `json:"features"`
	// Permissions are the account's effective permissions; the UI hides
	// what they don't cover (the BFF enforces it).
	Permissions []string `json:"permissions"`
	// Roles are the account's roles (empty before auth 1.5.0).
	Roles []authapi.RoleRef `json:"roles"`
	// RolesSupported: the auth service has roles (>= 1.5.0). Without them
	// role=admin grants every permission.
	RolesSupported bool `json:"roles_supported"`
}

func (h *Handlers) view(d session.Data, a *admin.Access) sessionView {
	v := sessionView{CSRFToken: d.CSRF, ExpiresAt: h.Sessions.Expiry(d), Features: h.features(),
		Permissions: []string{}, Roles: []authapi.RoleRef{}}
	if a == nil {
		v.User = &authapi.User{ID: d.UserID, Username: d.Username, Role: d.Role}
		return v
	}
	v.User, v.Permissions, v.RolesSupported = a.User, a.Permissions, !a.Legacy
	if a.User.Roles != nil {
		v.Roles = *a.User.Roles
	}
	return v
}

// getSession re-checks the account against the auth service (and refreshes
// the tokens if needed), so a disabled account, or one that lost
// admin.panel, is signed out on the next page load.
func (h *Handlers) getSession(w http.ResponseWriter, r *http.Request, s *session.Session) {
	a, err := h.Admin.Access(r.Context(), s, true)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, h.view(s.Snapshot(), a))
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
	// Login cached the account's permissions; this reads them back.
	a, _ := h.Admin.Access(r.Context(), session.New(d), false)
	writeJSON(w, http.StatusOK, h.view(d, a))
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
