package admin

import (
	"context"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/session"
)

// Settings is the network settings plus the name of who last changed them.
type Settings struct {
	authapi.NetworkSettings
	UpdatedByUsername string `json:"updated_by_username,omitempty"`
}

var (
	registrationModes = []string{authapi.RegistrationOpen, authapi.RegistrationInvite, authapi.RegistrationClosed}
	textCreators      = []string{authapi.CreateRegistered, authapi.CreateAdmins}
	voiceCreators     = []string{authapi.CreateAdmins, authapi.CreateFounders, authapi.CreateRegistered}
)

func oneOf(v *string, allowed []string, name string) error {
	if v != nil && !slices.Contains(allowed, *v) {
		return apperr.Invalid(name + " must be one of " + strings.Join(allowed, ", "))
	}
	return nil
}

func (s *AdminService) withUpdater(ctx context.Context, sess *session.Session, n *authapi.NetworkSettings) *Settings {
	out := &Settings{NetworkSettings: *n}
	if n.UpdatedBy != nil && *n.UpdatedBy != "" {
		if u, err := s.GetUser(ctx, sess, *n.UpdatedBy); err == nil {
			out.UpdatedByUsername = u.Username
		}
	}
	return out
}

func (s *AdminService) GetSettings(ctx context.Context, sess *session.Session) (*Settings, error) {
	var n *authapi.NetworkSettings
	if err := s.call(ctx, sess, func(at string) (err error) { n, err = s.Client.GetSettings(ctx, at); return }); err != nil {
		return nil, err
	}
	return s.withUpdater(ctx, sess, n), nil
}

func (s *AdminService) UpdateSettings(ctx context.Context, sess *session.Session, p authapi.SettingsPatch) (*Settings, error) {
	if err := oneOf(p.Registration, registrationModes, "registration"); err != nil {
		return nil, err
	}
	if err := oneOf(p.ChannelCreation, textCreators, "channel_creation"); err != nil {
		return nil, err
	}
	if err := oneOf(p.VoiceChannelCreation, voiceCreators, "voice_channel_creation"); err != nil {
		return nil, err
	}
	if p == (authapi.SettingsPatch{}) {
		return nil, apperr.Invalid("nothing to update")
	}
	var n *authapi.NetworkSettings
	if err := s.call(ctx, sess, func(at string) (err error) { n, err = s.Client.UpdateSettings(ctx, at, p); return }); err != nil {
		return nil, err
	}
	return s.withUpdater(ctx, sess, n), nil
}

