// Package branding is the network's look (branding contract, section 2):
// the theme and its images in the stack config file, the image files in
// config/branding/, and the auth service's email preview and test sends.
//
// The theme and the email settings are ordinary config keys
// (branding.theme, auth.email.*), edited through PATCH /api/config like
// every other setting. This package adds what doesn't fit a key:
//
//   - image uploads: sniffed, size-checked, re-encoded to WebP (SVG checked
//     and kept), written atomically as config/branding/<slot>-<hash>.<ext>,
//     recorded in branding.images and the replaced file removed;
//   - the files themselves, for the panel's preview;
//   - email previews (images inlined, for a sandboxed srcdoc iframe) and test
//     sends through auth's internal listener;
//   - the bundle the directory sync pushes to this network's listing.
package branding

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/configfile"
	"github.com/boson-chat/admin/internal/logger"
	"github.com/boson-chat/admin/internal/services/stackconfig"
	"github.com/boson-chat/admin/internal/theme"
	"gopkg.in/yaml.v3"
)

type Options struct {
	Config *stackconfig.Service
	// Dir is config/branding/ (default: next to the config file).
	Dir string
	// Email is auth's internal email API; nil when AUTH_INTERNAL_URL or
	// INTERNAL_API_TOKEN isn't set.
	Email authapi.EmailClientImpl
}

