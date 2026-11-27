package updates

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/boson-chat/admin/internal/logger"
)

func rollbackSteps(self bool) []Step {
	st := []Step{
		{ID: "inspect", Label: "Check whether the update migrated any database"},
		{ID: "restore", Label: "Put back the previous images, .env and config (and data)"},
		{ID: "health", Label: "Wait for the services to be healthy"},
		{ID: "verify", Label: "Check that everything works"},
	}
	if self {
		st = append(st, Step{ID: "self", Label: "Put back the previous admin panel (it reloads)"})
	}
	for i := range st {
		st[i].State = StepPending
	}
	return st
}

// rollbackTarget finds the run to roll back: the current one or a recent one.
func (s *Service) rollbackTarget(id string) *Run {
	if c := s.journal.Current; c != nil && (id == "" || c.ID == id) && c.Kind == "update" {
		return c.clone()
	}
	if r := s.journal.Runs[id]; r != nil && r.Kind == "update" {
		return r.clone()
	}
	return nil
}

// Rollback puts back what an update changed: the previous image digests,
// .env and config, and the databases and volumes when the update ran
// migrations (or when asked), then checks the stack again.
func (s *Service) Rollback(ctx context.Context, actor string, req RollbackRequest, token string) (*Run, error) {
	if s.o.Jobs == nil {
		return nil, errApplyOff
	}
	s.runMu.Lock()
	if s.busy {
		s.runMu.Unlock()
		return nil, errBusy
	}
	orig := s.rollbackTarget(req.RunID)
	if orig == nil || !orig.CanRollback || orig.Backup == nil || orig.RolledBackBy != "" && orig.State == StateRolledBack {
		s.runMu.Unlock()
		return nil, errNoRun
	}
	// Only what the update got as far as changing.
	items := slices.Clone(orig.Items)
	self := slices.ContainsFunc(items, func(i Item) bool { return i.Self }) && orig.step("self") != nil &&
		orig.step("self").State != StepPending && orig.step("self").State != StepSkipped
	if !self {
		items = slices.DeleteFunc(items, func(i Item) bool { return i.Self })
	}
	var comps []Component
	for _, it := range items {
		if c, ok := s.catalog(it.Component); ok {
			comps = append(comps, c)
		}
	}
	run := &Run{ID: s.o.Now().UTC().Format("20060102T150405Z") + "-" + newID(), Kind: "rollback", Actor: actor,
		State: StateRunning, StartedAt: s.o.Now().UTC(), Items: items, Summary: Summary(comps),
		Steps: rollbackSteps(self), Checks: []Check{}, Log: []string{}, RollbackOf: orig.ID,
		Backup: orig.Backup, Location: orig.Location, MigrationsRan: orig.MigrationsRan, Changed: true}
	if req.RestoreData != nil {
		run.RestoreData = *req.RestoreData
	}
	if s.journal.Current != nil && s.journal.Current.ID == orig.ID {
		s.journal.finish(s.journal.Current)
	}
	s.journal.Current = run
	s.busy = true
	s.tokens[run.ID] = token
	_ = s.journal.save()
	out := run.clone()
	s.runMu.Unlock()
	log := logger.FromCtx(ctx).With().Str("actor", actor).Str("run", run.ID).Str("rollback_of", orig.ID).Logger()
	for _, it := range items {
		log.Info().Str("component", it.Component).Str("from", it.To).Str("to", it.From).Msg("rollback started by admin")
	}
	bg := logger.WithLogger(context.WithoutCancel(ctx), log)
	go s.execRollback(bg, run.ID, req.RestoreData == nil)
	return out, nil
}

