package updates_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/clients/dockerapi"
	"github.com/boson-chat/admin/internal/fakestack"
	"github.com/boson-chat/admin/internal/services/updates"
)

// The boson.chat update of 2026-11-28 (stack 1.4.0 → 1.5.0): the release
// changed both socket proxies' allowlist, so compose recreated them, and
// for a while after that Docker's embedded DNS had no record for them
// ("lookup docker-proxy on 127.0.0.11:53: server misbehaving"). The run
// was marked failed, its automatic rollback hit the same wall at its first
// step and changed nothing, and minutes later everything was healthy on
// 1.5.0.

type outage struct {
	fs    *fakestack.Stack
	svc   *updates.Service
	fails atomic.Int32 // dials refused by the outage
}

// newOutageHost: the stack host with both proxies in the release (their
// compose config changes, so they are recreated), and Docker clients whose
// dial fails the way Docker's DNS did while the fake is in an outage.
func newOutageHost(t *testing.T, retry, proxyWait time.Duration) *outage {
	t.Helper()
	fs, srv := newStack(t)
	stackHost(t, fs)
	fs.Set(func(s *fakestack.Stack) {
		s.AddService("docker-proxy", "wollomatic/socket-proxy:1.13.2")
		s.AddService("docker-proxy-updates", "wollomatic/socket-proxy:1.13.2")
		r := s.StackReleases[0]
		r.Hashes["docker-proxy"] = "h2-proxy"
		r.Hashes["docker-proxy-updates"] = "h2-proxy-updates"
	})
	o := &outage{fs: fs}
	d := &net.Dialer{Timeout: time.Second}
	tr := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		if fs.ProxyDown() {
			o.fails.Add(1)
			return nil, &net.OpError{Op: "dial", Net: network,
				Err: &net.DNSError{Err: "server misbehaving", Name: "docker-proxy", Server: "127.0.0.11:53"}}
		}
		return d.DialContext(ctx, network, addr)
	}}
	jobs := &dockerapi.Client{BaseURL: srv.URL + "/docker", HTTP: &http.Client{Timeout: 5 * time.Second, Transport: tr},
		MaxLogBytes: 16 << 20, RetryWindow: retry, RetryBackoff: 10 * time.Millisecond}
	o.svc = outageService(t, fs, srv, jobs, proxyWait)
	return o
}

func outageService(t *testing.T, fs *fakestack.Stack, srv *httptest.Server, jobs *dockerapi.Client, proxyWait time.Duration) *updates.Service {
	t.Helper()
	reg := updates.NewOCIRegistry(5 * time.Second)
	reg.Override = srv.URL
	checks := updates.DefaultHTTPChecks()
	for i := range checks {
		checks[i].URL = srv.URL + "/check/" + checks[i].Service
	}
	return updates.New(updates.Options{
		Releases: updates.NewGitHub(srv.URL+"/gh", "", 5*time.Second), Registry: reg, Docker: jobs, Project: "boson",
		SelfVersion: "1.26.0",
		Jobs: &updates.Helper{Client: jobs, Project: "boson", SelfID: fs.AdminID[:12], Image: "docker:29.9.3-cli",
			BackupRoot: "/root/backups/boson", Poll: 5 * time.Millisecond},
		Verifier: &updates.StackVerifier{AuthURL: srv.URL + "/auth", Checks: checks},
		StateDir: t.TempDir(), BackupRoot: "/root/backups/boson", Keep: 3,
		HealthTimeout: 3 * time.Second, VerifyTimeout: time.Second, ProxyWait: proxyWait, RetryCheck: 50 * time.Millisecond,
	})
}

func (o *outage) applyStack(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if c := component(o.svc.Check(ctx), "stack"); !c.Update || !c.Applyable {
		t.Fatalf("stack: %+v", c)
	}
	if _, err := o.svc.Apply(ctx, "alice", updates.ApplyRequest{Mode: "selected", Components: []string{"stack"}}, "tok"); err != nil {
		t.Fatal(err)
	}
}

var finished = []string{updates.StateSucceeded, updates.StateFailed, updates.StateUnverified,
	updates.StateRolledBack, updates.StateRollbackFailed}

