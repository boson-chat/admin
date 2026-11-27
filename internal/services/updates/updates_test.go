package updates

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseAndCompareVersions(t *testing.T) {
	for _, s := range []string{"1.2.3", "v1.2.3", "1.2.3-rc.1", "1.1.0-boson.2", "2.19", "0.57.3+build.5"} {
		if _, ok := ParseVersion(s); !ok {
			t.Errorf("ParseVersion(%q) failed", s)
		}
	}
	for _, s := range []string{"", "latest", "dev", "1", "1.2.3.4", "01.2.3", "1.2.x", "1.2.3-"} {
		if _, ok := ParseVersion(s); ok {
			t.Errorf("ParseVersion(%q) should fail", s)
		}
	}
	ordered := []string{"0.9.9", "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta.2", "1.0.0-beta.11",
		"1.0.0-rc.1", "1.0.0", "1.0.1", "1.2.0", "1.10.0", "2.0.0"}
	for i := 0; i+1 < len(ordered); i++ {
		a, _ := ParseVersion(ordered[i])
		b, _ := ParseVersion(ordered[i+1])
		if Compare(a, b) != -1 || Compare(b, a) != 1 {
			t.Errorf("%s should sort before %s", ordered[i], ordered[i+1])
		}
	}
	a, _ := ParseVersion("v1.2.3")
	b, _ := ParseVersion("1.2.3+meta")
	if Compare(a, b) != 0 {
		t.Error("build metadata and v don't count")
	}
	if v, _ := ParseVersion("2.19"); v.String() != "2.19.0" {
		t.Errorf("String = %s", v.String())
	}
}

func TestImageRefs(t *testing.T) {
	for ref, want := range map[string][2]string{
		"harbor.floret.dev/boson/auth:1.2.3":            {"harbor.floret.dev/boson/auth", "1.2.3"},
		"harbor.floret.dev/boson/auth:1.2.3@sha256:abc": {"harbor.floret.dev/boson/auth", "1.2.3"},
		"localhost:5000/auth":                           {"localhost:5000/auth", ""},
		"postgres:18-alpine":                            {"postgres", "18-alpine"},
		"harbor.floret.dev/boson/auth@sha256:abc":       {"harbor.floret.dev/boson/auth", ""},
	} {
		if r, tag := ImageRepo(ref), ImageTag(ref); r != want[0] || tag != want[1] {
			t.Errorf("%s: repo %q tag %q, want %v", ref, r, tag, want)
		}
	}
}

func rels(tags ...string) []Release {
	var out []Release
	for _, t := range tags {
		out = append(out, Release{Tag: t, Body: "notes " + t})
	}
	return SortReleases(out)
}

func versions(rs []Release) string {
	var v []string
	for _, r := range rs {
		v = append(v, r.Version)
	}
	return strings.Join(v, ",")
}

func TestChangelogRange(t *testing.T) {
	// Out of order, with junk tags and a pre-release.
	rs := rels("v1.20.0", "v1.22.0", "nightly", "v1.21.0", "v1.23.0-rc.1", "v1.19.0", "v1.21.1", "v1.20.1")
	if got := versions(rs); got != "1.23.0-rc.1,1.22.0,1.21.1,1.21.0,1.20.1,1.20.0,1.19.0" {
		t.Fatalf("sorted = %s", got)
	}
	n, _ := Newest(rs)
	if n.Version != "1.22.0" {
		t.Fatalf("newest stable = %s", n.Version)
	}
	inst, _ := ParseVersion("1.20.0")
	if got := versions(Between(rs, inst, n.v)); got != "1.22.0,1.21.1,1.21.0,1.20.1" {
		t.Fatalf("4 behind: %s", got)
	}
	tgt, _ := ParseVersion("1.21.0")
	if got := versions(Between(rs, inst, tgt)); got != "1.21.0,1.20.1" {
		t.Fatalf("to an older target: %s", got)
	}
	// Installed ahead or equal: nothing.
	cur, _ := ParseVersion("1.22.0")
	if got := Between(rs, cur, n.v); len(got) != 0 {
		t.Fatalf("up to date: %v", versions(got))
	}
	// Installed version that was never released (a hotfix build) still works.
	odd, _ := ParseVersion("1.20.5")
	if got := versions(Between(rs, odd, n.v)); got != "1.22.0,1.21.1,1.21.0" {
		t.Fatalf("from 1.20.5: %s", got)
	}
	// Pre-releases count when going to one.
	rc, _ := ParseVersion("1.23.0-rc.1")
	if got := versions(Between(rs, cur, rc)); got != "1.23.0-rc.1" {
		t.Fatalf("to the rc: %s", got)
	}
}