func (s *Service) execRollback(ctx context.Context, runID string, autoData bool) {
	log := logger.FromCtx(ctx)
	fail := func(step string, err error) {
		s.setStep(step, StepFailed, err.Error())
		log.Error().Err(err).Str("step", step).Msg("rollback failed")
		s.finish(StateRollbackFailed, err.Error())
	}
	run := s.current()
	if run == nil || run.ID != runID {
		return
	}
	loc := run.Location
	if loc == nil {
		var err error
		if loc, err = s.o.Jobs.Locate(ctx); err != nil {
			fail("inspect", err)
			return
		}
		s.mutate(func(r *Run) { r.Location = loc })
	}
	env := s.env(loc, run)

	// 1. Did the update migrate a database? Compare with the backup.
	s.setStep("inspect", StepRunning, "")
	out, err := s.job(ctx, run.ID, "fingerprint", scriptFingerprint, env)
	if err != nil {
		fail("inspect", err)
		return
	}
	ran := migrated(run.Backup.Migrations, migrations(out))
	restore := run.RestoreData
	if autoData {
		restore = len(ran) > 0
	}
	s.mutate(func(r *Run) { r.MigrationsRan, r.RestoreData = ran, restore })
	detail := "no migrations ran: the data stays as it is"
	if len(ran) > 0 {
		detail = "migrations ran in " + strings.Join(ran, ", ")
	}
	if restore {
		detail += "; restoring the data from the backup (changes since the update are lost)"
	}
	s.setStep("inspect", StepOK, detail)

	// 2. Restore.
	s.setStep("restore", StepRunning, "")
	var sets, services, dbs, vols, stop []string
	var selfItem *Item
	for i, it := range run.Items {
		if it.Self {
			selfItem = &run.Items[i]
			continue
		}
		if img := fallbackImage(it); img != "" && it.EnvVar != "" {
			sets = append(sets, it.EnvVar+"="+img)
		}
		services = append(services, it.Services...)
	}
	if restore {
		have := map[string]bool{}
		for _, f := range run.Backup.Files {
			have[f.Name] = true
		}
		clients := PostgresClients()
		for _, db := range ran {
			if !have["pg-"+db+".dump"] {
				continue
			}
			dbs = append(dbs, db)
			stop = append(stop, clients[db]...)
		}
		// Data restore asked for by hand: every database of the rolled-back components.
		if !autoData {
			for _, it := range run.Items {
				for _, db := range it.Databases {
					if have["pg-"+db+".dump"] && !slices.Contains(dbs, db) {
						dbs = append(dbs, db)
						stop = append(stop, clients[db]...)
					}
				}
			}
		}
		// Volumes: by hand, every volume of the rolled-back components; by
		// itself (migrations ran), only those of the components whose
		// database migrated (their data goes back together). Only the
		// services that use a volume stop for it, never everything a
		// stack update recreated (that stopped Postgres before the
		// database restore that needs it).
		owners := s.volumeClients()
		for _, it := range run.Items {
			for _, v := range it.Volumes {
				if !have["vol-"+v+".tgz"] || slices.Contains(vols, v) {
					continue
				}
				if autoData && !s.volumeOfMigrated(v, dbs) {
					continue
				}
				vols = append(vols, v)
				if o := owners[v]; len(o) > 0 {
					stop = append(stop, o...)
				} else if it.Stack == nil {
					stop = append(stop, it.Services...)
				}
			}
		}
	}
	stack := slices.ContainsFunc(run.Items, func(i Item) bool { return i.Stack != nil })
	_, presentSvc, _ := s.installedVersions(ctx)
	// Everything that uses restored data stops first (the rolled-back
	// services too: nothing may write to a database while it's replaced).
	// Not Postgres: the databases are restored through it.
	stop = slices.DeleteFunc(uniq(stop), func(x string) bool {
		return x == "admin" || x == "postgres" || (presentSvc != nil && !presentSvc[x])
	})
	up := slices.DeleteFunc(slices.Clone(services), func(x string) bool { return slices.Contains(stop, x) })
	renv := with(env, "SETS", strings.Join(sets, " "),
		"SERVICES", strings.Join(up, " "), "STOP", strings.Join(stop, " "),
		"RESTORE_DBS", strings.Join(dbs, " "), "RESTORE_VOLUMES", strings.Join(vols, " "))
	if stack {
		renv["STACK_RESTORE"] = "1"
	}
	rout, err := s.job(ctx, run.ID, "rollback", scriptRollback, renv)
	if err != nil {
		// The script started the services again (on the restored files)
		// before giving up: say whether that worked.
		switch rec := recovered(rout); rec {
		case "ok":
			err = fmt.Errorf("%w; the stopped services were started again on the restored files", err)
		case "failed":
			err = fmt.Errorf("%w; starting the services again failed too (run docker compose up -d in the stack directory)", err)
		}
		fail("restore", err)
		return
	}
	// Services the update added: removed again, nothing to wait for; and
	// those the restored files don't have, left as they are.
	for _, tag := range []string{"REMOVED", "NOTSTOPPED"} {
		for _, f := range rout.Tagged(tag) {
			if len(f) == 1 {
				up = slices.DeleteFunc(up, func(x string) bool { return x == f[0] })
				stop = slices.DeleteFunc(stop, func(x string) bool { return x == f[0] })
			}
		}
	}
	d := "pinned " + strings.Join(sets, " ")
	if stack {
		d = "stack files, .env and config from the backup"
		if len(sets) > 0 {
			d += "; pinned " + strings.Join(sets, " ")
		}
	}
	if len(dbs) > 0 {
		d += "; restored databases " + strings.Join(dbs, ", ")
	}
	if len(vols) > 0 {
		d += "; restored volumes " + strings.Join(vols, ", ")
	}
	s.setStep("restore", StepOK, d)

	// 3. Health and 4. the same checks as after the update.
	all := append(slices.Clone(up), stop...)
	if len(all) > 0 {
		s.setStep("health", StepRunning, "")
		if detail, err := s.waitHealthy(ctx, all); err != nil {
			fail("health", err)
			return
		} else {
			s.setStep("health", StepOK, detail)
		}
	} else {
		s.setStep("health", StepSkipped, "")
	}
	var plain []Item
	for _, it := range run.Items {
		if !it.Self {
			plain = append(plain, it)
		}
	}
	if !s.verifyStep(ctx, run, plain, true) {
		return
	}
	if selfItem == nil {
		s.finish(StateRolledBack, "")
		log.Info().Msg("rollback finished")
		return
	}
	s.setStep("self", StepRunning, "the panel restarts; this page reconnects")
	s.mutate(func(r *Run) { r.State, r.Handoff = StateRestarting, "self" })
	if err := s.o.Jobs.Start(ctx, run.ID, "self", scriptSelf, with(env,
		"TARGET_SETS", selfItem.EnvVar+"="+fallbackImage(*selfItem), "FALLBACK_SETS", selfItem.EnvVar+"="+selfItem.ToImage)); err != nil {
		s.mutate(func(r *Run) { r.State, r.Handoff = StateRunning, "" })
		fail("self", fmt.Errorf("starting the helper: %w", err))
		return
	}
	s.collectHandoff(ctx)
}

// recovered: "ok" or "failed" when the rollback script brought the
// services back up after a failed step, "" when it didn't get to.
func recovered(out *JobOutput) string {
	if out == nil {
		return ""
	}
	for _, f := range out.Tagged("RECOVER") {
		if len(f) == 1 {
			return f[0]
		}
	}
	return ""
}

// volumeClients: each data volume → the services of the component that
// owns it.
func (s *Service) volumeClients() map[string][]string {
	m := map[string][]string{}
	for _, c := range s.o.Catalog {
		for _, v := range c.Volumes {
			m[v] = append(m[v], c.Services...)
		}
	}
	return m
}

// volumeOfMigrated: v belongs to a component one of whose databases is
// among dbs (restored).
func (s *Service) volumeOfMigrated(v string, dbs []string) bool {
	for _, c := range s.o.Catalog {
		if !slices.Contains(c.Volumes, v) {
			continue
		}
		for _, db := range c.Databases {
			if slices.Contains(dbs, db) {
				return true
			}
		}
	}
	return false
}

func uniq(in []string) []string {
	var out []string
	for _, x := range in {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}
