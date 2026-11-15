// Package authapitest is an in-memory fake of the auth service client.
package authapitest

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
)

// Fake models the auth service's token rules: access tokens are valid until
// revoked or replaced, refresh tokens rotate on use, and reusing one
// revokes the session (like the real service).
type Fake struct {
	mu        sync.Mutex
	Users     map[string]*authapi.User
	Passwords map[string]string // username -> password
	access    map[string]string // access token -> user id
	refresh   map[string]string // live refresh token -> user id
	used      map[string]bool   // already-rotated refresh tokens
	n         int
	Invites   []authapi.Invite
	Audit     []authapi.AuditEntry

	// Legacy makes the fake an auth v1.2: none of the newer admin
	// endpoints exist and audit filters are ignored.
	Legacy bool
	// Settings starts as an auth 1.8 (with the channel lifecycle fields and
	// guest_access); set GuestAccess to nil for a 1.7, and
	// ChannelAutoRegister / ChannelExpiryDays too for an older one.
	Settings  authapi.NetworkSettings
	Providers []authapi.SSOProviderConfig
	// Emails records password-reset / verification sends as "kind:userID".
	Emails []string

	// NoRoles makes the fake an auth v1.4: everything but roles.
	NoRoles bool
	Roles   []authapi.Role
	// UserRoles are users' explicit role IDs (Member is implicit).
	UserRoles map[string][]string
	// TokenPermissions: login/refresh responses carry roles and permissions.
	TokenPermissions bool
	// LastOrder is the last reorder request.
	LastOrder []string

	// NoRoleChannels makes the fake an auth with roles but without role
	// channels and channel access.
	NoRoleChannels bool
	// RoleChans are role (or "everyone") → channels; ChanAccess the
	// channel-access list.
	RoleChans  map[string][]authapi.RoleChannel
	ChanAccess []authapi.ChannelAccess

	// RefreshDelay slows refreshes down (to provoke races in tests).
	RefreshDelay time.Duration
	// RefreshErr, when set, is returned by Refresh.
	RefreshErr error
	// LastMeta is the client metadata of the last call.
	LastMeta authapi.ClientMeta

	RefreshCalls atomic.Int32
	MeCalls      atomic.Int32
	LogoutCalls  atomic.Int32
	Reuses       atomic.Int32
}

func New() *Fake {
	return &Fake{
		Users: map[string]*authapi.User{}, Passwords: map[string]string{},
		access: map[string]string{}, refresh: map[string]string{}, used: map[string]bool{},
		Settings: authapi.NetworkSettings{Registration: "invite", ChannelCreation: "registered", VoiceChannelCreation: "founders",
			ChannelAutoRegister: new(true), ChannelExpiryDays: new(0), GuestAccess: new(true)},
		Roles: defaultRoles(), UserRoles: map[string][]string{},
	}
}

// AddUser creates a user with a password. role=admin also gets the Admin
// role (as auth 1.5's migration does).
func (f *Fake) AddUser(id, username, role, password string) *authapi.User {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := &authapi.User{ID: id, Username: username, Role: role, CreatedAt: time.Unix(1700000000, 0).UTC()}
	f.Users[id] = u
	f.Passwords[username] = password
	if role == "admin" {
		f.UserRoles[id] = []string{RoleAdmin}
	}
	return u
}

// panelLocked: may this user call the admin API? role=admin before roles,
// admin.panel with them.
func (f *Fake) panelLocked(u *authapi.User) bool {
	if !f.rolesSupported() {
		return u.Role == "admin"
	}
	c := *u
	f.decorateLocked(&c)
	return slices.Contains(*c.Permissions, "admin.panel")
}

// ExpireAccess invalidates every access token (as if 15 minutes passed).
func (f *Fake) ExpireAccess() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.access = map[string]string{}
}

var errUnauth = apperr.New(http.StatusUnauthorized, "unauthenticated", "authentication required")

func (f *Fake) issueLocked(uid string) *authapi.Tokens {
	f.n++
	at, rt := fmt.Sprintf("at-%d", f.n), fmt.Sprintf("rt-%d", f.n)
	f.access[at], f.refresh[rt] = uid, uid
	u := *f.Users[uid]
	if f.TokenPermissions {
		f.decorateLocked(&u)
	}
	return &authapi.Tokens{TokenType: "Bearer", AccessToken: at, RefreshToken: rt, ExpiresIn: 900, RefreshExpiresIn: 86400,
		SessionID: "ses_" + uid, User: u}
}

func (f *Fake) meta(ctx context.Context) {
	if m, ok := authapi.MetaFrom(ctx); ok {
		f.LastMeta = m
	}
}

