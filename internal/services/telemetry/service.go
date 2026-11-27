// Package telemetry is the admin panel's install report: the stack's
// anonymous usage and health stats (docs/telemetry.md in the stack,
// schema 1). Once a day at a random time (24 h ± 1 h), 5–15 minutes after
// the panel first starts on a new install and about 5 minutes after every
// update or rollback, it POSTs a small JSON report of versions, profiles,
// bucketed counts, which features are used and how healthy the install is
// to https://telemetry.boson.chat/v1/report. Nothing in it names a person,
// a server or a network.
//
// A send that fails is never retried before the next scheduled one, never
// slows anything else and is only logged at debug level. TELEMETRY=off in
// the env, or admin.telemetry: false in config/boson.yaml, turns it off.
package telemetry

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/logger"
)

// DefaultBaseURL is where reports go (TELEMETRY_URL overrides it).
const DefaultBaseURL = "https://telemetry.boson.chat"

// ReportPath is appended to the base URL.
const ReportPath = "/v1/report"

// MaxBytes is the receiver's size cap for a report.
const MaxBytes = 16 << 10

// Files in Dir.
const (
	IDFile    = "telemetry-id"
	StateFile = "telemetry.json"
)

// Timing (schema 1).
const (
	Day         = 24 * time.Hour
	DayJitter   = time.Hour
	SetupMin    = 5 * time.Minute
	SetupMax    = 15 * time.Minute
	AfterUpdate = 5 * time.Minute
	SendTimeout = 10 * time.Second
	// BuildTimeout bounds collecting a report (auth, Docker, VictoriaMetrics…).
	BuildTimeout = 30 * time.Second
	// IRCSampleEvery: how often the IRC user count is sampled for the 24 h peak.
	IRCSampleEvery = 15 * time.Minute
	// KnownRefresh: how often an admin's visit refreshes the account counts.
	KnownRefresh = 12 * time.Hour
)

// Off reports whether an env value turns the report off.
func Off(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "false", "0", "no", "disabled":
		return true
	}
	return false
}

// Endpoint is base (TELEMETRY_URL, default DefaultBaseURL) + /v1/report.
func Endpoint(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = DefaultBaseURL
	}
	return base + ReportPath
}

// Options configure the service.
type Options struct {
	// Dir keeps the install ID and the send state (the panel's data
	// volume, /data). Empty = in memory only (a new ID every start).
	Dir string
	// URL is the full report endpoint (Endpoint()).
	URL string
	// EnvOff: TELEMETRY=off. Wins over the config file.
	EnvOff bool
	// FileOn is admin.telemetry (live; nil = on).
	FileOn    func() bool
	Collector *Collector
	// Auth reads the account counts with a signed-in admin's token.
	Auth authapi.AuthClientImpl
	HTTP *http.Client
	Now  func() time.Time
	// Rand returns a uniform duration in [lo, hi] (tests fix it).
	Rand func(lo, hi time.Duration) time.Duration
	// StartDelay overrides the first send's 5–15 min on a new install and
	// after a restart that is due (e2e tests).
	StartDelay time.Duration
}

// Result is the outcome of the last send.
type Result struct {
	At     time.Time `json:"at"`
	Reason string    `json:"reason"`
	// OK: the receiver answered 2xx.
	OK bool `json:"ok"`
	// Status is the HTTP status when there was an answer.
	Status int `json:"status,omitempty"`
	// Error is a class, never a message: timeout, dns, refused, tls,
	// network, too_large, build.
	Error string `json:"error,omitempty"`
}

type ircSample struct {
	T time.Time `json:"t"`
	N int       `json:"n"`
}

// state is StateFile.
type state struct {
	LastAttempt time.Time   `json:"last_attempt,omitzero"`
	Last        *Result     `json:"last,omitempty"`
	Sends       int         `json:"sends"`
	Known       *Known      `json:"known,omitempty"`
	IRC         []ircSample `json:"irc,omitempty"`
}

