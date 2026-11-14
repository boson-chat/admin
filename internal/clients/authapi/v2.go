package authapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
)

// The auth release each newer admin endpoint needs. Shown to admins when the
// running auth service doesn't have the endpoint yet ("requires auth ≥ …").
const (
	RequiresSettings      = "v1.3.0"
	RequiresSSO           = "v1.3.0"
	RequiresUserCreate    = "v1.3.0"
	RequiresUserEmails    = "v1.3.0"
	RequiresIdentities    = "v1.3.0"
	RequiresAuditFilters  = "v1.3.0"
	unsupportedMsgPattern = "%s needs a newer auth service (auth ≥ %s)"
)

// missingRoute reports whether err means "this auth release has no such
// endpoint": Go's mux answers unknown routes with a plain-text 404 (no JSON
// error code) and known paths with the wrong method with 405.
func missingRoute(err error) bool {
	e, ok := apperr.As(err)
	if !ok {
		return false
	}
	return e.Status == http.StatusMethodNotAllowed || e.Status == http.StatusNotImplemented ||
		(e.Status == http.StatusNotFound && e.Code == "upstream_404")
}

// feature turns a missing-route error into apperr.Unsupported.
func feature(err error, what, version string) error {
	if missingRoute(err) {
		return apperr.Unsupported(fmt.Sprintf(unsupportedMsgPattern, what, version))
	}
	return err
}

// ---- network settings ----

// Registration modes, text-channel and voice-channel creation policies.
const (
	RegistrationOpen   = "open"
	RegistrationInvite = "invite"
	RegistrationClosed = "closed"

	CreateRegistered = "registered"
	CreateAdmins     = "admins"
	CreateFounders   = "founders"
)

// NetworkSettings is GET /v1/admin/settings.
type NetworkSettings struct {
	Registration             string     `json:"registration"`
	RequireEmailVerification bool       `json:"require_email_verification"`
	ChannelCreation          string     `json:"channel_creation"`
	VoiceChannelCreation     string     `json:"voice_channel_creation"`
	UpdatedAt                *time.Time `json:"updated_at,omitempty"`
	UpdatedBy                *string    `json:"updated_by,omitempty"`
}

// SettingsPatch is PATCH /v1/admin/settings; nil fields are left alone.
type SettingsPatch struct {
	Registration             *string `json:"registration,omitempty"`
	RequireEmailVerification *bool   `json:"require_email_verification,omitempty"`
	ChannelCreation          *string `json:"channel_creation,omitempty"`
	VoiceChannelCreation     *string `json:"voice_channel_creation,omitempty"`
}

// settingsEnvelope accepts both {"registration":…} and {"settings":{…}}.
type settingsEnvelope struct {
	NetworkSettings
	Settings *NetworkSettings `json:"settings"`
}

func (e settingsEnvelope) get() *NetworkSettings {
	if e.Settings != nil {
		return e.Settings
	}
	return &e.NetworkSettings
}

func (c *AuthClient) GetSettings(ctx context.Context, at string) (*NetworkSettings, error) {
	var out settingsEnvelope
	if err := c.do(ctx, "admin.settings.get", http.MethodGet, "/v1/admin/settings", at, nil, &out); err != nil {
		return nil, feature(err, "Network settings", RequiresSettings)
	}
	return out.get(), nil
}

func (c *AuthClient) UpdateSettings(ctx context.Context, at string, p SettingsPatch) (*NetworkSettings, error) {
	var out settingsEnvelope
	if err := c.do(ctx, "admin.settings.update", http.MethodPatch, "/v1/admin/settings", at, p, &out); err != nil {
		return nil, feature(err, "Network settings", RequiresSettings)
	}
	return out.get(), nil
}

// ---- SSO providers ----

const (
	KindOIDC = "oidc"
	KindSAML = "saml"
)

// OIDCConfig is a provider's OpenID Connect settings. ClientSecret is
// write-only: responses carry HasClientSecret instead.
type OIDCConfig struct {
	Issuer          string   `json:"issuer"`
	ClientID        string   `json:"client_id"`
	ClientSecret    string   `json:"client_secret,omitempty"`
	HasClientSecret bool     `json:"has_client_secret,omitempty"`
	Scopes          []string `json:"scopes,omitempty"`
	UsernameClaim   string   `json:"username_claim,omitempty"`
	EmailClaim      string   `json:"email_claim,omitempty"`
}

