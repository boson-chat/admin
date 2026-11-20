package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/services/admin"
	"github.com/boson-chat/admin/internal/services/directory"
	"github.com/boson-chat/admin/internal/services/stackconfig"
	"github.com/boson-chat/admin/internal/session"
)

// The stack config file (config/boson.yaml): GET /api/config,
// /api/config/raw, /api/config/status and PATCH /api/config. Reading needs
// admin.panel (the file holds no secrets); each key's write needs its own
// permission (stackconfig.Keys).

// configWrite: PATCH /api/config takes any of these; the per-key check is
// in stackconfig.
const configWrite = admin.PermSettingsManage + "|" + admin.PermDirectoryManage + "|" + admin.PermServicesManage

func (h *Handlers) can(r *http.Request, s *session.Session) func(string) bool {
	a, err := h.Admin.Access(r.Context(), s, false)
	return func(perm string) bool { return err == nil && a.CanAny(strings.Split(perm, "|")...) }
}

func (h *Handlers) getConfig(w http.ResponseWriter, r *http.Request, s *session.Session) {
	v := h.Config.View(h.can(r, s))
	if v.ETag != "" {
		w.Header().Set("ETag", v.ETag)
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handlers) getConfigRaw(w http.ResponseWriter, r *http.Request, s *session.Session) {
	raw, err := h.Config.Raw()
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, raw)
}

func (h *Handlers) getConfigStatus(w http.ResponseWriter, r *http.Request, s *session.Session) {
	writeJSON(w, http.StatusOK, map[string]any{"services": h.Config.Statuses(r.Context())})
}

func (h *Handlers) patchConfig(w http.ResponseWriter, r *http.Request, s *session.Session) {
	var in stackconfig.Change
	if err := decode(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	if !h.writeConfig(w, r, s, in, func(v *stackconfig.View) any { return v }) {
		return
	}
	for _, k := range in.Keys() {
		if strings.HasPrefix(k, "branding.") {
			h.pushBranding(r.Context(), s.Snapshot().Username)
			break
		}
	}
}

// writeConfig saves a change and answers with render(view).
func (h *Handlers) writeConfig(w http.ResponseWriter, r *http.Request, s *session.Session, c stackconfig.Change, render func(*stackconfig.View) any) bool {
	v, err := h.Config.Update(r.Context(), s.Snapshot().Username, r.Header.Get("If-Match"), c, h.can(r, s))
	if err != nil {
		writeError(w, r, configErr(err))
		return false
	}
	if v.ETag != "" {
		w.Header().Set("ETag", v.ETag)
	}
	writeJSON(w, http.StatusOK, render(v))
	return true
}

func configErr(err error) error {
	var mp *stackconfig.MissingPermissionError
	if errors.As(err, &mp) {
		return admin.MissingPermission(mp.Perm)
	}
	return err
}

// ---- Network settings (auth) through the file ----

// settingsKeys are NetworkSettings' JSON names that live under auth.
var settingsKeys = []string{"registration", "require_email_verification", "guest_access", "guest_voice", "channel_creation",
	"voice_channel_creation", "channel_auto_register", "channel_expiry_days"}

// fileSettings is the Network settings response when the file manages
// them: auth's answer (its effective values) with the file's values on top
// (auth may not have reloaded yet), and where each comes from.
func (h *Handlers) fileSettings(st *admin.Settings) map[string]any {
	var m map[string]any
	b, _ := json.Marshal(st)
	_ = json.Unmarshal(b, &m)
	if m == nil {
		m = map[string]any{}
	}
	src := map[string]string{}
	for _, k := range settingsKeys {
		src[k] = "default"
		if v, ok := h.Config.FileValue("auth." + k); ok {
			m[k], src[k] = v, "file"
		}
	}
	return map[string]any{"settings": m, "config": map[string]any{"managed": true, "sources": src}}
}

func settingsChange(p authapi.SettingsPatch) stackconfig.Change {
	c := stackconfig.Change{Set: map[string]any{}}
	b, _ := json.Marshal(p)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for k, v := range m {
		c.Set["auth."+k] = v
	}
	return c
}

// ---- the stack directory's settings through the file ----

func (h *Handlers) fileFederation(f *directory.Federation) *directory.Federation {
	if f == nil || f.Settings == nil {
		return f
	}
	cp := *f.Settings
	str := func(key string, dst *string) {
		if v, ok := h.Config.FileValue(key); ok {
			*dst = v.(string)
		}
	}
	str("directory.name", &cp.Settings.Name)
	str("directory.operator", &cp.Settings.Operator)
	str("directory.contact", &cp.Settings.Contact)
	str("directory.sync_every", &cp.Settings.SyncEvery)
	str("directory.main_peer_url", &cp.Settings.MainPeerURL)
	if v, ok := h.Config.FileValue("directory.include_main"); ok {
		cp.Settings.IncludeMain = v.(bool)
		if !cp.IsMain {
			cp.MainActive = cp.Settings.IncludeMain
		}
	}
	f.Settings = &cp
	return f
}

func directoryChange(u directory.SettingsUpdate) stackconfig.Change {
	c := stackconfig.Change{Set: map[string]any{}}
	put := func(k string, v *string) {
		if v != nil {
			c.Set["directory."+k] = *v
		}
	}
	put("name", u.Name)
	put("operator", u.Operator)
	put("contact", u.Contact)
	put("sync_every", u.SyncEvery)
	return c
}
