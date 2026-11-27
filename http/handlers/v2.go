package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/clients/bosonapi"
	"github.com/boson-chat/admin/internal/services/containers"
	"github.com/boson-chat/admin/internal/services/directory"
	"github.com/boson-chat/admin/internal/services/stackconfig"
	"github.com/boson-chat/admin/internal/session"
)

// ---- network settings ----

func (h *Handlers) getSettings(w http.ResponseWriter, r *http.Request, s *session.Session) {
	st, err := h.Admin.GetSettings(r.Context(), s)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if h.Config.Enabled() {
		writeJSON(w, http.StatusOK, h.fileSettings(st))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": st})
}

// updateSettings writes auth's section of the stack config file when there
// is one (auth then refuses its own PATCH), else PATCHes auth as before.
func (h *Handlers) updateSettings(w http.ResponseWriter, r *http.Request, s *session.Session) {
	var p authapi.SettingsPatch
	if err := decode(w, r, &p); err != nil {
		writeError(w, r, err)
		return
	}
	if h.Config.Enabled() {
		if p == (authapi.SettingsPatch{}) {
			writeError(w, r, apperr.Invalid("nothing to update"))
			return
		}
		if err := h.checkGuestFirstFile(p); err != nil {
			writeError(w, r, err)
			return
		}
		if _, err := h.Config.Update(r.Context(), s.Snapshot().Username, r.Header.Get("If-Match"), settingsChange(p), h.can(r, s)); err != nil {
			writeError(w, r, configErr(err))
			return
		}
		h.getSettings(w, r, s)
		return
	}
	st, err := h.Admin.UpdateSettings(r.Context(), s, p)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": st})
}

// checkGuestFirstFile refuses a file change auth would refuse: guest_first
// on with guest_access off, or landing on a channel kept to roles. Values
// not in the patch come from the file (guest_access defaults to on).
func (h *Handlers) checkGuestFirstFile(p authapi.SettingsPatch) error {
	if p.GuestFirst == nil && p.GuestAccess == nil && p.GuestFirstChannel == nil {
		return nil
	}
	fileBool := func(key string, def bool, v *bool) bool {
		if v != nil {
			return *v
		}
		if x, ok := h.Config.FileValue(key); ok {
			if b, ok := x.(bool); ok {
				return b
			}
		}
		return def
	}
	access := fileBool(stackconfig.KeyGuestAccess, true, p.GuestAccess)
	first := fileBool(stackconfig.KeyGuestFirst, false, p.GuestFirst)
	channel := ""
	if p.GuestFirstChannel != nil {
		channel = *p.GuestFirstChannel
	} else if x, ok := h.Config.FileValue(stackconfig.KeyGuestFirstChannel); ok {
		channel, _ = x.(string)
	}
	chans, _ := h.Config.NetworkChannels()
	if err := stackconfig.CheckGuestFirst(access, first, channel, chans); err != nil {
		return apperr.Invalid(err.Error())
	}
	return nil
}

// ---- SSO providers ----

func (h *Handlers) listProviders(w http.ResponseWriter, r *http.Request, s *session.Session) {
	list, err := h.Admin.ListSSOProviders(r.Context(), s)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": list})
}

func (h *Handlers) getProvider(w http.ResponseWriter, r *http.Request, s *session.Session) {
	p, err := h.Admin.GetSSOProvider(r.Context(), s, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"provider": p})
}

func (h *Handlers) createProvider(w http.ResponseWriter, r *http.Request, s *session.Session) {
	var in authapi.SSOProviderInput
	if err := decode(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	p, err := h.Admin.CreateSSOProvider(r.Context(), s, in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"provider": p})
}

func (h *Handlers) updateProvider(w http.ResponseWriter, r *http.Request, s *session.Session) {
	var in authapi.SSOProviderInput
	if err := decode(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	p, err := h.Admin.UpdateSSOProvider(r.Context(), s, r.PathValue("id"), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"provider": p})
}