// SAMLAttributeMap names the IdP attributes boson reads.
type SAMLAttributeMap struct {
	Email       string `json:"email,omitempty"`
	Username    string `json:"username,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

// SAMLConfig is a provider's SAML settings: metadata by URL or inline XML.
type SAMLConfig struct {
	IDPMetadataURL string           `json:"idp_metadata_url,omitempty"`
	IDPMetadataXML string           `json:"idp_metadata_xml,omitempty"`
	EntityID       string           `json:"entity_id,omitempty"`
	AttributeMap   SAMLAttributeMap `json:"attribute_map"`
}

// ProviderSettings holds the kind-specific config.
type ProviderSettings struct {
	OIDC *OIDCConfig `json:"oidc,omitempty"`
	SAML *SAMLConfig `json:"saml,omitempty"`
}

// SSOProviderConfig is an SSO provider as the admin API returns it.
type SSOProviderConfig struct {
	ID          string           `json:"id"`
	Slug        string           `json:"slug"`
	Kind        string           `json:"kind"`
	DisplayName string           `json:"display_name"`
	Enabled     bool             `json:"enabled"`
	AllowSignup bool             `json:"allow_signup"`
	TrustEmail  bool             `json:"trust_email"`
	Config      ProviderSettings `json:"config"`
	// Where the IdP sends people back, set by the auth service.
	CallbackURL   string     `json:"callback_url,omitempty"`
	SPMetadataURL string     `json:"sp_metadata_url,omitempty"`
	ACSURL        string     `json:"acs_url,omitempty"`
	SPEntityID    string     `json:"sp_entity_id,omitempty"`
	CreatedAt     *time.Time `json:"created_at,omitempty"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
}

// SSOProviderInput creates (all of it) or patches (non-nil fields) a provider.
type SSOProviderInput struct {
	Slug        *string           `json:"slug,omitempty"`
	Kind        *string           `json:"kind,omitempty"`
	DisplayName *string           `json:"display_name,omitempty"`
	Enabled     *bool             `json:"enabled,omitempty"`
	AllowSignup *bool             `json:"allow_signup,omitempty"`
	TrustEmail  *bool             `json:"trust_email,omitempty"`
	Config      *ProviderSettings `json:"config,omitempty"`
}

// SSOTestResult is POST /v1/admin/sso/providers/{id}/test.
type SSOTestResult struct {
	OK      bool            `json:"ok"`
	Details json.RawMessage `json:"details,omitempty"`
}

type providerEnvelope struct {
	Provider SSOProviderConfig `json:"provider"`
}

const ssoWhat = "Managing sign-in providers"

func providerPath(id string) string { return "/v1/admin/sso/providers/" + url.PathEscape(id) }

func (c *AuthClient) ListSSOProviders(ctx context.Context, at string) ([]SSOProviderConfig, error) {
	var out struct {
		Providers []SSOProviderConfig `json:"providers"`
	}
	if err := c.do(ctx, "admin.sso.list", http.MethodGet, "/v1/admin/sso/providers", at, nil, &out); err != nil {
		return nil, feature(err, ssoWhat, RequiresSSO)
	}
	if out.Providers == nil {
		out.Providers = []SSOProviderConfig{}
	}
	return out.Providers, nil
}

func (c *AuthClient) GetSSOProvider(ctx context.Context, at, id string) (*SSOProviderConfig, error) {
	var out providerEnvelope
	if err := c.do(ctx, "admin.sso.get", http.MethodGet, providerPath(id), at, nil, &out); err != nil {
		return nil, feature(err, ssoWhat, RequiresSSO)
	}
	return &out.Provider, nil
}

func (c *AuthClient) CreateSSOProvider(ctx context.Context, at string, in SSOProviderInput) (*SSOProviderConfig, error) {
	var out providerEnvelope
	if err := c.do(ctx, "admin.sso.create", http.MethodPost, "/v1/admin/sso/providers", at, in, &out); err != nil {
		return nil, feature(err, ssoWhat, RequiresSSO)
	}
	return &out.Provider, nil
}

func (c *AuthClient) UpdateSSOProvider(ctx context.Context, at, id string, in SSOProviderInput) (*SSOProviderConfig, error) {
	var out providerEnvelope
	if err := c.do(ctx, "admin.sso.update", http.MethodPatch, providerPath(id), at, in, &out); err != nil {
		return nil, feature(err, ssoWhat, RequiresSSO)
	}
	return &out.Provider, nil
}

func (c *AuthClient) DeleteSSOProvider(ctx context.Context, at, id string) error {
	return feature(c.do(ctx, "admin.sso.delete", http.MethodDelete, providerPath(id), at, nil, nil), ssoWhat, RequiresSSO)
}

