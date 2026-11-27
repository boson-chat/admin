package updates

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/boson-chat/admin/internal/clients/dockerapi"
)

// The stack row: the installed release (VERSION on the host), the newest
// one on GitHub, and what it would change here (stack.go).

const (
	assetManifest = "manifest.json"
	maxManifest   = 4 << 20
)

func bundleAsset(v string) string { return "boson-stack-" + v + ".tar.gz" }

// runningImages: compose service → the image its container runs.
func (s *Service) runningImages(ctx context.Context) map[string]string {
	out := map[string]string{}
	if s.o.Docker == nil {
		return out
	}
	cs, err := s.o.Docker.List(ctx, s.o.Project)
	if err != nil {
		return out
	}
	for _, c := range cs {
		svc := c.Labels[dockerapi.LabelService]
		if _, dup := out[svc]; !dup || c.State == "running" {
			out[svc] = c.Image
		}
	}
	return out
}

// stackStateNow reads the installed stack through a helper job.
func (s *Service) stackStateNow(ctx context.Context) (*Location, *stackState, error) {
	loc, err := s.o.Jobs.Locate(ctx)
	if err != nil {
		return nil, nil, err
	}
	env := map[string]string{"STACK_DIR": loc.Dir, "ENV_FILE": loc.EnvFile}
	out, err := s.o.Jobs.Run(ctx, fmt.Sprintf("check%d", time.Now().UnixNano()), "stack-state", scriptStackState, env, nil)
	if err != nil {
		return loc, nil, err
	}
	if _, ok := out.Result("stack-state"); !ok || out.Exit != 0 {
		return loc, nil, fmt.Errorf("reading the stack's VERSION failed: %s", out.Tail(3))
	}
	st := parseStackState(out)
	return loc, &st, nil
}

func (s *Service) stackComponent(ctx context.Context, c Component) ComponentStatus {
	cs := ComponentStatus{ID: c.ID, Name: c.Name, About: c.About, Repo: c.Repo, Mode: c.Mode, Present: true,
		Impact: c.Impact, Releases: []Release{}}
	// GitHub first: without a release there is nothing to ask the host.
	rs, err := s.o.Releases.Releases(ctx, c.Repo)
	if err != nil {
		var nr ErrNoReleases
		if errors.As(err, &nr) {
			cs.Error = "No releases published yet."
			cs.Installed = strings.TrimPrefix(s.o.StackVersion, "v")
			return cs
		}
		cs.Error = err.Error()
		if len(rs) == 0 {
			return cs
		}
	}
	var (
		loc   *Location
		state *stackState
	)
	installed := strings.TrimPrefix(s.o.StackVersion, "v")
	if s.o.Jobs != nil && len(rs) > 0 {
		var err error
		loc, state, err = s.stackStateNow(ctx)
		if err != nil {
			cs.Error = joinErr(cs.Error, "can't read the installed stack: "+err.Error())
		} else if state.Version != "" {
			installed = strings.TrimPrefix(state.Version, "v")
		} else {
			installed = ""
		}
	}
	cs.Installed = installed
	newest, ok := Newest(rs)
	if !ok {
		cs.Error = joinErr(cs.Error, "No releases published yet.")
		return cs
	}
	cs.Latest = newest.Version
	cur, isVersion := ParseVersion(installed)
	pre := !isVersion
	switch {
	case pre && state == nil:
		// Without the helper the panel can't read VERSION: show the newest.
		if installed == "" {
			cs.Error = joinErr(cs.Error, "installed version unknown")
		} else {
			cs.Error = joinErr(cs.Error, "installed version “"+installed+"” isn't a release version")
		}
		return cs
	case !pre && !Less(cur, newest.v):
		return cs
	}
	var rng []Release
	if pre {
		// From before stack releases: the newest release's notes only.
		rng = []Release{newest}
		rng[0].Breaking = Breaking(newest, newest.v)
	} else {
		rng = slices.Clone(Between(rs, cur, newest.v))
		MarkBreaking(rng, cur)
	}
	for i := range rng {
		rng[i].Published = true
		if len(rng[i].Breaking) > 0 {
			cs.Breaking = true
			cs.BreakingNotes = append(cs.BreakingNotes, BreakingNotes(rng[i].Body)...)
		}
	}
	cs.Releases, cs.Update = rng, true
	ss := &StackStatus{Version: newest.Version, PreRelease: pre, Files: []StackFile{}, Migrations: []StackMigration{},
		Images: []ImageChange{}, Pins: []PinChange{}, Included: []ImageChange{}, Restarts: []string{}, Impacts: []ItemImpact{},
		AnalyzedAt: s.o.Now().UTC()}
	cs.Stack = ss
	why := s.stackAnalyze(ctx, &cs, ss, newest, loc, state)
	switch {
	case why != "":
		cs.Why = why
	case !s.applyInfo().Enabled:
		cs.Why = s.applyInfo().Why
	default:
		cs.Applyable = true
	}
	return cs
}