// Status is GET /api/telemetry.
type Status struct {
	// Enabled: reports are sent (neither the env nor the file turned it off).
	Enabled bool `json:"enabled"`
	// Locked: TELEMETRY=off in the panel's env (.env) turns it off; the
	// switch can't change that.
	Locked bool `json:"locked"`
	// FileOn is admin.telemetry as the config file has it (default on).
	FileOn     bool       `json:"file_on"`
	Endpoint   string     `json:"endpoint"`
	LastSentAt *time.Time `json:"last_sent_at,omitempty"`
	Last       *Result    `json:"last,omitempty"`
	NextAt     *time.Time `json:"next_at,omitempty"`
	NextReason string     `json:"next_reason,omitempty"`
}

// Service schedules and sends the report.
type Service struct {
	o Options

	mu         sync.Mutex
	st         state
	id         string
	next       time.Time
	nextReason string
	refreshing bool
	sending    bool
	// loadedSends is the state file's count (used when the ID file is there).
	loadedSends int

	kick   chan struct{}
	events chan string
}

// New loads the state; Start runs the schedule.
func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Rand == nil {
		o.Rand = randDuration
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: SendTimeout}
	}
	if o.URL == "" {
		o.URL = Endpoint("")
	}
	if o.Collector == nil {
		o.Collector = &Collector{}
	}
	s := &Service{o: o, kick: make(chan struct{}, 1), events: make(chan string, 8)}
	s.load()
	s.next, s.nextReason = s.plan(o.Now(), true)
	return s
}

func randDuration(lo, hi time.Duration) time.Duration {
	if hi <= lo {
		return lo
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(hi-lo)+1))
	if err != nil {
		return lo
	}
	return lo + time.Duration(n.Int64())
}

// Enabled: neither TELEMETRY=off nor admin.telemetry: false.
func (s *Service) Enabled() bool {
	return !s.o.EnvOff && s.fileOn()
}

func (s *Service) fileOn() bool { return s.o.FileOn == nil || s.o.FileOn() }

// Locked: TELEMETRY=off in the env.
func (s *Service) Locked() bool { return s.o.EnvOff }

func (s *Service) path(name string) string {
	if s.o.Dir == "" {
		return ""
	}
	return filepath.Join(s.o.Dir, name)
}

func (s *Service) load() {
	if p := s.path(StateFile); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			_ = json.Unmarshal(b, &s.st)
		}
	}
	s.loadedSends = s.st.Sends
	// No (valid) ID file: a new install as far as the stats go, so the
	// first report is "setup". Deleting the file makes a new ID.
	s.st.Sends = 0
	if p := s.path(IDFile); p != "" {
		if b, err := os.ReadFile(p); err == nil && validID(strings.TrimSpace(string(b))) {
			s.id = strings.TrimSpace(string(b))
			s.reloadSends()
		}
	}
}