func (c *AuthClient) TestSSOProvider(ctx context.Context, at, id string) (*SSOTestResult, error) {
	var out SSOTestResult
	if err := c.do(ctx, "admin.sso.test", http.MethodPost, providerPath(id)+"/test", at, nil, &out); err != nil {
		return nil, feature(err, ssoWhat, RequiresSSO)
	}
	return &out, nil
}

// ---- users ----

// Identity is a linked SSO sign-in of a user.
type Identity struct {
	ID           string     `json:"id"`
	ProviderID   string     `json:"provider_id,omitempty"`
	ProviderSlug string     `json:"provider_slug,omitempty"`
	ProviderName string     `json:"provider_name,omitempty"`
	Kind         string     `json:"kind,omitempty"`
	Subject      string     `json:"subject"`
	Email        string     `json:"email,omitempty"`
	CreatedAt    *time.Time `json:"created_at,omitempty"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
}

// CreateUser is POST /v1/admin/users. Either Password or SendInviteEmail.
type CreateUser struct {
	Username        string  `json:"username"`
	Email           string  `json:"email,omitempty"`
	Role            string  `json:"role,omitempty"`
	Password        *string `json:"password,omitempty"`
	SendInviteEmail bool    `json:"send_invite_email,omitempty"`
}

// CreatedUser is the create response.
type CreatedUser struct {
	User User `json:"user"`
	// InviteSent is true when a set-your-password email went out.
	InviteSent bool `json:"invite_sent,omitempty"`
}

func (c *AuthClient) CreateUser(ctx context.Context, at string, in CreateUser) (*CreatedUser, error) {
	var out CreatedUser
	if err := c.do(ctx, "admin.users.create", http.MethodPost, "/v1/admin/users", at, in, &out); err != nil {
		return nil, feature(err, "Creating users", RequiresUserCreate)
	}
	return &out, nil
}

func (c *AuthClient) SendPasswordReset(ctx context.Context, at, id string) error {
	err := c.do(ctx, "admin.users.password_reset", http.MethodPost, "/v1/admin/users/"+url.PathEscape(id)+"/password-reset", at, nil, nil)
	return feature(err, "Sending password resets", RequiresUserEmails)
}

func (c *AuthClient) ResendVerification(ctx context.Context, at, id string) error {
	err := c.do(ctx, "admin.users.verification", http.MethodPost, "/v1/admin/users/"+url.PathEscape(id)+"/verification", at, nil, nil)
	return feature(err, "Resending verification emails", RequiresUserEmails)
}

// ---- audit ----

// AuditQuery is GET /v1/admin/audit. Event is exact or a prefix ending in
// "*". Actor/Target are user IDs or usernames.
type AuditQuery struct {
	Event  string
	Actor  string
	Target string
	Since  time.Time
	Until  time.Time
	Cursor string
	Limit  int
}

// AuditPage is one page of the audit log.
type AuditPage struct {
	Entries    []AuditEntry
	NextCursor string
	// Supported is false when the auth service ignored the filters (no
	// next_cursor field in the response): the caller filters itself.
	Supported bool
}

func (c *AuthClient) QueryAudit(ctx context.Context, at string, aq AuditQuery) (*AuditPage, error) {
	q := url.Values{}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("event", aq.Event)
	set("actor", aq.Actor)
	set("target", aq.Target)
	set("cursor", aq.Cursor)
	if !aq.Since.IsZero() {
		q.Set("since", aq.Since.UTC().Format(time.RFC3339))
	}
	if !aq.Until.IsZero() {
		q.Set("until", aq.Until.UTC().Format(time.RFC3339))
	}
	// Older releases filter by actor ID only, as user_id.
	if isUserID(aq.Actor) {
		q.Set("user_id", aq.Actor)
	}
	if aq.Limit > 0 {
		q.Set("limit", strconv.Itoa(aq.Limit))
	}
	var out struct {
		Entries    []AuditEntry    `json:"entries"`
		NextCursor json.RawMessage `json:"next_cursor"`
	}
	if err := c.do(ctx, "admin.audit.list", http.MethodGet, "/v1/admin/audit?"+q.Encode(), at, nil, &out); err != nil {
		return nil, err
	}
	p := &AuditPage{Entries: out.Entries, Supported: out.NextCursor != nil}
	if p.Entries == nil {
		p.Entries = []AuditEntry{}
	}
	if p.Supported {
		_ = json.Unmarshal(out.NextCursor, &p.NextCursor) // null → ""
	}
	return p, nil
}

// isUserID reports whether s looks like an auth user ID (usr_<ULID>).
func isUserID(s string) bool {
	return strings.HasPrefix(s, "usr_") && !strings.ContainsAny(s, " /?#")
}
