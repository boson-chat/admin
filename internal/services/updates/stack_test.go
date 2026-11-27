package updates

import (
	"slices"
	"strings"
	"testing"
)

const hb = "harbor.floret.dev/boson/"

func testManifest() *Manifest {
	return &Manifest{Schema: 1, Version: "1.4.0", Services: map[string]ManifestService{
		"auth":        {Image: hb + "auth:1.27.0", Env: "AUTH_IMAGE"},
		"api":         {Image: hb + "backend:0.57.3", Env: "API_IMAGE"},
		"api-migrate": {Image: hb + "backend:0.57.3", Env: "API_IMAGE", Oneshot: true},
		"web":         {Image: hb + "web:0.102.0", Env: "WEB_IMAGE"},
		"files":       {Image: hb + "files:1.0.3", Env: "FILES_IMAGE"},
		"evrd-links":  {Image: "ghcr.io/4evrd/evrd:0.62.0", Env: "EVRD_LINKS_IMAGE"},
		"evrd":        {Image: "ghcr.io/4evrd/evrd:0.61.0", Env: "EVRD_IMAGE", Mounts: []string{"bridge/evrd.conf"}},
		"ergo":        {Image: hb + "stack-ergo:1.4.0", Built: true, Mounts: []string{"config"}},
		"wellknown":   {Image: "busybox:1.37.0", Mounts: []string{"images/wellknown/serve.sh"}},
		"config-init": {Image: "busybox:1.37.0", Oneshot: true, Mounts: []string{"config"}},
		"admin":       {Image: hb + "admin:1.30.0", Env: "ADMIN_IMAGE"},
	}}
}

// The precedence of .env pins against a release's defaults.
func TestStackPins(t *testing.T) {
	env := map[string]string{
		"AUTH_IMAGE":  hb + "auth:1.20.0",            // older: raised
		"API_IMAGE":   hb + "backend:0.58.0",         // newer: kept
		"WEB_IMAGE":   "registry.example/web:0.90.0", // another image: kept
		"FILES_IMAGE": hb + "files:1.0.3",            // the same
		"EVRD_IMAGE":  "ghcr.io/4evrd/evrd:latest",   // not a version: kept
		"ADMIN_IMAGE": hb + "admin:1.29.0",
	}
	running := map[string]string{"evrd-links": "ghcr.io/4evrd/evrd:0.60.0", "ergo": hb + "stack-ergo:2.19.1"}
	sets, pins, images := stackPins(testManifest(), env, running)
	want := []string{"ADMIN_IMAGE=" + hb + "admin:1.30.0", "AUTH_IMAGE=" + hb + "auth:1.27.0", "EVRD_LINKS_IMAGE=ghcr.io/4evrd/evrd:0.60.0"}
	slices.Sort(sets)
	if !slices.Equal(sets, want) {
		t.Fatalf("sets %v, want %v", sets, want)
	}
	acts := map[string]string{}
	for _, p := range pins {
		acts[p.EnvVar] = p.Action
	}
	for k, a := range map[string]string{"AUTH_IMAGE": "raise", "API_IMAGE": "keep", "WEB_IMAGE": "keep", "FILES_IMAGE": "keep",
		"EVRD_IMAGE": "keep", "EVRD_LINKS_IMAGE": "pin", "ADMIN_IMAGE": "raise"} {
		if acts[k] != a {
			t.Errorf("%s: %s, want %s", k, acts[k], a)
		}
	}
	for svc, img := range map[string]string{"auth": hb + "auth:1.27.0", "api": hb + "backend:0.58.0", "api-migrate": hb + "backend:0.58.0",
		"web": "registry.example/web:0.90.0", "evrd-links": "ghcr.io/4evrd/evrd:0.60.0", "ergo": hb + "stack-ergo:1.4.0"} {
		if images[svc] != img {
			t.Errorf("%s runs %s after, want %s", svc, images[svc], img)
		}
	}
	// No pin: compose's default (the release's) and nothing to write;
	// evrd-links already on the default: nothing either.
	sets, pins, _ = stackPins(testManifest(), map[string]string{}, map[string]string{"evrd-links": "ghcr.io/4evrd/evrd:0.62.0"})
	if len(sets) != 0 {
		t.Fatalf("no pins: %v", sets)
	}
	for _, p := range pins {
		if p.Action != "follow" {
			t.Errorf("%s: %s", p.EnvVar, p.Action)
		}
	}
	// A set evrd-links pin is never moved, even when older.
	sets, _, _ = stackPins(testManifest(), map[string]string{"EVRD_LINKS_IMAGE": "ghcr.io/4evrd/evrd:0.59.0"}, nil)
	if len(sets) != 0 {
		t.Fatalf("evrd-links moved: %v", sets)
	}
}