// stackAnalyze fills ss for the release r; it returns why the update
// can't be applied ("" = it can).
func (s *Service) stackAnalyze(ctx context.Context, cs *ComponentStatus, ss *StackStatus, r Release, loc *Location, state *stackState) string {
	fail := func(msg string) string {
		ss.Error = msg
		return msg
	}
	src, ok := s.o.Releases.(AssetSource)
	if !ok {
		return fail("this panel can't download release assets")
	}
	mURL, bURL, sURL := r.Assets[assetManifest], r.Assets[bundleAsset(r.Version)], r.Assets[bundleAsset(r.Version)+".sha256"]
	if mURL == "" || bURL == "" || sURL == "" {
		cs.Pending = append(cs.Pending, r.Version)
		return fail("release " + r.Version + " has no bundle (" + bundleAsset(r.Version) + ", its .sha256 and manifest.json) yet")
	}
	if !reURL.MatchString(bURL) {
		return fail("the bundle's download address isn't a plain https URL: " + bURL)
	}
	mb, err := src.Asset(ctx, mURL, maxManifest)
	if err != nil {
		return fail(err.Error())
	}
	m, err := ParseManifest(mb)
	if err != nil {
		return fail(err.Error())
	}
	if strings.TrimPrefix(m.Version, "v") != r.Version {
		return fail(fmt.Sprintf("the release %s's manifest says version %s", r.Version, m.Version))
	}
	sb, err := src.Asset(ctx, sURL, 4096)
	if err != nil {
		return fail(err.Error())
	}
	sum, err := parseSHA256File(sb)
	if err != nil {
		return fail(err.Error())
	}
	ss.bundleURL, ss.bundleSHA = bURL, sum
	ss.MinAdmin = m.MinAdmin
	if mv, ok := ParseVersion(m.MinAdmin); ok {
		if sv, ok := ParseVersion(s.o.SelfVersion); ok && Less(sv, mv) {
			ss.AdminTooOld = true
		}
	}
	// The images the stack builds must be on the registry.
	if s.o.Registry != nil {
		seen := map[string]bool{}
		for _, svc := range sortedServices(m.Services) {
			ms := m.Services[svc]
			if !ms.Built || seen[ms.Image] || ImageTag(ms.Image) == "" {
				continue
			}
			seen[ms.Image] = true
			okTag, err := s.o.Registry.TagExists(ctx, ImageRepo(ms.Image), ImageTag(ms.Image))
			if err != nil {
				cs.Error = joinErr(cs.Error, "registry: "+err.Error())
				continue
			}
			if !okTag {
				cs.Pending = append(cs.Pending, r.Version)
				return fail(ms.Image + " isn't on the registry yet")
			}
		}
	}
	running := s.runningImages(ctx)
	env := map[string]string{}
	if state != nil {
		env = state.EnvImages
		ss.Baseline = state.Baseline
	}
	sets, pins, images := stackPins(m, env, running)
	ss.Pins = pins
	ss.images = images
	ss.mounts = map[string][]string{}
	ss.oneshot = map[string]bool{}
	for svc, ms := range m.Services {
		ss.mounts[svc] = ms.Mounts
		if ms.Oneshot {
			ss.oneshot[svc] = true
		}
	}
	for _, kv := range sets {
		if strings.HasPrefix(kv, "ADMIN_IMAGE=") {
			// The panel's own image moves through its hand-off, last.
			ss.selfImage = strings.TrimPrefix(kv, "ADMIN_IMAGE=")
			continue
		}
		ss.sets = append(ss.sets, kv)
	}
	for _, svc := range sortedServices(m.Services) {
		ms := m.Services[svc]
		from, to := running[svc], images[svc]
		if from == "" || to == "" {
			continue
		}
		ch := ImageChange{Service: svc, Name: svc, From: from, To: to, Changed: from != to && !strings.HasPrefix(from, to+"@")}
		if n := builtNames[svc]; n != "" {
			ch.Name = n
		}
		if ms.Built {
			ss.Included = append(ss.Included, ch)
		}
		if ch.Changed {
			ss.Images = append(ss.Images, ch)
		}
	}
	if ss.AdminTooOld {
		return fail(fmt.Sprintf("this release needs admin panel %s or later (this is %s): update the panel first", m.MinAdmin, s.o.SelfVersion))
	}
	if s.o.Jobs == nil || loc == nil || state == nil {
		// Without the helper: what's known from the manifest only.
		return ""
	}
	// On the host: the bundle staged and checked, then a dry run.
	runID := fmt.Sprintf("check%d", time.Now().UnixNano())
	base := s.env(loc, &Run{ID: runID})
	out, err := s.o.Jobs.Run(ctx, runID, jobStage, scriptStage, with(base, "STACK_VERSION", r.Version,
		"BUNDLE_URL", bURL, "BUNDLE_SHA256", sum), nil)
	if err != nil || out.Exit != 0 {
		return fail("downloading the release bundle on the server failed: " + errOr(err, out))
	}
	stage := stageDir(loc, r.Version)
	out, err = s.o.Jobs.Run(ctx, runID, "stack-plan", scriptStackPlan, with(base, "STAGE_DIR", stage, "STACK_VERSION", r.Version), nil)
	if err != nil || out.Exit != 0 {
		return fail("the dry run of the release on the server failed: " + errOr(err, out))
	}
	p := parseStackPlan(out)
	ss.Unchanged = p.Unchanged
	ss.Files = p.Files
	for i := range ss.Files {
		ss.Files[i].Services = fileServices(m, ss.Files[i].Path, p.New)
	}
	for _, mg := range p.Migrations {
		ss.Migrations = append(ss.Migrations, mg)
	}
	never := s.neverRestart()
	ss.Restarts, ss.Oneshots, ss.Kept, ss.Removed, ss.Self = stackRestarts(m, p, images, running, never)
	if ss.selfImage != "" {
		ss.Self = true
	}
	if ss.Self && ss.selfImage == "" {
		ss.selfImage = running["admin"]
		if ss.selfImage == "" {
			ss.selfImage = images["admin"]
		}
	}
	var comps []Component
	for _, svc := range ss.Restarts {
		name, im := s.stackServiceImpact(svc)
		ss.Impacts = append(ss.Impacts, ItemImpact{Component: svc, Name: name, Impact: im})
		comps = append(comps, Component{ID: svc, Name: name, Impact: im})
	}
	ss.Impact = combinedImpact(comps)
	cs.Impact = ss.Impact
	cs.Services = ss.Restarts
	cs.NeverRestart = ss.Kept
	return ""
}

