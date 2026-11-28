package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/dockerapi"
	"github.com/boson-chat/admin/internal/logger"
)

// Location is where the stack lives on the host, read from the compose
// labels on the panel's own container.
type Location struct {
	Project      string   `json:"project"`
	Dir          string   `json:"dir"`
	EnvFile      string   `json:"env_file"`
	ComposeFiles []string `json:"compose_files"`
	// MissingFiles: compose files the containers' labels list that no
	// longer exist (left out of ComposeFiles).
	MissingFiles []string `json:"missing_compose_files,omitempty"`
}

// Compose's labels on every container it creates.
const (
	labelWorkingDir  = "com.docker.compose.project.working_dir"
	labelConfigFiles = "com.docker.compose.project.config_files"
	labelEnvFile     = "com.docker.compose.project.environment_file"
	// LabelJob marks helper containers (and their util containers) so
	// they can be found and cleaned up by label.
	LabelJob    = "chat.boson.updates"
	LabelJobRun = "chat.boson.updates.run"
)

// JobOutput is what a helper job printed.
type JobOutput struct {
	Exit  int
	Lines []string
}

// Result returns the JSON of the job's @@RESULT <kind> line.
func (o *JobOutput) Result(kind string) (json.RawMessage, bool) {
	for _, l := range o.Lines {
		if rest, ok := strings.CutPrefix(l, "@@RESULT "+kind+" "); ok {
			return json.RawMessage(rest), true
		}
	}
	return nil, false
}

// Tagged returns the fields of every "@@<tag> …" line.
func (o *JobOutput) Tagged(tag string) [][]string {
	var out [][]string
	for _, l := range o.Lines {
		if rest, ok := strings.CutPrefix(l, "@@"+tag+" "); ok {
			out = append(out, strings.Fields(rest))
		}
	}
	return out
}

// Tail is the last n lines without the @@ markers (for an error message).
func (o *JobOutput) Tail(n int) string {
	var keep []string
	for _, l := range o.Lines {
		if !strings.HasPrefix(l, "@@") {
			keep = append(keep, l)
		}
	}
	if len(keep) > n {
		keep = keep[len(keep)-n:]
	}
	return strings.Join(keep, "\n")
}

// JobRunner runs helper jobs on the host.
type JobRunner interface {
	Locate(ctx context.Context) (*Location, error)
	// Run runs a job to completion, calling onLine for each output line.
	Run(ctx context.Context, runID, name, script string, env map[string]string, onLine func(string)) (*JobOutput, error)
	// Start starts a job and leaves it running (it outlives the panel).
	Start(ctx context.Context, runID, name, script string, env map[string]string) error
	// Collect returns a started job's output once it has exited (done) and
	// removes its container.
	Collect(ctx context.Context, runID, name string) (out *JobOutput, done bool, err error)
	// Ping: nil when the proxy jobs go through answers.
	Ping(ctx context.Context) error
}

// Helper runs jobs in a docker:cli container created through the socket proxy.
type Helper struct {
	Client  dockerapi.JobsClientImpl
	Project string
	// SelfID is the panel's container ID or prefix (its hostname).
	SelfID string
	// Image is the helper image (docker CLI with the compose plugin).
	Image string
	// BackupRoot is the host directory backups go in.
	BackupRoot string
	// Poll is how often a running job's logs are read.
	Poll time.Duration
	// FetchNetwork is the network of the one job that downloads (the stack
	// release bundle, job "stage"); every other job has none. Default
	// "bridge" (Docker's default network, with a way out).
	FetchNetwork string
}

func (h *Helper) poll() time.Duration {
	if h.Poll <= 0 {
		return time.Second
	}
	return h.Poll
}

// Ping asks the updates proxy for _ping.
func (h *Helper) Ping(ctx context.Context) error { return h.Client.Ping(ctx) }

