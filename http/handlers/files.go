package handlers

import (
	"context"
	"net/http"
	"strconv"

	"github.com/boson-chat/admin/internal/clients/bosonapi"
	"github.com/boson-chat/admin/internal/session"
)

// Uploads (the files service): GET /api/files/status is its live status
// for the Uploads page (whose settings are files.* keys of PATCH
// /api/config); the moderation list, delete and a user's usage here.

func (h *Handlers) filesStatus(w http.ResponseWriter, r *http.Request, s *session.Session) {
	writeJSON(w, http.StatusOK, h.Files.Status(r.Context()))
}

func (h *Handlers) listFiles(w http.ResponseWriter, r *http.Request, s *session.Session) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	names := func(ctx context.Context, id string) string {
		u, err := h.Admin.GetUser(ctx, s, id)
		if err != nil || u == nil {
			return ""
		}
		return u.Username
	}
	p, err := h.Files.List(r.Context(), bosonapi.FilesQuery{User: q.Get("user"), Q: q.Get("q"), Cursor: q.Get("cursor"), Limit: limit}, names)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *Handlers) deleteFile(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if err := h.Files.Delete(r.Context(), s.Snapshot().Username, r.PathValue("id"), r.URL.Query().Get("reason")); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) userFilesUsage(w http.ResponseWriter, r *http.Request, s *session.Session) {
	u, err := h.Files.Usage(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}
