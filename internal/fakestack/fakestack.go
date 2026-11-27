// Package fakestack is a stand-in Boson host for the Updates tests: the
// GitHub releases API, an OCI registry, the Docker Engine API (as the socket
// proxy passes it) and the stack's service endpoints, in one HTTP server.
//
// Helper jobs aren't shell-executed: a created job container "runs" its job
// by name (preflight, backup, pull, configure, recreate, fingerprint,
// rollback, self) against the fake state, printing the lines the real
// scripts print (@@RESULT, @@IMAGE, @@MIG, @@FILE…). The scripts themselves
// are tested against a stub docker CLI in the updates package.
package fakestack

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	labelProject = "com.docker.compose.project"
	labelService = "com.docker.compose.service"
	labelWorkDir = "com.docker.compose.project.working_dir"
	labelFiles   = "com.docker.compose.project.config_files"
	labelRun     = "chat.boson.updates.run"
)

// Release is a GitHub release in the fake.
type Release struct {
	Tag    string  `json:"tag_name"`
	Name   string  `json:"name"`
	Body   string  `json:"body"`
	At     string  `json:"published_at"`
	URL    string  `json:"html_url"`
	Assets []Asset `json:"assets"`
}

// Asset is a release asset.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// StackRelease is a boson-chat/stack release: served as a GitHub release
// with its bundle, .sha256 and manifest.json, and what the stack jobs
// (stack-plan, stack-apply) report and do with it.
type StackRelease struct {
	Version string
	Body    string
	// Manifest is manifest.json (the test writes it).
	Manifest string
	// Bundle is the tarball's bytes (anything: only its SHA-256 matters).
	Bundle []byte
	// BadSHA: the .sha256 asset doesn't match the bundle.
	BadSHA bool
	// NoAssets: a release without its bundle yet.
	NoAssets bool
	// Files: "<action> <path>" as stack-apply.sh prints them; Diffs per path.
	Files []string
	Diffs map[string][]string
	// Migrations: name → about; those not in Stack.Applied are pending.
	Migrations map[string]string
	// Hashes: compose config hashes with this release (a service that has
	// no container yet is new); unlisted services keep theirs.
	Hashes map[string]string
	// Defaults: the compose default image per service (services without an
	// env var, or with none set in .env) after this release.
	Defaults map[string]string
	// Restart: services the migrations ask to restart.
	Restart []string
	// FailMigration: stack-apply fails in the migrations (after the files).
	FailMigration bool
}

type container struct {
	ID, Name, Service, Image string
	State                    string // running, exited, created
	Health                   string
	Exit                     int
	Labels                   map[string]string
	logs                     []string
	job                      string
	env                      map[string]string
	hold                     bool
	network                  string
}

type snapshot struct {
	env        map[string]string
	migrations map[string]string
	stack      *stackSnap
}

type stackSnap struct {
	version  string
	baseline bool
	applied  []string
	hashes   map[string]string
	defaults map[string]string
}

// Stack is the fake host.
type Stack struct {
	mu      sync.Mutex
	Project string
	Dir     string
	// Env is the stack .env (only the *_IMAGE lines matter).
	Env map[string]string
	// Releases per GitHub repo, newest first.
	Releases map[string][]Release
	// Tags present in the registry ("repo/name:tag", host stripped).
	Tags map[string]bool
	// Broken image refs never become healthy.
	Broken map[string]bool
	// Migrates: an image ref that, when it starts, changes this database's
	// migration table.
	Migrates map[string]string
	// Migrations: "db table" → hash, as the fingerprint prints it.
	Migrations map[string]string
	FreeKB     int64
	// RateLimited: GitHub answers 403 with the limit used up.
	RateLimited bool
	// HoldSelf: the self-update job keeps running until ReleaseSelf.
	HoldSelf bool
	// Files that exist on the host (compose files). A job given a
	// COMPOSE_FILE naming one that doesn't fails the way compose does.
	Files map[string]bool
	// ConfigFiles is the config_files label of containers created from now
	// on (comma-separated, as compose writes it); a recreate sets it to the
	// job's COMPOSE_FILE.
	ConfigFiles string

	// The installed stack: VERSION ("" = pre-release), whether its files
	// are recorded, migrations run, compose config hashes and default
	// images (service → hash / image), and the releases on GitHub.
	StackVersion  string
	StackBaseline bool
	Applied       []string
	Hashes        map[string]string
	Defaults      map[string]string
	Profiles      string
	StackReleases []*StackRelease
	// Downloads counts bundle downloads (stage jobs that fetched).
	Downloads int
	// StageNetworks: the network mode of each stage job (it must have one).
	StageNetworks []string

	cts      map[string]*container
	backups  map[string]snapshot
	restored []string
	seq      int
	clock    time.Time
	ghCalls  int
	Requests []string
	Server   string // base URL, set by the caller (for the registry realm)
	AdminID  string
}

