package updates_test

import (
	"context"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/clients/dockerapi"
	"github.com/boson-chat/admin/internal/fakestack"
	"github.com/boson-chat/admin/internal/services/updates"
)

const harbor = "harbor.floret.dev/boson/"

// newStack is a host running auth 1.20.0, api 0.57.0, web 0.100.0, voice
// 1.15.0, Ergo 2.19.1 and admin 1.26.0, with releases on the fake GitHub:
// auth four versions ahead, the api one breaking minor ahead, the web
// client and the panel one ahead, nothing new for voice.
func newStack(t *testing.T) (*fakestack.Stack, *httptest.Server) {
	t.Helper()
	fs := fakestack.New(map[string]string{
		"auth": harbor + "auth:1.20.0", "api": harbor + "backend:0.57.0", "web": harbor + "web:0.100.0",
		"quicvoiced": harbor + "quicvoice:1.15.0", "ergo": harbor + "stack-ergo:2.19.1", "admin": harbor + "admin:1.26.0",
		"postgres": "postgres:18-alpine",
	})
	fs.Env = map[string]string{"AUTH_IMAGE": harbor + "auth:1.20.0", "API_IMAGE": harbor + "backend:0.57.0",
		"WEB_IMAGE": harbor + "web:0.100.0", "QUICVOICE_IMAGE": harbor + "quicvoice:1.15.0", "ADMIN_IMAGE": harbor + "admin:1.26.0"}
	rel := func(tag, body string) fakestack.Release {
		return fakestack.Release{Tag: tag, Name: tag, Body: body, At: "2026-11-20T10:00:00Z"}
	}
	fs.Releases = map[string][]fakestack.Release{
		"boson-chat/auth": {
			rel("v1.22.0", "## 1.22.0\n\n### Features\n\n* send users back to the app after email verification"),
			rel("v1.21.1", "## 1.21.1\n\n### Bug Fixes\n\n* refresh race"),
			rel("v1.21.0", "## 1.21.0\n\n### Features\n\n* role channels"),
			rel("v1.20.1", "## 1.20.1\n\n### Bug Fixes\n\n* typo"),
			rel("v1.20.0", "## 1.20.0"), rel("v1.19.0", "## 1.19.0"),
		},
		"boson-chat/backend": {
			rel("v0.58.0", "## 0.58.0\n\n### ⚠ BREAKING CHANGES\n\n* nick claims need NICK_CLAIM_DOMAIN set\n\n### Features\n\n* claims"),
			rel("v0.57.0", "## 0.57.0"),
		},
		"boson-chat/boson":     {rel("v0.101.0", "## 0.101.0\n\n* video tiles"), rel("v0.100.0", "")},
		"boson-chat/quicvoice": {rel("v1.15.0", "")},
		"ergochat/ergo":        {rel("v2.19.1", "")},
		"boson-chat/admin":     {rel("v1.27.0", "## 1.27.0\n\n* Updates page"), rel("v1.26.0", "")},
	}
	for _, tag := range []string{"auth:1.20.1", "auth:1.21.0", "auth:1.21.1", "auth:1.22.0", "backend:0.58.0", "web:0.101.0", "admin:1.27.0"} {
		fs.Tags["boson/"+tag] = true
	}
	srv := httptest.NewServer(nil)
	srv.Config.Handler = fs.Handler(srv.URL)
	t.Cleanup(srv.Close)
	return fs, srv
}

