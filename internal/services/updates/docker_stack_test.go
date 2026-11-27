package updates_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/clients/dockerapi"
	"github.com/boson-chat/admin/internal/services/updates"
)

// TestStackScriptsAgainstDocker runs a stack release update for real: the
// helper scripts (state, stage, plan, apply, backup, rollback) on a real
// Docker host through the socket proxy with the updates allowlist, and the
// stack repository's own scripts/stack-apply.sh and migrations/lib.sh in
// the bundles. A small compose project goes from release 1.0.0 to 2.0.0
// (one image changes, a mounted file changes, a one-shot service, a
// migration adds a .env key, one service doesn't change and must not be
// recreated, a local edit is kept as .local), then 3.0.0's migration fails
// and the update rolls itself back to 2.0.0.
//
//	UPDATES_DOCKER_TEST=1 STACK_REPO=~/projects/stack go test -run TestStackScriptsAgainstDocker ./internal/services/updates/
func TestStackScriptsAgainstDocker(t *testing.T) {
	repo := os.Getenv("STACK_REPO")
	if os.Getenv("UPDATES_DOCKER_TEST") == "" || repo == "" {
		t.Skip("set UPDATES_DOCKER_TEST=1 and STACK_REPO (a boson-chat/stack checkout; needs Docker)")
	}
	applySh, err := os.ReadFile(filepath.Join(repo, "scripts", "stack-apply.sh"))
	if err != nil {
		t.Fatal(err)
	}
	libSh, err := os.ReadFile(filepath.Join(repo, "migrations", "lib.sh"))
	if err != nil {
		t.Fatal(err)
	}
	const project = "bosonstacktest"
	home, _ := os.UserHomeDir()
	dir, err := os.MkdirTemp(home, ".bosonstacktest-")
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
		_ = exec.Command("docker", "run", "--rm", "-v", dir+":/d", "busybox:1.37.0", "sh", "-c", "rm -rf /d/backups /d/.boson").Run()
		_ = os.RemoveAll(dir)
	})

	compose := func(webImage string) string {
		return `services:
  init:
    image: busybox:1.37.0
    command: ["sh", "-c", "echo init ran > /data/init"]
    volumes: [data:/data]
    restart: "no"
  steady:
    image: busybox:1.37.0
    command: ["sleep", "3600"]
  web:
    image: ${WEB_IMAGE:-` + webImage + `}
    command: ["sleep", "3600"]
  mount:
    image: busybox:1.37.0
    command: ["sh", "/srv/serve.sh"]
    volumes:
      - ./serve.sh:/srv/serve.sh:ro
  admin:
    image: busybox:1.37.0
    command: ["sleep", "3600"]
volumes:
  data:
`
	}
	type bundleT struct {
		version, sha string
		tgz          []byte
		manifest     []byte
	}
	mk := func(version string, files map[string]string, migrations map[string]string) bundleT {
		t.Helper()
		all := map[string]string{"scripts/stack-apply.sh": string(applySh), "migrations/lib.sh": string(libSh), "VERSION": version + "\n"}
		for k, v := range files {
			all[k] = v
		}
		for k, v := range migrations {
			all["migrations/"+k] = v
		}
		var paths []string
		for p := range all {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		var sums strings.Builder
		fileHashes := map[string]string{}
		for _, p := range paths {
			h := sha256.Sum256([]byte(all[p]))
			fileHashes[p] = hex.EncodeToString(h[:])
			fmt.Fprintf(&sums, "%s  %s\n", fileHashes[p], p)
		}
		var migs []string
		for k := range migrations {
			migs = append(migs, k)
		}
		sort.Strings(migs)
		m, _ := json.Marshal(updates.Manifest{Schema: 1, Version: version, Release: true, MinAdmin: "1.0.0", Migrations: migs, Files: fileHashes,
			Services: map[string]updates.ManifestService{
				"init":   {Image: "busybox:1.37.0", Oneshot: true},
				"steady": {Image: "busybox:1.37.0"},
				"web":    {Image: "busybox:" + map[bool]string{true: "1.36.1", false: "1.37.0"}[version == "1.0.0"], Env: "WEB_IMAGE"},
				"mount":  {Image: "busybox:1.37.0", Mounts: []string{"serve.sh"}},
				"admin":  {Image: "busybox:1.37.0"},
			}})
		all[".bundle/files.sha256"] = sums.String()
		all[".bundle/manifest.json"] = string(m)
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		var keys []string
		for p := range all {
			keys = append(keys, p)
		}
		sort.Strings(keys)
		for _, p := range keys {
			mode := int64(0o644)
			if strings.HasSuffix(p, ".sh") {
				mode = 0o755
			}
			_ = tw.WriteHeader(&tar.Header{Name: "./" + p, Mode: mode, Size: int64(len(all[p])), ModTime: time.Now()})
			_, _ = tw.Write([]byte(all[p]))
		}
		_ = tw.Close()
		_ = gz.Close()
		h := sha256.Sum256(buf.Bytes())
		return bundleT{version: version, sha: hex.EncodeToString(h[:]), tgz: buf.Bytes(), manifest: m}
	}
	serve := "echo serving v1; sleep 3600\n"
	v1 := mk("1.0.0", map[string]string{"compose.yaml": compose("busybox:1.36.1"), "serve.sh": serve, "notes.txt": "one\n"}, nil)
	addKey := "#!/bin/sh\n# Adds NEW_KEY to .env.\nset -eu\n. \"$MIGRATIONS_LIB\"\nenv_set_default NEW_KEY \"$(gen_secret 8)\"\nsay \"NEW_KEY added\"\n"
	v2 := mk("2.0.0", map[string]string{"compose.yaml": compose("busybox:1.37.0"), "serve.sh": "echo serving v2; sleep 3600\n", "notes.txt": "two\n"},
		map[string]string{"0001-new-key.sh": addKey})
	v3 := mk("3.0.0", map[string]string{"compose.yaml": compose("busybox:1.37.0") + "# 3.0.0\n", "serve.sh": "echo serving v3; sleep 3600\n", "notes.txt": "two\n", "added.txt": "new in 3\n"},
		map[string]string{"0001-new-key.sh": addKey, "0002-broken.sh": "#!/bin/sh\n# Always fails.\nexit 1\n"})

	// 1.0.0 installed: unpacked, stack-apply.sh files, then up.
	inst := filepath.Join(dir, "v1")
	if err := os.MkdirAll(inst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "v1.tgz"), v1.tgz, 0o644); err != nil {
		t.Fatal(err)
	}
	run("tar", "xzf", "v1.tgz", "-C", "v1")
	cmd := exec.Command("sh", filepath.Join(inst, "scripts", "stack-apply.sh"), "files", inst)
	cmd.Env = append(os.Environ(), "STACK_DIR="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("installing 1.0.0: %v\n%s", err, out)
	}
	_ = os.RemoveAll(inst)
	_ = os.Remove(filepath.Join(dir, "v1.tgz"))
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("# test stack\nOTHER=kept\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A local edit of a bundle file: replaced by 2.0.0, kept as .local.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("one, edited here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dc("up", "-d")
	steadyID := dc("ps", "-q", "steady")
	mountID := dc("ps", "-q", "mount")
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

	// GitHub: the releases and their assets. The stage job downloads the
	// bundle from here (host.docker.internal: the helper runs in Docker).
	bundles := map[string]bundleT{"2.0.0": v2}
	var ghURL string
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			v, name, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/assets/"), "/")
			b, ok := bundles[v]
			switch {
			case !ok:
				http.NotFound(w, r)
			case name == "manifest.json":
				_, _ = w.Write(b.manifest)
			case strings.HasSuffix(name, ".sha256"):
				fmt.Fprintf(w, "%s  boson-stack-%s.tar.gz\n", b.sha, v)
			default:
				_, _ = w.Write(b.tgz)
			}
			return
		}
		var rels []map[string]any
		for _, v := range []string{"3.0.0", "2.0.0", "1.0.0"} {
			if _, ok := bundles[v]; !ok && v != "1.0.0" {
				continue
			}
			var assets []map[string]string
			for _, n := range []string{"boson-stack-" + v + ".tar.gz", "boson-stack-" + v + ".tar.gz.sha256", "manifest.json"} {
				assets = append(assets, map[string]string{"name": n, "browser_download_url": ghURL + "/assets/" + v + "/" + n})
			}
			rels = append(rels, map[string]any{"tag_name": "v" + v, "body": "## " + v, "assets": assets})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rels)
	}))
	defer gh.Close()
	ghURL = strings.Replace(gh.URL, "127.0.0.1", "host.docker.internal", 1)
	// The panel reaches the fake GitHub on 127.0.0.1, the helper through host.docker.internal.
	jobs := dockerapi.NewJobsClient(proxy, 3*time.Minute, nil)
	cat := []updates.Component{{ID: "stack", Name: "Stack", Repo: "boson-chat/stack", Mode: updates.ModeStack,
		Impact: updates.Impact{Restart: true, Downtime: time.Second, Who: "test"}}}
	ghc := updates.NewGitHub(gh.URL, "", 5*time.Second)
	ghc.MinAge = 0
	svc := updates.New(updates.Options{
		Releases: hostRewrite{ghc}, Docker: jobs, Project: project, SelfVersion: "1.30.0",
		Jobs: &updates.Helper{Client: jobs, Project: project, SelfID: adminID[:12], Image: "docker:29.9.3-cli",
			BackupRoot: filepath.Join(dir, "backups"), Poll: 200 * time.Millisecond},
		BackupRoot: filepath.Join(dir, "backups"), Keep: 3, MinFree: 1 << 20,
		Catalog: cat, HealthTimeout: time.Minute, VerifyTimeout: time.Second,
	})
	ctx := context.Background()

	// ---- 1.0.0 → 2.0.0 ----------------------------------------------------------
	st := svc.Check(ctx)
	c := st.Components[0]
	if !c.Update || !c.Applyable || c.Installed != "1.0.0" || c.Latest != "2.0.0" || c.Stack == nil {
		t.Fatalf("check: %+v / %s / %+v", c, c.Why, c.Stack)
	}
	ss := c.Stack
	actions := map[string]string{}
	for _, f := range ss.Files {
		actions[f.Path] = f.Action
	}
	if actions["compose.yaml"] != "update" || actions["serve.sh"] != "update" || actions["notes.txt"] != "local" {
		t.Fatalf("files: %+v", ss.Files)
	}
	if strings.Join(ss.Restarts, ",") != "mount,web" || strings.Join(ss.Oneshots, ",") != "" {
		t.Fatalf("restarts %v oneshots %v (%+v)", ss.Restarts, ss.Oneshots, ss)
	}
	if len(ss.Migrations) != 1 || ss.Migrations[0].Name != "0001-new-key.sh" || ss.Migrations[0].About != "Adds NEW_KEY to .env." {
		t.Fatalf("migrations: %+v", ss.Migrations)
	}
	if _, err := svc.Apply(ctx, "tester", updates.ApplyRequest{Mode: "all", AcknowledgeBreaking: true}, ""); err != nil {
		t.Fatal(err)
	}
	done := waitRunLong(t, svc, updates.StateSucceeded, updates.StateFailed, updates.StateRolledBack, updates.StateRollbackFailed)
	if done.State != updates.StateSucceeded {
		t.Fatalf("update: %s %+v\n%s", done.Error, done.Steps, strings.Join(done.Log, "\n"))
	}
	if v, _ := os.ReadFile(filepath.Join(dir, "VERSION")); strings.TrimSpace(string(v)) != "2.0.0" {
		t.Fatalf("VERSION %q", v)
	}
	if got := run("docker", "inspect", "-f", "{{.Config.Image}}", dc("ps", "-q", "web")); got != "busybox:1.37.0" {
		t.Fatalf("web runs %s", got)
	}
	if dc("ps", "-q", "steady") != steadyID {
		t.Fatal("steady didn't change and must not be recreated")
	}
	if dc("ps", "-q", "mount") == mountID || !strings.Contains(dc("logs", "mount"), "serving v2") {
		t.Fatal("mount bind-mounts serve.sh, which changed: recreated")
	}
	if dc("ps", "-q", "admin") != adminID {
		t.Fatal("the panel is only recreated through its hand-off")
	}
	env, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if !strings.Contains(string(env), "OTHER=kept") || !strings.Contains(string(env), "\nNEW_KEY=") {
		t.Fatalf(".env: %s", env)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "notes.txt.local")); string(b) != "one, edited here\n" {
		t.Fatalf("the local edit isn't kept: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".boson", "migrations.applied")); !strings.HasPrefix(string(b), "0001-new-key.sh ") {
		t.Fatalf("migrations.applied: %q", b)
	}

	// ---- 2.0.0 → 3.0.0: the migration fails, the update rolls back ------------
	bundles["3.0.0"] = v3
	for i := 0; i < 100; i++ {
		if c := svc.Check(ctx).Components[0]; c.Applyable && c.Latest == "3.0.0" && c.Installed == "2.0.0" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := svc.Apply(ctx, "tester", updates.ApplyRequest{Mode: "all", AcknowledgeBreaking: true}, ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	rb := waitRunLong(t, svc, updates.StateRolledBack, updates.StateRollbackFailed)
	if rb.State != updates.StateRolledBack || rb.Kind != "rollback" {
		t.Fatalf("rollback: %s %+v\n%s", rb.Error, rb.Steps, strings.Join(rb.Log, "\n"))
	}
	if v, _ := os.ReadFile(filepath.Join(dir, "VERSION")); strings.TrimSpace(string(v)) != "2.0.0" {
		t.Fatalf("VERSION after the rollback %q", v)
	}
	if _, err := os.Stat(filepath.Join(dir, "added.txt")); err == nil {
		t.Fatal("a file the failed update added is removed again")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "compose.yaml")); strings.Contains(string(b), "# 3.0.0") {
		t.Fatal("compose.yaml is 2.0.0's again")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".boson", "migrations.applied")); strings.Contains(string(b), "0002") {
		t.Fatalf("migrations.applied: %q", b)
	}
	if !strings.Contains(dc("logs", "mount"), "serving v2") {
		t.Fatal("mount runs 2.0.0's file again")
	}
}

// hostRewrite: release asset URLs the helper (in Docker) can reach.
type hostRewrite struct{ *updates.GitHub }

func (h hostRewrite) Releases(ctx context.Context, repo string) ([]updates.Release, error) {
	rs, err := h.GitHub.Releases(ctx, repo)
	return rs, err
}

func (h hostRewrite) Asset(ctx context.Context, url string, max int64) ([]byte, error) {
	return h.GitHub.Asset(ctx, strings.Replace(url, "host.docker.internal", "127.0.0.1", 1), max)
}