// New is a stack running the given images (service → image ref).
func New(images map[string]string) *Stack {
	s := &Stack{Project: "boson", Dir: "/srv/boson", Env: map[string]string{}, Releases: map[string][]Release{},
		Tags: map[string]bool{}, Broken: map[string]bool{}, Migrates: map[string]string{},
		Migrations: map[string]string{"auth public.schema_migrations": "a1", "api public.goose_db_version": "b1"},
		FreeKB:     50 << 20, cts: map[string]*container{}, backups: map[string]snapshot{},
		Files: map[string]bool{"/srv/boson/compose.yaml": true}, ConfigFiles: "/srv/boson/compose.yaml",
		Hashes: map[string]string{}, Defaults: map[string]string{},
		clock: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)}
	svcs := make([]string, 0, len(images))
	for svc := range images {
		svcs = append(svcs, svc)
	}
	sort.Strings(svcs)
	for _, svc := range svcs {
		c := s.add(svc, images[svc])
		if svc == "admin" {
			s.AdminID = c.ID
		}
	}
	return s
}

func (s *Stack) add(svc, image string) *container {
	s.seq++
	id := fmt.Sprintf("%012x%052d", 0xc0ffee000000+s.seq, s.seq)
	c := &container{ID: id, Name: s.Project + "-" + svc + "-1", Service: svc, Image: image, State: "running",
		Labels: map[string]string{labelProject: s.Project, labelService: svc, labelWorkDir: s.Dir,
			labelFiles: s.ConfigFiles}}
	s.cts[id] = c
	return c
}

// Image is the image a service runs now.
func (s *Stack) Image(svc string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.cts {
		if c.Service == svc && c.job == "" {
			return c.Image
		}
	}
	return ""
}

// EnvValue reads the fake .env.
func (s *Stack) EnvValue(k string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Env[k]
}

// SetConfigFiles relabels every service container as created from these
// compose files (comma-separated), and the ones created from now on.
func (s *Stack) SetConfigFiles(files string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ConfigFiles = files
	for _, c := range s.cts {
		if c.job == "" {
			c.Labels[labelFiles] = files
		}
	}
}

// Labels is a service container's compose labels.
func (s *Stack) Labels(svc string) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.svc(svc); c != nil {
		return cp(c.Labels)
	}
	return nil
}

// Restored lists the databases a rollback restored.
func (s *Stack) Restored() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.restored...)
}

// Jobs lists helper containers still present (should be none after a run).
func (s *Stack) Jobs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, c := range s.cts {
		if c.job != "" {
			out = append(out, c.Name)
		}
	}
	return out
}

// ReleaseSelf lets a held self-update job finish.
func (s *Stack) ReleaseSelf() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.cts {
		if c.job == "self" && c.hold {
			c.hold = false
			s.runJob(c)
		}
	}
}

// GitHubCalls counts release list requests that reached the fake.
func (s *Stack) GitHubCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ghCalls
}

