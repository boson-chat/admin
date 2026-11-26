package updates

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/dockerapi"
	"github.com/boson-chat/admin/internal/logger"
)

// ApplyRequest is POST /api/updates/apply.
type ApplyRequest struct {
	// Components to update ("" / empty with Mode all = everything available).
	Components []string `json:"components"`
	// Mode: "all", "selected" (Components) or "quiet" (all, minus what
	// disconnects users).
	Mode string `json:"mode"`
	// AcknowledgeBreaking must be true when a release in the plan is marked breaking.
	AcknowledgeBreaking bool `json:"acknowledge_breaking"`
}

// RollbackRequest is POST /api/updates/rollback.
type RollbackRequest struct {
	RunID string `json:"run_id"`
	// RestoreData forces the data restore on or off; nil = restore when
	// the update ran migrations.
	RestoreData *bool `json:"restore_data,omitempty"`
}

var (
	errBusy      = apperr.New(http.StatusConflict, "update_running", "an update or rollback is already running")
	errNoUpdates = apperr.New(http.StatusConflict, "nothing_to_update", "none of the selected components has an update that can be applied")
	errBreaking  = apperr.New(http.StatusPreconditionRequired, "breaking_unacknowledged", "a release in this update is marked breaking; read it and confirm")
	errApplyOff  = apperr.New(http.StatusServiceUnavailable, "updates_apply_off", "applying updates is off")
	errNoRun     = apperr.New(http.StatusNotFound, "not_found", "no such update run, or it can't be rolled back")
)

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Preview is POST /api/updates/plan: what an apply request would do.
type Preview struct {
	Items    []Item   `json:"items"`
	Skipped  []string `json:"skipped"`
	Summary  string   `json:"summary"`
	Breaking bool     `json:"breaking"`
	// Disruptive lists who is affected, per component, for the confirm box.
	Impacts []ItemImpact `json:"impacts"`
}

// ItemImpact is one component's line in the confirm box.
type ItemImpact struct {
	Component string `json:"component"`
	Name      string `json:"name"`
	Impact    Impact `json:"impact"`
}

// Preview resolves a request without applying it.
func (s *Service) Preview(req ApplyRequest) (*Preview, error) {
	if req.Mode == "" {
		req.Mode = "all"
	}
	items, comps, skipped, breaking, err := s.Plan(req)
	if err != nil {
		return nil, err
	}
	p := &Preview{Items: items, Skipped: skipped, Summary: Summary(comps), Breaking: breaking, Impacts: []ItemImpact{}}
	if p.Skipped == nil {
		p.Skipped = []string{}
	}
	for _, c := range comps {
		p.Impacts = append(p.Impacts, ItemImpact{Component: c.ID, Name: c.Name, Impact: c.Impact})
	}
	return p, nil
}

// Plan resolves which components a request would update.
func (s *Service) Plan(req ApplyRequest) (items []Item, comps []Component, skipped []string, breaking bool, err error) {
	want := map[string]bool{}
	for _, id := range req.Components {
		want[id] = true
	}
	for _, c := range s.o.Catalog {
		st, ok := s.componentStatus(c.ID)
		if !ok || !st.Update || !st.Applyable {
			continue
		}
		if req.Mode == "selected" && !want[c.ID] {
			continue
		}
		if req.Mode == "quiet" && c.Impact.Disruptive {
			skipped = append(skipped, c.ID)
			continue
		}
		inst, _ := s.installedImage(c.ID)
		to := c.Image + ":" + st.Latest
		items = append(items, Item{Component: c.ID, Name: c.Name, From: st.Installed, To: st.Latest, FromImage: inst,
			ToImage: to, EnvVar: c.EnvVar, Services: c.Services, Pre: c.Pre, Self: c.Self,
			Databases: c.Databases, Volumes: c.Volumes})
		comps = append(comps, c)
		breaking = breaking || st.Breaking
	}
	if len(items) == 0 {
		return nil, nil, skipped, false, errNoUpdates
	}
	return items, comps, skipped, breaking, nil
}

