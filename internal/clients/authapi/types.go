package authapi

import (
	"context"
	"encoding/json"
	"time"
)

// User is the auth service's user representation.
type User struct {
	ID            string     `json:"id"`
	Username      string     `json:"username"`
	Email         *string    `json:"email"`
	EmailVerified bool       `json:"email_verified"`
	DisplayName   string     `json:"display_name"`
	Role          string     `json:"role"`
	CreatedAt     time.Time  `json:"created_at"`
	DisabledAt    *time.Time `json:"disabled_at,omitempty"`
	// Identities are linked SSO sign-ins. nil = the auth release doesn't
	// report them (older than RequiresIdentities); empty = none linked.
	Identities *[]Identity `json:"identities,omitempty"`
	// HasPassword: false for SSO-only accounts (auth >= 1.4.0 reports it).
	HasPassword *bool `json:"has_password,omitempty"`
	// Roles and Permissions (effective) come with auth >= 1.5.0; nil = the
	// auth release has no roles.
	Roles       *[]RoleRef `json:"roles,omitempty"`
	Permissions *[]string  `json:"permissions,omitempty"`

	// Sign-in / activity history (newer auth releases). Nil = never
	// signed in, or the auth release doesn't report it: ActivityTracked
	// tells the two apart.
	LastSignInAt     *time.Time `json:"last_sign_in_at"`
	LastSignInMethod *string    `json:"last_sign_in_method"`
	LastSignInIP     *string    `json:"last_sign_in_ip"`
	LastActiveAt     *time.Time `json:"last_active_at"`
	// ActivityTracked: the auth response had last_active_at (even null).
	ActivityTracked bool `json:"activity_tracked"`
}

// UnmarshalJSON decodes a user and notes whether auth reported activity.
func (u *User) UnmarshalJSON(b []byte) error {
	type plain User
	var probe struct {
		LastActiveAt    json.RawMessage `json:"last_active_at"`
		ActivityTracked bool            `json:"activity_tracked"`
	}
	if err := json.Unmarshal(b, (*plain)(u)); err != nil {
		return err
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return err
	}
	u.ActivityTracked = probe.LastActiveAt != nil || probe.ActivityTracked
	return nil
}

// Tokens is the login / refresh response.
type Tokens struct {
	TokenType        string `json:"token_type"`
	AccessToken      string `json:"access_token"`
	ExpiresIn        int64  `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int64  `json:"refresh_expires_in"`
	SessionID        string `json:"session_id"`
	User             User   `json:"user"`
}

// Session is an active login of some user.
type Session struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id,omitempty"`
	AuthMethod string    `json:"auth_method"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// Invite is a registration invite. Code is only set in the create response.
type Invite struct {
	ID        string     `json:"id"`
	Code      string     `json:"code,omitempty"`
	Note      string     `json:"note"`
	MaxUses   int        `json:"max_uses"`
	Uses      int        `json:"uses"`
	CreatedBy *string    `json:"created_by"`
	ExpiresAt *time.Time `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// CreateInvite is the body of POST /v1/admin/invites.
type CreateInvite struct {
	MaxUses int    `json:"max_uses"`
	TTL     string `json:"ttl,omitempty"`
	Note    string `json:"note"`
}

// AuditEntry is one audit_log row. UserID is the actor (the target of an
// admin action is in Meta["user_id"], or TargetUserID on newer releases).
type AuditEntry struct {
	ID     string          `json:"id"`
	UserID *string         `json:"user_id"`
	Event  string          `json:"event"`
	IP     string          `json:"ip"`
	Meta   json.RawMessage `json:"meta"`
	At     time.Time       `json:"at"`
	// Set by auth releases with server-side audit filters.
	ActorUsername  string `json:"actor_username,omitempty"`
	TargetUserID   string `json:"target_user_id,omitempty"`
	TargetUsername string `json:"target_username,omitempty"`
}

// SSOProvider is one configured provider from /v1/config.
type SSOProvider struct {
	Slug        string `json:"slug"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
}

// PublicConfig is GET /v1/config.
type PublicConfig struct {
	Registration             string        `json:"registration"`
	RequireEmailVerification bool          `json:"require_email_verification"`
	SSOProviders             []SSOProvider `json:"sso_providers"`
}

// UserList is GET /v1/admin/users.
type UserList struct {
	Users []User `json:"users"`
	Total int64  `json:"total"`
	// ActivitySupported: auth reported last sign-in / activity, so it also
	// honours the sort (older releases ignore it). Set by the client.
	ActivitySupported bool `json:"activity_supported"`
}

// User list sorts (newest first; auth releases without activity ignore them).
const (
	SortCreated    = "created"
	SortLastActive = "last_active"
	SortLastSignIn = "last_sign_in"
)

// ValidSort reports whether s is a user list sort ("" = created).
func ValidSort(s string) bool {
	switch s {
	case "", SortCreated, SortLastActive, SortLastSignIn:
		return true
	}
	return false
}

// ClientMeta is forwarded so the auth service records the admin's real
// IP and browser on sessions and in the audit log (the IP only counts when
// the panel is in the auth service's TRUSTED_PROXIES).
type ClientMeta struct {
	IP        string
	UserAgent string
}

type metaKey struct{}

// WithMeta returns ctx carrying the admin's client metadata.
func WithMeta(ctx context.Context, m ClientMeta) context.Context {
	return context.WithValue(ctx, metaKey{}, m)
}

// MetaFrom returns the client metadata in ctx.
func MetaFrom(ctx context.Context) (ClientMeta, bool) {
	m, ok := ctx.Value(metaKey{}).(ClientMeta)
	return m, ok
}
