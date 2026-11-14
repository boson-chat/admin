package containers

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/dockerapi"
)

type fakeDocker struct {
	mu      sync.Mutex
	cs      []dockerapi.Container
	actions []string
}

func (f *fakeDocker) List(_ context.Context, project string) ([]dockerapi.Container, error) {
	var out []dockerapi.Container
	for _, c := range f.cs {
		if c.Labels[dockerapi.LabelProject] == project {
			out = append(out, c)
		}
	}
	return out, nil
}
func (f *fakeDocker) Inspect(_ context.Context, id string) (*dockerapi.Inspect, error) {
	in := &dockerapi.Inspect{ID: id, RestartCount: 2}
	in.State.Running = true
	in.State.StartedAt = time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	in.Config.Image = "harbor.floret.dev/boson/auth:1.2.1"
	return in, nil
}
func (f *fakeDocker) act(a, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, a+":"+id)
	return nil
}
func (f *fakeDocker) Start(_ context.Context, id string) error   { return f.act("start", id) }
func (f *fakeDocker) Stop(_ context.Context, id string) error    { return f.act("stop", id) }
func (f *fakeDocker) Restart(_ context.Context, id string) error { return f.act("restart", id) }
func (f *fakeDocker) Logs(_ context.Context, id string, o dockerapi.LogOptions) ([]dockerapi.LogLine, error) {
	return []dockerapi.LogLine{{Text: "hi " + id}}, nil
}
func (f *fakeDocker) Stats(context.Context, string) (*dockerapi.Stats, error) {
	return &dockerapi.Stats{CPUPercent: 1.5, MemoryBytes: 10 << 20}, nil
}

func (f *fakeDocker) done() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.actions...)
}

func ct(id, name, service, project, state string) dockerapi.Container {
	return dockerapi.Container{ID: id, Names: []string{"/" + name}, State: state,
		Labels: map[string]string{dockerapi.LabelProject: project, dockerapi.LabelService: service}}
}

func setup() (*fakeDocker, ContainersServiceImpl) {
	f := &fakeDocker{cs: []dockerapi.Container{
		ct("aaaaaaaaaaaa1111", "boson-auth-1", "auth", "boson", "running"),
		ct("bbbbbbbbbbbb2222", "boson-admin-1", "admin", "boson", "running"),
		ct("cccccccccccc3333", "boson-traefik-1", "traefik", "boson", "running"),
		ct("dddddddddddd4444", "evil-1", "auth", "other", "running"),
	}}
	return f, NewContainersService(Options{Client: f, Project: "boson", SelfID: "bbbbbbbbbbbb", DeferSelf: time.Millisecond})
}

func TestList(t *testing.T) {
	_, svc := setup()
	l := svc.List(context.Background())
	if !l.Configured || len(l.Services) != 3 {
		t.Fatalf("list %+v", l)
	}
	admin := l.Services[0]
	if admin.Name != "boson-admin-1" || !admin.Self || !admin.Protected {
		t.Fatalf("admin %+v", admin)
	}
	auth := l.Services[1]
	if auth.Protected || auth.Version != "1.2.1" || auth.CPUPercent == nil || auth.RestartCount != 2 || auth.StartedAt == nil {
		t.Fatalf("auth %+v", auth)
	}
	if !l.Services[2].Protected {
		t.Fatal("traefik must be protected")
	}
	if off := NewContainersService(Options{}).List(context.Background()); off.Configured {
		t.Fatal("no client = not configured")
	}
}

func TestActions(t *testing.T) {
	f, svc := setup()
	ctx := context.Background()
	if _, err := svc.Action(ctx, "root", "boson-auth-1", "restart", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Action(ctx, "root", "evil-1", "stop", "auth"); err == nil {
		t.Fatal("a container outside the project was stopped")
	}
	if _, err := svc.Action(ctx, "root", "boson-auth-1", "remove", ""); err == nil {
		t.Fatal("unknown action accepted")
	}
	_, err := svc.Action(ctx, "root", "boson-traefik-1", "stop", "")
	if e, ok := apperr.As(err); !ok || e.Code != "confirmation_required" {
		t.Fatalf("traefik without confirmation: %v", err)
	}
	if _, err := svc.Action(ctx, "root", "boson-traefik-1", "stop", "boson-traefik-1"); err == nil {
		t.Fatal("confirmation must be the service name")
	}
	if _, err := svc.Action(ctx, "root", "boson-traefik-1", "stop", "traefik"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Action(ctx, "root", "boson-traefik-1", "start", ""); err != nil {
		t.Fatal("start needs no typed confirmation")
	}
	res, err := svc.Action(ctx, "root", "boson-admin-1", "restart", "admin")
	if err != nil || !res.Deferred {
		t.Fatalf("self restart: %v %+v", err, res)
	}
	time.Sleep(50 * time.Millisecond)
	want := []string{"restart:aaaaaaaaaaaa1111", "stop:cccccccccccc3333", "start:cccccccccccc3333", "restart:bbbbbbbbbbbb2222"}
	if got := f.done(); len(got) != len(want) || got[3] != want[3] || got[0] != want[0] {
		t.Fatalf("actions %v", got)
	}
	lines, err := svc.Logs(ctx, "boson-auth-1", 10, time.Time{})
	if err != nil || len(lines) != 1 {
		t.Fatalf("logs %v %v", err, lines)
	}
	if _, err := svc.Logs(ctx, "evil-1", 10, time.Time{}); err == nil {
		t.Fatal("logs outside the project")
	}
}
