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
	RequiresUserCreate    = "v1.3.0"
	RequiresUserEmails    = "v1.3.0"
	RequiresAuditFilters  = "v1.3.0"
	RequiresSSO           = "v1.4.0"
	RequiresIdentities    = "v1.4.0"
	RequiresChannelExpiry = "v1.7.0"
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

	// MaxChannelExpiryDays bounds channel_expiry_days (0 = never expire).
	MaxChannelExpiryDays = 3650
)

// NetworkSettings is GET /v1/admin/settings.
//
// ChannelAutoRegister and ChannelExpiryDays arrive with auth 1.7.0,
// GuestAccess (IRC lets people connect without an account) with 1.8.0,
// GuestVoice (guests may also join voice and video) with 1.18.0; they stay nil (and are left out of the panel's own JSON) on older releases.
type NetworkSettings struct {
	Registration             string     `json:"registration"`
	RequireEmailVerification bool       `json:"require_email_verification"`
	ChannelCreation          string     `json:"channel_creation"`
	VoiceChannelCreation     string     `json:"voice_channel_creation"`
	ChannelAutoRegister      *bool      `json:"channel_auto_register,omitempty"`
	ChannelExpiryDays        *int       `json:"channel_expiry_days,omitempty"`
	GuestAccess              *bool      `json:"guest_access,omitempty"`
	GuestVoice               *bool      `json:"guest_voice,omitempty"`
	UpdatedAt                *time.Time `json:"updated_at,omitempty"`
	UpdatedBy                *string    `json:"updated_by,omitempty"`
	// EmailConfigured: auth can send email (nil: older auth, assumed yes).
	EmailConfigured *bool `json:"email_configured,omitempty"`
	// RequireEmailVerificationEffective is what sign-in enforces: false
	// while email isn't configured even if RequireEmailVerification is on.
	RequireEmailVerificationEffective *bool `json:"require_email_verification_effective,omitempty"`
}

