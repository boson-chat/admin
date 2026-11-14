package handlers

import (
	"net/http"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/session"
)

func (h *Handlers) listPermissions(w http.ResponseWriter, r *http.Request, s *session.Session) {
	list, err := h.Admin.ListPermissions(r.Context(), s)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *Handlers) listRoles(w http.ResponseWriter, r *http.Request, s *session.Session) {
	list, err := h.Admin.ListRoles(r.Context(), s)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": list})
}

func (h *Handlers) getRole(w http.ResponseWriter, r *http.Request, s *session.Session) {
	role, err := h.Admin.GetRole(r.Context(), s, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"role": role})
}

func (h *Handlers) createRole(w http.ResponseWriter, r *http.Request, s *session.Session) {
	var in authapi.RoleInput
	if err := decode(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	role, err := h.Admin.CreateRole(r.Context(), s, in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"role": role})
}

func (h *Handlers) updateRole(w http.ResponseWriter, r *http.Request, s *session.Session) {
	var in authapi.RoleInput
	if err := decode(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	role, err := h.Admin.UpdateRole(r.Context(), s, r.PathValue("id"), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"role": role})
}

func (h *Handlers) deleteRole(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if err := h.Admin.DeleteRole(r.Context(), s, r.PathValue("id")); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) reorderRoles(w http.ResponseWriter, r *http.Request, s *session.Session) {
	var body struct {
		Order []string `json:"order"`
	}
	if err := decode(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	list, err := h.Admin.ReorderRoles(r.Context(), s, body.Order)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": list})
}

func (h *Handlers) roleMembers(w http.ResponseWriter, r *http.Request, s *session.Session) {
	p, err := h.Admin.RoleMembers(r.Context(), s, r.PathValue("id"), r.URL.Query().Get("cursor"), intQuery(r, "limit", 50, 1, 200))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": p.Users, "next_cursor": p.NextCursor})
}

func (h *Handlers) addRoleMember(w http.ResponseWriter, r *http.Request, s *session.Session) {
	roles, err := h.Admin.AddRoleMember(r.Context(), s, r.PathValue("id"), r.PathValue("user"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": roles})
}

func (h *Handlers) removeRoleMember(w http.ResponseWriter, r *http.Request, s *session.Session) {
	roles, err := h.Admin.RemoveRoleMember(r.Context(), s, r.PathValue("id"), r.PathValue("user"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": roles})
}

func (h *Handlers) setUserRoles(w http.ResponseWriter, r *http.Request, s *session.Session) {
	var body struct {
		RoleIDs *[]string `json:"role_ids"`
	}
	if err := decode(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	if body.RoleIDs == nil {
		writeError(w, r, apperr.Invalid("role_ids is required (an empty list removes every role)"))
		return
	}
	roles, err := h.Admin.SetUserRoles(r.Context(), s, r.PathValue("id"), *body.RoleIDs)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": roles})
}