// The proxies don't resolve for a moment after compose replaced them:
// the calls retry through it, the update succeeds, nothing rolls back.
func TestStackUpdateOutlastsTheProxiesBeingReplaced(t *testing.T) {
	o := newOutageHost(t, 5*time.Second, 5*time.Second)
	o.fs.Set(func(s *fakestack.Stack) { s.ProxyOutage = 700 * time.Millisecond })
	o.applyStack(t)
	done := waitRun(t, o.svc, finished...)
	if done.Kind != "update" || done.State != updates.StateSucceeded {
		t.Fatalf("%s %s: %s\nsteps %+v\nlog %v", done.Kind, done.State, done.Error, done.Steps, done.Log)
	}
	if o.fails.Load() == 0 {
		t.Fatal("the proxies were never out: the test proves nothing")
	}
	var rec []string
	o.fs.Set(func(s *fakestack.Stack) { rec = append(rec, s.Recreated...) })
	if n := len(rec); n < 3 || rec[n-2] != "docker-proxy" || rec[n-1] != "docker-proxy-updates" {
		t.Fatalf("the proxies are recreated last: %v", rec)
	}
	h := o.svc.Status(context.Background()).History
	if len(h) != 1 || h[0].Kind != "update" || h[0].State != updates.StateSucceeded {
		t.Fatalf("no rollback: %+v", h)
	}
	o.fs.Set(func(s *fakestack.Stack) {
		if len(s.Rollbacks) != 0 || s.StackVersion != "1.1.0" {
			t.Errorf("rollbacks %v, stack %s", s.Rollbacks, s.StackVersion)
		}
	})
}

// The proxies stay out past the retry window: "couldn't verify", the stack
// left as it is, no rollback. A rollback by hand then first checks that
// Docker answers: while it doesn't, it says it didn't run and changes
// nothing; once Docker is back, it rolls back.
func TestStackUpdateProxiesDownPastTheWindow(t *testing.T) {
	o := newOutageHost(t, 200*time.Millisecond, 300*time.Millisecond)
	o.fs.Set(func(s *fakestack.Stack) { s.ProxyOutage = time.Hour })
	o.applyStack(t)
	done := waitRun(t, o.svc, finished...)
	if done.Kind != "update" || done.State != updates.StateUnverified || !done.CanRollback {
		t.Fatalf("%s %s (can roll back %v): %s\nsteps %+v", done.Kind, done.State, done.CanRollback, done.Error, done.Steps)
	}
	if want := "Couldn't verify the update: Docker was unreachable. Nothing was rolled back; the stack is on 1.1.0."; done.Error != want {
		t.Fatalf("error %q\nwant  %q", done.Error, want)
	}
	if st := step(done, "recreate"); st.State != updates.StepFailed || !strings.Contains(st.Detail, "server misbehaving") {
		t.Fatalf("recreate step: %+v", st)
	}
	time.Sleep(300 * time.Millisecond) // an automatic rollback would have started by now
	ctx := context.Background()
	if r := o.svc.Status(ctx).Run; r.Kind != "update" || r.State != updates.StateUnverified {
		t.Fatalf("a rollback started: %+v", r)
	}

	// Rolling back by hand while Docker is still out.
	if _, err := o.svc.Rollback(ctx, "alice", updates.RollbackRequest{RunID: done.ID}, "tok"); err != nil {
		t.Fatal(err)
	}
	rb := waitRun(t, o.svc, updates.StateRolledBack, updates.StateRollbackFailed)
	if want := "Rollback didn't run: Docker was unreachable. Nothing was changed; the stack is on 1.1.0."; rb.Error != want || rb.Changed {
		t.Fatalf("rollback: %q (changed %v)\nwant      %q\nsteps %+v", rb.Error, rb.Changed, want, rb.Steps)
	}
	if st := step(rb, "inspect"); st.State != updates.StepFailed || !strings.Contains(st.Detail, "docker-proxy") {
		t.Fatalf("inspect step: %+v", st)
	}
	o.fs.Set(func(s *fakestack.Stack) {
		if len(s.Rollbacks) != 0 || s.StackVersion != "1.1.0" {
			t.Errorf("nothing changes: rollbacks %v, stack %s", s.Rollbacks, s.StackVersion)
		}
		s.ProxyDownUntil = time.Time{}
	})

	// Docker is back: the same update can still be rolled back.
	if _, err := o.svc.Rollback(ctx, "alice", updates.RollbackRequest{RunID: done.ID}, "tok"); err != nil {
		t.Fatal(err)
	}
	rb = waitRun(t, o.svc, updates.StateRolledBack, updates.StateRollbackFailed)
	if rb.State != updates.StateRolledBack || !rb.Changed {
		t.Fatalf("rollback: %s %s\nsteps %+v\nlog %v", rb.State, rb.Error, rb.Steps, rb.Log)
	}
	if v := o.fs.Image("traefik"); v != "traefik:v3.7.19" {
		t.Fatalf("traefik %s after the rollback", v)
	}
}

