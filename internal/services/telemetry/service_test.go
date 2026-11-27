package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/clients/authapi"
)

// clock is an injectable time.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// fixedRand picks a point in [lo, hi] (0 = lo, 1 = hi).
func fixedRand(f float64) func(lo, hi time.Duration) time.Duration {
	return func(lo, hi time.Duration) time.Duration { return lo + time.Duration(f*float64(hi-lo)) }
}

func newSvc(t *testing.T, dir string, c *clock, f float64, o Options) *Service {
	t.Helper()
	o.Dir, o.Now, o.Rand = dir, c.Now, fixedRand(f)
	return New(o)
}

func TestScheduleNewInstall(t *testing.T) {
	c := &clock{t: now}
	for _, f := range []float64{0, 0.5, 1} {
		s := newSvc(t, t.TempDir(), c, f, Options{})
		d := s.next.Sub(now)
		if s.nextReason != ReasonSetup || d < SetupMin || d > SetupMax {
			t.Fatalf("f=%v: first report in %s (%s), want 5–15 min setup", f, d, s.nextReason)
		}
	}
	if s := newSvc(t, t.TempDir(), c, 0, Options{StartDelay: 2 * time.Second}); s.next.Sub(now) != 2*time.Second {
		t.Fatalf("StartDelay: %s", s.next.Sub(now))
	}
}