// Set changes the fake under its lock (tests).
func (s *Stack) Set(fn func(s *Stack)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ServeHTTP routes /gh (GitHub), /v2 + /token (registry), /docker
// (Engine API), /check/<service> (health), /auth (auth's public API) and
// /__state (the scenario, for the browser tests).
func (s *Stack) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.Requests = append(s.Requests, r.Method+" "+r.URL.Path)
	s.mu.Unlock()
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/gh/"):
		s.github(w, r, strings.TrimPrefix(p, "/gh"))
	case strings.HasPrefix(p, "/v2/") || p == "/token":
		s.registry(w, r)
	case strings.HasPrefix(p, "/docker/"):
		s.docker(w, r, strings.TrimPrefix(p, "/docker"))
	case strings.HasPrefix(p, "/assets/"):
		s.asset(w, r, strings.TrimPrefix(p, "/assets/"))
	case strings.HasPrefix(p, "/check/"):
		s.check(w, strings.TrimPrefix(p, "/check/"))
	case strings.HasPrefix(p, "/auth/"):
		s.auth(w, r, strings.TrimPrefix(p, "/auth"))
	case p == "/__state":
		s.state(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Stack) github(w http.ResponseWriter, r *http.Request, p string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reset := strconv.FormatInt(time.Now().Add(30*time.Minute).Unix(), 10)
	w.Header().Set("X-RateLimit-Limit", "60")
	w.Header().Set("X-RateLimit-Reset", reset)
	if s.RateLimited {
		w.Header().Set("X-RateLimit-Remaining", "0")
		writeJSON(w, http.StatusForbidden, map[string]string{"message": "API rate limit exceeded"})
		return
	}
	s.ghCalls++
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(max(0, 60-s.ghCalls)))
	repo, ok := strings.CutSuffix(strings.TrimPrefix(p, "/repos/"), "/releases")
	rs, found := s.Releases[repo]
	if repo == "boson-chat/stack" && len(s.StackReleases) > 0 {
		rs, found = s.stackGitHub(), true
	}
	if !ok || !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	b, _ := json.Marshal(rs)
	etag := fmt.Sprintf(`"%x"`, len(b)*31+int(b[len(b)/2]))
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

func (s *Stack) registry(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/token" {
		writeJSON(w, http.StatusOK, map[string]string{"token": "anon"})
		return
	}
	if r.Header.Get("Authorization") != "Bearer anon" {
		w.Header().Set("Www-Authenticate", `Bearer realm="`+s.Server+`/token",service="harbor-registry"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/v2/")
	repo, tag, ok := strings.Cut(rest, "/manifests/")
	s.mu.Lock()
	exists := ok && s.Tags[repo+":"+tag]
	s.mu.Unlock()
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// healthy: a service is up unless its image is marked broken.
func (s *Stack) healthy(svc string) (bool, bool) {
	for _, c := range s.cts {
		if c.Service == svc && c.job == "" {
			return c.State == "running" && !s.Broken[c.Image], true
		}
	}
	return false, false
}

func (s *Stack) check(w http.ResponseWriter, svc string) {
	s.mu.Lock()
	ok, found := s.healthy(svc)
	s.mu.Unlock()
	switch {
	case !found:
		w.WriteHeader(http.StatusServiceUnavailable)
	case ok:
		_, _ = w.Write([]byte("<!doctype html><title>ok</title>"))
	default:
		w.WriteHeader(http.StatusServiceUnavailable)
	}
}

func (s *Stack) auth(w http.ResponseWriter, r *http.Request, p string) {
	s.mu.Lock()
	ok, _ := s.healthy("auth")
	s.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	switch p {
	case "/v1/voice/token":
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"token": "eyJ.eyJ.sig"})
	case "/.well-known/jwks.json":
		writeJSON(w, http.StatusOK, map[string]any{"keys": []any{map[string]string{"kty": "OKP"}}})
	default:
		http.NotFound(w, r)
	}
}

// state: GET dumps, POST merges a scenario (browser tests).
func (s *Stack) state(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var in struct {
			Broken      map[string]bool   `json:"broken"`
			Migrates    map[string]string `json:"migrates"`
			RateLimited *bool             `json:"rate_limited"`
			FreeKB      *int64            `json:"free_kb"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		s.mu.Lock()
		for k, v := range in.Broken {
			s.Broken[k] = v
		}
		for k, v := range in.Migrates {
			s.Migrates[k] = v
		}
		if in.RateLimited != nil {
			s.RateLimited = *in.RateLimited
		}
		if in.FreeKB != nil {
			s.FreeKB = *in.FreeKB
		}
		s.mu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	imgs := map[string]string{}
	for _, c := range s.cts {
		if c.job == "" {
			imgs[c.Service] = c.Image
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"images": imgs, "env": s.Env, "restored": s.restored, "migrations": s.Migrations})
}

func (s *Stack) docker(w http.ResponseWriter, r *http.Request, p string) {
	// Strip an API version prefix (/v1.47/...).
	if strings.HasPrefix(p, "/v1.") {
		if i := strings.Index(p[1:], "/"); i >= 0 {
			p = p[i+1:]
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && p == "/containers/json":
		s.list(w, r)
	case r.Method == http.MethodPost && p == "/images/create":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"Pulling"}` + "\n" + `{"status":"Done"}` + "\n"))
	case r.Method == http.MethodPost && p == "/containers/create":
		s.create(w, r)
	case strings.HasPrefix(p, "/containers/"):
		rest := strings.TrimPrefix(p, "/containers/")
		id, op, _ := strings.Cut(rest, "/")
		c := s.find(id)
		if c == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "No such container: " + id})
			return
		}
		switch {
		case r.Method == http.MethodGet && op == "json":
			s.inspect(w, c)
		case r.Method == http.MethodGet && op == "logs":
			w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
			since, _ := strconv.ParseFloat(r.URL.Query().Get("since"), 64)
			for _, l := range c.logs {
				ts, _, _ := strings.Cut(l, " ")
				t, _ := time.Parse(time.RFC3339Nano, ts)
				if since > 0 && float64(t.UnixNano())/1e9 < since-1e-9 {
					continue
				}
				_, _ = w.Write([]byte(l + "\n"))
			}
		case r.Method == http.MethodPost && op == "start":
			if c.job != "" && c.State == "created" {
				c.State = "running"
				if !(c.job == "self" && c.hold) {
					s.runJob(c)
				}
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && op == "":
			delete(s.cts, c.ID)
			w.WriteHeader(http.StatusNoContent)
		default:
			writeJSON(w, http.StatusForbidden, map[string]string{"message": "forbidden by the fake proxy"})
		}
	default:
		writeJSON(w, http.StatusForbidden, map[string]string{"message": "forbidden by the fake proxy"})
	}
}

func (s *Stack) find(id string) *container {
	if c, ok := s.cts[id]; ok {
		return c
	}
	for _, c := range s.cts {
		if c.Name == id || strings.HasPrefix(c.ID, id) && len(id) >= 12 {
			return c
		}
	}
	return nil
}

type apiContainer struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	ImageID string            `json:"ImageID"`
	Labels  map[string]string `json:"Labels"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Created int64             `json:"Created"`
}

func (s *Stack) list(w http.ResponseWriter, r *http.Request) {
	var f map[string][]string
	_ = json.Unmarshal([]byte(r.URL.Query().Get("filters")), &f)
	var out []apiContainer
	ids := make([]string, 0, len(s.cts))
	for id := range s.cts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		c := s.cts[id]
		match := true
		for _, l := range f["label"] {
			k, v, hasV := strings.Cut(l, "=")
			got, ok := c.Labels[k]
			if !ok || hasV && got != v {
				match = false
			}
		}
		if !match {
			continue
		}
		out = append(out, apiContainer{ID: c.ID, Names: []string{"/" + c.Name}, Image: c.Image, ImageID: "sha256:" + digest(c.Image),
			Labels: c.Labels, State: c.State, Status: c.State, Created: 1})
	}
	if out == nil {
		out = []apiContainer{}
	}
	writeJSON(w, http.StatusOK, out)
}

func digest(ref string) string {
	h := uint64(1469598103934665603)
	for i := 0; i < len(ref); i++ {
		h ^= uint64(ref[i])
		h *= 1099511628211
	}
	return fmt.Sprintf("%016x%016x%016x%016x", h, h^0xabc, h^0xdef, h^0x123)
}

func (s *Stack) inspect(w http.ResponseWriter, c *container) {
	st := map[string]any{"Status": c.State, "Running": c.State == "running", "Restarting": false,
		"ExitCode": c.Exit, "StartedAt": "2026-11-26T12:00:00Z", "FinishedAt": "0001-01-01T00:00:00Z"}
	if c.job == "" && s.Broken[c.Image] {
		st["Status"], st["Restarting"], st["Running"], st["ExitCode"] = "restarting", true, true, 1
	}
	if c.Health != "" {
		st["Health"] = map[string]string{"Status": c.Health}
	}
	writeJSON(w, http.StatusOK, map[string]any{"Id": c.ID, "Name": "/" + c.Name, "Image": "sha256:" + digest(c.Image),
		"RestartCount": 0, "State": st, "Config": map[string]any{"Image": c.Image, "Labels": c.Labels, "Tty": false}})
}

func (s *Stack) create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Image      string
		Cmd        []string
		Env        []string
		Labels     map[string]string
		HostConfig struct{ NetworkMode string }
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	name := r.URL.Query().Get("name")
	for _, c := range s.cts {
		if c.Name == name {
			writeJSON(w, http.StatusConflict, map[string]string{"message": "name in use"})
			return
		}
	}
	runID := body.Labels[labelRun]
	job := strings.TrimPrefix(name, "boson-updates-"+runID+"-")
	s.seq++
	c := &container{ID: fmt.Sprintf("%064x", 0xfeed0000+s.seq), Name: name, Image: body.Image, State: "created",
		Labels: body.Labels, job: job, env: map[string]string{}, hold: job == "self" && s.HoldSelf, network: body.HostConfig.NetworkMode}
	for _, kv := range body.Env {
		k, v, _ := strings.Cut(kv, "=")
		c.env[k] = v
	}
	s.cts[c.ID] = c
	writeJSON(w, http.StatusCreated, map[string]string{"Id": c.ID})
}

func (s *Stack) say(c *container, format string, a ...any) {
	s.clock = s.clock.Add(time.Millisecond)
	c.logs = append(c.logs, s.clock.Format(time.RFC3339Nano)+" "+fmt.Sprintf(format, a...))
}

func (s *Stack) svc(name string) *container {
	for _, c := range s.cts {
		if c.Service == name && c.job == "" {
			return c
		}
	}
	return nil
}

// up recreates a service from the .env value of the variable that names its
// image (the fake knows them by the image's repository), else from its
// compose default (Defaults); a service with no container yet but a default
// is created.
func (s *Stack) up(name string) {
	c := s.svc(name)
	if c == nil {
		if img := s.Defaults[name]; img != "" {
			s.add(name, img)
		}
		return
	}
	v := s.effective(name)
	old := c.Image
	delete(s.cts, c.ID)
	n := s.add(name, v)
	if name == "admin" {
		s.AdminID = n.ID
	}
	if db := s.Migrates[v]; db != "" && v != old {
		for k := range s.Migrations {
			if strings.HasPrefix(k, db+" ") {
				s.Migrations[k] += "+" + ImageTagOf(v)
			}
		}
	}
}

// ImageTagOf is the tag of an image reference.
func ImageTagOf(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		return ref[i+1:]
	}
	return ""
}

