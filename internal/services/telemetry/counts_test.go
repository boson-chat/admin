package telemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/clients/dockerapi"
	"github.com/boson-chat/admin/internal/clients/ircprobe"
	"github.com/boson-chat/admin/internal/clients/ircstats"
)

type fakeCounter struct {
	c     *ircstats.Counts
	err   error
	calls int
}

func (f *fakeCounter) Counts(context.Context) (*ircstats.Counts, error) {
	f.calls++
	return f.c, f.err
}

func authStats(st *authapi.Stats, err error) func(context.Context) (*authapi.Stats, error) {
	return func(context.Context) (*authapi.Stats, error) { return st, err }
}

func str(p *string) string {
	if p == nil {
		return "null"
	}
	return *p
}

// Account counts come from auth's internal stats on every report, with no
// admin visit; an older auth falls back to what a visit read.
func TestAccountCountsFromAuthStats(t *testing.T) {
	stale := &Known{At: now.Add(-30 * 24 * time.Hour), Accounts: ptr[int64](1)} // older than MaxAge
	fresh := &Known{At: now.Add(-time.Hour), Accounts: ptr[int64](3), Active7d: ptr[int64](1), Active30d: ptr[int64](2)}
	for name, tc := range map[string]struct {
		src          func(context.Context) (*authapi.Stats, error)
		known        *Known
		acc, a7, a30 string
	}{
		"internal stats, no visit": {authStats(&authapi.Stats{Accounts: 60, Active7d: 9, Active30d: 20}, nil), nil, "51-100", "2-10", "11-50"},
		"internal stats win":       {authStats(&authapi.Stats{Accounts: 0, Active7d: 0, Active30d: 0}, nil), fresh, "0", "0", "0"},
		"older auth, a visit":      {authStats(nil, apperr.Unsupported("no stats")), fresh, "2-10", "1", "2-10"},
		"older auth, stale visit":  {authStats(nil, apperr.Unsupported("no stats")), stale, "null", "null", "null"},
		"no internal token":        {nil, fresh, "2-10", "1", "2-10"},
		"auth down":                {authStats(nil, errors.New("refused")), nil, "null", "null", "null"},
	} {
		c := &Collector{S: Sources{AuthStats: tc.src}}
		_, r := buildJSON(t, c, Input{InstallID: testID, Reason: ReasonDaily, Now: now, Known: tc.known})
		if got := [3]string{str(r.Counts.Accounts), str(r.Counts.Active7d), str(r.Counts.Active30d)}; got != [3]string{tc.acc, tc.a7, tc.a30} {
			t.Errorf("%s: counts %v", name, got)
		}
	}
}

