// Package admin wraps the auth service's admin API for the panel: it signs
// admins in, keeps their tokens fresh, and runs admin operations with them.
package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/logger"
	"github.com/boson-chat/admin/internal/session"
	"github.com/boson-chat/admin/metrics"
)

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
	// refreshSkew refreshes access tokens this long before they expire.
	refreshSkew = 30 * time.Second
	// MaxAuditFetch is the auth service's page size limit.
	MaxAuditFetch = 200
	// maxNameLookups bounds username resolution for one audit page.
	maxNameLookups = 50
)

// AuditFilter selects audit entries. Auth releases with server-side
// filters apply all of it; older ones only the actor (by ID) and the panel
// filters the newest MaxAuditFetch entries itself.
type AuditFilter struct {
	// Actor is who performed the action: a user ID or username.
	Actor string
	// Target is the account an admin action affected (user ID or username).
	Target string
	// Event is an exact event name, or a prefix ending in "*" (e.g. "admin_*").
	Event  string
	Since  time.Time
	Until  time.Time
	Cursor string
	Limit  int
}

// AuditEntry is an audit row with usernames resolved where possible.
type AuditEntry struct {
	authapi.AuditEntry
	Username string `json:"username,omitempty"`
}

// AuditResult is one page of the audit log.
type AuditResult struct {
	Entries    []AuditEntry `json:"entries"`
	NextCursor string       `json:"next_cursor,omitempty"`
	// ServerFiltered is false on auth releases without audit filters: the
	// page is the newest entries filtered here, with no further pages.
	ServerFiltered bool   `json:"server_filtered"`
	Requires       string `json:"requires,omitempty"`
}

type AdminServiceImpl interface {
	// Login signs in through the auth service; only admins get a session.
	Login(ctx context.Context, login, password string) (session.Data, error)
	// Logout ends the upstream session.
	Logout(ctx context.Context, s *session.Session) error
	// Me re-checks the signed-in admin against the auth service.
	Me(ctx context.Context, s *session.Session) (*authapi.User, error)
	// WithToken runs fn with the admin's valid access token (refreshed when
	// needed, retried once on 401), for auth calls made through other
	// clients (the internal email API).
	WithToken(ctx context.Context, s *session.Session, fn func(accessToken string) error) error
	// Access is the signed-in account's permissions (cached for AccessTTL
	// unless fresh). Without admin.panel it fails with ErrNotAdmin.
	Access(ctx context.Context, s *session.Session, fresh bool) (*Access, error)
	// Forget drops the session's cached permissions.
	Forget(s *session.Session)
	AuthConfig(ctx context.Context) (*authapi.PublicConfig, error)

	ListUsers(ctx context.Context, s *session.Session, query string, limit, offset int) (*authapi.UserList, error)
	GetUser(ctx context.Context, s *session.Session, id string) (*authapi.User, error)
	SetRole(ctx context.Context, s *session.Session, id, role string) (*authapi.User, error)
	SetDisabled(ctx context.Context, s *session.Session, id string, disabled bool) (*authapi.User, error)
	ListUserSessions(ctx context.Context, s *session.Session, userID string) ([]authapi.Session, error)
	RevokeSession(ctx context.Context, s *session.Session, sessionID string) error
	ListInvites(ctx context.Context, s *session.Session) ([]authapi.Invite, error)
	CreateInvite(ctx context.Context, s *session.Session, in authapi.CreateInvite) (*authapi.Invite, error)
	RevokeInvite(ctx context.Context, s *session.Session, id string) error
	ListAudit(ctx context.Context, s *session.Session, f AuditFilter) (*AuditResult, error)

	GetSettings(ctx context.Context, s *session.Session) (*Settings, error)
	UpdateSettings(ctx context.Context, s *session.Session, p authapi.SettingsPatch) (*Settings, error)

	ListSSOProviders(ctx context.Context, s *session.Session) ([]authapi.SSOProviderConfig, error)
	GetSSOProvider(ctx context.Context, s *session.Session, id string) (*authapi.SSOProviderConfig, error)
	CreateSSOProvider(ctx context.Context, s *session.Session, in authapi.SSOProviderInput) (*authapi.SSOProviderConfig, error)
	UpdateSSOProvider(ctx context.Context, s *session.Session, id string, in authapi.SSOProviderInput) (*authapi.SSOProviderConfig, error)
	DeleteSSOProvider(ctx context.Context, s *session.Session, id string) error
	TestSSOProvider(ctx context.Context, s *session.Session, id string) (*authapi.SSOTestResult, error)

	CreateUser(ctx context.Context, s *session.Session, in authapi.CreateUser) (*authapi.CreatedUser, error)
	SendPasswordReset(ctx context.Context, s *session.Session, id string) error
	ResendVerification(ctx context.Context, s *session.Session, id string) error
	// AccessToken returns a fresh access token for calls to other boson
	// services that accept auth tokens (the boson API's admin profile read).
	AccessToken(ctx context.Context, s *session.Session) (string, error)

	ListPermissions(ctx context.Context, s *session.Session) (*PermissionList, error)
	ListRoles(ctx context.Context, s *session.Session) ([]authapi.Role, error)
	GetRole(ctx context.Context, s *session.Session, id string) (*authapi.Role, error)
	CreateRole(ctx context.Context, s *session.Session, in authapi.RoleInput) (*authapi.Role, error)
	UpdateRole(ctx context.Context, s *session.Session, id string, in authapi.RoleInput) (*authapi.Role, error)
	DeleteRole(ctx context.Context, s *session.Session, id string) error
	ReorderRoles(ctx context.Context, s *session.Session, order []string) ([]authapi.Role, error)
	RoleMembers(ctx context.Context, s *session.Session, id, cursor string, limit int) (*authapi.MemberPage, error)
	SetUserRoles(ctx context.Context, s *session.Session, userID string, roleIDs []string) ([]authapi.RoleRef, error)
	// AddRoleMember / RemoveRoleMember change one role of one user.
	AddRoleMember(ctx context.Context, s *session.Session, roleID, userID string) ([]authapi.RoleRef, error)
	RemoveRoleMember(ctx context.Context, s *session.Session, roleID, userID string) ([]authapi.RoleRef, error)
}

