// Package authapi is the HTTP client for the boson auth service's public
// and admin API.
package authapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
)

// AuthClientImpl is everything the panel asks of the auth service. Every
// admin call takes the admin's access token.
type AuthClientImpl interface {
	Login(ctx context.Context, login, password string) (*Tokens, error)
	Refresh(ctx context.Context, refreshToken string) (*Tokens, error)
	Logout(ctx context.Context, accessToken string) error
	Me(ctx context.Context, accessToken string) (*User, error)
	PublicConfig(ctx context.Context) (*PublicConfig, error)

	ListUsers(ctx context.Context, accessToken, query string, limit, offset int) (*UserList, error)
	GetUser(ctx context.Context, accessToken, id string) (*User, error)
	SetRole(ctx context.Context, accessToken, id, role string) (*User, error)
	SetDisabled(ctx context.Context, accessToken, id string, disabled bool) (*User, error)
	ListUserSessions(ctx context.Context, accessToken, userID string) ([]Session, error)
	RevokeSession(ctx context.Context, accessToken, sessionID string) error
	ListInvites(ctx context.Context, accessToken string, limit int) ([]Invite, error)
	CreateInvite(ctx context.Context, accessToken string, in CreateInvite) (*Invite, error)
	RevokeInvite(ctx context.Context, accessToken, id string) error
	// QueryAudit lists audit entries. Auth releases without server-side
	// filters ignore everything but user_id and limit (Supported=false).
	QueryAudit(ctx context.Context, accessToken string, q AuditQuery) (*AuditPage, error)

	GetSettings(ctx context.Context, accessToken string) (*NetworkSettings, error)
	UpdateSettings(ctx context.Context, accessToken string, p SettingsPatch) (*NetworkSettings, error)

	ListSSOProviders(ctx context.Context, accessToken string) ([]SSOProviderConfig, error)
	GetSSOProvider(ctx context.Context, accessToken, id string) (*SSOProviderConfig, error)
	CreateSSOProvider(ctx context.Context, accessToken string, in SSOProviderInput) (*SSOProviderConfig, error)
	UpdateSSOProvider(ctx context.Context, accessToken, id string, in SSOProviderInput) (*SSOProviderConfig, error)
	DeleteSSOProvider(ctx context.Context, accessToken, id string) error
	TestSSOProvider(ctx context.Context, accessToken, id string) (*SSOTestResult, error)

	CreateUser(ctx context.Context, accessToken string, in CreateUser) (*CreatedUser, error)
	SendPasswordReset(ctx context.Context, accessToken, id string) error
	ResendVerification(ctx context.Context, accessToken, id string) error

	// Roles (auth >= 1.5.0).
	ListPermissions(ctx context.Context, accessToken string) ([]Permission, error)
	ListRoles(ctx context.Context, accessToken string) ([]Role, error)
	GetRole(ctx context.Context, accessToken, id string) (*Role, error)
	CreateRole(ctx context.Context, accessToken string, in RoleInput) (*Role, error)
	UpdateRole(ctx context.Context, accessToken, id string, in RoleInput) (*Role, error)
	DeleteRole(ctx context.Context, accessToken, id string) error
	ReorderRoles(ctx context.Context, accessToken string, order []string) ([]Role, error)
	RoleMembers(ctx context.Context, accessToken, id, cursor string, limit int) (*MemberPage, error)
	SetUserRoles(ctx context.Context, accessToken, userID string, roleIDs []string) ([]RoleRef, error)
}

// Observer is told about every upstream call (for metrics).
type Observer func(op string, status int, err error, d time.Duration)

// AuthClient talks to the auth service over HTTP.
type AuthClient struct {
	BaseURL string
	HTTP    *http.Client
	Observe Observer
}

func NewAuthClient(baseURL string, timeout time.Duration, observe Observer) AuthClientImpl {
	return &AuthClient{BaseURL: baseURL, HTTP: &http.Client{Timeout: timeout}, Observe: observe}
}