func (s *Service) reloadSends() { s.st.Sends = s.loadedSends }

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, r := range id {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// saveLocked writes the state (mode 0600, atomic).
func (s *Service) saveLocked() {
	p := s.path(StateFile)
	if p == "" {
		return
	}
	b, err := json.Marshal(s.st)
	if err != nil {
		return
	}
	_ = writeFile(p, b)
}

func writeFile(p string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// installID returns the install ID, making it (128 random bits) and
// keeping it in Dir the first time.
func (s *Service) installIDLocked() string {
	if s.id != "" {
		return s.id
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	s.id = hex.EncodeToString(b)
	if p := s.path(IDFile); p != "" {
		_ = writeFile(p, []byte(s.id+"\n"))
	}
	return s.id
}

// plan is when the next report goes, and why. start: right after the
// panel started (a restart sends only when a day has passed).
func (s *Service) plan(now time.Time, start bool) (time.Time, string) {
	first := func() time.Duration {
		if s.o.StartDelay > 0 {
			return s.o.StartDelay
		}
		return s.o.Rand(SetupMin, SetupMax)
	}
	if s.st.Sends == 0 {
		return now.Add(first()), ReasonSetup
	}
	last := s.st.LastAttempt
	if !start {
		return last.Add(Day + s.o.Rand(-DayJitter, DayJitter)), ReasonDaily
	}
	if now.Sub(last) >= Day {
		return now.Add(first()), ReasonDaily
	}
	return last.Add(Day + s.o.Rand(0, DayJitter)), ReasonDaily
}

// Notify is told when an update or a rollback finished ("update",
// "rollback"): a report goes about 5 minutes later.
func (s *Service) Notify(kind string) {
	reason := ReasonUpdate
	if kind == "rollback" {
		reason = ReasonRollback
	}
	select {
	case s.events <- reason:
	default:
	}
}

// Kick re-plans after the switch changed.
func (s *Service) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// rank orders reasons when several are pending: the first report of a
// new install stays "setup".
func rank(r string) int {
	switch r {
	case ReasonSetup:
		return 3
	case ReasonRollback:
		return 2
	case ReasonUpdate:
		return 1
	}
	return 0
}

// Start runs the schedule and the IRC sampling until ctx ends.
func (s *Service) Start(ctx context.Context) {
	go s.loop(ctx)
	go s.sampleIRC(ctx)
}

func (s *Service) loop(ctx context.Context) {
	for {
		s.mu.Lock()
		wait := max(s.next.Sub(s.o.Now()), 0)
		s.mu.Unlock()
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case reason := <-s.events:
			t.Stop()
			s.mu.Lock()
			s.event(reason, s.o.Now())
			s.mu.Unlock()
		case <-s.kick:
			t.Stop()
			s.mu.Lock()
			s.replan(s.o.Now())
			s.mu.Unlock()
		case <-t.C:
			s.fire(ctx)
		}
	}
}

// event: an update or rollback finished. The report goes AfterUpdate
// from now; a later event pushes it back, so an update and its automatic
// rollback make one report ("rollback").
func (s *Service) event(reason string, now time.Time) {
	if rank(reason) > rank(s.nextReason) {
		s.nextReason = reason
	}
	s.next = now.Add(AfterUpdate)
}

// replan after the switch was turned on: a report that is overdue (or the
// first one) goes in 5–15 minutes instead of a day later.
func (s *Service) replan(now time.Time) {
	if !s.Enabled() {
		return
	}
	overdue := s.st.Sends == 0 || now.Sub(s.st.LastAttempt) >= Day
	if overdue && s.next.Sub(now) > SetupMax {
		s.next = now.Add(s.o.Rand(SetupMin, SetupMax))
	}
}

// fire sends the due report (when on) and plans the next one.
func (s *Service) fire(ctx context.Context) {
	s.mu.Lock()
	reason := s.nextReason
	s.mu.Unlock()
	if s.Enabled() {
		s.Send(ctx, reason)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.Sends == 0 || !s.Enabled() {
		// Off: look again in a day (turning it on kicks the loop).
		s.next = s.o.Now().Add(Day + s.o.Rand(-DayJitter, DayJitter))
		if s.st.Sends > 0 {
			s.nextReason = ReasonDaily
		}
		return
	}
	s.next, s.nextReason = s.plan(s.o.Now(), false)
}

// Send builds and posts one report now. It never panics and never
// returns an error: the outcome is in Status.
func (s *Service) Send(ctx context.Context, reason string) (res Result) {
	log := logger.FromCtx(ctx)
	now := s.o.Now().UTC()
	res = Result{At: now, Reason: reason}
	defer func() {
		if p := recover(); p != nil {
			log.Debug().Interface("panic", p).Msg("usage & health stats: sending the report panicked")
			res.OK, res.Error = false, "build"
		}
		s.mu.Lock()
		s.st.LastAttempt, s.st.Last = now, &res
		s.st.Sends++
		s.sending = false
		s.saveLocked()
		s.mu.Unlock()
	}()
	s.mu.Lock()
	s.sending = true
	s.mu.Unlock()
	r := s.build(ctx, reason)
	body, err := json.Marshal(r)
	if err != nil {
		res.Error = "build"
		return res
	}
	if len(body) > MaxBytes {
		res.Error = "too_large"
		log.Debug().Int("bytes", len(body)).Msg("usage & health stats: the report is over 16 KiB, not sent")
		return res
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), SendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(sctx, http.MethodPost, s.o.URL, bytes.NewReader(body))
	if err != nil {
		res.Error = "network"
		return res
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "boson-admin")
	resp, err := s.o.HTTP.Do(req)
	if err != nil {
		res.Error = netClass(err)
		log.Debug().Str("error", res.Error).Msg("usage & health stats: the report wasn't sent")
		return res
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	_ = resp.Body.Close()
	res.Status = resp.StatusCode
	res.OK = resp.StatusCode >= 200 && resp.StatusCode < 300
	if !res.OK {
		log.Debug().Int("status", resp.StatusCode).Msg("usage & health stats: the receiver refused the report")
	}
	return res
}

// netClass names a transport error without its text (it can hold hostnames).
func netClass(err error) string {
	var dnsErr *net.DNSError
	var netErr net.Error
	var tlsErr *tls.CertificateVerificationError
	var recErr tls.RecordHeaderError
	switch {
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		return "timeout"
	case errors.As(err, &dnsErr):
		return "dns"
	case errors.As(err, &tlsErr) || errors.As(err, &recErr):
		return "tls"
	case strings.Contains(err.Error(), "connection refused"):
		return "refused"
	}
	return "network"
}

// build collects the report now.
func (s *Service) build(ctx context.Context, reason string) *Report {
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), BuildTimeout)
	defer cancel()
	now := s.o.Now()
	s.mu.Lock()
	id := s.installIDLocked()
	known := s.st.Known
	s.mu.Unlock()
	r, irc := s.o.Collector.Build(bctx, Input{InstallID: id, Reason: reason, Now: now, Known: known, IRCPeak: s.peak(now)})
	if irc != nil && irc.Users != nil {
		// This probe is a sample too.
		s.addSample(now, *irc.Users)
		r.Counts.IRCPeak24h = bucketOrNil(s.peak(now))
	}
	return r
}

func bucketOrNil(n *int) *string {
	if n == nil {
		return nil
	}
	return bucketPtr(int64(*n))
}

// Preview is the next report exactly as it would be sent now.
func (s *Service) Preview(ctx context.Context) *Report {
	s.mu.Lock()
	reason := s.nextReason
	s.mu.Unlock()
	return s.build(ctx, reason)
}

// Status is the switch, the last send and the next one.
func (s *Service) Status() *Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := &Status{Enabled: s.Enabled(), Locked: s.Locked(), FileOn: s.fileOn(), Endpoint: s.o.URL}
	if s.st.Last != nil {
		l := *s.st.Last
		st.Last = &l
		at := l.At
		st.LastSentAt = &at
	}
	if st.Enabled {
		n := s.next.UTC()
		st.NextAt, st.NextReason = &n, s.nextReason
	}
	return st
}

// ---- IRC users: the 24 h peak ----

func (s *Service) addSample(now time.Time, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keep := s.st.IRC[:0]
	for _, x := range s.st.IRC {
		if now.Sub(x.T) <= Day {
			keep = append(keep, x)
		}
	}
	s.st.IRC = append(keep, ircSample{T: now.UTC(), N: n})
	s.saveLocked()
}

// peak is the most IRC users sampled in the last 24 h (nil: none).
func (s *Service) peak(now time.Time) *int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var best *int
	for _, x := range s.st.IRC {
		if now.Sub(x.T) <= Day && (best == nil || x.N > *best) {
			n := x.N
			best = &n
		}
	}
	return best
}