func newService(t *testing.T, fs *fakestack.Stack, srv *httptest.Server, version, stateDir string) *updates.Service {
	t.Helper()
	reg := updates.NewOCIRegistry(5 * time.Second)
	reg.Override = srv.URL
	gh := updates.NewGitHub(srv.URL+"/gh", "", 5*time.Second)
	checks := updates.DefaultHTTPChecks()
	for i := range checks {
		checks[i].URL = srv.URL + "/check/" + checks[i].Service
	}
	jobs := dockerapi.NewJobsClient(srv.URL+"/docker", 5*time.Second, nil)
	return updates.New(updates.Options{
		Releases: gh, Registry: reg, Docker: jobs, Project: "boson", SelfVersion: version,
		Jobs:     &updates.Helper{Client: jobs, Project: "boson", SelfID: fs.AdminID[:12], Image: "docker:29.9.3-cli", BackupRoot: "/root/backups/boson", Poll: 5 * time.Millisecond},
		Verifier: &updates.StackVerifier{AuthURL: srv.URL + "/auth", Checks: checks},
		StateDir: stateDir, BackupRoot: "/root/backups/boson", Keep: 3,
		HealthTimeout: 3 * time.Second, VerifyTimeout: time.Second,
	})
}

func component(st *updates.Status, id string) updates.ComponentStatus {
	for _, c := range st.Components {
		if c.ID == id {
			return c
		}
	}
	return updates.ComponentStatus{}
}