func (h *Helper) Locate(ctx context.Context) (*Location, error) {
	loc, err := h.locateLabels(ctx)
	if err != nil {
		return nil, err
	}
	// The labels say which files the containers were created from, not which
	// exist now: ask the host (the helper sees the stack directory at its
	// host path; the panel doesn't see it at all).
	cands := composeCandidates(loc.Dir, loc.ComposeFiles)
	env := map[string]string{"STACK_DIR": loc.Dir, "ENV_FILE": loc.EnvFile, "COMPOSE_FILE": strings.Join(cands, ":")}
	runID := fmt.Sprintf("locate%d", time.Now().UnixNano())
	out, err := h.Run(ctx, runID, "files", scriptFiles, env, nil)
	if err != nil {
		return nil, fmt.Errorf("checking the stack's compose files: %w", err)
	}
	if out.Exit != 0 {
		return nil, fmt.Errorf("checking the stack's compose files failed (exit %d): %s", out.Exit, out.Tail(4))
	}
	exists := map[string]bool{}
	for _, l := range out.Lines {
		// The rest of the line is the path (it may contain spaces).
		if p, ok := strings.CutPrefix(l, "@@EXISTS "); ok {
			exists[p] = true
		}
	}
	files, missing, err := resolveComposeFiles(loc.Dir, loc.ComposeFiles, func(p string) bool { return exists[p] })
	if err != nil {
		return nil, err
	}
	if len(missing) > 0 {
		logger.FromCtx(ctx).Warn().Strs("missing", missing).Strs("compose_files", files).
			Msg("updates: compose files listed in the containers' labels no longer exist; leaving them out")
	}
	loc.ComposeFiles, loc.MissingFiles = files, missing
	return loc, nil
}

// locateLabels reads the location from compose's labels alone.
func (h *Helper) locateLabels(ctx context.Context) (*Location, error) {
	cs, err := h.Client.List(ctx, h.Project)
	if err != nil {
		return nil, err
	}
	// The panel's own container first; any other of the project will do.
	sort.SliceStable(cs, func(i, j int) bool {
		return h.SelfID != "" && strings.HasPrefix(cs[i].ID, h.SelfID) && !strings.HasPrefix(cs[j].ID, h.SelfID)
	})
	for _, c := range cs {
		dir := c.Labels[labelWorkingDir]
		if dir == "" {
			continue
		}
		loc := &Location{Project: h.Project, Dir: dir}
		for _, f := range strings.Split(c.Labels[labelConfigFiles], ",") {
			if f = strings.TrimSpace(f); f != "" {
				if !filepath.IsAbs(f) {
					f = filepath.Join(dir, f)
				}
				loc.ComposeFiles = append(loc.ComposeFiles, f)
			}
		}
		if len(loc.ComposeFiles) == 0 {
			loc.ComposeFiles = []string{filepath.Join(dir, "compose.yaml")}
		}
		loc.EnvFile = filepath.Join(dir, ".env")
		if ef := strings.TrimSpace(strings.Split(c.Labels[labelEnvFile], ",")[0]); ef != "" {
			if !filepath.IsAbs(ef) {
				ef = filepath.Join(dir, ef)
			}
			loc.EnvFile = ef
		}
		return loc, nil
	}
	return nil, apperr.New(502, "stack_not_found", "can't tell where the stack lives: no container of the "+h.Project+" compose project has compose's working_dir label")
}

// Docker compose's own file names, in the order it looks for them when
// neither -f nor COMPOSE_FILE is given.
var (
	defaultComposeNames  = []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}
	defaultOverrideNames = []string{"compose.override.yaml", "compose.override.yml", "docker-compose.override.yaml", "docker-compose.override.yml"}
)

func inDirNamed(dir, f string, names []string) bool {
	return filepath.Dir(f) == filepath.Clean(dir) && slices.Contains(names, filepath.Base(f))
}

// defaultSet: the labelled files are what plain `docker compose up` picks
// (a default-named file in the project directory, plus its override), so
// the stack was not started with a custom -f / COMPOSE_FILE.
func defaultSet(dir string, labelled []string) bool {
	if len(labelled) == 0 || len(labelled) > 2 || !inDirNamed(dir, labelled[0], defaultComposeNames) {
		return false
	}
	return len(labelled) == 1 || inDirNamed(dir, labelled[1], defaultOverrideNames)
}

