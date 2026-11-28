// Package updates tells the admin when any part of the Boson install has a
// newer release, shows every changelog between the installed and the newest
// version with who an update affects, and applies it: backup (config, image
// digests, databases, data volumes), new versions in the stack's .env, pull,
// recreate only the changed services, health and real post-update checks,
// and a rollback that puts the previous digests, config and (when the
// update migrated a database) data back.
//
// Host work runs in a short-lived helper container (docker CLI + compose)
// that the panel starts through a Docker socket proxy; the panel updates
// itself last by handing off to a detached helper and picking the run up
// again when it starts.
package updates

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/boson-chat/admin/internal/clients/dockerapi"
	"github.com/boson-chat/admin/internal/logger"
)

// Options configure the service. Releases is required; everything else
// switches a part on.
type Options struct {
	Releases ReleaseSource
	Registry Registry // nil = every release counts as published
	// Docker lists the compose project's containers (installed versions).
	Docker  dockerapi.DockerClientImpl
	Project string
	// Jobs runs helper jobs (nil = applying is off, ApplyOff says why).
	Jobs     JobRunner
	ApplyOff string
	Verifier Verifier
	// SelfVersion is the panel's own version (VERSION).
	SelfVersion string
	// StackVersion is the stack's version when it reports one.
	StackVersion string
	// Interval between automatic checks (default 6 h).
	Interval time.Duration
	// While a release waits for its image (Pending), the rows waiting are
	// checked again every PendingInterval (default 2 min), at most
	// PendingChecks times (default 15) per full check, so the update is
	// offered once the push finishes instead of an Interval later. Only
	// those rows are asked again (GitHub's unauthenticated limit is 60 an
	// hour).
	PendingInterval time.Duration
	PendingChecks   int
	// StateDir keeps the update journal (runs survive the panel's own
	// restart). Empty = in memory only; the panel can't update itself.
	StateDir string
	// BackupRoot is the host directory for backups; Keep how many stay.
	BackupRoot string
	Keep       int
	// MinFree is the free space that must remain after the backup.
	MinFree int64
	// UtilImage runs tar/du on volumes (busybox).
	UtilImage     string
	HealthTimeout time.Duration
	VerifyTimeout time.Duration
	// ProxyWait is how long a run waits for the Docker socket proxies to
	// answer again (after a recreate, before a rollback). Default 90 s.
	ProxyWait time.Duration
	// RetryCheck: a check that couldn't reach Docker runs again this soon
	// (default 1 min) instead of an Interval later, so its error clears
	// once the proxies are back.
	RetryCheck time.Duration
	Catalog    []Component
	Now        func() time.Time
	// OnFinish is called (in its own goroutine) when an update or a
	// rollback finishes, failed or not, with the run's kind ("update" or
	// "rollback"): the usage & health stats send a report soon after.
	OnFinish func(kind string)
}

// ServiceImpl is what the HTTP layer uses.
type ServiceImpl interface {
	Status(ctx context.Context) *Status
	Check(ctx context.Context) *Status
	Preview(req ApplyRequest) (*Preview, error)
	Apply(ctx context.Context, actor string, req ApplyRequest, token string) (*Run, error)
	Rollback(ctx context.Context, actor string, req RollbackRequest, token string) (*Run, error)
}

// Component status as the UI shows it.
type ComponentStatus struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	About     string    `json:"about"`
	Repo      string    `json:"repo"`
	Mode      Mode      `json:"mode"`
	Installed string    `json:"installed,omitempty"`
	Present   bool      `json:"present"`
	AddOn     bool      `json:"add_on,omitempty"`
	Latest    string    `json:"latest,omitempty"`
	Update    bool      `json:"update"`
	Releases  []Release `json:"releases"`
	// Pending: newer releases whose image isn't in the registry yet.
	Pending  []string `json:"pending,omitempty"`
	Breaking bool     `json:"breaking"`
	// BreakingNotes are the BREAKING CHANGE paragraphs of the range.
	BreakingNotes []string `json:"breaking_notes,omitempty"`
	Impact        Impact   `json:"impact"`
	Services      []string `json:"services"`
	NeverRestart  []string `json:"never_restart,omitempty"`
	// Applyable: the panel can apply it (else Why).
	Applyable bool   `json:"applyable"`
	Why       string `json:"why,omitempty"`
	Error     string `json:"error,omitempty"`
	// Stack: the stack row's detail (what its newest release changes here).
	Stack *StackStatus `json:"stack,omitempty"`
	// PartOf: comes with that component's update (not listed).
	PartOf string `json:"-"`
}

