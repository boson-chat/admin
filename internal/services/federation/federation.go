// Package federation is the Federation page: this server's rules as a
// remote (who may sign in here with an account from their home Boson
// server, and what remote users get), the remote users who did, banning
// them or blocking their whole home domain, and this server as a home
// (whether its boson.json publishes the federation block).
//
// The settings are auth.federation.* keys of the stack config file when
// there is one (saved through stackconfig, which checks and logs them);
// without it auth keeps them in its database (PATCH /v1/admin/federation),
// and when auth can't change them either they come from its env and the
// page shows them read-only. Everything about remote users is auth's
// federation admin API (auth docs/federation.md), which audits it.
package federation

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/logger"
	"github.com/boson-chat/admin/internal/services/admin"
	"github.com/boson-chat/admin/internal/services/stackconfig"
	"github.com/boson-chat/admin/internal/session"
)

// DefaultDirectoryURL is the directory "directory" mode trusts by default.
const DefaultDirectoryURL = "https://directory.boson.chat"

// Defaults are auth's built-in values (the federation spec), shown when
// auth can't be asked.
var Defaults = authapi.FederationSettings{
	Mode: authapi.FederationDirectory, Allow: []string{}, Block: []string{},
	DirectoryURL: DefaultDirectoryURL, RemoteVoice: true,
}

// Field is one setting: its JSON name, config key and env var.
type Field struct {
	Name string `json:"name"`
	Key  string `json:"key"`
	Env  string `json:"env"`
}

// Fields in the order the page shows them.
var Fields = []Field{
	{"mode", stackconfig.KeyFederationMode, "FEDERATION_MODE"},
	{"allow", stackconfig.KeyFederationAllow, "FEDERATION_ALLOW"},
	{"block", stackconfig.KeyFederationBlock, "FEDERATION_BLOCK"},
	{"directory_url", stackconfig.KeyFederationDirectory, "FEDERATION_DIRECTORY_URL"},
	{"remote_upload_quota", stackconfig.KeyFederationUploadQuota, "FEDERATION_REMOTE_UPLOAD_QUOTA"},
	{"remote_voice", stackconfig.KeyFederationRemoteVoice, "FEDERATION_REMOTE_VOICE"},
}

func fieldByName(name string) (Field, bool) {
	i := slices.IndexFunc(Fields, func(f Field) bool { return f.Name == name })
	if i < 0 {
		return Field{}, false
	}
	return Fields[i], true
}

// Where changes are saved.
const (
	StoreFile = "file" // the stack config file
	StoreAuth = "auth" // auth's database (PATCH /v1/admin/federation)
	StoreEnv  = "env"  // nowhere: auth's env, read-only here
)

// Tokens runs fn with the signed-in admin's access token (admin.AdminServiceImpl).
type Tokens interface {
	WithToken(ctx context.Context, s *session.Session, fn func(accessToken string) error) error
}

type Options struct {
	// AuthVersion reads the running auth release (nil or "" = unknown);
	// cached for a few minutes here.
	AuthVersion func(ctx context.Context) (string, error)
	Client      authapi.FederationClient
	Tokens      Tokens
	// Config is the stack config file (never nil; disabled without BOSON_CONFIG).
	Config *stackconfig.Service
	// Home checks this server's boson.json (nil = not checked).
	Home *HomeChecker
}

type ServiceImpl interface {
	View(ctx context.Context, s *session.Session, can func(string) bool, fresh bool) *View
	Update(ctx context.Context, s *session.Session, in Patch, can func(string) bool) (*View, error)
	Users(ctx context.Context, s *session.Session, q authapi.RemoteUserQuery) (*authapi.RemoteUserPage, error)
	Ban(ctx context.Context, s *session.Session, id, reason string) (*authapi.RemoteUser, error)
	Unban(ctx context.Context, s *session.Session, id string) (*authapi.RemoteUser, error)
	BlockDomain(ctx context.Context, s *session.Session, domain string, can func(string) bool) (*BlockResult, error)
	UnblockDomain(ctx context.Context, s *session.Session, domain string, can func(string) bool) (*View, error)
	// Check is a stackconfig.CheckFunc for the auth.federation section.
	Check(ctx context.Context, after, before stackconfig.Lookup) error
}