type AdminService struct {
	Client    authapi.AuthClientImpl
	Refresher *Refresher
	Metrics   *metrics.Metrics
	Now       func() time.Time
	access    accessCache
}

func NewAdminService(client authapi.AuthClientImpl, refresher *Refresher, m *metrics.Metrics, now func() time.Time) AdminServiceImpl {
	if now == nil {
		now = time.Now
	}
	if m == nil {
		m = metrics.Noop()
	}
	return &AdminService{Client: client, Refresher: refresher, Metrics: m, Now: now}
}

func (s *AdminService) Login(ctx context.Context, login, password string) (session.Data, error) {
	login = strings.TrimSpace(login)
	if login == "" || password == "" {
		return session.Data{}, apperr.Invalid("enter your username or email and password")
	}
	t, err := s.Client.Login(ctx, login, password)
	if err != nil {
		s.Metrics.Login(ctx, loginOutcome(err))
		return session.Data{}, err
	}
	// The login response's user may not carry permissions; /v1/me does
	// (auth >= 1.5.0). Without it, fall back to the token's user.
	u := &t.User
	if u.Permissions == nil {
		if me, merr := s.Client.Me(ctx, t.AccessToken); merr == nil {
			u = me
		} else {
			logger.FromCtx(ctx).Warn().Err(merr).Msg("reading permissions after login")
		}
	}
	a := accessFor(u)
	if !a.Can(PermPanel) {
		s.Metrics.Login(ctx, metrics.OutcomeNotAdmin)
		// Don't leave a stray session behind for someone without panel access.
		if lerr := s.Client.Logout(context.WithoutCancel(ctx), t.AccessToken); lerr != nil {
			logger.FromCtx(ctx).Warn().Err(lerr).Msg("logout of non-admin session failed")
		}
		return session.Data{}, apperr.ErrNotAdmin
	}
	s.Metrics.Login(ctx, metrics.OutcomeSuccess)
	now := s.Now()
	d := session.Data{CSRF: session.NewCSRFToken(), IssuedAt: now, RBAC: !a.Legacy}
	applyTokens(&d, t, now)
	s.access.put(d.SessionID, a, now)
	return d, nil
}