// composeCandidates is every file whose existence decides the compose file
// list: the labelled ones and, for a default set, compose's override names.
func composeCandidates(dir string, labelled []string) []string {
	out := append([]string(nil), labelled...)
	if defaultSet(dir, labelled) {
		for _, n := range defaultOverrideNames {
			if p := filepath.Join(dir, n); !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	return out
}

// resolveComposeFiles is the COMPOSE_FILE for the helper from the files the
// containers' labels list and the files that exist now.
//
// Labels are frozen when a container is created: delete an override file
// and every container still names it, and compose refuses a COMPOSE_FILE
// with a missing file. A default set (compose.yaml, maybe with
// compose.override.yaml) is resolved the way docker compose itself does
// without -f: the main file plus the override if one exists now, whether
// or not the labels had it. A custom set (-f compose.yaml -f
// compose.auth-src.yaml…) is kept as it is, in order, minus the files that
// are gone; nothing is added to it, as compose adds nothing to an explicit
// -f list. The first file is the project's main file: if it is gone the
// stack can't be updated, and that is an error.
func resolveComposeFiles(dir string, labelled []string, exists func(string) bool) (files, missing []string, err error) {
	if len(labelled) == 0 {
		labelled = []string{filepath.Join(dir, "compose.yaml")}
	}
	if !exists(labelled[0]) {
		return nil, nil, apperr.New(409, "compose_file_missing", fmt.Sprintf(
			"the stack's compose file %s is missing, so the stack can't be updated: put it back (or recreate the stack with docker compose up -d from %s)", labelled[0], dir))
	}
	files = []string{labelled[0]}
	if defaultSet(dir, labelled) {
		if len(labelled) == 2 && !exists(labelled[1]) {
			missing = append(missing, labelled[1])
		}
		for _, n := range defaultOverrideNames {
			if p := filepath.Join(dir, n); exists(p) {
				files = append(files, p)
				break
			}
		}
		return files, missing, nil
	}
	for _, f := range labelled[1:] {
		switch {
		case slices.Contains(files, f):
		case exists(f):
			files = append(files, f)
		default:
			missing = append(missing, f)
		}
	}
	return files, missing, nil
}

// jobStage is the job that downloads the stack release bundle: the only one
// with a network.
const jobStage = "stage"

func jobName(runID, name string) string { return "boson-updates-" + runID + "-" + name }

func (h *Helper) spec(runID, name, script string, env map[string]string) (dockerapi.JobSpec, error) {
	if err := validEnv(env); err != nil {
		return dockerapi.JobSpec{}, err
	}
	dir := env["STACK_DIR"]
	binds := []string{"/var/run/docker.sock:/var/run/docker.sock", dir + ":" + dir}
	if r := env["BACKUP_ROOT"]; r != "" && !within(r, dir) {
		binds = append(binds, r+":"+r)
	}
	seen := map[string]bool{dir: true}
	extra := strings.Split(env["COMPOSE_FILE"], ":")
	extra = append(extra, env["ENV_FILE"])
	for _, f := range extra {
		d := filepath.Dir(f)
		if f == "" || within(d, dir) || seen[d] {
			continue
		}
		seen[d] = true
		binds = append(binds, d+":"+d+":ro")
	}
	vars := make([]string, 0, len(env))
	for k, v := range env {
		vars = append(vars, k+"="+v)
	}
	sort.Strings(vars)
	network := "none"
	if name == jobStage {
		network = h.FetchNetwork
		if network == "" {
			network = "bridge"
		}
	}
	return dockerapi.JobSpec{
		Name: jobName(runID, name), Image: h.Image, Cmd: []string{"sh", "-c", script}, Env: vars,
		Labels:     map[string]string{LabelJob: "job", LabelJobRun: runID},
		Binds:      binds,
		WorkingDir: dir, NetworkMode: network,
	}, nil
}

func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

func (h *Helper) removeNamed(ctx context.Context, spec dockerapi.JobSpec) {
	if old, _ := h.Client.ListLabel(ctx, LabelJobRun+"="+spec.Labels[LabelJobRun]); len(old) > 0 {
		for _, c := range old {
			if c.Name() == spec.Name {
				_ = h.Client.Remove(ctx, c.ID, true)
			}
		}
	}
}

func (h *Helper) create(ctx context.Context, spec dockerapi.JobSpec) (string, error) {
	// A leftover with the same name (a crashed run): ours by label, so remove it.
	h.removeNamed(ctx, spec)
	if err := h.Client.Pull(ctx, spec.Image); err != nil {
		// Offline but already present is fine; create says otherwise.
		_ = err
	}
	id, err := h.Client.Create(ctx, spec)
	// The client doesn't retry a create the proxy may have carried out
	// (the connection dropped after sending it): the container, if it was
	// made, goes, then a second try.
	for try := 0; try < 2 && err != nil && errors.Is(err, dockerapi.ErrUnavailable) && dockerapi.IsTransient(err) && ctx.Err() == nil; try++ {
		h.removeNamed(ctx, spec)
		id, err = h.Client.Create(ctx, spec)
	}
	if err != nil {
		return "", fmt.Errorf("starting the update helper (%s): %w", spec.Image, err)
	}
	if err := h.Client.Start(ctx, id); err != nil {
		_ = h.Client.Remove(context.WithoutCancel(ctx), id, true)
		return "", err
	}
	return id, nil
}

func (h *Helper) Run(ctx context.Context, runID, name, script string, env map[string]string, onLine func(string)) (*JobOutput, error) {
	spec, err := h.spec(runID, name, script, env)
	if err != nil {
		return nil, err
	}
	id, err := h.create(ctx, spec)
	if err != nil {
		return nil, err
	}
	defer func() { _ = h.Client.Remove(context.WithoutCancel(ctx), id, true) }()
	out := &JobOutput{}
	var since time.Time
	read := func() {
		lines, err := h.Client.Logs(ctx, id, dockerapi.LogOptions{Since: since})
		if err != nil {
			return
		}
		for _, l := range lines {
			out.Lines = append(out.Lines, l.Text)
			if onLine != nil {
				onLine(l.Text)
			}
			if l.At.After(since) {
				since = l.At
			}
		}
	}
	for {
		in, err := h.Client.Inspect(ctx, id)
		if err != nil {
			return out, err
		}
		read()
		if !in.State.Running && in.State.Status != "created" {
			out.Exit = in.State.ExitCode
			return out, nil
		}
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(h.poll()):
		}
	}
}

func (h *Helper) Start(ctx context.Context, runID, name, script string, env map[string]string) error {
	spec, err := h.spec(runID, name, script, env)
	if err != nil {
		return err
	}
	_, err = h.create(ctx, spec)
	return err
}

func (h *Helper) Collect(ctx context.Context, runID, name string) (*JobOutput, bool, error) {
	cs, err := h.Client.ListLabel(ctx, LabelJobRun+"="+runID)
	if err != nil {
		return nil, false, err
	}
	for _, c := range cs {
		if c.Name() != jobName(runID, name) {
			continue
		}
		in, err := h.Client.Inspect(ctx, c.ID)
		if err != nil {
			return nil, false, err
		}
		if in.State.Running || in.State.Status == "created" {
			return nil, false, nil
		}
		lines, err := h.Client.Logs(ctx, c.ID, dockerapi.LogOptions{})
		if err != nil {
			return nil, false, err
		}
		out := &JobOutput{Exit: in.State.ExitCode}
		for _, l := range lines {
			out.Lines = append(out.Lines, l.Text)
		}
		_ = h.Client.Remove(ctx, c.ID, true)
		return out, true, nil
	}
	return nil, false, errJobGone
}

var errJobGone = errors.New("the update helper container is gone")

// parseKB reads a number from a result field.
func parseKB(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	}
	return 0
}