type Service struct {
	o  Options
	mu sync.Mutex

	verMu sync.Mutex
	ver   string
	verAt time.Time
}

// RequiresBlockDisconnect is the auth release whose domain blocks also drop
// the domain's users from IRC (auth #30); before it they stayed connected
// until they disconnected.
const RequiresBlockDisconnect = "1.26.0"

func (s *Service) authVersion(ctx context.Context) string {
	if s.o.AuthVersion == nil {
		return ""
	}
	s.verMu.Lock()
	defer s.verMu.Unlock()
	if !s.verAt.IsZero() && time.Since(s.verAt) < 5*time.Minute {
		return s.ver
	}
	v, err := s.o.AuthVersion(ctx)
	if err != nil {
		v = ""
	}
	s.ver, s.verAt = strings.TrimPrefix(strings.TrimSpace(v), "v"), time.Now()
	return s.ver
}

// olderThan: is version a release before min? Unparsable (dev builds) = no.
func olderThan(version, min string) bool {
	parse := func(v string) ([3]int, bool) {
		var out [3]int
		v, _, _ = strings.Cut(v, "-")
		v, _, _ = strings.Cut(v, "+")
		parts := strings.Split(v, ".")
		if len(parts) != 3 {
			return out, false
		}
		for i, p := range parts {
			n, err := strconv.Atoi(p)
			if err != nil {
				return out, false
			}
			out[i] = n
		}
		return out, true
	}
	a, ok1 := parse(version)
	b, ok2 := parse(min)
	if !ok1 || !ok2 {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func New(o Options) *Service { return &Service{o: o} }

// View is GET /api/federation.
type View struct {
	// Supported: auth has the federation admin API. Without it the
	// settings still save to the config file, but there are no remote
	// users and the effective values are the file's or the defaults.
	Supported bool   `json:"supported"`
	Requires  string `json:"requires,omitempty"`
	Error     string `json:"error,omitempty"`

	Settings authapi.FederationSettings `json:"settings"`
	// Sources per field: file, database, env or default.
	Sources map[string]string `json:"sources"`
	// Store: where a save goes (file, auth or env = read-only).
	Store string `json:"store"`
	// Editable: the viewer may save (settings.manage and a store).
	Editable bool                       `json:"editable"`
	Fields   []Field                    `json:"fields"`
	Defaults authapi.FederationSettings `json:"defaults"`

	Issuer      string `json:"issuer,omitempty"`
	Domain      string `json:"domain,omitempty"`
	RemoteUsers int    `json:"remote_users"`
	// RemoteRole is the role new remote users get ("" = none).
	RemoteRole string `json:"remote_role,omitempty"`
	// UpdatedAt / UpdatedBy: auth's last stored change (database store).
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	UpdatedBy string     `json:"updated_by,omitempty"`
	// AuthUsesFile: auth reads the stack config file but the panel doesn't
	// have it (BOSON_CONFIG unset here), so neither can save.
	AuthUsesFile bool `json:"auth_uses_file,omitempty"`
	// AuthVersion is the running auth release when the panel can read it
	// ("" = unknown). BlockKeepsIRC: it's older than RequiresBlockDisconnect,
	// so a domain block doesn't drop users already on IRC.
	AuthVersion   string `json:"auth_version,omitempty"`
	BlockKeepsIRC bool   `json:"block_keeps_irc,omitempty"`

	Home *Home `json:"home,omitempty"`
}

func (s *Service) status(ctx context.Context, sess *session.Session) (*authapi.FederationStatus, error) {
	var st *authapi.FederationStatus
	err := s.o.Tokens.WithToken(ctx, sess, func(at string) (err error) { st, err = s.o.Client.FederationStatus(ctx, at); return })
	return st, err
}

func (s *Service) View(ctx context.Context, sess *session.Session, can func(string) bool, fresh bool) *View {
	v := &View{Sources: map[string]string{}, Fields: Fields, Defaults: Defaults, Settings: Defaults}
	v.Settings.Allow, v.Settings.Block = []string{}, []string{}
	st, err := s.status(ctx, sess)
	switch {
	case err == nil:
		v.Supported = true
		v.Settings = st.FederationSettings
		v.Issuer, v.Domain, v.RemoteUsers, v.RemoteRole, v.UpdatedAt = st.Issuer, st.Domain, st.RemoteUsers, st.RemoteRole, st.UpdatedAt
		if st.UpdatedBy != nil {
			v.UpdatedBy = *st.UpdatedBy
		}
		for _, f := range Fields {
			v.Sources[f.Name] = "default"
			if src := st.Sources[f.Name]; src != "" {
				v.Sources[f.Name] = src
			}
		}
	case apperr.IsUnsupported(err):
		v.Requires = authapi.RequiresFederation
	default:
		v.Supported = true // it's there, it failed
		v.Error = err.Error()
	}
	if v.Sources["mode"] == "" {
		for _, f := range Fields {
			v.Sources[f.Name] = "default"
		}
	}
	// The file wins (auth may not have reloaded a save yet).
	if s.o.Config.Enabled() {
		for _, f := range Fields {
			if x, ok := s.o.Config.FileValue(f.Key); ok {
				setField(&v.Settings, f.Name, x)
				v.Sources[f.Name] = "file"
			}
		}
	}
	if v.Settings.Allow == nil {
		v.Settings.Allow = []string{}
	}
	if v.Settings.Block == nil {
		v.Settings.Block = []string{}
	}
	switch {
	case s.o.Config.Enabled():
		v.Store = StoreFile
	case st != nil && st.Editable:
		v.Store = StoreAuth
	default:
		v.Store = StoreEnv
	}
	if v.Store == StoreEnv && st != nil {
		for _, src := range st.Sources {
			if src == "file" {
				v.AuthUsesFile = true
			}
		}
	}
	v.Editable = v.Store != StoreEnv && can(admin.PermSettingsManage)
	v.AuthVersion = s.authVersion(ctx)
	v.BlockKeepsIRC = v.AuthVersion != "" && olderThan(v.AuthVersion, RequiresBlockDisconnect)
	if s.o.Home != nil {
		v.Home = s.o.Home.Check(ctx, v.Domain, v.Issuer, v.Settings.Mode, fresh)
		if v.Domain == "" {
			v.Domain = v.Home.Domain
		}
	}
	return v
}

func setField(st *authapi.FederationSettings, name string, x any) {
	switch name {
	case "mode":
		st.Mode, _ = x.(string)
	case "allow":
		st.Allow = strList(x)
	case "block":
		st.Block = strList(x)
	case "directory_url":
		st.DirectoryURL, _ = x.(string)
	case "remote_upload_quota":
		st.RemoteUploadQuota, _ = x.(string)
	case "remote_voice":
		st.RemoteVoice, _ = x.(bool)
	}
}

func strList(x any) []string {
	switch l := x.(type) {
	case []string:
		return slices.Clone(l)
	case []any:
		out := make([]string, 0, len(l))
		for _, it := range l {
			if s, ok := it.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return []string{}
}

// Patch is PATCH /api/federation: fields to set, and (with the config
// file) fields to reset to auth's env default.
type Patch struct {
	authapi.FederationPatch
	Unset []string `json:"unset,omitempty"`
}

// cleaned validates a patch like the config file does, so the auth store
// gets the same values.
func cleaned(p authapi.FederationPatch) (authapi.FederationPatch, error) {
	clean := func(f Field, v any) (any, error) {
		k, _ := stackconfig.KeyByName(f.Key)
		out, err := k.Clean(v)
		if err != nil {
			return nil, apperr.InvalidField(f.Name, err.Error())
		}
		return out, nil
	}
	f := func(n string) Field { x, _ := fieldByName(n); return x }
	if p.Mode != nil {
		v, err := clean(f("mode"), *p.Mode)
		if err != nil {
			return p, err
		}
		s := v.(string)
		p.Mode = &s
	}
	for _, l := range []struct {
		name string
		v    **[]string
	}{{"allow", &p.Allow}, {"block", &p.Block}} {
		if *l.v == nil {
			continue
		}
		v, err := clean(f(l.name), **l.v)
		if err != nil {
			return p, err
		}
		s := v.([]string)
		*l.v = &s
	}
	if p.DirectoryURL != nil {
		v, err := clean(f("directory_url"), *p.DirectoryURL)
		if err != nil {
			return p, err
		}
		s := v.(string)
		p.DirectoryURL = &s
	}
	if p.RemoteUploadQuota != nil && strings.TrimSpace(*p.RemoteUploadQuota) != "" {
		v, err := clean(f("remote_upload_quota"), *p.RemoteUploadQuota)
		if err != nil {
			return p, err
		}
		s := v.(string)
		p.RemoteUploadQuota = &s
	}
	return p, nil
}

// consistent checks the settings as a whole.
func consistent(st authapi.FederationSettings) error {
	if st.Mode == authapi.FederationAllowlist && len(st.Allow) == 0 {
		return apperr.InvalidField("allow", "allowlist mode needs at least one home domain in the allowlist (or pick another mode)")
	}
	for _, d := range st.Allow {
		if slices.Contains(st.Block, d) {
			return apperr.InvalidField("block", d+" is on both the allowlist and the blocklist: take it off one")
		}
	}
	return nil
}

func apply(st authapi.FederationSettings, p authapi.FederationPatch) authapi.FederationSettings {
	if p.Mode != nil {
		st.Mode = *p.Mode
	}
	if p.Allow != nil {
		st.Allow = *p.Allow
	}
	if p.Block != nil {
		st.Block = *p.Block
	}
	if p.DirectoryURL != nil {
		st.DirectoryURL = *p.DirectoryURL
	}
	if p.RemoteUploadQuota != nil {
		st.RemoteUploadQuota = *p.RemoteUploadQuota
	}
	if p.RemoteVoice != nil {
		st.RemoteVoice = *p.RemoteVoice
	}
	return st
}

// Check (stackconfig.CheckFunc) runs on every config change under
// auth.federation, from this page or the Stack config page.
// Keys that aren't in the file come from auth's env, which the panel
// doesn't know, so only what the file says is checked.
func (s *Service) Check(ctx context.Context, after, before stackconfig.Lookup) error {
	mode, modeOK := after(stackconfig.KeyFederationMode)
	allow, allowOK := after(stackconfig.KeyFederationAllow)
	block, blockOK := after(stackconfig.KeyFederationBlock)
	st := authapi.FederationSettings{}
	if modeOK && allowOK {
		st.Mode, _ = mode.(string)
	}
	if allowOK {
		st.Allow = strList(allow)
	}
	if blockOK {
		st.Block = strList(block)
	}
	return consistent(st)
}

func (s *Service) Update(ctx context.Context, sess *session.Session, in Patch, can func(string) bool) (*View, error) {
	if in.Empty() && len(in.Unset) == 0 {
		return nil, apperr.Invalid("nothing to update")
	}
	if !can(admin.PermSettingsManage) {
		return nil, admin.MissingPermission(admin.PermSettingsManage)
	}
	p, err := cleaned(in.FederationPatch)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.View(ctx, sess, can, false)
	if err := consistent(apply(before.Settings, p)); err != nil {
		return nil, err
	}
	switch before.Store {
	case StoreFile:
		c := stackconfig.Change{Set: map[string]any{}}
		for _, f := range Fields {
			if v, ok := patchValue(p, f.Name); ok {
				if f.Name == "remote_upload_quota" && v == "" {
					c.Unset = append(c.Unset, f.Key)
					continue
				}
				c.Set[f.Key] = v
			}
		}
		for _, n := range in.Unset {
			f, ok := fieldByName(n)
			if !ok {
				return nil, apperr.Invalid(n + " isn't a federation setting")
			}
			if _, dup := c.Set[f.Key]; !dup && before.Sources[n] == "file" {
				c.Unset = append(c.Unset, f.Key)
			}
		}
		if len(c.Set) == 0 && len(c.Unset) == 0 {
			return before, nil
		}
		if _, err := s.o.Config.Update(ctx, sess.Snapshot().Username, "", c, can); err != nil {
			return nil, err
		}
	case StoreAuth:
		for _, n := range in.Unset {
			if _, ok := fieldByName(n); !ok {
				return nil, apperr.Invalid(n + " isn't a federation setting")
			}
			if _, set := patchValue(p, n); !set {
				p.Reset = append(p.Reset, n)
			}
		}
		err := s.o.Tokens.WithToken(ctx, sess, func(at string) error { _, err := s.o.Client.UpdateFederation(ctx, at, p); return err })
		ch := changes(before.Settings, apply(before.Settings, p))
		for _, n := range p.Reset {
			ch = append(ch, n+": reset to the env value")
		}
		logChange(ctx, sess, "federation.update", err, ch)
		if e, ok := apperr.As(err); ok && e.Status == http.StatusConflict && e.Code == "settings_managed" {
			return nil, errManaged
		}
		if err != nil {
			return nil, err
		}
	default:
		return nil, errReadOnly
	}
	return s.View(ctx, sess, can, false), nil
}

// errManaged: auth switched to the stack config file after the page loaded.
var errManaged = apperr.New(http.StatusConflict, "settings_managed",
	"auth now reads its federation settings from the stack config file: set BOSON_CONFIG on the admin service (the same config/boson.yaml) to change them here, or edit the file's auth.federation section")

var errReadOnly = apperr.New(http.StatusConflict, "federation_read_only",
	"federation settings come from the auth service's env here: set FEDERATION_* in the stack's .env and recreate auth, or set BOSON_CONFIG to change them from the panel")

func patchValue(p authapi.FederationPatch, name string) (any, bool) {
	switch name {
	case "mode":
		if p.Mode != nil {
			return *p.Mode, true
		}
	case "allow":
		if p.Allow != nil {
			return *p.Allow, true
		}
	case "block":
		if p.Block != nil {
			return *p.Block, true
		}
	case "directory_url":
		if p.DirectoryURL != nil {
			return *p.DirectoryURL, true
		}
	case "remote_upload_quota":
		if p.RemoteUploadQuota != nil {
			return strings.TrimSpace(*p.RemoteUploadQuota), true
		}
	case "remote_voice":
		if p.RemoteVoice != nil {
			return *p.RemoteVoice, true
		}
	}
	return nil, false
}

func changes(a, b authapi.FederationSettings) []string {
	var out []string
	str := func(name, x, y string) {
		if x != y {
			out = append(out, name+": "+x+" → "+y)
		}
	}
	list := func(name string, x, y []string) {
		for _, d := range y {
			if !slices.Contains(x, d) {
				out = append(out, name+": +"+d)
			}
		}
		for _, d := range x {
			if !slices.Contains(y, d) {
				out = append(out, name+": -"+d)
			}
		}
	}
	str("mode", a.Mode, b.Mode)
	list("allow", a.Allow, b.Allow)
	list("block", a.Block, b.Block)
	str("directory_url", a.DirectoryURL, b.DirectoryURL)
	str("remote_upload_quota", a.RemoteUploadQuota, b.RemoteUploadQuota)
	if a.RemoteVoice != b.RemoteVoice {
		out = append(out, "remote_voice: "+onOff(a.RemoteVoice)+" → "+onOff(b.RemoteVoice))
	}
	return out
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// logChange is the panel's own record of a federation action (auth audits
// them too; config file saves are logged by stackconfig).
func logChange(ctx context.Context, sess *session.Session, action string, err error, changes []string, kv ...string) {
	ev := logger.FromCtx(ctx).Info()
	outcome := "success"
	if err != nil {
		ev, outcome = logger.FromCtx(ctx).Warn().Err(err), "error"
	}
	for i := 0; i+1 < len(kv); i += 2 {
		ev = ev.Str(kv[i], kv[i+1])
	}
	ev.Str("actor", sess.Snapshot().Username).Str("action", action).Str("outcome", outcome).
		Strs("changes", changes).Msg("federation change")
}

// ---- remote users ----

func (s *Service) Users(ctx context.Context, sess *session.Session, q authapi.RemoteUserQuery) (*authapi.RemoteUserPage, error) {
	q.Q, q.Domain = strings.TrimSpace(q.Q), strings.ToLower(strings.TrimSpace(q.Domain))
	if q.Limit <= 0 || q.Limit > 100 {
		q.Limit = 50
	}
	var page *authapi.RemoteUserPage
	err := s.o.Tokens.WithToken(ctx, sess, func(at string) (err error) { page, err = s.o.Client.RemoteUsers(ctx, at, q); return })
	if err != nil {
		return nil, err
	}
	// With the config file, the file's block list is what the panel saved
	// and what auth applies once it re-reads it (within seconds). Until then
	// auth still reports the old state: show the saved one, marked applying.
	if s.o.Config.Enabled() {
		var block []string
		if x, ok := s.o.Config.FileValue(stackconfig.KeyFederationBlock); ok {
			block = strList(x)
		} else if st, err := s.status(ctx, sess); err == nil {
			block = st.Block // from auth's env
		}
		for i := range page.Users {
			u := &page.Users[i]
			saved := slices.ContainsFunc(block, func(b string) bool { return stackconfig.FederationDomainMatches(b, u.HomeDomain) })
			if saved != u.DomainBlocked {
				u.DomainBlocked, u.Applying = saved, true
			}
		}
	}
	return page, nil
}

func (s *Service) Ban(ctx context.Context, sess *session.Session, id, reason string) (*authapi.RemoteUser, error) {
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) > 500 || strings.ContainsAny(reason, "\r\n\x00") {
		return nil, apperr.InvalidField("reason", "the reason is at most 500 characters on one line")
	}
	var u *authapi.RemoteUser
	err := s.o.Tokens.WithToken(ctx, sess, func(at string) (err error) { u, err = s.o.Client.BanRemoteUser(ctx, at, id, reason); return })
	who := id
	if u != nil {
		who = u.Identity
	}
	logChange(ctx, sess, "federation.ban", err, []string{"banned " + who}, "user_id", id)
	return u, err
}

func (s *Service) Unban(ctx context.Context, sess *session.Session, id string) (*authapi.RemoteUser, error) {
	var u *authapi.RemoteUser
	err := s.o.Tokens.WithToken(ctx, sess, func(at string) (err error) { u, err = s.o.Client.UnbanRemoteUser(ctx, at, id); return })
	who := id
	if u != nil {
		who = u.Identity
	}
	logChange(ctx, sess, "federation.unban", err, []string{"unbanned " + who}, "user_id", id)
	return u, err
}

// BlockResult is POST /api/federation/blocked-domains.
type BlockResult struct {
	Domain string `json:"domain"`
	// Revoked sessions; RevokeError says why they couldn't be ended (the
	// block is saved either way, and auth refuses the domain's sign-ins).
	Revoked     int    `json:"revoked"`
	RevokeError string `json:"revoke_error,omitempty"`
	View        *View  `json:"view"`
}

// BlockDomain adds a home domain to the blocklist (taking it off the
// allowlist) and ends its users' sessions here.
func (s *Service) BlockDomain(ctx context.Context, sess *session.Session, domain string, can func(string) bool) (*BlockResult, error) {
	d, err := stackconfig.FederationDomain(domain)
	if err != nil {
		return nil, apperr.InvalidField("domain", err.Error())
	}
	cur := s.View(ctx, sess, can, false)
	if cur.Domain != "" && stackconfig.FederationDomainMatches(d, cur.Domain) {
		return nil, apperr.InvalidField("domain", d+" is this server's own domain")
	}
	p := Patch{}
	if !slices.Contains(cur.Settings.Block, d) {
		block := append(slices.Clone(cur.Settings.Block), d)
		p.Block = &block
	}
	if slices.Contains(cur.Settings.Allow, d) {
		allow := slices.DeleteFunc(slices.Clone(cur.Settings.Allow), func(x string) bool { return x == d })
		p.Allow = &allow
		if len(allow) == 0 && cur.Settings.Mode == authapi.FederationAllowlist {
			return nil, apperr.InvalidField("domain", d+" is the only domain on the allowlist: switch the mode first, or federation would have nobody to allow")
		}
	}
	v := cur
	if !p.Empty() {
		if v, err = s.Update(ctx, sess, p, can); err != nil {
			return nil, err
		}
	}
	res := &BlockResult{Domain: d, View: v}
	var rv *authapi.RevokedSessions
	err = s.o.Tokens.WithToken(ctx, sess, func(at string) (err error) { rv, err = s.o.Client.RevokeDomainSessions(ctx, at, d); return })
	if err != nil {
		res.RevokeError = err.Error()
	} else {
		res.Revoked = rv.Revoked
	}
	logChange(ctx, sess, "federation.block_domain", err, []string{"blocked " + d}, "domain", d)
	return res, nil
}

func (s *Service) UnblockDomain(ctx context.Context, sess *session.Session, domain string, can func(string) bool) (*View, error) {
	d := strings.ToLower(strings.TrimSpace(domain))
	cur := s.View(ctx, sess, can, false)
	if !slices.Contains(cur.Settings.Block, d) {
		return cur, nil
	}
	block := slices.DeleteFunc(slices.Clone(cur.Settings.Block), func(x string) bool { return x == d })
	return s.Update(ctx, sess, Patch{FederationPatch: authapi.FederationPatch{Block: &block}}, can)
}

// Unsupported is a FederationClient for an auth without the federation
// API (and for wiring without an auth client).
type Unsupported struct{}

var errUnsupported = apperr.Unsupported("Federation needs a newer auth service (auth ≥ " + authapi.RequiresFederation + ")")

func (Unsupported) FederationStatus(context.Context, string) (*authapi.FederationStatus, error) {
	return nil, errUnsupported
}
func (Unsupported) UpdateFederation(context.Context, string, authapi.FederationPatch) (*authapi.FederationStatus, error) {
	return nil, errUnsupported
}
func (Unsupported) RemoteUsers(context.Context, string, authapi.RemoteUserQuery) (*authapi.RemoteUserPage, error) {
	return nil, errUnsupported
}
func (Unsupported) BanRemoteUser(context.Context, string, string, string) (*authapi.RemoteUser, error) {
	return nil, errUnsupported
}
func (Unsupported) UnbanRemoteUser(context.Context, string, string) (*authapi.RemoteUser, error) {
	return nil, errUnsupported
}
func (Unsupported) RevokeDomainSessions(context.Context, string, string) (*authapi.RevokedSessions, error) {
	return nil, errUnsupported
}
