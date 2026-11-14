package admin

import "strings"

// AuditEvents are the event names the auth service records (for the
// audit filter). Kept in sync with auth's internal/services/audit.
var AuditEvents = []string{
	"register", "email_verified", "login", "login_failed", "logout", "refresh_token_reuse",
	"password_reset_requested", "password_reset", "password_changed", "session_revoked",
	"internal_verify_password", "admin_user_created", "admin_user_disabled", "admin_user_enabled",
	"admin_role_changed", "admin_session_revoked", "admin_invite_created", "admin_invite_revoked",
	"signing_key_created", "settings_updated", "admin_password_reset_sent", "admin_verification_sent",
	"sso_identity_linked", "sso_identity_unlinked", "sso_login_failed",
	"sso_provider_created", "sso_provider_updated", "sso_provider_deleted",
}

// expandEvents turns a filter ("login", "admin_*", "admin_*,settings_updated")
// into exact event names: the auth service matches a comma list exactly.
// Unknown exact names pass through (newer auth releases may have more).
func expandEvents(filter string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(e string) {
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	for _, f := range strings.Split(filter, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if p, ok := strings.CutSuffix(f, "*"); ok {
			for _, e := range AuditEvents {
				if strings.HasPrefix(e, p) {
					add(e)
				}
			}
			continue
		}
		add(f)
	}
	return out
}