func waitRun(t *testing.T, svc *updates.Service, states ...string) *updates.Run {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if r := svc.Status(context.Background()).Run; r != nil && slices.Contains(states, r.State) {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	r := svc.Status(context.Background()).Run
	t.Fatalf("run never reached %v: %+v", states, r)
	return nil
}

// Update available → changelog across several versions → apply → health → done.
func TestUpdateAvailableChangelogApplyHealthy(t *testing.T) {
	fs, srv := newStack(t)
	ctx := context.Background()
	svc := newService(t, fs, srv, "1.26.0", t.TempDir())
	st := svc.Check(ctx)

	if st.Available != 4 { // auth, api, web, admin
		t.Fatalf("available = %d, want 4: %+v", st.Available, st.Components)
	}
	auth := component(st, "auth")
	if !auth.Update || auth.Installed != "1.20.0" || auth.Latest != "1.22.0" {
		t.Fatalf("auth: %+v", auth)
	}
	var got []string
	for _, r := range auth.Releases {
		got = append(got, r.Version)
	}
	if strings.Join(got, ",") != "1.22.0,1.21.1,1.21.0,1.20.1" {
		t.Fatalf("auth changelog range = %v, want the four releases newest first", got)
	}
	if auth.Breaking {
		t.Fatal("auth isn't breaking")
	}
	api := component(st, "api")
	if !api.Breaking || len(api.BreakingNotes) == 0 || !strings.Contains(api.BreakingNotes[0], "NICK_CLAIM_DOMAIN") {
		t.Fatalf("api should be breaking with its note: %+v", api)
	}
	if v := component(st, "voice"); v.Update || v.Installed != "1.15.0" {
		t.Fatalf("voice: %+v", v)
	}
	if e := component(st, "ergo"); e.ID != "" {
		t.Fatalf("ergo comes with the stack, no row of its own: %+v", e)
	}
	if w := component(st, "webircgateway"); w.ID != "" {
		t.Fatalf("the web IRC gateway comes with the stack: %+v", w)
	}
	if c := component(st, "evrd"); c.ID != "" {
		t.Fatal("evrd isn't installed: not listed")
	}

	// A breaking release needs the acknowledgement.
	if _, err := svc.Apply(ctx, "alice", updates.ApplyRequest{Mode: "selected", Components: []string{"api"}}, "tok"); err == nil {
		t.Fatal("applying a breaking release without acknowledging it must fail")
	}

	run, err := svc.Apply(ctx, "alice", updates.ApplyRequest{Mode: "selected", Components: []string{"auth", "web"}}, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(run.Summary, "Nobody is disconnected") {
		t.Fatalf("summary = %q", run.Summary)
	}
	done := waitRun(t, svc, updates.StateSucceeded, updates.StateFailed)
	if done.State != updates.StateSucceeded {
		t.Fatalf("run failed: %s\nsteps: %+v\nlog: %v", done.Error, done.Steps, done.Log)
	}
	if fs.Image("auth") != harbor+"auth:1.22.0" || fs.Image("web") != harbor+"web:0.101.0" {
		t.Fatalf("images now: auth %s web %s", fs.Image("auth"), fs.Image("web"))
	}
	if fs.Image("api") != harbor+"backend:0.57.0" || fs.Image("admin") != harbor+"admin:1.26.0" {
		t.Fatal("unselected components must not change")
	}
	if fs.EnvValue("AUTH_IMAGE") != harbor+"auth:1.22.0" {
		t.Fatalf(".env AUTH_IMAGE = %s", fs.EnvValue("AUTH_IMAGE"))
	}
	for _, s := range done.Steps {
		if s.State != updates.StepOK {
			t.Fatalf("step %s = %s (%s)", s.ID, s.State, s.Detail)
		}
	}
	if done.Backup == nil || done.Backup.Bytes == 0 || !strings.HasPrefix(done.Backup.Dir, "/root/backups/boson/") {
		t.Fatalf("backup: %+v", done.Backup)
	}
	if !strings.Contains(done.Items[0].PinnedImage, "@sha256:") {
		t.Fatalf("rollback pin should be a digest: %+v", done.Items[0])
	}
	var checkIDs []string
	for _, c := range done.Checks {
		if !c.OK {
			t.Fatalf("check %s failed: %s", c.ID, c.Detail)
		}
		checkIDs = append(checkIDs, c.ID)
	}
	for _, want := range []string{"auth-token", "web", "version-auth", "version-web"} {
		if !slices.Contains(checkIDs, want) {
			t.Fatalf("checks %v miss %s", checkIDs, want)
		}
	}
	if len(done.MigrationsRan) != 0 {
		t.Fatalf("no migrations ran: %v", done.MigrationsRan)
	}
	if j := fs.Jobs(); len(j) != 0 {
		t.Fatalf("helper containers left behind: %v", j)
	}
	h := svc.Status(ctx).History
	if len(h) != 1 || h[0].Actor != "alice" || h[0].State != updates.StateSucceeded {
		t.Fatalf("history: %+v", h)
	}
	// The next check sees the new versions.
	if a := component(svc.Check(ctx), "auth"); a.Update || a.Installed != "1.22.0" {
		t.Fatalf("after the update: %+v", a)
	}
}

// A failing upgrade that ran a migration → rollback restores the digest,
// .env and the migrated database → the same checks pass again.
func TestFailedUpgradeWithMigrationRollsBack(t *testing.T) {
	fs, srv := newStack(t)
	ctx := context.Background()
	fs.Set(func(s *fakestack.Stack) {
		s.Broken[harbor+"backend:0.58.0"] = true
		s.Migrates[harbor+"backend:0.58.0"] = "api"
	})
	svc := newService(t, fs, srv, "1.26.0", t.TempDir())
	svc.Check(ctx)
	if _, err := svc.Apply(ctx, "bob", updates.ApplyRequest{Mode: "selected", Components: []string{"api"}, AcknowledgeBreaking: true}, "tok"); err != nil {
		t.Fatal(err)
	}
	failed := waitRun(t, svc, updates.StateSucceeded, updates.StateFailed)
	if failed.State != updates.StateFailed || !failed.CanRollback || !failed.Changed {
		t.Fatalf("want a failed run that can roll back: %+v", failed)
	}
	if s := failed.Steps; s[5].ID != "health" || s[5].State != updates.StepFailed || s[6].State != updates.StepSkipped {
		t.Fatalf("health should fail, verify be skipped: %+v", s)
	}
	if fs.Image("api") != harbor+"backend:0.58.0" {
		t.Fatal("the broken version should be what runs before the rollback")
	}

	rb, err := svc.Rollback(ctx, "bob", updates.RollbackRequest{RunID: failed.ID}, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if rb.Kind != "rollback" || rb.RollbackOf != failed.ID {
		t.Fatalf("rollback run: %+v", rb)
	}
	done := waitRun(t, svc, updates.StateRolledBack, updates.StateRollbackFailed)
	if done.State != updates.StateRolledBack {
		t.Fatalf("rollback failed: %s\n%+v\n%v", done.Error, done.Steps, done.Log)
	}
	if !slices.Equal(done.MigrationsRan, []string{"api"}) || !done.RestoreData {
		t.Fatalf("the api migration should be detected and the data restored: %+v", done)
	}
	if !slices.Equal(fs.Restored(), []string{"api"}) {
		t.Fatalf("restored databases = %v, want [api]", fs.Restored())
	}
	if img := fs.Image("api"); !strings.HasPrefix(img, harbor+"backend:0.57.0@sha256:") {
		t.Fatalf("api should run the exact previous digest, runs %s", img)
	}
	for _, c := range done.Checks {
		if !c.OK {
			t.Fatalf("check after rollback %s: %s", c.ID, c.Detail)
		}
	}
	if !slices.ContainsFunc(done.Checks, func(c updates.Check) bool { return c.ID == "version-api" && strings.Contains(c.Detail, "0.57.0") }) {
		t.Fatalf("versions should match the previous ones: %+v", done.Checks)
	}
	h := svc.Status(ctx).History
	if len(h) != 2 || h[0].Kind != "rollback" || h[1].State != updates.StateRolledBack {
		t.Fatalf("history: %+v", h)
	}
	if _, err := svc.Rollback(ctx, "bob", updates.RollbackRequest{RunID: failed.ID}, ""); err == nil {
		t.Fatal("a rolled back run can't be rolled back twice")
	}
}

// A rollback after a failure without migrations leaves the data alone.
func TestRollbackWithoutMigrationKeepsData(t *testing.T) {
	fs, srv := newStack(t)
	ctx := context.Background()
	fs.Set(func(s *fakestack.Stack) { s.Broken[harbor+"web:0.101.0"] = true })
	svc := newService(t, fs, srv, "1.26.0", "")
	svc.Check(ctx)
	if _, err := svc.Apply(ctx, "bob", updates.ApplyRequest{Mode: "quiet", AcknowledgeBreaking: true}, ""); err != nil {
		t.Fatal(err)
	}
	failed := waitRun(t, svc, updates.StateSucceeded, updates.StateFailed)
	if failed.State != updates.StateFailed {
		t.Fatalf("%+v", failed)
	}
	// quiet = auth, api, web (none disruptive); admin needs a state dir.
	var ids []string
	for _, it := range failed.Items {
		ids = append(ids, it.Component)
	}
	if strings.Join(ids, ",") != "auth,api,web" {
		t.Fatalf("quiet plan = %v", ids)
	}
	if _, err := svc.Rollback(ctx, "bob", updates.RollbackRequest{RunID: failed.ID}, ""); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, svc, updates.StateRolledBack, updates.StateRollbackFailed)
	if done.State != updates.StateRolledBack || done.RestoreData || len(fs.Restored()) != 0 {
		t.Fatalf("no data restore expected: %+v restored=%v", done, fs.Restored())
	}
	if !strings.HasPrefix(fs.Image("web"), harbor+"web:0.100.0") || !strings.HasPrefix(fs.Image("auth"), harbor+"auth:1.20.0") {
		t.Fatalf("images after rollback: web %s auth %s", fs.Image("web"), fs.Image("auth"))
	}
	// Without a token the auth check still passes on the signing keys.
	for _, c := range done.Checks {
		if c.ID == "auth-token" && (!c.OK || !strings.Contains(c.Detail, "signing key")) {
			t.Fatalf("auth check without a token: %+v", c)
		}
	}
}

// Not enough disk for the backup: refused, nothing changed.
func TestBackupRefusedWhenDiskIsFull(t *testing.T) {
	fs, srv := newStack(t)
	ctx := context.Background()
	fs.Set(func(s *fakestack.Stack) { s.FreeKB = 1024 })
	svc := newService(t, fs, srv, "1.26.0", "")
	svc.Check(ctx)
	if _, err := svc.Apply(ctx, "carol", updates.ApplyRequest{Mode: "selected", Components: []string{"auth"}}, ""); err != nil {
		t.Fatal(err)
	}
	r := waitRun(t, svc, updates.StateFailed, updates.StateSucceeded)
	if r.State != updates.StateFailed || r.Changed || r.CanRollback || !strings.Contains(r.Error, "not enough disk space") {
		t.Fatalf("%+v", r)
	}
	if fs.EnvValue("AUTH_IMAGE") != harbor+"auth:1.20.0" || fs.Image("auth") != harbor+"auth:1.20.0" {
		t.Fatal("nothing may change when the backup is refused")
	}
}

// The panel updates itself: it hands off to a detached helper, the old
// process goes away, the new one picks the run up and checks its version.
func TestSelfUpdateHandsOff(t *testing.T) {
	fs, srv := newStack(t)
	dir := t.TempDir()
	fs.Set(func(s *fakestack.Stack) { s.HoldSelf = true })
	ctx1, stop1 := context.WithCancel(context.Background())
	old := newService(t, fs, srv, "1.26.0", dir)
	old.Check(ctx1)
	if _, err := old.Apply(ctx1, "alice", updates.ApplyRequest{Mode: "selected", Components: []string{"admin", "web"}}, ""); err != nil {
		t.Fatal(err)
	}
	r := waitRun(t, old, updates.StateRestarting, updates.StateFailed)
	if r.State != updates.StateRestarting || r.Handoff != "self" {
		t.Fatalf("%+v", r)
	}
	if fs.Image("web") != harbor+"web:0.101.0" {
		t.Fatal("the other components are updated before the hand-off")
	}
	for i := 0; i < 500 && !slices.ContainsFunc(fs.Jobs(), func(n string) bool { return strings.HasSuffix(n, "-self") }); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	stop1() // the old panel is replaced
	time.Sleep(50 * time.Millisecond)
	fs.ReleaseSelf()

	next := newService(t, fs, srv, "1.27.0", dir)
	next.Start(context.Background())
	done := waitRun(t, next, updates.StateSucceeded, updates.StateFailed)
	if done.State != updates.StateSucceeded {
		var rq []string
		for _, r := range fs.Requests {
			if strings.Contains(r, "containers") && !strings.Contains(r, "/json") {
				rq = append(rq, r)
			}
		}
		t.Fatalf("%s %+v\n%v", done.Error, done.Steps, rq)
	}
	if fs.Image("admin") != harbor+"admin:1.27.0" {
		t.Fatalf("admin runs %s", fs.Image("admin"))
	}
	if !slices.ContainsFunc(done.Checks, func(c updates.Check) bool { return c.ID == "version-admin" && c.OK }) {
		t.Fatalf("the new panel checks its own version: %+v", done.Checks)
	}
}

// A new panel that doesn't stay up is put back by the helper.
func TestSelfUpdateFallsBack(t *testing.T) {
	fs, srv := newStack(t)
	fs.Set(func(s *fakestack.Stack) { s.Broken[harbor+"admin:1.27.0"] = true })
	svc := newService(t, fs, srv, "1.26.0", t.TempDir())
	svc.Check(context.Background())
	if _, err := svc.Apply(context.Background(), "alice", updates.ApplyRequest{Mode: "selected", Components: []string{"admin"}}, ""); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, svc, updates.StateSucceeded, updates.StateFailed)
	if done.State != updates.StateFailed || !strings.Contains(done.Error, "previous version was put back") {
		t.Fatalf("%+v", done)
	}
	if !strings.HasPrefix(fs.Image("admin"), harbor+"admin:1.26.0") {
		t.Fatalf("admin runs %s", fs.Image("admin"))
	}
}

// GitHub's rate limit: the last answer is kept and the status says why.
func TestRateLimitKeepsCache(t *testing.T) {
	fs, srv := newStack(t)
	ctx := context.Background()
	gh := updates.NewGitHub(srv.URL+"/gh", "", 5*time.Second)
	gh.MinAge = 0
	if _, err := gh.Releases(ctx, "boson-chat/auth"); err != nil {
		t.Fatal(err)
	}
	calls := fs.GitHubCalls()
	// The second ask is conditional (ETag) and answered 304.
	rs, err := gh.Releases(ctx, "boson-chat/auth")
	if err != nil || len(rs) != 6 || fs.GitHubCalls() != calls+1 {
		t.Fatalf("conditional request: %d releases, err %v", len(rs), err)
	}
	fs.Set(func(s *fakestack.Stack) { s.RateLimited = true })
	rs, err = gh.Releases(ctx, "boson-chat/auth")
	var rl updates.ErrRateLimited
	if len(rs) != 6 || err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("rate limited: %d releases, err %v (%T)", len(rs), err, rl)
	}
	if l := gh.Limit(); l == nil || l.Remaining != 0 {
		t.Fatalf("limit: %+v", l)
	}
	// While limited, nothing is sent.
	before := len(fs.Requests)
	_, _ = gh.Releases(ctx, "boson-chat/backend")
	if len(fs.Requests) != before {
		t.Fatal("no requests while the limit is used up")
	}
}