func TestBreakingDetection(t *testing.T) {
	prev, _ := ParseVersion("1.9.0")
	cases := []struct {
		tag, body string
		want      string
	}{
		{"v1.10.0", "### Features\n\n* nice", ""},
		{"v2.0.0", "### Features\n\n* nice", "major version"},
		{"v1.10.0", "### ⚠ BREAKING CHANGES\n\n* the config moved", "breaking change"},
		{"v1.10.0", "* foo\n\nBREAKING CHANGE: AUTH_KEY is required", "breaking change"},
		{"v1.10.0", "Before upgrading, run `auth migrate keys`.", "manual steps"},
		{"v1.10.0", "This release needs a manual step: set FILES_HOST.", "manual steps"},
		{"v1.10.0", "* breaking news ticker fix", ""},
		{"v2.0.0", "## ⚠ BREAKING CHANGES\n\n* drop v1 API\n\nYou must update the web client too.", "major version,breaking change,manual steps"},
	}
	for _, c := range cases {
		got := strings.Join(Breaking(Release{Tag: c.tag, Body: c.body}, prev), ",")
		if got != c.want {
			t.Errorf("%s %q: %q, want %q", c.tag, c.body, got, c.want)
		}
	}
	// Major is relative to the release before it in the range.
	rs := rels("v3.0.0", "v2.1.0", "v2.0.0")
	inst, _ := ParseVersion("1.5.0")
	MarkBreaking(rs, inst)
	if len(rs[0].Breaking) != 1 || len(rs[1].Breaking) != 0 || len(rs[2].Breaking) != 1 {
		t.Fatalf("marks: %+v", rs)
	}
	notes := BreakingNotes("## 2.0.0\n\n### ⚠ BREAKING CHANGES\n\n* drop the v1 API\n* rename X\n\n### Features\n\n* y\n\nBREAKING CHANGE: also this")
	if strings.Join(notes, "|") != "drop the v1 API|rename X|also this" {
		t.Fatalf("notes = %q", notes)
	}
}

func comps(ids ...string) []Component {
	var out []Component
	for _, id := range ids {
		c, ok := ByID(id)
		if !ok {
			panic(id)
		}
		out = append(out, c)
	}
	return out
}

func TestImpactSummary(t *testing.T) {
	for _, c := range []struct {
		ids  []string
		want string
	}{
		{nil, "Nothing selected."},
		{[]string{"ergo", "auth"}, "This update will disconnect all chat users for ~10 seconds (clients reconnect automatically). Auth pauses for a few seconds."},
		{[]string{"voice", "video"}, "This update will drop ongoing voice and video calls for ~5 seconds."},
		{[]string{"ergo", "voice", "files"}, "This update will disconnect all chat users for ~10 seconds (clients reconnect automatically), drop ongoing voice and video calls for ~5 seconds and make uploads in progress fail."},
		{[]string{"web"}, "Nobody is disconnected. Web client users get the new version the next time they load it."},
		{[]string{"auth", "api", "admin"}, "Nobody is disconnected. Auth and API pause for a few seconds. The panel reloads at the end."},
		{[]string{"webircgateway"}, "This update will disconnect web client users from chat for ~5 seconds (they reconnect automatically)."},
		{[]string{"files"}, "This update will make uploads in progress fail."},
	} {
		if got := Summary(comps(c.ids...)); got != c.want {
			t.Errorf("%v:\n got %q\nwant %q", c.ids, got, c.want)
		}
	}
	keep, skipped := NonDisruptive(comps("ergo", "auth", "voice", "web", "files", "admin", "evrd"))
	var k, s []string
	for _, c := range keep {
		k = append(k, c.ID)
	}
	for _, c := range skipped {
		s = append(s, c.ID)
	}
	if strings.Join(k, ",") != "auth,web,admin,evrd" || strings.Join(s, ",") != "ergo,voice,files" {
		t.Fatalf("keep %v skipped %v", k, s)
	}
}