// Channels and IRC users come from the API of the install's mode: Ergo
// for a fresh install, the bridge for an add-on; the IRC probe (which
// needs guest access) only when the API has no user count.
func TestIRCCountsByMode(t *testing.T) {
	ergoDocker := &fakeDocker{cs: []dockerapi.Container{container("ergo", "harbor.floret.dev/boson/stack-ergo:2.19.1", "running")}}
	bridgeDocker := &fakeDocker{cs: []dockerapi.Container{container("evrd", "ghcr.io/4evrd/evrd:0.63.0", "running"),
		container("evrd-links", "ghcr.io/4evrd/evrd:0.63.0", "running")}}
	ergo := &ircstats.Counts{Users: ptr(30), Channels: ptr(7)}
	bridge := &ircstats.Counts{Users: ptr(600), Channels: ptr(120)}
	noGuests := func(context.Context) *ircprobe.Result { return &ircprobe.Result{OK: true, Version: "UnrealIRCd-6.1.8"} }
	probe := func(context.Context) *ircprobe.Result {
		return &ircprobe.Result{OK: true, Version: "ergo-2.19.1", Users: ptr(4), Channels: ptr(99)}
	}
	for name, tc := range map[string]struct {
		s              Sources
		peak           *int
		users          *int
		ircPeak, chans string
	}{
		"fresh, Ergo API": {Sources{Docker: ergoDocker, IRC: noGuests, Ergo: &fakeCounter{c: ergo}, Bridge: &fakeCounter{err: errors.New("refused")}},
			nil, ptr(30), "11-50", "2-10"},
		"fresh, earlier peak higher": {Sources{Docker: ergoDocker, Ergo: &fakeCounter{c: ergo}}, ptr(70), ptr(30), "51-100", "2-10"},
		"fresh, Ergo API down: probe": {Sources{Docker: ergoDocker, IRC: probe, Ergo: &fakeCounter{err: errors.New("refused")}},
			nil, ptr(4), "2-10", "null"},
		"fresh, Ergo without the patch": {Sources{Docker: ergoDocker, Ergo: &fakeCounter{c: &ircstats.Counts{Users: ptr(1)}}},
			nil, ptr(1), "1", "null"},
		"add-on, bridge API": {Sources{Docker: bridgeDocker, IRC: noGuests, Ergo: &fakeCounter{err: errors.New("refused")}, Bridge: &fakeCounter{c: bridge}},
			nil, ptr(600), "501-1k", "101-500"},
		"add-on, bridge hasn't heard": {Sources{Docker: bridgeDocker, IRC: noGuests, Bridge: &fakeCounter{c: &ircstats.Counts{}}},
			nil, nil, "null", "null"},
		"add-on, older evrd, no guests": {Sources{Docker: bridgeDocker, IRC: noGuests, Bridge: &fakeCounter{err: errors.New("404")}},
			nil, nil, "null", "null"},
		"no Docker: whichever answers": {Sources{Ergo: &fakeCounter{err: errors.New("refused")}, Bridge: &fakeCounter{c: bridge}},
			nil, ptr(600), "501-1k", "101-500"},
	} {
		r, users := (&Collector{S: tc.s}).Build(context.Background(), Input{InstallID: testID, Reason: ReasonDaily, Now: now, IRCPeak: tc.peak})
		if (users == nil) != (tc.users == nil) || (users != nil && *users != *tc.users) {
			t.Errorf("%s: users now %v, want %v", name, users, tc.users)
		}
		if got := [2]string{str(r.Counts.IRCPeak24h), str(r.Counts.Channels)}; got != [2]string{tc.ircPeak, tc.chans} {
			t.Errorf("%s: irc_peak_24h, channels = %v", name, got)
		}
	}
}

func TestUsersNow(t *testing.T) {
	down := &fakeCounter{err: errors.New("refused")}
	ergo := &fakeCounter{c: &ircstats.Counts{Users: ptr(5)}}
	bridge := &fakeCounter{c: &ircstats.Counts{Users: ptr(50)}}
	probe := func(context.Context) *ircprobe.Result { return &ircprobe.Result{Users: ptr(2)} }
	noGuests := func(context.Context) *ircprobe.Result { return &ircprobe.Result{OK: true} }
	for name, tc := range map[string]struct {
		s    Sources
		want *int
	}{
		"Ergo first":       {Sources{Ergo: ergo, Bridge: bridge, IRC: probe}, ptr(5)},
		"then the bridge":  {Sources{Ergo: down, Bridge: bridge, IRC: probe}, ptr(50)},
		"then the probe":   {Sources{Ergo: down, Bridge: down, IRC: probe}, ptr(2)},
		"probe, no guests": {Sources{Ergo: down, IRC: noGuests}, nil},
		"nothing":          {Sources{}, nil},
	} {
		got := (&Collector{S: tc.s}).UsersNow(context.Background())
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

// The sampler for the 24 h peak counts IRC users through the APIs, so the
// peak is known without guest access.
func TestSampleIRCWithoutGuestAccess(t *testing.T) {
	old := IRCSampleEvery
	IRCSampleEvery = 5 * time.Millisecond
	defer func() { IRCSampleEvery = old }()
	c := &clock{t: now}
	ergo := &fakeCounter{c: &ircstats.Counts{Users: ptr(23)}}
	s := newSvc(t, t.TempDir(), c, 0.5, Options{Collector: &Collector{S: Sources{Ergo: ergo,
		IRC: func(context.Context) *ircprobe.Result { return &ircprobe.Result{OK: true} }}}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.sampleIRC(ctx)
	deadline := time.Now().Add(2 * time.Second)
	for s.peak(now) == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if p := s.peak(now); p == nil || *p != 23 {
		t.Fatalf("peak %v", p)
	}
	// The report built now carries it too, and counts as a sample.
	r := s.build(context.Background(), ReasonDaily)
	if str(r.Counts.IRCPeak24h) != "11-50" {
		t.Fatalf("report peak %s", str(r.Counts.IRCPeak24h))
	}
}
