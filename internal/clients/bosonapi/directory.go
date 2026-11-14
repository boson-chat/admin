package bosonapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
)

// The boson network directory (github.com/boson-chat/directory), /v1 API.
//
//   - public: GET /v1/networks/lookup?hostname= (verified rows only);
//   - owner, as the network itself: "Authorization: Network <key>" on
//     POST /v1/networks, GET /v1/networks/lookup, PATCH/DELETE
//     /v1/networks/{id}, POST /v1/networks/{id}/verify and
//     /regenerate-token. The directory stores only sha256(key);
//   - operator, the stack's own directory only: "X-Internal-Token:
//     <DIRECTORY_INTERNAL_TOKEN>" on GET /v1/networks?owner=<auth_user_id>
//     (every status), the peers API (/v1/admin/peers…) and, from directory
//     1.4.0, the runtime settings (GET/PATCH /v1/admin/settings).
//     X-Acting-User names the panel user for the directory's audit log.
//
// Before the directory existed the panel spoke a proposed
// /directory/networks API on api.boson.chat; an old backend answers the
// /v1 paths with a plain 404, which reads as "unsupported", and the public
// check falls back to its GET /servers search.

// Server is a directory listing, in the shape the panel's UI uses. It reads
// both the directory's /v1 network (status, last_checked_at, created_at)
// and the backend's legacy /servers row (verification_status, …).
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
	LastVerifiedAt            *time.Time `json:"last_verified_at,omitempty"`
	HealthStatus              string     `json:"health_status,omitempty"`
	UserCount                 *int       `json:"user_count,omitempty"`
	IconURL                   string     `json:"icon_url,omitempty"`
	BannerURL                 string     `json:"banner_url,omitempty"`
	RegisteredAt              *time.Time `json:"registered_at,omitempty"`
	WebsiteURL                string     `json:"website_url,omitempty"`
	AuthURL                   string     `json:"auth_url,omitempty"`
	// local | peer:<id>: where the directory got the listing.
	Origin string `json:"origin,omitempty"`
}

func (s *Server) UnmarshalJSON(b []byte) error {
	type plain Server
	var w struct {
		plain
		Status            string     `json:"status"`
		LastCheckedAt     *time.Time `json:"last_checked_at"`
		CreatedAt         *time.Time `json:"created_at"`
		VerificationToken string     `json:"verification_token"`
	}
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*s = Server(w.plain)
	if s.VerificationStatus == "" {
		s.VerificationStatus = w.Status
	}
	if s.VerificationLastCheckedAt == nil {
		s.VerificationLastCheckedAt = w.LastCheckedAt
	}
	if s.RegisteredAt == nil {
		s.RegisteredAt = w.CreatedAt
	}
	return nil
}

