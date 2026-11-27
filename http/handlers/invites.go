package handlers

import (
	"net/http"

	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/session"
)

func (h *Handlers) listInvites(w http.ResponseWriter, r *http.Request, s *session.Session) {
	list, err := h.Admin.ListInvites(r.Context(), s)
	if err != nil {
		writeError(w, r, err)
		return
	}
	cfg, _ := h.Admin.AuthConfig(r.Context()) // registration mode, best effort
	registration := ""
	if cfg != nil {
		registration = cfg.Registration
	}
	writeJSON(w, http.StatusOK, map[string]any{"invites": list, "registration": registration})
}

func (h *Handlers) createInvite(w http.ResponseWriter, r *http.Request, s *session.Session) {
	var req authapi.CreateInvite
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	inv, err := h.Admin.CreateInvite(r.Context(), s, req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"invite": inv})
}

func (h *Handlers) revokeInvite(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if err := h.Admin.RevokeInvite(r.Context(), s, r.PathValue("id")); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// authAccounts is what the panel needs from auth's /v1/config to explain
// its account settings: whether auth can send email (no SMTP: no email
// verification, no reset emails, links to share by hand) and where
// accounts live (local or NickServ).
func (h *Handlers) authAccounts(w http.ResponseWriter, r *http.Request, _ *session.Session) {
	cfg, err := h.Admin.AuthConfig(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	// Older auth doesn't report email (assumed configured) or accounts.
	out := map[string]any{
		"email_configured":           cfg.EmailConfigured(),
		"email_reported":             cfg.Email != nil,
		"account_backend":            cfg.AccountBackend(),
		"require_email_verification": cfg.RequireEmailVerification,
		"password_reset":             cfg.AccountBackend() == authapi.AccountBackendLocal && cfg.EmailConfigured(),
		"signup":                     cfg.Registration != "closed",
		"signup_message":             nil,
	}
	if a := cfg.Accounts; a != nil {
		out["password_reset"], out["signup"], out["signup_message"] = a.PasswordReset, a.Signup, a.SignupMessage
	}
	writeJSON(w, http.StatusOK, out)
}
