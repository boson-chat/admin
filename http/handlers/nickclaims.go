package handlers

import (
	"net/http"

	"github.com/boson-chat/admin/internal/services/nickclaim"
	"github.com/boson-chat/admin/internal/session"
)

// Nick registration (the api's nick claims): GET /api/nick-claims is the
// api's status, POST /api/nick-claims/test tries the (unsaved) mailbox.
// The settings themselves are nick_claim.* keys of PATCH /api/config.

func (h *Handlers) nickClaimStatus(w http.ResponseWriter, r *http.Request, s *session.Session) {
	writeJSON(w, http.StatusOK, h.NickClaims.Status(r.Context()))
}

func (h *Handlers) nickClaimTest(w http.ResponseWriter, r *http.Request, s *session.Session) {
	var in nickclaim.TestRequest
	if err := decode(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	res, err := h.NickClaims.Test(r.Context(), s.Snapshot().Username, in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
