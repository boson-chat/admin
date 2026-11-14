// Package containers shows and controls the stack's compose services
// through the Docker socket proxy: status, uptime, image, CPU and memory,
// start / stop / restart, and logs. Every action is checked against the
// compose project (the proxy can't filter by label), logged with the admin
// who asked for it, and counted.
package containers

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/dockerapi"
	"github.com/boson-chat/admin/internal/logger"
	"github.com/boson-chat/admin/metrics"
)

const (
	ActionStart   = "start"
	ActionStop    = "stop"
	ActionRestart = "restart"

	MaxTail     = 5000
	MaxDownload = 50000
)

// Service is one container of the compose project.
type Service struct {
	// Name is the container name (boson-auth-1); it is the key in URLs.
	Name string `json:"name"`
	// Service is the compose service (auth).
	Service      string     `json:"service"`
	Image        string     `json:"image"`
	Version      string     `json:"version,omitempty"`
	State        string     `json:"state"` // running, exited, restarting, paused, created, dead
	Status       string     `json:"status"`
	Health       string     `json:"health,omitempty"` // healthy, unhealthy, starting
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	ExitCode     int        `json:"exit_code"`
	OOMKilled    bool       `json:"oom_killed,omitempty"`
	RestartCount int        `json:"restart_count"`
	CPUPercent   *float64   `json:"cpu_percent,omitempty"`
	MemoryBytes  *uint64    `json:"memory_bytes,omitempty"`
	MemoryLimit  *uint64    `json:"memory_limit,omitempty"`
	// Protected services (the panel itself, the edge) need the service name
	// typed to stop or restart them.
	Protected bool `json:"protected"`
	// Self is this admin panel's own container.
	Self bool `json:"self"`
}

// List is GET /api/services.
type List struct {
	Configured bool      `json:"configured"`
	Project    string    `json:"project,omitempty"`
	Services   []Service `json:"services"`
	Error      string    `json:"error,omitempty"`
	CheckedAt  time.Time `json:"checked_at"`
}

// ActionResult is what an action returns.
type ActionResult struct {
	Service Service `json:"service"`
	// Deferred: the action runs just after the response (the panel's own
	// container), so the result isn't known yet.
	Deferred bool `json:"deferred,omitempty"`
}

type ContainersServiceImpl interface {
	List(ctx context.Context) *List
	Get(ctx context.Context, name string) (*Service, error)
	Action(ctx context.Context, actor, name, action, confirm string) (*ActionResult, error)
	Logs(ctx context.Context, name string, tail int, since time.Time) ([]dockerapi.LogLine, error)
}

type Options struct {
	Client  dockerapi.DockerClientImpl // nil = not configured
	Project string
	// Protected compose service names (default admin, traefik).
	Protected []string
	// SelfID is this container's ID or its prefix (the hostname in Docker).
	SelfID  string
	Metrics *metrics.Metrics
	// DeferSelf delays actions on the panel's own container so the
	// response gets out first.
	DeferSelf time.Duration
}

type ContainersService struct {
	o Options
}

func NewContainersService(o Options) ContainersServiceImpl {
	if o.Metrics == nil {
		o.Metrics = metrics.Noop()
	}
	if o.Project == "" {
		o.Project = "boson"
	}
	if o.Protected == nil {
		o.Protected = []string{"admin", "traefik"}
	}
	if o.DeferSelf == 0 {
		o.DeferSelf = 500 * time.Millisecond
	}
	return &ContainersService{o: o}
}

var ErrNotConfigured = apperr.New(http.StatusServiceUnavailable, "docker_not_configured",
	"services control is off: set DOCKER_PROXY_URL to a Docker socket proxy")

func (s *ContainersService) isSelf(id string) bool {
	return s.o.SelfID != "" && len(s.o.SelfID) >= 12 && strings.HasPrefix(id, s.o.SelfID)
}

func (s *ContainersService) view(c dockerapi.Container) Service {
	v := Service{
		Name: c.Name(), Service: c.Labels[dockerapi.LabelService], Image: c.Image,
		Version: c.Labels[dockerapi.LabelVersion], State: c.State, Status: c.Status,
		Self: s.isSelf(c.ID),
	}
	if v.Service == "" {
		v.Service = v.Name
	}
	v.Protected = v.Self || slices.Contains(s.o.Protected, v.Service)
	return v
}

func (s *ContainersService) enrich(ctx context.Context, id string, v *Service) {
	in, err := s.o.Client.Inspect(ctx, id)
	if err != nil {
		return
	}
	v.RestartCount, v.ExitCode, v.OOMKilled = in.RestartCount, in.State.ExitCode, in.State.OOMKilled
	if in.State.Health != nil {
		v.Health = in.State.Health.Status
	}
	if !in.State.StartedAt.IsZero() && in.State.StartedAt.Year() > 1 {
		t := in.State.StartedAt
		v.StartedAt = &t
	}
	if !in.State.Running && in.State.FinishedAt.Year() > 1 {
		t := in.State.FinishedAt
		v.FinishedAt = &t
	}
	if v.Version == "" {
		v.Version = in.Config.Labels[dockerapi.LabelVersion]
	}
	if v.Version == "" {
		if _, tag, ok := strings.Cut(lastSegment(in.Config.Image), ":"); ok {
			v.Version = tag
		}
	}
	if v.State != "running" {
		return
	}
	if st, err := s.o.Client.Stats(ctx, id); err == nil {
		v.CPUPercent, v.MemoryBytes = &st.CPUPercent, &st.MemoryBytes
		if st.MemoryLimit > 0 {
			v.MemoryLimit = &st.MemoryLimit
		}
	}
}