// IsUnauthorized reports whether the auth service answered 401 (the access
// token is expired or its session revoked); callers refresh and retry.
func IsUnauthorized(err error) bool {
	e, ok := apperr.As(err)
	return ok && e.Status == http.StatusUnauthorized
}

type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

const maxResponse = 4 << 20

func (c *AuthClient) do(ctx context.Context, op, method, path string, token string, body, out any) error {
	start := time.Now()
	status, err := c.roundTrip(ctx, method, path, token, body, out)
	if c.Observe != nil {
		c.Observe(op, status, err, time.Since(start))
	}
	return err
}

func (c *AuthClient) roundTrip(ctx context.Context, method, path, token string, body, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if meta, ok := MetaFrom(ctx); ok {
		if meta.IP != "" {
			req.Header.Set("X-Forwarded-For", meta.IP)
		}
		if meta.UserAgent != "" {
			req.Header.Set("User-Agent", meta.UserAgent)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, fmt.Errorf("%w: %v", apperr.ErrUpstream, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return resp.StatusCode, fmt.Errorf("%w: reading response: %v", apperr.ErrUpstream, err)
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, upstreamError(resp, data)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, fmt.Errorf("%w: decoding %s %s: %v", apperr.ErrUpstream, method, path, err)
		}
	}
	return resp.StatusCode, nil
}

// upstreamError keeps the auth service's error code and message for 4xx
// (they are meant for users) and hides 5xx details.
func upstreamError(resp *http.Response, data []byte) error {
	var eb errorBody
	_ = json.Unmarshal(data, &eb)
	if resp.StatusCode >= 500 {
		return apperr.New(http.StatusBadGateway, "upstream_error", "the auth service failed to handle the request")
	}
	if eb.Error == "" {
		eb.Error = "upstream_" + strconv.Itoa(resp.StatusCode)
		eb.Message = http.StatusText(resp.StatusCode)
	}
	e := apperr.New(resp.StatusCode, eb.Error, eb.Message)
	if resp.StatusCode == http.StatusTooManyRequests {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			e.Message += " (retry after " + ra + "s)"
		}
	}
	return e
}

func (c *AuthClient) Login(ctx context.Context, login, password string) (*Tokens, error) {
	var t Tokens
	err := c.do(ctx, "login", http.MethodPost, "/v1/login", "",
		map[string]any{"login": login, "password": password}, &t)
	return checkTokens(&t, err)
}

func (c *AuthClient) Refresh(ctx context.Context, refreshToken string) (*Tokens, error) {
	var t Tokens
	err := c.do(ctx, "refresh", http.MethodPost, "/v1/token/refresh", "",
		map[string]string{"refresh_token": refreshToken}, &t)
	return checkTokens(&t, err)
}

func checkTokens(t *Tokens, err error) (*Tokens, error) {
	if err != nil {
		return nil, err
	}
	if t.AccessToken == "" || t.RefreshToken == "" {
		return nil, fmt.Errorf("%w: token response without tokens", apperr.ErrUpstream)
	}
	return t, nil
}

func (c *AuthClient) Logout(ctx context.Context, accessToken string) error {
	return c.do(ctx, "logout", http.MethodPost, "/v1/logout", accessToken, nil, nil)
}

func (c *AuthClient) Me(ctx context.Context, accessToken string) (*User, error) {
	var out userEnvelope
	if err := c.do(ctx, "me", http.MethodGet, "/v1/me", accessToken, nil, &out); err != nil {
		return nil, err
	}
	return out.merge(), nil
}

