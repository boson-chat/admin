package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// receiver answers each report with the next status of codes (the last
// one from then on).
func receiver(t *testing.T, codes ...int) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		i := min(int(n.Add(1))-1, len(codes)-1)
		w.WriteHeader(codes[i])
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

// Failed on the way (5xx, network, TLS): retried at ~15 min, 30 min, 1 h,
// 2 h, … with the same reason, until the delay reaches the daily cadence;
// a success goes back to daily.
func TestRetrySchedule(t *testing.T) {
	srv, hits := receiver(t, 503, 503, 503, 503, 503, 503, 503, 503, 202)
	c := &clock{t: now}
	dir := t.TempDir()
	s := newSvc(t, dir, c, 0.5, Options{URL: Endpoint(srv.URL)}) // 0.5: no jitter
	want := []time.Duration{15 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 16 * time.Hour}
	s.mu.Lock()
	s.nextReason = ReasonSetup
	s.mu.Unlock()
	for i, w := range want {
		s.fire(context.Background())
		if d := s.next.Sub(c.Now()); d != w || s.nextReason != ReasonSetup {
			t.Fatalf("failure %d: next in %s (%s), want %s (setup)", i+1, d, s.nextReason, w)
		}
		c.Add(w)
	}
	// The 8th failure: the backoff reached a day, the normal cadence.
	s.fire(context.Background())
	if d := s.next.Sub(c.Now()); d != Day || s.nextReason != ReasonSetup {
		t.Fatalf("after 8 failures: %s %s", d, s.nextReason)
	}
	c.Add(Day)
	s.fire(context.Background()) // 202
	if d := s.next.Sub(c.Now()); d != Day || s.nextReason != ReasonDaily || s.st.Failures != 0 || s.st.RetryReason != "" {
		t.Fatalf("after success: %s %s %+v", d, s.nextReason, s.st)
	}
	if hits.Load() != 9 {
		t.Fatalf("%d sends", hits.Load())
	}
}

// A 4xx (the receiver refused the report) waits for the next daily send.
func TestRefusedReportWaitsADay(t *testing.T) {
	srv, _ := receiver(t, 400)
	c := &clock{t: now}
	s := newSvc(t, t.TempDir(), c, 0.5, Options{URL: Endpoint(srv.URL)})
	s.mu.Lock()
	s.nextReason = ReasonUpdate
	s.mu.Unlock()
	s.fire(context.Background())
	if d := s.next.Sub(c.Now()); d != Day || s.nextReason != ReasonDaily || s.st.Failures != 0 {
		t.Fatalf("400: next in %s (%s), failures %d", d, s.nextReason, s.st.Failures)
	}
}

func TestRetryJitterAndFloor(t *testing.T) {
	c := &clock{t: now}
	for f, want := range map[float64]time.Duration{0: 12 * time.Minute, 1: 18 * time.Minute} {
		s := newSvc(t, t.TempDir(), c, f, Options{})
		s.st = state{Sends: 1, LastAttempt: now, Failures: 1, RetryReason: ReasonDaily}
		if next, _ := s.plan(now, false); next.Sub(now) != want {
			t.Errorf("rand %v: %s, want %s", f, next.Sub(now), want)
		}
	}
	// Whatever the jitter, never sooner than RetryMin.
	s := newSvc(t, t.TempDir(), c, 0, Options{})
	s.o.Rand = func(lo, _ time.Duration) time.Duration { return lo * 10 }
	s.st = state{Sends: 1, LastAttempt: now, Failures: 1, RetryReason: ReasonDaily}
	if next, _ := s.plan(now, false); next.Sub(now) != RetryMin {
		t.Errorf("floor: %s", next.Sub(now))
	}
}

// The failures survive a restart (telemetry.json): a retry that was due
// goes 5–15 min after the start (never under RetryMin), one that isn't
// keeps its time, both with the failed send's reason.
func TestRetrySurvivesRestart(t *testing.T) {
	srv, _ := receiver(t, 503)
	c := &clock{t: now}
	dir := t.TempDir()
	s := newSvc(t, dir, c, 0.5, Options{URL: Endpoint(srv.URL)})
	s.Send(context.Background(), ReasonSetup) // the first report, tls/5xx: failed
	s.Send(context.Background(), ReasonSetup)

	c.Add(10 * time.Minute) // the 2nd retry is due 30 min after the last attempt
	s2 := newSvc(t, dir, c, 0.5, Options{})
	if d := s2.next.Sub(c.Now()); d != 20*time.Minute || s2.nextReason != ReasonSetup {
		t.Fatalf("restart before due: %s %s", d, s2.nextReason)
	}
	c.Add(3 * time.Hour)
	s3 := newSvc(t, dir, c, 0.5, Options{})
	if d := s3.next.Sub(c.Now()); d != RetryMin || s3.nextReason != ReasonSetup {
		t.Fatalf("restart when overdue: %s %s", d, s3.nextReason)
	}
}

func TestRetryable(t *testing.T) {
	for r, want := range map[Result]bool{
		{OK: true, Status: 202}: false, {Status: 500}: true, {Status: 503}: true, {Status: 400}: false,
		{Status: 413}: false, {Status: 429}: false, {Error: "tls"}: true, {Error: "timeout"}: true,
		{Error: "dns"}: true, {Error: "refused"}: true, {Error: "network"}: true, {Error: "build"}: false,
		{Error: "too_large"}: false,
	} {
		if got := retryable(r); got != want {
			t.Errorf("retryable(%+v) = %v", r, got)
		}
	}
}

// TELEMETRY_URL= (empty, as compose passes it by default) is the default
// receiver.
func TestEndpointDefault(t *testing.T) {
	for in, want := range map[string]string{
		"": DefaultBaseURL + ReportPath, "  ": DefaultBaseURL + ReportPath,
		"https://t.example/": "https://t.example/v1/report", "http://127.0.0.1:9": "http://127.0.0.1:9/v1/report",
	} {
		if got := Endpoint(in); got != want {
			t.Errorf("Endpoint(%q) = %q, want %q", in, got, want)
		}
	}
	if s := New(Options{}); s.o.URL != DefaultBaseURL+ReportPath {
		t.Errorf("no URL: %s", s.o.URL)
	}
}
