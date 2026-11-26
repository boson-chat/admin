package authapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// RequiresFederation is the auth release with federated login (the
// federation v1 spec, auth docs/federation.md).
const RequiresFederation = "v1.24.0"

const federationWhat = "Federation"

// Federation trust modes (FEDERATION_MODE, auth.federation.mode): which home
// servers' users may sign in here.
const (
	FederationOff       = "off"
	FederationAllowlist = "allowlist"
	FederationDirectory = "directory"
	FederationOpen      = "open"
)

// FederationModes in order from closed to open.
var FederationModes = []string{FederationOff, FederationAllowlist, FederationDirectory, FederationOpen}

// FederationSettings are this server's rules as a remote: who may sign in
// with a home account elsewhere, and what remote users get.
type FederationSettings struct {
	Mode  string   `json:"mode"`
	Allow []string `json:"allow"`
	Block []string `json:"block"`
	// DirectoryURL is the directory whose verified listings "directory" mode
	// trusts (default https://directory.boson.chat).
	DirectoryURL string `json:"directory_url"`
	// RemoteUploadQuota is remote users' rolling upload quota ("1GB"; ""
	// = the account quota).
	RemoteUploadQuota string `json:"remote_upload_quota"`
	// RemoteVoice: remote users may join voice rooms (and so video).
	RemoteVoice bool `json:"remote_voice"`
}

// FederationStatus is GET /v1/admin/federation: the settings auth runs
// with, where each comes from, and this server as a home.
type FederationStatus struct {
	FederationSettings
	// Sources: per setting, "file" (config file), "database" (changed
	// through the admin API), "env" or "default".
	Sources map[string]string `json:"sources,omitempty"`
	// Editable: PATCH /v1/admin/federation is accepted (no config file:
	// auth keeps the settings in its database).
	Editable bool `json:"editable"`
	// Issuer is this auth's token issuer (boson.json's federation.issuer).
	Issuer string `json:"issuer,omitempty"`
	// Domain is the home domain this server's users carry (boson_domain).
	Domain string `json:"domain,omitempty"`
	// RemoteUsers is how many federated accounts exist here.
	RemoteUsers int `json:"remote_users"`
	// RemoteRole is the role new federated accounts get
	// (FEDERATION_REMOTE_ROLE; "" = none).
	RemoteRole string `json:"remote_role"`
	// UpdatedAt / UpdatedBy: the last change through PATCH (database store).
	UpdatedAt *time.Time `json:"updated_at"`
	UpdatedBy *string    `json:"updated_by"`
}

// FederationPatch is PATCH /v1/admin/federation: only the fields set
// change; Reset fields are sent as null (back to auth's env value).
type FederationPatch struct {
	Mode              *string   `json:"mode,omitempty"`
	Allow             *[]string `json:"allow,omitempty"`
	Block             *[]string `json:"block,omitempty"`
	DirectoryURL      *string   `json:"directory_url,omitempty"`
	RemoteUploadQuota *string   `json:"remote_upload_quota,omitempty"`
	RemoteVoice       *bool     `json:"remote_voice,omitempty"`
	// Reset names fields to send as null.
	Reset []string `json:"-"`
}

// Empty: nothing to change.
func (p FederationPatch) Empty() bool {
	return p.Mode == nil && p.Allow == nil && p.Block == nil && p.DirectoryURL == nil &&
		p.RemoteUploadQuota == nil && p.RemoteVoice == nil && len(p.Reset) == 0
}