func (s *Service) installedImage(id string) (string, bool) {
	inst, _, err := s.installedVersions(context.Background())
	if err != nil {
		return "", false
	}
	i, ok := inst[id]
	return i.image, ok
}

func updateSteps(self bool) []Step {
	st := []Step{
		{ID: "preflight", Label: "Check the stack and free disk space"},
		{ID: "backup", Label: "Back up .env, config, image digests, databases and data"},
		{ID: "pull", Label: "Pull the new images"},
		{ID: "configure", Label: "Set the new versions in .env"},
		{ID: "recreate", Label: "Recreate the changed services"},
		{ID: "health", Label: "Wait for the services to be healthy"},
		{ID: "verify", Label: "Check that everything works"},
	}
	if self {
		st = append(st, Step{ID: "self", Label: "Update the admin panel (it reloads)"})
	}
	for i := range st {
		st[i].State = StepPending
	}
	return st
}

// Apply starts an update in the background and returns its run.
func (s *Service) Apply(ctx context.Context, actor string, req ApplyRequest, token string) (*Run, error) {
	if s.o.Jobs == nil {
		return nil, errApplyOff
	}
	if req.Mode == "" {
		req.Mode = "all"
	}
	if req.Mode != "all" && req.Mode != "selected" && req.Mode != "quiet" {
		return nil, apperr.Invalid("mode must be all, selected or quiet")
	}
	items, comps, skipped, breaking, err := s.Plan(req)
	if err != nil {
		return nil, err
	}
	if breaking && !req.AcknowledgeBreaking {
		return nil, errBreaking
	}
	s.runMu.Lock()
	if s.busy || (s.journal.Current != nil && (s.journal.Current.State == StateRunning || s.journal.Current.State == StateRestarting)) {
		s.runMu.Unlock()
		return nil, errBusy
	}
	self := slices.ContainsFunc(items, func(i Item) bool { return i.Self })
	run := &Run{ID: s.o.Now().UTC().Format("20060102T150405Z") + "-" + newID(), Kind: "update", Actor: actor,
		State: StateRunning, StartedAt: s.o.Now().UTC(), Items: items, Skipped: skipped, Summary: Summary(comps),
		Steps: updateSteps(self), Checks: []Check{}, Log: []string{}}
	s.journal.Current = run
	s.busy = true
	s.tokens[run.ID] = token
	_ = s.journal.save()
	out := run.clone()
	s.runMu.Unlock()
	log := logger.FromCtx(ctx).With().Str("actor", actor).Str("run", run.ID).Logger()
	for _, it := range items {
		log.Info().Str("component", it.Component).Str("from", it.From).Str("to", it.To).Msg("update applied by admin")
	}
	bg := logger.WithLogger(context.WithoutCancel(ctx), log)
	go s.execUpdate(bg, run.ID)
	return out, nil
}

// mutate changes the current run under the lock and saves the journal.
func (s *Service) mutate(fn func(r *Run)) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if s.journal.Current == nil {
		return
	}
	fn(s.journal.Current)
	_ = s.journal.save()
}

func (s *Service) current() *Run {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if s.journal.Current == nil {
		return nil
	}
	return s.journal.Current.clone()
}

func (s *Service) setStep(id, state, detail string) {
	now := s.o.Now().UTC()
	s.mutate(func(r *Run) {
		st := r.step(id)
		if st == nil {
			return
		}
		st.State = state
		if detail != "" || state == StepOK {
			st.Detail = detail
		}
		switch state {
		case StepRunning:
			st.StartedAt = &now
		case StepOK, StepFailed, StepSkipped:
			st.FinishedAt = &now
		}
	})
}

// logLine keeps the helper's output for the live view (no @@ markers).
func (s *Service) logLine(l string) {
	if rest, ok := strings.CutPrefix(l, "@@STEP "); ok {
		l = "→ " + rest
	} else if strings.HasPrefix(l, "@@") {
		return
	}
	s.mutate(func(r *Run) {
		r.Log = append(r.Log, l)
		if len(r.Log) > 400 {
			r.Log = r.Log[len(r.Log)-400:]
		}
	})
}

