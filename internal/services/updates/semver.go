package updates

import (
	"strconv"
	"strings"
)

// Version is a semantic version (semver 2.0). Tags may carry a leading "v".
type Version struct {
	Major, Minor, Patch int
	// Pre is the pre-release part ("rc.1", "boson.2"), without the dash.
	Pre string
	// Raw is the text it was parsed from, without the "v".
	Raw string
}

// ParseVersion reads "1.2.3", "v1.2.3", "1.2.3-rc.1+build" or "2.19" (a
// missing patch is 0). ok is false for anything else ("latest", "dev").
func ParseVersion(s string) (Version, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	raw := s
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	var pre string
	if i := strings.IndexByte(s, '-'); i >= 0 {
		s, pre = s[:i], s[i+1:]
		if pre == "" {
			return Version{}, false
		}
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return Version{}, false
	}
	var n [3]int
	for i, p := range parts {
		if p == "" || (len(p) > 1 && p[0] == '0') {
			return Version{}, false
		}
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 {
			return Version{}, false
		}
		n[i] = v
	}
	return Version{Major: n[0], Minor: n[1], Patch: n[2], Pre: pre, Raw: raw}, true
}

// String is the canonical form without "v" (build metadata dropped).
func (v Version) String() string {
	s := strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Compare returns -1, 0 or 1. A pre-release sorts before its release
// (1.0.0-rc.1 < 1.0.0); pre-release identifiers compare numerically when
// both are numbers, else as text, and a longer list wins a tie.
func Compare(a, b Version) int {
	for _, d := range [3][2]int{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}} {
		if d[0] != d[1] {
			if d[0] < d[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case a.Pre == b.Pre:
		return 0
	case a.Pre == "":
		return 1
	case b.Pre == "":
		return -1
	}
	ap, bp := strings.Split(a.Pre, "."), strings.Split(b.Pre, ".")
	for i := 0; i < len(ap) && i < len(bp); i++ {
		if ap[i] == bp[i] {
			continue
		}
		an, aerr := strconv.Atoi(ap[i])
		bn, berr := strconv.Atoi(bp[i])
		switch {
		case aerr == nil && berr == nil:
			if an < bn {
				return -1
			}
			return 1
		case aerr == nil: // numeric identifiers sort first
			return -1
		case berr == nil:
			return 1
		case ap[i] < bp[i]:
			return -1
		default:
			return 1
		}
	}
	switch {
	case len(ap) < len(bp):
		return -1
	case len(ap) > len(bp):
		return 1
	}
	return 0
}

// Less is Compare(a, b) < 0.
func Less(a, b Version) bool { return Compare(a, b) < 0 }

// ImageTag is the tag of an image reference: "1.2.3" for
// "harbor.floret.dev/boson/auth:1.2.3" and for "…/auth:1.2.3@sha256:…";
// "" without a tag.
func ImageTag(ref string) string {
	if i := strings.IndexByte(ref, '@'); i >= 0 {
		ref = ref[:i]
	}
	last := ref
	if i := strings.LastIndexByte(ref, '/'); i >= 0 {
		last = ref[i+1:]
	}
	if _, tag, ok := strings.Cut(last, ":"); ok {
		return tag
	}
	return ""
}

// ImageRepo is the reference without tag and digest.
func ImageRepo(ref string) string {
	if i := strings.IndexByte(ref, '@'); i >= 0 {
		ref = ref[:i]
	}
	slash := strings.LastIndexByte(ref, '/')
	if i := strings.LastIndexByte(ref, ':'); i > slash {
		return ref[:i]
	}
	return ref
}
