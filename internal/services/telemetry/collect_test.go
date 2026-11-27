package telemetry

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/clients/bosonapi"
	"github.com/boson-chat/admin/internal/clients/dockerapi"
	"github.com/boson-chat/admin/internal/clients/ircprobe"
	"github.com/boson-chat/admin/internal/clients/promql"
	"github.com/boson-chat/admin/internal/services/updates"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed testdata/report-1.schema.json
var schemaJSON []byte

// ---- fakes ----

type fakeDocker struct {
	cs      []dockerapi.Container
	inspect map[string]*dockerapi.Inspect
	err     error
}

func (f *fakeDocker) List(context.Context, string) ([]dockerapi.Container, error) { return f.cs, f.err }
func (f *fakeDocker) Inspect(_ context.Context, id string) (*dockerapi.Inspect, error) {
	if in, ok := f.inspect[id]; ok {
		return in, nil
	}
	return nil, errors.New("no such container")
}

type fakeProm map[string]float64 // query substring → value

func (f fakeProm) Query(_ context.Context, q string) ([]promql.Sample, error) {
	for k, v := range f {
		if strings.Contains(q, k) {
			return []promql.Sample{{Labels: map[string]string{"instance": "host", "nodename": "secret-node.example.org"}, Value: v}}, nil
		}
	}
	return nil, nil
}

type fakeFiles struct {
	st    *bosonapi.FilesStatus
	pages []bosonapi.FilePage
}

func (f *fakeFiles) Status(context.Context) (*bosonapi.FilesStatus, error) { return f.st, nil }
func (f *fakeFiles) List(_ context.Context, q bosonapi.FilesQuery) (*bosonapi.FilePage, error) {
	i := 0
	if q.Cursor != "" {
		i = int(q.Cursor[0] - '0')
	}
	return &f.pages[i], nil
}

func container(svc, image, state string) dockerapi.Container {
	return dockerapi.Container{ID: "id-" + svc, Names: []string{"/boson-" + svc + "-1"}, Image: image, State: state,
		Labels: map[string]string{dockerapi.LabelService: svc, dockerapi.LabelProject: "boson"}}
}

func inspect(restarts int, status string, health string) *dockerapi.Inspect {
	in := &dockerapi.Inspect{RestartCount: restarts}
	in.State.Status = status
	in.State.Running = status == "running"
	in.State.Restarting = status == "restarting"
	if health != "" {
		in.State.Health = &struct {
			Status string `json:"Status"`
		}{Status: health}
	}
	return in
}

var now = time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

