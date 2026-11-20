package handlers

import (
	"net/http"

	"github.com/boson-chat/admin/internal/session"
)

// GET /api/metrics/host?range=1h|6h|24h|7d&instance= is the Metrics page:
// the VPS's node-exporter numbers and every scrape target's health, from a
// fixed set of server-side queries (the browser never sends PromQL).
func (h *Handlers) hostMetrics(w http.ResponseWriter, r *http.Request, s *session.Session) {
	q := r.URL.Query()
	v, err := h.HostMetrics.Host(r.Context(), q.Get("range"), q.Get("instance"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
