package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/dockerapi"
)

// Location is where the stack lives on the host, read from the compose
// labels on the panel's own container.
type Location struct {
	Project      string   `json:"project"`
	Dir          string   `json:"dir"`
	EnvFile      string   `json:"env_file"`
	ComposeFiles []string `json:"compose_files"`
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
}

func (h *Helper) poll() time.Duration {
	if h.Poll <= 0 {
		return time.Second
	}
	return h.Poll
}

func (h *Helper) Locate(ctx context.Context) (*Location, error) {
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
	return dockerapi.JobSpec{
		Name: jobName(runID, name), Image: h.Image, Cmd: []string{"sh", "-c", script}, Env: vars,
		Labels:     map[string]string{LabelJob: "job", LabelJobRun: runID},
		Binds:      binds,
		WorkingDir: dir, NetworkMode: "none",
	}, nil
}

func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

func (h *Helper) create(ctx context.Context, spec dockerapi.JobSpec) (string, error) {
	// A leftover with the same name (a crashed run): ours by label, so remove it.
	if old, _ := h.Client.ListLabel(ctx, LabelJobRun+"="+spec.Labels[LabelJobRun]); len(old) > 0 {
		for _, c := range old {
			if c.Name() == spec.Name {
				_ = h.Client.Remove(ctx, c.ID, true)
			}
		}
	}
	if err := h.Client.Pull(ctx, spec.Image); err != nil {
		// Offline but already present is fine; create says otherwise.
		_ = err
	}
	id, err := h.Client.Create(ctx, spec)
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