func loginOutcome(err error) string {
	e, ok := apperr.As(err)
	switch {
	case !ok:
		return metrics.OutcomeError
	case e.Status == http.StatusTooManyRequests:
		return metrics.OutcomeRateLimited
	case e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden:
		return metrics.OutcomeInvalidCredentials
	default:
		return metrics.OutcomeError
	}
}

func applyTokens(d *session.Data, t *authapi.Tokens, now time.Time) {
	d.SessionID = t.SessionID
	d.UserID, d.Username, d.Role = t.User.ID, t.User.Username, t.User.Role
	d.AccessToken, d.AccessExp = t.AccessToken, now.Add(time.Duration(t.ExpiresIn)*time.Second)
	d.RefreshToken, d.RefreshExp = t.RefreshToken, now.Add(time.Duration(t.RefreshExpiresIn)*time.Second)
}

// refresh swaps the session's tokens for new ones.
func (s *AdminService) refresh(ctx context.Context, sess *session.Session, used string) error {
	t, shared, err := s.Refresher.Refresh(ctx, used)
	if err != nil {
		if definitive(err) {
			s.Metrics.Refresh(ctx, metrics.OutcomeExpired)
			return apperr.ErrSessionExpired
		}
		s.Metrics.Refresh(ctx, metrics.OutcomeError)
		return err
	}
	if shared {
		s.Metrics.Refresh(ctx, metrics.OutcomeShared)
	} else {
		s.Metrics.Refresh(ctx, metrics.OutcomeSuccess)
	}
	// A cheap check on the refreshed user. With roles the token's user may
	// not list permissions (then the per-request permission check decides);
	// without roles only role=admin may keep the session.
	if t.User.Permissions != nil {
		if !accessFor(&t.User).Can(PermPanel) {
			return apperr.ErrNotAdmin
		}
	} else if !sess.Snapshot().RBAC && t.User.Roles == nil && t.User.Role != RoleAdmin {
		return apperr.ErrNotAdmin
	}
	now := s.Now()
	sess.Update(func(d *session.Data) {
		// Another goroutine of this request may have refreshed already.
		if d.RefreshToken == used {
			applyTokens(d, t, now)
		}
	})
	return nil
}

// call runs fn with a valid access token, refreshing first when the token
// is (nearly) expired and once more if the auth service still says 401.
func (s *AdminService) call(ctx context.Context, sess *session.Session, fn func(accessToken string) error) error {
	d := sess.Snapshot()
	refreshed := false
	if !s.Now().Before(d.AccessExp.Add(-refreshSkew)) {
		if err := s.refresh(ctx, sess, d.RefreshToken); err != nil {
			return err
		}
		refreshed = true
		d = sess.Snapshot()
	}
	err := fn(d.AccessToken)
	if authapi.IsUnauthorized(err) && !refreshed {
		if err := s.refresh(ctx, sess, d.RefreshToken); err != nil {
			return err
		}
		err = fn(sess.Snapshot().AccessToken)
	}
	if authapi.IsUnauthorized(err) {
		return apperr.ErrSessionExpired
	}
	return err
}

func (s *AdminService) WithToken(ctx context.Context, sess *session.Session, fn func(accessToken string) error) error {
	return s.call(ctx, sess, fn)
}

func (s *AdminService) Logout(ctx context.Context, sess *session.Session) error {
	err := s.call(ctx, sess, func(at string) error { return s.Client.Logout(ctx, at) })
	if errors.Is(err, apperr.ErrSessionExpired) || errors.Is(err, apperr.ErrNotAdmin) {
		return nil // already gone upstream
	}
	return err
}

func (s *AdminService) Me(ctx context.Context, sess *session.Session) (*authapi.User, error) {
	a, err := s.Access(ctx, sess, true)
	if err != nil {
		return nil, err
	}
	return a.User, nil
}

func (s *AdminService) AuthConfig(ctx context.Context) (*authapi.PublicConfig, error) {
	return s.Client.PublicConfig(ctx)
}