// SettingsPatch is PATCH /v1/admin/settings; nil fields are left alone.
type SettingsPatch struct {
	Registration             *string `json:"registration,omitempty"`
	RequireEmailVerification *bool   `json:"require_email_verification,omitempty"`
	ChannelCreation          *string `json:"channel_creation,omitempty"`
	VoiceChannelCreation     *string `json:"voice_channel_creation,omitempty"`
	ChannelAutoRegister      *bool   `json:"channel_auto_register,omitempty"`
	ChannelExpiryDays        *int    `json:"channel_expiry_days,omitempty"`
	GuestAccess              *bool   `json:"guest_access,omitempty"`
	GuestVoice               *bool   `json:"guest_voice,omitempty"`
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
// write-only: on update it is sent only when set (the stored one is kept
// otherwise); responses carry SSOProviderConfig.HasClientSecret instead.
type OIDCConfig struct {
	Issuer           string   `json:"issuer"`
	ClientID         string   `json:"client_id"`
	ClientSecret     string   `json:"client_secret,omitempty"`
	Scopes           []string `json:"scopes,omitempty"`
	UsernameClaim    string   `json:"username_claim,omitempty"`
	EmailClaim       string   `json:"email_claim,omitempty"`
	DisplayNameClaim string   `json:"display_name_claim,omitempty"`
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
	// NameIDFormat: persistent | email | unspecified | a URN; empty = the IdP's default.
	NameIDFormat string `json:"name_id_format,omitempty"`
	SignRequests bool   `json:"sign_requests,omitempty"`
}

// ProviderSettings holds the kind-specific config. The panel's own API
// nests it ({oidc} / {saml}); the auth service takes one flat object whose
// keys depend on the provider's kind (see wireConfig).
type ProviderSettings struct {
	OIDC *OIDCConfig `json:"oidc,omitempty"`
	SAML *SAMLConfig `json:"saml,omitempty"`
}

// SSOProviderConfig is an SSO provider as the admin API returns it.
type SSOProviderConfig struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
	Enabled     bool   `json:"enabled"`
	AllowSignup bool   `json:"allow_signup"`
	TrustEmail  bool   `json:"trust_email"`
	// SignupWhenClosed (auth 1.33+): AllowSignup also while registration is
	// closed or invite-only.
	SignupWhenClosed bool             `json:"signup_when_closed"`
	Config           ProviderSettings `json:"config"`
	HasClientSecret  bool             `json:"has_client_secret"`
	// URLs set by the auth service. StartURL begins a sign-in; RedirectURI
	// (OIDC) and the ACS / SP metadata / entity ID / certificate (SAML) go
	// to the identity provider.
	StartURL      string     `json:"start_url,omitempty"`
	RedirectURI   string     `json:"redirect_uri,omitempty"`
	ACSURL        string     `json:"acs_url,omitempty"`
	SPMetadataURL string     `json:"sp_metadata_url,omitempty"`
	SPEntityID    string     `json:"sp_entity_id,omitempty"`
	SPCertificate string     `json:"sp_certificate,omitempty"`
	CreatedAt     *time.Time `json:"created_at,omitempty"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
}

// wireProvider is the auth service's shape: config is flat.
type wireProvider struct {
	SSOProviderConfig
	Config json.RawMessage `json:"config"`
}

func (w *wireProvider) decode() (*SSOProviderConfig, error) {
	p := w.SSOProviderConfig
	if len(w.Config) > 0 && string(w.Config) != "null" {
		switch p.Kind {
		case KindOIDC:
			var c OIDCConfig
			if err := json.Unmarshal(w.Config, &c); err != nil {
				return nil, fmt.Errorf("%w: provider config: %v", apperr.ErrUpstream, err)
			}
			c.ClientSecret = ""
			p.Config = ProviderSettings{OIDC: &c}
		case KindSAML:
			var c SAMLConfig
			if err := json.Unmarshal(w.Config, &c); err != nil {
				return nil, fmt.Errorf("%w: provider config: %v", apperr.ErrUpstream, err)
			}
			p.Config = ProviderSettings{SAML: &c}
		}
	}
	return &p, nil
}

// SSOProviderInput creates (all of it) or patches (non-nil fields) a provider.
type SSOProviderInput struct {
	Slug             *string           `json:"slug,omitempty"`
	Kind             *string           `json:"kind,omitempty"`
	DisplayName      *string           `json:"display_name,omitempty"`
	Enabled          *bool             `json:"enabled,omitempty"`
	AllowSignup      *bool             `json:"allow_signup,omitempty"`
	TrustEmail       *bool             `json:"trust_email,omitempty"`
	SignupWhenClosed *bool             `json:"signup_when_closed,omitempty"`
	Config           *ProviderSettings `json:"config,omitempty"`
}

// wireInput is SSOProviderInput as the auth service takes it.
type wireInput struct {
	Slug             *string        `json:"slug,omitempty"`
	Kind             *string        `json:"kind,omitempty"`
	DisplayName      *string        `json:"display_name,omitempty"`
	Enabled          *bool          `json:"enabled,omitempty"`
	AllowSignup      *bool          `json:"allow_signup,omitempty"`
	TrustEmail       *bool          `json:"trust_email,omitempty"`
	SignupWhenClosed *bool          `json:"signup_when_closed,omitempty"`
	Config           map[string]any `json:"config,omitempty"`
}

// wireConfig flattens the kind-specific config. The auth service merges a
// PATCH key by key (a missing key keeps its value, null removes it), so on
// update every optional key the form owns is sent, empty ones as null; on
// create empty keys are left out. client_secret is only sent when set.
func wireConfig(c *ProviderSettings, update bool) map[string]any {
	if c == nil {
		return nil
	}
	m := map[string]any{}
	opt := func(k, v string) {
		switch {
		case v != "":
			m[k] = v
		case update:
			m[k] = nil
		}
	}
	if o := c.OIDC; o != nil {
		m["issuer"], m["client_id"] = o.Issuer, o.ClientID
		if o.ClientSecret != "" {
			m["client_secret"] = o.ClientSecret
		}
		if len(o.Scopes) > 0 {
			m["scopes"] = o.Scopes
		} else if update {
			m["scopes"] = nil
		}
		opt("username_claim", o.UsernameClaim)
		opt("email_claim", o.EmailClaim)
		opt("display_name_claim", o.DisplayNameClaim)
	}
	if sm := c.SAML; sm != nil {
		opt("idp_metadata_url", sm.IDPMetadataURL)
		opt("idp_metadata_xml", sm.IDPMetadataXML)
		opt("entity_id", sm.EntityID)
		opt("name_id_format", sm.NameIDFormat)
		m["attribute_map"] = sm.AttributeMap
		m["sign_requests"] = sm.SignRequests
	}
	return m
}

func (in SSOProviderInput) wire(update bool) wireInput {
	return wireInput{Slug: in.Slug, Kind: in.Kind, DisplayName: in.DisplayName, Enabled: in.Enabled,
		AllowSignup: in.AllowSignup, TrustEmail: in.TrustEmail, SignupWhenClosed: in.SignupWhenClosed, Config: wireConfig(in.Config, update)}
}

// SSOTestResult is POST /v1/admin/sso/providers/{id}/test.
type SSOTestResult struct {
	OK      bool            `json:"ok"`
	Details json.RawMessage `json:"details,omitempty"`
}

type providerEnvelope struct {
	Provider wireProvider `json:"provider"`
}

const ssoWhat = "Managing sign-in providers"

func providerPath(id string) string { return "/v1/admin/sso/providers/" + url.PathEscape(id) }

func (c *AuthClient) ListSSOProviders(ctx context.Context, at string) ([]SSOProviderConfig, error) {
	var out struct {
		Providers []wireProvider `json:"providers"`
	}
	if err := c.do(ctx, "admin.sso.list", http.MethodGet, "/v1/admin/sso/providers", at, nil, &out); err != nil {
		return nil, feature(err, ssoWhat, RequiresSSO)
	}
	list := make([]SSOProviderConfig, 0, len(out.Providers))
	for i := range out.Providers {
		p, err := out.Providers[i].decode()
		if err != nil {
			return nil, err
		}
		list = append(list, *p)
	}
	return list, nil
}

func (c *AuthClient) GetSSOProvider(ctx context.Context, at, id string) (*SSOProviderConfig, error) {
	var out providerEnvelope
	if err := c.do(ctx, "admin.sso.get", http.MethodGet, providerPath(id), at, nil, &out); err != nil {
		return nil, feature(err, ssoWhat, RequiresSSO)
	}
	return out.Provider.decode()
}

func (c *AuthClient) CreateSSOProvider(ctx context.Context, at string, in SSOProviderInput) (*SSOProviderConfig, error) {
	var out providerEnvelope
	if err := c.do(ctx, "admin.sso.create", http.MethodPost, "/v1/admin/sso/providers", at, in.wire(false), &out); err != nil {
		return nil, feature(err, ssoWhat, RequiresSSO)
	}
	return out.Provider.decode()
}

func (c *AuthClient) UpdateSSOProvider(ctx context.Context, at, id string, in SSOProviderInput) (*SSOProviderConfig, error) {
	var out providerEnvelope
	if err := c.do(ctx, "admin.sso.update", http.MethodPatch, providerPath(id), at, in.wire(true), &out); err != nil {
		return nil, feature(err, ssoWhat, RequiresSSO)
	}
	return out.Provider.decode()
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
	ID                  string     `json:"id"`
	ProviderID          string     `json:"provider_id"`
	ProviderSlug        string     `json:"provider_slug"`
	ProviderKind        string     `json:"provider_kind"`
	ProviderDisplayName string     `json:"provider_display_name"`
	Subject             string     `json:"subject"`
	Email               *string    `json:"email"`
	CreatedAt           *time.Time `json:"created_at,omitempty"`
	LastLoginAt         *time.Time `json:"last_login_at"`
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
	// InviteEmailSent is true when the set-your-password email was queued.
	InviteEmailSent bool `json:"invite_email_sent"`
	// SetupLink is the set-your-password link for the admin to share when
	// auth can't send email (InviteEmailSent false).
	SetupLink string `json:"setup_link,omitempty"`
}

// PasswordReset is the answer to POST /v1/admin/users/{id}/password-reset.
type PasswordReset struct {
	// EmailSent: the link was emailed. False when auth can't send email;
	// Link is then the reset link for the admin to share.
	EmailSent bool   `json:"email_sent"`
	Link      string `json:"link,omitempty"`
}

func (c *AuthClient) CreateUser(ctx context.Context, at string, in CreateUser) (*CreatedUser, error) {
	var out CreatedUser
	if err := c.do(ctx, "admin.users.create", http.MethodPost, "/v1/admin/users", at, in, &out); err != nil {
		return nil, feature(err, "Creating users", RequiresUserCreate)
	}
	return &out, nil
}

func (c *AuthClient) SendPasswordReset(ctx context.Context, at, id string) (*PasswordReset, error) {
	// Older auth answers {status} only: the link was emailed.
	out := PasswordReset{EmailSent: true}
	err := c.do(ctx, "admin.users.password_reset", http.MethodPost, "/v1/admin/users/"+url.PathEscape(id)+"/password-reset", at, nil, &out)
	if err != nil {
		return nil, feature(err, "Sending password resets", RequiresUserEmails)
	}
	if out.Link != "" {
		out.EmailSent = false
	}
	return &out, nil
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