func (h *Handlers) deleteProvider(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if err := h.Admin.DeleteSSOProvider(r.Context(), s, r.PathValue("id")); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) testProvider(w http.ResponseWriter, r *http.Request, s *session.Session) {
	res, err := h.Admin.TestSSOProvider(r.Context(), s, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ---- users ----

func (h *Handlers) createUser(w http.ResponseWriter, r *http.Request, s *session.Session) {
	var in authapi.CreateUser
	if err := decode(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := h.Admin.CreateUser(r.Context(), s, in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handlers) sendPasswordReset(w http.ResponseWriter, r *http.Request, s *session.Session) {
	out, err := h.Admin.SendPasswordReset(r.Context(), s, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if out.EmailSent {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Auth can't send email: the admin shares the link themselves.
	writeJSON(w, http.StatusOK, out)
}

func (h *Handlers) resendVerification(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if err := h.Admin.ResendVerification(r.Context(), s, r.PathValue("id")); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// userProfile is the user's boson profile. It always answers 200 with
// what is known, so the user page degrades: configured=false (no
// BOSON_API_URL), unsupported (the API lacks the admin endpoint), error.
func (h *Handlers) userProfile(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if h.Profiles == nil {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false})
		return
	}
	at, err := h.Admin.AccessToken(r.Context(), s)
	if err != nil {
		writeError(w, r, err)
		return
	}
	p, err := h.Profiles.AdminProfile(r.Context(), at, r.PathValue("id"))
	switch {
	case apperr.IsUnsupported(err):
		e, _ := apperr.As(err)
		writeJSON(w, http.StatusOK, map[string]any{"configured": true, "unsupported": e.Message})
	case err != nil:
		msg := "the boson API didn't answer"
		if e, ok := apperr.As(err); ok {
			msg = e.Message
		}
		writeJSON(w, http.StatusOK, map[string]any{"configured": true, "error": msg})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"configured": true, "profile": p.Profile, "servers": p.Servers})
	}
}

// ---- services (Docker) ----

func (h *Handlers) listServices(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if h.Containers == nil {
		writeJSON(w, http.StatusOK, containers.List{Services: []containers.Service{}, CheckedAt: time.Now().UTC()})
		return
	}
	writeJSON(w, http.StatusOK, h.Containers.List(r.Context()))
}

func (h *Handlers) containersOr503(w http.ResponseWriter, r *http.Request) bool {
	if h.Containers == nil {
		writeError(w, r, containers.ErrNotConfigured)
		return false
	}
	return true
}

func (h *Handlers) getService(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if !h.containersOr503(w, r) {
		return
	}
	v, err := h.Containers.Get(r.Context(), r.PathValue("name"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"service": v})
}

func (h *Handlers) serviceAction(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if !h.containersOr503(w, r) {
		return
	}
	var body struct {
		Confirm string `json:"confirm"`
	}
	if err := decode(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	res, err := h.Containers.Action(r.Context(), s.Snapshot().Username, r.PathValue("name"), r.PathValue("action"), body.Confirm)
	if err != nil {
		writeError(w, r, err)
		return
	}
	status := http.StatusOK
	if res.Deferred {
		status = http.StatusAccepted
	}
	writeJSON(w, status, res)
}

func (h *Handlers) serviceLogs(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if !h.containersOr503(w, r) {
		return
	}
	q := r.URL.Query()
	download := q.Get("download") == "1"
	maxTail := containers.MaxTail
	if download {
		maxTail = containers.MaxDownload
	}
	tail := intQuery(r, "tail", 200, 1, maxTail)
	var since time.Time
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			writeError(w, r, apperr.Invalid("since must be an RFC 3339 timestamp"))
			return
		}
		since = t
	}
	name := r.PathValue("name")
	lines, err := h.Containers.Logs(r.Context(), name, tail, since)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if download {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.log"`,
			strings.NewReplacer(`"`, "", "/", "", "\\", "").Replace(name), time.Now().UTC().Format("20060102T150405Z")))
		w.Header().Set("Cache-Control", "no-store")
		var b strings.Builder
		for _, l := range lines {
			ts := ""
			if !l.At.IsZero() {
				ts = l.At.UTC().Format(time.RFC3339Nano) + " "
			}
			b.WriteString(ts + l.Stream + " " + l.Text + "\n")
		}
		_, _ = w.Write([]byte(b.String()))
		return
	}
	var last *time.Time
	for i := len(lines) - 1; i >= 0; i-- {
		if !lines[i].At.IsZero() {
			last = &lines[i].At
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines, "last_at": last, "truncated": len(lines) >= tail})
}

// ---- directory ----

func (h *Handlers) directoryOr503(w http.ResponseWriter, r *http.Request) bool {
	if h.Directory == nil {
		writeError(w, r, apperr.New(http.StatusServiceUnavailable, "directory_off", "the directory is off: DIRECTORY_API_URL is empty"))
		return false
	}
	return true
}

func (h *Handlers) directoryStatus(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if !h.directoryOr503(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, h.Directory.Status(r.Context(), r.URL.Query().Get("hostname")))
}

func (h *Handlers) directorySubmit(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if !h.directoryOr503(w, r) {
		return
	}
	var in bosonapi.Submission
	if err := decode(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	l, err := h.Directory.Submit(r.Context(), s.Snapshot().Username, in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, l)
}

func dirHost(r *http.Request) (string, error) {
	h := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("hostname")))
	if h == "" {
		return "", apperr.Invalid("hostname is required")
	}
	return h, nil
}

