package updates_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/fakestack"
	"github.com/boson-chat/admin/internal/services/updates"
)

// manifest builds a stack release's manifest.json.
func manifest(t *testing.T, version, minAdmin string, svcs map[string]updates.ManifestService) string {
	t.Helper()
	b, err := json.Marshal(updates.Manifest{Schema: 1, Version: version, Release: true, MinAdmin: minAdmin, Services: svcs,
		Migrations: []string{"0001-bridge-client-webirc.sh"}, Files: map[string]string{"compose.yaml": strings.Repeat("a", 64)}})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// stackRelease 1.1.0: Ergo, RoleServ and the web IRC gateway on the
// stack's version, auth's default 1.21.0 (the install pins 1.20.0: raised),
// voice's 1.14.0 (the install pins the newer 1.15.0: kept), the web
// client's 0.100.0 (the pin), Traefik's config changes, a migration, a
// file edited on this server.
func stackRelease(t *testing.T) *fakestack.StackRelease {
	m := map[string]updates.ManifestService{
		"traefik":       {Compose: "compose.yaml", Image: "traefik:v3.7.20", Mounts: []string{"traefik/dynamic"}},
		"ergo":          {Compose: "compose.yaml", Image: harbor + "stack-ergo:1.1.0", Built: true, Profiles: []string{"irc"}, Mounts: []string{"config"}},
		"roleserv":      {Compose: "compose.yaml", Image: harbor + "stack-ergo:1.1.0", Built: true, Profiles: []string{"irc"}},
		"webircgateway": {Compose: "compose.yaml", Image: harbor + "stack-webircgateway:1.1.0", Built: true, Profiles: []string{"web"}},
		"auth":          {Compose: "compose.yaml", Image: harbor + "auth:1.21.0", Env: "AUTH_IMAGE"},
		"api":           {Compose: "compose.yaml", Image: harbor + "backend:0.57.0", Env: "API_IMAGE"},
		"web":           {Compose: "compose.yaml", Image: harbor + "web:0.100.0", Env: "WEB_IMAGE"},
		"quicvoiced":    {Compose: "compose.yaml", Image: harbor + "quicvoice:1.14.0", Env: "QUICVOICE_IMAGE"},
		"admin":         {Compose: "compose.yaml", Image: harbor + "admin:1.26.0", Env: "ADMIN_IMAGE"},
		"postgres":      {Compose: "compose.yaml", Image: "postgres:18-alpine"},
		"config-init":   {Compose: "compose.yaml", Image: "busybox:1.37.0", Oneshot: true, Mounts: []string{"config"}},
		"victoriametrics": {Compose: "compose.yaml", Image: "victoriametrics/victoria-metrics:v1.131.0",
			Mounts: []string{"monitoring/scrape.yml"}},
	}
	return &fakestack.StackRelease{
		Version: "1.1.0", Body: "## 1.1.0\n\n### Features\n\n* versioned stack releases",
		Manifest: manifest(t, "1.1.0", "1.26.0", m), Bundle: []byte("bundle 1.1.0"),
		Files: []string{"update compose.yaml", "local traefik/dynamic/stack.yml", "add VERSION", "update monitoring/scrape.yml"},
		Diffs: map[string][]string{
			"compose.yaml": {"--- /srv/boson/compose.yaml", "+++ /srv/boson/.boson/staging/1.1.0/compose.yaml", "@@ -1 +1 @@",
				"-    image: traefik:v3.7.19", "+    image: traefik:v3.7.20"},
		},
		Migrations: map[string]string{"0001-bridge-client-webirc.sh": "Bridge (add-on installs): the web gateway's WEBIRC password"},
		Hashes:     map[string]string{"traefik": "h1-traefik"},
		Defaults: map[string]string{"ergo": harbor + "stack-ergo:1.1.0", "roleserv": harbor + "stack-ergo:1.1.0",
			"traefik": "traefik:v3.7.20"},
	}
}

// newStackHost: newStack plus Traefik, Postgres, VictoriaMetrics and
// RoleServ running, an install from before stack releases (no VERSION), and
// the stack's 1.1.0 release.
func newStackHost(t *testing.T) (*fakestack.Stack, *updates.Service) {
	fs, srv := newStack(t)
	fs.Set(func(s *fakestack.Stack) {
		s.AddService("traefik", "traefik:v3.7.19")
		s.AddService("roleserv", harbor+"stack-ergo:2.19.1")
		s.AddService("victoriametrics", "victoriametrics/victoria-metrics:v1.131.0")
		s.Defaults = map[string]string{"ergo": harbor + "stack-ergo:2.19.1", "roleserv": harbor + "stack-ergo:2.19.1",
			"traefik": "traefik:v3.7.19", "victoriametrics": "victoriametrics/victoria-metrics:v1.131.0"}
		s.Profiles = "irc,auth,api,web,voice,admin,updates,metrics"
		s.StackReleases = []*fakestack.StackRelease{stackRelease(t)}
		s.Tags["boson/stack-ergo:1.1.0"] = true
		s.Tags["boson/stack-webircgateway:1.1.0"] = true
		s.Tags["boson/auth:1.21.0"] = true
		s.Tags["traefik:v3.7.20"] = true
	})
	return fs, newService(t, fs, srv, "1.26.0", t.TempDir())
}

func TestStackReleaseReviewApplyHealthy(t *testing.T) {
	fs, svc := newStackHost(t)
	ctx := context.Background()
	st := svc.Check(ctx)
	c := component(st, "stack")
	if !c.Update || !c.Applyable || c.Latest != "1.1.0" || c.Installed != "" || c.Stack == nil {
		t.Fatalf("stack: %+v (why %q, error %q)", c, c.Why, c.Error)
	}
	ss := c.Stack
	if !ss.PreRelease || ss.Baseline || ss.Error != "" {
		t.Fatalf("pre-release install: %+v", ss)
	}
	if len(ss.Files) != 4 || ss.Files[0].Path != "compose.yaml" || len(ss.Files[0].Diff) != 5 || ss.Files[1].Action != "local" {
		t.Fatalf("files: %+v", ss.Files)
	}
	if f := ss.Files[3]; f.Path != "monitoring/scrape.yml" || !slices.Equal(f.Services, []string{"victoriametrics"}) {
		t.Fatalf("a mounted file restarts its service: %+v", f)
	}
	if len(ss.Migrations) != 1 || ss.Migrations[0].State != "pending" || !strings.Contains(ss.Migrations[0].About, "WEBIRC") {
		t.Fatalf("migrations: %+v", ss.Migrations)
	}
	pins := map[string]updates.PinChange{}
	for _, p := range ss.Pins {
		pins[p.EnvVar] = p
	}
	if p := pins["AUTH_IMAGE"]; p.Action != "raise" || p.To != harbor+"auth:1.21.0" {
		t.Fatalf("auth's older pin is raised: %+v", p)
	}
	if p := pins["QUICVOICE_IMAGE"]; p.Action != "keep" || p.To != harbor+"quicvoice:1.15.0" {
		t.Fatalf("voice's newer pin is kept: %+v", p)
	}
	if p := pins["WEB_IMAGE"]; p.Action != "keep" {
		t.Fatalf("web: %+v", p)
	}
	var included []string
	for _, i := range ss.Included {
		included = append(included, i.Name+":"+i.To)
	}
	if !slices.Contains(included, "Ergo:"+harbor+"stack-ergo:1.1.0") {
		t.Fatalf("Ergo is included, on the stack's version: %v", included)
	}
	if !slices.Equal(ss.Restarts, []string{"auth", "ergo", "roleserv", "traefik", "victoriametrics"}) {
		t.Fatalf("restarts: %v", ss.Restarts)
	}
	if !ss.Impact.ChatDisconnect || !c.Impact.ChatDisconnect || !strings.Contains(ss.Impact.Who, "Ergo") {
		t.Fatalf("Ergo changes: IRC users are disconnected: %+v", ss.Impact)
	}
	for _, id := range []string{"ergo", "webircgateway"} {
		if x := component(st, id); x.ID != "" {
			t.Fatalf("%s has no row: it comes with the stack", id)
		}
	}

	p, err := svc.Preview(updates.ApplyRequest{Mode: "selected", Components: []string{"stack"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 || p.Items[0].Stack == nil || !strings.Contains(p.Summary, "disconnect all chat users") {
		t.Fatalf("preview: %+v", p)
	}
	// "Without disconnecting users" leaves the stack out.
	if q, err := svc.Preview(updates.ApplyRequest{Mode: "quiet"}); err != nil || !slices.Contains(q.Skipped, "stack") {
		t.Fatalf("quiet: %+v %v", q, err)
	}

	if _, err := svc.Apply(ctx, "alice", updates.ApplyRequest{Mode: "selected", Components: []string{"stack"}}, "tok"); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, svc, updates.StateSucceeded, updates.StateFailed, updates.StateRolledBack)
	if done.State != updates.StateSucceeded {
		t.Fatalf("stack update: %s\nsteps: %+v\nlog: %v", done.Error, done.Steps, done.Log)
	}
	var ids []string
	for _, s := range done.Steps {
		ids = append(ids, s.ID)
		if s.State != updates.StepOK {
			t.Fatalf("step %s = %s (%s)", s.ID, s.State, s.Detail)
		}
	}
	if strings.Join(ids, ",") != "preflight,backup,stage,pull,configure,recreate,health,verify" {
		t.Fatalf("steps %v", ids)
	}
	fs.Set(func(s *fakestack.Stack) {
		if s.StackVersion != "1.1.0" || !s.StackBaseline || !slices.Contains(s.Applied, "0001-bridge-client-webirc.sh") {
			t.Errorf("stack state: %s %v %v", s.StackVersion, s.StackBaseline, s.Applied)
		}
		if len(s.StageNetworks) == 0 || s.StageNetworks[0] != "bridge" {
			t.Errorf("only the stage job has a network: %v", s.StageNetworks)
		}
	})
	if fs.Image("ergo") != harbor+"stack-ergo:1.1.0" || fs.Image("roleserv") != harbor+"stack-ergo:1.1.0" || fs.Image("traefik") != "traefik:v3.7.20" {
		t.Fatalf("ergo %s roleserv %s traefik %s", fs.Image("ergo"), fs.Image("roleserv"), fs.Image("traefik"))
	}
	if fs.Image("auth") != harbor+"auth:1.21.0" || fs.EnvValue("AUTH_IMAGE") != harbor+"auth:1.21.0" {
		t.Fatalf("auth's pin raised: %s / %s", fs.Image("auth"), fs.EnvValue("AUTH_IMAGE"))
	}
	if fs.Image("quicvoiced") != harbor+"quicvoice:1.15.0" || fs.EnvValue("QUICVOICE_IMAGE") != harbor+"quicvoice:1.15.0" {
		t.Fatal("voice's newer pin is kept and voice isn't recreated")
	}
	if fs.Image("admin") != harbor+"admin:1.26.0" {
		t.Fatal("the panel isn't touched")
	}
	it := done.Items[0].Stack
	if !slices.Equal(it.Recreated, []string{"auth", "ergo", "roleserv", "traefik", "victoriametrics"}) || !slices.Equal(it.Migrations, []string{"0001-bridge-client-webirc.sh"}) {
		t.Fatalf("applied: %+v", it)
	}
	var checks []string
	for _, c := range done.Checks {
		checks = append(checks, c.ID)
		if !c.OK && !c.Skipped {
			t.Fatalf("check %s: %s", c.ID, c.Detail)
		}
	}
	for _, want := range []string{"version-stack", "image-ergo", "image-traefik"} {
		if !slices.Contains(checks, want) {
			t.Fatalf("checks %v miss %s", checks, want)
		}
	}
	if done.Backup == nil || !slices.ContainsFunc(done.Backup.Files, func(f updates.BackupFile) bool { return f.Name == "stack-dir.tgz" }) {
		t.Fatalf("the backup has the stack directory: %+v", done.Backup)
	}
	// Installed now: nothing more to update (the panel checks again by
	// itself when a run ends; a Check meanwhile answers with that one's).
	var after updates.ComponentStatus
	for i := 0; i < 100; i++ {
		if after = component(svc.Check(ctx), "stack"); !after.Update && after.Installed == "1.1.0" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if after.Update || after.Installed != "1.1.0" {
		t.Fatalf("after: %+v", after)
	}
	for i := 0; i < 100 && len(fs.Jobs()) > 0; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if j := fs.Jobs(); len(j) != 0 {
		t.Fatalf("helper containers left behind: %v", j)
	}
}

// A migration fails after the files are written: the update rolls itself
// back (files, .env, images) without anyone pressing a button.
func TestStackReleaseFailedMigrationRollsBackByItself(t *testing.T) {
	fs, svc := newStackHost(t)
	ctx := context.Background()
	fs.Set(func(s *fakestack.Stack) { s.StackReleases[0].FailMigration = true })
	svc.Check(ctx)
	if _, err := svc.Apply(ctx, "alice", updates.ApplyRequest{Mode: "selected", Components: []string{"stack"}}, "tok"); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, svc, updates.StateRolledBack, updates.StateRollbackFailed)
	if done.Kind != "rollback" || done.Actor != "automatic rollback" || done.State != updates.StateRolledBack {
		t.Fatalf("automatic rollback: %+v\nsteps %+v\nlog %v", done, done.Steps, done.Log)
	}
	h := svc.Status(ctx).History
	if len(h) != 2 || h[1].State != updates.StateRolledBack || !strings.Contains(h[1].Error, "migration") {
		t.Fatalf("history: %+v", h)
	}
	fs.Set(func(s *fakestack.Stack) {
		if s.StackVersion != "" || len(s.Applied) != 0 {
			t.Errorf("stack state back: %q %v", s.StackVersion, s.Applied)
		}
	})
	if fs.EnvValue("AUTH_IMAGE") != harbor+"auth:1.20.0" || fs.Image("ergo") != harbor+"stack-ergo:2.19.1" {
		t.Fatalf("after the rollback: AUTH_IMAGE %s, ergo %s", fs.EnvValue("AUTH_IMAGE"), fs.Image("ergo"))
	}
}

// Ergo's new image never gets healthy: rolled back by itself, and the
// previous Ergo runs again.
func TestStackReleaseUnhealthyRollsBackByItself(t *testing.T) {
	fs, svc := newStackHost(t)
	ctx := context.Background()
	fs.Set(func(s *fakestack.Stack) { s.Broken[harbor+"stack-ergo:1.1.0"] = true })
	svc.Check(ctx)
	if _, err := svc.Apply(ctx, "alice", updates.ApplyRequest{Mode: "selected", Components: []string{"stack"}}, "tok"); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, svc, updates.StateRolledBack, updates.StateRollbackFailed)
	if done.State != updates.StateRolledBack {
		t.Fatalf("rollback: %+v\n%v", done.Steps, done.Log)
	}
	if fs.Image("ergo") != harbor+"stack-ergo:2.19.1" || fs.Image("traefik") != "traefik:v3.7.19" {
		t.Fatalf("ergo %s traefik %s", fs.Image("ergo"), fs.Image("traefik"))
	}
}

// What blocks a stack update before anything happens.
func TestStackReleaseNotApplyable(t *testing.T) {
	ctx := context.Background()
	t.Run("panel too old", func(t *testing.T) {
		fs, svc := newStackHost(t)
		fs.Set(func(s *fakestack.Stack) {
			r := s.StackReleases[0]
			var m updates.Manifest
			_ = json.Unmarshal([]byte(r.Manifest), &m)
			m.MinAdmin = "1.30.0"
			b, _ := json.Marshal(m)
			r.Manifest = string(b)
		})
		c := component(svc.Check(ctx), "stack")
		if !c.Update || c.Applyable || !c.Stack.AdminTooOld || !strings.Contains(c.Why, "admin panel 1.30.0") {
			t.Fatalf("%+v", c)
		}
	})
	t.Run("bundle checksum wrong", func(t *testing.T) {
		fs, svc := newStackHost(t)
		fs.Set(func(s *fakestack.Stack) { s.StackReleases[0].BadSHA = true })
		c := component(svc.Check(ctx), "stack")
		if c.Applyable || !strings.Contains(c.Stack.Error, "SHA-256") {
			t.Fatalf("%+v / %+v", c, c.Stack)
		}
		fs.Set(func(s *fakestack.Stack) {
			if s.StackVersion != "" {
				t.Error("nothing changes")
			}
		})
	})
	t.Run("no bundle yet", func(t *testing.T) {
		fs, svc := newStackHost(t)
		fs.Set(func(s *fakestack.Stack) { s.StackReleases[0].NoAssets = true })
		c := component(svc.Check(ctx), "stack")
		if c.Applyable || !slices.Contains(c.Pending, "1.1.0") {
			t.Fatalf("%+v", c)
		}
	})
	t.Run("Ergo's image not on the registry yet", func(t *testing.T) {
		fs, svc := newStackHost(t)
		fs.Set(func(s *fakestack.Stack) { delete(s.Tags, "boson/stack-ergo:1.1.0") })
		c := component(svc.Check(ctx), "stack")
		if c.Applyable || !slices.Contains(c.Pending, "1.1.0") || !strings.Contains(c.Why, "stack-ergo:1.1.0") {
			t.Fatalf("%+v", c)
		}
	})
	t.Run("up to date", func(t *testing.T) {
		fs, svc := newStackHost(t)
		fs.Set(func(s *fakestack.Stack) { s.StackVersion, s.StackBaseline = "1.1.0", true })
		c := component(svc.Check(ctx), "stack")
		if c.Update || c.Installed != "1.1.0" || c.Latest != "1.1.0" {
			t.Fatalf("%+v", c)
		}
	})
}

// An add-on install (bridge, no Ergo): the release moves evrd-links'
// default, which must never recreate it; EVRD_LINKS_IMAGE gets pinned to
// what runs. evrd restarts because the migration changed its config file.
func TestStackReleaseAddOnNeverRestartsLinks(t *testing.T) {
	fs := fakestack.New(map[string]string{
		"auth": harbor + "auth:1.27.0", "evrd": "ghcr.io/4evrd/evrd:0.61.0", "evrd-links": "ghcr.io/4evrd/evrd:0.60.0",
		"webircgateway": harbor + "stack-webircgateway:1.1.0-boson.2", "admin": harbor + "admin:1.26.0", "postgres": "postgres:18-alpine",
	})
	fs.Env = map[string]string{"AUTH_IMAGE": harbor + "auth:1.27.0", "EVRD_IMAGE": "ghcr.io/4evrd/evrd:0.61.0", "ADMIN_IMAGE": harbor + "admin:1.26.0"}
	fs.Profiles = "auth,bridge,web,admin,updates"
	fs.Defaults = map[string]string{"webircgateway": harbor + "stack-webircgateway:1.1.0-boson.2", "evrd-links": "ghcr.io/4evrd/evrd:0.60.0"}
	m := map[string]updates.ManifestService{
		"evrd-links":    {Compose: "compose.yaml", Image: "ghcr.io/4evrd/evrd:0.62.0", Env: "EVRD_LINKS_IMAGE", Profiles: []string{"bridge"}},
		"evrd":          {Compose: "compose.yaml", Image: "ghcr.io/4evrd/evrd:0.61.0", Env: "EVRD_IMAGE", Profiles: []string{"bridge"}, Mounts: []string{"bridge/evrd.conf"}},
		"webircgateway": {Compose: "compose.yaml", Image: harbor + "stack-webircgateway:1.2.0", Built: true, Profiles: []string{"web"}},
		"auth":          {Compose: "compose.yaml", Image: harbor + "auth:1.27.0", Env: "AUTH_IMAGE"},
		"admin":         {Compose: "compose.yaml", Image: harbor + "admin:1.26.0", Env: "ADMIN_IMAGE"},
	}
	fs.Releases = map[string][]fakestack.Release{"boson-chat/auth": {{Tag: "v1.27.0"}}, "4evrd/evrd": {{Tag: "v0.61.0"}},
		"boson-chat/admin": {{Tag: "v1.26.0"}}}
	fs.StackVersion, fs.StackBaseline = "1.1.0", true
	fs.StackReleases = []*fakestack.StackRelease{{Version: "1.2.0", Body: "## 1.2.0", Manifest: manifest(t, "1.2.0", "1.26.0", m),
		Bundle: []byte("b"), Files: []string{"update compose.yaml"},
		Migrations: map[string]string{"0001-bridge-client-webirc.sh": "Bridge"},
		Hashes:     map[string]string{"evrd-links": "h1-links", "webircgateway": "h1-wig"},
		Defaults:   map[string]string{"webircgateway": harbor + "stack-webircgateway:1.2.0", "evrd-links": "ghcr.io/4evrd/evrd:0.62.0"},
		Restart:    []string{"evrd"}}}
	fs.Tags["boson/stack-webircgateway:1.2.0"] = true
	srv := newServer(t, fs)
	svc := newService(t, fs, srv, "1.26.0", t.TempDir())
	ctx := context.Background()
	c := component(svc.Check(ctx), "stack")
	if !c.Applyable || c.Installed != "1.1.0" || c.Latest != "1.2.0" {
		t.Fatalf("%+v / %q", c, c.Why)
	}
	if !slices.Contains(c.Stack.Kept, "evrd-links") || slices.Contains(c.Stack.Restarts, "evrd-links") {
		t.Fatalf("evrd-links is never restarted: %+v", c.Stack)
	}
	var links updates.PinChange
	for _, p := range c.Stack.Pins {
		if p.EnvVar == "EVRD_LINKS_IMAGE" {
			links = p
		}
	}
	if links.Action != "pin" || links.To != "ghcr.io/4evrd/evrd:0.60.0" {
		t.Fatalf("EVRD_LINKS_IMAGE: %+v", links)
	}
	if _, err := svc.Apply(ctx, "op", updates.ApplyRequest{Mode: "selected", Components: []string{"stack"}}, "tok"); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, svc, updates.StateSucceeded, updates.StateFailed, updates.StateRolledBack)
	if done.State != updates.StateSucceeded {
		t.Fatalf("%s\n%+v\n%v\nhistory %+v", done.Error, done.Steps, done.Log, svc.Status(ctx).History)
	}
	if fs.Image("evrd-links") != "ghcr.io/4evrd/evrd:0.60.0" || fs.EnvValue("EVRD_LINKS_IMAGE") != "ghcr.io/4evrd/evrd:0.60.0" {
		t.Fatalf("evrd-links: %s, pin %q", fs.Image("evrd-links"), fs.EnvValue("EVRD_LINKS_IMAGE"))
	}
	it := done.Items[0].Stack
	if !slices.Contains(it.Recreated, "evrd") || !slices.Contains(it.Recreated, "webircgateway") || slices.Contains(it.Recreated, "evrd-links") {
		t.Fatalf("recreated %v kept %v", it.Recreated, it.Kept)
	}
	if fs.Image("webircgateway") != harbor+"stack-webircgateway:1.2.0" {
		t.Fatal(fs.Image("webircgateway"))
	}
}

func newServer(t *testing.T, fs *fakestack.Stack) *httptest.Server {
	srv := httptest.NewServer(nil)
	srv.Config.Handler = fs.Handler(srv.URL)
	t.Cleanup(srv.Close)
	return srv
}