// The impact table: every component says who is affected, restart-needing
// ones have a downtime, and the evrd links holder is never restarted.
func TestCatalogImpactTable(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Catalog {
		if seen[c.ID] {
			t.Fatalf("duplicate %s", c.ID)
		}
		seen[c.ID] = true
		if c.Impact.Who == "" || c.Repo == "" {
			t.Errorf("%s: needs Who and Repo", c.ID)
		}
		if c.Impact.Restart && c.Impact.Downtime <= 0 {
			t.Errorf("%s restarts but has no downtime", c.ID)
		}
		if c.Mode == ModeImage && (c.EnvVar == "" || c.Image == "" || len(c.Services) == 0) {
			t.Errorf("%s: image mode needs EnvVar, Image and Services", c.ID)
		}
		for _, n := range c.NeverRestart {
			for _, x := range Catalog {
				for _, s := range x.Services {
					if s == n {
						t.Errorf("%s is never restarted but %s recreates it", n, x.ID)
					}
				}
			}
		}
	}
	for _, id := range []string{"stack", "ergo", "auth", "api", "web", "voice", "video", "files", "directory", "webircgateway", "admin", "evrd", "pushrelay"} {
		if !seen[id] {
			t.Errorf("catalog misses %s", id)
		}
	}
	if Catalog[len(Catalog)-1].ID != "admin" {
		t.Error("the panel updates itself last")
	}
	if e, _ := ByID("evrd"); !e.AddOn || len(e.NeverRestart) == 0 {
		t.Error("evrd is an add-on whose links holder is never restarted")
	}
	if e, _ := ByID("pushrelay"); !e.AddOn || e.Mode != ModeImage || e.EnvVar != "PUSHRELAY_IMAGE" || e.Impact.ChatDisconnect {
		t.Error("the push relay is an image add-on shown only where it runs, and chat stays up")
	}
	if e, _ := ByID("ergo"); !e.Impact.ChatDisconnect || e.Impact.Downtime != 10*time.Second {
		t.Error("ergo disconnects everyone")
	}
}

func TestMigrated(t *testing.T) {
	before := map[string]string{"auth public.schema_migrations": "a", "api public.goose_db_version": "b"}
	after := map[string]string{"auth public.schema_migrations": "a", "api public.goose_db_version": "c", "files public.schema_migrations": "z"}
	if got := strings.Join(migrated(before, after), ","); got != "api,files" {
		t.Fatalf("migrated = %s", got)
	}
	if len(migrated(before, before)) != 0 {
		t.Fatal("same fingerprint: nothing ran")
	}
}

func TestPinnedDigest(t *testing.T) {
	b := &Backup{Images: []ImageRecord{{Service: "auth", Ref: "harbor.floret.dev/boson/auth:1.20.0", ID: "sha256:1",
		Digests: []string{"other/auth@sha256:zzz", "harbor.floret.dev/boson/auth@sha256:abc"}}}}
	it := Item{Services: []string{"auth"}, FromImage: "x"}
	if got := pinned(b, it); got != "harbor.floret.dev/boson/auth:1.20.0@sha256:abc" {
		t.Fatalf("pinned = %s", got)
	}
	// No digest (a locally built image): the tag.
	b.Images[0].Digests = nil
	if got := pinned(b, it); got != "harbor.floret.dev/boson/auth:1.20.0" {
		t.Fatalf("pinned = %s", got)
	}
}

func TestValidEnv(t *testing.T) {
	ok := map[string]string{"STACK_DIR": "/srv/boson", "ENV_FILE": "/srv/boson/.env", "COMPOSE_FILE": "/srv/boson/compose.yaml:/srv/boson/compose.override.yaml",
		"SERVICES": "auth api-migrate", "SETS": "AUTH_IMAGE=harbor.floret.dev/boson/auth:1.2.3@sha256:abc", "KEEP": "5", "COMPOSE_PROJECT_NAME": "boson"}
	if err := validEnv(ok); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"STACK_DIR": "relative", "ENV_FILE": "/srv/$(reboot)", "SERVICES": "auth;rm", "SETS": "AUTH_IMAGE=x y`z`",
		"KEEP": "0", "BACKUP_DIR": "/srv/../etc", "FOO": "bar",
	} {
		bad := map[string]string{k: v}
		if k == "SETS" {
			bad[k] = "AUTH_IMAGE=a'b"
		}
		if err := validEnv(bad); err == nil {
			t.Errorf("%s=%q should be refused", k, v)
		}
	}
}