// ---- SSO ----

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,38}[a-z0-9]$`)

func httpsURL(v, name string) error {
	u, err := url.Parse(v)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return apperr.Invalid(name + " must be an https:// URL")
	}
	return nil
}

// validateProvider checks a create (full) or patch (partial) input.
func validateProvider(in *authapi.SSOProviderInput, create bool) error {
	if in.Slug != nil {
		*in.Slug = strings.ToLower(strings.TrimSpace(*in.Slug))
		if !slugRe.MatchString(*in.Slug) {
			return apperr.Invalid("slug must be 2–40 lowercase letters, digits or dashes, e.g. google or acme-okta")
		}
	} else if create {
		return apperr.Invalid("slug is required")
	}
	if in.DisplayName != nil {
		*in.DisplayName = strings.TrimSpace(*in.DisplayName)
		if *in.DisplayName == "" || len(*in.DisplayName) > 60 {
			return apperr.Invalid("display name must be 1–60 characters")
		}
	} else if create {
		return apperr.Invalid("display name is required")
	}
	kind := ""
	if in.Kind != nil {
		kind = *in.Kind
		if kind != authapi.KindOIDC && kind != authapi.KindSAML {
			return apperr.Invalid("kind must be oidc or saml")
		}
	} else if create {
		return apperr.Invalid("kind is required")
	}
	if in.Config == nil {
		if create {
			return apperr.Invalid("config is required")
		}
		return nil
	}
	if o := in.Config.OIDC; o != nil {
		if kind == authapi.KindSAML {
			return apperr.Invalid("a SAML provider takes saml config")
		}
		o.Issuer, o.ClientID = strings.TrimSpace(o.Issuer), strings.TrimSpace(o.ClientID)
		if err := httpsURL(o.Issuer, "issuer"); err != nil {
			return err
		}
		if o.ClientID == "" {
			return apperr.Invalid("client ID is required")
		}
		if create && o.ClientSecret == "" {
			return apperr.Invalid("client secret is required")
		}
		o.HasClientSecret = false // response-only
		scopes := o.Scopes[:0]
		for _, sc := range o.Scopes {
			if sc = strings.TrimSpace(sc); sc != "" && !slices.Contains(scopes, sc) {
				scopes = append(scopes, sc)
			}
		}
		if !slices.Contains(scopes, "openid") {
			scopes = append([]string{"openid"}, scopes...)
		}
		o.Scopes = scopes
	}
	if sm := in.Config.SAML; sm != nil {
		if kind == authapi.KindOIDC {
			return apperr.Invalid("an OIDC provider takes oidc config")
		}
		sm.IDPMetadataURL, sm.IDPMetadataXML = strings.TrimSpace(sm.IDPMetadataURL), strings.TrimSpace(sm.IDPMetadataXML)
		switch {
		case sm.IDPMetadataURL == "" && sm.IDPMetadataXML == "":
			return apperr.Invalid("give the IdP metadata URL or paste its XML")
		case sm.IDPMetadataURL != "" && sm.IDPMetadataXML != "":
			return apperr.Invalid("give the IdP metadata URL or its XML, not both")
		case sm.IDPMetadataURL != "":
			if err := httpsURL(sm.IDPMetadataURL, "IdP metadata URL"); err != nil {
				return err
			}
		case !strings.Contains(sm.IDPMetadataXML, "EntityDescriptor"):
			return apperr.Invalid("the pasted metadata isn't SAML metadata (no EntityDescriptor)")
		case len(sm.IDPMetadataXML) > 48<<10:
			return apperr.Invalid("the pasted metadata is over 48 KB; use its URL instead")
		}
	}
	if create && in.Config.OIDC == nil && in.Config.SAML == nil {
		return apperr.Invalid("config is required")
	}
	return nil
}

func (s *AdminService) ListSSOProviders(ctx context.Context, sess *session.Session) ([]authapi.SSOProviderConfig, error) {
	var out []authapi.SSOProviderConfig
	err := s.call(ctx, sess, func(at string) (err error) { out, err = s.Client.ListSSOProviders(ctx, at); return })
	return out, err
}

func (s *AdminService) GetSSOProvider(ctx context.Context, sess *session.Session, id string) (*authapi.SSOProviderConfig, error) {
	var out *authapi.SSOProviderConfig
	err := s.call(ctx, sess, func(at string) (err error) { out, err = s.Client.GetSSOProvider(ctx, at, id); return })
	return out, err
}

func (s *AdminService) CreateSSOProvider(ctx context.Context, sess *session.Session, in authapi.SSOProviderInput) (*authapi.SSOProviderConfig, error) {
	if err := validateProvider(&in, true); err != nil {
		return nil, err
	}
	var out *authapi.SSOProviderConfig
	err := s.call(ctx, sess, func(at string) (err error) { out, err = s.Client.CreateSSOProvider(ctx, at, in); return })
	return out, err
}

func (s *AdminService) UpdateSSOProvider(ctx context.Context, sess *session.Session, id string, in authapi.SSOProviderInput) (*authapi.SSOProviderConfig, error) {
	if in.Kind != nil {
		return nil, apperr.Invalid("a provider's kind can't change; add a new provider instead")
	}
	if in.Config != nil {
		// The kind decides which config is allowed; read it.
		cur, err := s.GetSSOProvider(ctx, sess, id)
		if err != nil {
			return nil, err
		}
		in.Kind = &cur.Kind
	}
	if err := validateProvider(&in, false); err != nil {
		return nil, err
	}
	in.Kind = nil
	var out *authapi.SSOProviderConfig
	err := s.call(ctx, sess, func(at string) (err error) { out, err = s.Client.UpdateSSOProvider(ctx, at, id, in); return })
	return out, err
}

func (s *AdminService) DeleteSSOProvider(ctx context.Context, sess *session.Session, id string) error {
	return s.call(ctx, sess, func(at string) error { return s.Client.DeleteSSOProvider(ctx, at, id) })
}

func (s *AdminService) TestSSOProvider(ctx context.Context, sess *session.Session, id string) (*authapi.SSOTestResult, error) {
	var out *authapi.SSOTestResult
	err := s.call(ctx, sess, func(at string) (err error) { out, err = s.Client.TestSSOProvider(ctx, at, id); return })
	return out, err
}

// ---- users ----

func (s *AdminService) CreateUser(ctx context.Context, sess *session.Session, in authapi.CreateUser) (*authapi.CreatedUser, error) {
	in.Username, in.Email = strings.TrimSpace(in.Username), strings.TrimSpace(in.Email)
	// The auth service applies the IRC nick rules; this only catches typos.
	if in.Username == "" || len(in.Username) > 30 || strings.ContainsAny(in.Username, " \t@.") {
		return nil, apperr.Invalid("a username is 1–30 characters with no spaces, dots or @ (it is also the IRC nick)")
	}
	if in.Email != "" {
		if a, err := mail.ParseAddress(in.Email); err != nil || a.Address != in.Email {
			return nil, apperr.Invalid("that email address doesn't look right")
		}
	}
	if in.Role == "" {
		in.Role = RoleUser
	}
	if in.Role != RoleUser && in.Role != RoleAdmin {
		return nil, apperr.Invalid("role must be user or admin")
	}
	switch {
	case in.SendInviteEmail && in.Password != nil:
		return nil, apperr.Invalid("set a password or email a sign-up link, not both")
	case in.SendInviteEmail && in.Email == "":
		return nil, apperr.Invalid("an email address is needed to send the set-password link")
	case !in.SendInviteEmail && (in.Password == nil || len(*in.Password) < 10):
		return nil, apperr.Invalid("the password must be at least 10 characters (or email a set-password link instead)")
	}
	var out *authapi.CreatedUser
	err := s.call(ctx, sess, func(at string) (err error) { out, err = s.Client.CreateUser(ctx, at, in); return })
	return out, err
}

func (s *AdminService) SendPasswordReset(ctx context.Context, sess *session.Session, id string) error {
	return s.call(ctx, sess, func(at string) error { return s.Client.SendPasswordReset(ctx, at, id) })
}

func (s *AdminService) ResendVerification(ctx context.Context, sess *session.Session, id string) error {
	return s.call(ctx, sess, func(at string) error { return s.Client.ResendVerification(ctx, at, id) })
}

func (s *AdminService) AccessToken(ctx context.Context, sess *session.Session) (string, error) {
	var tok string
	err := s.call(ctx, sess, func(at string) error { tok = at; return nil })
	return tok, err
}
