package fakestack

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"path"
	"sort"
	"strings"
)

// Stack releases in the fake: GitHub releases of boson-chat/stack with
// their assets, and the helper jobs that stage, dry-run and apply them
// (the real scripts run the bundle's scripts/stack-apply.sh; see
// internal/services/updates/stack.go).

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (r *StackRelease) bundleName() string { return "boson-stack-" + r.Version + ".tar.gz" }

func (s *Stack) stackGitHub() []Release {
	var out []Release
	for i := len(s.StackReleases) - 1; i >= 0; i-- {
		r := s.StackReleases[i]
		rel := Release{Tag: "v" + r.Version, Name: "v" + r.Version, Body: r.Body, At: "2026-11-27T10:00:00Z",
			URL: "https://github.com/boson-chat/stack/releases/tag/v" + r.Version, Assets: []Asset{}}
		if !r.NoAssets {
			for _, n := range []string{r.bundleName(), r.bundleName() + ".sha256", "manifest.json"} {
				rel.Assets = append(rel.Assets, Asset{Name: n, URL: s.Server + "/assets/" + r.Version + "/" + n})
			}
		}
		out = append(out, rel)
	}
	return out
}

func (s *Stack) stackRelease(v string) *StackRelease {
	for _, r := range s.StackReleases {
		if r.Version == v {
			return r
		}
	}
	return nil
}

func (s *Stack) asset(w http.ResponseWriter, _ *http.Request, p string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, name, _ := strings.Cut(p, "/")
	r := s.stackRelease(v)
	if r == nil || r.NoAssets {
		http.NotFound(w, nil)
		return
	}
	switch name {
	case "manifest.json":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(r.Manifest))
	case r.bundleName():
		_, _ = w.Write(r.Bundle)
	case r.bundleName() + ".sha256":
		h := sum(r.Bundle)
		if r.BadSHA {
			h = sum([]byte("something else"))
		}
		_, _ = w.Write([]byte(h + "  " + r.bundleName() + "\n"))
	default:
		http.NotFound(w, nil)
	}
}

