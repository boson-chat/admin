package updates_test

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/clients/dockerapi"
	"github.com/boson-chat/admin/internal/services/updates"
)

// TestRollbackScriptAgainstDocker runs the rollback helper script alone on
// a real Docker host, in the shape of boson.chat's failed rollback
// (2026-11-27): a database (pushrelay) to restore and Postgres among the
// services to stop. Postgres must stay up (or be started) for the restore,
// and when a restore fails the services it stopped must be running again
// afterwards.
//
//	UPDATES_DOCKER_TEST=1 go test -run TestRollbackScriptAgainstDocker ./internal/services/updates/
func TestRollbackScriptAgainstDocker(t *testing.T) {
	if os.Getenv("UPDATES_DOCKER_TEST") == "" {
		t.Skip("set UPDATES_DOCKER_TEST=1 (needs Docker)")
	}
	const project = "bosonrbtest"
	home, _ := os.UserHomeDir()
	dir, err := os.MkdirTemp(home, ".bosonrbtest-")
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
		_ = exec.Command("docker", "run", "--rm", "-v", dir+":/d", "busybox:1.37.0", "rm", "-rf", "/d/backups").Run()
		_ = os.RemoveAll(dir)
	})
	compose := `services:
  postgres:
    image: postgres:18-alpine
    environment: {POSTGRES_USER: auth, POSTGRES_PASSWORD: pw, POSTGRES_DB: auth}
    volumes: [pg_data:/var/lib/postgresql]
    healthcheck: {test: ["CMD", "pg_isready", "-U", "auth"], interval: 1s, retries: 30}
  pushrelay:
    image: busybox:1.37.0
    command: ["sleep", "3600"]
    depends_on: {postgres: {condition: service_healthy}}
  ergo:
    image: busybox:1.37.0
    command: ["sleep", "3600"]
  admin:
    image: busybox:1.37.0
    command: ["sleep", "3600"]
volumes:
  pg_data:
`
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("# test stack\nOTHER=kept\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dc("up", "-d", "--wait")
	psql := func(db, q string) string {
		return dc("exec", "-T", "postgres", "psql", "-U", "auth", "-d", db, "-Atc", q)
	}
	psql("auth", "create database pushrelay")
	psql("pushrelay", "create table schema_migrations (version int primary key); insert into schema_migrations values (1)")
	// The backup: .env and the relay's database before the "update".
	bdir := filepath.Join(dir, "backups", "rb1")
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bdir, "env"), []byte("# test stack\nOTHER=kept\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dump := exec.Command("docker", "compose", "-p", project, "exec", "-T", "postgres", "pg_dump", "-U", "auth", "-Fc", "-d", "pushrelay")
	dump.Dir = dir
	b, err := dump.Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bdir, "pg-pushrelay.dump"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	// The "update": the relay's migration ran.
	psql("pushrelay", "insert into schema_migrations values (2)")
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
	jobs := dockerapi.NewJobsClient(proxy, 3*time.Minute, nil)
	h := &updates.Helper{Client: jobs, Project: project, SelfID: adminID[:12], Image: "docker:29.9.3-cli",
		BackupRoot: filepath.Join(dir, "backups"), Poll: 200 * time.Millisecond}
	ctx := context.Background()
	loc, err := h.Locate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	running := func(svc string) bool {
		return dc("ps", "-q", "--status", "running", svc) != ""
	}

	// 1. Postgres among STOP (what the panel used to pass): it stays up,
	// the database is restored, everything runs after.
	env := updates.RollbackEnv(loc, filepath.Join(dir, "backups"), bdir,
		"STOP", "pushrelay ergo postgres", "RESTORE_DBS", "pushrelay", "SERVICES", "")
	out, err := h.Run(ctx, "rb1", "rollback", updates.ScriptRollback, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Exit != 0 {
		t.Fatalf("rollback exit %d:\n%s", out.Exit, strings.Join(out.Lines, "\n"))
	}
	if got := psql("pushrelay", "select string_agg(version::text, ',' order by version) from schema_migrations"); got != "1" {
		t.Fatalf("pushrelay after the restore: %q", got)
	}
	for _, s := range []string{"postgres", "pushrelay", "ergo"} {
		if !running(s) {
			t.Fatalf("%s isn't running after the rollback:\n%s", s, strings.Join(out.Lines, "\n"))
		}
	}

	// 2. Postgres stopped before the rollback (as boson.chat's was left):
	// started again for the restore.
	dc("stop", "postgres", "pushrelay")
	out, err = h.Run(ctx, "rb2", "rollback", updates.ScriptRollback, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Exit != 0 || !running("postgres") || !running("pushrelay") {
		t.Fatalf("rollback with Postgres stopped: exit %d:\n%s", out.Exit, strings.Join(out.Lines, "\n"))
	}

	// 3. A restore that fails (a broken dump): the job fails, and what it
	// stopped is running again.
	if err := os.WriteFile(filepath.Join(bdir, "pg-pushrelay.dump"), []byte("not a dump"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = h.Run(ctx, "rb3", "rollback", updates.ScriptRollback, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := ""
	for _, f := range out.Tagged("RECOVER") {
		rec = strings.Join(f, " ")
	}
	if out.Exit == 0 || rec != "ok" {
		t.Fatalf("a failed restore: exit %d, recover %q:\n%s", out.Exit, rec, strings.Join(out.Lines, "\n"))
	}
	for _, s := range []string{"postgres", "pushrelay", "ergo"} {
		if !running(s) {
			t.Fatalf("%s is left stopped after the failed rollback:\n%s", s, strings.Join(out.Lines, "\n"))
		}
	}
}