// An image not pushed yet isn't offered; the release is listed as pending.
func TestUnpublishedImageIsPending(t *testing.T) {
	fs, srv := newStack(t)
	fs.Set(func(s *fakestack.Stack) { delete(s.Tags, "boson/auth:1.22.0") })
	svc := newService(t, fs, srv, "1.26.0", "")
	a := component(svc.Check(context.Background()), "auth")
	if a.Latest != "1.21.1" || !slices.Equal(a.Pending, []string{"1.22.0"}) || len(a.Releases) != 3 {
		t.Fatalf("%+v", a)
	}
}

// The containers were created with a compose.override.yaml that has since
// been deleted: their labels still name it. The update must leave it out
// (compose refuses a COMPOSE_FILE naming a missing file) and say so; the
// recreated containers then carry the files that exist.
func TestDeletedOverrideFileIsLeftOut(t *testing.T) {
	fs, srv := newStack(t)
	ctx := context.Background()
	fs.SetConfigFiles("/srv/boson/compose.yaml,/srv/boson/compose.override.yaml")
	svc := newService(t, fs, srv, "1.26.0", t.TempDir())
	svc.Check(ctx)
	if _, err := svc.Apply(ctx, "alice", updates.ApplyRequest{Mode: "selected", Components: []string{"auth"}}, ""); err != nil {
		t.Fatal(err)
	}
	r := waitRun(t, svc, updates.StateSucceeded, updates.StateFailed)
	if r.State != updates.StateSucceeded {
		t.Fatalf("run failed: %s\nsteps: %+v", r.Error, r.Steps)
	}
	if !slices.Equal(r.Location.ComposeFiles, []string{"/srv/boson/compose.yaml"}) ||
		!slices.Equal(r.Location.MissingFiles, []string{"/srv/boson/compose.override.yaml"}) {
		t.Fatalf("location: %+v", r.Location)
	}
	if d := r.Steps[0].Detail; r.Steps[0].ID != "preflight" || !strings.Contains(d, "left out compose.override.yaml") {
		t.Fatalf("preflight detail should name the missing file: %+v", r.Steps[0])
	}
	if got := fs.Labels("auth")["com.docker.compose.project.config_files"]; got != "/srv/boson/compose.yaml" {
		t.Fatalf("recreated auth labelled %q", got)
	}
	if j := fs.Jobs(); len(j) != 0 {
		t.Fatalf("helper containers left behind: %v", j)
	}
}