// finish ends the current run.
func (s *Service) finish(state, errText string) {
	now := s.o.Now().UTC()
	s.runMu.Lock()
	defer s.runMu.Unlock()
	r := s.journal.Current
	if r == nil {
		return
	}
	r.State, r.FinishedAt = state, &now
	if errText != "" {
		r.Error = errText
	}
	r.CanRollback = r.Kind == "update" && r.Changed && r.Backup != nil
	if r.Kind == "rollback" && r.RollbackOf != "" {
		if orig := s.journal.Runs[r.RollbackOf]; orig != nil {
			orig.RolledBackBy = r.ID
			orig.CanRollback = state == StateRollbackFailed
			if state == StateRolledBack {
				orig.State = StateRolledBack
				for i := range s.journal.History {
					if s.journal.History[i].ID == orig.ID {
						s.journal.History[i].State = StateRolledBack
					}
				}
			}
		}
	}
	// Steps still pending after a failure were skipped.
	for i := range r.Steps {
		if r.Steps[i].State == StepPending || r.Steps[i].State == StepRunning {
			if state == StateFailed || state == StateRollbackFailed {
				r.Steps[i].State = StepSkipped
			}
		}
	}
	s.journal.finish(r)
	delete(s.tokens, r.ID)
	s.busy = false
	_ = s.journal.save()
	// What's installed changed: check again (GitHub answers come from the cache).
	go s.Check(context.Background())
}

func (s *Service) env(loc *Location, run *Run) map[string]string {
	root := s.o.BackupRoot
	if root == "" {
		root = filepath.Join(loc.Dir, "backups", "updates")
	}
	bdir := filepath.Join(root, run.ID)
	if run.Backup != nil && run.Backup.Dir != "" {
		bdir = run.Backup.Dir
	}
	return map[string]string{
		"COMPOSE_PROJECT_NAME": loc.Project, "COMPOSE_FILE": strings.Join(loc.ComposeFiles, ":"),
		"STACK_DIR": loc.Dir, "ENV_FILE": loc.EnvFile, "BACKUP_ROOT": root, "BACKUP_DIR": bdir,
		"UTIL_IMAGE": s.o.UtilImage,
		"SERVICES":   "", "PRE": "", "STOP": "", "VOLUMES": "", "RESTORE_DBS": "", "RESTORE_VOLUMES": "",
		"SETS": "", "TARGET_SETS": "", "FALLBACK_SETS": "", "IMAGES": "", "KEEP": fmt.Sprint(s.o.Keep),
	}
}

func with(env map[string]string, kv ...string) map[string]string {
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}

// job runs a helper job; a non-zero exit is an error with its last lines.
func (s *Service) job(ctx context.Context, runID, name, script string, env map[string]string) (*JobOutput, error) {
	out, err := s.o.Jobs.Run(ctx, runID, name, script, env, s.logLine)
	if err != nil {
		return out, err
	}
	if out.Exit != 0 {
		return out, fmt.Errorf("%s failed (exit %d): %s", name, out.Exit, out.Tail(6))
	}
	return out, nil
}

// allVolumes is every data volume of the installed components (and the
// add-ons), for the backup.
func (s *Service) allVolumes() []string {
	var out []string
	for _, c := range s.o.Catalog {
		out = append(out, c.Volumes...)
	}
	return out
}

func (s *Service) allServices() []string {
	var out []string
	for _, c := range s.o.Catalog {
		out = append(out, c.Services...)
	}
	return out
}