func errOr(err error, out *JobOutput) string {
	if err != nil {
		return err.Error()
	}
	if out != nil {
		return fmt.Sprintf("exit %d: %s", out.Exit, out.Tail(4))
	}
	return "no output"
}

func stageDir(loc *Location, version string) string {
	return strings.TrimRight(loc.Dir, "/") + "/.boson/staging/" + version
}

// neverRestart: services no update recreates (the catalog's NeverRestart).
func (s *Service) neverRestart() []string {
	var out []string
	for _, c := range s.o.Catalog {
		out = append(out, c.NeverRestart...)
	}
	return uniq(out)
}

// combinedImpact is the worst of several services' impacts.
func combinedImpact(comps []Component) Impact {
	im := Impact{}
	var who []string
	for _, c := range comps {
		x := c.Impact
		im.Restart = im.Restart || x.Restart
		im.Disruptive = im.Disruptive || x.Disruptive
		im.ChatDisconnect = im.ChatDisconnect || x.ChatDisconnect
		im.DropsCalls = im.DropsCalls || x.DropsCalls
		im.Downtime = max(im.Downtime, x.Downtime)
	}
	switch {
	case len(comps) == 0:
		im.Who = "Nothing restarts: only files nothing reads at run time change."
	case im.ChatDisconnect:
		who = append(who, "Everyone on IRC is disconnected briefly (Ergo restarts); clients reconnect automatically.")
	case im.Disruptive:
		who = append(who, "Some people are interrupted briefly; they reconnect automatically.")
	default:
		who = append(who, "Nobody is disconnected; restarted services pause for a few seconds.")
	}
	if len(comps) > 0 {
		names := make([]string, len(comps))
		for i, c := range comps {
			names[i] = c.Name
		}
		sort.Strings(names)
		im.Who = strings.Join(who, " ") + " Restarts: " + strings.Join(uniq(names), ", ") + "."
	}
	return im
}