func (s *Stack) setenv(c *container, list string) {
	for _, kv := range strings.Fields(list) {
		k, v, _ := strings.Cut(kv, "=")
		s.Env[k] = v
		s.say(c, "@@SET %s=%s", k, v)
	}
}

func (s *Stack) fingerprint(c *container) {
	keys := make([]string, 0, len(s.Migrations))
	for k := range s.Migrations {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s.say(c, "@@MIG %s %s", k, s.Migrations[k])
	}
}

func cp(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (s *Stack) runJob(c *container) {
	e := c.env
	fail := func(msg string) {
		s.say(c, "%s", msg)
		c.State, c.Exit = "exited", 1
	}
	if s.stackJob(c) {
		return
	}
	if c.job == "files" {
		for _, f := range strings.Split(e["COMPOSE_FILE"], ":") {
			if f != "" && s.Files[f] {
				s.say(c, "@@EXISTS %s", f)
			}
		}
		s.say(c, "@@RESULT files {}")
		c.State = "exited"
		return
	}
	if cf := e["COMPOSE_FILE"]; cf != "" {
		for _, f := range strings.Split(cf, ":") {
			if !s.Files[f] {
				fail(fmt.Sprintf("compose file %q set by COMPOSE_FILE environment variable is invalid: stat %s: no such file or directory", f, f))
				return
			}
		}
		s.ConfigFiles = strings.ReplaceAll(cf, ":", ",")
	}
	switch c.job {
	case "preflight":
		s.say(c, "@@STEP checking the compose project")
		s.say(c, `@@RESULT preflight {"free_kb":%d,"need_kb":%d}`, s.FreeKB, 2048)
	case "backup":
		dir := e["BACKUP_DIR"]
		s.say(c, "@@STEP saving .env and config")
		for _, svc := range strings.Fields(e["SERVICES"]) {
			if x := s.svc(svc); x != nil {
				repo := x.Image
				if i := strings.LastIndex(repo, ":"); i > strings.LastIndex(repo, "/") {
					repo = repo[:i]
				}
				s.say(c, "@@IMAGE %s %s sha256:%s %s@sha256:%s", svc, x.Image, digest(x.Image), repo, digest(x.Image))
			}
		}
		s.say(c, "@@STEP dumping the databases")
		s.fingerprint(c)
		for _, f := range []string{"env 812", "config.tgz 2048", "images.txt 400", "pg-globals.sql 900", "pg-auth.dump 81234", "pg-api.dump 40321", "vol-ergo_db.tgz 5120"} {
			s.say(c, "@@FILE %s", f)
		}
		snap := snapshot{env: cp(s.Env), migrations: cp(s.Migrations)}
		if e["STACK_FILES"] == "1" {
			s.say(c, "@@STEP saving the stack directory")
			s.say(c, "@@FILE stack-dir.tgz 300123")
			snap.stack = s.snapStack()
		}
		s.backups[dir] = snap
		s.say(c, `@@RESULT backup {"dir":"%s"}`, dir)
	case "pull":
		for _, img := range strings.Fields(e["IMAGES"]) {
			s.say(c, "@@STEP pulling %s", img)
			name := img
			if i := strings.Index(name, "/"); i >= 0 && strings.ContainsAny(name[:i], ".:") {
				name = name[i+1:]
			}
			if !s.Tags[name] {
				fail("Error response from daemon: manifest for " + img + " not found")
				return
			}
		}
		s.say(c, "@@RESULT pull {}")
	case "configure":
		s.setenv(c, e["SETS"])
		s.say(c, "@@RESULT configure {}")
	case "recreate":
		for _, p := range strings.Fields(e["PRE"]) {
			s.say(c, "@@STEP running %s", p)
		}
		s.say(c, "@@STEP recreating %s", e["SERVICES"])
		for _, svc := range append(strings.Fields(e["SERVICES"]), strings.Fields(e["FORCE"])...) {
			s.up(svc)
			s.say(c, "Container boson-%s-1  Started", svc)
		}
		s.say(c, "@@RESULT recreate {}")
	case "fingerprint":
		s.fingerprint(c)
		s.say(c, "@@RESULT fingerprint {}")
	case "rollback":
		snap, ok := s.backups[e["BACKUP_DIR"]]
		if !ok {
			fail("cat: " + e["BACKUP_DIR"] + "/env: No such file or directory")
			return
		}
		services := strings.Fields(e["SERVICES"])
		if e["STACK_RESTORE"] == "1" && snap.stack != nil {
			s.say(c, "@@STEP restoring the stack files")
			s.restoreStack(snap.stack)
		}
		s.say(c, "@@STEP restoring .env and config")
		s.Env = cp(snap.env)
		s.setenv(c, e["SETS"])
		for _, db := range strings.Fields(e["RESTORE_DBS"]) {
			s.say(c, "@@STEP restoring database %s", db)
			for k := range s.Migrations {
				if strings.HasPrefix(k, db+" ") {
					s.Migrations[k] = snap.migrations[k]
				}
			}
			s.restored = append(s.restored, db)
		}
		for _, svc := range append(services, strings.Fields(e["STOP"])...) {
			s.up(svc)
		}
		s.say(c, "@@RESULT rollback {}")
	case "self":
		s.setenv(c, e["TARGET_SETS"])
		s.up("admin")
		if s.Broken[s.Env["ADMIN_IMAGE"]] {
			s.say(c, "@@STEP the new panel did not stay up: putting the previous one back")
			s.setenv(c, e["FALLBACK_SETS"])
			s.up("admin")
			s.say(c, `@@RESULT self {"ok":false,"fell_back":true}`)
		} else {
			s.say(c, `@@RESULT self {"ok":true}`)
		}
	default:
		fail("unknown job " + c.job)
		return
	}
	c.State = "exited"
}

// Handler with a base URL: the fake needs its own address for the
// registry's token realm.
func (s *Stack) Handler(base string) http.Handler {
	s.Server = strings.TrimRight(base, "/")
	return s
}