// Image is one stored image.
type Image struct {
	Slot        string `json:"slot"`
	File        string `json:"file"`
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Size        int    `json:"size"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
}

// Asset is an image file to serve.
type Asset struct {
	Name        string
	ContentType string
	Data        []byte
	ModTime     time.Time
	// Immutable: a content-addressed name the panel wrote.
	Immutable bool
}

type BrandingServiceImpl interface {
	Upload(ctx context.Context, actor, ifMatch, slot string, data []byte) (*Image, error)
	Remove(ctx context.Context, actor, ifMatch, slot string) error
	Asset(name string) (*Asset, error)
	// Bundle is the theme and its images as the directory takes them.
	Bundle(ctx context.Context) (*theme.Bundle, error)

	EmailEnabled() bool
	// Preview and SendTest call auth as the signed-in admin: with runs a
	// call with their access token (admin.AdminServiceImpl.WithToken).
	Preview(ctx context.Context, actor string, with TokenCall, in authapi.EmailPreviewRequest) (*authapi.EmailPreview, error)
	SendTest(ctx context.Context, actor string, with TokenCall, in authapi.EmailTestRequest) (*authapi.EmailTestResult, error)
}

// TokenCall runs fn with a valid access token for the acting admin.
type TokenCall func(fn func(accessToken string) error) error

type BrandingService struct{ o Options }

func NewBrandingService(o Options) BrandingServiceImpl {
	if o.Dir == "" && o.Config != nil && o.Config.Path() != "" {
		o.Dir = filepath.Join(filepath.Dir(o.Config.Path()), "branding")
	}
	return &BrandingService{o: o}
}

// AssetURL is where the panel serves a stored file.
func AssetURL(file string) string { return "/api/branding/assets/" + file }

var (
	errOff = apperr.New(http.StatusServiceUnavailable, "config_file_off",
		"branding is kept in the stack config file: set BOSON_CONFIG=/config/boson.yaml on the admin service and mount the stack's config/ directory read-write")
	errSlot = apperr.Invalid("unknown image slot; one of " + strings.Join(theme.Slots, ", "))
)

func (b *BrandingService) enabled() bool {
	return b.o.Config != nil && b.o.Config.Enabled() && b.o.Dir != ""
}

// images reads branding.images from a root mapping (nil-safe). Entries
// that aren't valid are dropped, so an upload repairs a bad hand edit
// instead of failing on it.
func images(root *yaml.Node) map[string]string {
	out := map[string]string{}
	_, n := configfile.Lookup(root, configfile.Split(stackconfig.KeyBrandingImages))
	if n == nil || n.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if v.Kind != yaml.ScalarNode {
			continue
		}
		if m, err := theme.CleanImages(map[string]any{k.Value: v.Value}); err == nil {
			out[k.Value] = m[k.Value]
		}
	}
	return out
}

func (b *BrandingService) current() map[string]string {
	snap := b.o.Config.Snapshot()
	if snap == nil {
		return map[string]string{}
	}
	return images(snap.Root())
}

func hashName(slot string, p *Processed) string {
	sum := sha256.Sum256(p.Data)
	return theme.FileStem(slot) + "-" + hex.EncodeToString(sum[:8]) + "." + p.Ext
}

func (b *BrandingService) Upload(ctx context.Context, actor, ifMatch, slot string, data []byte) (*Image, error) {
	if !b.enabled() {
		return nil, errOff
	}
	if !theme.ValidSlot(slot) {
		return nil, errSlot
	}
	p, err := Process(data)
	if err != nil {
		logger.FromCtx(ctx).Info().Str("actor", actor).Str("action", "branding.image.upload").Str("slot", slot).
			Str("outcome", "rejected").Err(err).Msg("branding image")
		return nil, err
	}
	name := hashName(slot, p)
	if err := os.MkdirAll(b.o.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", b.o.Dir, err)
	}
	path := filepath.Join(b.o.Dir, name)
	_, statErr := os.Stat(path)
	existed := statErr == nil
	if !existed {
		if err := writeAtomic(path, p.Data); err != nil {
			return nil, err
		}
	}
	var old string
	var after map[string]string
	err = b.o.Config.Write(ctx, actor, ifMatch, "branding.image.upload", []string{stackconfig.KeyBrandingImages},
		func(root *yaml.Node) ([]string, error) {
			m := images(root)
			old = m[slot]
			m[slot] = name
			after = m
			if err := configfile.Set(root, configfile.Split(stackconfig.KeyBrandingImages), m); err != nil {
				return nil, apperr.Invalid(err.Error())
			}
			return []string{fmt.Sprintf("%s.%s: %s → %s (%s %d×%d, %d bytes)", stackconfig.KeyBrandingImages, slot,
				orNone(old), name, p.Source, p.Width, p.Height, len(p.Data))}, nil
		})
	if err != nil {
		if !existed && !slices.Contains(slices.Collect(mapValues(b.current())), name) {
			_ = os.Remove(path)
		}
		return nil, err
	}
	b.removeUnused(ctx, old, after)
	return &Image{Slot: slot, File: name, URL: AssetURL(name), ContentType: p.ContentType, Size: len(p.Data), Width: p.Width, Height: p.Height}, nil
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func mapValues(m map[string]string) func(func(string) bool) {
	return func(yield func(string) bool) {
		for _, v := range m {
			if !yield(v) {
				return
			}
		}
	}
}

// removeUnused deletes a replaced file once no slot names it.
func (b *BrandingService) removeUnused(ctx context.Context, file string, now map[string]string) {
	if file == "" || file != filepath.Base(file) || strings.HasPrefix(file, ".") {
		return
	}
	if slices.Contains(slices.Collect(mapValues(now)), file) {
		return
	}
	if err := os.Remove(filepath.Join(b.o.Dir, file)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		logger.FromCtx(ctx).Warn().Err(err).Str("file", file).Msg("removing a replaced branding image")
	}
}

func (b *BrandingService) Remove(ctx context.Context, actor, ifMatch, slot string) error {
	if !b.enabled() {
		return errOff
	}
	if !theme.ValidSlot(slot) {
		return errSlot
	}
	var old string
	var after map[string]string
	err := b.o.Config.Write(ctx, actor, ifMatch, "branding.image.remove", []string{stackconfig.KeyBrandingImages},
		func(root *yaml.Node) ([]string, error) {
			m := images(root)
			old = m[slot]
			if old == "" {
				return nil, nil
			}
			delete(m, slot)
			after = m
			path := configfile.Split(stackconfig.KeyBrandingImages)
			if len(m) == 0 {
				configfile.Unset(root, path)
			} else if err := configfile.Set(root, path, m); err != nil {
				return nil, apperr.Invalid(err.Error())
			}
			return []string{stackconfig.KeyBrandingImages + "." + slot + ": " + old + " → (none)"}, nil
		})
	if err != nil {
		return err
	}
	b.removeUnused(ctx, old, after)
	return nil
}

var contentTypes = map[string]string{
	".webp": "image/webp", ".svg": "image/svg+xml", ".png": "image/png",
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
}

// Asset reads a file of config/branding/ that the config names (or that
// the panel wrote).
func (b *BrandingService) Asset(name string) (*Asset, error) {
	if !b.enabled() {
		return nil, apperr.ErrNotFound
	}
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		return nil, apperr.ErrNotFound
	}
	ct, ok := contentTypes[strings.ToLower(filepath.Ext(name))]
	ours := theme.FileRe.MatchString(name)
	if !ok || (!ours && !slices.Contains(slices.Collect(mapValues(b.current())), name)) {
		return nil, apperr.ErrNotFound
	}
	path := filepath.Join(b.o.Dir, name)
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() || fi.Size() > 4*MaxImageBytes {
		return nil, apperr.ErrNotFound
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, apperr.ErrNotFound
	}
	return &Asset{Name: name, ContentType: ct, Data: data, ModTime: fi.ModTime(), Immutable: ours}, nil
}

// Bundle is the stored theme with its image files, for the directory.
func (b *BrandingService) Bundle(ctx context.Context) (*theme.Bundle, error) {
	if !b.enabled() {
		return nil, errOff
	}
	out := &theme.Bundle{Theme: theme.Default(), Images: map[string]theme.BundleImage{}}
	if v, ok := b.o.Config.FileValue(stackconfig.KeyBrandingTheme); ok {
		out.Theme = v.(theme.Theme)
	}
	for slot, file := range b.current() {
		a, err := b.Asset(file)
		if err != nil {
			logger.FromCtx(ctx).Warn().Str("slot", slot).Str("file", file).Msg("branding image named in the config is missing")
			continue
		}
		sum := sha256.Sum256(a.Data)
		out.Images[slot] = theme.BundleImage{ContentType: a.ContentType, Data: a.Data, Hash: hex.EncodeToString(sum[:])}
	}
	return out, nil
}

// writeAtomic writes data to path through a temp file in the same
// directory (fsync, rename), mode 0644 so every service can read it.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", filepath.Base(path), err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Chmod(0o644)
	}
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return fmt.Errorf("writing %s: %w", filepath.Base(path), werr)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("writing %s: %w", filepath.Base(path), err)
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// ---- email ----

var errEmailOff = apperr.New(http.StatusServiceUnavailable, "email_preview_off",
	"email previews go through the auth service's internal listener: set AUTH_INTERNAL_URL on the admin service")

func (b *BrandingService) EmailEnabled() bool { return b.o.Email != nil }

func checkTemplate(t string) error {
	if !slices.Contains(stackconfig.EmailTemplates, t) {
		return apperr.Invalid("template must be one of " + strings.Join(stackconfig.EmailTemplates, ", "))
	}
	return nil
}

func (b *BrandingService) Preview(ctx context.Context, actor string, with TokenCall, in authapi.EmailPreviewRequest) (*authapi.EmailPreview, error) {
	if b.o.Email == nil {
		return nil, errEmailOff
	}
	if err := checkTemplate(in.Template); err != nil {
		return nil, err
	}
	if in.Subject != nil && len([]rune(*in.Subject)) > 200 {
		return nil, apperr.Invalid("the subject must be at most 200 characters")
	}
	if in.Body != nil && len([]rune(*in.Body)) > 5000 {
		return nil, apperr.Invalid("the body must be at most 5000 characters")
	}
	var p *authapi.EmailPreview
	err := with(func(at string) error {
		var err error
		p, err = b.o.Email.Preview(ctx, at, in)
		return err
	})
	if err != nil {
		return nil, err
	}
	p.HTML = b.inline(p.HTML)
	return p, nil
}

// assetRef is a branding image URL in auth's rendered email.
var assetRef = regexp.MustCompile(`(?:https?:)?//[^"'\s()<>]*?/v1/branding/assets/([A-Za-z0-9._-]+)(?:\?[^"'\s()<>]*)?`)

// inline turns branding image URLs into data: URIs: the preview is a
// sandboxed srcdoc iframe under the panel's CSP (img-src 'self' data:),
// and the auth service's public host may not even resolve from here.
func (b *BrandingService) inline(html string) string {
	cache := map[string]string{}
	return assetRef.ReplaceAllStringFunc(html, func(ref string) string {
		m := assetRef.FindStringSubmatch(ref)
		if v, ok := cache[m[1]]; ok {
			return v
		}
		a, err := b.Asset(m[1])
		if err != nil || len(a.Data) > MaxImageBytes {
			cache[m[1]] = ref
			return ref
		}
		v := "data:" + a.ContentType + ";base64," + base64.StdEncoding.EncodeToString(a.Data)
		cache[m[1]] = v
		return v
	})
}

func (b *BrandingService) SendTest(ctx context.Context, actor string, with TokenCall, in authapi.EmailTestRequest) (*authapi.EmailTestResult, error) {
	if b.o.Email == nil {
		return nil, errEmailOff
	}
	if err := checkTemplate(in.Template); err != nil {
		return nil, err
	}
	in.To = strings.TrimSpace(in.To)
	addr, err := mail.ParseAddress(in.To)
	if err != nil || addr.Name != "" || len(in.To) > 254 || bytes.ContainsAny([]byte(in.To), "\r\n") {
		return nil, apperr.Invalid("send the test to one email address, like you@example.com")
	}
	var r *authapi.EmailTestResult
	err = with(func(at string) error {
		var err error
		r, err = b.o.Email.SendTest(ctx, at, in)
		return err
	})
	ev := logger.FromCtx(ctx).Info()
	if err != nil {
		ev = logger.FromCtx(ctx).Warn().Err(err)
	}
	ev.Str("actor", actor).Str("action", "email.test").Str("template", in.Template).Str("to", in.To).Msg("test email")
	return r, err
}