func (s *AdminService) ListUsers(ctx context.Context, sess *session.Session, query string, limit, offset int) (*authapi.UserList, error) {
	var out *authapi.UserList
	err := s.call(ctx, sess, func(at string) (err error) {
		out, err = s.Client.ListUsers(ctx, at, strings.TrimSpace(query), limit, offset)
		return
	})
	return out, err
}

func (s *AdminService) GetUser(ctx context.Context, sess *session.Session, id string) (*authapi.User, error) {
	var out *authapi.User
	err := s.call(ctx, sess, func(at string) (err error) { out, err = s.Client.GetUser(ctx, at, id); return })
	return out, err
}

func (s *AdminService) SetRole(ctx context.Context, sess *session.Session, id, role string) (*authapi.User, error) {
	if role != RoleAdmin && role != RoleUser {
		return nil, apperr.Invalid("role must be user or admin")
	}
	var out *authapi.User
	err := s.call(ctx, sess, func(at string) (err error) { out, err = s.Client.SetRole(ctx, at, id, role); return })
	return out, err
}

func (s *AdminService) SetDisabled(ctx context.Context, sess *session.Session, id string, disabled bool) (*authapi.User, error) {
	var out *authapi.User
	err := s.call(ctx, sess, func(at string) (err error) { out, err = s.Client.SetDisabled(ctx, at, id, disabled); return })
	return out, err
}

func (s *AdminService) ListUserSessions(ctx context.Context, sess *session.Session, userID string) ([]authapi.Session, error) {
	var out []authapi.Session
	err := s.call(ctx, sess, func(at string) (err error) { out, err = s.Client.ListUserSessions(ctx, at, userID); return })
	return out, err
}

func (s *AdminService) RevokeSession(ctx context.Context, sess *session.Session, sessionID string) error {
	return s.call(ctx, sess, func(at string) error { return s.Client.RevokeSession(ctx, at, sessionID) })
}

func (s *AdminService) ListInvites(ctx context.Context, sess *session.Session) ([]authapi.Invite, error) {
	var out []authapi.Invite
	err := s.call(ctx, sess, func(at string) (err error) { out, err = s.Client.ListInvites(ctx, at, MaxAuditFetch); return })
	return out, err
}

func (s *AdminService) CreateInvite(ctx context.Context, sess *session.Session, in authapi.CreateInvite) (*authapi.Invite, error) {
	if in.MaxUses < 0 || in.MaxUses > 10000 {
		return nil, apperr.Invalid("max uses must be between 1 and 10000")
	}
	if in.MaxUses == 0 {
		in.MaxUses = 1
	}
	if in.TTL != "" {
		if d, err := time.ParseDuration(in.TTL); err != nil || d <= 0 {
			return nil, apperr.Invalid("expiry must be a positive duration like 168h")
		}
	}
	in.Note = strings.TrimSpace(in.Note)
	if len(in.Note) > 200 {
		return nil, apperr.Invalid("note must be at most 200 characters")
	}
	var out *authapi.Invite
	err := s.call(ctx, sess, func(at string) (err error) { out, err = s.Client.CreateInvite(ctx, at, in); return })
	return out, err
}

func (s *AdminService) RevokeInvite(ctx context.Context, sess *session.Session, id string) error {
	return s.call(ctx, sess, func(at string) error { return s.Client.RevokeInvite(ctx, at, id) })
}

