package handlers

import (
	"net/http"

	"github.com/boson-chat/admin/internal/session"
)

func (h *Handlers) overview(w http.ResponseWriter, r *http.Request, s *session.Session) {
	// Confirms the session is still live and admin upstream before showing
	// infrastructure details.
	if _, err := h.Admin.Me(r.Context(), s); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, h.Overview.Overview(r.Context()))
}
