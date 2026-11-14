package handlers

import (
	"net/http"
	"strings"

	"github.com/boson-chat/admin/internal/services/admin"
	"github.com/boson-chat/admin/internal/session"
)

// route is one gated /api endpoint.
type route struct {
	Pattern string
	// Perm is needed on top of admin.panel ("" = admin.panel alone; "a|b"
	// = either).
	Perm string
	// Fresh re-checks the account against /v1/me on every call instead of
	// the AccessTTL cache: Docker and the directory aren't authorised by the
	// auth service, so a demoted admin's still-valid cookie must not drive them.
	Fresh bool
	fn    authedFunc
}

// routes is the panel's permission table. The auth service enforces its
// own rules (and the role hierarchy) on top for everything it serves.
func (h *Handlers) routes() []route {
	return []route{
		{"GET /api/overview", admin.PermServicesView, false, h.overview},

		{"GET /api/users", admin.PermUsersView, false, h.listUsers},
		{"POST /api/users", admin.PermUsersManage, false, h.createUser},
		{"POST /api/users/{id}/password-reset", admin.PermUsersManage, false, h.sendPasswordReset},
		{"POST /api/users/{id}/verification", admin.PermUsersManage, false, h.resendVerification},
		{"GET /api/users/{id}/profile", admin.PermUsersView, false, h.userProfile},
		{"GET /api/users/{id}/networks", admin.PermUsersView, false, h.userNetworks},
		{"GET /api/users/{id}", admin.PermUsersView, false, h.getUser},
		// auth 1.x role switch (user | admin): a role change.
		{"PATCH /api/users/{id}", admin.PermRolesManage, false, h.updateUser},
		{"POST /api/users/{id}/disable", moderate, false, h.setDisabled(true)},
		{"POST /api/users/{id}/enable", moderate, false, h.setDisabled(false)},
		{"GET /api/users/{id}/sessions", admin.PermUsersView, false, h.listUserSessions},
		{"GET /api/users/{id}/audit", admin.PermUsersView, false, h.userAudit},
		{"PUT /api/users/{id}/roles", admin.PermRolesManage, false, h.setUserRoles},
		{"DELETE /api/sessions/{id}", moderate, false, h.revokeSession},

		{"GET /api/invites", admin.PermInvitesManage, false, h.listInvites},
		{"POST /api/invites", admin.PermInvitesManage, false, h.createInvite},
		{"DELETE /api/invites/{id}", admin.PermInvitesManage, false, h.revokeInvite},

		{"GET /api/audit", admin.PermAuditView, false, h.audit},

		{"GET /api/settings", admin.PermSettingsManage, false, h.getSettings},
		{"PATCH /api/settings", admin.PermSettingsManage, false, h.updateSettings},

		// The stack config file: reading needs admin.panel only (no
		// secrets in it); each key's write needs its own permission.
		{"GET /api/config", "", false, h.getConfig},
		{"GET /api/config/raw", "", false, h.getConfigRaw},
		{"GET /api/config/status", "", false, h.getConfigStatus},
		{"PATCH /api/config", configWrite, true, h.patchConfig},

		// Branding: the theme and email wording are config keys (above);
		// images and email previews here.
		{"PUT /api/branding/images/{slot}", admin.PermSettingsManage, true, h.putBrandingImage},
		{"DELETE /api/branding/images/{slot}", admin.PermSettingsManage, true, h.deleteBrandingImage},
		{"GET /api/branding/assets/{file}", "", false, h.brandingAsset},
		{"POST /api/emails/preview", admin.PermSettingsManage, false, h.emailPreview},
		{"POST /api/emails/test", admin.PermSettingsManage, true, h.emailTest},
		{"GET /api/emails/mjml/{part}", admin.PermSettingsManage, false, h.getEmailMJML},
		{"PUT /api/emails/mjml/{part}", admin.PermSettingsManage, true, h.putEmailMJML},
		{"DELETE /api/emails/mjml/{part}", admin.PermSettingsManage, true, h.deleteEmailMJML},
		{"GET /api/emails/default-mjml", admin.PermSettingsManage, false, h.defaultEmailMJML},

		// Nick registration: its settings are nick_claim.* config keys; the
		// api's status and the mailbox test here.
		{"GET /api/nick-claims", admin.PermSettingsManage, false, h.nickClaimStatus},
		{"POST /api/nick-claims/test", admin.PermSettingsManage, true, h.nickClaimTest},

		{"GET /api/sso/providers", admin.PermSSOManage, false, h.listProviders},
		{"POST /api/sso/providers", admin.PermSSOManage, false, h.createProvider},
		{"GET /api/sso/providers/{id}", admin.PermSSOManage, false, h.getProvider},
		{"PATCH /api/sso/providers/{id}", admin.PermSSOManage, false, h.updateProvider},
		{"DELETE /api/sso/providers/{id}", admin.PermSSOManage, false, h.deleteProvider},
		{"POST /api/sso/providers/{id}/test", admin.PermSSOManage, false, h.testProvider},

		{"GET /api/services", admin.PermServicesView, true, h.listServices},
		{"GET /api/services/{name}", admin.PermServicesView, true, h.getService},
		{"POST /api/services/{name}/{action}", admin.PermServicesManage, true, h.serviceAction},
		{"GET /api/services/{name}/logs", admin.PermServicesView, true, h.serviceLogs},

		{"GET /api/directory", admin.PermDirectoryManage, true, h.directoryStatus},
		{"POST /api/directory", admin.PermDirectoryManage, true, h.directorySubmit},
		{"PATCH /api/directory/{id}", admin.PermDirectoryManage, true, h.directoryUpdate},
		{"POST /api/directory/{id}/verify", admin.PermDirectoryManage, true, h.directoryVerify},
		{"POST /api/directory/{id}/regenerate-token", admin.PermDirectoryManage, true, h.directoryRegenerate},
		{"DELETE /api/directory/{id}", admin.PermDirectoryManage, true, h.directoryUnlist},
		{"GET /api/directory/federation", admin.PermDirectoryManage, true, h.directoryFederation},
		{"PUT /api/directory/federation/include-main", admin.PermDirectoryManage, true, h.directoryIncludeMain},
		{"PATCH /api/directory/settings", admin.PermDirectoryManage, true, h.directorySettings},
		{"GET /api/directory/peers/check", admin.PermDirectoryManage, true, h.directoryPeerCheck},
		{"POST /api/directory/peers", admin.PermDirectoryManage, true, h.directoryPeerAdd},
		{"PATCH /api/directory/peers/{id}", admin.PermDirectoryManage, true, h.directoryPeerUpdate},
		{"DELETE /api/directory/peers/{id}", admin.PermDirectoryManage, true, h.directoryPeerRemove},
		{"POST /api/directory/peers/{id}/sync", admin.PermDirectoryManage, true, h.directoryPeerSync},

		// Reading roles and the catalog needs only admin.panel, as in auth:
		// user pages label effective permissions and show the hierarchy.
		{"GET /api/permissions", "", false, h.listPermissions},
		{"GET /api/roles", "", false, h.listRoles},
		{"GET /api/roles/{id}", "", false, h.getRole},
		{"GET /api/roles/{id}/members", "", false, h.roleMembers},
		{"POST /api/roles", admin.PermRolesManage, false, h.createRole},
		{"POST /api/roles/reorder", admin.PermRolesManage, false, h.reorderRoles},
		{"PATCH /api/roles/{id}", admin.PermRolesManage, false, h.updateRole},
		{"DELETE /api/roles/{id}", admin.PermRolesManage, false, h.deleteRole},
		{"PUT /api/roles/{id}/members/{user}", admin.PermRolesManage, false, h.addRoleMember},
		{"DELETE /api/roles/{id}/members/{user}", admin.PermRolesManage, false, h.removeRoleMember},
	}
}

// moderate: disabling, enabling and revoking sessions take either.
const moderate = admin.PermUsersModerate + "|" + admin.PermUsersManage

// gate authenticates (authed), then checks admin.panel and perm. Without
// admin.panel the panel session ends (403 not_admin); a missing perm is a
// 403 missing_permission that leaves it alone.
func (h *Handlers) gate(perm string, fresh bool, next authedFunc) http.HandlerFunc {
	return h.authed(func(w http.ResponseWriter, r *http.Request, s *session.Session) {
		a, err := h.Admin.Access(r.Context(), s, fresh)
		if err != nil {
			writeError(w, r, err)
			return
		}
		if perm != "" && !a.CanAny(strings.Split(perm, "|")...) {
			writeError(w, r, admin.MissingPermission(perm))
			return
		}
		next(w, r, s)
	})
}