// An override added after the containers were created is picked up, as a
// plain `docker compose up` would.
func TestNewOverrideFileIsPickedUp(t *testing.T) {
	fs, srv := newStack(t)
	ctx := context.Background()
	fs.Set(func(s *fakestack.Stack) { s.Files["/srv/boson/compose.override.yaml"] = true })
	svc := newService(t, fs, srv, "1.26.0", t.TempDir())
	svc.Check(ctx)
	if _, err := svc.Apply(ctx, "alice", updates.ApplyRequest{Mode: "selected", Components: []string{"auth"}}, ""); err != nil {
		t.Fatal(err)
	}
	r := waitRun(t, svc, updates.StateSucceeded, updates.StateFailed)
	if r.State != updates.StateSucceeded || !slices.Equal(r.Location.ComposeFiles, []string{"/srv/boson/compose.yaml", "/srv/boson/compose.override.yaml"}) {
		t.Fatalf("%s %+v", r.State, r.Location)
	}
}

// compose.yaml itself is gone: the update fails at preflight, clearly, with
// nothing changed.
func TestMissingComposeFileFailsPreflight(t *testing.T) {
	fs, srv := newStack(t)
	ctx := context.Background()
	fs.Set(func(s *fakestack.Stack) { delete(s.Files, "/srv/boson/compose.yaml") })
	svc := newService(t, fs, srv, "1.26.0", t.TempDir())
	svc.Check(ctx)
	if _, err := svc.Apply(ctx, "alice", updates.ApplyRequest{Mode: "selected", Components: []string{"auth"}}, ""); err != nil {
		t.Fatal(err)
	}
	r := waitRun(t, svc, updates.StateSucceeded, updates.StateFailed)
	if r.State != updates.StateFailed || r.Changed || !strings.Contains(r.Error, "compose file /srv/boson/compose.yaml is missing") {
		t.Fatalf("%s %q", r.State, r.Error)
	}
	if fs.Image("auth") != harbor+"auth:1.20.0" {
		t.Fatal("nothing may change")
	}
}

