package handlers

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/clients/bosonapi"
	"github.com/boson-chat/admin/internal/clients/dockerapi"
	"github.com/boson-chat/admin/internal/services/containers"
)

func TestSettingsAndSSOEndpoints(t *testing.T) {
	h := newHarness(t)
	c, csrf := h.login()
	w := h.do("PATCH", "/api/settings", map[string]any{"voice_channel_creation": "admins"}, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"voice_channel_creation":"admins"`) || !strings.Contains(w.Body.String(), `"updated_by_username":"root"`) {
		t.Fatalf("patch settings: %d %s", w.Code, w.Body)
	}
	if w := h.do("PATCH", "/api/settings", map[string]any{"registration": "x"}, withCookie(c)); w.Code != 403 {
		t.Fatalf("no csrf: %d", w.Code)
	}
	w = h.do("POST", "/api/sso/providers", map[string]any{"slug": "kc", "kind": "oidc", "display_name": "Keycloak",
		"config": map[string]any{"oidc": map[string]any{"issuer": "https://sso.example.com/realms/boson", "client_id": "boson", "client_secret": "s"}}},
		withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != 201 || strings.Contains(w.Body.String(), `"client_secret"`) || !strings.Contains(w.Body.String(), `"has_client_secret":true`) {
		t.Fatalf("create provider: %d %s", w.Code, w.Body)
	}
	w = h.do("POST", "/api/sso/providers/idp_1/test", nil, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("test provider: %d %s", w.Code, w.Body)
	}
	h.fake.Legacy = true
	w = h.do("GET", "/api/settings", nil, withCookie(c))
	if w.Code != 501 || !strings.Contains(w.Body.String(), "upstream_unsupported") || !strings.Contains(w.Body.String(), "auth ≥") {
		t.Fatalf("legacy settings: %d %s", w.Code, w.Body)
	}
}

func TestUserEndpoints(t *testing.T) {
	h := newHarness(t)
	c, csrf := h.login()
	w := h.do("POST", "/api/users", map[string]any{"username": "ana", "email": "ana@example.com", "send_invite_email": true}, withCookie(c), withHeader(CSRFHeader, csrf))
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"invite_email_sent":true`) {
		t.Fatalf("create user: %d %s", w.Code, w.Body)
	}
	if w := h.do("POST", "/api/users/usr_bob/password-reset", nil, withCookie(c), withHeader(CSRFHeader, csrf)); w.Code != 204 {
		t.Fatalf("reset: %d %s", w.Code, w.Body)
	}
	if w := h.do("GET", "/api/users/usr_bob/profile", nil, withCookie(c)); w.Code != 200 || !strings.Contains(w.Body.String(), `"configured":false`) {
		t.Fatalf("profile off: %d %s", w.Code, w.Body)
	}
	h.h.Profiles = fakeProfiles{}
	w = h.do("GET", "/api/users/usr_bob/profile", nil, withCookie(c))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"handle":"bob"`) || !strings.Contains(w.Body.String(), `"display_name":"token:at-`) {
		t.Fatalf("profile: %d %s", w.Code, w.Body)
	}
}

type fakeProfiles struct{}

func (fakeProfiles) AdminProfile(_ context.Context, at, id string) (*bosonapi.AdminProfile, error) {
	// echo the token so the test can see the admin's token was used
	return &bosonapi.AdminProfile{Profile: &bosonapi.Profile{AuthUserID: id, Handle: "bob", DisplayName: "token:" + at}, Servers: []bosonapi.Server{}}, nil
}

type fakeDocker struct{ actions []string }