// fullSources is a busy install: every source answers.
func fullSources() Sources {
	d := &fakeDocker{
		cs: []dockerapi.Container{
			container("traefik", "traefik:v3.7.19", "running"),
			container("ergo", "harbor.floret.dev/boson/ergo:1.48.0", "running"),
			container("auth", "harbor.floret.dev/boson/auth:1.9.2@sha256:abc", "running"),
			container("postgres", "postgres:17.6-alpine", "running"),
			container("quicvoiced", "harbor.floret.dev/boson/quicvoice:latest", "running"),
			container("victoriametrics", "victoriametrics/victoria-metrics:v1.131.0", "running"),
			container("admin", "harbor.floret.dev/boson/admin:edge", "running"),
			container("files", "harbor.floret.dev/boson/files:2.1", "restarting"),
			container("config-init", "busybox:1.37.0", "exited"),
			container("api-migrate", "harbor.floret.dev/boson/api:1.0.0", "exited"),
			container("my-own-bot", "example/bot:1.0.0", "running"),
		},
		inspect: map[string]*dockerapi.Inspect{
			"id-traefik": inspect(0, "running", ""), "id-ergo": inspect(7, "running", "unhealthy"),
			"id-auth": inspect(0, "running", "healthy"), "id-postgres": inspect(2000, "running", ""),
			"id-quicvoiced": inspect(1, "running", ""), "id-victoriametrics": inspect(0, "running", ""),
			"id-admin": inspect(0, "running", ""), "id-files": inspect(3, "restarting", ""),
		},
	}
	d.cs[3].Labels[dockerapi.LabelVersion] = "17.6"
	d.cs[4].Labels[dockerapi.LabelVersion] = "v1.8.0"
	return Sources{
		Docker: d, Project: "boson", SelfVersion: "1.32.0", GOARCH: "amd64",
		StackVersion: func(context.Context) string { return "v1.48.0" },
		Prom: fakeProm{
			`up{job="node"}`: 1, "quicvoice_participants": 77.4, "quicvideo_sessions": 0, "pushrelay": 12,
			`quantile_over_time(0.95, (100 * (1 - avg(rate(node_cpu`: 34.54, `max_over_time((100 * (1 - avg(rate`: 98.24,
			"MemAvailable": 71, "node_filesystem_avail_bytes": 62.33, "node_load1": 140, "node_boot_time_seconds": 41.5 * 86400,
		},
		IRC: func(context.Context) *ircprobe.Result {
			return &ircprobe.Result{OK: true, Version: "ergo-2.19.1", Users: ptr(9), Channels: ptr(4)}
		},
		PublicConfig: func(context.Context) (*authapi.PublicConfig, error) {
			return &authapi.PublicConfig{Registration: "invite", Email: &authapi.PublicEmail{Configured: true},
				SSOProviders: []authapi.SSOProvider{{Slug: "corp", Kind: "oidc", DisplayName: "Corp"}, {Slug: "b", Kind: "oidc"}, {Slug: "s", Kind: "saml"}}}, nil
		},
		Config: func(key string) (any, bool) {
			switch key {
			case "auth.guest_access":
				return false, true
			case "auth.federation.allow":
				return []any{"a.example", "b.example", "c.example"}, true
			}
			return nil, false
		},
		Runs: func(context.Context) []updates.RunSummary {
			return []updates.RunSummary{
				{Kind: "rollback", State: updates.StateRolledBack, StartedAt: now.Add(-time.Hour)},
				{Kind: "update", State: updates.StateRolledBack, StartedAt: now.Add(-2 * time.Hour), Error: "x", FailedStep: "verify"},
				{Kind: "update", State: updates.StateSucceeded, StartedAt: now.Add(-3 * time.Hour)},
				{Kind: "update", State: updates.StateFailed, StartedAt: now.Add(-30 * time.Hour), FailedStep: "pull"},
			}
		},
		Files: &fakeFiles{
			st: &bosonapi.FilesStatus{Config: &bosonapi.FilesConfigEcho{WindowSeconds: 72 * 3600}, Totals: bosonapi.FilesTotals{WindowUploads: 999}},
			pages: []bosonapi.FilePage{
				{Files: []bosonapi.FileRecord{{Name: "a.png", CreatedAt: now.Add(-time.Minute)}, {Name: "b.png", CreatedAt: now.Add(-time.Hour)}}, NextCursor: "1"},
				{Files: []bosonapi.FileRecord{{Name: "c.png", CreatedAt: now.Add(-23 * time.Hour)}, {Name: "old.png", CreatedAt: now.Add(-25 * time.Hour)}}},
			},
		},
		DirectoryListed: func(context.Context) bool { return true },
	}
}

func buildJSON(t *testing.T, c *Collector, in Input) ([]byte, *Report) {
	t.Helper()
	r, _ := c.Build(context.Background(), in)
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return b, r
}

func compileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("report-1.json", doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile("report-1.json")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func validate(t *testing.T, s *jsonschema.Schema, b []byte) error {
	t.Helper()
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return s.Validate(v)
}

const testID = "3f2a9c0e5b7d41e8a6c2f90b1d4e7a53"

func TestFullReport(t *testing.T) {
	c := &Collector{S: fullSources()}
	known := &Known{At: now.Add(-time.Hour), Accounts: ptr[int64](42), Active7d: ptr[int64](7), Active30d: ptr[int64](30)}
	_, r := buildJSON(t, c, Input{InstallID: testID, Reason: ReasonDaily, Now: now, Known: known, IRCPeak: ptr(12)})

	wantSvcs := map[string]string{"traefik": "3.7.19", "ergo": "1.48.0", "auth": "1.9.2", "postgres": "17.6.0",
		"quicvoiced": "1.8.0", "victoriametrics": "1.131.0", "admin": "1.32.0", "files": "2.1.0"}
	if len(r.Services) != len(wantSvcs) {
		t.Fatalf("services %v", r.Services)
	}
	for k, v := range wantSvcs {
		if r.Services[k] != v {
			t.Errorf("services[%s] = %q, want %q", k, r.Services[k], v)
		}
	}
	if got := strings.Join(r.Profiles, ","); got != "admin,auth,files,irc,metrics,voice" {
		t.Errorf("profiles %s", got)
	}
	if r.Stack.Version != "1.48.0" || r.Install != (Install{Mode: "fresh", IRCd: "ergo", Arch: "amd64"}) {
		t.Errorf("stack %+v install %+v", r.Stack, r.Install)
	}
	if *r.Counts.Accounts != "11-50" || *r.Counts.Active7d != "2-10" || *r.Counts.Active30d != "11-50" ||
		*r.Counts.IRCPeak24h != "11-50" || r.Counts.Channels != nil {
		t.Errorf("counts %+v", r.Counts)
	}
	f := r.Features
	if *f.VoiceMinutes24h != "51-100" || f.VideoMinutes24h != nil || *f.FilesUploaded != "2-10" || f.PushSent24h != nil {
		t.Errorf("features voice=%v video=%v files=%v push=%v (no quicvideod, no pushrelay: null)", f.VoiceMinutes24h, f.VideoMinutes24h, f.FilesUploaded, f.PushSent24h)
	}
	st := r.Settings
	if st.Registration != "invite" || st.GuestAccess || st.GuestFirst != nil || !st.SMTP ||
		strings.Join(st.SSO, ",") != "oidc,saml" || st.FederationPeers != "2-10" || !st.DirectoryListed {
		t.Errorf("settings %+v", st)
	}
	u := r.Health.Updates
	if u.Attempts != 2 || u.Failures != 1 || u.Rollbacks != 1 || strings.Join(u.Errors, ",") != "checks" {
		t.Errorf("updates %+v", u)
	}
	hs := r.Health.Services
	if hs["ergo"] != (ServiceHealth{7, "unhealthy"}) || hs["postgres"].Restarts != 1000 || hs["files"].State != "restarting" {
		t.Errorf("health services %+v", hs)
	}
	if _, ok := hs["config-init"]; ok {
		t.Error("one-shot init containers are left out")
	}
	h := r.Health.Host
	if h == nil || *h.CPUP95 != 34.5 || *h.CPUMax != 98.2 || *h.MemP95 != 71 || *h.DiskUsed != 62.3 || *h.LoadPerCPU != 100 {
		t.Errorf("host %+v", h)
	}
	if r.UptimeDays == nil || *r.UptimeDays != 41 || r.Health.Healthy {
		t.Errorf("uptime %v healthy %v", r.UptimeDays, r.Health.Healthy)
	}
}

func TestMinimalReport(t *testing.T) {
	// Nothing configured: no Docker, no metrics, no auth.
	c := &Collector{S: Sources{GOARCH: "riscv64"}}
	_, r := buildJSON(t, c, Input{InstallID: testID, Reason: ReasonSetup, Now: now,
		Known: &Known{At: now.Add(-8 * 24 * time.Hour), Accounts: ptr[int64](3)}})
	if r.Health.Host != nil || r.UptimeDays != nil || r.Counts.Accounts != nil || r.Install.Arch != "other" ||
		r.Install.Mode != "unknown" || r.Stack.Version != "unknown" || !r.Health.Healthy || r.Settings.Registration != "open" {
		t.Fatalf("minimal %+v", r)
	}
	if strings.Join(r.Profiles, ",") != "admin" {
		t.Fatalf("profiles %v", r.Profiles)
	}
}

func TestAddonInstall(t *testing.T) {
	s := Sources{
		Docker: &fakeDocker{cs: []dockerapi.Container{container("evrd", "harbor.floret.dev/boson/evrd:0.9.0", "running"),
			container("evrd-links", "harbor.floret.dev/boson/evrd:0.9.0", "running")}},
		IRC: func(context.Context) *ircprobe.Result { return &ircprobe.Result{OK: true, Version: "UnrealIRCd-6.1.8"} },
	}
	_, r := buildJSON(t, &Collector{S: s}, Input{InstallID: testID, Reason: ReasonDaily, Now: now})
	if r.Install.Mode != "addon" || r.Install.IRCd != "unrealircd" || !contains(r.Profiles, "bridge") {
		t.Fatalf("install %+v profiles %v", r.Install, r.Profiles)
	}
}

func TestReportsValidateAgainstTheSchema(t *testing.T) {
	sch := compileSchema(t)
	full, _ := buildJSON(t, &Collector{S: fullSources()}, Input{InstallID: testID, Reason: ReasonUpdate, Now: now,
		Known: &Known{At: now, Accounts: ptr[int64](6000), Active7d: ptr[int64](0), Active30d: ptr[int64](1)}, IRCPeak: ptr(0)})
	minimal, _ := buildJSON(t, &Collector{S: Sources{}}, Input{InstallID: testID, Reason: ReasonSetup, Now: now})
	for name, b := range map[string][]byte{"full": full, "minimal": minimal} {
		if err := validate(t, sch, b); err != nil {
			t.Errorf("%s report doesn't validate: %v\n%s", name, err, b)
		}
		if len(b) > MaxBytes {
			t.Errorf("%s report is %d bytes", name, len(b))
		}
	}
	// The check must catch an extra or misspelled field and a wrong enum.
	for name, mutate := range map[string]func(m map[string]any){
		"extra top-level field": func(m map[string]any) { m["hostname"] = "x" },
		"misspelled field":      func(m map[string]any) { m["uptime_dayz"] = m["uptime_days"]; delete(m, "uptime_days") },
		"nested extra field":    func(m map[string]any) { m["settings"].(map[string]any)["sso_names"] = []any{} },
		"missing required":      func(m map[string]any) { delete(m["counts"].(map[string]any), "accounts") },
		"exact count":           func(m map[string]any) { m["counts"].(map[string]any)["accounts"] = "42" },
		"unknown service":       func(m map[string]any) { m["services"].(map[string]any)["my-own-bot"] = "1.0.0" },
		"bad version":           func(m map[string]any) { m["stack"].(map[string]any)["version"] = "1.2.3+meta" },
	} {
		var m map[string]any
		_ = json.Unmarshal(full, &m)
		mutate(m)
		b, _ := json.Marshal(m)
		if err := validate(t, sch, b); err == nil {
			t.Errorf("%s: the schema check didn't catch it", name)
		}
	}
}

// ---- no secrets, no names ----

// secrets are values a real install has in its env and config file.
var secrets = []string{
	"session-key-V2hhdCBhIHNlY3JldA==", "internal-token-7f3a9", "api-internal-token-d00d", "files-token-c0ffee",
	"directory-token-beef", "db-password-hunter2", "smtp-password-swordfish", "sso-client-secret-xyz",
	"sso-client-id-abc123", "github-token-ghp_123", "nick-pop3-password",
	"example-secret.org", "irc.example-secret.org", "auth.example-secret.org", "voice.example-secret.org",
	"admin@example-secret.org", "Secret Network Name", "secret-node.example.org", "alice", "bob@example-secret.org",
	"#secret-channel", "Corp Single Sign-On", "corp-sso", "https://idp.example-secret.org", "203.0.113.7",
	"boson-auth-1", "secret-project",
}

func TestReportHasNoSecretsOrNames(t *testing.T) {
	s := fullSources()
	s.Project = "secret-project"
	// Every source is full of secrets and names where it can be.
	d := s.Docker.(*fakeDocker)
	for i := range d.cs {
		d.cs[i].Names = []string{"/boson-auth-1"}
		d.cs[i].Labels["com.docker.compose.project.working_dir"] = "/home/alice/example-secret.org"
		d.cs[i].Labels["DOMAIN"] = "example-secret.org"
	}
	s.IRC = func(context.Context) *ircprobe.Result {
		return &ircprobe.Result{OK: true, Version: "ergo-2.19.1", Detail: "irc.example-secret.org 203.0.113.7", Users: ptr(3)}
	}
	s.PublicConfig = func(context.Context) (*authapi.PublicConfig, error) {
		return &authapi.PublicConfig{Registration: "open", Email: &authapi.PublicEmail{Configured: true},
			SSOProviders: []authapi.SSOProvider{{Slug: "corp-sso", Kind: "oidc", DisplayName: "Corp Single Sign-On"},
				{Slug: "sso-client-id-abc123", Kind: "https://idp.example-secret.org"}}}, nil
	}
	file := map[string]any{
		"network.name": "Secret Network Name", "auth.smtp.host": "smtp.example-secret.org",
		"auth.smtp.password": "smtp-password-swordfish", "auth.federation.allow": []any{"example-secret.org"},
		"irc.channels": []any{map[string]any{"name": "#secret-channel"}}, "admin.directory_hostname": "irc.example-secret.org",
		"auth.registration": "open",
	}
	s.Config = func(k string) (any, bool) { v, ok := file[k]; return v, ok }
	s.Runs = func(context.Context) []updates.RunSummary {
		return []updates.RunSummary{{Kind: "update", Actor: "alice", State: updates.StateFailed, StartedAt: now.Add(-time.Hour),
			Error: "pull harbor.example-secret.org: db-password-hunter2 refused", FailedStep: "pull", BackupDir: "/home/alice/backups",
			Items: []updates.Item{{Component: "auth", EnvVar: "AUTH_IMAGE", FromImage: "auth.example-secret.org/x:1"}}}}
	}
	s.Files = &fakeFiles{st: &bosonapi.FilesStatus{Config: &bosonapi.FilesConfigEcho{WindowSeconds: 86400},
		Totals: bosonapi.FilesTotals{WindowUploads: 4}, Errors: []bosonapi.FilesError{{Error: "bob@example-secret.org"}}}}
	known := &Known{At: now, Accounts: ptr[int64](5)}
	b, r := buildJSON(t, &Collector{S: s}, Input{InstallID: testID, Reason: ReasonDaily, Now: now, Known: known})
	for _, sec := range secrets {
		if bytes.Contains(bytes.ToLower(b), bytes.ToLower([]byte(sec))) {
			t.Errorf("the report contains %q:\n%s", sec, b)
		}
	}
	if strings.Join(r.Settings.SSO, ",") != "oidc,other" || r.Health.Updates.Errors[0] != "pull" {
		t.Fatalf("sso %v errors %v", r.Settings.SSO, r.Health.Updates.Errors)
	}
	if err := validate(t, compileSchema(t), b); err != nil {
		t.Fatal(err)
	}
}
