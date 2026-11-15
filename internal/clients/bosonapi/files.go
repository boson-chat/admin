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

// FilesEndpoints names what the panel needs from the files service.
const FilesEndpoints = "GET /internal/files/status, GET /internal/files, DELETE /internal/files/{id} and GET /internal/files/usage"

// FilesStorage is the storage backend's health.
type FilesStorage struct {
	Driver string `json:"driver"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	// Local: the data directory; s3: the bucket and endpoint.
	Path     string `json:"path,omitempty"`
	Bucket   string `json:"bucket,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	// Free / total space of the volume (local driver; nil when unknown).
	FreeBytes  *int64 `json:"free_bytes,omitempty"`
	TotalBytes *int64 `json:"total_bytes,omitempty"`
}

// FilesTotals are the service's counters.
type FilesTotals struct {
	// Live files and their bytes (not yet expired or deleted).
	Files int64 `json:"files"`
	Bytes int64 `json:"bytes"`
	// Uploads (incl. deleted / expired) still inside the quota window.
	WindowUploads int64 `json:"window_uploads"`
	WindowBytes   int64 `json:"window_bytes"`
	// Distinct uploaders inside the window.
	Uploaders int64 `json:"uploaders"`
}

// FilesLimits is one class's limits (account or guest), in bytes.
type FilesLimits struct {
	QuotaBytes    int64 `json:"quota_bytes"`
	WindowSeconds int64 `json:"window_seconds"`
	MaxFileBytes  int64 `json:"max_file_bytes"`
}

// FilesConfigEcho is the configuration the service runs with (file > env
// > defaults), the same shape as its public GET /v1/config.
type FilesConfigEcho struct {
	Enabled      bool `json:"enabled"`
	GuestEnabled bool `json:"guest_enabled"`
	Limits       struct {
		Account FilesLimits `json:"account"`
		Guest   FilesLimits `json:"guest"`
	} `json:"limits"`
	RetentionSeconds int64 `json:"retention_seconds"`
}

// FilesStatus is GET /internal/files/status.
type FilesStatus struct {
	Version string           `json:"version,omitempty"`
	Storage FilesStorage     `json:"storage"`
	Totals  FilesTotals      `json:"totals"`
	Config  *FilesConfigEcho `json:"config,omitempty"`
	// Errors are current problems (config rejected, storage failing...).
	Errors []string `json:"errors"`
}

// FileOwner is who uploaded a file: an account (ID = auth user id) or a
// guest (ID = the guest token id; IPHash the hashed client IP).
type FileOwner struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	IPHash string `json:"ip_hash,omitempty"`
}

// FileRecord is one upload in the moderation list.
type FileRecord struct {
	ID          string     `json:"id"`
	URL         string     `json:"url"`
	Name        string     `json:"name"`
	Size        int64      `json:"size"`
	ContentType string     `json:"content_type"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
	Width       *int       `json:"width,omitempty"`
	Height      *int       `json:"height,omitempty"`
	Owner       FileOwner  `json:"owner"`
}

// FilePage is GET /internal/files.
type FilePage struct {
	Files      []FileRecord `json:"files"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

// FilesQuery filters the moderation list.
type FilesQuery struct {
	// User is an account id or guest key.
	User   string
	Q      string
	Cursor string
	Limit  int
}

// FileFree is when some quota comes back.
type FileFree struct {
	Bytes int64     `json:"bytes"`
	At    time.Time `json:"at"`
}

// FilesUsage is GET /internal/files/usage?user= (the same shape as the
// public /v1/me/usage).
type FilesUsage struct {
	Used          int64      `json:"used"`
	Quota         int64      `json:"quota"`
	WindowSeconds int64      `json:"window_seconds"`
	MaxFile       int64      `json:"max_file"`
	Frees         []FileFree `json:"frees"`
	// Live files of the user right now (optional).
	Files *int64 `json:"files,omitempty"`
	// Override: a per-user quota set through PUT /internal/quota/{user}.
	Override json.RawMessage `json:"override,omitempty"`
}

type FilesClientImpl interface {
	Status(ctx context.Context) (*FilesStatus, error)
	List(ctx context.Context, q FilesQuery) (*FilePage, error)
	Delete(ctx context.Context, actor, id string) error
	Usage(ctx context.Context, user string) (*FilesUsage, error)
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
		out.Errors = []string{}
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
