package admin

// AuditEvents are the event names the auth service records (for the
// audit filter). Kept in sync with auth's internal/services/audit.
var AuditEvents = []string{
	"register", "email_verified", "login", "login_failed", "logout", "refresh_token_reuse",
	"password_reset_requested", "password_reset", "password_changed", "session_revoked",
	"internal_verify_password", "admin_user_created", "admin_user_disabled", "admin_user_enabled",
	"admin_role_changed", "admin_session_revoked", "admin_invite_created", "admin_invite_revoked",
	"signing_key_created",
}