func (f *Fake) Login(ctx context.Context, login, password string) (*authapi.Tokens, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.meta(ctx)
	for id, u := range f.Users {
		if u.Username == login && f.Passwords[login] == password {
			if u.DisabledAt != nil {
				return nil, apperr.New(http.StatusForbidden, "account_disabled", "account disabled")
			}
			return f.issueLocked(id), nil
		}
	}
	return nil, apperr.New(http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
}

func (f *Fake) Refresh(ctx context.Context, rt string) (*authapi.Tokens, error) {
	f.RefreshCalls.Add(1)
	if f.RefreshDelay > 0 {
		time.Sleep(f.RefreshDelay)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.meta(ctx)
	if f.RefreshErr != nil {
		return nil, f.RefreshErr
	}
	if f.used[rt] {
		f.Reuses.Add(1)
		// Theft detection: kill the session.
		for k, v := range f.refresh {
			_ = v
			delete(f.refresh, k)
		}
		f.access = map[string]string{}
		return nil, apperr.New(http.StatusUnauthorized, "invalid_refresh_token", "refresh token reused")
	}
	uid, ok := f.refresh[rt]
	if !ok {
		return nil, apperr.New(http.StatusUnauthorized, "invalid_refresh_token", "invalid refresh token")
	}
	delete(f.refresh, rt)
	f.used[rt] = true
	return f.issueLocked(uid), nil
}

func (f *Fake) authLocked(at string, admin bool) (*authapi.User, error) {
	uid, ok := f.access[at]
	if !ok {
		return nil, errUnauth
	}
	u := f.Users[uid]
	if u.DisabledAt != nil {
		return nil, errUnauth
	}
	if admin && !f.panelLocked(u) {
		return nil, apperr.New(http.StatusForbidden, "forbidden", "admin only")
	}
	return u, nil
}

func (f *Fake) Logout(_ context.Context, at string) error {
	f.LogoutCalls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.authLocked(at, false); err != nil {
		return err
	}
	delete(f.access, at)
	return nil
}

func (f *Fake) Me(_ context.Context, at string) (*authapi.User, error) {
	f.MeCalls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	u, err := f.authLocked(at, false)
	if err != nil {
		return nil, err
	}
	c := *u
	f.decorateLocked(&c)
	return &c, nil
}

func (f *Fake) PublicConfig(context.Context) (*authapi.PublicConfig, error) {
	return &authapi.PublicConfig{Registration: "invite", SSOProviders: []authapi.SSOProvider{}}, nil
}

func (f *Fake) ListUsers(_ context.Context, at, _ string, _, _ int) (*authapi.UserList, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.authLocked(at, true); err != nil {
		return nil, err
	}
	out := &authapi.UserList{Users: []authapi.User{}}
	for _, u := range f.Users {
		c := *u
		f.decorateLocked(&c)
		out.Users = append(out.Users, c)
	}
	out.Total = int64(len(out.Users))
	return out, nil
}

func (f *Fake) GetUser(_ context.Context, at, id string) (*authapi.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.authLocked(at, true); err != nil {
		return nil, err
	}
	u, ok := f.Users[id]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	c := *u
	f.decorateLocked(&c)
	return &c, nil
}

func (f *Fake) SetRole(_ context.Context, at, id, role string) (*authapi.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.authLocked(at, true); err != nil {
		return nil, err
	}
	u, ok := f.Users[id]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	u.Role = role
	c := *u
	return &c, nil
}

func (f *Fake) SetDisabled(_ context.Context, at, id string, disabled bool) (*authapi.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.authLocked(at, true); err != nil {
		return nil, err
	}
	u, ok := f.Users[id]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	if disabled {
		now := time.Now()
		u.DisabledAt = &now
	} else {
		u.DisabledAt = nil
	}
	c := *u
	return &c, nil
}

func (f *Fake) ListUserSessions(_ context.Context, at, userID string) ([]authapi.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.authLocked(at, true); err != nil {
		return nil, err
	}
	return []authapi.Session{{ID: "ses_" + userID, UserID: userID, AuthMethod: "password"}}, nil
}

func (f *Fake) RevokeSession(_ context.Context, at, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, err := f.authLocked(at, true)
	return err
}

func (f *Fake) ListInvites(_ context.Context, at string, _ int) ([]authapi.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.authLocked(at, true); err != nil {
		return nil, err
	}
	return append([]authapi.Invite{}, f.Invites...), nil
}

func (f *Fake) CreateInvite(_ context.Context, at string, in authapi.CreateInvite) (*authapi.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.authLocked(at, true); err != nil {
		return nil, err
	}
	inv := authapi.Invite{ID: fmt.Sprintf("inv_%d", len(f.Invites)+1), Note: in.Note, MaxUses: in.MaxUses}
	f.Invites = append(f.Invites, inv)
	inv.Code = "code-" + inv.ID
	return &inv, nil
}

func (f *Fake) RevokeInvite(_ context.Context, at, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, err := f.authLocked(at, true)
	return err
}

var _ authapi.AuthClientImpl = (*Fake)(nil)
