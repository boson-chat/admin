package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/services/admin"
	"github.com/boson-chat/admin/internal/session"
)

// parseSince accepts RFC 3339 or a Go duration back from now ("24h").
func parseSince(v string, now time.Time) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return now.Add(-d), nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	return time.Time{}, apperr.Invalid("since must be RFC 3339 or a duration like 24h")
}

func (h *Handlers) audit(w http.ResponseWriter, r *http.Request, s *session.Session) {
	q := r.URL.Query()
	since, err := parseSince(q.Get("since"), time.Now())
	if err != nil {
		writeError(w, r, err)
		return
	}
	f := admin.AuditFilter{
		UserID: strings.TrimSpace(q.Get("user_id")),
		Event:  strings.TrimSpace(q.Get("event")),
		Since:  since,
		Limit:  intQuery(r, "limit", 100, 1, admin.MaxAuditFetch),
	}
	entries, err := h.Admin.ListAudit(r.Context(), s, f)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "events": admin.AuditEvents})
}