func writeState(t *testing.T, dir string, st state) {
	t.Helper()
	b, _ := json.Marshal(st)
	if err := os.WriteFile(filepath.Join(dir, StateFile), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, IDFile), []byte(testID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestScheduleRestart(t *testing.T) {
	c := &clock{t: now}
	// Restarted 3 h after the last send: not before 24 h since it.
	for _, f := range []float64{0, 1} {
		dir := t.TempDir()
		writeState(t, dir, state{LastAttempt: now.Add(-3 * time.Hour), Sends: 4})
		s := newSvc(t, dir, c, f, Options{})
		since := s.next.Sub(now.Add(-3 * time.Hour))
		if s.nextReason != ReasonDaily || since < Day || since > Day+DayJitter {
			t.Fatalf("f=%v: next %s after the last send (%s)", f, since, s.nextReason)
		}
	}
	// Restarted 30 h after: due, 5–15 min after start.
	dir := t.TempDir()
	writeState(t, dir, state{LastAttempt: now.Add(-30 * time.Hour), Sends: 4})
	s := newSvc(t, dir, c, 0.5, Options{})
	if d := s.next.Sub(now); s.nextReason != ReasonDaily || d < SetupMin || d > SetupMax {
		t.Fatalf("overdue: in %s (%s)", d, s.nextReason)
	}
	// The ID file gone: a new install again.
	dir = t.TempDir()
	writeState(t, dir, state{LastAttempt: now.Add(-3 * time.Hour), Sends: 4})
	_ = os.Remove(filepath.Join(dir, IDFile))
	if s := newSvc(t, dir, c, 0, Options{}); s.nextReason != ReasonSetup {
		t.Fatalf("without an ID: %s", s.nextReason)
	}
}

func TestScheduleDailyJitter(t *testing.T) {
	c := &clock{t: now}
	for _, f := range []float64{0, 0.5, 1} {
		dir := t.TempDir()
		writeState(t, dir, state{LastAttempt: now, Sends: 1})
		s := newSvc(t, dir, c, f, Options{})
		next, reason := s.plan(now, false)
		if d := next.Sub(now); reason != ReasonDaily || d < Day-DayJitter || d > Day+DayJitter {
			t.Fatalf("f=%v: daily in %s", f, d)
		}
	}
}

func TestScheduleEvents(t *testing.T) {
	c := &clock{t: now}
	dir := t.TempDir()
	writeState(t, dir, state{LastAttempt: now.Add(-time.Hour), Sends: 2})
	s := newSvc(t, dir, c, 0.5, Options{})
	s.event(ReasonUpdate, now)
	if s.nextReason != ReasonUpdate || s.next != now.Add(AfterUpdate) {
		t.Fatalf("update: %s at %s", s.nextReason, s.next)
	}
	// The automatic rollback right after: one report, "rollback", pushed back.
	s.event(ReasonRollback, now.Add(time.Minute))
	if s.nextReason != ReasonRollback || s.next != now.Add(time.Minute+AfterUpdate) {
		t.Fatalf("rollback: %s at %s", s.nextReason, s.next)
	}
	s.event(ReasonUpdate, now.Add(2*time.Minute))
	if s.nextReason != ReasonRollback {
		t.Fatal("a later update doesn't hide the rollback")
	}
	// A new install's first report stays "setup".
	n := newSvc(t, t.TempDir(), c, 0, Options{})
	n.event(ReasonUpdate, now)
	if n.nextReason != ReasonSetup {
		t.Fatalf("setup: %s", n.nextReason)
	}
}

func TestReplanWhenTurnedOn(t *testing.T) {
	c := &clock{t: now}
	on := false
	dir := t.TempDir()
	writeState(t, dir, state{LastAttempt: now.Add(-40 * time.Hour), Sends: 2})
	s := newSvc(t, dir, c, 0.5, Options{FileOn: func() bool { return on }})
	s.next = now.Add(20 * time.Hour) // off: looked again a day later
	s.replan(now)
	if s.next != now.Add(20*time.Hour) {
		t.Fatal("replanned while off")
	}
	on = true
	s.replan(now)
	if d := s.next.Sub(now); d < SetupMin || d > SetupMax {
		t.Fatalf("turned on, overdue: in %s", d)
	}
}

func TestOptOutPrecedence(t *testing.T) {
	for _, c := range []struct {
		env, file     bool
		enabled, lock bool
	}{
		{env: false, file: true, enabled: true},
		{env: false, file: false, enabled: false},
		{env: true, file: true, enabled: false, lock: true},
		{env: true, file: false, enabled: false, lock: true},
	} {
		file := c.file
		s := New(Options{EnvOff: c.env, FileOn: func() bool { return file }})
		st := s.Status()
		if s.Enabled() != c.enabled || st.Enabled != c.enabled || st.Locked != c.lock || st.FileOn != c.file {
			t.Errorf("env off=%v file on=%v: enabled=%v locked=%v", c.env, c.file, st.Enabled, st.Locked)
		}
		if (st.NextAt != nil) != c.enabled {
			t.Errorf("next_at shown while off")
		}
	}
	if !New(Options{}).Enabled() {
		t.Fatal("default on")
	}
}

// A disabled service's loop never posts, even when the report is due.
func TestNothingSentWhenOff(t *testing.T) {
	var hits int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	for _, o := range []Options{{EnvOff: true}, {FileOn: func() bool { return false }}} {
		o.URL, o.StartDelay = srv.URL+ReportPath, time.Millisecond
		s := New(o)
		ctx, cancel := context.WithCancel(context.Background())
		s.Start(ctx)
		time.Sleep(100 * time.Millisecond)
		cancel()
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 0 {
		t.Fatalf("%d reports sent while off", hits)
	}
}

func TestSendOK(t *testing.T) {
	var got []byte
	var ct string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ReportPath || r.Method != http.MethodPost || r.URL.RawQuery != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("request %s %s", r.Method, r.URL)
		}
		ct = r.Header.Get("Content-Type")
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := &clock{t: now}
	dir := t.TempDir()
	s := newSvc(t, dir, c, 0.5, Options{URL: Endpoint(srv.URL), Collector: &Collector{S: Sources{GOARCH: "arm64"}}})
	res := s.Send(context.Background(), ReasonSetup)
	if !res.OK || res.Status != 202 || ct != "application/json" {
		t.Fatalf("result %+v ct %q", res, ct)
	}
	var r Report
	if err := json.Unmarshal(got, &r); err != nil || r.Reason != ReasonSetup || !validID(r.InstallID) || r.Install.Arch != "arm64" {
		t.Fatalf("body %s (%v)", got, err)
	}
	if err := validate(t, compileSchema(t), got); err != nil {
		t.Fatal(err)
	}
	// The ID is kept, mode 0600, and the next build uses it.
	fi, err := os.Stat(filepath.Join(dir, IDFile))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("id file %v %v", fi, err)
	}
	if st, err := os.Stat(filepath.Join(dir, StateFile)); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("state file %v", err)
	}
	if p := s.Preview(context.Background()); p.InstallID != r.InstallID {
		t.Fatal("preview has another ID")
	}
	st := s.Status()
	if st.Last == nil || !st.Last.OK || st.LastSentAt == nil || !st.LastSentAt.Equal(now) {
		t.Fatalf("status %+v", st)
	}
	// A restart reads it back: same ID, the last send, no "setup".
	s2 := newSvc(t, dir, c, 0.5, Options{})
	if s2.id != r.InstallID || s2.nextReason != ReasonDaily || s2.Status().Last == nil {
		t.Fatalf("after restart: id %s reason %s", s2.id, s2.nextReason)
	}
}

