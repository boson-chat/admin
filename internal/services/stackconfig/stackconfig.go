// Package stackconfig is the admin panel's view of the stack's shared
// config file (config/boson.yaml, see internal/configfile): the settings
// pages read and write it through here, it holds the panel's own keys
// (admin.*), and it asks every service whether it applied the file
// (GET /internal/config/status).
//
// Each key is validated, permission-gated (Key.Perm), logged with the
// actor and counted (admin.settings.changes). A value in the file beats
// the env, which stays the default; removing a key resets it.
package stackconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/boson-chat/admin/config"
	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/configfile"
	"github.com/boson-chat/admin/internal/logger"
	"github.com/boson-chat/admin/metrics"
	"gopkg.in/yaml.v3"
)

// Defaults are the admin keys' env values.
type Defaults struct {
	DirectoryRegistration bool
	DirectoryHostname     string
	TLSCheckHosts         []string
}

// AdminValues are the panel's effective settings.
type AdminValues struct {
	DirectoryRegistration bool
	DirectoryHostname     string
	TLSCheckHosts         []string
	TLSTargets            []config.TLSTarget
	// Telemetry: admin.telemetry (default on; TELEMETRY=off in the env
	// turns the report off whatever the file says).
	Telemetry bool
}

type Options struct {
	// Store is nil without BOSON_CONFIG: the admin keys are the env's.
	Store    *configfile.Store
	Defaults Defaults
	// Targets: each service's /internal/config/status, named by compose service.
	Targets []config.Target
	Client  *http.Client
	Metrics *metrics.Metrics
	Now     func() time.Time
}