func (f *fakeDocker) List(context.Context, string) ([]dockerapi.Container, error) {
	return []dockerapi.Container{
		{ID: "a1", Names: []string{"/boson-auth-1"}, State: "running", Labels: map[string]string{dockerapi.LabelProject: "boson", dockerapi.LabelService: "auth"}},
		{ID: "t1", Names: []string{"/boson-traefik-1"}, State: "running", Labels: map[string]string{dockerapi.LabelProject: "boson", dockerapi.LabelService: "traefik"}},
	}, nil
}
func (f *fakeDocker) Inspect(_ context.Context, id string) (*dockerapi.Inspect, error) {
	return &dockerapi.Inspect{ID: id}, nil
}
func (f *fakeDocker) Start(_ context.Context, id string) error {
	f.actions = append(f.actions, "start:"+id)
	return nil
}
func (f *fakeDocker) Stop(_ context.Context, id string) error {
	f.actions = append(f.actions, "stop:"+id)
	return nil
}
func (f *fakeDocker) Restart(_ context.Context, id string) error {
	f.actions = append(f.actions, "restart:"+id)
	return nil
}
func (f *fakeDocker) Logs(context.Context, string, dockerapi.LogOptions) ([]dockerapi.LogLine, error) {
	return []dockerapi.LogLine{{At: time.Date(2026, 11, 13, 10, 0, 0, 5, time.UTC), Stream: "stderr", Text: "boom"}}, nil
}
func (f *fakeDocker) Stats(context.Context, string) (*dockerapi.Stats, error) {
	return &dockerapi.Stats{}, nil
}

func TestServicesEndpoints(t *testing.T) {
	h := newHarness(t)
	c, csrf := h.login()
	if w := h.do("GET", "/api/services", nil, withCookie(c)); w.Code != 200 || !strings.Contains(w.Body.String(), `"configured":false`) {
		t.Fatalf("off: %d %s", w.Code, w.Body)
	}
	if w := h.do("POST", "/api/services/boson-auth-1/restart", map[string]string{}, withCookie(c), withHeader(CSRFHeader, csrf)); w.Code != 503 {
		t.Fatalf("action while off: %d", w.Code)
	}
	fd := &fakeDocker{}
	h.h.Containers = containers.NewContainersService(containers.Options{Client: fd})
	w := h.do("GET", "/api/services", nil, withCookie(c))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"boson-traefik-1"`) || !strings.Contains(w.Body.String(), `"protected":true`) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if w := h.do("POST", "/api/services/boson-auth-1/restart", map[string]string{}, withCookie(c)); w.Code != 403 {
		t.Fatalf("no csrf: %d", w.Code)
	}
	if w := h.do("POST", "/api/services/boson-auth-1/restart", map[string]string{}, withCookie(c), withHeader(CSRFHeader, csrf)); w.Code != 200 {
		t.Fatalf("restart: %d %s", w.Code, w.Body)
	}
	if w := h.do("POST", "/api/services/boson-traefik-1/stop", map[string]string{}, withCookie(c), withHeader(CSRFHeader, csrf)); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("traefik without confirm: %d %s", w.Code, w.Body)
	}
	if w := h.do("POST", "/api/services/boson-traefik-1/stop", map[string]string{"confirm": "traefik"}, withCookie(c), withHeader(CSRFHeader, csrf)); w.Code != 200 {
		t.Fatalf("traefik confirmed: %d %s", w.Code, w.Body)
	}
	if strings.Join(fd.actions, ",") != "restart:a1,stop:t1" {
		t.Fatalf("actions %v", fd.actions)
	}
	w = h.do("GET", "/api/services/boson-auth-1/logs?tail=10", nil, withCookie(c))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"last_at":"2026-11-13T10:00:00.000000005Z"`) {
		t.Fatalf("logs: %d %s", w.Code, w.Body)
	}
	w = h.do("GET", "/api/services/boson-auth-1/logs?download=1", nil, withCookie(c))
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Disposition"), `attachment; filename="boson-auth-1-`) || w.Body.String() != "2026-11-13T10:00:00.000000005Z stderr boom\n" {
		t.Fatalf("download: %d %q %q", w.Code, w.Header().Get("Content-Disposition"), w.Body)
	}
	// A demoted admin's cookie no longer drives Docker.
	h.fake.Users["usr_admin"].Role = "user"
	h.fake.SetRoles("usr_admin")
	if w := h.do("POST", "/api/services/boson-auth-1/restart", map[string]string{}, withCookie(c), withHeader(CSRFHeader, csrf)); w.Code != 403 {
		t.Fatalf("demoted admin: %d %s", w.Code, w.Body)
	}
	if len(fd.actions) != 2 {
		t.Fatalf("demoted admin restarted something: %v", fd.actions)
	}
}

func TestDirectoryOff(t *testing.T) {
	h := newHarness(t)
	c, _ := h.login()
	if w := h.do("GET", "/api/directory", nil, withCookie(c)); w.Code != 503 || !strings.Contains(w.Body.String(), "directory_off") {
		t.Fatalf("directory off: %d %s", w.Code, w.Body)
	}
}
