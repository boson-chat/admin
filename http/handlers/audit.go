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
	now := time.Now()
	since, err := parseSince(q.Get("since"), now)
	if err != nil {
		writeError(w, r, err)
		return
	}
	until, err := parseSince(q.Get("until"), now)
	if err != nil {
		writeError(w, r, apperr.Invalid("until must be RFC 3339 or a duration like 1h"))
		return
	}
	actor := strings.TrimSpace(q.Get("actor"))
	if actor == "" {
		actor = strings.TrimSpace(q.Get("user_id")) // older links
	}
	res, err := h.Admin.ListAudit(r.Context(), s, admin.AuditFilter{
		Actor: actor, Target: q.Get("target"), Event: strings.TrimSpace(q.Get("event")),
		Since: since, Until: until, Cursor: q.Get("cursor"),
		Limit: intQuery(r, "limit", 50, 1, admin.MaxAuditFetch),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": res.Entries, "next_cursor": res.NextCursor, "server_filtered": res.ServerFiltered,
		"requires": res.Requires, "events": admin.AuditEvents,
	})
}
