// Package files is the Uploads and Uploaded files pages: the files
// service (uploads with a rolling quota, docs/files.md in the stack repo).
//
// The settings are files.* keys of the stack config file (saved through
// stackconfig, which checks and logs them). This service reads the files
// service's internal status, lists and deletes uploads for moderation
// (logged with the actor), reads a user's usage, and checks that a change
// to the files: section makes sense as a whole.
package files

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/bosonapi"
	"github.com/boson-chat/admin/internal/logger"
	"github.com/boson-chat/admin/internal/services/stackconfig"
)

// KeysEnv are the S3 credentials' env vars (never the config file).
var KeysEnv = []string{"FILES_S3_ACCESS_KEY", "FILES_S3_SECRET_KEY"}

// Built-in defaults of the files service (the contract), for the
// cross-key checks when a key isn't in the file.
const (
	DefaultAccountQuota   = "5GB"
	DefaultAccountMaxFile = "100MB"
	DefaultGuestQuota     = "500MB"
	DefaultGuestMaxFile   = "25MB"
)

// ErrOff: BOSON_FILES_INTERNAL_URL isn't set.
var ErrOff = apperr.New(http.StatusServiceUnavailable, "files_off",
	"the files service isn't reachable from the admin panel: set BOSON_FILES_INTERNAL_URL (http://files:3001) and BOSON_FILES_INTERNAL_TOKEN on the admin service")

// Resolver finds an account's username ("" = unknown).
type Resolver func(ctx context.Context, userID string) string

type Options struct {
	// Client is nil without BOSON_FILES_INTERNAL_URL.
	Client bosonapi.FilesClientImpl
	Now    func() time.Time
}

type ServiceImpl interface {
	Configured() bool
	Status(ctx context.Context) *View
	List(ctx context.Context, q bosonapi.FilesQuery, names Resolver) (*Page, error)
	Delete(ctx context.Context, actor, id, reason string) error
	Usage(ctx context.Context, userID string) (*bosonapi.FilesUsage, error)
	// SetOverride sets (in) or clears (nil) a user's quota override.
	SetOverride(ctx context.Context, actor, userID string, in *OverrideRequest) (*bosonapi.FilesUsage, error)
	// Check is a stackconfig.CheckFunc for the files section.
	Check(ctx context.Context, after, before stackconfig.Lookup) error
}

type Service struct{ o Options }

func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{o: o}
}

func (s *Service) Configured() bool { return s.o.Client != nil }

// View is GET /api/files/status.
type View struct {
	// Configured: BOSON_FILES_INTERNAL_URL is set.
	Configured bool     `json:"configured"`
	KeysEnv    []string `json:"keys_env"`
	// Status is the files service's answer (nil when it couldn't be read).
	Status    *bosonapi.FilesStatus `json:"status,omitempty"`
	Error     string                `json:"error,omitempty"`
	ErrorCode string                `json:"error_code,omitempty"`
	CheckedAt time.Time             `json:"checked_at"`
}

// Status never fails: the page shows the form with whatever went wrong.
func (s *Service) Status(ctx context.Context) *View {
	v := &View{Configured: s.o.Client != nil, KeysEnv: KeysEnv, CheckedAt: s.o.Now().UTC()}
	if s.o.Client == nil {
		v.Error, v.ErrorCode = ErrOff.Message, ErrOff.Code
		return v
	}
	st, err := s.o.Client.Status(ctx)
	if err != nil {
		v.Error, v.ErrorCode = err.Error(), "files_status_failed"
		if e, ok := apperr.As(err); ok {
			v.Error, v.ErrorCode = e.Message, e.Code
		}
		return v
	}
	v.Status = st
	return v
}

// Record is a file with its uploader's username (accounts).
type Record struct {
	bosonapi.FileRecord
	Username string `json:"username,omitempty"`
}

