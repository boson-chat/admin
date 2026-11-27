package updates

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// Stack releases (boson-chat/stack, docs/updates.md "Stack releases"): every
// merge to the stack's main is a version whose GitHub release carries a
// bundle (boson-stack-X.Y.Z.tar.gz, everything an install needs from the
// repository: compose files, migrations, mounted files, VERSION), its
// .sha256 and manifest.json. An update to it downloads and checks the
// bundle on the host, runs the bundle's own scripts/stack-apply.sh (files,
// then migrations) and recreates only the services whose compose
// configuration changed. Ergo, RoleServ, the web IRC gateway and certsync
// are built by the stack and versioned with it: they are part of this
// update, not components of their own.

// Manifest is a stack release's manifest.json.
type Manifest struct {
	Schema   int                        `json:"schema"`
	Version  string                     `json:"version"`
	Commit   string                     `json:"commit"`
	Release  bool                       `json:"release"`
	MinAdmin string                     `json:"min_admin"`
	Services map[string]ManifestService `json:"services"`
	// VideoNode: compose.video-node.yaml's services (overrides).
	VideoNode  map[string]ManifestService `json:"video_node_services"`
	Migrations []string                   `json:"migrations"`
	Files      map[string]string          `json:"files"`
}

// ManifestService is one compose service of a release.
type ManifestService struct {
	Compose string `json:"compose"`
	// Image is what compose runs when Env isn't set in .env.
	Image string `json:"image"`
	Env   string `json:"env,omitempty"`
	// Built: the stack builds it (versioned with the stack).
	Built    bool     `json:"built,omitempty"`
	Oneshot  bool     `json:"oneshot,omitempty"`
	Profiles []string `json:"profiles"`
	// Mounts are its ./… bind-mount sources (files and directories).
	Mounts []string `json:"mounts"`
}

// ParseManifest reads and checks a manifest.json.
func ParseManifest(b []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("the release's manifest.json: %w", err)
	}
	if m.Schema != 1 {
		return nil, fmt.Errorf("the release's manifest.json has schema %d; this panel knows 1 (update the panel first)", m.Schema)
	}
	if _, ok := ParseVersion(m.Version); !ok {
		return nil, fmt.Errorf("the release's manifest.json: version %q", m.Version)
	}
	for svc, ms := range m.Services {
		if !reService.MatchString(svc) || (ms.Image != "" && !reValue.MatchString(ms.Image)) || (ms.Env != "" && !reEnvKey.MatchString(ms.Env)) {
			return nil, fmt.Errorf("the release's manifest.json: service %q", svc)
		}
	}
	return &m, nil
}

// StackStatus is the stack row's detail: what the newest release changes
// here, worked out on the host before anything is applied.
type StackStatus struct {
	// Version is the release this describes.
	Version string `json:"version"`
	// PreRelease: the install has no VERSION (files from before stack
	// releases); it can still update.
	PreRelease bool `json:"pre_release,omitempty"`
	// Baseline: the install records its bundle files, so local edits are
	// told apart from older versions.
	Baseline    bool   `json:"baseline"`
	MinAdmin    string `json:"min_admin,omitempty"`
	AdminTooOld bool   `json:"admin_too_old,omitempty"`
	// Files that differ from the installed ones (same left out).
	Files      []StackFile      `json:"files"`
	Unchanged  int              `json:"unchanged"`
	Migrations []StackMigration `json:"migrations"`
	// Images: services whose image changes.
	Images []ImageChange `json:"images"`
	// Pins: what happens to .env's *_IMAGE pins (precedence).
	Pins []PinChange `json:"pins"`
	// Included: the images the stack builds, with their change (if any).
	Included []ImageChange `json:"included"`
	// Restarts are recreated; Oneshots run once; Kept change but are never
	// restarted (evrd-links); Removed are no longer in the compose files.
	Restarts []string `json:"restarts"`
	Oneshots []string `json:"oneshots,omitempty"`
	Kept     []string `json:"kept,omitempty"`
	Removed  []string `json:"removed,omitempty"`
	// Self: the panel itself is recreated at the end (its image or its
	// compose configuration changes).
	Self    bool         `json:"self,omitempty"`
	Impacts []ItemImpact `json:"impacts"`
	Impact  Impact       `json:"impact"`
	// Error: the analysis on the host failed (the update can't be applied).
	Error      string    `json:"error,omitempty"`
	AnalyzedAt time.Time `json:"analyzed_at"`

	bundleURL, bundleSHA string
	sets                 []string
	images               map[string]string // service → image it runs after the update
	mounts               map[string][]string
	oneshot              map[string]bool
	selfImage            string
}

