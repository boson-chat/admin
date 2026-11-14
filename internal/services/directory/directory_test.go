package directory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/bosonapi"
)

// fakeV1 is the boson directory's /v1 API as the panel assumes it
// (github.com/boson-chat/directory): networks keyed by the sha of the
// management key, the public lookup, the owner API and the operator API.
type fakeV1 struct {
	mu       sync.Mutex
	nets     map[string]map[string]any // id -> wire network
	keys     map[string]string         // id -> management key
	token    map[string]string         // id -> verification token
	dnsOK    bool
	peers    []map[string]any
	acting   []string
	internal string
	// settings: the 1.4.0 settings API; nil = an older directory (404).
	settings map[string]any
	isMain   bool
	patches  []map[string]any
	// theme API (branding contract, section 3); noTheme = an older directory.
	noTheme   bool
	themes    map[string]map[string]any
	themePuts int
	imported  map[string]bool // id -> not_local
}

func newFakeV1(t *testing.T) (*fakeV1, *httptest.Server) {
	f := &fakeV1{nets: map[string]map[string]any{}, keys: map[string]string{}, token: map[string]string{}, internal: "itok",
		peers: []map[string]any{
			{"id": "peer_main", "url": "https://directory.boson.chat", "name": "boson", "enabled": true, "builtin": true, "listing_count": 42},
			{"id": "peer_2", "url": "https://dir.example.net", "enabled": true, "builtin": false, "listing_count": 3, "last_error": "timeout"},
		}}
	mux := http.NewServeMux()
	jsonErr := func(w http.ResponseWriter, code int, e string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`{"error":"` + e + `"}`))
	}
	key := func(r *http.Request) string {
		k, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Network ")
		return k
	}
	listing := func(id string) map[string]any {
		out := map[string]any{"network": f.nets[id]}
		if f.nets[id]["status"] != "verified" {
			out["verification"] = map[string]any{"record_name": "_boson." + f.nets[id]["hostname"].(string),
				"record_value": "boson-verify=" + f.token[id], "expires_at": time.Now().Add(72 * time.Hour)}
		}
		return out
	}
	owned := func(w http.ResponseWriter, r *http.Request) (string, bool) {
		id := r.PathValue("id")
		if _, ok := f.nets[id]; !ok {
			jsonErr(w, 404, "not_found")
			return "", false
		}
		if f.keys[id] != key(r) {
			jsonErr(w, 403, "forbidden")
			return "", false
		}
		return id, true
	}
	lock := func(fn http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			defer f.mu.Unlock()
			fn(w, r)
		}
	}
	mux.HandleFunc("GET /v1/networks/lookup", lock(func(w http.ResponseWriter, r *http.Request) {
		h := strings.ToLower(r.URL.Query().Get("hostname"))
		for id, n := range f.nets {
			if n["hostname"] != h {
				continue
			}
			if k := key(r); k != "" && f.keys[id] == k {
				_ = json.NewEncoder(w).Encode(listing(id))
				return
			}
			if n["status"] == "verified" {
				_ = json.NewEncoder(w).Encode(n) // public: the bare network
				return
			}
		}
		jsonErr(w, 404, "not_found")
	}))
	mux.HandleFunc("POST /v1/networks", lock(func(w http.ResponseWriter, r *http.Request) {
		var s bosonapi.Submission
		_ = json.NewDecoder(r.Body).Decode(&s)
		for id, n := range f.nets {
			if n["hostname"] == s.Hostname {
				if f.keys[id] == key(r) {
					_ = json.NewEncoder(w).Encode(listing(id)) // idempotent: 200
					return
				}
				jsonErr(w, 409, "hostname_taken")
				return
			}
		}
		id := "net_" + strings.ReplaceAll(s.Hostname, ".", "")
		f.nets[id] = map[string]any{"id": id, "hostname": s.Hostname, "port": s.Port, "tls": *s.TLS, "name": s.Name,
			"status": "pending", "origin": "local", "created_at": time.Now().UTC()}
		f.keys[id], f.token[id] = key(r), "tok1"
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(listing(id))
	}))
	mux.HandleFunc("PATCH /v1/networks/{id}", lock(func(w http.ResponseWriter, r *http.Request) {
		id, ok := owned(w, r)
		if !ok {
			return
		}
		var s bosonapi.Submission
		_ = json.NewDecoder(r.Body).Decode(&s)
		if s.Name != "" {
			f.nets[id]["name"] = s.Name
		}
		_ = json.NewEncoder(w).Encode(listing(id))
	}))
	mux.HandleFunc("PUT /v1/networks/{id}/theme", lock(func(w http.ResponseWriter, r *http.Request) {
		if f.noTheme {
			http.NotFound(w, r)
			return
		}
		id, ok := owned(w, r)
		if !ok {
			return
		}
		if f.imported[id] {
			jsonErr(w, 409, "not_local")
			return
		}
		var t map[string]any
		if json.NewDecoder(r.Body).Decode(&t) != nil || r.Header.Get("Content-Type") != "application/json" {
			jsonErr(w, 400, "invalid_theme")
			return
		}
		if logo, _ := t["logo"].(string); logo != "" && !strings.HasPrefix(logo, "https://") {
			jsonErr(w, 400, "invalid_theme")
			return
		}
		f.themePuts++
		if f.themes == nil {
			f.themes = map[string]map[string]any{}
		}
		f.themes[id] = t
		_ = json.NewEncoder(w).Encode(listing(id))
	}))
	mux.HandleFunc("DELETE /v1/networks/{id}/theme", lock(func(w http.ResponseWriter, r *http.Request) {
		if f.noTheme {
			http.NotFound(w, r)
			return
		}
		id, ok := owned(w, r)
		if !ok {
			return
		}
		if _, ok := f.themes[id]; !ok {
			jsonErr(w, 404, "not_found")
			return
		}
		delete(f.themes, id)
		w.WriteHeader(204)
	}))
	mux.HandleFunc("POST /v1/networks/{id}/verify", lock(func(w http.ResponseWriter, r *http.Request) {
		id, ok := owned(w, r)
		if !ok {
			return
		}
		if !f.dnsOK {
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"success":false,"report":{"success":false,"results":{"cloudflare":{"outcome":"match"},"google":{"outcome":"missing_record"},"quad9":{"outcome":"timeout"}}}}`))
			return
		}
		f.nets[id]["status"] = "verified"
		f.nets[id]["last_checked_at"] = time.Now().UTC()
		f.nets[id]["last_verified_at"] = time.Now().UTC()
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "network": f.nets[id],
			"report": map[string]any{"success": true, "results": map[string]any{"cloudflare": map[string]string{"outcome": "match"}, "google": map[string]string{"outcome": "match"}, "quad9": map[string]string{"outcome": "match"}}}})
	}))
	mux.HandleFunc("POST /v1/networks/{id}/regenerate-token", lock(func(w http.ResponseWriter, r *http.Request) {
		id, ok := owned(w, r)
		if !ok {
			return
		}
		f.token[id] = "tok2"
		f.nets[id]["status"] = "pending"
		_ = json.NewEncoder(w).Encode(listing(id))
	}))
	mux.HandleFunc("DELETE /v1/networks/{id}", lock(func(w http.ResponseWriter, r *http.Request) {
		id, ok := owned(w, r)
		if !ok {
			return
		}
		delete(f.nets, id)
		w.WriteHeader(204)
	}))
	operator := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("X-Internal-Token") != f.internal {
			jsonErr(w, 401, "unauthorized")
			return false
		}
		if a := r.Header.Get("X-Acting-User"); a != "" {
			f.acting = append(f.acting, a)
		}
		return true
	}
	mux.HandleFunc("GET /v1/networks", lock(func(w http.ResponseWriter, r *http.Request) {
		if !operator(w, r) {
			return
		}
		owner := r.URL.Query().Get("owner")
		// Two pages for usr_ana, so the client has to follow next_cursor.
		switch {
		case owner == "usr_ana" && r.URL.Query().Get("cursor") == "":
			_, _ = w.Write([]byte(`{"networks":[{"id":"net_a","hostname":"irc.ana.org","port":6697,"tls":true,"name":"Ana","status":"verified","created_at":"2026-11-01T10:00:00Z"}],"next_cursor":"c2","total_estimate":2}`))
		case owner == "usr_ana":
			_, _ = w.Write([]byte(`{"networks":[{"id":"net_b","hostname":"irc.ana2.org","port":6697,"tls":true,"name":"Ana 2","status":"pending"}],"next_cursor":null}`))
		default:
			_, _ = w.Write([]byte(`{"networks":[],"next_cursor":null}`))
		}
	}))
	mux.HandleFunc("GET /v1/admin/peers", lock(func(w http.ResponseWriter, r *http.Request) {
		if !operator(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"peers": f.peers})
	}))
	mux.HandleFunc("PATCH /v1/admin/peers/{id}", lock(func(w http.ResponseWriter, r *http.Request) {
		if !operator(w, r) {
			return
		}
		var in map[string]bool
		_ = json.NewDecoder(r.Body).Decode(&in)
		for _, p := range f.peers {
			if p["id"] == r.PathValue("id") {
				p["enabled"] = in["enabled"]
				_ = json.NewEncoder(w).Encode(map[string]any{"peer": p})
				return
			}
		}
		jsonErr(w, 404, "not_found")
	}))
	// The settings API (directory >= 1.4.0) and the rest of the peers API.
	settingsOn := func(w http.ResponseWriter, r *http.Request) bool {
		if f.settings == nil {
			http.NotFound(w, r)
			return false
		}
		return operator(w, r)
	}
	builtin := func() map[string]any {
		for _, p := range f.peers {
			if p["builtin"] == true {
				return p
			}
		}
		return nil
	}
	settingsBody := func() map[string]any {
		b := builtin()
		active := b != nil && b["enabled"] == true && f.settings["include_main"] == true && !f.isMain
		return map[string]any{"settings": f.settings, "defaults": f.settings, "overridden": []string{}, "is_main": f.isMain,
			"main_active": active, "main_peer": b}
	}
	mux.HandleFunc("GET /v1/admin/settings", lock(func(w http.ResponseWriter, r *http.Request) {
		if !settingsOn(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(settingsBody())
	}))
	mux.HandleFunc("PATCH /v1/admin/settings", lock(func(w http.ResponseWriter, r *http.Request) {
		if !settingsOn(w, r) {
			return
		}
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.patches = append(f.patches, in)
		if v, ok := in["sync_every"].(string); ok && v == "5s" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"invalid_request","message":"sync_every must be between 1m and 168h"}`))
			return
		}
		for k, v := range in {
			f.settings[k] = v
		}
		if on, ok := in["include_main"].(bool); ok {
			// The directory creates/re-enables the builtin on include_main=true
			// and hides it on false.
			if b := builtin(); on && b == nil {
				f.peers = append([]map[string]any{{"id": "peer_main", "url": "https://directory.boson.chat", "name": "boson", "enabled": true, "builtin": true, "listing_count": 0}}, f.peers...)
			} else if on {
				b["enabled"] = true
			} else if b != nil {
				f.peers = f.peers[1:]
			}
		}
		_ = json.NewEncoder(w).Encode(settingsBody())
	}))
	mux.HandleFunc("GET /v1/admin/peers/check", lock(func(w http.ResponseWriter, r *http.Request) {
		if !settingsOn(w, r) {
			return
		}
		u := r.URL.Query().Get("url")
		if strings.Contains(u, "down") {
			w.WriteHeader(502)
			_, _ = w.Write([]byte(`{"error":"peer_unreachable","message":"connection refused"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"url": u, "well_known": map[string]any{"name": "Friends", "api": u + "/v1"}})
	}))
	mux.HandleFunc("POST /v1/admin/peers", lock(func(w http.ResponseWriter, r *http.Request) {
		if !settingsOn(w, r) {
			return
		}
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		p := map[string]any{"id": "peer_new", "url": in["url"], "name": "Friends", "enabled": true, "builtin": false, "listing_count": 0}
		f.peers = append(f.peers, p)
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"peer": p})
	}))
	mux.HandleFunc("DELETE /v1/admin/peers/{id}", lock(func(w http.ResponseWriter, r *http.Request) {
		if !settingsOn(w, r) {
			return
		}
		for i, p := range f.peers {
			if p["id"] == r.PathValue("id") {
				f.peers = append(f.peers[:i], f.peers[i+1:]...)
				w.WriteHeader(204)
				return
			}
		}
		jsonErr(w, 404, "not_found")
	}))
	mux.HandleFunc("POST /v1/admin/peers/{id}/sync", lock(func(w http.ResponseWriter, r *http.Request) {
		if !settingsOn(w, r) {
			return
		}
		for _, p := range f.peers {
			if p["id"] == r.PathValue("id") {
				p["listing_count"], p["last_sync_at"] = 5, time.Now()
				_ = json.NewEncoder(w).Encode(map[string]any{"peer": p, "result": map[string]any{"created": 5, "verified": 4}})
				return
			}
		}
		jsonErr(w, 404, "not_found")
	}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, srv
}

func TestRegistrationFlow(t *testing.T) {
	f, srv := newFakeV1(t)
	d := NewDirectoryService(Options{Client: bosonapi.NewDirectoryClient(srv.URL, time.Second, nil), Registration: true,
		Secret: []byte("0123456789abcdef0123456789abcdef"), Hostname: "irc.example.org"}).(*DirectoryService)
	ctx := context.Background()
	st := d.Status(ctx, "")
	if st.Listing != nil || st.Public != nil || st.Unsupported != "" || st.ListingError != "" || st.PublicError != "" || st.Hostname != "irc.example.org" {
		t.Fatalf("before: %+v", st)
	}
	if _, err := d.Submit(ctx, "root", bosonapi.Submission{Hostname: "1.2.3.4", Port: 6697, Name: "x"}); err == nil {
		t.Fatal("IP accepted")
	}
	l, err := d.Submit(ctx, "root", bosonapi.Submission{Hostname: "IRC.Example.org.", Port: 6697, Name: "Example"})
	if err != nil || l.Verification == nil || l.Verification.RecordName != "_boson.irc.example.org" || l.Network.Hostname != "irc.example.org" ||
		l.Network.VerificationStatus != "pending" || l.Network.RegisteredAt == nil {
		t.Fatalf("submit: %v %+v", err, l)
	}
	id := l.Network.ID
	if f.keys[id] != d.Key("irc.example.org") || d.Key("irc.example.org") == d.Key("irc.other.org") {
		t.Fatal("management key not derived per hostname")
	}
	// Create is idempotent for the same key.
	if again, err := d.Submit(ctx, "root", bosonapi.Submission{Hostname: "irc.example.org", Port: 6697, Name: "Example"}); err != nil || again.Network.ID != id {
		t.Fatalf("resubmit: %v %+v", err, again)
	}
	st = d.Status(ctx, "irc.example.org")
	if st.Listing == nil || st.Listing.Network.VerificationStatus != "pending" || st.Public != nil {
		t.Fatalf("pending: %+v", st)
	}
	// DNS not there yet: 409 + report is a result, not an error.
	r, err := d.Verify(ctx, "root", "irc.example.org", id)
	if err != nil || r.Success || r.Report.Results["google"].Outcome != "missing_record" {
		t.Fatalf("failed verify: %v %+v", err, r)
	}
	f.dnsOK = true
	r, err = d.Verify(ctx, "root", "irc.example.org", id)
	if err != nil || !r.Success || r.Report.Results["quad9"].Outcome != "match" || r.Network == nil || r.Network.VerificationStatus != "verified" {
		t.Fatalf("verify: %v %+v", err, r)
	}
	st = d.Status(ctx, "irc.example.org")
	if st.Public == nil || st.Public.ID != id || st.Listing.Network.LastVerifiedAt == nil || st.Listing.Network.VerificationLastCheckedAt == nil {
		t.Fatalf("listed: %+v", st)
	}
	if l, err := d.Update(ctx, "root", "irc.example.org", id, bosonapi.Submission{Name: "Renamed"}); err != nil || l.Network.Name != "Renamed" {
		t.Fatalf("update: %v %+v", err, l)
	}
	if l, err := d.Regenerate(ctx, "root", "irc.example.org", id); err != nil || l.Verification.RecordValue != "boson-verify=tok2" {
		t.Fatalf("regenerate: %v %+v", err, l)
	}
	// Another panel (different secret) can't touch it, nor take the hostname.
	other := NewDirectoryService(Options{Client: bosonapi.NewDirectoryClient(srv.URL, time.Second, nil), Registration: true, Secret: []byte("x")})
	if _, err := other.Verify(ctx, "eve", "irc.example.org", id); err == nil {
		t.Fatal("foreign key accepted")
	}
	if _, err := other.Submit(ctx, "eve", bosonapi.Submission{Hostname: "irc.example.org", Port: 6697, Name: "Mine"}); err == nil {
		t.Fatal("hostname taken by another key")
	} else if e, _ := apperr.As(err); e.Code != "hostname_taken" {
		t.Fatalf("taken: %v", err)
	}
	if err := d.Unlist(ctx, "root", "irc.example.org", id); err != nil {
		t.Fatalf("unlist: %v", err)
	}
	if st := d.Status(ctx, "irc.example.org"); st.Listing != nil || st.Public != nil {
		t.Fatalf("after unlist: %+v", st)
	}
}

func TestRegistrationNotLive(t *testing.T) {
	// An old backend (api.boson.chat): no /v1/networks, only GET /servers.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /servers", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"servers":[{"id":"x","hostname":"irc.other.org","name":"Other","verification_status":"verified"}],"count":1}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := bosonapi.NewDirectoryClient(srv.URL, time.Second, nil)
	on := NewDirectoryService(Options{Client: c, Registration: true, Secret: []byte("s"), Hostname: "irc.other.org"})
	st := on.Status(context.Background(), "")
	if st.Unsupported == "" || st.Public == nil || st.Public.Name != "Other" {
		t.Fatalf("unsupported but publicly listed: %+v", st)
	}
	off := NewDirectoryService(Options{Client: c, Hostname: "irc.example.org"})
	if _, err := off.Submit(context.Background(), "root", bosonapi.Submission{Hostname: "irc.example.org", Port: 6697, Name: "x"}); err == nil {
		t.Fatal("submit with registration off")
	} else if e, _ := apperr.As(err); e.Code != "directory_registration_off" {
		t.Fatalf("got %v", err)
	}
}

func TestLocalDirectory(t *testing.T) {
	f, srv := newFakeV1(t)
	l := NewLocalService(LocalOptions{Client: bosonapi.NewOperatorClient(srv.URL, "itok", time.Second, nil), URL: srv.URL})
	ctx := context.Background()
	fed := l.Federation(ctx)
	if fed.Error != "" || fed.MainPeer == nil || !fed.MainPeer.Enabled || fed.MainPeer.URL != "https://directory.boson.chat" || len(fed.Peers) != 2 {
		t.Fatalf("federation: %+v", fed)
	}
	fed, err := l.SetIncludeMain(ctx, "root", false)
	if err != nil || fed.MainPeer.Enabled || fed.Peers[0].Enabled {
		t.Fatalf("off: %v %+v", err, fed)
	}
	if len(f.acting) == 0 || f.acting[len(f.acting)-1] != "root" {
		t.Fatalf("acting user not sent: %v", f.acting)
	}
	if fed := l.Federation(ctx); fed.MainPeer.Enabled {
		t.Fatal("stayed on")
	}
	nets, err := l.OwnedNetworks(ctx, "usr_ana")
	if err != nil || len(nets) != 2 || nets[0].VerificationStatus != "verified" || nets[1].VerificationStatus != "pending" || nets[0].RegisteredAt == nil {
		t.Fatalf("owned: %v %+v", err, nets)
	}
	// No builtin peer (DIRECTORY_INCLUDE_MAIN=false on the directory).
	f.peers = f.peers[1:]
	if _, err := l.SetIncludeMain(ctx, "root", true); err == nil {
		t.Fatal("no main peer")
	} else if e, _ := apperr.As(err); e.Code != "main_peer_missing" {
		t.Fatalf("got %v", err)
	}
	// Wrong operator token.
	bad := NewLocalService(LocalOptions{Client: bosonapi.NewOperatorClient(srv.URL, "nope", time.Second, nil)})
	if fed := bad.Federation(ctx); fed.Error == "" {
		t.Fatalf("bad token: %+v", fed)
	}
	// A directory without the operator API.
	empty := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(empty.Close)
	old := NewLocalService(LocalOptions{Client: bosonapi.NewOperatorClient(empty.URL, "itok", time.Second, nil)})
	if fed := old.Federation(ctx); fed.Unsupported == "" {
		t.Fatalf("unsupported: %+v", fed)
	}
	if _, err := old.OwnedNetworks(ctx, "usr_ana"); !apperr.IsUnsupported(err) {
		t.Fatalf("owned unsupported: %v", err)
	}
}

// A 1.4.0 directory: include_main through the settings API (also when no
// builtin peer exists yet), is_main, name/operator/contact and peers.
func TestLocalDirectorySettings(t *testing.T) {
	f, srv := newFakeV1(t)
	f.settings = map[string]any{"name": "Stack dir", "operator": "", "contact": "", "include_main": false,
		"main_peer_url": "https://directory.boson.chat", "sync_every": "10m"}
	f.peers = f.peers[1:] // include_main off: no builtin
	l := NewLocalService(LocalOptions{Client: bosonapi.NewOperatorClient(srv.URL, "itok", time.Second, nil), URL: srv.URL})
	ctx := context.Background()

	fed := l.Federation(ctx)
	if fed.Settings == nil || fed.Settings.Settings.Name != "Stack dir" || fed.MainPeer != nil || fed.SettingsUnsupported != "" {
		t.Fatalf("federation: %+v", fed)
	}
	fed, err := l.SetIncludeMain(ctx, "root", true)
	if err != nil || fed.MainPeer == nil || !fed.MainPeer.Enabled || !fed.Settings.MainActive || !fed.Settings.Settings.IncludeMain {
		t.Fatalf("include on without a builtin: %v %+v", err, fed)
	}
	if last := f.patches[len(f.patches)-1]; last["include_main"] != true || f.acting[len(f.acting)-1] != "root" {
		t.Fatalf("patch %v acting %v", last, f.acting)
	}
	if fed, err = l.SetIncludeMain(ctx, "root", false); err != nil || fed.MainPeer != nil || fed.Settings.Settings.IncludeMain {
		t.Fatalf("include off: %v %+v", err, fed)
	}

	name, op, every := " New name ", "Ops team", "1h"
	fed, err = l.UpdateSettings(ctx, "root", SettingsUpdate{Name: &name, Operator: &op, SyncEvery: &every})
	if err != nil || fed.Settings.Settings.Name != "New name" || fed.Settings.Settings.Operator != "Ops team" || fed.Settings.Settings.SyncEvery != "1h" {
		t.Fatalf("settings: %v %+v", err, fed.Settings)
	}
	if _, ok := f.patches[len(f.patches)-1]["contact"]; ok {
		t.Fatal("unset fields must not be sent")
	}
	empty, bad := "", "5s"
	if _, err := l.UpdateSettings(ctx, "root", SettingsUpdate{Name: &empty}); err == nil {
		t.Fatal("empty name")
	}
	if _, err := l.UpdateSettings(ctx, "root", SettingsUpdate{}); err == nil {
		t.Fatal("empty update")
	}
	if _, err := l.UpdateSettings(ctx, "root", SettingsUpdate{SyncEvery: &bad}); err == nil || !strings.Contains(err.Error(), "between 1m") {
		t.Fatalf("directory validation passes through: %v", err)
	}

	// Peers.
	c, err := l.CheckPeer(ctx, "dir.friends.net/")
	if err != nil || c.URL != "https://dir.friends.net" || c.WellKnown.Name != "Friends" {
		t.Fatalf("check: %v %+v", err, c)
	}
	if _, err := l.CheckPeer(ctx, "down.example"); err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("unreachable: %v", err)
	}
	if _, err := l.CheckPeer(ctx, "  "); err == nil {
		t.Fatal("empty url")
	}
	fed, err = l.AddPeer(ctx, "root", "https://dir.friends.net")
	if err != nil || len(fed.Peers) != 2 {
		t.Fatalf("add: %v %+v", err, fed)
	}
	fed, res, err := l.SyncPeer(ctx, "root", "peer_new")
	if err != nil || res.Created != 5 || fed.Peers[1].ListingCount != 5 {
		t.Fatalf("sync: %v %+v %+v", err, res, fed)
	}
	if fed, err = l.SetPeerEnabled(ctx, "root", "peer_new", false); err != nil || fed.Peers[1].Enabled {
		t.Fatalf("disable: %v %+v", err, fed)
	}
	if fed, err = l.RemovePeer(ctx, "root", "peer_new"); err != nil || len(fed.Peers) != 1 {
		t.Fatalf("remove: %v %+v", err, fed)
	}

	// The main directory itself.
	f.isMain = true
	if fed := l.Federation(ctx); !fed.Settings.IsMain {
		t.Fatalf("is_main: %+v", fed.Settings)
	}
	if _, err := l.SetIncludeMain(ctx, "root", true); err == nil {
		t.Fatal("include on the main directory")
	} else if e, _ := apperr.As(err); e.Code != "is_main_directory" {
		t.Fatalf("got %v", err)
	}
}

// Before 1.4.0 the settings are unsupported; the page degrades.
func TestLocalDirectoryWithoutSettings(t *testing.T) {
	_, srv := newFakeV1(t)
	l := NewLocalService(LocalOptions{Client: bosonapi.NewOperatorClient(srv.URL, "itok", time.Second, nil)})
	ctx := context.Background()
	fed := l.Federation(ctx)
	if fed.Settings != nil || !strings.Contains(fed.SettingsUnsupported, "1.4.0") || fed.MainPeer == nil {
		t.Fatalf("old directory: %+v", fed)
	}
	name := "x"
	if _, err := l.UpdateSettings(ctx, "root", SettingsUpdate{Name: &name}); !apperr.IsUnsupported(err) || !strings.Contains(err.Error(), "1.4.0") {
		t.Fatalf("settings on an old directory: %v", err)
	}
}