// An update that genuinely failed (a migration) rolls back by itself, but
// the proxies are out just then: the rollback waits for them instead of
// failing at its first step, then puts everything back.
func TestAutomaticRollbackWaitsForTheProxies(t *testing.T) {
	o := newOutageHost(t, 200*time.Millisecond, 5*time.Second)
	o.fs.Set(func(s *fakestack.Stack) {
		s.OutageAtFingerprint = 1500 * time.Millisecond
		s.StackReleases[0].FailMigration = true
	})
	o.applyStack(t)
	done := waitRun(t, o.svc, updates.StateRolledBack, updates.StateRollbackFailed)
	if done.Kind != "rollback" || done.Actor != "automatic rollback" || done.State != updates.StateRolledBack {
		t.Fatalf("%s by %s: %s %s\nsteps %+v\nlog %v", done.Kind, done.Actor, done.State, done.Error, done.Steps, done.Log)
	}
	if o.fails.Load() == 0 {
		t.Fatal("the proxies were never out")
	}
}

// Same, with the proxies out for good: the rollback says it didn't run.
func TestAutomaticRollbackDoesntRunWithoutDocker(t *testing.T) {
	o := newOutageHost(t, 100*time.Millisecond, 300*time.Millisecond)
	o.fs.Set(func(s *fakestack.Stack) {
		s.OutageAtFingerprint = time.Hour
		s.StackReleases[0].FailMigration = true
	})
	o.applyStack(t)
	done := waitRun(t, o.svc, updates.StateRolledBack, updates.StateRollbackFailed)
	if done.Kind != "rollback" || done.Changed || !strings.HasPrefix(done.Error, "Rollback didn't run: Docker was unreachable. Nothing was changed;") {
		t.Fatalf("%s %s (changed %v): %q", done.Kind, done.State, done.Changed, done.Error)
	}
	o.fs.Set(func(s *fakestack.Stack) {
		if len(s.Rollbacks) != 0 {
			t.Errorf("rollback jobs ran: %v", s.Rollbacks)
		}
	})
}

// A check that can't reach Docker says so, and runs again soon instead of
// hours later; the next one that can clears the error.
func TestCheckWithoutDockerRetriesSoon(t *testing.T) {
	o := newOutageHost(t, 100*time.Millisecond, time.Second)
	o.fs.Set(func(s *fakestack.Stack) { s.ProxyDownUntil = time.Now().Add(time.Hour) })
	ctx := context.Background()
	st := o.svc.Check(ctx)
	if !strings.Contains(st.Error, "docker_unavailable") || st.NextCheckAt == nil || st.NextCheckAt.Sub(*st.CheckedAt) > time.Second {
		t.Fatalf("error %q, next %v after %v", st.Error, st.NextCheckAt, st.CheckedAt)
	}
	// The loop's first check (5 s after it starts) still can't reach
	// Docker; the next comes RetryCheck later, not an Interval (6 h), and
	// clears the error.
	back := time.Now().Add(5500 * time.Millisecond)
	o.fs.Set(func(s *fakestack.Stack) { s.ProxyDownUntil = back })
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	o.svc.Start(runCtx)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if s := o.svc.Status(ctx); s.Error == "" && s.CheckedAt != nil && s.CheckedAt.After(back) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the error stayed: %q", o.svc.Status(ctx).Error)
}

func step(r *updates.Run, id string) updates.Step {
	for _, s := range r.Steps {
		if s.ID == id {
			return s
		}
	}
	return updates.Step{}
}