// stackItem is the plan's item for the stack row (nil when it can't be).
func (s *Service) stackItem(st ComponentStatus) (*Item, []Component) {
	ss := st.Stack
	if ss == nil || ss.bundleURL == "" {
		return nil, nil
	}
	from := st.Installed
	if from == "" {
		from = "pre-release"
	}
	it := &Item{Component: "stack", Name: st.Name, From: from, To: st.Latest, Services: slices.Clone(ss.Restarts)}
	var comps []Component
	pull := map[string]bool{}
	for _, svc := range ss.Restarts {
		for _, c := range s.o.Catalog {
			if slices.Contains(c.Services, svc) {
				it.Volumes = append(it.Volumes, c.Volumes...)
				it.Databases = append(it.Databases, c.Databases...)
				it.Pre = append(it.Pre, c.Pre...)
			}
		}
	}
	// Only images that change: what runs is there already.
	for _, ch := range ss.Images {
		if slices.Contains(ss.Restarts, ch.Service) || slices.Contains(ss.Oneshots, ch.Service) {
			pull[ch.To] = true
		}
	}
	it.Volumes, it.Databases, it.Pre = uniq(it.Volumes), uniq(it.Databases), uniq(it.Pre)
	var oneshot []string
	for svc := range ss.oneshot {
		oneshot = append(oneshot, svc)
	}
	sort.Strings(oneshot)
	it.Stack = &StackItem{Version: ss.Version, BundleURL: ss.bundleURL, BundleSHA256: ss.bundleSHA, Sets: slices.Clone(ss.sets),
		Images: ss.images, Mounts: ss.mounts, Oneshot: oneshot, Never: s.neverRestart(), Planned: slices.Clone(ss.Restarts)}
	for img := range pull {
		it.Stack.Pull = append(it.Stack.Pull, img)
	}
	sort.Strings(it.Stack.Pull)
	for _, im := range ss.Impacts {
		comps = append(comps, Component{ID: im.Component, Name: im.Name, Impact: im.Impact})
	}
	return it, comps
}

// stackSelfItem: the panel's own recreate at the end of a stack update
// (its image pin raised, or its compose configuration changed).
func (s *Service) stackSelfItem(st ComponentStatus) *Item {
	ss := st.Stack
	if ss == nil || !ss.Self || ss.selfImage == "" {
		return nil
	}
	from, _ := s.installedImage("admin")
	to := strings.TrimPrefix(ImageTag(ss.selfImage), "v")
	return &Item{Component: "admin", Name: "Admin panel", From: strings.TrimPrefix(s.o.SelfVersion, "v"), To: to,
		FromImage: from, ToImage: ss.selfImage, EnvVar: "ADMIN_IMAGE", Services: []string{"admin"}, Self: true}
}

// mergeSets: KEY=value lists, later ones win unless the earlier value is
// a newer version of the same image.
func mergeSets(lists ...[]string) []string {
	var keys []string
	val := map[string]string{}
	for _, l := range lists {
		for _, kv := range l {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			old, had := val[k]
			if !had {
				keys = append(keys, k)
				val[k] = v
				continue
			}
			ov, ok1 := ParseVersion(ImageTag(old))
			nv, ok2 := ParseVersion(ImageTag(v))
			if ok1 && ok2 && ImageRepo(old) == ImageRepo(v) && Less(nv, ov) {
				continue
			}
			val[k] = v
		}
	}
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+val[k])
	}
	return out
}