// MarshalJSON writes the set fields plus a null per Reset field.
func (p FederationPatch) MarshalJSON() ([]byte, error) {
	type plain FederationPatch
	b, err := json.Marshal(plain(p))
	if err != nil || len(p.Reset) == 0 {
		return b, err
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	for _, f := range p.Reset {
		if _, set := m[f]; !set {
			m[f] = nil
		}
	}
	return json.Marshal(m)
}

// RemoteUser is a federated account: someone from another server who
// signed in here with their home account.
type RemoteUser struct {
	// ID is the local account's ID.
	ID string `json:"id"`
	// Identity is how they're shown: alice@boson.chat.
	Identity string `json:"identity"`
	// Handle is the local handle: their IRC account name and nick here.
	Handle      string     `json:"handle"`
	HomeDomain  string     `json:"home_domain"`
	Issuer      string     `json:"issuer,omitempty"`
	Subject     string     `json:"subject,omitempty"`
	DisplayName string     `json:"display_name,omitempty"`
	Picture     *string    `json:"picture,omitempty"`
	FirstSeenAt time.Time  `json:"first_seen_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	// LastSeenAt is the later of the last sign-in and the last activity.
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	Banned     bool       `json:"banned"`
	BannedAt   *time.Time `json:"banned_at,omitempty"`
	BanReason  string     `json:"ban_reason,omitempty"`
	// DomainBlocked: their home domain is on the block list.
	DomainBlocked bool `json:"domain_blocked,omitempty"`
	// Disabled: the local account is disabled (a ban does that, so IRC
	// drops them; an admin can too).
	Disabled bool `json:"disabled,omitempty"`
	Sessions int  `json:"sessions"`
	// Applying is the panel's own: its saved block list and auth's answer
	// differ because auth hasn't re-read the config file yet.
	Applying bool `json:"applying,omitempty"`
}

// RemoteUserQuery filters GET /v1/admin/federation/users.
type RemoteUserQuery struct {
	Q      string
	Domain string
	Cursor string
	Limit  int
}

// RemoteUserPage is one page of remote users, newest last seen first.
type RemoteUserPage struct {
	Users      []RemoteUser `json:"users"`
	NextCursor string       `json:"next_cursor,omitempty"`
	Total      int          `json:"total"`
}

// RevokedSessions is how many sessions a domain block ended.
type RevokedSessions struct {
	Domain  string `json:"domain"`
	Revoked int    `json:"revoked"`
}

// FederationClient is auth's federation admin API.
type FederationClient interface {
	FederationStatus(ctx context.Context, at string) (*FederationStatus, error)
	UpdateFederation(ctx context.Context, at string, p FederationPatch) (*FederationStatus, error)
	RemoteUsers(ctx context.Context, at string, q RemoteUserQuery) (*RemoteUserPage, error)
	BanRemoteUser(ctx context.Context, at, id, reason string) (*RemoteUser, error)
	UnbanRemoteUser(ctx context.Context, at, id string) (*RemoteUser, error)
	// RevokeDomainSessions ends every session of a home domain's users.
	RevokeDomainSessions(ctx context.Context, at, domain string) (*RevokedSessions, error)
}

var _ FederationClient = (*AuthClient)(nil)

func (c *AuthClient) fed(ctx context.Context, op, method, path, at string, body, out any) error {
	return feature(c.do(ctx, op, method, path, at, body, out), federationWhat, RequiresFederation)
}

// FederationStatus is GET /v1/admin/federation.
func (c *AuthClient) FederationStatus(ctx context.Context, at string) (*FederationStatus, error) {
	var out FederationStatus
	if err := c.fed(ctx, "admin.federation.get", http.MethodGet, "/v1/admin/federation", at, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateFederation is PATCH /v1/admin/federation (auth without a config file).
func (c *AuthClient) UpdateFederation(ctx context.Context, at string, p FederationPatch) (*FederationStatus, error) {
	var out FederationStatus
	if err := c.fed(ctx, "admin.federation.update", http.MethodPatch, "/v1/admin/federation", at, p, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RemoteUsers is GET /v1/admin/federation/users.
func (c *AuthClient) RemoteUsers(ctx context.Context, at string, q RemoteUserQuery) (*RemoteUserPage, error) {
	v := url.Values{}
	if q.Q != "" {
		v.Set("q", q.Q)
	}
	if q.Domain != "" {
		v.Set("domain", q.Domain)
	}
	if q.Cursor != "" {
		v.Set("cursor", q.Cursor)
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	path := "/v1/admin/federation/users"
	if len(v) > 0 {
		path += "?" + v.Encode()
	}
	var out RemoteUserPage
	if err := c.fed(ctx, "admin.federation.users", http.MethodGet, path, at, nil, &out); err != nil {
		return nil, err
	}
	if out.Users == nil {
		out.Users = []RemoteUser{}
	}
	return &out, nil
}

func remoteUserPath(id string) string {
	return "/v1/admin/federation/users/" + url.PathEscape(id) + "/ban"
}

// BanRemoteUser is POST /v1/admin/federation/users/{id}/ban: the identity
// can't sign in here again and its sessions end.
func (c *AuthClient) BanRemoteUser(ctx context.Context, at, id, reason string) (*RemoteUser, error) {
	var out RemoteUser
	if err := c.fed(ctx, "admin.federation.ban", http.MethodPost, remoteUserPath(id), at, map[string]string{"reason": reason}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UnbanRemoteUser is DELETE /v1/admin/federation/users/{id}/ban.
func (c *AuthClient) UnbanRemoteUser(ctx context.Context, at, id string) (*RemoteUser, error) {
	var out RemoteUser
	if err := c.fed(ctx, "admin.federation.unban", http.MethodDelete, remoteUserPath(id), at, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RevokeDomainSessions is POST /v1/admin/federation/domains/{domain}/revoke-sessions.
func (c *AuthClient) RevokeDomainSessions(ctx context.Context, at, domain string) (*RevokedSessions, error) {
	var out RevokedSessions
	path := "/v1/admin/federation/domains/" + url.PathEscape(domain) + "/revoke-sessions"
	if err := c.fed(ctx, "admin.federation.revoke_domain", http.MethodPost, path, at, nil, &out); err != nil {
		return nil, err
	}
	if out.Domain == "" {
		out.Domain = domain
	}
	return &out, nil
}
