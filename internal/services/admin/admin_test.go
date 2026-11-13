package admin

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/clients/authapi/authapitest"
	"github.com/boson-chat/admin/internal/session"
)

func setup(t *testing.T) (*authapitest.Fake, *AdminService, *time.Time) {
	t.Helper()
	f := authapitest.New()
	f.AddUser("usr_admin", "root", RoleAdmin, "correct horse")
	f.AddUser("usr_bob", "bob", RoleUser, "bob password")
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	svc := NewAdminService(f, NewRefresher(f, 2*time.Minute, 5*time.Second, clock), nil, clock).(*AdminService)
	return f, svc, &now
}

func login(t *testing.T, svc *AdminService) *session.Session {
	t.Helper()
	d, err := svc.Login(context.Background(), "root", "correct horse")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	return session.New(d)
}

func TestLoginAdmin(t *testing.T) {
	_, svc, now := setup(t)
	d, err := svc.Login(context.Background(), " root ", "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if d.Username != "root" || d.Role != RoleAdmin || d.CSRF == "" || d.SessionID == "" {
		t.Fatalf("bad session: %+v", d)
	}
	if want := now.Add(900 * time.Second); !d.AccessExp.Equal(want) {
		t.Fatalf("access exp %v, want %v", d.AccessExp, want)
	}
}

func TestLoginRejectsNonAdminAndLogsOut(t *testing.T) {
	f, svc, _ := setup(t)
	_, err := svc.Login(context.Background(), "bob", "bob password")
	if !errors.Is(err, apperr.ErrNotAdmin) {
		t.Fatalf("got %v, want ErrNotAdmin", err)
	}
	if f.LogoutCalls.Load() != 1 {
		t.Fatalf("non-admin session was not logged out")
	}
}

func TestLoginBadPassword(t *testing.T) {
	_, svc, _ := setup(t)
	_, err := svc.Login(context.Background(), "root", "nope")
	e, ok := apperr.As(err)
	if !ok || e.Code != "invalid_credentials" {
		t.Fatalf("got %v", err)
	}
	if _, err := svc.Login(context.Background(), "", ""); err == nil {
		t.Fatal("empty login accepted")
	}
}

func TestRefreshesExpiredTokenBeforeCall(t *testing.T) {
	f, svc, now := setup(t)
	s := login(t, svc)
	*now = now.Add(15 * time.Minute)
	f.ExpireAccess()
	if _, err := svc.ListUsers(context.Background(), s, "", 10, 0); err != nil {
		t.Fatal(err)
	}
	if f.RefreshCalls.Load() != 1 || !s.Dirty() {
		t.Fatalf("expected one refresh and a dirty session (calls=%d)", f.RefreshCalls.Load())
	}
	if s.Snapshot().RefreshToken == "rt-1" {
		t.Fatal("refresh token not rotated in session")
	}
}

func TestRetriesOnceAfter401(t *testing.T) {
	f, svc, _ := setup(t)
	s := login(t, svc)
	f.ExpireAccess() // token looks fresh locally but the auth service says no
	if _, err := svc.GetUser(context.Background(), s, "usr_bob"); err != nil {
		t.Fatal(err)
	}
	if f.RefreshCalls.Load() != 1 {
		t.Fatalf("refresh calls = %d", f.RefreshCalls.Load())
	}
}

func TestRefreshFailureEndsSession(t *testing.T) {
	f, svc, _ := setup(t)
	s := login(t, svc)
	f.ExpireAccess()
	f.RefreshErr = apperr.New(401, "invalid_refresh_token", "gone")
	_, err := svc.ListInvites(context.Background(), s)
	if !errors.Is(err, apperr.ErrSessionExpired) {
		t.Fatalf("got %v, want ErrSessionExpired", err)
	}
}

