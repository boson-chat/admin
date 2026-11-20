package stackconfig

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/boson-chat/admin/config"
	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/configfile"
	"github.com/boson-chat/admin/internal/services/admin"
)

func all(string) bool { return true }

func only(perms ...string) func(string) bool {
	return func(p string) bool {
		for _, x := range perms {
			if x == p {
				return true
			}
		}
		return false
	}
}

func newSvc(t *testing.T, content string) (*Service, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "boson.yaml")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store := configfile.Open(path, configfile.Options{Header: FileHeader})
	return New(Options{Store: store, Defaults: Defaults{
		DirectoryRegistration: true, DirectoryHostname: "irc.env.example",
		TLSCheckHosts: []string{"irc.env.example:6697"},
	}}), path
}

func TestCleanValidates(t *testing.T) {
	k, _ := KeyByName(KeyTLSCheckHosts)
	good := []any{"irc.example.org:6697", "voice.example.org:443@traefik:443", "10.0.0.1:443", "[::1]:8443"}
	if v, err := k.Clean(good); err != nil || len(v.([]string)) != 4 {
		t.Fatalf("good hosts: %v %v", v, err)
	}
	for name, bad := range map[string][]any{
		"no port":    {"irc.example.org"},
		"port 0":     {"irc.example.org:0"},
		"port 65536": {"irc.example.org:65536"},
		"bad dial":   {"irc.example.org:6697@nope"},
		"bad host":   {"irc_example!:6697"},
		"duplicate":  {"a.example.org:1", "A.example.org:1"},
		"not text":   {5},
	} {
		if _, err := k.Clean(bad); err == nil {
			t.Errorf("%s: accepted %v", name, bad)
		}
	}
	many := make([]any, 21)
	for i := range many {
		many[i] = fmt.Sprintf("h%d.example.org:443", i)
	}
	if _, err := k.Clean(many); err == nil || !strings.Contains(err.Error(), "at most 20") {
		t.Errorf("21 hosts: %v", err)
	}

	h, _ := KeyByName(KeyDirectoryHostname)
	if v, err := h.Clean(" IRC.Example.org. "); err != nil || v != "irc.example.org" {
		t.Errorf("hostname: %v %v", v, err)
	}
	for _, bad := range []any{"203.0.113.5", "localhost", "", "irc..example.org", true} {
		if _, err := h.Clean(bad); err == nil {
			t.Errorf("hostname %v accepted", bad)
		}
	}
	r, _ := KeyByName("voice.rooms")
	if _, err := r.Clean([]any{map[string]any{"id": "General", "name": "x"}}); err == nil {
		t.Error("uppercase room id accepted")
	}
	if _, err := r.Clean([]any{map[string]any{"id": "a", "name": "A"}, map[string]any{"id": "a", "name": "B"}}); err == nil {
		t.Error("duplicate room accepted")
	}
	p, _ := KeyByName("irc.roles_poll_seconds")
	if _, err := p.Clean(float64(70000)); err == nil {
		t.Error("70000 s accepted")
	}
	if _, err := p.Clean(1.5); err == nil {
		t.Error("fractional port accepted")
	}
	e, _ := KeyByName("auth.registration")
	if _, err := e.Clean("sometimes"); err == nil {
		t.Error("bad enum accepted")
	}
	d, _ := KeyByName("directory.sync_every")
	if _, err := d.Clean("1m"); err == nil {
		t.Error("1m sync accepted")
	}
}

func TestAdminValuesFollowTheFile(t *testing.T) {
	s, path := newSvc(t, "")
	if a := s.Admin(); !a.DirectoryRegistration || a.DirectoryHostname != "irc.env.example" || len(a.TLSTargets) != 1 {
		t.Fatalf("env defaults: %+v", a)
	}
	_, err := s.Update(context.Background(), "alice", "", Change{Set: map[string]any{
		KeyDirectoryRegistration: false,
		KeyDirectoryHostname:     "irc.file.example",
		KeyTLSCheckHosts:         []any{"a.file.example:443", "b.file.example:6697@ergo:6697"},
	}}, all)
	if err != nil {
		t.Fatal(err)
	}
	a := s.Admin()
	if a.DirectoryRegistration || a.DirectoryHostname != "irc.file.example" || len(a.TLSTargets) != 2 || a.TLSTargets[1].Address != "ergo:6697" {
		t.Fatalf("file values: %+v", a)
	}
	// Reset to default removes the key.
	if _, err := s.Update(context.Background(), "alice", "", Change{Unset: []string{KeyDirectoryHostname}}, all); err != nil {
		t.Fatal(err)
	}
	if s.Admin().DirectoryHostname != "irc.env.example" {
		t.Fatalf("reset: %+v", s.Admin())
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "directory_hostname") {
		t.Fatalf("reset left the key:\n%s", b)
	}
}

