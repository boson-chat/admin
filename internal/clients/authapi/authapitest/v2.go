package authapitest

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
)

func (f *Fake) adminLocked(at string) error {
	_, err := f.authLocked(at, true)
	return err
}

func unsupported(what string) error {
	return apperr.Unsupported(what + " needs a newer auth service (auth ≥ v1.3.0)")
}

func (f *Fake) GetSettings(_ context.Context, at string) (*authapi.NetworkSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.adminLocked(at); err != nil {
		return nil, err
	}
	if f.Legacy {
		return nil, unsupported("Network settings")
	}
	s := f.Settings
	return &s, nil
}

func (f *Fake) UpdateSettings(_ context.Context, at string, p authapi.SettingsPatch) (*authapi.NetworkSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.adminLocked(at); err != nil {
		return nil, err
	}
	if f.Legacy {
		return nil, unsupported("Network settings")
	}
	if p.Registration != nil {
		f.Settings.Registration = *p.Registration
	}
	if p.RequireEmailVerification != nil {
		f.Settings.RequireEmailVerification = *p.RequireEmailVerification
	}
	if p.ChannelCreation != nil {
		f.Settings.ChannelCreation = *p.ChannelCreation
	}
	if p.VoiceChannelCreation != nil {
		f.Settings.VoiceChannelCreation = *p.VoiceChannelCreation
	}
	now := time.Now().UTC()
	uid := f.access[at]
	f.Settings.UpdatedAt, f.Settings.UpdatedBy = &now, &uid
	s := f.Settings
	return &s, nil
}

func (f *Fake) providerLocked(id string) (int, error) {
	for i, p := range f.Providers {
		if p.ID == id {
			return i, nil
		}
	}
	return -1, apperr.ErrNotFound
}

func (f *Fake) ListSSOProviders(_ context.Context, at string) ([]authapi.SSOProviderConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.adminLocked(at); err != nil {
		return nil, err
	}
	if f.Legacy {
		return nil, unsupported("Managing sign-in providers")
	}
	return append([]authapi.SSOProviderConfig{}, f.Providers...), nil
}

func (f *Fake) GetSSOProvider(_ context.Context, at, id string) (*authapi.SSOProviderConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.adminLocked(at); err != nil {
		return nil, err
	}
	if f.Legacy {
		return nil, unsupported("Managing sign-in providers")
	}
	i, err := f.providerLocked(id)
	if err != nil {
		return nil, err
	}
	p := f.Providers[i]
	return &p, nil
}

func apply(p *authapi.SSOProviderConfig, in authapi.SSOProviderInput) {
	if in.Slug != nil {
		p.Slug = *in.Slug
	}
	if in.Kind != nil {
		p.Kind = *in.Kind
	}
	if in.DisplayName != nil {
		p.DisplayName = *in.DisplayName
	}
	if in.Enabled != nil {
		p.Enabled = *in.Enabled
	}
	if in.AllowSignup != nil {
		p.AllowSignup = *in.AllowSignup
	}
	if in.TrustEmail != nil {
		p.TrustEmail = *in.TrustEmail
	}
	if in.Config != nil {
		if o := in.Config.OIDC; o != nil {
			had := p.Config.OIDC != nil && p.Config.OIDC.HasClientSecret
			c := *o
			c.HasClientSecret = had || c.ClientSecret != ""
			c.ClientSecret = ""
			p.Config.OIDC = &c
		}
		if s := in.Config.SAML; s != nil {
			c := *s
			p.Config.SAML = &c
		}
	}
	base := "https://auth.example.com/v1/sso/" + p.Slug
	if p.Kind == authapi.KindSAML {
		p.ACSURL, p.SPMetadataURL, p.SPEntityID, p.CallbackURL = base+"/acs", base+"/metadata", base+"/metadata", ""
	} else {
		p.CallbackURL, p.ACSURL, p.SPMetadataURL, p.SPEntityID = base+"/callback", "", "", ""
	}
}

func (f *Fake) CreateSSOProvider(_ context.Context, at string, in authapi.SSOProviderInput) (*authapi.SSOProviderConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.adminLocked(at); err != nil {
		return nil, err
	}
	if f.Legacy {
		return nil, unsupported("Managing sign-in providers")
	}
	for _, p := range f.Providers {
		if in.Slug != nil && p.Slug == *in.Slug {
			return nil, apperr.New(409, "slug_taken", "a provider with that slug exists")
		}
	}
	p := authapi.SSOProviderConfig{ID: fmt.Sprintf("idp_%d", len(f.Providers)+1)}
	apply(&p, in)
	f.Providers = append(f.Providers, p)
	return &p, nil
}