func (s *Service) execUpdate(ctx context.Context, runID string) {
	log := logger.FromCtx(ctx)
	fail := func(step string, err error) {
		s.setStep(step, StepFailed, err.Error())
		log.Error().Err(err).Str("step", step).Msg("update failed")
		// Did it get as far as migrating a database? The rollback offer says so.
		if r := s.current(); r != nil && r.Changed && r.Backup != nil && r.Location != nil {
			if out, err := s.job(ctx, r.ID, "fingerprint", scriptFingerprint, s.env(r.Location, r)); err == nil {
				ran := migrated(r.Backup.Migrations, migrations(out))
				s.mutate(func(x *Run) { x.MigrationsRan = ran })
			}
		}
		s.finish(StateFailed, err.Error())
	}
	run := s.current()
	if run == nil || run.ID != runID {
		return
	}
	// 1. Preflight.
	s.setStep("preflight", StepRunning, "")
	loc, err := s.o.Jobs.Locate(ctx)
	if err != nil {
		fail("preflight", err)
		return
	}
	s.mutate(func(r *Run) { r.Location = loc })
	run.Location = loc
	env := s.env(loc, run)
	out, err := s.job(ctx, run.ID, "preflight", scriptPreflight, with(env, "VOLUMES", strings.Join(s.allVolumes(), " ")))
	if err != nil {
		fail("preflight", err)
		return
	}
	var pf map[string]any
	if raw, ok := out.Result("preflight"); ok {
		_ = json.Unmarshal(raw, &pf)
	}
	free, need := parseKB(pf["free_kb"])*1024, parseKB(pf["need_kb"])*1024
	if free > 0 && free < need+need/5+s.o.MinFree {
		fail("preflight", fmt.Errorf("not enough disk space for the backup: %s free in %s, needs about %s plus %s to spare",
			bytesText(free), env["BACKUP_ROOT"], bytesText(need+need/5), bytesText(s.o.MinFree)))
		return
	}
	s.setStep("preflight", StepOK, fmt.Sprintf("%s free, backup about %s", bytesText(free), bytesText(need)))

	// 2. Backup.
	s.setStep("backup", StepRunning, "")
	out, err = s.job(ctx, run.ID, "backup", scriptBackup, with(env, "SERVICES", strings.Join(s.allServices(), " "),
		"VOLUMES", strings.Join(s.allVolumes(), " ")))
	if err != nil {
		fail("backup", fmt.Errorf("backup failed, nothing was changed: %w", err))
		return
	}
	if _, ok := out.Result("backup"); !ok {
		fail("backup", fmt.Errorf("backup didn't finish, nothing was changed: %s", out.Tail(4)))
		return
	}
	b := parseBackup(out, env["BACKUP_DIR"])
	b.FreeBytes = free
	s.mutate(func(r *Run) {
		r.Backup = b
		for i := range r.Items {
			r.Items[i].PinnedImage = pinned(b, r.Items[i])
		}
	})
	s.setStep("backup", StepOK, fmt.Sprintf("%s in %s (%d files)", bytesText(b.Bytes), b.Dir, len(b.Files)))
	run = s.current()

	var plain, selfItems []Item
	for _, it := range run.Items {
		if it.Self {
			selfItems = append(selfItems, it)
		} else {
			plain = append(plain, it)
		}
	}
	var images, sets, services, pre []string
	for _, it := range run.Items {
		images = append(images, it.ToImage)
	}
	for _, it := range plain {
		sets = append(sets, it.EnvVar+"="+it.ToImage)
		services = append(services, it.Services...)
		pre = append(pre, it.Pre...)
	}

	// 3. Pull (before anything changes).
	s.setStep("pull", StepRunning, "")
	if _, err := s.job(ctx, run.ID, "pull", scriptPull, with(env, "IMAGES", strings.Join(images, " "))); err != nil {
		fail("pull", fmt.Errorf("pulling failed, nothing was changed: %w", err))
		return
	}
	s.setStep("pull", StepOK, strings.Join(images, ", "))

	if len(plain) > 0 {
		// 4. .env.
		s.setStep("configure", StepRunning, "")
		s.mutate(func(r *Run) { r.Changed = true })
		if _, err := s.job(ctx, run.ID, "configure", scriptConfigure, with(env, "SETS", strings.Join(sets, " "))); err != nil {
			fail("configure", err)
			return
		}
		s.setStep("configure", StepOK, strings.Join(sets, " "))
		// 5. Recreate.
		s.setStep("recreate", StepRunning, "")
		if _, err := s.job(ctx, run.ID, "recreate", scriptRecreate, with(env, "SERVICES", strings.Join(services, " "), "PRE", strings.Join(pre, " "))); err != nil {
			fail("recreate", err)
			return
		}
		s.setStep("recreate", StepOK, strings.Join(services, ", "))
		// 6. Health.
		s.setStep("health", StepRunning, "")
		if detail, err := s.waitHealthy(ctx, services); err != nil {
			fail("health", err)
			return
		} else {
			s.setStep("health", StepOK, detail)
		}
	} else {
		for _, id := range []string{"configure", "recreate", "health"} {
			s.setStep(id, StepSkipped, "only the panel itself is updated")
		}
	}
	// 7. Verify.
	if !s.verifyStep(ctx, run, plain, false) {
		return
	}
	if len(selfItems) == 0 {
		s.finish(StateSucceeded, "")
		log.Info().Msg("update succeeded")
		return
	}
	// 8. The panel itself: hand off to a detached helper.
	it := selfItems[0]
	s.setStep("self", StepRunning, "the panel restarts; this page reconnects")
	s.mutate(func(r *Run) { r.State, r.Handoff, r.Changed = StateRestarting, "self", true })
	if err := s.o.Jobs.Start(ctx, run.ID, "self", scriptSelf, with(env,
		"TARGET_SETS", it.EnvVar+"="+it.ToImage, "FALLBACK_SETS", it.EnvVar+"="+fallbackImage(it))); err != nil {
		s.mutate(func(r *Run) { r.State, r.Handoff = StateRunning, "" })
		fail("self", err)
		return
	}
	log.Info().Msg("handing off to the panel's new version")
	// This process is about to be replaced. If it isn't (the helper put
	// this version back), collect the result here.
	s.collectHandoff(ctx)
}

