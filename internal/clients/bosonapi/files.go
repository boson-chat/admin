package bosonapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
)

// Files: the files (uploads) service's internal listener
// (http://files:3001), authorised with its INTERNAL_TOKEN in
// X-Internal-Token. X-Acting-User names the panel user for its logs.
//
//	GET    /internal/files/status
//	GET    /internal/files?user=&q=&cursor=&limit=
//	DELETE /internal/files/{id}
//	GET    /internal/files/usage?user=
//	PUT    /internal/quota/{user}  {quota_bytes, max_file_bytes} (DELETE clears)

// FilesEndpoints names what the panel needs from the files service.
const FilesEndpoints = "GET /internal/files/status, GET /internal/files, DELETE /internal/files/{id}, GET /internal/files/usage and PUT /internal/quota/{user}"

// FilesStorage is the storage backend's health.
type FilesStorage struct {
	Driver    string `json:"driver"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	LatencyMS int64  `json:"latency_ms"`
	// Local: the volume (statfs); s3: the capacity budget minus stored
	// bytes (nil: s3 without a capacity).
	FreeBytes  *int64 `json:"free_bytes"`
	TotalBytes *int64 `json:"total_bytes"`
	// UsedBytes are the bodies the service has in storage.
	UsedBytes     int64 `json:"used_bytes"`
	MinFreeBytes  int64 `json:"min_free_bytes"`
	CapacityBytes int64 `json:"capacity_bytes"`
	// Full: new uploads are refused (507 storage_full).
	Full bool `json:"full"`
}

// FilesTotals are the service's counters.
type FilesTotals struct {
	LiveFiles     int64 `json:"live_files"`
	LiveBytes     int64 `json:"live_bytes"`
	StoredBytes   int64 `json:"stored_bytes"`
	WindowUploads int64 `json:"window_uploads"`
	WindowBytes   int64 `json:"window_bytes"`
	WindowUsers   int64 `json:"window_users"`
	Pending       int64 `json:"pending"`
	AwaitingPurge int64 `json:"awaiting_purge"`
}

// FilesLimits is one class's limits, in bytes.
type FilesLimits struct {
	QuotaBytes   int64 `json:"quota_bytes"`
	MaxFileBytes int64 `json:"max_file_bytes"`
}

// FilesConfigEcho is the configuration the service runs with (file > env
// > defaults).
type FilesConfigEcho struct {
	Enabled          bool        `json:"enabled"`
	RetentionSeconds int64       `json:"retention_seconds"`
	WindowSeconds    int64       `json:"window_seconds"`
	Account          FilesLimits `json:"account"`
	Guest            FilesLimits `json:"guest"`
	GuestEnabled     bool        `json:"guest_enabled"`
	Storage          struct {
		Driver string `json:"driver"`
		S3     struct {
			Endpoint      string `json:"endpoint"`
			Bucket        string `json:"bucket"`
			Region        string `json:"region"`
			PathStyle     bool   `json:"path_style"`
			PublicBaseURL string `json:"public_base_url"`
		} `json:"s3"`
		MinFreeBytes  int64 `json:"min_free_bytes"`
		CapacityBytes int64 `json:"capacity_bytes"`
	} `json:"storage"`
	// Source: "file" (the config file's files: section) or "env".
	Source string `json:"source"`
}

// FilesError is a recent background or storage error.
type FilesError struct {
	At    time.Time `json:"at"`
	Op    string    `json:"op"`
	Error string    `json:"error"`
}

// FilesStatus is GET /internal/files/status.
type FilesStatus struct {
	OK      bool             `json:"ok"`
	Storage FilesStorage     `json:"storage"`
	Totals  FilesTotals      `json:"totals"`
	Config  *FilesConfigEcho `json:"config,omitempty"`
	// ConfigFile is the stack config file's status (as /internal/config/status).
	ConfigFile      json.RawMessage `json:"config_file,omitempty"`
	RestartRequired []string        `json:"restart_required"`
	Errors          []FilesError    `json:"errors"`
}

// FileRecord is one upload in the moderation list: the public view plus
// its owner (an auth user id, or a guest key gst_…) and state.
type FileRecord struct {
	ID          string     `json:"id"`
	URL         string     `json:"url"`
	Name        string     `json:"name"`
	Size        int64      `json:"size"`
	ContentType string     `json:"content_type"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	Width       *int       `json:"width,omitempty"`
	Height      *int       `json:"height,omitempty"`
	User        string     `json:"user"`
	UserKind    string     `json:"user_kind"`
	Status      string     `json:"status"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
	DeletedBy   *string    `json:"deleted_by,omitempty"`
}

// FilePage is GET /internal/files.
type FilePage struct {
	Files      []FileRecord `json:"files"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

// FileStatuses filter the moderation list (live is the service's default).
var FileStatuses = []string{"live", "all", "ready", "pending", "deleted", "expired"}

// FilesQuery filters the moderation list.
type FilesQuery struct {
	// User is an auth user id or a guest key.
	User   string
	Q      string
	Status string
	Cursor string
	Limit  int
}

// FileFree is when some quota comes back.
type FileFree struct {
	Bytes int64     `json:"bytes"`
	At    time.Time `json:"at"`
}

// FilesOverride is a per-user quota override (nil fields = the default).
type FilesOverride struct {
	QuotaBytes   *int64     `json:"quota_bytes"`
	MaxFileBytes *int64     `json:"max_file_bytes"`
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
	UpdatedBy    *string    `json:"updated_by,omitempty"`
}

// FilesUsage is GET /internal/files/usage?user=.
type FilesUsage struct {
	Used          int64          `json:"used"`
	Quota         int64          `json:"quota"`
	WindowSeconds int64          `json:"window_seconds"`
	MaxFile       int64          `json:"max_file"`
	Frees         []FileFree     `json:"frees"`
	User          string         `json:"user"`
	UserKind      string         `json:"user_kind"`
	LiveFiles     int64          `json:"live_files"`
	LiveBytes     int64          `json:"live_bytes"`
	Override      *FilesOverride `json:"override"`
	Defaults      FilesLimits    `json:"defaults"`
}

// FilesOverrideInput is PUT /internal/quota/{user}.
type FilesOverrideInput struct {
	QuotaBytes   *int64 `json:"quota_bytes"`
	MaxFileBytes *int64 `json:"max_file_bytes"`
}

type FilesClientImpl interface {
	Status(ctx context.Context) (*FilesStatus, error)
	List(ctx context.Context, q FilesQuery) (*FilePage, error)
	Delete(ctx context.Context, actor, id string) error
	Usage(ctx context.Context, user string) (*FilesUsage, error)
	// SetOverride sets a user's quota override; nil in clears it.
	SetOverride(ctx context.Context, actor, user string, in *FilesOverrideInput) (*FilesUsage, error)
}

type FilesClient struct {
	c     *Client
	token string
}

func NewFilesClient(baseURL, token string, timeout time.Duration, observe Observer) FilesClientImpl {
	return &FilesClient{c: newClient(baseURL, "the files service", timeout, observe), token: token}
}

func (f *FilesClient) headers(actor string) map[string]string {
	h := map[string]string{"X-Internal-Token": f.token}
	if actor != "" {
		h["X-Acting-User"] = actor
	}
	return h
}

// explain turns the internal listener's refusals into what to fix.
func (f *FilesClient) explain(err error) error {
	e, ok := apperr.As(err)
	if !ok {
		return err
	}
	switch {
	case e.Code == apperr.CodeUnsupported:
		return apperr.Unsupported("needs a files service release with " + FilesEndpoints)
	case e.Code == "boson_api_unavailable":
		return apperr.New(http.StatusBadGateway, "files_unreachable",
			"the files service isn't reachable from the admin panel (BOSON_FILES_INTERNAL_URL)")
	case e.Status == http.StatusServiceUnavailable && e.Code == "internal_api_disabled":
		return apperr.New(http.StatusServiceUnavailable, "files_token",
			"the files service has no internal token: set FILES_INTERNAL_TOKEN in .env and recreate files and admin")
	case e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden:
		return apperr.New(http.StatusBadGateway, "files_token",
			"the files service refused the panel's internal token: BOSON_FILES_INTERNAL_TOKEN on admin must match the files service's INTERNAL_TOKEN")
	}
	return err
}

func (f *FilesClient) Status(ctx context.Context) (*FilesStatus, error) {
	var out FilesStatus
	if err := f.c.callH(ctx, "files.status", http.MethodGet, "/internal/files/status", "", f.headers(""), nil, &out); err != nil {
		return nil, f.explain(err)
	}
	if out.Errors == nil {
		out.Errors = []FilesError{}
	}
	if out.RestartRequired == nil {
		out.RestartRequired = []string{}
	}
	return &out, nil
}

func (f *FilesClient) List(ctx context.Context, q FilesQuery) (*FilePage, error) {
	v := url.Values{}
	if q.User != "" {
		v.Set("user", q.User)
	}
	if q.Q != "" {
		v.Set("q", q.Q)
	}
	if q.Status != "" {
		v.Set("status", q.Status)
	}
	if q.Cursor != "" {
		v.Set("cursor", q.Cursor)
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	path := "/internal/files"
	if len(v) > 0 {
		path += "?" + v.Encode()
	}
	var out FilePage
	if err := f.c.callH(ctx, "files.list", http.MethodGet, path, "", f.headers(""), nil, &out); err != nil {
		return nil, f.explain(err)
	}
	if out.Files == nil {
		out.Files = []FileRecord{}
	}
	return &out, nil
}

func (f *FilesClient) Delete(ctx context.Context, actor, id string) error {
	err := f.c.callH(ctx, "files.delete", http.MethodDelete, "/internal/files/"+url.PathEscape(id), "", f.headers(actor), nil, nil)
	if e, ok := apperr.As(err); ok && e.Status == http.StatusNotFound && e.Code != apperr.CodeUnsupported {
		return apperr.New(http.StatusNotFound, "file_not_found", "that file doesn't exist or was already deleted")
	}
	return f.explain(err)
}

func (f *FilesClient) Usage(ctx context.Context, user string) (*FilesUsage, error) {
	var out FilesUsage
	if err := f.c.callH(ctx, "files.usage", http.MethodGet, "/internal/files/usage?user="+url.QueryEscape(user), "", f.headers(""), nil, &out); err != nil {
		return nil, f.explain(err)
	}
	if out.Frees == nil {
		out.Frees = []FileFree{}
	}
	return &out, nil
}

func (f *FilesClient) SetOverride(ctx context.Context, actor, user string, in *FilesOverrideInput) (*FilesUsage, error) {
	path := "/internal/quota/" + url.PathEscape(user)
	if in == nil {
		if err := f.c.callH(ctx, "files.quota.clear", http.MethodDelete, path, "", f.headers(actor), nil, nil); err != nil {
			return nil, f.explain(err)
		}
		return f.Usage(ctx, user)
	}
	var out FilesUsage
	if err := f.c.callH(ctx, "files.quota.set", http.MethodPut, path, "", f.headers(actor), in, &out); err != nil {
		return nil, f.explain(err)
	}
	if out.Frees == nil {
		out.Frees = []FileFree{}
	}
	return &out, nil
}