// Page is GET /api/files.
type Page struct {
	Files      []Record `json:"files"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

const (
	maxQuery = 200
	maxLimit = 100
	// maxNames bounds username lookups per page.
	maxNames = 50
)

func (s *Service) List(ctx context.Context, q bosonapi.FilesQuery, names Resolver) (*Page, error) {
	if s.o.Client == nil {
		return nil, ErrOff
	}
	q.User, q.Q, q.Status = strings.TrimSpace(q.User), strings.TrimSpace(q.Q), strings.TrimSpace(q.Status)
	if q.Status != "" && !slices.Contains(bosonapi.FileStatuses, q.Status) {
		return nil, apperr.InvalidField("status", "status must be one of "+strings.Join(bosonapi.FileStatuses, ", "))
	}
	if len(q.Q) > maxQuery || len(q.User) > maxQuery || len(q.Cursor) > 1024 {
		return nil, apperr.InvalidField("q", "the search is too long")
	}
	if q.Limit <= 0 || q.Limit > maxLimit {
		q.Limit = 50
	}
	p, err := s.o.Client.List(ctx, q)
	if err != nil {
		return nil, err
	}
	out := &Page{Files: make([]Record, len(p.Files)), NextCursor: p.NextCursor}
	seen := map[string]string{}
	for i, f := range p.Files {
		out.Files[i] = Record{FileRecord: f}
		if names == nil || f.UserKind != "account" || f.User == "" {
			continue
		}
		name, ok := seen[f.User]
		if !ok && len(seen) < maxNames {
			name = names(ctx, f.User)
			seen[f.User] = name
		}
		out.Files[i].Username = name
	}
	return out, nil
}

// Delete removes a file for moderation (it isn't refunded: the upload
// still counts against its owner's window). Logged with the actor.
func (s *Service) Delete(ctx context.Context, actor, id, reason string) error {
	if s.o.Client == nil {
		return ErrOff
	}
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 128 || strings.ContainsAny(id, "/?#") {
		return apperr.InvalidField("id", "not a file id")
	}
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) > 500 {
		return apperr.InvalidField("reason", "the reason must be at most 500 characters")
	}
	err := s.o.Client.Delete(ctx, actor, id)
	ev := logger.FromCtx(ctx).Info()
	outcome := "success"
	if err != nil {
		ev, outcome = logger.FromCtx(ctx).Warn().Err(err), "error"
	}
	ev.Str("actor", actor).Str("action", "files.delete").Str("outcome", outcome).
		Str("file", id).Str("reason", reason).Msg("uploaded file deleted")
	return err
}

func (s *Service) Usage(ctx context.Context, userID string) (*bosonapi.FilesUsage, error) {
	if s.o.Client == nil {
		return nil, ErrOff
	}
	return s.o.Client.Usage(ctx, userID)
}

// OverrideRequest is PUT /api/users/{id}/files-quota: sizes like 10GB;
// "" or absent = the class default.
type OverrideRequest struct {
	Quota   string `json:"quota"`
	MaxFile string `json:"max_file"`
}

func (s *Service) SetOverride(ctx context.Context, actor, userID string, in *OverrideRequest) (*bosonapi.FilesUsage, error) {
	if s.o.Client == nil {
		return nil, ErrOff
	}
	var body *bosonapi.FilesOverrideInput
	if in != nil {
		body = &bosonapi.FilesOverrideInput{}
		parse := func(field, v string, min int64) (*int64, error) {
			if strings.TrimSpace(v) == "" {
				return nil, nil
			}
			n, err := stackconfig.ParseSize(v)
			if err != nil {
				return nil, apperr.InvalidField(field, err.Error())
			}
			if n < min || n > stackconfig.MaxSize {
				return nil, apperr.InvalidField(field, "must be from "+stackconfig.FormatSize(min)+" to "+stackconfig.FormatSize(stackconfig.MaxSize))
			}
			return &n, nil
		}
		var err error
		if body.QuotaBytes, err = parse("quota", in.Quota, stackconfig.MinQuota); err != nil {
			return nil, err
		}
		if body.MaxFileBytes, err = parse("max_file", in.MaxFile, stackconfig.MinFile); err != nil {
			return nil, err
		}
		if body.QuotaBytes != nil && body.MaxFileBytes != nil && *body.MaxFileBytes > *body.QuotaBytes {
			return nil, apperr.InvalidField("max_file", "the largest file can't be bigger than the quota")
		}
		if body.QuotaBytes == nil && body.MaxFileBytes == nil {
			body = nil // nothing left: clear it
		}
	}
	u, err := s.o.Client.SetOverride(ctx, actor, userID, body)
	ev := logger.FromCtx(ctx).Info()
	outcome := "success"
	if err != nil {
		ev, outcome = logger.FromCtx(ctx).Warn().Err(err), "error"
	}
	ev = ev.Str("actor", actor).Str("action", "files.quota").Str("outcome", outcome).Str("user", userID)
	if body != nil {
		if body.QuotaBytes != nil {
			ev = ev.Int64("quota_bytes", *body.QuotaBytes)
		}
		if body.MaxFileBytes != nil {
			ev = ev.Int64("max_file_bytes", *body.MaxFileBytes)
		}
	} else {
		ev = ev.Bool("cleared", true)
	}
	ev.Msg("uploads quota override")
	return u, err
}

// Check refuses a files: section that can't work: the s3 driver without
// an endpoint and a bucket, and a per-file limit above the quota.
func (s *Service) Check(_ context.Context, after, _ stackconfig.Lookup) error {
	str := func(key, def string) string {
		if v, ok := after(key); ok {
			if s, _ := v.(string); s != "" {
				return s
			}
		}
		return def
	}
	if str(stackconfig.KeyFilesDriver, "local") == "s3" {
		for _, k := range []struct{ key, what string }{
			{stackconfig.KeyFilesS3Endpoint, "an endpoint"},
			{stackconfig.KeyFilesS3Bucket, "a bucket"},
		} {
			if strings.TrimSpace(str(k.key, "")) == "" {
				return apperr.InvalidField(k.key, "S3 storage needs "+k.what)
			}
		}
	}
	for _, c := range []struct{ quota, maxFile, defQuota, defMax, who string }{
		{stackconfig.KeyFilesAccountQuota, stackconfig.KeyFilesAccountMaxFile, DefaultAccountQuota, DefaultAccountMaxFile, "an account"},
		{stackconfig.KeyFilesGuestQuota, stackconfig.KeyFilesGuestMaxFile, DefaultGuestQuota, DefaultGuestMaxFile, "a guest"},
	} {
		q, err1 := stackconfig.ParseSize(str(c.quota, c.defQuota))
		m, err2 := stackconfig.ParseSize(str(c.maxFile, c.defMax))
		if err1 == nil && err2 == nil && m > q {
			return apperr.InvalidField(c.maxFile, "the largest file for "+c.who+" can't be bigger than its quota ("+stackconfig.FormatSize(q)+")")
		}
	}
	return nil
}
