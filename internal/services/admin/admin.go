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

// AuditFilter selects audit entries. UserID is applied by the auth service
// (it matches the actor); Event and Since are applied here.
type AuditFilter struct {
	UserID string
	// Event is an exact event name, or a prefix ending in "*" (e.g. "admin_*").
	Event string
	Since time.Time
	Limit int
}

// AuditEntry is an audit row with usernames resolved where possible.
type AuditEntry struct {
	authapi.AuditEntry
	Username       string `json:"username,omitempty"`
	TargetUserID   string `json:"target_user_id,omitempty"`
	TargetUsername string `json:"target_username,omitempty"`
}

type AdminServiceImpl interface {
	// Login signs in through the auth service; only admins get a session.
	Login(ctx context.Context, login, password string) (session.Data, error)
	// Logout ends the upstream session.
	Logout(ctx context.Context, s *session.Session) error
	// Me re-checks the signed-in admin against the auth service.
	Me(ctx context.Context, s *session.Session) (*authapi.User, error)
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
	ListAudit(ctx context.Context, s *session.Session, f AuditFilter) ([]AuditEntry, error)
}

type AdminService struct {
	Client    authapi.AuthClientImpl
	Refresher *Refresher
	Metrics   *metrics.Metrics
	Now       func() time.Time
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
	if t.User.Role != RoleAdmin {
		s.Metrics.Login(ctx, metrics.OutcomeNotAdmin)
		// Don't leave a stray session behind for a non-admin.
		if lerr := s.Client.Logout(context.WithoutCancel(ctx), t.AccessToken); lerr != nil {
			logger.FromCtx(ctx).Warn().Err(lerr).Msg("logout of non-admin session failed")
		}
		return session.Data{}, apperr.ErrNotAdmin
	}
	s.Metrics.Login(ctx, metrics.OutcomeSuccess)
	now := s.Now()
	d := session.Data{CSRF: session.NewCSRFToken(), IssuedAt: now}
	applyTokens(&d, t, now)
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
	if t.User.Role != RoleAdmin {
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

func (s *AdminService) Logout(ctx context.Context, sess *session.Session) error {
	err := s.call(ctx, sess, func(at string) error { return s.Client.Logout(ctx, at) })
	if errors.Is(err, apperr.ErrSessionExpired) || errors.Is(err, apperr.ErrNotAdmin) {
		return nil // already gone upstream
	}
	return err
}

func (s *AdminService) Me(ctx context.Context, sess *session.Session) (*authapi.User, error) {
	var u *authapi.User
	err := s.call(ctx, sess, func(at string) (err error) { u, err = s.Client.Me(ctx, at); return })
	if err != nil {
		return nil, err
	}
	if u.Role != RoleAdmin {
		return nil, apperr.ErrNotAdmin
	}
	return u, nil
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

func (s *AdminService) ListAudit(ctx context.Context, sess *session.Session, f AuditFilter) ([]AuditEntry, error) {
	limit := f.Limit
	if limit <= 0 || limit > MaxAuditFetch {
		limit = 100
	}
	fetch := limit
	if f.Event != "" || !f.Since.IsZero() {
		fetch = MaxAuditFetch // filtered here, so fetch the most the API allows
	}
	var raw []authapi.AuditEntry
	err := s.call(ctx, sess, func(at string) (err error) { raw, err = s.Client.ListAudit(ctx, at, f.UserID, fetch); return })
	if err != nil {
		return nil, err
	}
	out := make([]AuditEntry, 0, min(len(raw), limit))
	for _, e := range raw {
		if len(out) == limit {
			break
		}
		if !f.Since.IsZero() && e.At.Before(f.Since) {
			continue
		}
		if !eventMatches(f.Event, e.Event) {
			continue
		}
		entry := AuditEntry{AuditEntry: e, TargetUserID: metaString(e.Meta, "user_id")}
		out = append(out, entry)
	}
	s.resolveNames(ctx, sess, out)
	return out, nil
}

func eventMatches(filter, event string) bool {
	if filter == "" {
		return true
	}
	if p, ok := strings.CutSuffix(filter, "*"); ok {
		return strings.HasPrefix(event, p)
	}
	return event == filter
}

// resolveNames fills usernames for actors and targets, best effort.
func (s *AdminService) resolveNames(ctx context.Context, sess *session.Session, entries []AuditEntry) {
	ids := map[string]string{}
	for _, e := range entries {
		if e.UserID != nil && *e.UserID != "" {
			ids[*e.UserID] = ""
		}
		if e.TargetUserID != "" {
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
		if entries[i].UserID != nil {
			entries[i].Username = ids[*entries[i].UserID]
		}
		entries[i].TargetUsername = ids[entries[i].TargetUserID]
	}
}