// A bad value written by hand keeps that key's last good value and is
// reported with its line; the other keys still apply.
func TestInvalidHandEditKeepsLastGood(t *testing.T) {
	s, path := newSvc(t, "version: 1\nadmin:\n  directory_hostname: irc.good.example\n")
	if s.Admin().DirectoryHostname != "irc.good.example" {
		t.Fatal("not loaded")
	}
	_ = os.WriteFile(path, []byte("version: 1\nadmin:\n  directory_hostname: 203.0.113.9\n  directory_registration: false\n"), 0o644)
	s.o.Store.Reload()
	a := s.Admin()
	if a.DirectoryHostname != "irc.good.example" || a.DirectoryRegistration {
		t.Fatalf("after bad edit: %+v", a)
	}
	own := s.Own()
	if own.OK || own.ErrorLine != 3 || !strings.Contains(string(own.Error), "directory_hostname") {
		t.Fatalf("own status: %+v %s", own, own.Error)
	}
	v := s.View(all)
	if len(v.Issues) != 1 || v.Issues[0].Key != KeyDirectoryHostname || v.Issues[0].Line != 3 {
		t.Fatalf("issues: %+v", v.Issues)
	}
}

func TestUpdatePermissionsAndErrors(t *testing.T) {
	s, path := newSvc(t, "version: 1\n")
	ctx := context.Background()
	dirOnly := only(admin.PermDirectoryManage)
	_, err := s.Update(ctx, "d", "", Change{Set: map[string]any{KeyTLSCheckHosts: []any{"a.example.org:1"}}}, dirOnly)
	var mp *MissingPermissionError
	if !errors.As(err, &mp) || mp.Perm != admin.PermServicesManage {
		t.Fatalf("tls hosts without services.manage: %v", err)
	}
	if _, err := s.Update(ctx, "d", "", Change{Unset: []string{"auth.registration"}}, dirOnly); !errors.As(err, &mp) {
		t.Fatalf("reset without permission: %v", err)
	}
	if _, err := s.Update(ctx, "d", "", Change{Set: map[string]any{KeyDirectoryRegistration: false}}, dirOnly); err != nil {
		t.Fatalf("directory.manage: %v", err)
	}
	if _, err := s.Update(ctx, "d", "", Change{Set: map[string]any{"made.up": 1}}, all); err == nil {
		t.Fatal("unknown key accepted")
	}
	if _, err := s.Update(ctx, "d", "", Change{}, all); err == nil {
		t.Fatal("empty change accepted")
	}
	_, err = s.Update(ctx, "d", `"old"`, Change{Set: map[string]any{"network.name": "X"}}, all)
	if e, ok := apperr.As(err); !ok || e.Status != http.StatusPreconditionFailed {
		t.Fatalf("stale If-Match: %v", err)
	}
	_ = os.WriteFile(path, []byte("version: 1\nnetwork: [\n"), 0o644)
	_, err = s.Update(ctx, "d", "", Change{Set: map[string]any{"network.name": "X"}}, all)
	if e, ok := apperr.As(err); !ok || e.Code != "config_file_invalid" || !strings.Contains(e.Message, "line") {
		t.Fatalf("broken file: %v", err)
	}
	v := s.View(all)
	if v.Error == nil || v.Error.Line == 0 {
		t.Fatalf("view error: %+v", v.Error)
	}
}

func TestViewMarksSourceAndEditable(t *testing.T) {
	s, _ := newSvc(t, "version: 1\nvoice:\n  rooms: [{id: general, name: General}]\n")
	v := s.View(only(admin.PermDirectoryManage))
	byKey := map[string]Value{}
	for _, k := range v.Keys {
		byKey[k.Key.Key] = k
	}
	if r := byKey["voice.rooms"]; !r.Set || r.Line != 3 || r.Editable {
		t.Fatalf("voice.rooms: %+v", r)
	}
	if h := byKey[KeyDirectoryHostname]; h.Set || h.Default != "irc.env.example" || !h.Editable {
		t.Fatalf("hostname: %+v", h)
	}
	if !v.Enabled || v.ETag == "" {
		t.Fatalf("view: %+v", v)
	}
}

func TestDisabledWithoutFile(t *testing.T) {
	s := New(Options{Defaults: Defaults{DirectoryRegistration: false, DirectoryHostname: "irc.x.example"}})
	if s.Enabled() || s.Admin().DirectoryHostname != "irc.x.example" {
		t.Fatal("defaults")
	}
	if _, err := s.Update(context.Background(), "a", "", Change{Set: map[string]any{"network.name": "x"}}, all); err == nil {
		t.Fatal("update without a file")
	}
	if v := s.View(all); v.Enabled || v.Keys[0].Editable {
		t.Fatal("view says editable")
	}
}

