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
