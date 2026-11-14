package admin

import (
	"context"
	"testing"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
)

func bptr(b bool) *bool { return &b }

func TestSettings(t *testing.T) {
	f, svc, _ := setup(t)
	s := login(t, svc)
	ctx := context.Background()
	if _, err := svc.UpdateSettings(ctx, s, authapi.SettingsPatch{VoiceChannelCreation: ptr("everyone")}); err == nil {
		t.Fatal("bad voice policy accepted")
	}
	if _, err := svc.UpdateSettings(ctx, s, authapi.SettingsPatch{}); err == nil {
		t.Fatal("empty patch accepted")
	}
	st, err := svc.UpdateSettings(ctx, s, authapi.SettingsPatch{Registration: ptr("closed"), RequireEmailVerification: bptr(true)})
	if err != nil || st.Registration != "closed" || !st.RequireEmailVerification || st.UpdatedByUsername != "root" || st.VoiceChannelCreation != "founders" {
		t.Fatalf("update: %v %+v", err, st)
	}
	f.Legacy = true
	if _, err := svc.GetSettings(ctx, s); !apperr.IsUnsupported(err) {
		t.Fatalf("legacy: %v", err)
	}
}

func TestProviders(t *testing.T) {
	f, svc, _ := setup(t)
	s := login(t, svc)
	ctx := context.Background()
	oidc := func(issuer, secret string) authapi.SSOProviderInput {
		return authapi.SSOProviderInput{Slug: ptr(" Google "), Kind: ptr("oidc"), DisplayName: ptr("Google"),
			Config: &authapi.ProviderSettings{OIDC: &authapi.OIDCConfig{Issuer: issuer, ClientID: "cid", ClientSecret: secret, Scopes: []string{"email", "email"}}}}
	}
	for _, bad := range []authapi.SSOProviderInput{
		oidc("ftp://x", "s"), oidc("https://accounts.google.com", ""),
		{Slug: ptr("x y"), Kind: ptr("oidc"), DisplayName: ptr("X")},
		{Slug: ptr("okta"), Kind: ptr("saml"), DisplayName: ptr("Okta"), Config: &authapi.ProviderSettings{SAML: &authapi.SAMLConfig{}}},
		{Slug: ptr("okta"), Kind: ptr("saml"), DisplayName: ptr("Okta"), Config: &authapi.ProviderSettings{OIDC: &authapi.OIDCConfig{Issuer: "https://x", ClientID: "c", ClientSecret: "s"}}},
	} {
		if _, err := svc.CreateSSOProvider(ctx, s, bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	p, err := svc.CreateSSOProvider(ctx, s, oidc("https://accounts.google.com", "s3cret"))
	if err != nil || p.Slug != "google" || !p.HasClientSecret || p.RedirectURI == "" {
		t.Fatalf("create: %v %+v", err, p)
	}
	if got := f.Providers[0].Config.OIDC.Scopes; len(got) != 2 || got[0] != "openid" || got[1] != "email" {
		t.Fatalf("scopes not normalised: %v", got)
	}
	// Patching config without a secret keeps the stored one.
	p, err = svc.UpdateSSOProvider(ctx, s, p.ID, authapi.SSOProviderInput{Enabled: bptr(true),
		Config: &authapi.ProviderSettings{OIDC: &authapi.OIDCConfig{Issuer: "https://accounts.google.com", ClientID: "cid2"}}})
	if err != nil || !p.Enabled || p.Config.OIDC.ClientID != "cid2" || !p.HasClientSecret {
		t.Fatalf("update: %v %+v", err, p)
	}
	if _, err := svc.UpdateSSOProvider(ctx, s, p.ID, authapi.SSOProviderInput{Kind: ptr("saml")}); err == nil {
		t.Fatal("kind change accepted")
	}
	if _, err := svc.UpdateSSOProvider(ctx, s, p.ID, authapi.SSOProviderInput{Config: &authapi.ProviderSettings{SAML: &authapi.SAMLConfig{IDPMetadataURL: "https://x"}}}); err == nil {
		t.Fatal("saml config on an oidc provider accepted")
	}
	res, err := svc.TestSSOProvider(ctx, s, p.ID)
	if err != nil || !res.OK {
		t.Fatalf("test: %v %+v", err, res)
	}
	if err := svc.DeleteSSOProvider(ctx, s, p.ID); err != nil || len(f.Providers) != 0 {
		t.Fatalf("delete: %v", err)
	}
}

func TestCreateUserValidation(t *testing.T) {
	f, svc, _ := setup(t)
	s := login(t, svc)
	ctx := context.Background()
	pw := "long enough pw"
	short := "short"
	for _, bad := range []authapi.CreateUser{
		{Username: "a b", Password: &pw},
		{Username: "ana", Password: &short},
		{Username: "ana"},
		{Username: "ana", SendInviteEmail: true},
		{Username: "ana", Email: "ana@x.org", SendInviteEmail: true, Password: &pw},
		{Username: "ana", Email: "not an email", Password: &pw},
		{Username: "ana", Role: "owner", Password: &pw},
	} {
		if _, err := svc.CreateUser(ctx, s, bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	u, err := svc.CreateUser(ctx, s, authapi.CreateUser{Username: " ana ", Email: "ana@x.org", SendInviteEmail: true})
	if err != nil || u.User.Username != "ana" || !u.InviteEmailSent || u.User.Role != "user" {
		t.Fatalf("create: %v %+v", err, u)
	}
	if err := svc.SendPasswordReset(ctx, s, u.User.ID); err != nil || f.Emails[0] != "reset:"+u.User.ID {
		t.Fatalf("reset: %v %v", err, f.Emails)
	}
	if err := svc.ResendVerification(ctx, s, "usr_nope"); err == nil {
		t.Fatal("unknown user")
	}
}

func TestExpandEvents(t *testing.T) {
	got := expandEvents("sso_provider_*, settings_updated,login,sso_provider_created")
	want := []string{"sso_provider_created", "sso_provider_updated", "sso_provider_deleted", "settings_updated", "login"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if len(expandEvents("nope_*")) != 0 || len(expandEvents("")) != 0 {
		t.Fatal("empty expansions")
	}
}

func TestFriendlyErrors(t *testing.T) {
	err := friendly(apperr.New(400, "no_email", "this account has no email address"))
	if e, _ := apperr.As(err); e.Code != "no_email" || e.Status != 400 || e.Message == "this account has no email address" {
		t.Fatalf("got %v", err)
	}
	other := apperr.New(400, "invalid_request", "x")
	if friendly(other) != error(other) {
		t.Fatal("unknown codes pass through")
	}
}
