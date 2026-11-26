package updates_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/clients/dockerapi"
	"github.com/boson-chat/admin/internal/services/updates"
)

// TestScriptsAgainstDocker runs the real helper scripts on a real Docker
// host, through wollomatic/socket-proxy with the allowlist the stack's
// updates profile grants: a small compose project (Postgres with a
// migrations table, an "auth" service whose image is AUTH_IMAGE in .env,
// and a stand-in "admin"), updated from busybox:1.36.1 to 1.37.0. The
// update "migrates" the database and fails its checks; the rollback must
// detect the migration, restore the database from the backup, pin the
// previous digest and pass the checks again.
//
//	UPDATES_DOCKER_TEST=1 go test -run TestScriptsAgainstDocker ./internal/services/updates/
//
// Everything it creates is named bosonupdtest* or labelled
// chat.boson.updates and removed at the end.
func TestScriptsAgainstDocker(t *testing.T) {
	if os.Getenv("UPDATES_DOCKER_TEST") == "" {
		t.Skip("set UPDATES_DOCKER_TEST=1 (needs Docker)")
	}
	const project = "bosonupdtest"
	home, _ := os.UserHomeDir()
	// Under the home directory: Docker Desktop shares it with its VM.
	dir, err := os.MkdirTemp(home, ".bosonupdtest-")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	run := func(name string, args ...string) string {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	dc := func(args ...string) string {
		return run("docker", append([]string{"compose", "-p", project}, args...)...)
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "compose", "-p", project, "--project-directory", dir, "down", "-v", "--remove-orphans").Run()
		_ = exec.Command("docker", "rm", "-f", project+"-proxy").Run()
		if ids, _ := exec.Command("docker", "ps", "-aq", "--filter", "label=chat.boson.updates").Output(); len(ids) > 0 {
			_ = exec.Command("docker", append([]string{"rm", "-f"}, strings.Fields(string(ids))...)...).Run()
		}
		// Backups are written as root by the helper: remove them the same way.
		_ = exec.Command("docker", "run", "--rm", "-v", dir+":/d", "busybox:1.37.0", "rm", "-rf", "/d/backups").Run()
		_ = os.RemoveAll(dir)
	})
	compose := `services:
  postgres:
    image: postgres:18-alpine
    environment: {POSTGRES_USER: auth, POSTGRES_PASSWORD: pw, POSTGRES_DB: auth}
    volumes: [pg_data:/var/lib/postgresql]
    healthcheck: {test: ["CMD", "pg_isready", "-U", "auth"], interval: 1s, retries: 30}
  auth:
    image: ${AUTH_IMAGE:-busybox:1.36.1}
    command: ["sleep", "3600"]
    volumes: [auth_data:/data]
    depends_on: {postgres: {condition: service_healthy}}
  admin:
    image: ${ADMIN_IMAGE:-busybox:1.37.0}
    command: ["sleep", "3600"]
volumes:
  pg_data:
  auth_data:
`
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("# test stack\nAUTH_IMAGE=busybox:1.36.1\nOTHER=kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	dc("up", "-d", "--wait")
	psql := func(q string) string {
		return dc("exec", "-T", "postgres", "psql", "-U", "auth", "-d", "auth", "-Atc", q)
	}
	psql("create table schema_migrations (version int primary key); insert into schema_migrations values (1); create table users (name text); insert into users values ('alice')")
	dc("exec", "-T", "auth", "sh", "-c", "echo v1 > /data/state")
	adminID := dc("ps", "-q", "admin")

	// The socket proxy with the stack's updates allowlist.
	run("docker", "run", "-d", "--name", project+"-proxy", "--user", "0:0", "-p", "127.0.0.1::2375",
		"-v", "/var/run/docker.sock:/var/run/docker.sock:ro", "wollomatic/socket-proxy:1.13.2",
		"-listenip=0.0.0.0", "-allowfrom=0.0.0.0/0",
		`-allowGET=(/v1\.[0-9]+)?/(_ping|version|containers/json|containers/[a-zA-Z0-9_.-]+/(json|logs|stats))`,
		`-allowPOST=(/v1\.[0-9]+)?/(containers/[a-zA-Z0-9_.-]+/(start|stop|restart)|containers/create|images/create)`,
		`-allowDELETE=(/v1\.[0-9]+)?/containers/[a-zA-Z0-9_.-]+`)
	port := run("docker", "port", project+"-proxy", "2375/tcp")
	proxy := "http://" + port[strings.LastIndex(port, "\n")+1:]
	for i := 0; i < 50; i++ {
		if r, err := http.Get(proxy + "/_ping"); err == nil {
			r.Body.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"tag_name":"v1.37.0","body":"### Features\n\n* newer"},{"tag_name":"v1.36.1","body":""}]`))
	}))
	defer gh.Close()

	var migrateOnVerify, failChecks atomic.Bool
	migrateOnVerify.Store(true)
	failChecks.Store(true)
	verifier := verifierFunc(func(ctx context.Context, in updates.VerifyInput) []updates.Check {
		if migrateOnVerify.CompareAndSwap(true, false) {
			// What auth 1.37 would do at start: a migration and new data.
			psql("insert into schema_migrations values (2); alter table users add column email text; insert into users values ('bob', 'b@x')")
		}
		if failChecks.Load() {
			return []updates.Check{{ID: "auth-token", Label: "Auth issues a token", Detail: "answered 500"}}
		}
		return []updates.Check{{ID: "auth-token", Label: "Auth issues a token", OK: true}}
	})
	cat := []updates.Component{{ID: "auth", Name: "Auth", Repo: "test/auth", Image: "busybox", EnvVar: "AUTH_IMAGE",
		Services: []string{"auth"}, Mode: updates.ModeImage, Databases: []string{"auth"}, Volumes: []string{"auth_data"},
		Impact: updates.Impact{Restart: true, Downtime: time.Second, Who: "test"}}}
	jobs := dockerapi.NewJobsClient(proxy, 3*time.Minute, nil)
	svc := updates.New(updates.Options{
		Releases: updates.NewGitHub(gh.URL, "", 5*time.Second), Docker: jobs, Project: project,
		Jobs: &updates.Helper{Client: jobs, Project: project, SelfID: adminID[:12], Image: "docker:29.9.3-cli",
			BackupRoot: filepath.Join(dir, "backups"), Poll: 200 * time.Millisecond},
		Verifier: verifier, BackupRoot: filepath.Join(dir, "backups"), Keep: 2, MinFree: 1 << 20,
		Catalog: cat, HealthTimeout: time.Minute, VerifyTimeout: time.Second,
	})
	ctx := context.Background()
	st := svc.Check(ctx)
	if len(st.Components) != 1 || !st.Components[0].Update || st.Components[0].Latest != "1.37.0" {
		t.Fatalf("check: %+v %s", st.Components, st.Error)
	}
	if _, err := svc.Apply(ctx, "tester", updates.ApplyRequest{Mode: "all"}, ""); err != nil {
		t.Fatal(err)
	}
	failed := waitRunLong(t, svc, updates.StateFailed, updates.StateSucceeded)
	if failed.State != updates.StateFailed || !failed.CanRollback {
		t.Fatalf("want a failed update: %s %+v\n%s", failed.Error, failed.Steps, strings.Join(failed.Log, "\n"))
	}
	for _, s := range failed.Steps[:6] {
		if s.State != updates.StepOK {
			t.Fatalf("step %s: %s %s\n%s", s.ID, s.State, s.Detail, strings.Join(failed.Log, "\n"))
		}
	}
	if got := run("docker", "inspect", "-f", "{{.Config.Image}}", dc("ps", "-q", "auth")); got != "busybox:1.37.0" {
		t.Fatalf("auth runs %s after the update", got)
	}
	env, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if !strings.Contains(string(env), "AUTH_IMAGE=busybox:1.37.0\n") || !strings.Contains(string(env), "OTHER=kept") {
		t.Fatalf(".env after the update:\n%s", env)
	}
	if !strings.Contains(strings.Join(failed.MigrationsRan, ","), "auth") {
		t.Fatalf("the migration should be noticed: %v", failed.MigrationsRan)
	}
	b := failed.Backup
	names := map[string]bool{}
	for _, f := range b.Files {
		names[f.Name] = true
	}
	for _, want := range []string{"env", "pg-auth.dump", "pg-globals.sql", "images.txt", "vol-auth_data.tgz", "migrations.txt"} {
		if !names[want] {
			t.Fatalf("backup misses %s: %+v", want, b.Files)
		}
	}
	if !strings.Contains(failed.Items[0].PinnedImage, "busybox:1.36.1@sha256:") {
		t.Fatalf("pin: %q", failed.Items[0].PinnedImage)
	}
	dc("exec", "-T", "auth", "sh", "-c", "echo v2 > /data/state")

	failChecks.Store(false)
	if _, err := svc.Rollback(ctx, "tester", updates.RollbackRequest{RunID: failed.ID}, ""); err != nil {
		t.Fatal(err)
	}
	done := waitRunLong(t, svc, updates.StateRolledBack, updates.StateRollbackFailed)
	if done.State != updates.StateRolledBack {
		t.Fatalf("rollback: %s %+v\n%s", done.Error, done.Steps, strings.Join(done.Log, "\n"))
	}
	if got := psql("select string_agg(version::text, ',' order by version) from schema_migrations"); got != "1" {
		t.Fatalf("migrations after the restore: %q", got)
	}
	if got := psql("select count(*) from information_schema.columns where table_name = 'users' and column_name = 'email'"); got != "0" {
		t.Fatalf("the migrated schema should be gone: %s", got)
	}
	if got := dc("exec", "-T", "auth", "cat", "/data/state"); got != "v1" {
		t.Fatalf("volume after the restore: %q", got)
	}
	if got := run("docker", "inspect", "-f", "{{.Config.Image}}", dc("ps", "-q", "auth")); !strings.HasPrefix(got, "busybox:1.36.1@sha256:") {
		t.Fatalf("auth runs %s after the rollback", got)
	}
	env, _ = os.ReadFile(filepath.Join(dir, ".env"))
	if !strings.Contains(string(env), "AUTH_IMAGE=busybox:1.36.1@sha256:") || !strings.Contains(string(env), "# test stack") {
		t.Fatalf(".env after the rollback:\n%s", env)
	}
	if ids := run("docker", "ps", "-aq", "--filter", "label=chat.boson.updates"); ids != "" {
		t.Fatalf("helper containers left behind: %s", ids)
	}
	fmt.Println("backup:", b.Dir, b.Bytes, "bytes")

	// The self-update helper: to a working image, then to one that can't
	// be pulled (it must put the previous one back).
	h := &updates.Helper{Client: jobs, Project: project, SelfID: adminID[:12], Image: "docker:29.9.3-cli", Poll: 200 * time.Millisecond}
	loc, err := h.Locate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	self := func(runID, target, fallback string) string {
		t.Helper()
		env := updates.SelfEnv(loc, filepath.Join(dir, "backups"), target, fallback, 12)
		if err := h.Start(ctx, runID, "self", updates.ScriptSelf, env); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 300; i++ {
			out, done, err := h.Collect(ctx, runID, "self")
			if err != nil {
				t.Fatal(err)
			}
			if done {
				raw, _ := out.Result("self")
				return string(raw)
			}
			time.Sleep(500 * time.Millisecond)
		}
		t.Fatal("self helper never finished")
		return ""
	}
	if res := self("self1", "ADMIN_IMAGE=busybox:1.36.1", "ADMIN_IMAGE=busybox:1.37.0"); res != `{"ok":true}` {
		t.Fatalf("self update: %s", res)
	}
	if got := run("docker", "inspect", "-f", "{{.Config.Image}}", dc("ps", "-q", "admin")); got != "busybox:1.36.1" {
		t.Fatalf("admin runs %s", got)
	}
	if res := self("self2", "ADMIN_IMAGE=busybox:0.0.0-nope", "ADMIN_IMAGE=busybox:1.36.1"); res != `{"ok":false,"fell_back":true}` {
		t.Fatalf("self update to a broken image: %s", res)
	}
	if got := run("docker", "inspect", "-f", "{{.Config.Image}}", dc("ps", "-q", "admin")); got != "busybox:1.36.1" {
		t.Fatalf("admin should be back on busybox:1.36.1, runs %s", got)
	}
	env, _ = os.ReadFile(filepath.Join(dir, ".env"))
	if !strings.Contains(string(env), "ADMIN_IMAGE=busybox:1.36.1\n") {
		t.Fatalf(".env after the fallback:\n%s", env)
	}
}

type verifierFunc func(ctx context.Context, in updates.VerifyInput) []updates.Check

func (f verifierFunc) Verify(ctx context.Context, in updates.VerifyInput) []updates.Check {
	return f(ctx, in)
}

func waitRunLong(t *testing.T, svc *updates.Service, states ...string) *updates.Run {
	t.Helper()
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		if r := svc.Status(context.Background()).Run; r != nil {
			for _, s := range states {
				if r.State == s {
					return r
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timeout: %+v", svc.Status(context.Background()).Run)
	return nil
}