func (f *Fake) UpdateSSOProvider(_ context.Context, at, id string, in authapi.SSOProviderInput) (*authapi.SSOProviderConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.adminLocked(at); err != nil {
		return nil, err
	}
	if f.Legacy {
		return nil, unsupported("Managing sign-in providers")
	}
	i, err := f.providerLocked(id)
	if err != nil {
		return nil, err
	}
	apply(&f.Providers[i], in)
	p := f.Providers[i]
	return &p, nil
}

func (f *Fake) DeleteSSOProvider(_ context.Context, at, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.adminLocked(at); err != nil {
		return err
	}
	if f.Legacy {
		return unsupported("Managing sign-in providers")
	}
	i, err := f.providerLocked(id)
	if err != nil {
		return err
	}
	f.Providers = append(f.Providers[:i], f.Providers[i+1:]...)
	return nil
}

func (f *Fake) TestSSOProvider(_ context.Context, at, id string) (*authapi.SSOTestResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.adminLocked(at); err != nil {
		return nil, err
	}
	if f.Legacy {
		return nil, unsupported("Managing sign-in providers")
	}
	i, err := f.providerLocked(id)
	if err != nil {
		return nil, err
	}
	p := f.Providers[i]
	if p.Config.OIDC != nil && strings.HasPrefix(p.Config.OIDC.Issuer, "https://") {
		return &authapi.SSOTestResult{OK: true, Details: []byte(`"discovery document and JWKS fetched"`)}, nil
	}
	return &authapi.SSOTestResult{OK: false, Details: []byte(`"issuer must be an https URL"`)}, nil
}

func (f *Fake) CreateUser(_ context.Context, at string, in authapi.CreateUser) (*authapi.CreatedUser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.adminLocked(at); err != nil {
		return nil, err
	}
	if f.Legacy {
		return nil, unsupported("Creating users")
	}
	for _, u := range f.Users {
		if u.Username == in.Username {
			return nil, apperr.New(409, "username_taken", "that username is taken")
		}
	}
	id := fmt.Sprintf("usr_new%d", len(f.Users)+1)
	u := &authapi.User{ID: id, Username: in.Username, Role: in.Role, CreatedAt: time.Now().UTC()}
	if in.Email != "" {
		e := in.Email
		u.Email = &e
	}
	if u.Role == "" {
		u.Role = "user"
	}
	f.Users[id] = u
	if in.Password != nil {
		f.Passwords[in.Username] = *in.Password
	}
	return &authapi.CreatedUser{User: *u, InviteSent: in.SendInviteEmail}, nil
}

func (f *Fake) userEmail(at, id, kind string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.adminLocked(at); err != nil {
		return err
	}
	if f.Legacy {
		return unsupported("Sending emails")
	}
	if _, ok := f.Users[id]; !ok {
		return apperr.ErrNotFound
	}
	f.Emails = append(f.Emails, kind+":"+id)
	return nil
}

func (f *Fake) SendPasswordReset(_ context.Context, at, id string) error {
	return f.userEmail(at, id, "reset")
}

func (f *Fake) ResendVerification(_ context.Context, at, id string) error {
	return f.userEmail(at, id, "verification")
}

// QueryAudit filters by actor ID (always) and, unless Legacy, by event,
// target, time and cursor (an index into Audit) like the newer service.
func (f *Fake) QueryAudit(_ context.Context, at string, q authapi.AuditQuery) (*authapi.AuditPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.adminLocked(at); err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	page := &authapi.AuditPage{Entries: []authapi.AuditEntry{}, Supported: !f.Legacy}
	start := 0
	if q.Cursor != "" && !f.Legacy {
		_, _ = fmt.Sscanf(q.Cursor, "%d", &start)
	}
	for i := start; i < len(f.Audit); i++ {
		e := f.Audit[i]
		actor := q.Actor
		if f.Legacy && !strings.HasPrefix(actor, "usr_") {
			actor = ""
		}
		if actor != "" && (e.UserID == nil || (*e.UserID != actor && f.Users[*e.UserID] != nil && f.Users[*e.UserID].Username != actor)) {
			continue
		}
		if !f.Legacy {
			if q.Event != "" && !(e.Event == q.Event || (strings.HasSuffix(q.Event, "*") && strings.HasPrefix(e.Event, strings.TrimSuffix(q.Event, "*")))) {
				continue
			}
			if q.Target != "" && e.TargetUserID != q.Target {
				continue
			}
			if !q.Since.IsZero() && e.At.Before(q.Since) {
				continue
			}
			if !q.Until.IsZero() && !e.At.Before(q.Until) {
				continue
			}
			if e.UserID != nil && f.Users[*e.UserID] != nil {
				e.ActorUsername = f.Users[*e.UserID].Username
			}
		}
		if len(page.Entries) == limit {
			if !f.Legacy {
				page.NextCursor = fmt.Sprint(i)
			}
			break
		}
		page.Entries = append(page.Entries, e)
	}
	return page, nil
}
