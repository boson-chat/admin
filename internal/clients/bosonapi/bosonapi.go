// Package bosonapi talks to the boson backend (the stack's `api` service) and
// the boson network directory.
//
// Two parts:
//   - profiles: the admin read of a user's boson profile, GET
//     /v1/admin/profiles/{auth_user_id}, authorised with the admin's own
//     auth-service access token (the backend already verifies those).
//     The backend doesn't have this endpoint yet; see README "Upstream
//     work".
//   - directory: the boson network directory (directory.boson.chat or a
//     stack's own, github.com/boson-chat/directory), /v1 API: the public
//     lookup, the network-key owner API and the operator API (directory.go).
package bosonapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
)

// Observer is told about every call (for metrics).
type Observer func(op string, status int, err error, d time.Duration)

type Client struct {
	BaseURL string
	HTTP    *http.Client
	Observe Observer
	// Name is used in error messages ("the boson API", "api.boson.chat").
	Name string
}

func newClient(baseURL, name string, timeout time.Duration, observe Observer) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: timeout}, Observe: observe, Name: name}
}

// RawBody is a request body sent as it is (an image upload).
type RawBody struct {
	ContentType string
	Data        []byte
}

// call does one JSON request. auth is a full Authorization header value.
func (c *Client) call(ctx context.Context, op, method, path, auth string, body, out any) error {
	return c.callH(ctx, op, method, path, auth, nil, body, out)
}

// callH is call with extra request headers.
func (c *Client) callH(ctx context.Context, op, method, path, auth string, hdr map[string]string, body, out any) error {
	start := time.Now()
	status, err := c.roundTrip(ctx, method, path, auth, hdr, body, out)
	if c.Observe != nil {
		c.Observe(op, status, err, time.Since(start))
	}
	return err
}

func (c *Client) roundTrip(ctx context.Context, method, path, auth string, hdr map[string]string, body, out any) (int, error) {
	var rd io.Reader
	contentType := "application/json"
	if raw, ok := body.(RawBody); ok {
		rd, contentType = bytes.NewReader(raw.Data), raw.ContentType
	} else if body != nil {
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
	req.Header.Set("User-Agent", "boson-admin")
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, apperr.New(http.StatusBadGateway, "boson_api_unavailable", c.Name+" is unreachable")
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 400 {
		if resp.StatusCode == http.StatusConflict && out != nil {
			_ = json.Unmarshal(data, out) // verify reports a failed check as 409 + report
		}
		return resp.StatusCode, c.httpError(resp, data)
	}
	if out != nil && strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		// A web UI's SPA fallback (the directory serves its site on every
		// unknown path): this release doesn't have the endpoint.
		return resp.StatusCode, apperr.New(http.StatusNotImplemented, apperr.CodeUnsupported, "")
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, apperr.New(http.StatusBadGateway, "boson_api_error", c.Name+" sent a response the panel can't read")
		}
	}
	return resp.StatusCode, nil
}

func (c *Client) httpError(resp *http.Response, data []byte) error {
	var eb struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(data, &eb)
	switch {
	case resp.StatusCode == http.StatusBadGateway && eb.Error == "peer_unreachable" && eb.Message != "":
		// The directory couldn't reach a peer directory: say why.
		return apperr.New(http.StatusBadGateway, "peer_unreachable", "couldn't read that directory: "+eb.Message)
	case resp.StatusCode == http.StatusServiceUnavailable && eb.Error == "internal_api_disabled":
		// An internal listener without its token configured.
		return apperr.New(http.StatusServiceUnavailable, eb.Error, c.Name+" has its internal API turned off")
	case resp.StatusCode >= 500:
		return apperr.New(http.StatusBadGateway, "boson_api_error", c.Name+" failed to handle the request")
	case eb.Error == "" && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed):
		// Unknown route: this backend release doesn't have the endpoint.
		return apperr.New(http.StatusNotImplemented, apperr.CodeUnsupported, "")
	case resp.StatusCode == http.StatusTooManyRequests:
		msg := "too many attempts"
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			msg += "; try again in " + ra + " s"
		}
		return apperr.New(http.StatusTooManyRequests, "rate_limited", msg)
	}
	msg := eb.Message
	if msg == "" {
		msg = eb.Error
	}
	if msg == "" {
		msg = strings.TrimSpace(string(data))
	}
	if msg == "" || len(msg) > 200 {
		msg = http.StatusText(resp.StatusCode)
	}
	code := "boson_api_" + strconv.Itoa(resp.StatusCode)
	if eb.Error != "" && !strings.Contains(eb.Error, " ") {
		code = eb.Error
	}
	return apperr.New(resp.StatusCode, code, c.Name+": "+msg)
}

// unsupported rewrites the bare unsupported error with what is missing.
func unsupported(err error, msg string) error {
	if apperr.IsUnsupported(err) {
		return apperr.Unsupported(msg)
	}
	return err
}

// ---- profiles ----

// Profile is a user's boson profile (the backend's users row).
type Profile struct {
	UserID          string     `json:"user_id"`
	AuthUserID      string     `json:"auth_user_id"`
	Handle          string     `json:"handle"`
	DisplayName     string     `json:"display_name,omitempty"`
	AvatarURL       string     `json:"avatar_url,omitempty"`
	IsDiscoverable  bool       `json:"is_discoverable"`
	HandleChangedAt *time.Time `json:"handle_changed_at,omitempty"`
	CreatedAt       *time.Time `json:"created_at,omitempty"`
}

// AdminProfile is GET /v1/admin/profiles/{auth_user_id}. Profile is nil
// when the person never opened boson (no profile yet).
type AdminProfile struct {
	Profile *Profile `json:"profile"`
	Servers []Server `json:"servers"`
}

type ProfileClientImpl interface {
	AdminProfile(ctx context.Context, accessToken, authUserID string) (*AdminProfile, error)
}

// ProfilesEndpoint is the proposed backend endpoint (documented in the README).
const ProfilesEndpoint = "GET /v1/admin/profiles/{auth_user_id}"

type ProfileClient struct{ c *Client }

func NewProfileClient(baseURL string, timeout time.Duration, observe Observer) ProfileClientImpl {
	return &ProfileClient{c: newClient(baseURL, "the boson API", timeout, observe)}
}

func (p *ProfileClient) AdminProfile(ctx context.Context, at, authUserID string) (*AdminProfile, error) {
	var out AdminProfile
	err := p.c.call(ctx, "admin.profiles.get", http.MethodGet, "/v1/admin/profiles/"+url.PathEscape(authUserID), "Bearer "+at, nil, &out)
	if e, ok := apperr.As(err); ok && e.Status == http.StatusNotFound && (e.Code == "profile_not_found" || e.Code == "needs_setup") {
		return &AdminProfile{Servers: []Server{}}, nil
	}
	if err != nil {
		return nil, unsupported(err, "Boson profiles need the boson API's "+ProfilesEndpoint+" (not in this API release yet)")
	}
	if out.Servers == nil {
		out.Servers = []Server{}
	}
	return &out, nil
}