// StackFile is a bundle file the update writes or removes.
type StackFile struct {
	Path string `json:"path"`
	// Action: add, update, local (edited here: replaced, kept as
	// <file>.local), unknown (differs, no record: replaced), remove, keep
	// (dropped by the release but edited here: kept).
	Action string `json:"action"`
	// Diff is the unified diff (installed → release), line by line.
	Diff      []string `json:"diff,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
	// Services restarted for it (they bind-mount the file).
	Services []string `json:"services,omitempty"`
}

// StackMigration is a migration of the release.
type StackMigration struct {
	Name  string `json:"name"`
	About string `json:"about,omitempty"`
	// State: pending (runs with this update) or done.
	State string `json:"state"`
}

// ImageChange is one service's image before and after.
type ImageChange struct {
	Service string `json:"service"`
	Name    string `json:"name"`
	From    string `json:"from,omitempty"`
	To      string `json:"to"`
	Changed bool   `json:"changed"`
}

// PinChange is what a stack update does with one *_IMAGE pin in .env.
type PinChange struct {
	EnvVar string `json:"env_var"`
	// Pin is .env's value ("" = none); Default the release's.
	Pin     string `json:"pin,omitempty"`
	Default string `json:"default"`
	// Action: raise (the pin is older: set to the default), keep (newer,
	// or another image), pin (evrd-links: pinned to what runs), follow (no
	// pin: compose's default, the release's).
	Action string `json:"action"`
	To     string `json:"to"`
	Note   string `json:"note"`
}

// stackState is what the host says about the installed stack.
type stackState struct {
	Version  string
	Baseline bool
	Applied  map[string]bool
	// EnvImages: .env's *_IMAGE lines.
	EnvImages map[string]string
	Profiles  string
}

func parseStackState(out *JobOutput) stackState {
	st := stackState{Applied: map[string]bool{}, EnvImages: map[string]string{}}
	for _, l := range out.Lines {
		switch {
		case strings.HasPrefix(l, "@@STACKVERSION "):
			v := strings.TrimSpace(strings.TrimPrefix(l, "@@STACKVERSION "))
			if v != "none" {
				st.Version = v
			}
		case l == "@@BASELINE yes":
			st.Baseline = true
		case strings.HasPrefix(l, "@@APPLIED "):
			st.Applied[strings.TrimSpace(strings.TrimPrefix(l, "@@APPLIED "))] = true
		case strings.HasPrefix(l, "@@ENVIMG "):
			k, v, ok := strings.Cut(strings.TrimPrefix(l, "@@ENVIMG "), "=")
			v = strings.Trim(strings.TrimSpace(v), `"'`)
			if ok && reEnvKey.MatchString(k) && (v == "" || reValue.MatchString(v)) {
				st.EnvImages[k] = v // the last line wins, as for compose
			}
		case strings.HasPrefix(l, "@@PROFILES "):
			st.Profiles = strings.TrimSpace(strings.TrimPrefix(l, "@@PROFILES "))
		}
	}
	return st
}

// stackPlanOutput is the host's dry run of a release.
type stackPlanOutput struct {
	Files      []StackFile
	Unchanged  int
	Migrations []StackMigration
	Old, New   map[string]string // service → compose config hash
	Restart    []string          // asked for by migrations (apply only)
}