func TestDemotedAdminLosesSessionOnRefresh(t *testing.T) {
	f, svc, _ := setup(t)
	s := login(t, svc)
	f.Users["usr_admin"].Role = RoleUser
	f.ExpireAccess()
	_, err := svc.ListUsers(context.Background(), s, "", 10, 0)
	if !errors.Is(err, apperr.ErrNotAdmin) {
		t.Fatalf("got %v, want ErrNotAdmin", err)
	}
}

// Concurrent requests carrying the same expired cookie must share one
// refresh; the fake (like the auth service) revokes the session on reuse.
func TestConcurrentRefreshIsSingleFlight(t *testing.T) {
	f, svc, _ := setup(t)
	base := login(t, svc).Snapshot()
	f.ExpireAccess()
	f.RefreshDelay = 50 * time.Millisecond

	var wg sync.WaitGroup
	errs := make([]error, 8)
	sessions := make([]*session.Session, 8)
	for i := range 8 {
		sessions[i] = session.New(base) // each request decodes its own copy of the cookie
		wg.Go(func() { _, errs[i] = svc.ListUsers(context.Background(), sessions[i], "", 10, 0) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	if n := f.RefreshCalls.Load(); n != 1 {
		t.Fatalf("refresh calls = %d, want 1", n)
	}
	if f.Reuses.Load() != 0 {
		t.Fatal("refresh token reused")
	}
	want := sessions[0].Snapshot().RefreshToken
	for _, s := range sessions {
		if s.Snapshot().RefreshToken != want {
			t.Fatal("requests ended with different tokens")
		}
	}

	// A late request with the old cookie (its response raced the new
	// Set-Cookie) gets the remembered result instead of tripping reuse.
	late := session.New(base)
	f.RefreshDelay = 0
	if _, err := svc.ListUsers(context.Background(), late, "", 10, 0); err != nil {
		t.Fatalf("late request: %v", err)
	}
	if f.RefreshCalls.Load() != 1 || f.Reuses.Load() != 0 {
		t.Fatalf("late request refreshed again (calls=%d reuses=%d)", f.RefreshCalls.Load(), f.Reuses.Load())
	}
}

func TestRefresherForgetsAfterWindow(t *testing.T) {
	f := authapitest.New()
	f.AddUser("u1", "root", RoleAdmin, "pw")
	now := time.Unix(0, 0)
	r := NewRefresher(f, time.Minute, time.Second, func() time.Time { return now })
	tok, _ := f.Login(context.Background(), "root", "pw")
	if _, shared, err := r.Refresh(context.Background(), tok.RefreshToken); err != nil || shared {
		t.Fatalf("first: %v shared=%v", err, shared)
	}
	if _, shared, err := r.Refresh(context.Background(), tok.RefreshToken); err != nil || !shared {
		t.Fatalf("within window: %v shared=%v", err, shared)
	}
	now = now.Add(2 * time.Minute)
	if _, _, err := r.Refresh(context.Background(), tok.RefreshToken); err == nil {
		t.Fatal("after the window the old token must reach the auth service again (and fail)")
	}
}

func TestRefresherRetriesTransportErrors(t *testing.T) {
	f := authapitest.New()
	f.AddUser("u1", "root", RoleAdmin, "pw")
	r := NewRefresher(f, time.Minute, time.Second, nil)
	tok, _ := f.Login(context.Background(), "root", "pw")
	f.RefreshErr = errors.New("connection refused")
	if _, _, err := r.Refresh(context.Background(), tok.RefreshToken); err == nil {
		t.Fatal("want error")
	}
	f.RefreshErr = nil
	if _, shared, err := r.Refresh(context.Background(), tok.RefreshToken); err != nil || shared {
		t.Fatalf("retry after transport error: %v shared=%v", err, shared)
	}
}

func TestRefreshSurvivesCallerCancel(t *testing.T) {
	f := authapitest.New()
	f.AddUser("u1", "root", RoleAdmin, "pw")
	f.RefreshDelay = 30 * time.Millisecond
	r := NewRefresher(f, time.Minute, time.Second, nil)
	tok, _ := f.Login(context.Background(), "root", "pw")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := r.Refresh(ctx, tok.RefreshToken); err != nil {
		t.Fatalf("the exchange must finish despite the cancelled caller: %v", err)
	}
	if _, shared, err := r.Refresh(context.Background(), tok.RefreshToken); err != nil || !shared {
		t.Fatalf("result not remembered: %v", err)
	}
}

func ptr(s string) *string { return &s }

func TestAuditFilterAndNames(t *testing.T) {
	f, svc, now := setup(t)
	s := login(t, svc)
	meta, _ := json.Marshal(map[string]any{"user_id": "usr_bob"})
	f.Audit = []authapi.AuditEntry{
		{ID: "a1", UserID: ptr("usr_admin"), Event: "admin_user_disabled", Meta: meta, At: now.Add(-time.Minute)},
		{ID: "a2", UserID: ptr("usr_bob"), Event: "login", At: now.Add(-2 * time.Minute)},
		{ID: "a3", UserID: ptr("usr_bob"), Event: "login_failed", At: now.Add(-48 * time.Hour)},
		{ID: "a4", Event: "signing_key_created", At: now.Add(-time.Hour)},
	}
	got, err := svc.ListAudit(context.Background(), s, AuditFilter{Event: "admin_*"})
	if err != nil || len(got) != 1 || got[0].ID != "a1" {
		t.Fatalf("prefix filter: %v %+v", err, got)
	}
	if got[0].Username != "root" || got[0].TargetUserID != "usr_bob" || got[0].TargetUsername != "bob" {
		t.Fatalf("names not resolved: %+v", got[0])
	}
	got, _ = svc.ListAudit(context.Background(), s, AuditFilter{Since: now.Add(-24 * time.Hour)})
	if len(got) != 3 {
		t.Fatalf("since filter: %d entries", len(got))
	}
	got, _ = svc.ListAudit(context.Background(), s, AuditFilter{Event: "login"})
	if len(got) != 1 || got[0].ID != "a2" {
		t.Fatalf("exact filter: %+v", got)
	}
	got, _ = svc.ListAudit(context.Background(), s, AuditFilter{Limit: 2})
	if len(got) != 2 {
		t.Fatalf("limit: %d", len(got))
	}
}

func TestCreateInviteValidation(t *testing.T) {
	_, svc, _ := setup(t)
	s := login(t, svc)
	for _, in := range []authapi.CreateInvite{{MaxUses: -1}, {MaxUses: 1, TTL: "7d"}, {MaxUses: 1, TTL: "-1h"}} {
		if _, err := svc.CreateInvite(context.Background(), s, in); err == nil {
			t.Fatalf("accepted %+v", in)
		}
	}
	inv, err := svc.CreateInvite(context.Background(), s, authapi.CreateInvite{TTL: "168h", Note: " ana "})
	if err != nil || inv.Code == "" || inv.MaxUses != 1 || inv.Note != "ana" {
		t.Fatalf("create: %v %+v", err, inv)
	}
}

func TestSetRoleValidation(t *testing.T) {
	_, svc, _ := setup(t)
	s := login(t, svc)
	if _, err := svc.SetRole(context.Background(), s, "usr_bob", "owner"); err == nil {
		t.Fatal("accepted unknown role")
	}
	u, err := svc.SetRole(context.Background(), s, "usr_bob", RoleAdmin)
	if err != nil || u.Role != RoleAdmin {
		t.Fatalf("%v %+v", err, u)
	}
}

func TestLogoutIgnoresDeadSession(t *testing.T) {
	f, svc, _ := setup(t)
	s := login(t, svc)
	f.ExpireAccess()
	f.RefreshErr = apperr.New(401, "invalid_refresh_token", "gone")
	if err := svc.Logout(context.Background(), s); err != nil {
		t.Fatalf("logout of a dead session should succeed: %v", err)
	}
}