func TestSendFailuresAreIsolated(t *testing.T) {
	c := &clock{t: now}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	s := newSvc(t, t.TempDir(), c, 0.5, Options{URL: Endpoint(srv.URL)})
	if res := s.Send(context.Background(), ReasonDaily); res.OK || res.Status != 500 || res.Error != "" {
		t.Fatalf("500: %+v", res)
	}
	// Then the next send: a day later, not retried now.
	s.fire(context.Background())
	if d := s.next.Sub(c.Now()); d < Day-DayJitter {
		t.Fatalf("retried after %s", d)
	}

	// A closed port: refused, quickly.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	s = newSvc(t, t.TempDir(), c, 0.5, Options{URL: "http://" + addr + ReportPath})
	start := time.Now()
	res := s.Send(context.Background(), ReasonDaily)
	if res.OK || res.Status != 0 || res.Error != "refused" || time.Since(start) > SendTimeout {
		t.Fatalf("closed port: %+v", res)
	}
	if s.Status().Last.Error != "refused" {
		t.Fatal("not in the status")
	}

	// A receiver that never answers: the 10 s timeout (shortened here).
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer hang.Close()
	s = newSvc(t, t.TempDir(), c, 0.5, Options{URL: Endpoint(hang.URL), HTTP: &http.Client{Timeout: 100 * time.Millisecond}})
	if res := s.Send(context.Background(), ReasonDaily); res.Error != "timeout" {
		t.Fatalf("hang: %+v", res)
	}

	// A source that panics: recovered, that value is unknown, the report still goes.
	s = newSvc(t, t.TempDir(), c, 0.5, Options{URL: Endpoint(srv.URL), Collector: &Collector{S: Sources{
		StackVersion: func(context.Context) string { panic("boom") },
	}}})
	if res := s.Send(context.Background(), ReasonDaily); res.Status != 500 {
		t.Fatalf("panicking source: %+v", res)
	}
	if r := s.Preview(context.Background()); r.Stack.Version != Unknown {
		t.Fatalf("stack version %q", r.Stack.Version)
	}
}

// fakeAuth answers the two admin reads the counts need.
type fakeAuth struct {
	authapi.AuthClientImpl
	mu    sync.Mutex
	calls int
	users []authapi.User
	guest *bool
}

func (f *fakeAuth) ListUsers(_ context.Context, at, _, sort string, limit, offset int) (*authapi.UserList, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if at != "tok" {
		return nil, io.EOF
	}
	l := &authapi.UserList{Total: int64(len(f.users)), ActivitySupported: true}
	end := min(offset+limit, len(f.users))
	if offset < end {
		l.Users = f.users[offset:end]
	}
	return l, nil
}

func (f *fakeAuth) GetSettings(context.Context, string) (*authapi.NetworkSettings, error) {
	return &authapi.NetworkSettings{GuestAccess: f.guest}, nil
}

func TestObserveAdminReadsCounts(t *testing.T) {
	c := &clock{t: now}
	var users []authapi.User
	for i := range 250 {
		var at *time.Time
		switch {
		case i < 20:
			at = ptr(now.Add(-time.Duration(i) * time.Hour)) // today
		case i < 150:
			at = ptr(now.Add(-10 * 24 * time.Hour)) // this month
		case i < 200:
			at = ptr(now.Add(-40 * 24 * time.Hour))
		}
		users = append(users, authapi.User{ID: "u", Username: "alice", LastActiveAt: at, ActivityTracked: true})
	}
	fa := &fakeAuth{users: users, guest: ptr(false)}
	s := newSvc(t, t.TempDir(), c, 0.5, Options{Auth: fa})
	tok := func(context.Context) (string, error) { return "tok", nil }
	s.ObserveAdmin(context.Background(), true, true, tok)
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		k, busy := s.st.Known, s.refreshing
		s.mu.Unlock()
		if k != nil && !busy {
			if *k.Accounts != 250 || *k.Active7d != 20 || *k.Active30d != 150 || *k.GuestAccess {
				t.Fatalf("known %+v", k)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no counts")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Not again for 12 h.
	fa.mu.Lock()
	calls := fa.calls
	fa.mu.Unlock()
	s.ObserveAdmin(context.Background(), true, true, tok)
	time.Sleep(50 * time.Millisecond)
	fa.mu.Lock()
	defer fa.mu.Unlock()
	if fa.calls != calls {
		t.Fatal("read again within 12 h")
	}
	r := s.Preview(context.Background())
	if *r.Counts.Accounts != "101-500" || *r.Counts.Active7d != "11-50" || r.Settings.GuestAccess {
		t.Fatalf("preview counts %+v", r.Counts)
	}
}

func TestObserveAdminOffDoesNothing(t *testing.T) {
	fa := &fakeAuth{}
	s := New(Options{Auth: fa, EnvOff: true})
	s.ObserveAdmin(context.Background(), true, true, func(context.Context) (string, error) { return "tok", nil })
	time.Sleep(30 * time.Millisecond)
	if fa.calls != 0 {
		t.Fatal("read counts while off")
	}
}

func TestIRCPeak(t *testing.T) {
	c := &clock{t: now}
	s := newSvc(t, t.TempDir(), c, 0.5, Options{})
	s.addSample(now.Add(-25*time.Hour), 99) // too old
	s.addSample(now.Add(-2*time.Hour), 14)
	s.addSample(now.Add(-time.Hour), 6)
	if p := s.peak(now); p == nil || *p != 14 {
		t.Fatalf("peak %v", p)
	}
	if p := newSvc(t, t.TempDir(), c, 0.5, Options{}).peak(now); p != nil {
		t.Fatal("no samples: nil")
	}
}