func parseStackPlan(out *JobOutput) stackPlanOutput {
	p := stackPlanOutput{Old: map[string]string{}, New: map[string]string{}}
	var cur *StackFile
	for _, l := range out.Lines {
		switch {
		case strings.HasPrefix(l, "@@STACKFILE "):
			f := strings.Fields(l)
			if len(f) != 3 {
				continue
			}
			if f[1] == "same" {
				p.Unchanged++
				cur = nil
				continue
			}
			p.Files = append(p.Files, StackFile{Action: f[1], Path: f[2]})
			cur = nil
		case strings.HasPrefix(l, "@@DIFF "):
			path := strings.TrimPrefix(l, "@@DIFF ")
			cur = nil
			for i := range p.Files {
				if p.Files[i].Path == path {
					cur = &p.Files[i]
				}
			}
		case strings.HasPrefix(l, "@@D "), l == "@@D":
			if cur != nil {
				line := strings.TrimPrefix(strings.TrimPrefix(l, "@@D"), " ")
				if strings.HasPrefix(line, "... (diff cut") {
					cur.Truncated = true
					continue
				}
				cur.Diff = append(cur.Diff, line)
			}
		case strings.HasPrefix(l, "@@MIGRATION "):
			f := strings.SplitN(strings.TrimPrefix(l, "@@MIGRATION "), " ", 3)
			if len(f) < 2 {
				continue
			}
			m := StackMigration{Name: f[0], State: f[1]}
			if len(f) == 3 {
				m.About = f[2]
			}
			p.Migrations = append(p.Migrations, m)
		case strings.HasPrefix(l, "@@HASH "):
			f := strings.Fields(l)
			if len(f) == 4 && reService.MatchString(f[2]) {
				if f[1] == "old" {
					p.Old[f[2]] = f[3]
				} else if f[1] == "new" {
					p.New[f[2]] = f[3]
				}
			}
		case strings.HasPrefix(l, "@@RESTART "):
			if s := strings.TrimSpace(strings.TrimPrefix(l, "@@RESTART ")); reService.MatchString(s) {
				p.Restart = append(p.Restart, s)
			}
		}
	}
	return p
}

// neverRaise: pins a stack update never moves (a new evrd-links drops every
// Boson user off IRC).
var neverRaise = map[string]bool{"EVRD_LINKS_IMAGE": true}

// stackPins applies the precedence rule to .env's pins: a pin older than
// the release’s default (same image) is raised to it, a newer one or
// another image is kept, no pin follows the default. EVRD_LINKS_IMAGE is
// never raised; unset, it is pinned to what runs. sets are the .env lines
// to write; images is each service's image after the update.
func stackPins(m *Manifest, env map[string]string, running map[string]string) (sets []string, pins []PinChange, images map[string]string) {
	images = map[string]string{}
	byVar := map[string]string{} // var → default
	var vars []string
	svcs := sortedServices(m.Services)
	for _, svc := range svcs {
		ms := m.Services[svc]
		if ms.Env != "" {
			if _, ok := byVar[ms.Env]; !ok {
				vars = append(vars, ms.Env)
				byVar[ms.Env] = ms.Image
			}
		}
	}
	effective := map[string]string{}
	for _, v := range vars {
		def, pin := byVar[v], env[v]
		pc := PinChange{EnvVar: v, Pin: pin, Default: def}
		switch {
		case neverRaise[v] && pin == "":
			// What runs stays what runs: pin it so no compose default moves it.
			cur := ""
			for _, svc := range svcs {
				if m.Services[svc].Env == v && running[svc] != "" {
					cur = running[svc]
				}
			}
			if cur == "" || cur == def {
				pc.Action, pc.To, pc.Note = "follow", def, "never recreated by an update"
			} else {
				pc.Action, pc.To, pc.Note = "pin", cur, "pinned to what runs: it is never recreated by an update"
				sets = append(sets, v+"="+cur)
			}
		case neverRaise[v]:
			pc.Action, pc.To, pc.Note = "keep", pin, "never moved by an update"
		case pin == "":
			pc.Action, pc.To, pc.Note = "follow", def, "no pin in .env: the release’s default"
		case ImageRepo(pin) != ImageRepo(def):
			pc.Action, pc.To, pc.Note = "keep", pin, "another image than the release’s: kept"
		default:
			pv, okp := ParseVersion(ImageTag(pin))
			dv, okd := ParseVersion(ImageTag(def))
			switch {
			case okp && okd && Less(pv, dv):
				pc.Action, pc.To, pc.Note = "raise", def, "older than the release’s default: raised to it"
				sets = append(sets, v+"="+def)
			case pin == def:
				pc.Action, pc.To, pc.Note = "keep", pin, "the release’s default"
			case okp && okd:
				pc.Action, pc.To, pc.Note = "keep", pin, "newer than the release’s default: kept"
			default:
				pc.Action, pc.To, pc.Note = "keep", pin, "not a version: kept"
			}
		}
		effective[v] = pc.To
		pins = append(pins, pc)
	}
	for _, svc := range svcs {
		ms := m.Services[svc]
		img := ms.Image
		if ms.Env != "" {
			img = effective[ms.Env]
		}
		if img != "" {
			images[svc] = img
		}
	}
	return sets, pins, images
}