func TestStatuses(t *testing.T) {
	s, _ := newSvc(t, "version: 1\n")
	snap, _ := s.o.Store.Current()
	mod := snap.ModTime.UTC().Format(time.RFC3339Nano)
	old := snap.ModTime.Add(-time.Hour).UTC().Format(time.RFC3339)
	mux := http.NewServeMux()
	mux.HandleFunc("/applied", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"loaded_at":%q,"file_mtime":%q,"ok":true,"restart_required":[]}`, mod, mod)
	})
	mux.HandleFunc("/stale", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"file_mtime":%q,"ok":true}`, old)
	})
	mux.HandleFunc("/restart", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"file_mtime":%q,"ok":true,"restart_required":["irc.persistent_history"]}`, mod)
	})
	mux.HandleFunc("/bad", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"file_mtime":%q,"ok":false,"error":"bad room","error_line":4,"error_column":7}`, mod)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s.o.Targets = []config.Target{
		{Name: "auth", URL: srv.URL + "/applied"}, {Name: "directory", URL: srv.URL + "/stale"},
		{Name: "ergo", URL: srv.URL + "/restart"}, {Name: "quicvoiced", URL: srv.URL + "/bad"},
		{Name: "old", URL: srv.URL + "/missing"}, {Name: "gone", URL: "http://127.0.0.1:1/x"},
	}
	got := map[string]ServiceStatus{}
	for _, st := range s.Statuses(context.Background()) {
		got[st.Name] = st
	}
	want := map[string]string{"admin": StateApplied, "auth": StateApplied, "directory": StatePending, "ergo": StateRestartRequired,
		"quicvoiced": StateError, "old": StateUnsupported, "gone": StateUnreachable}
	for name, state := range want {
		if got[name].State != state {
			t.Errorf("%s: %s, want %s (%+v)", name, got[name].State, state, got[name])
		}
	}
	if got["quicvoiced"].Error != "line 4, column 7: bad room" {
		t.Errorf("error text %q", got["quicvoiced"].Error)
	}
	if got["ergo"].RestartRequired[0] != "irc.persistent_history" {
		t.Errorf("restart keys %v", got["ergo"].RestartRequired)
	}
	if s := got["quicvoiced"].Sections; len(s) != 2 || s[0] != "network" {
		t.Errorf("sections %v", s)
	}
}

func TestRoomsAcceptBareIDs(t *testing.T) {
	s, _ := newSvc(t, "version: 1\nvoice:\n  rooms: [general, {id: gaming, name: Gaming}]\n")
	v, ok := s.FileValue("voice.rooms")
	rs, _ := v.([]Room)
	if !ok || len(rs) != 2 || rs[0].Name != "general" || rs[1].Name != "Gaming" {
		t.Fatalf("rooms %v %v", v, ok)
	}
	k, _ := KeyByName("irc.history_expire")
	for _, good := range []any{"7d", "1w2d", "0"} {
		if _, err := k.Clean(good); err != nil {
			t.Errorf("%v: %v", good, err)
		}
	}
	if _, err := k.Clean("7 days"); err == nil {
		t.Error("bad duration accepted")
	}
}

func TestRoleChannelKeys(t *testing.T) {
	a, ok := KeyByName(KeyRoleChannelsAutojoin)
	if !ok || a.Kind != KindBool || a.Perm != admin.PermSettingsManage {
		t.Fatalf("autojoin key: %+v", a)
	}
	r, ok := KeyByName(KeyRoleChannelsRejoin)
	if !ok || r.Kind != KindEnum {
		t.Fatalf("rejoin key: %+v", r)
	}
	for _, v := range []string{RejoinLogin, RejoinOnce} {
		if got, err := r.Clean(v); err != nil || got != v {
			t.Errorf("%s: %v %v", v, got, err)
		}
	}
	if _, err := r.Clean("always"); err == nil {
		t.Error("rejoin took always")
	}
	if !slices.Contains(readsSections("ergo"), "roles") || !slices.Contains(readsSections("auth"), "roles") {
		t.Error("auth and ergo read roles:")
	}
}

func TestVoiceBitrateCaps(t *testing.T) {
	s, path := newSvc(t, "version: 1\nvoice:\n  rooms: [general, {id: music, name: Music, max_kbps: 128}]\n")
	v, _ := s.FileValue("voice.rooms")
	if rs, _ := v.([]Room); len(rs) != 2 || rs[0].MaxKbps != 0 || rs[1].MaxKbps != 128 {
		t.Fatalf("rooms %v", v)
	}
	_, err := s.Update(context.Background(), "alice", "", Change{Set: map[string]any{
		"voice.max_kbps": float64(64),
		"voice.rooms": []any{
			map[string]any{"id": "general", "name": "General"},
			map[string]any{"id": "music", "name": "Music", "max_kbps": float64(96)},
		},
	}}, all)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "max_kbps: 64") || !strings.Contains(string(b), "max_kbps: 96") || strings.Count(string(b), "max_kbps") != 2 {
		t.Fatalf("file:\n%s", b)
	}
	k, _ := KeyByName("voice.max_kbps")
	r, _ := KeyByName("voice.rooms")
	for _, good := range []float64{0, 6, 64, 510} {
		if _, err := k.Clean(good); err != nil {
			t.Errorf("max_kbps %v: %v", good, err)
		}
	}
	for _, bad := range []any{float64(3), float64(511), 64.5, "64"} {
		if _, err := k.Clean(bad); err == nil {
			t.Errorf("max_kbps %v accepted", bad)
		}
		if _, err := r.Clean([]any{map[string]any{"id": "a", "name": "A", "max_kbps": bad}}); err == nil {
			t.Errorf("room max_kbps %v accepted", bad)
		}
	}
}