func lastSegment(image string) string {
	if i := strings.LastIndex(image, "/"); i >= 0 {
		return image[i+1:]
	}
	return image
}

func (s *ContainersService) List(ctx context.Context) *List {
	out := &List{Configured: s.o.Client != nil, Project: s.o.Project, Services: []Service{}, CheckedAt: time.Now().UTC()}
	if s.o.Client == nil {
		return out
	}
	cs, err := s.o.Client.List(ctx, s.o.Project)
	if err != nil {
		out.Error = errText(err)
		return out
	}
	out.Services = make([]Service, len(cs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, c := range cs {
		out.Services[i] = s.view(c)
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			s.enrich(ctx, c.ID, &out.Services[i])
		})
	}
	wg.Wait()
	sort.Slice(out.Services, func(i, j int) bool { return out.Services[i].Name < out.Services[j].Name })
	return out
}

func errText(err error) string {
	if e, ok := apperr.As(err); ok {
		return e.Message
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "the Docker socket proxy took too long to answer"
	}
	return "the Docker socket proxy failed"
}

// find resolves a container name inside the project. Anything outside it
// is "not found", whatever the proxy would allow.
func (s *ContainersService) find(ctx context.Context, name string) (dockerapi.Container, error) {
	if s.o.Client == nil {
		return dockerapi.Container{}, ErrNotConfigured
	}
	cs, err := s.o.Client.List(ctx, s.o.Project)
	if err != nil {
		return dockerapi.Container{}, err
	}
	for _, c := range cs {
		if c.Name() == name {
			return c, nil
		}
	}
	return dockerapi.Container{}, apperr.New(http.StatusNotFound, "not_found", "no container "+name+" in the "+s.o.Project+" project")
}

func (s *ContainersService) Get(ctx context.Context, name string) (*Service, error) {
	c, err := s.find(ctx, name)
	if err != nil {
		return nil, err
	}
	v := s.view(c)
	s.enrich(ctx, c.ID, &v)
	return &v, nil
}

var errConfirm = apperr.New(http.StatusPreconditionRequired, "confirmation_required",
	"type the service name to confirm; this is the admin panel or the edge proxy")

func (s *ContainersService) Action(ctx context.Context, actor, name, action, confirm string) (*ActionResult, error) {
	if action != ActionStart && action != ActionStop && action != ActionRestart {
		return nil, apperr.Invalid("action must be start, stop or restart")
	}
	log := logger.FromCtx(ctx).With().Str("actor", actor).Str("action", action).Str("container", name).Logger()
	c, err := s.find(ctx, name)
	if err != nil {
		s.o.Metrics.ServiceAction(ctx, action, "error")
		return nil, err
	}
	v := s.view(c)
	if v.Protected && action != ActionStart && strings.TrimSpace(confirm) != v.Service {
		s.o.Metrics.ServiceAction(ctx, action, "refused")
		log.Warn().Str("service", v.Service).Msg("service action refused: typed confirmation missing")
		return nil, errConfirm
	}
	do := func(ctx context.Context) error {
		switch action {
		case ActionStart:
			return s.o.Client.Start(ctx, c.ID)
		case ActionStop:
			return s.o.Client.Stop(ctx, c.ID)
		default:
			return s.o.Client.Restart(ctx, c.ID)
		}
	}
	if v.Self && action != ActionStart {
		log.Warn().Str("service", v.Service).Msg("service action on the admin panel itself; running after the response")
		s.o.Metrics.ServiceAction(ctx, action, "success")
		bg := context.WithoutCancel(ctx)
		time.AfterFunc(s.o.DeferSelf, func() {
			actx, cancel := context.WithTimeout(bg, 30*time.Second)
			defer cancel()
			if err := do(actx); err != nil {
				log.Error().Err(err).Msg("deferred service action failed")
			}
		})
		return &ActionResult{Service: v, Deferred: true}, nil
	}
	// stop/restart wait up to 10 s for the process to exit.
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := do(actx); err != nil {
		s.o.Metrics.ServiceAction(ctx, action, "error")
		log.Error().Err(err).Str("service", v.Service).Msg("service action failed")
		return nil, err
	}
	s.o.Metrics.ServiceAction(ctx, action, "success")
	log.Info().Str("service", v.Service).Msg("service action")
	nv := s.view(c)
	if fresh, err := s.Get(actx, name); err == nil {
		nv = *fresh
	}
	return &ActionResult{Service: nv}, nil
}

func (s *ContainersService) Logs(ctx context.Context, name string, tail int, since time.Time) ([]dockerapi.LogLine, error) {
	c, err := s.find(ctx, name)
	if err != nil {
		return nil, err
	}
	in, err := s.o.Client.Inspect(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	lines, err := s.o.Client.Logs(ctx, c.ID, dockerapi.LogOptions{Tail: tail, Since: since, Tty: in.Config.Tty})
	if err != nil {
		return nil, err
	}
	if lines == nil {
		lines = []dockerapi.LogLine{}
	}
	return lines, nil
}