func (s *Service) sampleIRC(ctx context.Context) {
	irc := s.o.Collector.S.IRC
	if irc == nil {
		return
	}
	t := time.NewTicker(IRCSampleEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !s.Enabled() {
			continue
		}
		func() {
			defer func() { _ = recover() }()
			pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if r := irc(pctx); r != nil && r.Users != nil {
				s.addSample(s.o.Now(), *r.Users)
			}
		}()
	}
}

// ---- account counts from an admin's token ----

// ObserveAdmin is called on a signed-in admin's requests: at most every
// KnownRefresh it reads the account counts (users.view) and guest access
// (settings.manage) from auth with that admin's token, in the background.
// Only the counts are kept, bucketed when a report is built.
func (s *Service) ObserveAdmin(ctx context.Context, usersView, settingsManage bool, token func(context.Context) (string, error)) {
	if s == nil || s.o.Auth == nil || !s.Enabled() || (!usersView && !settingsManage) {
		return
	}
	now := s.o.Now()
	s.mu.Lock()
	if s.refreshing || (s.st.Known != nil && now.Sub(s.st.Known.At) < KnownRefresh) {
		s.mu.Unlock()
		return
	}
	s.refreshing = true
	s.mu.Unlock()
	at, err := token(ctx)
	if err != nil || at == "" {
		s.mu.Lock()
		s.refreshing = false
		s.mu.Unlock()
		return
	}
	go func() {
		defer func() {
			_ = recover()
			s.mu.Lock()
			s.refreshing = false
			s.mu.Unlock()
		}()
		bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		k := s.readKnown(bctx, at, usersView, settingsManage, now)
		if k == nil {
			return
		}
		s.mu.Lock()
		if old := s.st.Known; old != nil { // keep what this admin couldn't read
			if k.Accounts == nil {
				k.Accounts = old.Accounts
			}
			if k.Active7d == nil {
				k.Active7d, k.Active30d = old.Active7d, old.Active30d
			}
			if k.GuestAccess == nil {
				k.GuestAccess = old.GuestAccess
			}
		}
		s.st.Known = k
		s.saveLocked()
		s.mu.Unlock()
	}()
}