func (c *AuthClient) PublicConfig(ctx context.Context) (*PublicConfig, error) {
	var out PublicConfig
	if err := c.do(ctx, "config", http.MethodGet, "/v1/config", "", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *AuthClient) ListUsers(ctx context.Context, at, query string, limit, offset int) (*UserList, error) {
	q := url.Values{}
	if query != "" {
		q.Set("q", query)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	var out UserList
	if err := c.do(ctx, "admin.users.list", http.MethodGet, "/v1/admin/users?"+q.Encode(), at, nil, &out); err != nil {
		return nil, err
	}
	if out.Users == nil {
		out.Users = []User{}
	}
	return &out, nil
}

type userEnvelope struct {
	User User `json:"user"`
	// Identities and HasPassword sit next to the user (auth >= 1.4.0).
	Identities  *[]Identity `json:"identities"`
	HasPassword *bool       `json:"has_password"`
	// Roles and Permissions sit next to the user too (auth >= 1.5.0).
	Roles       *[]RoleRef `json:"roles"`
	Permissions *[]string  `json:"permissions"`
}

func (e *userEnvelope) merge() *User {
	if e.User.Identities == nil && e.Identities != nil {
		e.User.Identities = e.Identities
	}
	if e.User.HasPassword == nil {
		e.User.HasPassword = e.HasPassword
	}
	if e.User.Roles == nil {
		e.User.Roles = e.Roles
	}
	if e.User.Permissions == nil {
		e.User.Permissions = e.Permissions
	}
	return &e.User
}

func (c *AuthClient) GetUser(ctx context.Context, at, id string) (*User, error) {
	var out userEnvelope
	if err := c.do(ctx, "admin.users.get", http.MethodGet, "/v1/admin/users/"+url.PathEscape(id), at, nil, &out); err != nil {
		return nil, err
	}
	return out.merge(), nil
}

func (c *AuthClient) SetRole(ctx context.Context, at, id, role string) (*User, error) {
	var out userEnvelope
	if err := c.do(ctx, "admin.users.set_role", http.MethodPatch, "/v1/admin/users/"+url.PathEscape(id), at,
		map[string]string{"role": role}, &out); err != nil {
		return nil, err
	}
	return &out.User, nil
}

func (c *AuthClient) SetDisabled(ctx context.Context, at, id string, disabled bool) (*User, error) {
	action, op := "enable", "admin.users.enable"
	if disabled {
		action, op = "disable", "admin.users.disable"
	}
	var out userEnvelope
	if err := c.do(ctx, op, http.MethodPost, "/v1/admin/users/"+url.PathEscape(id)+"/"+action, at, nil, &out); err != nil {
		return nil, err
	}
	return &out.User, nil
}

func (c *AuthClient) ListUserSessions(ctx context.Context, at, userID string) ([]Session, error) {
	var out struct {
		Sessions []Session `json:"sessions"`
	}
	if err := c.do(ctx, "admin.sessions.list", http.MethodGet, "/v1/admin/users/"+url.PathEscape(userID)+"/sessions", at, nil, &out); err != nil {
		return nil, err
	}
	if out.Sessions == nil {
		out.Sessions = []Session{}
	}
	return out.Sessions, nil
}

func (c *AuthClient) RevokeSession(ctx context.Context, at, sessionID string) error {
	return c.do(ctx, "admin.sessions.revoke", http.MethodDelete, "/v1/admin/sessions/"+url.PathEscape(sessionID), at, nil, nil)
}

func (c *AuthClient) ListInvites(ctx context.Context, at string, limit int) ([]Invite, error) {
	path := "/v1/admin/invites"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var out struct {
		Invites []Invite `json:"invites"`
	}
	if err := c.do(ctx, "admin.invites.list", http.MethodGet, path, at, nil, &out); err != nil {
		return nil, err
	}
	if out.Invites == nil {
		out.Invites = []Invite{}
	}
	return out.Invites, nil
}

func (c *AuthClient) CreateInvite(ctx context.Context, at string, in CreateInvite) (*Invite, error) {
	var out struct {
		Invite Invite `json:"invite"`
	}
	if err := c.do(ctx, "admin.invites.create", http.MethodPost, "/v1/admin/invites", at, in, &out); err != nil {
		return nil, err
	}
	if out.Invite.Code == "" {
		return nil, errors.Join(apperr.ErrUpstream, errors.New("invite response without a code"))
	}
	return &out.Invite, nil
}

func (c *AuthClient) RevokeInvite(ctx context.Context, at, id string) error {
	return c.do(ctx, "admin.invites.revoke", http.MethodDelete, "/v1/admin/invites/"+url.PathEscape(id), at, nil, nil)
}
