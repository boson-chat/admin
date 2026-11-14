package handlers

import (
	"net/http"

	"github.com/boson-chat/admin/internal/session"
)

func (h *Handlers) overview(w http.ResponseWriter, r *http.Request, s *session.Session) {
	// gate has confirmed the session is live and may see services.
	writeJSON(w, http.StatusOK, h.Overview.Overview(r.Context()))
}
