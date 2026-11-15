package handlers

import (
	"net/http"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/session"
)

func (h *Handlers) roleChannels(w http.ResponseWriter, r *http.Request, s *session.Session) {
	list, err := h.Admin.RoleChannels(r.Context(), s, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": list})
}

func (h *Handlers) setRoleChannels(w http.ResponseWriter, r *http.Request, s *session.Session) {
	var body struct {
		Channels *[]authapi.RoleChannel `json:"channels"`
	}
	if err := decode(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	if body.Channels == nil {
		writeError(w, r, apperr.Invalid("channels is required (an empty list removes every channel)"))
		return
	}
	list, err := h.Admin.SetRoleChannels(r.Context(), s, r.PathValue("id"), *body.Channels)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": list})
}

func (h *Handlers) channelAccess(w http.ResponseWriter, r *http.Request, s *session.Session) {
	list, err := h.Admin.ChannelAccess(r.Context(), s)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": list})
}

func (h *Handlers) setChannelAccess(w http.ResponseWriter, r *http.Request, s *session.Session) {
	var body struct {
		Channels *[]authapi.ChannelAccess `json:"channels"`
	}
	if err := decode(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	if body.Channels == nil {
		writeError(w, r, apperr.Invalid("channels is required (an empty list makes every channel public)"))
		return
	}
	list, err := h.Admin.SetChannelAccess(r.Context(), s, *body.Channels)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": list})
}

func (h *Handlers) channelOverview(w http.ResponseWriter, r *http.Request, s *session.Session) {
	ov, err := h.Admin.ChannelOverview(r.Context(), s)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ov)
}
