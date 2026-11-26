package handlers

import (
	"net/http"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/services/admin"
	"github.com/boson-chat/admin/internal/services/updates"
	"github.com/boson-chat/admin/internal/session"
)

var errUpdatesOff = apperr.New(http.StatusServiceUnavailable, "updates_off", "update checks are off (UPDATES_ENABLED=false)")

func (h *Handlers) updatesOr503(w http.ResponseWriter, r *http.Request) bool {
	if h.Updates == nil {
		writeError(w, r, errUpdatesOff)
		return false
	}
	return true
}

func (h *Handlers) updatesStatus(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if h.updatesOr503(w, r) {
		writeJSON(w, http.StatusOK, h.Updates.Status(r.Context()))
	}
}

func (h *Handlers) updatesCheck(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if h.updatesOr503(w, r) {
		writeJSON(w, http.StatusOK, h.Updates.Check(r.Context()))
	}
}

// canApply: applying rewrites the stack's .env, so it needs settings.manage
// on top of services.manage (the route's gate).
func (h *Handlers) canApply(w http.ResponseWriter, r *http.Request, s *session.Session) bool {
	if !h.updatesOr503(w, r) {
		return false
	}
	a, err := h.Admin.Access(r.Context(), s, false)
	if err != nil {
		writeError(w, r, err)
		return false
	}
	if !a.CanAny(admin.PermSettingsManage) {
		writeError(w, r, admin.MissingPermission(admin.PermSettingsManage))
		return false
	}
	return true
}

func (h *Handlers) updatesApply(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if !h.canApply(w, r, s) {
		return
	}
	var req updates.ApplyRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	d := s.Snapshot()
	run, err := h.Updates.Apply(r.Context(), d.Username, req, d.AccessToken)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (h *Handlers) updatesRollback(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if !h.canApply(w, r, s) {
		return
	}
	var req updates.RollbackRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	d := s.Snapshot()
	run, err := h.Updates.Rollback(r.Context(), d.Username, req, d.AccessToken)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (h *Handlers) updatesPlan(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if !h.updatesOr503(w, r) {
		return
	}
	var req updates.ApplyRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	p, err := h.Updates.Preview(req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}