func (h *Handlers) directoryUpdate(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if !h.directoryOr503(w, r) {
		return
	}
	host, err := dirHost(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in bosonapi.Submission
	if err := decode(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	l, err := h.Directory.Update(r.Context(), s.Snapshot().Username, host, r.PathValue("id"), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (h *Handlers) directoryVerify(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if !h.directoryOr503(w, r) {
		return
	}
	host, err := dirHost(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	res, err := h.Directory.Verify(r.Context(), s.Snapshot().Username, host, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handlers) directoryRegenerate(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if !h.directoryOr503(w, r) {
		return
	}
	host, err := dirHost(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	l, err := h.Directory.Regenerate(r.Context(), s.Snapshot().Username, host, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (h *Handlers) directoryUnlist(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if !h.directoryOr503(w, r) {
		return
	}
	host, err := dirHost(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.Directory.Unlist(r.Context(), s.Snapshot().Username, host, r.PathValue("id")); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- the stack's own directory ----

func (h *Handlers) localDirectoryOr503(w http.ResponseWriter, r *http.Request) bool {
	if h.LocalDirectory == nil {
		writeError(w, r, apperr.New(http.StatusServiceUnavailable, "directory_local_off",
			"the stack's own directory isn't connected: DIRECTORY_LOCAL_URL is empty"))
		return false
	}
	return true
}

func (h *Handlers) directoryFederation(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if !h.localDirectoryOr503(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, h.fileFederation(h.LocalDirectory.Federation(r.Context())))
}

func (h *Handlers) directoryIncludeMain(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if !h.localDirectoryOr503(w, r) {
		return
	}
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decode(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	if in.Enabled == nil {
		writeError(w, r, apperr.Invalid("enabled (true or false) is required"))
		return
	}
	if h.Config.Enabled() {
		c := stackconfig.Change{Set: map[string]any{"directory.include_main": *in.Enabled}}
		if _, err := h.Config.Update(r.Context(), s.Snapshot().Username, r.Header.Get("If-Match"), c, h.can(r, s)); err != nil {
			writeError(w, r, configErr(err))
			return
		}
		h.directoryFederation(w, r, s)
		return
	}
	f, err := h.LocalDirectory.SetIncludeMain(r.Context(), s.Snapshot().Username, *in.Enabled)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// directorySettings changes the stack directory's runtime settings
// (name, operator, contact, sync interval): in the stack config file when
// there is one, else in the directory's database. Nobody edits .env.
func (h *Handlers) directorySettings(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if !h.localDirectoryOr503(w, r) {
		return
	}
	var in directory.SettingsUpdate
	if err := decode(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	if h.Config.Enabled() {
		c := directoryChange(in)
		if len(c.Set) == 0 {
			writeError(w, r, apperr.Invalid("nothing to update"))
			return
		}
		if _, err := h.Config.Update(r.Context(), s.Snapshot().Username, r.Header.Get("If-Match"), c, h.can(r, s)); err != nil {
			writeError(w, r, configErr(err))
			return
		}
		h.directoryFederation(w, r, s)
		return
	}
	f, err := h.LocalDirectory.UpdateSettings(r.Context(), s.Snapshot().Username, in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (h *Handlers) directoryPeerCheck(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if !h.localDirectoryOr503(w, r) {
		return
	}
	c, err := h.LocalDirectory.CheckPeer(r.Context(), r.URL.Query().Get("url"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *Handlers) directoryPeerAdd(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if !h.localDirectoryOr503(w, r) {
		return
	}
	var in struct {
		URL string `json:"url"`
	}
	if err := decode(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	f, err := h.LocalDirectory.AddPeer(r.Context(), s.Snapshot().Username, in.URL)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, f)
}

func (h *Handlers) directoryPeerUpdate(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if !h.localDirectoryOr503(w, r) {
		return
	}
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decode(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	if in.Enabled == nil {
		writeError(w, r, apperr.Invalid("enabled (true or false) is required"))
		return
	}
	f, err := h.LocalDirectory.SetPeerEnabled(r.Context(), s.Snapshot().Username, r.PathValue("id"), *in.Enabled)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (h *Handlers) directoryPeerRemove(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if !h.localDirectoryOr503(w, r) {
		return
	}
	f, err := h.LocalDirectory.RemovePeer(r.Context(), s.Snapshot().Username, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (h *Handlers) directoryPeerSync(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if !h.localDirectoryOr503(w, r) {
		return
	}
	f, res, err := h.LocalDirectory.SyncPeer(r.Context(), s.Snapshot().Username, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"federation": f, "result": res})
}

// userNetworks is the listings a user owns in the stack's own directory.
// Like userProfile it always answers 200 so the user page degrades.
func (h *Handlers) userNetworks(w http.ResponseWriter, r *http.Request, s *session.Session) {
	if h.LocalDirectory == nil {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false})
		return
	}
	n, err := h.LocalDirectory.OwnedNetworks(r.Context(), r.PathValue("id"))
	switch {
	case apperr.IsUnsupported(err):
		e, _ := apperr.As(err)
		writeJSON(w, http.StatusOK, map[string]any{"configured": true, "unsupported": e.Message})
	case err != nil:
		msg := "the stack's directory didn't answer"
		if e, ok := apperr.As(err); ok {
			msg = e.Message
		}
		writeJSON(w, http.StatusOK, map[string]any{"configured": true, "error": msg})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"configured": true, "networks": n})
	}
}