// stackResult is what a stack apply changed, from the job's output.
type stackResult struct {
	// force: recreated although compose's config is the same (a file they
	// read changed, or a migration asked).
	recreate, force, oneshots, kept []string
	files                           []StackFile
	migrations                      []string
	self                            bool
}

// stackApplied: the services to recreate after the files, pins and
// migrations are in place: compose's config hash changed (or the service
// is new), a migration asked for it, or it bind-mounts a file that
// changed. evrd-links never; the panel through its hand-off; one-shot
// services are run, not recreated.
func stackApplied(it *StackItem, p stackPlanOutput) stackResult {
	var r stackResult
	set, same := map[string]bool{}, map[string]bool{}
	for svc, h := range p.New {
		if p.Old[svc] != h {
			set[svc] = true
		}
	}
	for _, svc := range p.Restart {
		if _, ok := p.New[svc]; ok && !set[svc] {
			set[svc], same[svc] = true, true
		}
	}
	for _, f := range p.Files {
		f.Diff = nil
		r.files = append(r.files, f)
		for svc, ms := range it.Mounts {
			if _, ok := p.New[svc]; ok && slices.Contains(ms, f.Path) && !set[svc] {
				set[svc], same[svc] = true, true
			}
		}
	}
	for _, m := range p.Migrations {
		if m.State == "applied" {
			r.migrations = append(r.migrations, m.Name)
		}
	}
	for svc := range set {
		switch {
		case slices.Contains(it.Never, svc):
			r.kept = append(r.kept, svc)
		case svc == "admin":
			r.self = true
		case slices.Contains(it.Oneshot, svc):
			r.oneshots = append(r.oneshots, svc)
		default:
			r.recreate = append(r.recreate, svc)
			if same[svc] {
				r.force = append(r.force, svc)
			}
		}
	}
	sort.Strings(r.recreate)
	sort.Strings(r.force)
	sort.Strings(r.oneshots)
	sort.Strings(r.kept)
	return r
}

// preFor: the catalog's one-off services (migrations) of the services.
func (s *Service) preFor(services []string) []string {
	var out []string
	for _, c := range s.o.Catalog {
		for _, svc := range c.Services {
			if slices.Contains(services, svc) {
				out = append(out, c.Pre...)
			}
		}
	}
	return uniq(out)
}

// selfRecreateItem: the panel recreated on the image it runs (its compose
// configuration changed with the stack).
func (s *Service) selfRecreateItem() *Item {
	img, ok := s.installedImage("admin")
	if !ok || img == "" {
		return nil
	}
	v := strings.TrimPrefix(s.o.SelfVersion, "v")
	return &Item{Component: "admin", Name: "Admin panel", From: v, To: v, FromImage: img, ToImage: img,
		EnvVar: "ADMIN_IMAGE", Services: []string{"admin"}, Self: true}
}

// stackChecks: after a stack update (or its rollback), the installed
// release is the expected one and the recreated services run their new
// images.
func (s *Service) stackChecks(ctx context.Context, run *Run, rollback bool) []Check {
	var out []Check
	for _, it := range run.Items {
		if it.Stack == nil {
			continue
		}
		want := it.To
		if rollback {
			want = it.From
		}
		if want == "pre-release" {
			want = ""
		}
		_, st, err := s.stackStateNow(ctx)
		c := Check{ID: "version-stack", Label: "Stack release"}
		switch {
		case err != nil:
			c.Detail = err.Error()
		case st.Version == want:
			c.OK, c.Detail = true, "VERSION "+orNone(st.Version)
		default:
			c.Detail = fmt.Sprintf("VERSION %s, expected %s", orNone(st.Version), orNone(want))
		}
		out = append(out, c)
		if rollback {
			continue
		}
		running := s.runningImages(ctx)
		for _, svc := range it.Stack.Recreated {
			img := it.Stack.Images[svc]
			if img == "" || running[svc] == "" {
				continue
			}
			ok := running[svc] == img || strings.HasPrefix(running[svc], img+"@")
			out = append(out, Check{ID: "image-" + svc, Label: svc + " image", OK: ok,
				Detail: fmt.Sprintf("runs %s", running[svc]) + map[bool]string{true: "", false: ", expected " + img}[ok]})
		}
	}
	return out
}

func orNone(v string) string {
	if v == "" {
		return "none (pre-release)"
	}
	return v
}