func fallbackImage(it Item) string {
	if it.PinnedImage != "" {
		return it.PinnedImage
	}
	return it.FromImage
}

// pinned is the exact image that ran before (repo:tag@sha256:…).
func pinned(b *Backup, it Item) string {
	for _, rec := range b.Images {
		if !slices.Contains(it.Services, rec.Service) {
			continue
		}
		repo := ImageRepo(rec.Ref)
		for _, d := range rec.Digests {
			if r, dig, ok := strings.Cut(d, "@"); ok && r == repo {
				if strings.Contains(rec.Ref, "@") {
					return rec.Ref
				}
				return rec.Ref + "@" + dig
			}
		}
		return rec.Ref
	}
	return it.FromImage
}

func parseBackup(out *JobOutput, dir string) *Backup {
	b := &Backup{Dir: dir, Files: []BackupFile{}, Images: []ImageRecord{}, Migrations: map[string]string{}}
	if raw, ok := out.Result("backup"); ok {
		var r struct {
			Dir string `json:"dir"`
		}
		if json.Unmarshal(raw, &r) == nil && r.Dir != "" {
			b.Dir = r.Dir
		}
	}
	for _, f := range out.Tagged("FILE") {
		if len(f) == 2 {
			n := parseKB(f[1])
			b.Files = append(b.Files, BackupFile{Name: f[0], Bytes: n})
			b.Bytes += n
		}
	}
	for _, f := range out.Tagged("IMAGE") {
		if len(f) >= 3 {
			b.Images = append(b.Images, ImageRecord{Service: f[0], Ref: f[1], ID: f[2], Digests: f[3:]})
		}
	}
	b.Migrations = migrations(out)
	for _, f := range out.Tagged("PRUNED") {
		if len(f) == 1 {
			b.Pruned = append(b.Pruned, f[0])
		}
	}
	return b
}

func migrations(out *JobOutput) map[string]string {
	m := map[string]string{}
	for _, f := range out.Tagged("MIG") {
		if len(f) == 3 {
			m[f[0]+" "+f[1]] = f[2]
		}
	}
	return m
}

// migrated lists the databases whose migration tables differ.
func migrated(before, after map[string]string) []string {
	set := map[string]bool{}
	for k, v := range after {
		if before[k] != v {
			db, _, _ := strings.Cut(k, " ")
			set[db] = true
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			db, _, _ := strings.Cut(k, " ")
			set[db] = true
		}
	}
	out := make([]string, 0, len(set))
	for db := range set {
		out = append(out, db)
	}
	sort.Strings(out)
	return out
}