func (s *Service) readKnown(ctx context.Context, at string, usersView, settingsManage bool, now time.Time) *Known {
	k := &Known{At: now.UTC()}
	got := false
	if usersView {
		if l, err := s.o.Auth.ListUsers(ctx, at, "", "", 1, 0); err == nil {
			total := l.Total
			k.Accounts = &total
			got = true
		}
		var a7, a30 int64
		supported := false
	walk:
		for offset := 0; offset < bucketCap; offset += 100 {
			l, err := s.o.Auth.ListUsers(ctx, at, "", authapi.SortLastActive, 100, offset)
			if err != nil {
				supported = false
				break
			}
			if l.ActivitySupported {
				supported = true
			}
			for _, u := range l.Users {
				if u.LastActiveAt == nil || now.Sub(*u.LastActiveAt) > 30*Day {
					break walk
				}
				a30++
				if now.Sub(*u.LastActiveAt) <= 7*Day {
					a7++
				}
			}
			if len(l.Users) < 100 {
				break
			}
		}
		if supported {
			k.Active7d, k.Active30d = &a7, &a30
		}
	}
	if settingsManage {
		if n, err := s.o.Auth.GetSettings(ctx, at); err == nil && n.GuestAccess != nil {
			g := *n.GuestAccess
			k.GuestAccess = &g
			got = true
		}
	}
	if !got {
		return nil
	}
	return k
}

// ErrLocked: TELEMETRY=off in .env; the switch can't turn it on.
var ErrLocked = apperr.New(http.StatusConflict, "telemetry_locked",
	"TELEMETRY=off in the stack's .env turns usage & health stats off; remove it there (then docker compose up -d admin) to use this switch")