// ApplyInfo says whether updates can be applied here.
type ApplyInfo struct {
	Enabled    bool   `json:"enabled"`
	Why        string `json:"why,omitempty"`
	SelfUpdate bool   `json:"self_update"`
	BackupRoot string `json:"backup_root,omitempty"`
	Keep       int    `json:"keep"`
}

// Status is GET /api/updates.
type Status struct {
	CheckedAt   *time.Time        `json:"checked_at,omitempty"`
	NextCheckAt *time.Time        `json:"next_check_at,omitempty"`
	Checking    bool              `json:"checking"`
	Available   int               `json:"available"`
	Components  []ComponentStatus `json:"components"`
	RateLimit   *RateLimit        `json:"rate_limit,omitempty"`
	Error       string            `json:"error,omitempty"`
	Apply       ApplyInfo         `json:"apply"`
	Run         *Run              `json:"run,omitempty"`
	History     []RunSummary      `json:"history"`
	// dockerDown: the check couldn't reach Docker (it runs again soon).
	dockerDown bool
}

// Service implements ServiceImpl.
type Service struct {
	o Options

	mu       sync.Mutex
	status   *Status
	checking bool
	kick     chan struct{}

	runMu   sync.Mutex
	journal journal
	tokens  map[string]string
	busy    bool
}

// New builds the service; Start runs its check loop and resumes a run.
func New(o Options) *Service {
	if o.Interval <= 0 {
		o.Interval = 6 * time.Hour
	}
	if o.PendingInterval <= 0 {
		o.PendingInterval = 2 * time.Minute
	}
	if o.PendingChecks <= 0 {
		o.PendingChecks = 15
	}
	if o.Keep <= 0 {
		o.Keep = 5
	}
	if o.MinFree <= 0 {
		o.MinFree = 1 << 30
	}
	if o.UtilImage == "" {
		o.UtilImage = "busybox:1.37.0"
	}
	if o.HealthTimeout <= 0 {
		o.HealthTimeout = 3 * time.Minute
	}
	if o.VerifyTimeout <= 0 {
		o.VerifyTimeout = 90 * time.Second
	}
	if o.ProxyWait <= 0 {
		o.ProxyWait = 90 * time.Second
	}
	if o.RetryCheck <= 0 {
		o.RetryCheck = time.Minute
	}
	if o.Catalog == nil {
		o.Catalog = Catalog
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Project == "" {
		o.Project = "boson"
	}
	s := &Service{o: o, kick: make(chan struct{}, 1), tokens: map[string]string{}}
	s.journal = loadJournal(o.StateDir)
	return s
}

// Start checks now and every Interval (sooner for releases waiting for
// their image), and picks up a run that was handing off to the panel's new
// version.
func (s *Service) Start(ctx context.Context) {
	go s.resume(ctx)
	go func() {
		t := time.NewTimer(5 * time.Second)
		defer t.Stop()
		var due time.Time // the next full check (zero: now)
		rechecks := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			case <-s.kick:
				due = time.Time{}
			}
			var st *Status
			if !due.IsZero() && time.Now().Before(due) {
				st = s.RecheckPending(ctx)
				rechecks++
			} else {
				st = s.Check(ctx)
				due, rechecks = time.Now().Add(s.o.Interval), 0
				if st.dockerDown {
					due = time.Now().Add(s.o.RetryCheck)
				}
			}
			t.Reset(s.wait(st, due, rechecks, time.Now()))
		}
	}()
}

// wait is how long the loop sleeps after st: until the full check due,
// or PendingInterval while rows wait for their image (rechecks so far).
func (s *Service) wait(st *Status, due time.Time, rechecks int, now time.Time) time.Duration {
	w := due.Sub(now)
	if hasPending(st) && rechecks < s.o.PendingChecks {
		w = min(w, s.o.PendingInterval)
	}
	return max(w, time.Second)
}

func hasPending(st *Status) bool {
	return st != nil && slices.ContainsFunc(st.Components, func(c ComponentStatus) bool { return len(c.Pending) > 0 })
}

func (s *Service) applyInfo() ApplyInfo {
	a := ApplyInfo{Enabled: s.o.Jobs != nil, Why: s.o.ApplyOff, BackupRoot: s.o.BackupRoot, Keep: s.o.Keep,
		SelfUpdate: s.o.Jobs != nil && s.o.StateDir != ""}
	if a.Enabled {
		a.Why = ""
	} else if a.Why == "" {
		a.Why = "applying updates is off"
	}
	return a
}

