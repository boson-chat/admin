// Package bosonapi talks to the boson backend (the stack's `api` service and
// the public directory at api.boson.chat).
//
// Two parts:
//   - profiles: the admin read of a user's boson profile, GET
//     /v1/admin/profiles/{auth_user_id}, authorised with the admin's own
//     auth-service access token (the backend already verifies those).
//     The backend doesn't have this endpoint yet; see README "Upstream
//     work".
//   - directory: the public listing (GET /servers, GET /servers/{id}, live)
//     and a proposed self-hosted network registration authorised by a
//     per-network management key plus DNS proof (POST /directory/networks…).
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

// call does one JSON request. auth is a full Authorization header value.
func (c *Client) call(ctx context.Context, op, method, path, auth string, body, out any) error {
	start := time.Now()
	status, err := c.roundTrip(ctx, method, path, auth, body, out)
	if c.Observe != nil {
		c.Observe(op, status, err, time.Since(start))
	}
	return err
}

func (c *Client) roundTrip(ctx context.Context, method, path, auth string, body, out any) (int, error) {
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
	req.Header.Set("User-Agent", "boson-admin")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
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

// Server is a directory listing.
type Server struct {
	ID                        string     `json:"id"`
	Hostname                  string     `json:"hostname"`
	Port                      int        `json:"port"`
	TLS                       bool       `json:"tls"`
	Name                      string     `json:"name"`
	Description               string     `json:"description,omitempty"`
	Tags                      []string   `json:"tags,omitempty"`
	Languages                 []string   `json:"languages,omitempty"`
	IsNSFW                    bool       `json:"is_nsfw"`
	VerificationStatus        string     `json:"verification_status"`
	VerificationLastCheckedAt *time.Time `json:"verification_last_checked_at,omitempty"`
	HealthStatus              string     `json:"health_status,omitempty"`
	UserCount                 *int       `json:"user_count,omitempty"`
	IconURL                   string     `json:"icon_url,omitempty"`
	RegisteredAt              *time.Time `json:"registered_at,omitempty"`
	// Proposed for self-hosted networks.
	WebsiteURL string `json:"website_url,omitempty"`
	AuthURL    string `json:"auth_url,omitempty"`
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

// ---- directory ----

// Verification is the DNS TXT record that proves control of the hostname.
type Verification struct {
	RecordName  string     `json:"record_name"`  // _boson.<hostname>
	RecordValue string     `json:"record_value"` // boson-verify=<token>
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// Listing is a self-hosted network's directory entry as its owner sees it.
type Listing struct {
	Network      Server        `json:"network"`
	Verification *Verification `json:"verification,omitempty"`
}

// Submission creates or edits a listing.
type Submission struct {
	Hostname    string   `json:"hostname,omitempty"`
	Port        int      `json:"port,omitempty"`
	TLS         *bool    `json:"tls,omitempty"`
	Name        string   `json:"name,omitempty"`
	Description *string  `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Languages   []string `json:"languages,omitempty"`
	IsNSFW      *bool    `json:"is_nsfw,omitempty"`
	WebsiteURL  *string  `json:"website_url,omitempty"`
	AuthURL     *string  `json:"auth_url,omitempty"`
}

// ResolverResult is one public resolver's view of the TXT record.
type ResolverResult struct {
	Outcome string   `json:"outcome"` // match | missing_record | timeout | error
	Detail  string   `json:"detail,omitempty"`
	Records []string `json:"records,omitempty"`
}

// VerifyResult is POST …/verify.
type VerifyResult struct {
	Network *Server `json:"server,omitempty"`
	Success bool    `json:"success"`
	Report  struct {
		Success bool                      `json:"success"`
		Results map[string]ResolverResult `json:"results"`
	} `json:"report"`
}

type DirectoryClientImpl interface {
	// FindPublic finds a verified listing by hostname in the public directory
	// (GET /servers?q=…). nil when it isn't listed.
	FindPublic(ctx context.Context, hostname, name string) (*Server, error)
	// The rest is the proposed self-hosted registration API. key is the
	// network's management key.
	Lookup(ctx context.Context, key, hostname string) (*Listing, error)
	Submit(ctx context.Context, key string, s Submission) (*Listing, error)
	Update(ctx context.Context, key, id string, s Submission) (*Listing, error)
	Verify(ctx context.Context, key, id string) (*VerifyResult, error)
	RegenerateToken(ctx context.Context, key, id string) (*Listing, error)
	Unlist(ctx context.Context, key, id string) error
}

// DirectoryEndpoints is what the proposed registration needs (README).
const DirectoryEndpoints = "POST /directory/networks (and …/lookup, …/{id}/verify)"

type DirectoryClient struct{ c *Client }

func NewDirectoryClient(baseURL string, timeout time.Duration, observe Observer) DirectoryClientImpl {
	host := baseURL
	if u, err := url.Parse(baseURL); err == nil && u.Host != "" {
		host = u.Host
	}
	return &DirectoryClient{c: newClient(baseURL, host, timeout, observe)}
}

func keyAuth(key string) string { return "Network " + key }

func dirUnsupported(err error) error {
	return unsupported(err, "Self-hosted directory registration needs "+DirectoryEndpoints+" on the boson API, which isn't live yet")
}

func (d *DirectoryClient) FindPublic(ctx context.Context, hostname, name string) (*Server, error) {
	queries := []string{}
	if name != "" {
		queries = append(queries, name)
	}
	if label, _, _ := strings.Cut(hostname, "."); label != "" && label != name {
		queries = append(queries, label)
	}
	queries = append(queries, "") // newest 100, unfiltered
	for _, q := range queries {
		v := url.Values{"limit": {"100"}}
		if q != "" {
			v.Set("q", q)
		} else {
			v.Set("sort", "newest")
		}
		var out struct {
			Servers []Server `json:"servers"`
		}
		if err := d.c.call(ctx, "directory.search", http.MethodGet, "/servers?"+v.Encode(), "", nil, &out); err != nil {
			return nil, err
		}
		for _, s := range out.Servers {
			if strings.EqualFold(s.Hostname, hostname) {
				return &s, nil
			}
		}
	}
	return nil, nil
}

func (d *DirectoryClient) Lookup(ctx context.Context, key, hostname string) (*Listing, error) {
	var out Listing
	err := d.c.call(ctx, "directory.lookup", http.MethodGet, "/directory/networks/lookup?hostname="+url.QueryEscape(hostname), keyAuth(key), nil, &out)
	if e, ok := apperr.As(err); ok && e.Status == http.StatusNotFound && e.Code == "not_found" {
		return nil, nil
	}
	if err != nil {
		return nil, dirUnsupported(err)
	}
	return &out, nil
}

func (d *DirectoryClient) Submit(ctx context.Context, key string, s Submission) (*Listing, error) {
	var out Listing
	if err := d.c.call(ctx, "directory.submit", http.MethodPost, "/directory/networks", keyAuth(key), s, &out); err != nil {
		return nil, dirUnsupported(err)
	}
	return &out, nil
}

func netPath(id string) string { return "/directory/networks/" + url.PathEscape(id) }

func (d *DirectoryClient) Update(ctx context.Context, key, id string, s Submission) (*Listing, error) {
	var out Listing
	if err := d.c.call(ctx, "directory.update", http.MethodPatch, netPath(id), keyAuth(key), s, &out); err != nil {
		return nil, dirUnsupported(err)
	}
	return &out, nil
}

func (d *DirectoryClient) Verify(ctx context.Context, key, id string) (*VerifyResult, error) {
	var out VerifyResult
	err := d.c.call(ctx, "directory.verify", http.MethodPost, netPath(id)+"/verify", keyAuth(key), nil, &out)
	// A failed check is 409 with the report in the body; the backend's
	// /servers/{id}/verify does the same.
	if e, ok := apperr.As(err); ok && e.Status == http.StatusConflict && out.Report.Results != nil {
		return &out, nil
	}
	if err != nil {
		return nil, dirUnsupported(err)
	}
	return &out, nil
}

func (d *DirectoryClient) RegenerateToken(ctx context.Context, key, id string) (*Listing, error) {
	var out Listing
	if err := d.c.call(ctx, "directory.regenerate", http.MethodPost, netPath(id)+"/regenerate-token", keyAuth(key), nil, &out); err != nil {
		return nil, dirUnsupported(err)
	}
	return &out, nil
}

func (d *DirectoryClient) Unlist(ctx context.Context, key, id string) error {
	return dirUnsupported(d.c.call(ctx, "directory.unlist", http.MethodDelete, netPath(id), keyAuth(key), nil, nil))
}