// Issue is a value in the file that isn't valid; its service keeps using
// the last good one.
type Issue struct {
	Key     string `json:"key"`
	Message string `json:"message"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
}

type Service struct {
	o Options

	checks []check

	mu       sync.RWMutex
	admin    AdminValues
	issues   []Issue // admin keys only (for /internal/config/status)
	loadedAt time.Time
}

func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 3 * time.Second}
	}
	if o.Metrics == nil {
		o.Metrics = metrics.Noop()
	}
	s := &Service{o: o}
	s.admin = s.defaults()
	if o.Store != nil {
		snap, _ := o.Store.Current()
		s.apply(snap)
		o.Store.OnChange(s.apply)
	} else {
		s.loadedAt = o.Now()
	}
	return s
}

func (s *Service) defaults() AdminValues {
	d := s.o.Defaults
	t, _ := config.ParseTLSTargets(d.TLSCheckHosts)
	return AdminValues{DirectoryRegistration: d.DirectoryRegistration, DirectoryHostname: d.DirectoryHostname,
		TLSCheckHosts: slices.Clone(d.TLSCheckHosts), TLSTargets: t, Telemetry: true}
}

// Enabled: BOSON_CONFIG is set.
func (s *Service) Enabled() bool { return s != nil && s.o.Store != nil }

// Admin is the panel's own effective settings.
func (s *Service) Admin() AdminValues {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.admin
}

// apply takes the admin keys from a new snapshot. An invalid value keeps
// the last good one for that key.
func (s *Service) apply(snap *configfile.Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, def := s.admin, s.defaults()
	var issues []Issue
	root := snap.Root()
	for _, name := range []string{KeyDirectoryRegistration, KeyDirectoryHostname, KeyTLSCheckHosts, KeyTelemetry} {
		k, _ := KeyByName(name)
		_, n := configfile.Lookup(root, configfile.Split(name))
		var v any
		if n != nil {
			var err error
			if v, err = k.FromNode(n); err != nil {
				issues = append(issues, Issue{Key: name, Message: err.Error(), Line: n.Line, Column: n.Column})
				continue // last good
			}
		}
		switch name {
		case KeyDirectoryRegistration:
			next.DirectoryRegistration = def.DirectoryRegistration
			if v != nil {
				next.DirectoryRegistration = v.(bool)
			}
		case KeyDirectoryHostname:
			next.DirectoryHostname = def.DirectoryHostname
			if v != nil {
				next.DirectoryHostname = v.(string)
			}
		case KeyTLSCheckHosts:
			next.TLSCheckHosts, next.TLSTargets = def.TLSCheckHosts, def.TLSTargets
			if v != nil {
				next.TLSCheckHosts = v.([]string)
				next.TLSTargets, _ = config.ParseTLSTargets(next.TLSCheckHosts)
			}
		case KeyTelemetry:
			next.Telemetry = def.Telemetry
			if v != nil {
				next.Telemetry = v.(bool)
			}
		}
	}
	s.admin, s.issues, s.loadedAt = next, issues, s.o.Now()
}

// ---- reading ----

// Value is one key as the panel shows it.
type Value struct {
	Key
	// Set: the file has the key (else the service uses its env / default).
	Set   bool `json:"set"`
	Value any  `json:"value,omitempty"`
	// Default: the env value, known for the panel's own keys only.
	Default  any  `json:"default,omitempty"`
	Line     int  `json:"line,omitempty"`
	Editable bool `json:"editable"`
}

// View is GET /api/config.
type View struct {
	Enabled bool `json:"enabled"`
	// Path is where the file is inside the panel's container.
	Path       string              `json:"path,omitempty"`
	ETag       string              `json:"etag,omitempty"`
	Exists     bool                `json:"exists"`
	ModifiedAt *time.Time          `json:"modified_at,omitempty"`
	LoadedAt   *time.Time          `json:"loaded_at,omitempty"`
	SavedBy    string              `json:"saved_by,omitempty"`
	SavedAt    *time.Time          `json:"saved_at,omitempty"`
	Error      *configfile.Problem `json:"error,omitempty"`
	Issues     []Issue             `json:"issues"`
	Keys       []Value             `json:"keys"`
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	t = t.UTC()
	return &t
}

func (s *Service) adminDefault(name string) any {
	d := s.o.Defaults
	switch name {
	case KeyDirectoryRegistration:
		return d.DirectoryRegistration
	case KeyDirectoryHostname:
		return d.DirectoryHostname
	case KeyTLSCheckHosts:
		if d.TLSCheckHosts == nil {
			return []string{}
		}
		return d.TLSCheckHosts
	case KeyTelemetry:
		return true
	}
	return nil
}

// View is the file's state and every schema key. can says whether the
// viewer holds a permission.
func (s *Service) View(can func(perm string) bool) *View {
	v := &View{Enabled: s.Enabled(), Issues: []Issue{}, Keys: make([]Value, 0, len(Keys))}
	var root *yaml.Node
	if s.Enabled() {
		snap, problem := s.o.Store.Current()
		_, tag := s.o.Store.Disk()
		v.Path, v.ETag, v.Exists, v.Error = s.o.Store.Path(), tag, snap.Exists || problem != nil, problem
		v.ModifiedAt, v.LoadedAt = timePtr(snap.ModTime), timePtr(snap.LoadedAt)
		v.SavedBy, v.SavedAt = snap.SavedBy, timePtr(snap.SavedAt)
		root = snap.Root()
	}
	for _, k := range Keys {
		val := Value{Key: k, Editable: v.Enabled && can(k.Perm)}
		if strings.HasPrefix(k.Key, "admin.") {
			val.Default = s.adminDefault(k.Key)
		}
		if kn, n := configfile.Lookup(root, configfile.Split(k.Key)); n != nil {
			val.Set, val.Line = true, kn.Line
			x, err := k.FromNode(n)
			if err != nil {
				v.Issues = append(v.Issues, Issue{Key: k.Key, Message: err.Error(), Line: n.Line, Column: n.Column})
			} else {
				val.Value = x
			}
		}
		v.Keys = append(v.Keys, val)
	}
	return v
}

// Raw is GET /api/config/raw: the file as it is on disk.
type Raw struct {
	Path    string              `json:"path"`
	Exists  bool                `json:"exists"`
	Content string              `json:"content"`
	ETag    string              `json:"etag"`
	Error   *configfile.Problem `json:"error,omitempty"`
}

func (s *Service) Raw() (*Raw, error) {
	if !s.Enabled() {
		return nil, errOff
	}
	s.o.Store.Reload()
	snap, problem := s.o.Store.Current()
	disk, tag := s.o.Store.Disk()
	return &Raw{Path: s.o.Store.Path(), Exists: snap.Exists || problem != nil, Content: string(disk), ETag: tag, Error: problem}, nil
}

// FileValue is a key's value in the file (nil, false when unset or invalid).
func (s *Service) FileValue(name string) (any, bool) {
	if !s.Enabled() {
		return nil, false
	}
	k, ok := KeyByName(name)
	if !ok {
		return nil, false
	}
	snap, _ := s.o.Store.Current()
	_, n := configfile.Lookup(snap.Root(), configfile.Split(name))
	if n == nil {
		return nil, false
	}
	v, err := k.FromNode(n)
	return v, err == nil
}

// ---- writing ----

// Lookup returns a key's value as it is (or would be) in the file: ok is
// false when the key is unset.
type Lookup func(key string) (v any, ok bool)

// CheckFunc validates a change as a whole, for keys that depend on each
// other. after is the file with the change applied, before the file as it
// is. A returned *apperr.Error goes to the client as it is.
type CheckFunc func(ctx context.Context, after, before Lookup) error

type check struct {
	prefix string
	fn     CheckFunc
}

// AddCheck runs fn on every change that touches a key starting with
// prefix. Call it while wiring, before serving.
func (s *Service) AddCheck(prefix string, fn CheckFunc) {
	s.checks = append(s.checks, check{prefix: prefix, fn: fn})
}

func (s *Service) runChecks(ctx context.Context, set map[string]any, unset []string) error {
	keys := Change{Unset: unset, Set: set}.Keys()
	before := func(name string) (any, bool) { return s.FileValue(name) }
	after := func(name string) (any, bool) {
		if v, ok := set[name]; ok {
			return jsonShape(v), true
		}
		if slices.Contains(unset, name) {
			return nil, false
		}
		return s.FileValue(name)
	}
	for _, c := range s.checks {
		if !slices.ContainsFunc(keys, func(k string) bool { return strings.HasPrefix(k, c.prefix) }) {
			continue
		}
		if err := c.fn(ctx, after, before); err != nil {
			return err
		}
	}
	return nil
}

var (
	errOff = apperr.New(http.StatusServiceUnavailable, "config_file_off",
		"no stack config file: set BOSON_CONFIG=/config/boson.yaml on the admin service and mount the stack's config/ directory (docs/config.md)")
	errNothing = apperr.Invalid("nothing to change")
)

// Change is PATCH /api/config: values to set and keys to remove (reset
// to the env default).
type Change struct {
	Set   map[string]any `json:"set"`
	Unset []string       `json:"unset"`
}

// Keys lists the keys a change touches, sorted.
func (c Change) Keys() []string {
	out := slices.Clone(c.Unset)
	for k := range c.Set {
		out = append(out, k)
	}
	sort.Strings(out)
	return slices.Compact(out)
}

// MissingPermissionError: the change touches a key the actor can't edit.
type MissingPermissionError struct{ Perm, Key string }

func (e *MissingPermissionError) Error() string { return e.Key + " needs " + e.Perm }

// Update validates and writes a change. can checks the actor's permissions
// per key; ifMatch (optional) is the ETag the change was made against.
func (s *Service) Update(ctx context.Context, actor, ifMatch string, c Change, can func(perm string) bool) (*View, error) {
	if !s.Enabled() {
		return nil, errOff
	}
	if len(c.Set) == 0 && len(c.Unset) == 0 {
		return nil, errNothing
	}
	set := map[string]any{}
	for name, raw := range c.Set {
		k, ok := KeyByName(name)
		if !ok {
			return nil, apperr.Invalid(name + " isn't a setting the panel knows (edit the file by hand for it)")
		}
		if !can(k.Perm) {
			return nil, &MissingPermissionError{Perm: k.Perm, Key: name}
		}
		if k.Kind == KindImages {
			return nil, apperr.Invalid(name + " is written by image uploads (PUT /api/branding/images/{slot})")
		}
		if k.Kind == KindEmailFile {
			return nil, apperr.Invalid(name + " is written by saving the email's MJML (PUT /api/emails/mjml/{part})")
		}
		v, err := k.Clean(raw)
		if err != nil {
			return nil, apperr.Invalid(err.Error())
		}
		set[name] = v
	}
	for _, name := range c.Unset {
		k, ok := KeyByName(name)
		if !ok {
			return nil, apperr.Invalid(name + " isn't a setting the panel knows")
		}
		if k.Kind == KindImages {
			return nil, apperr.Invalid(name + " is written by image uploads (DELETE /api/branding/images/{slot})")
		}
		if k.Kind == KindEmailFile {
			return nil, apperr.Invalid(name + " is removed with the email's MJML (DELETE /api/emails/mjml/{part})")
		}
		if _, dup := set[name]; dup {
			return nil, apperr.Invalid(name + " is both set and reset")
		}
		if !can(k.Perm) {
			return nil, &MissingPermissionError{Perm: k.Perm, Key: name}
		}
	}

	if err := s.runChecks(ctx, set, c.Unset); err != nil {
		return nil, err
	}

	err := s.Write(ctx, actor, ifMatch, "config.update", c.Keys(), func(root *yaml.Node) ([]string, error) {
		var changes []string
		names := make([]string, 0, len(set))
		for name := range set {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			old := describe(root, name)
			if err := setValue(root, name, set[name]); err != nil {
				return nil, apperr.Invalid(err.Error())
			}
			if now := describe(root, name); now != old {
				changes = append(changes, name+": "+old+" → "+now)
			}
		}
		for _, name := range c.Unset {
			old := describe(root, name)
			configfile.Unset(root, configfile.Split(name))
			if old != unsetWord {
				changes = append(changes, name+": "+old+" → "+unsetWord)
			}
		}
		return changes, nil
	})
	if err != nil {
		return nil, err
	}
	return s.View(can), nil
}

// Edit changes the parsed file and returns the changes it made, for the log.
type Edit func(root *yaml.Node) (changes []string, err error)

// Write applies edit to the file (atomically, If-Match guarded) with the
// same log line and metrics as Update. keys name the settings it touches;
// action names it in the log (config.update, branding.image.upload, …).
// Callers check permissions first.
func (s *Service) Write(ctx context.Context, actor, ifMatch, action string, keys []string, edit Edit) error {
	if !s.Enabled() {
		return errOff
	}
	var changes []string
	_, err := s.o.Store.Update(ifMatch, actor, func(root *yaml.Node) error {
		var err error
		changes, err = edit(root)
		return err
	})
	outcome := "success"
	switch {
	case errors.Is(err, configfile.ErrConflict):
		outcome, err = "conflict", apperr.New(http.StatusPreconditionFailed, "config_changed",
			"the config file changed since you loaded it (someone else saved, or it was edited by hand); reload to see the new values")
	case err != nil:
		var inv *configfile.InvalidError
		if errors.As(err, &inv) {
			outcome, err = "invalid_file", apperr.New(http.StatusConflict, "config_file_invalid",
				"the config file on disk has an error, so the panel won't overwrite it: "+inv.Problem.Error()+". Fix it by hand first.")
		} else if _, ok := apperr.As(err); ok {
			outcome = "invalid"
		} else {
			outcome = "error"
		}
	}
	for _, sec := range sections(keys) {
		s.o.Metrics.SettingsChange(ctx, sec, outcome)
	}
	ev := logger.FromCtx(ctx).Info()
	if err != nil {
		ev = logger.FromCtx(ctx).Warn().Err(err)
	}
	ev.Str("actor", actor).Str("action", action).Str("outcome", outcome).
		Strs("keys", keys).Strs("changes", changes).Str("file", s.o.Store.Path()).Msg("config file change")
	return err
}

// Snapshot is the file's last good state (nil without BOSON_CONFIG).
func (s *Service) Snapshot() *configfile.Snapshot {
	if !s.Enabled() {
		return nil
	}
	snap, _ := s.o.Store.Current()
	return snap
}

// Path is the config file's path ("" without BOSON_CONFIG).
func (s *Service) Path() string {
	if !s.Enabled() {
		return ""
	}
	return s.o.Store.Path()
}

const unsetWord = "(default)"

func describe(root *yaml.Node, name string) string {
	_, n := configfile.Lookup(root, configfile.Split(name))
	if n == nil {
		return unsetWord
	}
	var v any
	if n.Decode(&v) != nil {
		return "?"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func sections(keys []string) []string {
	var out []string
	for _, k := range keys {
		sec, _, _ := strings.Cut(k, ".")
		if !slices.Contains(out, sec) {
			out = append(out, sec)
		}
	}
	return out
}

// ---- status ----

// Status is a service's GET /internal/config/status (the stack contract).
type Status struct {
	Path            string          `json:"path,omitempty"`
	LoadedAt        *time.Time      `json:"loaded_at,omitempty"`
	FileMTime       *time.Time      `json:"file_mtime,omitempty"`
	OK              bool            `json:"ok"`
	Error           json.RawMessage `json:"error,omitempty"`
	ErrorLine       int             `json:"error_line,omitempty"`
	ErrorColumn     int             `json:"error_column,omitempty"`
	RestartRequired []string        `json:"restart_required"`
}

// Own is the panel's own status.
func (s *Service) Own() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := Status{LoadedAt: timePtr(s.loadedAt), OK: true, RestartRequired: []string{}}
	if s.Enabled() {
		snap, problem := s.o.Store.Current()
		st.Path, st.FileMTime = s.o.Store.Path(), timePtr(snap.ModTime)
		switch {
		case problem != nil:
			st.OK = false
			st.Error, _ = json.Marshal(problem.Message)
			st.ErrorLine, st.ErrorColumn = problem.Line, problem.Column
		case len(s.issues) > 0:
			st.OK = false
			msgs := make([]string, len(s.issues))
			for i, is := range s.issues {
				msgs[i] = is.Message
			}
			st.Error, _ = json.Marshal(strings.Join(msgs, "; "))
			st.ErrorLine, st.ErrorColumn = s.issues[0].Line, s.issues[0].Column
		}
	}
	return st
}

// Service states.
const (
	StateApplied         = "applied"
	StatePending         = "pending" // hasn't loaded the current file yet
	StateRestartRequired = "restart_required"
	StateError           = "error"
	StateUnreachable     = "unreachable"
	StateUnsupported     = "unsupported" // no /internal/config/status (an older release)
)

// ServiceStatus is one row of GET /api/config/status.
type ServiceStatus struct {
	Name            string     `json:"name"`
	State           string     `json:"state"`
	LoadedAt        *time.Time `json:"loaded_at,omitempty"`
	FileMTime       *time.Time `json:"file_mtime,omitempty"`
	Error           string     `json:"error,omitempty"`
	RestartRequired []string   `json:"restart_required"`
	// Sections it reads.
	Sections []string `json:"sections"`
}

// Statuses asks every service how it's doing with the current file.
func (s *Service) Statuses(ctx context.Context) []ServiceStatus {
	var fileMod time.Time
	if s.Enabled() {
		s.o.Store.Reload()
		snap, _ := s.o.Store.Current()
		fileMod = snap.ModTime
	}
	out := make([]ServiceStatus, len(s.o.Targets)+1)
	out[0] = s.judge(self, s.Own(), nil, fileMod)
	var wg sync.WaitGroup
	for i, t := range s.o.Targets {
		wg.Go(func() {
			st, err := s.fetch(ctx, t.URL)
			out[i+1] = s.judge(t.Name, st, err, fileMod)
		})
	}
	wg.Wait()
	return out
}

var errUnsupported = errors.New("unsupported")

func (s *Service) fetch(ctx context.Context, url string) (Status, error) {
	var st Status
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return st, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := s.o.Client.Do(req)
	if err != nil {
		return st, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusMethodNotAllowed {
		return st, errUnsupported
	}
	if res.StatusCode != http.StatusOK {
		return st, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&st); err != nil {
		return st, fmt.Errorf("not a config status: %w", err)
	}
	return st, nil
}

func errorText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var p configfile.Problem
	if json.Unmarshal(raw, &p) == nil && p.Message != "" {
		return p.Error()
	}
	return string(raw)
}

func (s *Service) judge(name string, st Status, err error, fileMod time.Time) ServiceStatus {
	out := ServiceStatus{Name: name, RestartRequired: []string{}, Sections: readsSections(name)}
	switch {
	case errors.Is(err, errUnsupported):
		out.State = StateUnsupported
		return out
	case err != nil:
		out.State, out.Error = StateUnreachable, err.Error()
		return out
	}
	out.LoadedAt, out.FileMTime, out.Error = st.LoadedAt, st.FileMTime, errorText(st.Error)
	if out.Error != "" && st.ErrorLine > 0 && !strings.HasPrefix(out.Error, "line ") {
		p := configfile.Problem{Message: out.Error, Line: st.ErrorLine, Column: st.ErrorColumn}
		out.Error = p.Error()
	}
	if st.RestartRequired != nil {
		out.RestartRequired = st.RestartRequired
	}
	switch {
	case !st.OK:
		out.State = StateError
	case len(out.RestartRequired) > 0:
		out.State = StateRestartRequired
	case !fileMod.IsZero() && (st.FileMTime == nil || st.FileMTime.Before(fileMod.Truncate(time.Second))):
		out.State = StatePending
	default:
		out.State = StateApplied
	}
	return out
}

// readsSections: which sections of the file a service reads.
func readsSections(service string) []string {
	var out []string
	for _, k := range Keys {
		if slices.Contains(k.Services, service) || slices.Contains(k.Services, everybody) {
			sec, _, _ := strings.Cut(k.Key, ".")
			if !slices.Contains(out, sec) {
				out = append(out, sec)
			}
		}
	}
	return out
}

// FileHeader starts a config file the panel creates (the stack ships a
// fully commented config/boson.example.yaml; this is the fallback).
const FileHeader = `# boson stack config (schema v1). Docs: docs/config.md in the stack repo.
#
# Non-secret runtime settings for every service; each reads its own
# section. A value here beats the service's env var, which stays the
# default; delete a key to go back to the default. Services pick up
# changes within seconds. The admin panel edits this file and keeps
# comments and keys it doesn't know. Secrets stay in .env.`