// Status returns the last check (without checking).
func (s *Service) Status(ctx context.Context) *Status {
	s.mu.Lock()
	st := s.status
	checking := s.checking
	s.mu.Unlock()
	out := &Status{Components: []ComponentStatus{}}
	if st != nil {
		c := *st
		out = &c
	}
	out.Checking = checking
	out.Apply = s.applyInfo()
	if s.o.Releases != nil {
		out.RateLimit = s.o.Releases.Limit()
	}
	s.runMu.Lock()
	if s.journal.Current != nil {
		r := s.journal.Current.clone()
		out.Run = r
	}
	out.History = slices.Clone(s.journal.History)
	s.runMu.Unlock()
	if out.History == nil {
		out.History = []RunSummary{}
	}
	return out
}

type installed struct {
	version string
	image   string
	present bool
}

// installedVersions reads the compose project's containers: each catalog
// component's version is its first service's image tag.
func (s *Service) installedVersions(ctx context.Context) (map[string]installed, map[string]bool, error) {
	out := map[string]installed{}
	services := map[string]bool{}
	if s.o.Docker == nil {
		return out, services, errors.New("the Docker socket proxy is off (DOCKER_PROXY_URL): installed versions are unknown")
	}
	cs, err := s.o.Docker.List(ctx, s.o.Project)
	if err != nil {
		return out, services, err
	}
	bySvc := map[string]dockerapi.Container{}
	for _, c := range cs {
		svc := c.Labels[dockerapi.LabelService]
		services[svc] = true
		if _, dup := bySvc[svc]; !dup || c.State == "running" {
			bySvc[svc] = c
		}
	}
	for _, comp := range s.o.Catalog {
		for _, svc := range comp.Services {
			c, ok := bySvc[svc]
			if !ok {
				continue
			}
			v := ImageTag(c.Image)
			if _, ok := ParseVersion(v); !ok {
				if lv := c.Labels[dockerapi.LabelVersion]; lv != "" {
					v = lv
				}
			}
			out[comp.ID] = installed{version: strings.TrimPrefix(v, "v"), image: c.Image, present: true}
			break
		}
	}
	return out, services, nil
}

// Check asks GitHub and the registry now (at most one check at a time).
func (s *Service) Check(ctx context.Context) *Status {
	s.mu.Lock()
	if s.checking {
		s.mu.Unlock()
		return s.Status(ctx)
	}
	s.checking = true
	s.mu.Unlock()
	st := s.check(ctx)
	s.mu.Lock()
	s.status, s.checking = st, false
	s.mu.Unlock()
	return s.Status(ctx)
}

// RecheckPending asks again for the rows of the last check whose newest
// release waited for its image (the rest of the check stays).
func (s *Service) RecheckPending(ctx context.Context) *Status {
	s.mu.Lock()
	old := s.status
	if s.checking || !hasPending(old) {
		s.mu.Unlock()
		return s.Status(ctx)
	}
	s.checking = true
	s.mu.Unlock()
	st := s.recheck(ctx, old)
	s.mu.Lock()
	s.status, s.checking = st, false
	s.mu.Unlock()
	return s.Status(ctx)
}

func (s *Service) recheck(ctx context.Context, old *Status) *Status {
	st := *old
	st.Components = slices.Clone(old.Components)
	var inst map[string]installed
	for i, cs := range st.Components {
		c, ok := s.catalog(cs.ID)
		if len(cs.Pending) == 0 || !ok {
			continue
		}
		if c.Mode == ModeStack {
			st.Components[i] = s.stackComponent(ctx, c)
			continue
		}
		if inst == nil {
			var err error
			if inst, err = s.installed(ctx); err != nil {
				return old
			}
		}
		st.Components[i] = s.component(ctx, c, inst[c.ID])
	}
	st.Available = 0
	for _, c := range st.Components {
		if c.Update {
			st.Available++
		}
	}
	logger.FromCtx(ctx).Info().Int("available", st.Available).Msg("update recheck (releases waiting for their image)")
	return &st
}

// installed is installedVersions with the panel's own version filled in.
func (s *Service) installed(ctx context.Context) (map[string]installed, error) {
	inst, _, err := s.installedVersions(ctx)
	if s.o.SelfVersion != "" {
		if _, ok := ParseVersion(s.o.SelfVersion); ok {
			i := inst["admin"]
			if _, ok := ParseVersion(i.version); !ok {
				i.version = strings.TrimPrefix(s.o.SelfVersion, "v")
			}
			i.present = true
			inst["admin"] = i
		}
	}
	return inst, err
}

// CheckSoon wakes the loop (POST /api/updates/check answers right away
// with Checking set when a check is already running).
func (s *Service) CheckSoon() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

