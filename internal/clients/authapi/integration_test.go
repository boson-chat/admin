package authapi

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
)

// TestAgainstAuth runs against a real auth service (>= 1.4.0) when
// AUTH_TEST_URL, AUTH_TEST_ADMIN and AUTH_TEST_PASSWORD are set. It creates
// users and providers with a random suffix and removes the providers again.
func TestAgainstAuth(t *testing.T) {
	base := os.Getenv("AUTH_TEST_URL")
	if base == "" {
		t.Skip("AUTH_TEST_URL not set")
	}
	c := NewAuthClient(base, 10*time.Second, nil)
	ctx := context.Background()
	tok, err := c.Login(ctx, os.Getenv("AUTH_TEST_ADMIN"), os.Getenv("AUTH_TEST_PASSWORD"))
	if err != nil {
		t.Fatal(err)
	}
	at := tok.AccessToken
	sfx := fmt.Sprint(time.Now().UnixNano() % 1_000_000)

	st, err := c.GetSettings(ctx, at)
	if err != nil || st.Registration == "" {
		t.Fatalf("settings: %v %+v", err, st)
	}
	v := st.VoiceChannelCreation
	if st, err = c.UpdateSettings(ctx, at, SettingsPatch{VoiceChannelCreation: &v}); err != nil || st.UpdatedAt == nil {
		t.Fatalf("patch settings: %v %+v", err, st)
	}

	pw := "integration pw " + sfx
	u, err := c.CreateUser(ctx, at, CreateUser{Username: "it" + sfx, Role: "user", Password: &pw})
	if err != nil || u.InviteEmailSent {
		t.Fatalf("create user: %v %+v", err, u)
	}
	if _, err := c.SendPasswordReset(ctx, at, u.User.ID); err == nil {
		t.Fatal("reset for an account without email")
	} else if e, _ := apperr.As(err); e.Code != "no_email" {
		t.Fatalf("no_email: %v", err)
	}
	got, err := c.GetUser(ctx, at, u.User.ID)
	if err != nil || got.Identities == nil || got.HasPassword == nil || !*got.HasPassword {
		t.Fatalf("get user: %v %+v", err, got)
	}

	slug, kind, name := "it-"+sfx, KindOIDC, "IT "+sfx
	p, err := c.CreateSSOProvider(ctx, at, SSOProviderInput{Slug: &slug, Kind: &kind, DisplayName: &name, Config: &ProviderSettings{
		OIDC: &OIDCConfig{Issuer: "https://accounts.google.com", ClientID: "cid", ClientSecret: "s", UsernameClaim: "email"}}})
	if err != nil || !p.HasClientSecret || p.RedirectURI == "" || p.Config.OIDC.UsernameClaim != "email" {
		t.Fatalf("create provider: %v %+v", err, p)
	}
	defer func() { _ = c.DeleteSSOProvider(ctx, at, p.ID) }()
	if _, err := c.CreateSSOProvider(ctx, at, SSOProviderInput{Slug: &slug, Kind: &kind, DisplayName: &name, Config: &ProviderSettings{
		OIDC: &OIDCConfig{Issuer: "https://accounts.google.com", ClientID: "c", ClientSecret: "s"}}}); err == nil {
		t.Fatal("duplicate slug accepted")
	} else if e, _ := apperr.As(err); e.Code != "slug_taken" {
		t.Fatalf("slug_taken: %v", err)
	}
	// No secret and an empty claim: the secret stays, the claim goes.
	p, err = c.UpdateSSOProvider(ctx, at, p.ID, SSOProviderInput{Config: &ProviderSettings{OIDC: &OIDCConfig{Issuer: "https://accounts.google.com", ClientID: "cid2"}}})
	if err != nil || !p.HasClientSecret || p.Config.OIDC.ClientID != "cid2" || p.Config.OIDC.UsernameClaim != "" {
		t.Fatalf("patch provider: %v %+v", err, p.Config.OIDC)
	}

	page, err := c.QueryAudit(ctx, at, AuditQuery{Event: "sso_provider_created,sso_provider_updated", Limit: 1})
	if err != nil || !page.Supported || len(page.Entries) != 1 || page.NextCursor == "" || page.Entries[0].ActorUsername == "" {
		t.Fatalf("audit: %v %+v", err, page)
	}
	next, err := c.QueryAudit(ctx, at, AuditQuery{Event: "sso_provider_created,sso_provider_updated", Limit: 1, Cursor: page.NextCursor})
	if err != nil || len(next.Entries) != 1 || next.Entries[0].ID == page.Entries[0].ID {
		t.Fatalf("audit page 2: %v %+v", err, next)
	}
}
