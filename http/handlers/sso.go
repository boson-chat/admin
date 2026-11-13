package handlers

import (
	"net/http"

	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/session"
)

// sso is a placeholder until the auth service grows OIDC (phase 2) and
// SAML (phase 3) with an admin API for providers. For now it reports the
// providers the auth service already advertises publicly.
func (h *Handlers) sso(w http.ResponseWriter, r *http.Request, _ *session.Session) {
	providers := []authapi.SSOProvider{}
	errMsg := ""
	if cfg, err := h.Admin.AuthConfig(r.Context()); err != nil {
		errMsg = "could not read the auth service's public configuration"
	} else if cfg.SSOProviders != nil {
		providers = cfg.SSOProviders
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "planned",
		"providers": providers,
		"error":     errMsg,
		"roadmap": []map[string]string{
			{"phase": "2", "kind": "oidc", "title": "OpenID Connect providers", "detail": "coreos/go-oidc; account linking; /v1/sso/{slug}/start|callback"},
			{"phase": "3", "kind": "saml", "title": "SAML service provider", "detail": "crewjam/saml"},
		},
	})
}
