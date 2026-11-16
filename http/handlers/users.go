package handlers

import (
	"net/http"
	"strconv"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/services/admin"
	"github.com/boson-chat/admin/internal/session"
)

func intQuery(r *http.Request, name string, def, lo, hi int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return min(max(n, lo), hi)
}

func (h *Handlers) listUsers(w http.ResponseWriter, r *http.Request, s *session.Session) {
	limit := intQuery(r, "limit", 50, 1, 200)
	offset := intQuery(r, "offset", 0, 0, 1<<30)
	sort := r.URL.Query().Get("sort")
	list, err := h.Admin.ListUsers(r.Context(), s, r.URL.Query().Get("q"), sort, limit, offset)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if sort == "" || !list.ActivitySupported {
		sort = authapi.SortCreated // what auth actually did
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": list.Users, "total": list.Total, "limit": limit, "offset": offset,
		"sort": sort, "activity_supported": list.ActivitySupported})
}

func (h *Handlers) getUser(w http.ResponseWriter, r *http.Request, s *session.Session) {
	u, err := h.Admin.GetUser(r.Context(), s, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

type updateUserReq struct {
	Role *string `json:"role"`
}

func (h *Handlers) updateUser(w http.ResponseWriter, r *http.Request, s *session.Session) {
	var req updateUserReq
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if req.Role == nil {
		writeError(w, r, apperr.Invalid("nothing to update"))
		return
	}
	u, err := h.Admin.SetRole(r.Context(), s, r.PathValue("id"), *req.Role)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

func (h *Handlers) setDisabled(disabled bool) authedFunc {
	return func(w http.ResponseWriter, r *http.Request, s *session.Session) {
		u, err := h.Admin.SetDisabled(r.Context(), s, r.PathValue("id"), disabled)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"user": u})
	}
}

func (h *Handlers) listUserSessions(w http.ResponseWriter, r *http.Request, s *session.Session) {
	list, err := h.Admin.ListUserSessions(r.Context(), s, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	current := s.Snapshot().SessionID
	out := make([]map[string]any, 0, len(list))
	for _, ss := range list {
		out = append(out, map[string]any{
			"id": ss.ID, "user_id": ss.UserID, "auth_method": ss.AuthMethod, "ip": ss.IP, "user_agent": ss.UserAgent,
			"created_at": ss.CreatedAt, "last_seen_at": ss.LastSeenAt, "expires_at": ss.ExpiresAt,
			"current": ss.ID == current,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

func (h *Handlers) revokeSession(w http.ResponseWriter, r *http.Request, s *session.Session) {
	id := r.PathValue("id")
	if id == s.Snapshot().SessionID {
		writeError(w, r, apperr.Invalid("that is this panel session; sign out instead"))
		return
	}
	if err := h.Admin.RevokeSession(r.Context(), s, id); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) userAudit(w http.ResponseWriter, r *http.Request, s *session.Session) {
	res, err := h.Admin.ListAudit(r.Context(), s, admin.AuditFilter{
		Actor: r.PathValue("id"), Limit: intQuery(r, "limit", 25, 1, admin.MaxAuditFetch),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": res.Entries})
}