// active: the services with a container, as compose config --hash lists
// the active profiles' services.
func (s *Stack) active() []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range s.cts {
		if c.job == "" && !seen[c.Service] {
			seen[c.Service] = true
			out = append(out, c.Service)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Stack) hash(svc string) string {
	if h := s.Hashes[svc]; h != "" {
		return h
	}
	return "h0-" + svc
}

func (s *Stack) printHashes(c *container, label string, hashes map[string]string) {
	for _, svc := range s.active() {
		h := s.hash(svc)
		if hashes != nil && hashes[svc] != "" {
			h = hashes[svc]
		}
		s.say(c, "@@HASH %s %s %s", label, svc, h)
	}
	// New services of the release (no container yet).
	if hashes != nil {
		var extra []string
		for svc := range hashes {
			if s.svc(svc) == nil {
				extra = append(extra, svc)
			}
		}
		sort.Strings(extra)
		for _, svc := range extra {
			s.say(c, "@@HASH %s %s %s", label, svc, hashes[svc])
		}
	}
}

func (s *Stack) isApplied(m string) bool {
	for _, a := range s.Applied {
		if a == m {
			return true
		}
	}
	return false
}

func (s *Stack) migrationLines(c *container, r *StackRelease, plan bool) bool {
	names := make([]string, 0, len(r.Migrations))
	for n := range r.Migrations {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		switch {
		case s.isApplied(n):
			s.say(c, "@@MIGRATION %s done", n)
		case plan:
			s.say(c, "@@MIGRATION %s pending %s", n, r.Migrations[n])
		case r.FailMigration:
			s.say(c, "@@MIGRATION %s failed", n)
			s.say(c, "stack-apply.sh: migration %s failed (exit 1)", n)
			return false
		default:
			s.Applied = append(s.Applied, n)
			s.say(c, "@@MIGRATION %s applied", n)
		}
	}
	return true
}

// stackJob runs the stack jobs; false = not one of them.
func (s *Stack) stackJob(c *container) bool {
	e := c.env
	fail := func(msg string) {
		s.say(c, "%s", msg)
		c.State, c.Exit = "exited", 1
	}
	switch c.job {
	case "stack-state":
		v := s.StackVersion
		if v == "" {
			v = "none"
		}
		s.say(c, "@@STACKVERSION %s", v)
		if s.StackBaseline {
			s.say(c, "@@BASELINE yes")
		} else {
			s.say(c, "@@BASELINE no")
		}
		for _, a := range s.Applied {
			s.say(c, "@@APPLIED %s", a)
		}
		keys := make([]string, 0, len(s.Env))
		for k := range s.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if strings.HasSuffix(k, "_IMAGE") {
				s.say(c, "@@ENVIMG %s=%s", k, s.Env[k])
			}
		}
		s.say(c, "@@PROFILES %s", s.Profiles)
		s.say(c, "@@RESULT stack-state {}")
	case "stage":
		s.StageNetworks = append(s.StageNetworks, c.network)
		r := s.stackRelease(e["STACK_VERSION"])
		if r == nil {
			fail("wget: server returned error: HTTP/1.1 404 Not Found")
			return true
		}
		s.say(c, "@@STEP downloading the stack release %s", r.Version)
		s.Downloads++
		if sum(r.Bundle) != e["BUNDLE_SHA256"] {
			fail("the downloaded bundle doesn't match its SHA-256 (" + e["BUNDLE_SHA256"] + ")")
			return true
		}
		s.say(c, `@@RESULT stage {"dir":"%s"}`, e["STACK_DIR"]+"/.boson/staging/"+r.Version)
	case "stack-plan", "stack-apply":
		r := s.stackRelease(path.Base(e["STAGE_DIR"]))
		if r == nil {
			fail("stack-apply.sh: " + e["STAGE_DIR"] + " is not a stack bundle (no .bundle/manifest.json)")
			return true
		}
		if c.job == "stack-plan" {
			for _, f := range r.Files {
				s.say(c, "@@STACKFILE %s", f)
				if a, p, ok := strings.Cut(f, " "); ok && a != "same" {
					s.say(c, "@@DIFF %s", p)
					for _, l := range r.Diffs[p] {
						s.say(c, "@@D %s", l)
					}
				}
			}
			s.migrationLines(c, r, true)
			s.printHashes(c, "old", nil)
			s.printHashes(c, "new", r.Hashes)
			s.say(c, "@@RESULT stack-plan {}")
			c.State = "exited"
			return true
		}
		s.printHashes(c, "old", nil)
		for _, f := range r.Files {
			s.say(c, "@@STACKFILE %s", f)
		}
		s.StackVersion, s.StackBaseline = r.Version, true
		before := map[string]string{}
		for _, svc := range s.active() {
			before[svc] = s.effective(svc)
		}
		for svc, img := range r.Defaults {
			s.Defaults[svc] = img
		}
		s.setenv(c, e["SETS"])
		if !s.migrationLines(c, r, false) {
			c.State, c.Exit = "exited", 1
			return true
		}
		for _, svc := range r.Restart {
			s.say(c, "@@RESTART %s", svc)
		}
		for svc, h := range r.Hashes {
			s.Hashes[svc] = h
		}
		// An image change is a config change.
		for _, svc := range s.active() {
			if img := s.effective(svc); img != before[svc] {
				s.Hashes[svc] = s.hash(svc) + "~" + img
			}
		}
		s.printHashes(c, "new", nil)
		for svc, h := range r.Hashes {
			if s.svc(svc) == nil {
				s.say(c, "@@HASH new %s %s", svc, h)
			}
		}
		s.say(c, "@@RESULT stack-apply {}")
	default:
		return false
	}
	c.State = "exited"
	return true
}

// effective: the image compose would run a service with now: .env's value
// for its image's repository, else the compose default.
func (s *Stack) effective(svc string) string {
	// Two services on one repository (evrd, evrd-links) have their own variables.
	if v, ok := map[string]string{"evrd": "EVRD_IMAGE", "evrd-links": "EVRD_LINKS_IMAGE"}[svc]; ok {
		if img := s.Env[v]; img != "" {
			return img
		}
		if d := s.Defaults[svc]; d != "" {
			return d
		}
	}
	c := s.svc(svc)
	if c != nil {
		repo := repoOf(c.Image)
		for _, v := range s.Env {
			if repoOf(v) == repo {
				return v
			}
		}
	}
	if d := s.Defaults[svc]; d != "" {
		return d
	}
	if c != nil {
		return c.Image
	}
	return ""
}

func repoOf(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	return ref
}

func (s *Stack) snapStack() *stackSnap {
	return &stackSnap{version: s.StackVersion, baseline: s.StackBaseline, applied: append([]string(nil), s.Applied...),
		hashes: cp(s.Hashes), defaults: cp(s.Defaults)}
}

func (s *Stack) restoreStack(x *stackSnap) {
	s.StackVersion, s.StackBaseline, s.Applied = x.version, x.baseline, append([]string(nil), x.applied...)
	s.Hashes, s.Defaults = cp(x.hashes), cp(x.defaults)
}

// AddService starts a service container (call inside Set).
func (s *Stack) AddService(svc, image string) { s.add(svc, image) }

// RunImage replaces a service's container with one running image (call
// inside Set).
func (s *Stack) RunImage(svc, image string) {
	if c := s.svc(svc); c != nil {
		delete(s.cts, c.ID)
	}
	s.add(svc, image)
}