func (s *AdminService) ListAudit(ctx context.Context, sess *session.Session, f AuditFilter) (*AuditResult, error) {
	limit := f.Limit
	if limit <= 0 || limit > MaxAuditFetch {
		limit = 100
	}
	events := expandEvents(f.Event)
	if f.Event != "" && len(events) == 0 {
		return &AuditResult{Entries: []AuditEntry{}, ServerFiltered: true}, nil // a prefix nothing matches
	}
	q := authapi.AuditQuery{Event: strings.Join(events, ","), Actor: strings.TrimSpace(f.Actor), Target: strings.TrimSpace(f.Target),
		Since: f.Since, Until: f.Until, Cursor: f.Cursor, Limit: limit}
	var page *authapi.AuditPage
	err := s.call(ctx, sess, func(at string) (err error) { page, err = s.Client.QueryAudit(ctx, at, q); return })
	if err != nil {
		return nil, err
	}
	if page.Supported {
		out := make([]AuditEntry, 0, len(page.Entries))
		for _, e := range page.Entries {
			if e.TargetUserID == "" {
				e.TargetUserID = metaString(e.Meta, "user_id")
			}
			out = append(out, AuditEntry{AuditEntry: e, Username: e.ActorUsername})
		}
		s.resolveNames(ctx, sess, out)
		return &AuditResult{Entries: out, NextCursor: page.NextCursor, ServerFiltered: true}, nil
	}
	// Older auth: it returned the newest entries (by actor ID at most);
	// refetch the most it allows when filtering here.
	if f.Cursor != "" {
		return &AuditResult{Entries: []AuditEntry{}, Requires: authapi.RequiresAuditFilters}, nil
	}
	if q.Event != "" || q.Target != "" || !q.Since.IsZero() || !q.Until.IsZero() || (q.Actor != "" && !strings.HasPrefix(q.Actor, "usr_")) {
		if limit < MaxAuditFetch {
			q.Limit = MaxAuditFetch
			err := s.call(ctx, sess, func(at string) (err error) { page, err = s.Client.QueryAudit(ctx, at, q); return })
			if err != nil {
				return nil, err
			}
		}
	}
	out := make([]AuditEntry, 0, min(len(page.Entries), limit))
	for _, e := range page.Entries {
		if len(out) == limit {
			break
		}
		if !q.Since.IsZero() && e.At.Before(q.Since) || !q.Until.IsZero() && !e.At.Before(q.Until) || !eventMatches(q.Event, e.Event) {
			continue
		}
		e.TargetUserID = metaString(e.Meta, "user_id")
		out = append(out, AuditEntry{AuditEntry: e})
	}
	s.resolveNames(ctx, sess, out)
	// Username filters need the names, so they're applied last.
	if q.Actor != "" && !strings.HasPrefix(q.Actor, "usr_") {
		out = filterEntries(out, func(e AuditEntry) bool { return strings.EqualFold(e.Username, q.Actor) })
	}
	if q.Target != "" {
		out = filterEntries(out, func(e AuditEntry) bool {
			return e.TargetUserID == q.Target || (e.TargetUsername != "" && strings.EqualFold(e.TargetUsername, q.Target))
		})
	}
	return &AuditResult{Entries: out, ServerFiltered: false, Requires: authapi.RequiresAuditFilters}, nil
}

func filterEntries(in []AuditEntry, keep func(AuditEntry) bool) []AuditEntry {
	out := in[:0]
	for _, e := range in {
		if keep(e) {
			out = append(out, e)
		}
	}
	return out
}

// eventMatches applies an expanded comma list (see expandEvents) locally.
func eventMatches(filter, event string) bool {
	if filter == "" {
		return true
	}
	for _, f := range strings.Split(filter, ",") {
		if f == event {
			return true
		}
	}
	return false
}

// resolveNames fills usernames for actors and targets, best effort.
func (s *AdminService) resolveNames(ctx context.Context, sess *session.Session, entries []AuditEntry) {
	ids := map[string]string{}
	for _, e := range entries {
		if e.UserID != nil && *e.UserID != "" && e.Username == "" {
			ids[*e.UserID] = ""
		}
		if e.TargetUserID != "" && e.TargetUsername == "" {
			ids[e.TargetUserID] = ""
		}
		if len(ids) >= maxNameLookups {
			break
		}
	}
	if len(ids) == 0 {
		return
	}
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, 8)
	)
	keys := make([]string, 0, len(ids))
	for id := range ids {
		keys = append(keys, id)
	}
	for _, id := range keys {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			u, err := s.GetUser(ctx, sess, id)
			if err == nil {
				mu.Lock()
				ids[id] = u.Username
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	for i := range entries {
		if entries[i].UserID != nil && entries[i].Username == "" {
			entries[i].Username = ids[*entries[i].UserID]
		}
		if entries[i].TargetUsername == "" {
			entries[i].TargetUsername = ids[entries[i].TargetUserID]
		}
	}
}