func TestResolveComposeFiles(t *testing.T) {
	const dir = "/opt/boson-stack"
	p := func(n string) string { return dir + "/" + n }
	on := func(files ...string) func(string) bool {
		return func(f string) bool { return slices.Contains(files, f) }
	}
	for _, tc := range []struct {
		name             string
		labelled, exists []string
		want, missing    []string
		err              string
	}{
		{name: "override deleted after the containers were created",
			labelled: []string{p("compose.yaml"), p("compose.override.yaml")}, exists: []string{p("compose.yaml")},
			want: []string{p("compose.yaml")}, missing: []string{p("compose.override.yaml")}},
		{name: "default set as it is",
			labelled: []string{p("compose.yaml"), p("compose.override.yaml")}, exists: []string{p("compose.yaml"), p("compose.override.yaml")},
			want: []string{p("compose.yaml"), p("compose.override.yaml")}},
		{name: "override added after the containers were created",
			labelled: []string{p("compose.yaml")}, exists: []string{p("compose.yaml"), p("compose.override.yaml")},
			want: []string{p("compose.yaml"), p("compose.override.yaml")}},
		{name: "override renamed to .yml",
			labelled: []string{p("compose.yaml"), p("compose.override.yaml")}, exists: []string{p("compose.yaml"), p("compose.override.yml")},
			want: []string{p("compose.yaml"), p("compose.override.yml")}, missing: []string{p("compose.override.yaml")}},
		{name: "no label: compose.yaml",
			exists: []string{p("compose.yaml")}, want: []string{p("compose.yaml")}},
		{name: "custom set kept in order, nothing added",
			labelled: []string{p("compose.yaml"), p("compose.auth-src.yaml")},
			exists:   []string{p("compose.yaml"), p("compose.auth-src.yaml"), p("compose.override.yaml")},
			want:     []string{p("compose.yaml"), p("compose.auth-src.yaml")}},
		{name: "custom set with a deleted file",
			labelled: []string{p("compose.yaml"), p("compose.auth-src.yaml"), p("compose.override.yaml"), "/etc/boson/extra.yaml"},
			exists:   []string{p("compose.yaml"), p("compose.auth-src.yaml"), "/etc/boson/extra.yaml"},
			want:     []string{p("compose.yaml"), p("compose.auth-src.yaml"), "/etc/boson/extra.yaml"}, missing: []string{p("compose.override.yaml")}},
		{name: "compose.yaml missing",
			labelled: []string{p("compose.yaml"), p("compose.override.yaml")}, exists: []string{p("compose.override.yaml")},
			err: "compose file /opt/boson-stack/compose.yaml is missing"},
		{name: "nothing left",
			labelled: []string{p("compose.yaml")}, err: "is missing"},
		{name: "custom main file missing",
			labelled: []string{p("compose.prod.yaml"), p("compose.auth-src.yaml")}, exists: []string{p("compose.auth-src.yaml"), p("compose.yaml")},
			err: "compose file /opt/boson-stack/compose.prod.yaml is missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, missing, err := resolveComposeFiles(dir, tc.labelled, on(tc.exists...))
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) || !slices.Equal(missing, tc.missing) {
				t.Fatalf("files %v missing %v, want %v missing %v", got, missing, tc.want, tc.missing)
			}
		})
	}
}

func TestComposeCandidates(t *testing.T) {
	const dir = "/srv/boson"
	got := composeCandidates(dir, []string{dir + "/compose.yaml"})
	if !slices.Contains(got, dir+"/compose.override.yaml") || got[0] != dir+"/compose.yaml" {
		t.Fatalf("a default set probes the override names: %v", got)
	}
	custom := []string{dir + "/compose.yaml", dir + "/compose.auth-src.yaml"}
	if got := composeCandidates(dir, custom); !slices.Equal(got, custom) {
		t.Fatalf("a custom set probes only its own files: %v", got)
	}
}

func TestWaitRechecksPendingSoon(t *testing.T) {
	s := New(Options{Releases: nil, Interval: 6 * time.Hour})
	now := time.Now()
	due := now.Add(6 * time.Hour)
	none := &Status{Components: []ComponentStatus{{ID: "web"}}}
	waiting := &Status{Components: []ComponentStatus{{ID: "auth"}, {ID: "web", Pending: []string{"0.104.2"}}}}
	if w := s.wait(none, due, 0, now); w != 6*time.Hour {
		t.Fatalf("nothing waiting: %s", w)
	}
	if w := s.wait(waiting, due, 0, now); w != 2*time.Minute {
		t.Fatalf("a release waits for its image: %s", w)
	}
	if w := s.wait(waiting, due, 15, now); w != 6*time.Hour {
		t.Fatalf("after PendingChecks rechecks, back to the interval: %s", w)
	}
	if w := s.wait(waiting, now.Add(30*time.Second), 0, now); w != 30*time.Second {
		t.Fatalf("the full check comes first: %s", w)
	}
	if w := s.wait(nil, now.Add(-time.Minute), 0, now); w != time.Second {
		t.Fatalf("overdue: %s", w)
	}
}