func TestStackRestarts(t *testing.T) {
	m := testManifest()
	p := stackPlanOutput{
		Old:     map[string]string{"auth": "a", "ergo": "e", "evrd-links": "l", "evrd": "v", "wellknown": "w", "config-init": "c", "admin": "x", "gone": "g"},
		New:     map[string]string{"auth": "a", "ergo": "e2", "evrd-links": "l2", "evrd": "v", "wellknown": "w", "config-init": "c2", "admin": "x2", "new": "n"},
		Files:   []StackFile{{Path: "images/wellknown/serve.sh", Action: "update"}, {Path: "docs/x.md", Action: "update"}},
		Restart: []string{"evrd", "notrunning"},
	}
	images := map[string]string{"auth": hb + "auth:1.27.0"}
	running := map[string]string{"auth": hb + "auth:1.20.0"}
	restarts, oneshots, kept, removed, self := stackRestarts(m, p, images, running, []string{"evrd-links"})
	if !slices.Equal(restarts, []string{"auth", "ergo", "evrd", "new", "wellknown"}) {
		t.Fatalf("restarts %v", restarts)
	}
	if !slices.Equal(oneshots, []string{"config-init"}) || !slices.Equal(kept, []string{"evrd-links"}) || !slices.Equal(removed, []string{"gone"}) || !self {
		t.Fatalf("oneshots %v kept %v removed %v self %v", oneshots, kept, removed, self)
	}
	// The same rules after the apply.
	it := &StackItem{Mounts: map[string][]string{"wellknown": {"images/wellknown/serve.sh"}}, Oneshot: []string{"config-init"}, Never: []string{"evrd-links"}}
	p.Migrations = []StackMigration{{Name: "0001-x.sh", State: "applied"}, {Name: "0000-y.sh", State: "done"}}
	r := stackApplied(it, p)
	if !slices.Equal(r.recreate, []string{"ergo", "evrd", "new", "wellknown"}) || !r.self || !slices.Equal(r.kept, []string{"evrd-links"}) ||
		!slices.Equal(r.migrations, []string{"0001-x.sh"}) || !slices.Equal(r.oneshots, []string{"config-init"}) {
		t.Fatalf("applied: %+v", r)
	}
}

func TestParseStackPlanAndState(t *testing.T) {
	out := &JobOutput{Lines: []string{
		"@@STACKFILE same LICENSE",
		"@@STACKFILE update compose.yaml",
		"@@DIFF compose.yaml",
		"@@D --- a", "@@D +++ b", "@@D @@ -1 +1 @@", "@@D -x", "@@D +y", "@@D  ", "@@D", "@@D ... (diff cut at 600 lines)",
		"@@STACKFILE local traefik/dynamic/stack.yml",
		"@@MIGRATION 0001-a.sh pending Does a thing (stack #55).",
		"@@MIGRATION 0000-b.sh done",
		"@@HASH old auth 111", "@@HASH new auth 222", "@@HASH new BAD!NAME 1",
		"@@RESTART evrd",
		"plain line",
	}}
	p := parseStackPlan(out)
	if p.Unchanged != 1 || len(p.Files) != 2 || p.Files[0].Path != "compose.yaml" || len(p.Files[0].Diff) != 7 || !p.Files[0].Truncated {
		t.Fatalf("files: %+v", p.Files)
	}
	if p.Files[0].Diff[5] != " " || p.Files[0].Diff[6] != "" {
		t.Fatalf("context lines keep their space: %q", p.Files[0].Diff)
	}
	if len(p.Migrations) != 2 || p.Migrations[0].About != "Does a thing (stack #55)." || p.Migrations[1].State != "done" {
		t.Fatalf("migrations: %+v", p.Migrations)
	}
	if p.Old["auth"] != "111" || p.New["auth"] != "222" || len(p.New) != 1 || !slices.Equal(p.Restart, []string{"evrd"}) {
		t.Fatalf("hashes %v %v restart %v", p.Old, p.New, p.Restart)
	}
	st := parseStackState(&JobOutput{Lines: []string{"@@STACKVERSION 1.2.0", "@@BASELINE yes", "@@APPLIED 0001-a.sh",
		"@@ENVIMG AUTH_IMAGE=" + hb + "auth:1.20.0", "@@ENVIMG AUTH_IMAGE=" + hb + "auth:1.21.0", "@@ENVIMG WEB_IMAGE=\"x y\"", "@@PROFILES irc,auth"}})
	if st.Version != "1.2.0" || !st.Baseline || !st.Applied["0001-a.sh"] || st.EnvImages["AUTH_IMAGE"] != hb+"auth:1.21.0" || st.Profiles != "irc,auth" {
		t.Fatalf("state %+v", st)
	}
	if _, ok := st.EnvImages["WEB_IMAGE"]; ok {
		t.Fatal("a value that isn't a plain image reference is dropped")
	}
	if st := parseStackState(&JobOutput{Lines: []string{"@@STACKVERSION none", "@@BASELINE no"}}); st.Version != "" || st.Baseline {
		t.Fatalf("pre-release: %+v", st)
	}
}