// Two updates, then a rollback of the first: refused, its backup's .env is
// from before the second too (it would undo that one while its records
// stay). The newest one rolls back; then the first can.
func TestRollbackOfAnOlderUpdateIsRefused(t *testing.T) {
	fs, srv := newStack(t)
	ctx := context.Background()
	svc := newService(t, fs, srv, "1.26.0", "")
	svc.Check(ctx)
	apply := func(comp string) *updates.Run {
		t.Helper()
		if _, err := svc.Apply(ctx, "bob", updates.ApplyRequest{Mode: "selected", Components: []string{comp}}, ""); err != nil {
			t.Fatal(err)
		}
		r := waitRun(t, svc, updates.StateSucceeded, updates.StateFailed)
		if r.State != updates.StateSucceeded || !r.CanRollback {
			t.Fatalf("%s update: %+v", comp, r)
		}
		return r
	}
	first := apply("auth")
	second := apply("web")
	if _, err := svc.Rollback(ctx, "bob", updates.RollbackRequest{RunID: first.ID}, ""); err == nil || !strings.Contains(err.Error(), "later update") {
		t.Fatalf("rollback of the older update: err = %v, want refused", err)
	}
	if _, err := svc.Rollback(ctx, "bob", updates.RollbackRequest{RunID: second.ID}, ""); err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, svc, updates.StateRolledBack, updates.StateRollbackFailed); done.State != updates.StateRolledBack {
		t.Fatalf("%+v", done)
	}
	if _, err := svc.Rollback(ctx, "bob", updates.RollbackRequest{RunID: first.ID}, ""); err != nil {
		t.Fatalf("the first update after the second was rolled back: %v", err)
	}
	if done := waitRun(t, svc, updates.StateRolledBack, updates.StateRollbackFailed); done.State != updates.StateRolledBack {
		t.Fatalf("%+v", done)
	}
}