func sortedServices(m map[string]ManifestService) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// builtNames: the images the stack builds, as the page names them.
var builtNames = map[string]string{
	"ergo": "Ergo", "roleserv": "RoleServ", "webircgateway": "Web IRC gateway", "certsync": "certsync",
}

// infraImpacts: what restarting a service outside the catalog does.
var infraImpacts = map[string]Impact{
	"traefik": {Restart: true, Disruptive: true, Downtime: 5 * time.Second,
		Who: "Traefik restarts: web connections (the web client, sign-in, the API, voice signalling) drop for a few seconds and reconnect."},
	"postgres": {Restart: true, Disruptive: true, Downtime: 10 * time.Second,
		Who: "Postgres restarts: sign-in, the api, uploads and the directory pause for a few seconds."},
	"certsync": {Restart: true, Downtime: 2 * time.Second, Who: "certsync (certificate copies) restarts; nobody notices."},
	"roleserv": {Restart: true, Downtime: 5 * time.Second, Who: "RoleServ restarts; role channel changes wait a few seconds."},
}

// stackServiceImpact is the impact of recreating one service.
func (s *Service) stackServiceImpact(svc string) (string, Impact) {
	if im, ok := infraImpacts[svc]; ok {
		if n := builtNames[svc]; n != "" {
			return n, im
		}
		return svc, im
	}
	for _, c := range s.o.Catalog {
		if c.ID != "stack" && len(c.Services) > 0 && c.Services[0] == svc {
			return c.Name, c.Impact
		}
	}
	return svc, Impact{Restart: true, Downtime: 3 * time.Second, Who: svc + " restarts; it pauses for a few seconds."}
}

// stackRestarts works out what an update recreates, from the compose
// config hashes before and after, the images after, the bundle files that
// change (services that bind-mount them) and what migrations ask for.
func stackRestarts(m *Manifest, p stackPlanOutput, images, running map[string]string, never []string) (restarts, oneshots, kept, removed []string, self bool) {
	set := map[string]bool{}
	for svc, h := range p.New {
		if p.Old[svc] != h {
			set[svc] = true
		}
	}
	for svc := range p.Old {
		if _, ok := p.New[svc]; !ok {
			removed = append(removed, svc)
		}
	}
	active := func(svc string) bool { _, ok := p.New[svc]; return ok }
	for svc, img := range images {
		if active(svc) && running[svc] != "" && running[svc] != img && !strings.HasPrefix(running[svc], img+"@") {
			set[svc] = true
		}
	}
	for _, f := range p.Files {
		for svc, ms := range m.Services {
			if active(svc) && slices.Contains(ms.Mounts, f.Path) {
				set[svc] = true
			}
		}
	}
	for _, svc := range p.Restart {
		if active(svc) {
			set[svc] = true
		}
	}
	for svc := range set {
		switch {
		case slices.Contains(never, svc):
			kept = append(kept, svc)
		case svc == "admin":
			self = true
		case m.Services[svc].Oneshot:
			oneshots = append(oneshots, svc)
		default:
			restarts = append(restarts, svc)
		}
	}
	sort.Strings(restarts)
	sort.Strings(oneshots)
	sort.Strings(kept)
	sort.Strings(removed)
	return
}

// fileServices: the services that bind-mount a bundle file.
func fileServices(m *Manifest, path string, active map[string]string) []string {
	var out []string
	for svc, ms := range m.Services {
		if _, ok := active[svc]; ok && slices.Contains(ms.Mounts, path) {
			out = append(out, svc)
		}
	}
	sort.Strings(out)
	return out
}

var (
	reSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)
	reURL    = regexp.MustCompile(`^https?://[A-Za-z0-9.-]+(:[0-9]+)?/[A-Za-z0-9./_%~+@-]*$`)
)

// parseSHA256File reads a "<hex>  <name>" line.
func parseSHA256File(b []byte) (string, error) {
	f := strings.Fields(string(b))
	if len(f) == 0 || !reSHA256.MatchString(strings.ToLower(f[0])) {
		return "", fmt.Errorf("the release's .sha256 asset isn't a SHA-256")
	}
	return strings.ToLower(f[0]), nil
}