// waitHealthy waits until every container of services runs and, when it
// has a healthcheck, is healthy.
func (s *Service) waitHealthy(ctx context.Context, services []string) (string, error) {
	if s.o.Docker == nil {
		return "skipped: Docker is off", nil
	}
	deadline := time.Now().Add(s.o.HealthTimeout)
	var last string
	for {
		cs, err := s.o.Docker.List(ctx, s.o.Project)
		if err == nil {
			bad := []string{}
			for _, svc := range services {
				var c *dockerapi.Container
				for i := range cs {
					if cs[i].Labels[dockerapi.LabelService] == svc {
						c = &cs[i]
						break
					}
				}
				if c == nil {
					bad = append(bad, svc+": no container")
					continue
				}
				in, err := s.o.Docker.Inspect(ctx, c.ID)
				if err != nil {
					bad = append(bad, svc+": "+err.Error())
					continue
				}
				switch {
				case !in.State.Running || in.State.Restarting:
					bad = append(bad, fmt.Sprintf("%s: %s (exit %d)", svc, in.State.Status, in.State.ExitCode))
				case in.State.Health != nil && in.State.Health.Status != "healthy":
					bad = append(bad, svc+": "+in.State.Health.Status)
				}
			}
			if len(bad) == 0 {
				return strings.Join(services, ", ") + " running", nil
			}
			last = strings.Join(bad, "; ")
		} else {
			last = err.Error()
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("not healthy after %s: %s", s.o.HealthTimeout, last)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// verifyStep runs the post-update checks (retrying while services warm up)
// and the migration fingerprint; false = the run failed.
func (s *Service) verifyStep(ctx context.Context, run *Run, items []Item, rollback bool) bool {
	s.setStep("verify", StepRunning, "")
	expect := map[string]string{}
	for _, it := range items {
		if rollback {
			expect[it.Component] = it.From
		} else {
			expect[it.Component] = it.To
		}
	}
	checks := s.verify(ctx, run.ID, expect)
	s.mutate(func(r *Run) { r.Checks = checks })
	if !rollback && run.Location != nil && run.Backup != nil {
		env := s.env(run.Location, run)
		if out, err := s.job(ctx, run.ID, "fingerprint", scriptFingerprint, env); err == nil {
			ran := migrated(run.Backup.Migrations, migrations(out))
			s.mutate(func(r *Run) { r.MigrationsRan = ran })
		}
	}
	var failed []string
	for _, c := range checks {
		if !c.OK && !c.Skipped {
			failed = append(failed, c.Label)
		}
	}
	if len(failed) > 0 {
		err := "checks failed: " + strings.Join(failed, ", ")
		s.setStep("verify", StepFailed, err)
		if rollback {
			s.finish(StateRollbackFailed, err)
		} else {
			s.finish(StateFailed, err)
		}
		return false
	}
	s.setStep("verify", StepOK, fmt.Sprintf("%d checks passed", len(checks)))
	return true
}

func (s *Service) verify(ctx context.Context, runID string, expect map[string]string) []Check {
	s.runMu.Lock()
	token := s.tokens[runID]
	s.runMu.Unlock()
	deadline := time.Now().Add(s.o.VerifyTimeout)
	for {
		inst, services, _ := s.installedVersions(ctx)
		var checks []Check
		if s.o.Verifier != nil {
			checks = s.o.Verifier.Verify(ctx, VerifyInput{Services: services, Token: token})
		}
		for _, id := range sortedKeys(expect) {
			want := expect[id]
			c, _ := s.catalog(id)
			got := inst[id].version
			if c.Self {
				continue // checked by the panel's new version after the hand-off
			}
			ok := got == want
			d := "running " + got
			if !ok {
				d = fmt.Sprintf("running %q, expected %s", got, want)
			}
			checks = append(checks, Check{ID: "version-" + id, Label: c.Name + " version", OK: ok, Detail: d})
		}
		allOK := true
		for _, c := range checks {
			if !c.OK && !c.Skipped {
				allOK = false
			}
		}
		if allOK || time.Now().After(deadline) {
			if checks == nil {
				checks = []Check{}
			}
			return checks
		}
		select {
		case <-ctx.Done():
			return checks
		case <-time.After(3 * time.Second):
		}
	}
}

func sortedKeys(m map[string]string) []string {
	k := make([]string, 0, len(m))
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}

// resume picks up a run interrupted by the panel's restart.
func (s *Service) resume(ctx context.Context) {
	r := s.current()
	if r == nil {
		return
	}
	switch r.State {
	case StateRestarting:
		s.runMu.Lock()
		s.busy = true
		s.runMu.Unlock()
		s.collectHandoff(ctx)
	case StateRunning:
		s.runMu.Lock()
		s.busy = true
		s.runMu.Unlock()
		s.finish(StateFailed, "interrupted: the panel restarted during the "+r.Kind)
	}
}

// collectHandoff waits for the detached self-update helper and finishes the run.
func (s *Service) collectHandoff(ctx context.Context) {
	r := s.current()
	if r == nil || r.Handoff == "" {
		return
	}
	deadline := time.Now().Add(10 * time.Minute)
	for {
		out, done, err := s.o.Jobs.Collect(ctx, r.ID, r.Handoff)
		if ctx.Err() != nil {
			return // this process is going away; the next one collects
		}
		if err != nil && err == errJobGone {
			s.setStep("self", StepFailed, "the helper's result is lost")
			s.finish(failState(r), "the self-update helper disappeared before reporting")
			return
		}
		if done {
			for _, l := range out.Lines {
				s.logLine(l)
			}
			var res struct {
				OK       bool `json:"ok"`
				FellBack bool `json:"fell_back"`
			}
			raw, ok := out.Result("self")
			if ok {
				_ = json.Unmarshal(raw, &res)
			}
			s.mutate(func(x *Run) { x.Handoff = "" })
			if !ok || !res.OK {
				msg := "the panel's new version didn't stay up"
				if res.FellBack {
					msg += "; the previous version was put back"
				}
				if !ok {
					msg = "the self-update helper failed: " + out.Tail(4)
				}
				s.setStep("self", StepFailed, msg)
				s.finish(failState(r), msg)
				return
			}
			// The rest again, and this process's own version.
			expect := map[string]string{}
			var want string
			for _, it := range r.Items {
				v := it.To
				if r.Kind == "rollback" {
					v = it.From
				}
				if it.Self {
					want = v
				} else {
					expect[it.Component] = v
				}
			}
			checks := s.verify(ctx, r.ID, expect)
			got := strings.TrimPrefix(s.o.SelfVersion, "v")
			selfOK := want == "" || got == want
			checks = append(checks, Check{ID: "version-admin", Label: "Admin panel version", OK: selfOK,
				Detail: fmt.Sprintf("running %s, expected %s", got, want)})
			s.mutate(func(x *Run) { x.Checks = checks })
			for _, c := range checks {
				if !c.OK && !c.Skipped {
					s.setStep("self", StepFailed, c.Label+": "+c.Detail)
					s.finish(failState(r), "checks failed after the panel restarted: "+c.Label)
					return
				}
			}
			s.setStep("self", StepOK, "the panel runs "+got)
			s.finish(okState(r), "")
			return
		}
		if time.Now().After(deadline) {
			s.setStep("self", StepFailed, "no result after 10 minutes")
			s.finish(failState(r), "the self-update helper didn't finish")
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func failState(r *Run) string {
	if r.Kind == "rollback" {
		return StateRollbackFailed
	}
	return StateFailed
}

func okState(r *Run) string {
	if r.Kind == "rollback" {
		return StateRolledBack
	}
	return StateSucceeded
}

func bytesText(n int64) string {
	const k = 1024
	switch {
	case n >= k*k*k:
		return fmt.Sprintf("%.1f GiB", float64(n)/(k*k*k))
	case n >= k*k:
		return fmt.Sprintf("%.1f MiB", float64(n)/(k*k))
	case n >= k:
		return fmt.Sprintf("%.0f KiB", float64(n)/k)
	}
	return fmt.Sprintf("%d B", n)
}
