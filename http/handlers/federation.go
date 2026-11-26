package handlers

import (
	"net/http"
	"strconv"

	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/services/federation"
	"github.com/boson-chat/admin/internal/session"
)

// Federation (auth docs/federation.md): GET/PATCH /api/federation are the
// settings (auth.federation.* in the config file, else auth's database,
// else read-only from auth's env) and this server as a home; the remote
// users, banning them and blocking a home domain go to auth.

func (h *Handlers) getFederation(w http.ResponseWriter, r *http.Request, s *session.Session) {
	writeJSON(w, http.StatusOK, h.Federation.View(r.Context(), s, h.can(r, s), r.URL.Query().Get("fresh") == "1"))
}

func (h *Handlers) patchFederation(w http.ResponseWriter, r *http.Request, s *session.Session) {
	var in federation.Patch
	if err := decode(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	v, err := h.Federation.Update(r.Context(), s, in, h.can(r, s))
	if err != nil {
		writeError(w, r, configErr(err))
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handlers) remoteUsers(w http.ResponseWriter, r *http.Request, s *session.Session) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	p, err := h.Federation.Users(r.Context(), s, authapi.RemoteUserQuery{Q: q.Get("q"), Domain: q.Get("domain"), Cursor: q.Get("cursor"), Limit: limit})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *Handlers) banRemoteUser(w http.ResponseWriter, r *http.Request, s *session.Session) {
	var in struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength != 0 {
		if err := decode(w, r, &in); err != nil {
			writeError(w, r, err)
			return
		}
	}
	u, err := h.Federation.Ban(r.Context(), s, r.PathValue("id"), in.Reason)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (h *Handlers) unbanRemoteUser(w http.ResponseWriter, r *http.Request, s *session.Session) {
	u, err := h.Federation.Unban(r.Context(), s, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (h *Handlers) blockDomain(w http.ResponseWriter, r *http.Request, s *session.Session) {
	var in struct {
		Domain string `json:"domain"`
	}
	if err := decode(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	res, err := h.Federation.BlockDomain(r.Context(), s, in.Domain, h.can(r, s))
	if err != nil {
		writeError(w, r, configErr(err))
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handlers) unblockDomain(w http.ResponseWriter, r *http.Request, s *session.Session) {
	v, err := h.Federation.UnblockDomain(r.Context(), s, r.PathValue("domain"), h.can(r, s))
	if err != nil {
		writeError(w, r, configErr(err))
		return
	}
	writeJSON(w, http.StatusOK, v)
}