func TestParseManifest(t *testing.T) {
	if _, err := ParseManifest([]byte(`{"schema":2,"version":"1.0.0"}`)); err == nil || !strings.Contains(err.Error(), "update the panel") {
		t.Fatalf("schema 2: %v", err)
	}
	if _, err := ParseManifest([]byte(`{"schema":1,"version":"main"}`)); err == nil {
		t.Fatal("a version is needed")
	}
	if _, err := ParseManifest([]byte(`{"schema":1,"version":"1.0.0","services":{"a":{"image":"x;rm -rf /"}}}`)); err == nil {
		t.Fatal("an image reference with shell characters is refused")
	}
	m, err := ParseManifest([]byte(`{"schema":1,"version":"1.0.0","min_admin":"1.30.0","services":{"ergo":{"image":"h/stack-ergo:1.0.0","built":true}}}`))
	if err != nil || !m.Services["ergo"].Built || m.MinAdmin != "1.30.0" {
		t.Fatalf("%+v %v", m, err)
	}
}

func TestMergeSets(t *testing.T) {
	got := mergeSets([]string{"AUTH_IMAGE=" + hb + "auth:1.27.0", "EVRD_LINKS_IMAGE=x:1"},
		[]string{"AUTH_IMAGE=" + hb + "auth:1.28.0", "WEB_IMAGE=" + hb + "web:1.0.0"},
		[]string{"AUTH_IMAGE=" + hb + "auth:1.21.0"})
	want := []string{"AUTH_IMAGE=" + hb + "auth:1.28.0", "EVRD_LINKS_IMAGE=x:1", "WEB_IMAGE=" + hb + "web:1.0.0"}
	if !slices.Equal(got, want) {
		t.Fatalf("%v", got)
	}
}

func TestStackHelperEnv(t *testing.T) {
	base := map[string]string{"STACK_DIR": "/opt/boson-stack", "ENV_FILE": "/opt/boson-stack/.env"}
	ok := with(base, "STAGE_DIR", "/opt/boson-stack/.boson/staging/1.2.0", "STACK_VERSION", "1.2.0",
		"BUNDLE_URL", "https://github.com/boson-chat/stack/releases/download/v1.2.0/boson-stack-1.2.0.tar.gz",
		"BUNDLE_SHA256", strings.Repeat("ab", 32), "STACK_FILES", "1", "STACK_RESTORE", "")
	if err := validEnv(ok); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"BUNDLE_URL":    "https://example.com/x.tgz; rm -rf /",
		"BUNDLE_SHA256": "nothex",
		"STACK_VERSION": "1.2.0$(id)",
		"STAGE_DIR":     "/opt/../etc",
		"STACK_FILES":   "yes",
	} {
		if err := validEnv(with(ok, k, v)); err == nil {
			t.Errorf("%s=%q accepted", k, v)
		}
	}
	h := &Helper{Image: "docker:cli"}
	for name, want := range map[string]string{jobStage: "bridge", "stack-apply": "none", "backup": "none"} {
		spec, err := h.spec("r1", name, "true", base)
		if err != nil || spec.NetworkMode != want {
			t.Errorf("%s: network %q (%v), want %s", name, spec.NetworkMode, err, want)
		}
	}
}

func TestCombinedImpact(t *testing.T) {
	ergo, _ := ByID("ergo")
	auth, _ := ByID("auth")
	im := combinedImpact([]Component{auth, ergo})
	if !im.ChatDisconnect || !im.Restart || im.Downtime != ergo.Impact.Downtime || !strings.Contains(im.Who, "Auth, Ergo") {
		t.Fatalf("%+v", im)
	}
	if im := combinedImpact(nil); im.Restart || !strings.Contains(im.Who, "Nothing restarts") {
		t.Fatalf("%+v", im)
	}
}
