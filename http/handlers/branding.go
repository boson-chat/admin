package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/logger"
	"github.com/boson-chat/admin/internal/services/branding"
	"github.com/boson-chat/admin/internal/session"
)

// Branding (Branding and Emails pages). The theme and the email settings
// are config keys (branding.theme, auth.email.*) saved with PATCH
// /api/config; these routes are the rest:
//
//	PUT    /api/branding/images/{slot}   raw image body (settings.manage)
//	DELETE /api/branding/images/{slot}   (settings.manage)
//	GET    /api/branding/assets/{file}   the stored file (admin.panel)
//	POST   /api/emails/preview           {template, subject?, body?} (settings.manage)
//	POST   /api/emails/test              {template, to} (settings.manage)
//
// Image writes answer with the config view, like PATCH /api/config.

var errBrandingOff = apperr.New(http.StatusServiceUnavailable, "branding_off", "branding isn't available on this panel")

func (h *Handlers) brandingOn(w http.ResponseWriter, r *http.Request) bool {
	if h.Branding == nil {
		writeError(w, r, errBrandingOff)
		return false
	}
	return true
}

// readUpload reads a raw image body, at most the limit plus a little (the
// service gives the precise error).
func readUpload(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	if !strings.HasPrefix(ct, "image/") && !strings.HasPrefix(ct, "application/octet-stream") {
		return nil, apperr.New(http.StatusUnsupportedMediaType, "unsupported_media_type", "send the image file as the body (Content-Type image/…)")
	}
	r.Body = http.MaxBytesReader(w, r.Body, branding.MaxImageBytes+1)
	data, err := io.ReadAll(r.Body)
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return nil, apperr.New(http.StatusRequestEntityTooLarge, "image_too_large", "the image is too large (2 MB at most)")
	}
	if err != nil {
		return nil, apperr.Invalid("couldn't read the upload")
	}
	return data, nil
}

func (h *Handlers) putBrandingImage(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if !h.brandingOn(w, r) {
		return
	}
	data, err := readUpload(w, r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	actor := s.Snapshot().Username
	img, err := h.Branding.Upload(r.Context(), actor, r.Header.Get("If-Match"), r.PathValue("slot"), data)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.pushBranding(r.Context(), actor)
	v := h.Config.View(h.can(r, s))
	if v.ETag != "" {
		w.Header().Set("ETag", v.ETag)
	}
	writeJSON(w, http.StatusOK, map[string]any{"image": img, "config": v})
}

func (h *Handlers) deleteBrandingImage(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if !h.brandingOn(w, r) {
		return
	}
	actor := s.Snapshot().Username
	if err := h.Branding.Remove(r.Context(), actor, r.Header.Get("If-Match"), r.PathValue("slot")); err != nil {
		writeError(w, r, err)
		return
	}
	h.pushBranding(r.Context(), actor)
	v := h.Config.View(h.can(r, s))
	if v.ETag != "" {
		w.Header().Set("ETag", v.ETag)
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": v})
}

func (h *Handlers) brandingAsset(w http.ResponseWriter, r *http.Request, _ *session.Session) {
	if !h.brandingOn(w, r) {
		return
	}
	a, err := h.Branding.Asset(r.PathValue("file"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", a.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Opened on its own, an SVG can't run or load anything.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:")
	w.Header().Set("Content-Disposition", "inline")
	if a.Immutable {
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	http.ServeContent(w, r, a.Name, a.ModTime, bytes.NewReader(a.Data))
}

func (h *Handlers) emailPreview(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if !h.brandingOn(w, r) {
		return
	}
	var in authapi.EmailPreviewRequest
	if err := decode(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	p, err := h.Branding.Preview(r.Context(), s.Snapshot().Username, h.tokenCall(r, s), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *Handlers) emailTest(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if !h.brandingOn(w, r) {
		return
	}
	var in authapi.EmailTestRequest
	if err := decode(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	res, err := h.Branding.SendTest(r.Context(), s.Snapshot().Username, h.tokenCall(r, s), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// pushBranding updates this network's directory listing with the new
// branding in the background (the save already succeeded; the Directory
// page reports the listing).
func (h *Handlers) pushBranding(ctx context.Context, actor string) {
	if h.Directory == nil || h.Branding == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		// The directory gets auth's /v1/branding: wait until auth has
		// loaded the file just written.
		h.Branding.WaitApplied(ctx, 30*time.Second)
		res, err := h.Directory.PushBranding(ctx, actor)
		if err != nil {
			logger.FromCtx(ctx).Warn().Err(err).Str("actor", actor).Msg("pushing branding to the directory")
			return
		}
		if res != nil && !res.OK && !res.Unsupported {
			logger.FromCtx(ctx).Warn().Str("actor", actor).Str("error", res.Error).Msg("pushing branding to the directory")
		}
	}()
}

// tokenCall runs auth calls with the signed-in admin's access token.
func (h *Handlers) tokenCall(r *http.Request, s *session.Session) branding.TokenCall {
	return func(fn func(string) error) error { return h.Admin.WithToken(r.Context(), s, fn) }
}
