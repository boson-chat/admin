package updates

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

// Release is one GitHub release (made by semantic-release: the body is the
// markdown changelog of that version).
type Release struct {
	Tag         string    `json:"tag"`
	Version     string    `json:"version"`
	Name        string    `json:"name,omitempty"`
	Body        string    `json:"body"`
	URL         string    `json:"url,omitempty"`
	PublishedAt time.Time `json:"published_at"`
	// Breaking lists why this release needs attention ("major version",
	// "BREAKING CHANGE", "manual steps"); empty when it doesn't.
	Breaking []string `json:"breaking,omitempty"`
	// Published: the image for this version is in the registry (an update
	// to it can be applied). Unknown (not checked) counts as published.
	Published bool `json:"published"`

	v Version
}

// SortReleases parses the tags, drops what isn't a version, and sorts newest first.
func SortReleases(in []Release) []Release {
	out := make([]Release, 0, len(in))
	for _, r := range in {
		v, ok := ParseVersion(r.Tag)
		if !ok {
			continue
		}
		r.v, r.Version = v, v.String()
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return Less(out[j].v, out[i].v) })
	return out
}

// Between returns the releases newer than installed and no newer than
// target, newest first: every changelog an update from installed to target
// brings. Pre-releases are left out unless installed or target is one.
// releases must be sorted (SortReleases).
func Between(releases []Release, installed, target Version) []Release {
	var out []Release
	pre := installed.Pre != "" || target.Pre != ""
	for _, r := range releases {
		if r.v.Pre != "" && !pre {
			continue
		}
		if Less(installed, r.v) && !Less(target, r.v) {
			out = append(out, r)
		}
	}
	return out
}

// Newest is the newest stable release, or the newest at all when there are
// only pre-releases.
func Newest(releases []Release) (Release, bool) {
	for _, r := range releases {
		if r.v.Pre == "" {
			return r, true
		}
	}
	if len(releases) > 0 {
		return releases[0], true
	}
	return Release{}, false
}

var (
	breakingRe = regexp.MustCompile(`(?im)(BREAKING[ -]CHANGES?|⚠\s*BREAKING|^#+\s*breaking)`)
	// A release note that asks the operator to do something by hand.
	manualRe = regexp.MustCompile(`(?i)(manual (step|steps|action|migration|intervention)|action required|requires? (a )?manual|upgrade (notes?|guide|instructions)|migration (guide|steps|required)|before (upgrading|updating)|after (upgrading|updating)[, ]+(run|set|update|change|edit)|you (must|need to|have to) (run|set|update|change|edit|add|remove|migrate))`)
)

// Breaking says why a release needs attention before it's applied: a new
// major version (prev is the version it follows), a "BREAKING CHANGE" in its
// changelog, or a note asking for manual steps.
func Breaking(r Release, prev Version) []string {
	var why []string
	v, ok := ParseVersion(r.Tag)
	if ok && v.Major > prev.Major {
		why = append(why, "major version")
	}
	if breakingRe.MatchString(r.Body) {
		why = append(why, "breaking change")
	}
	if manualRe.MatchString(r.Body) {
		why = append(why, "manual steps")
	}
	return why
}

// MarkBreaking fills Breaking on a newest-first range that follows installed.
func MarkBreaking(rs []Release, installed Version) {
	for i := range rs {
		prev := installed
		if i+1 < len(rs) {
			prev = rs[i+1].v
		}
		rs[i].Breaking = Breaking(rs[i], prev)
	}
}

// BreakingNotes pulls the BREAKING CHANGE paragraphs out of a changelog body
// so they can be shown on their own (semantic-release writes them as a
// "### ⚠ BREAKING CHANGES" section or "BREAKING CHANGE: …" lines).
func BreakingNotes(body string) []string {
	var out []string
	lines := strings.Split(body, "\n")
	in := false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, "#"):
			in = breakingRe.MatchString(t)
			continue
		case in && t != "":
			out = append(out, strings.TrimSpace(strings.TrimLeft(t, "*-")))
		case !in:
			if i := strings.Index(strings.ToUpper(t), "BREAKING CHANGE:"); i >= 0 {
				out = append(out, strings.TrimSpace(t[i+len("BREAKING CHANGE:"):]))
			}
		}
	}
	return out
}