func (s *Service) check(ctx context.Context) *Status {
	now := s.o.Now().UTC()
	next := now.Add(s.o.Interval)
	st := &Status{CheckedAt: &now, NextCheckAt: &next, Components: []ComponentStatus{}}
	inst, err := s.installed(ctx)
	if err != nil {
		st.Error = err.Error()
		if errors.Is(err, dockerapi.ErrUnavailable) {
			st.dockerDown = true
			retry := now.Add(s.o.RetryCheck)
			st.NextCheckAt = &retry
		}
	}
	var wg sync.WaitGroup
	comps := make([]ComponentStatus, len(s.o.Catalog))
	for i, c := range s.o.Catalog {
		switch {
		case c.PartOf != "":
			// Comes with the stack's update (its Included list).
			comps[i] = ComponentStatus{ID: c.ID, PartOf: c.PartOf}
		case c.Mode == ModeStack:
			wg.Go(func() { comps[i] = s.stackComponent(ctx, c) })
		default:
			wg.Go(func() { comps[i] = s.component(ctx, c, inst[c.ID]) })
		}
	}
	wg.Wait()
	for _, c := range comps {
		if c.PartOf != "" || c.AddOn && !c.Present {
			continue
		}
		if c.Update {
			st.Available++
		}
		st.Components = append(st.Components, c)
	}
	logger.FromCtx(ctx).Info().Int("available", st.Available).Msg("update check")
	return st
}

func (s *Service) component(ctx context.Context, c Component, in installed) ComponentStatus {
	cs := ComponentStatus{ID: c.ID, Name: c.Name, About: c.About, Repo: c.Repo, Mode: c.Mode, Installed: in.version,
		Present: in.present, AddOn: c.AddOn, Impact: c.Impact, Services: c.Services, NeverRestart: c.NeverRestart,
		Releases: []Release{}}
	switch {
	case !s.applyInfo().Enabled:
		cs.Why = s.applyInfo().Why
	case c.Self && s.o.StateDir == "":
		cs.Why = "the panel can only update itself with UPDATES_STATE_DIR set (a volume)"
	default:
		cs.Applyable = true
	}
	if !in.present {
		return cs
	}
	rs, err := s.o.Releases.Releases(ctx, c.Repo)
	if err != nil {
		var nr ErrNoReleases
		if errors.As(err, &nr) {
			cs.Error = "No releases published yet."
			return cs
		}
		cs.Error = err.Error()
		if len(rs) == 0 {
			return cs
		}
	}
	cur, ok := ParseVersion(in.version)
	if !ok {
		if in.version == "" {
			cs.Error = joinErr(cs.Error, "installed version unknown")
		} else {
			cs.Error = joinErr(cs.Error, "installed version “"+in.version+"” isn't a release version")
		}
		if n, ok := Newest(rs); ok {
			cs.Latest = n.Version
		}
		return cs
	}
	newest, ok := Newest(rs)
	if !ok || !Less(cur, newest.v) {
		if ok {
			cs.Latest = newest.Version
		}
		return cs
	}
	cands := Between(rs, cur, newest.v)
	// The newest release whose image the registry has: GitHub releases
	// can be a few minutes ahead of the image push.
	target := -1
	for i, r := range cands {
		if c.Image == "" || s.o.Registry == nil {
			target = i
			break
		}
		okTag, err := s.o.Registry.TagExists(ctx, c.Image, r.Version)
		if err != nil {
			cs.Error = joinErr(cs.Error, "registry: "+err.Error())
			target = i // can't tell: offer it, the pull step will say
			break
		}
		if okTag {
			target = i
			break
		}
		cs.Pending = append(cs.Pending, r.Version)
		if i >= 4 {
			break
		}
	}
	if target < 0 {
		cs.Latest = cur.String()
		return cs
	}
	rng := slices.Clone(cands[target:])
	MarkBreaking(rng, cur)
	for i := range rng {
		rng[i].Published = true
		if len(rng[i].Breaking) > 0 {
			cs.Breaking = true
			cs.BreakingNotes = append(cs.BreakingNotes, BreakingNotes(rng[i].Body)...)
		}
	}
	cs.Releases = rng
	cs.Latest = rng[0].Version
	cs.Update = true
	return cs
}

func joinErr(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

func (s *Service) componentStatus(id string) (ComponentStatus, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status == nil {
		return ComponentStatus{}, false
	}
	for _, c := range s.status.Components {
		if c.ID == id {
			return c, true
		}
	}
	return ComponentStatus{}, false
}

func (s *Service) catalog(id string) (Component, bool) {
	for _, c := range s.o.Catalog {
		if c.ID == id {
			return c, true
		}
	}
	return Component{}, false
}