// Verification is the DNS TXT record that proves control of the hostname.
type Verification struct {
	RecordName  string     `json:"record_name"`  // _boson.<hostname>
	RecordValue string     `json:"record_value"` // boson-verify=<token>
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// Listing is a network's directory entry as its owner sees it.
type Listing struct {
	Network      Server        `json:"network"`
	Verification *Verification `json:"verification,omitempty"`
	// BrandingSync is set by the panel (not the directory): how pushing the
	// stack's theme to the listing went after a submit or edit.
	BrandingSync *BrandingSync `json:"branding_sync,omitempty"`
}

// BrandingSync reports a theme push to the directory.
type BrandingSync struct {
	OK bool `json:"ok"`
	// Unsupported: the directory has no theme API yet (not an error).
	Unsupported bool   `json:"unsupported,omitempty"`
	Error       string `json:"error,omitempty"`
	// Cleared: the network has no branding, so the listing's theme was removed.
	Cleared bool `json:"cleared,omitempty"`
}

// UnmarshalJSON takes {network, verification} or a bare network (with
// verification_token, when the directory answers that way).
func (l *Listing) UnmarshalJSON(b []byte) error {
	var probe struct {
		Network      json.RawMessage `json:"network"`
		Verification *Verification   `json:"verification"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return err
	}
	raw := []byte(probe.Network)
	if len(raw) == 0 || string(raw) == "null" {
		raw = b
	}
	if err := json.Unmarshal(raw, &l.Network); err != nil {
		return err
	}
	l.Verification = probe.Verification
	if l.Verification == nil && l.Network.VerificationStatus != "verified" {
		var t struct {
			Token string `json:"verification_token"`
		}
		_ = json.Unmarshal(raw, &t)
		if t.Token != "" {
			l.Verification = &Verification{RecordName: "_boson." + l.Network.Hostname, RecordValue: "boson-verify=" + t.Token}
		}
	}
	return nil
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

// VerifyResult is POST /v1/networks/{id}/verify: the backend's
// /servers/{id}/verify body (200, or 409 with the report when a resolver
// doesn't see the record).
type VerifyResult struct {
	Network *Server `json:"network,omitempty"`
	Success bool    `json:"success"`
	Report  struct {
		Success bool                      `json:"success"`
		Results map[string]ResolverResult `json:"results"`
	} `json:"report"`
}

func (v *VerifyResult) UnmarshalJSON(b []byte) error {
	type plain VerifyResult
	var w struct {
		plain
		Server *Server `json:"server"` // the backend's name for it
	}
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*v = VerifyResult(w.plain)
	if v.Network == nil {
		v.Network = w.Server
	}
	return nil
}

type DirectoryClientImpl interface {
	// FindPublic finds the verified public listing for hostname. nil when
	// it isn't listed.
	FindPublic(ctx context.Context, hostname, name string) (*Server, error)
	// The owner API, as the network: key is its management key.
	Lookup(ctx context.Context, key, hostname string) (*Listing, error)
	Submit(ctx context.Context, key string, s Submission) (*Listing, error)
	Update(ctx context.Context, key, id string, s Submission) (*Listing, error)
	Verify(ctx context.Context, key, id string) (*VerifyResult, error)
	RegenerateToken(ctx context.Context, key, id string) (*Listing, error)
	Unlist(ctx context.Context, key, id string) error
	// The listing's theme (branding contract, section 3): a boson-theme/1
	// document whose https image URLs the directory hot-links ("" removes
	// an image, omitted keeps it). DeleteTheme clears it.
	PutTheme(ctx context.Context, key, id string, theme any) error
	DeleteTheme(ctx context.Context, key, id string) error
}

// DirectoryEndpoints is what network registration needs.
const DirectoryEndpoints = "the boson directory's /v1/networks API (github.com/boson-chat/directory)"

type DirectoryClient struct{ c *Client }

func NewDirectoryClient(baseURL string, timeout time.Duration, observe Observer) DirectoryClientImpl {
	return &DirectoryClient{c: newClient(baseURL, hostOf(baseURL), timeout, observe)}
}

func hostOf(baseURL string) string {
	if u, err := url.Parse(baseURL); err == nil && u.Host != "" {
		return u.Host
	}
	return baseURL
}

func keyAuth(key string) string { return "Network " + key }

func (d *DirectoryClient) dirUnsupported(err error) error {
	return unsupported(err, d.c.Name+" doesn't serve "+DirectoryEndpoints+" (an older directory, or still the boson API)")
}

func isNotFound(err error) bool {
	e, ok := apperr.As(err)
	return ok && e.Status == http.StatusNotFound && (e.Code == "not_found" || e.Code == "network_not_found")
}

func (d *DirectoryClient) FindPublic(ctx context.Context, hostname, name string) (*Server, error) {
	var out Listing
	err := d.c.call(ctx, "directory.public_lookup", http.MethodGet, "/v1/networks/lookup?hostname="+url.QueryEscape(hostname), "", nil, &out)
	switch {
	case isNotFound(err):
		return nil, nil
	case apperr.IsUnsupported(err):
		return d.findLegacy(ctx, hostname, name)
	case err != nil:
		return nil, err
	case out.Network.VerificationStatus != "" && out.Network.VerificationStatus != "verified":
		return nil, nil
	}
	return &out.Network, nil
}

// findLegacy searches the backend's GET /servers (api.boson.chat before the
// directory took over; first 100 results per query).
func (d *DirectoryClient) findLegacy(ctx context.Context, hostname, name string) (*Server, error) {
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
	err := d.c.call(ctx, "directory.lookup", http.MethodGet, "/v1/networks/lookup?hostname="+url.QueryEscape(hostname), keyAuth(key), nil, &out)
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, d.dirUnsupported(err)
	}
	// A directory that ignores the key answers the public row: not ours.
	if out.Network.ID == "" {
		return nil, nil
	}
	return &out, nil
}

func (d *DirectoryClient) Submit(ctx context.Context, key string, s Submission) (*Listing, error) {
	var out Listing
	if err := d.c.call(ctx, "directory.submit", http.MethodPost, "/v1/networks", keyAuth(key), s, &out); err != nil {
		return nil, d.dirUnsupported(err)
	}
	return &out, nil
}

func netPath(id string) string { return "/v1/networks/" + url.PathEscape(id) }

func (d *DirectoryClient) Update(ctx context.Context, key, id string, s Submission) (*Listing, error) {
	var out Listing
	if err := d.c.call(ctx, "directory.update", http.MethodPatch, netPath(id), keyAuth(key), s, &out); err != nil {
		return nil, d.dirUnsupported(err)
	}
	return &out, nil
}

func (d *DirectoryClient) Verify(ctx context.Context, key, id string) (*VerifyResult, error) {
	var out VerifyResult
	err := d.c.call(ctx, "directory.verify", http.MethodPost, netPath(id)+"/verify", keyAuth(key), nil, &out)
	// A failed check is 409 with the report in the body.
	if e, ok := apperr.As(err); ok && e.Status == http.StatusConflict && out.Report.Results != nil {
		return &out, nil
	}
	if err != nil {
		return nil, d.dirUnsupported(err)
	}
	return &out, nil
}

func (d *DirectoryClient) RegenerateToken(ctx context.Context, key, id string) (*Listing, error) {
	var out Listing
	if err := d.c.call(ctx, "directory.regenerate", http.MethodPost, netPath(id)+"/regenerate-token", keyAuth(key), nil, &out); err != nil {
		return nil, d.dirUnsupported(err)
	}
	return &out, nil
}

func (d *DirectoryClient) Unlist(ctx context.Context, key, id string) error {
	return d.dirUnsupported(d.c.call(ctx, "directory.unlist", http.MethodDelete, netPath(id), keyAuth(key), nil, nil))
}

// ThemeEndpoints is what pushing the branding needs.
const ThemeEndpoints = "PUT/DELETE /v1/networks/{id}/theme"

func (d *DirectoryClient) themeError(err error) error {
	if e, ok := apperr.As(err); ok && e.Code == "not_local" {
		return apperr.New(http.StatusConflict, "not_local", "the listing was imported from another directory; edit its theme there")
	}
	return unsupported(err, d.c.Name+" doesn't have "+ThemeEndpoints+" (an older directory)")
}

func (d *DirectoryClient) PutTheme(ctx context.Context, key, id string, theme any) error {
	return d.themeError(d.c.call(ctx, "directory.theme", http.MethodPut, netPath(id)+"/theme", keyAuth(key), theme, nil))
}

func (d *DirectoryClient) DeleteTheme(ctx context.Context, key, id string) error {
	err := d.c.call(ctx, "directory.theme_delete", http.MethodDelete, netPath(id)+"/theme", keyAuth(key), nil, nil)
	if isNotFound(err) {
		return nil // no theme there
	}
	return d.themeError(err)
}

// ---- operator API (the stack's own directory) ----

// Peer is another directory this one imports listings from.
type Peer struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	Name    string `json:"name,omitempty"`
	Enabled bool   `json:"enabled"`
	// Builtin: the main directory (setting main_peer_url), kept while the
	// include_main setting is on; switched off, never deleted.
	Builtin bool `json:"builtin"`
	// Active: synced right now (enabled, and for the builtin peer, included).
	Active       bool       `json:"active"`
	API          string     `json:"api,omitempty"`
	LastSyncAt   *time.Time `json:"last_sync_at,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
	ListingCount int        `json:"listing_count"`
}

// DirectorySettingValues are a directory's runtime settings.
type DirectorySettingValues struct {
	Name        string `json:"name"`
	Operator    string `json:"operator"`
	Contact     string `json:"contact"`
	IncludeMain bool   `json:"include_main"`
	MainPeerURL string `json:"main_peer_url"`
	SyncEvery   string `json:"sync_every"`
}

// DirectorySettings is GET /v1/admin/settings (directory >= 1.4.0): env
// values are defaults, overrides live in the directory's database.
type DirectorySettings struct {
	Settings   DirectorySettingValues `json:"settings"`
	Defaults   DirectorySettingValues `json:"defaults"`
	Overridden []string               `json:"overridden"`
	// IsMain: this directory is the main directory (main_peer_url is its
	// own URL); include_main doesn't apply.
	IsMain bool `json:"is_main"`
	// MainActive: the builtin main peer is being synced.
	MainActive bool       `json:"main_active"`
	MainPeer   *Peer      `json:"main_peer,omitempty"`
	PublicURL  string     `json:"public_url,omitempty"`
	UpdatedAt  *time.Time `json:"updated_at,omitempty"`
	UpdatedBy  *string    `json:"updated_by,omitempty"`
}

// PeerCheck is GET /v1/admin/peers/check: a directory's .well-known.
type PeerCheck struct {
	URL       string `json:"url"`
	WellKnown struct {
		Name     string `json:"name"`
		API      string `json:"api"`
		Operator string `json:"operator,omitempty"`
		Contact  string `json:"contact,omitempty"`
	} `json:"well_known"`
}

// SyncResult is what one peer sync did (POST /v1/admin/peers/{id}/sync).
type SyncResult struct {
	Created  int    `json:"created"`
	Updated  int    `json:"updated"`
	Deleted  int    `json:"deleted"`
	Verified int    `json:"verified"`
	Error    string `json:"error,omitempty"`
}

type OperatorClientImpl interface {
	// OwnedNetworks is every listing (any status) a boson account owns.
	OwnedNetworks(ctx context.Context, authUserID string) ([]Server, error)
	Peers(ctx context.Context) ([]Peer, error)
	SetPeerEnabled(ctx context.Context, actor, id string, enabled bool) (*Peer, error)
	CheckPeer(ctx context.Context, rawURL string) (*PeerCheck, error)
	AddPeer(ctx context.Context, actor, rawURL string) (*Peer, error)
	DeletePeer(ctx context.Context, actor, id string) error
	SyncPeer(ctx context.Context, actor, id string) (*Peer, *SyncResult, error)
	// Settings / UpdateSettings: unsupported on directories before 1.4.0.
	Settings(ctx context.Context) (*DirectorySettings, error)
	UpdateSettings(ctx context.Context, actor string, patch map[string]any) (*DirectorySettings, error)
}

// OperatorEndpoints is what the operator features need.
const OperatorEndpoints = "GET /v1/networks?owner=, GET/PATCH /v1/admin/peers"

// SettingsEndpoints is what changing the directory's settings needs.
const SettingsEndpoints = "GET/PATCH /v1/admin/settings (directory 1.4.0 or newer)"

type OperatorClient struct {
	c *Client
	// slow is for calls where the directory itself talks to another
	// directory (peer check/add: its peer timeout is 15 s; sync now: up to 45 s).
	slow  *Client
	token string
}

// SlowTimeout bounds peer check/add/sync calls (the panel's write timeout is 60 s).
const SlowTimeout = 50 * time.Second

func NewOperatorClient(baseURL, token string, timeout time.Duration, observe Observer) OperatorClientImpl {
	name := "the stack's directory"
	return &OperatorClient{c: newClient(baseURL, name, timeout, observe), slow: newClient(baseURL, name, max(timeout, SlowTimeout), observe), token: token}
}

func (o *OperatorClient) headers(actor string) map[string]string {
	h := map[string]string{"X-Internal-Token": o.token}
	if actor != "" {
		h["X-Acting-User"] = actor
	}
	return h
}

func (o *OperatorClient) opUnsupported(err error) error {
	return unsupported(err, "the stack's directory doesn't have "+OperatorEndpoints+" (needs a newer directory release)")
}

// maxOwnedPages bounds OwnedNetworks (100 a page).
const maxOwnedPages = 5

func (o *OperatorClient) OwnedNetworks(ctx context.Context, authUserID string) ([]Server, error) {
	all := []Server{}
	cursor := ""
	for range maxOwnedPages {
		v := url.Values{"owner": {authUserID}, "limit": {"100"}}
		if cursor != "" {
			v.Set("cursor", cursor)
		}
		var page struct {
			Networks   []Server `json:"networks"`
			NextCursor *string  `json:"next_cursor"`
		}
		if err := o.c.callH(ctx, "directory.owned", http.MethodGet, "/v1/networks?"+v.Encode(), "", o.headers(""), nil, &page); err != nil {
			return nil, o.opUnsupported(err)
		}
		all = append(all, page.Networks...)
		if page.NextCursor == nil || *page.NextCursor == "" {
			break
		}
		cursor = *page.NextCursor
	}
	return all, nil
}

func (o *OperatorClient) Peers(ctx context.Context) ([]Peer, error) {
	var out struct {
		Peers []Peer `json:"peers"`
	}
	if err := o.c.callH(ctx, "directory.peers", http.MethodGet, "/v1/admin/peers", "", o.headers(""), nil, &out); err != nil {
		return nil, o.opUnsupported(err)
	}
	if out.Peers == nil {
		out.Peers = []Peer{}
	}
	return out.Peers, nil
}

func (o *OperatorClient) SetPeerEnabled(ctx context.Context, actor, id string, enabled bool) (*Peer, error) {
	var raw json.RawMessage
	err := o.c.callH(ctx, "directory.peer_update", http.MethodPatch, "/v1/admin/peers/"+url.PathEscape(id), "",
		o.headers(actor), map[string]bool{"enabled": enabled}, &raw)
	if err != nil {
		return nil, o.opUnsupported(err)
	}
	return o.peerFrom(raw)
}

// peerFrom reads {peer: {...}} or the bare peer.
func (o *OperatorClient) peerFrom(raw json.RawMessage) (*Peer, error) {
	var wrapped struct {
		Peer *Peer `json:"peer"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && wrapped.Peer != nil {
		return wrapped.Peer, nil
	}
	var p Peer
	if err := json.Unmarshal(raw, &p); err != nil || p.ID == "" {
		return nil, apperr.New(http.StatusBadGateway, "boson_api_error", o.c.Name+" sent a response the panel can't read")
	}
	return &p, nil
}

func peerPath(id string) string { return "/v1/admin/peers/" + url.PathEscape(id) }

func (o *OperatorClient) CheckPeer(ctx context.Context, rawURL string) (*PeerCheck, error) {
	var out PeerCheck
	if err := o.slow.callH(ctx, "directory.peer_check", http.MethodGet, "/v1/admin/peers/check?url="+url.QueryEscape(rawURL), "",
		o.headers(""), nil, &out); err != nil {
		return nil, o.opUnsupported(err)
	}
	return &out, nil
}

func (o *OperatorClient) AddPeer(ctx context.Context, actor, rawURL string) (*Peer, error) {
	var raw json.RawMessage
	if err := o.slow.callH(ctx, "directory.peer_add", http.MethodPost, "/v1/admin/peers", "", o.headers(actor),
		map[string]string{"url": rawURL}, &raw); err != nil {
		return nil, o.opUnsupported(err)
	}
	return o.peerFrom(raw)
}

func (o *OperatorClient) DeletePeer(ctx context.Context, actor, id string) error {
	return o.opUnsupported(o.c.callH(ctx, "directory.peer_delete", http.MethodDelete, peerPath(id), "", o.headers(actor), nil, nil))
}

func (o *OperatorClient) SyncPeer(ctx context.Context, actor, id string) (*Peer, *SyncResult, error) {
	var out struct {
		Peer   *Peer       `json:"peer"`
		Result *SyncResult `json:"result"`
	}
	if err := o.slow.callH(ctx, "directory.peer_sync", http.MethodPost, peerPath(id)+"/sync", "", o.headers(actor), nil, &out); err != nil {
		return nil, nil, o.opUnsupported(err)
	}
	if out.Peer == nil {
		return nil, nil, apperr.New(http.StatusBadGateway, "boson_api_error", o.c.Name+" sent a response the panel can't read")
	}
	if out.Result == nil {
		out.Result = &SyncResult{}
	}
	return out.Peer, out.Result, nil
}

func (o *OperatorClient) settingsUnsupported(err error) error {
	return unsupported(err, "the stack's directory doesn't have "+SettingsEndpoints)
}

func (o *OperatorClient) Settings(ctx context.Context) (*DirectorySettings, error) {
	var out DirectorySettings
	if err := o.c.callH(ctx, "directory.settings", http.MethodGet, "/v1/admin/settings", "", o.headers(""), nil, &out); err != nil {
		return nil, o.settingsUnsupported(err)
	}
	if out.Overridden == nil {
		out.Overridden = []string{}
	}
	return &out, nil
}

func (o *OperatorClient) UpdateSettings(ctx context.Context, actor string, patch map[string]any) (*DirectorySettings, error) {
	var out DirectorySettings
	if err := o.c.callH(ctx, "directory.settings_update", http.MethodPatch, "/v1/admin/settings", "", o.headers(actor), patch, &out); err != nil {
		return nil, o.settingsUnsupported(err)
	}
	if out.Overridden == nil {
		out.Overridden = []string{}
	}
	return &out, nil
}
