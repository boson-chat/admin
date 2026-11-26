package handlers

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/boson-chat/admin/internal/services/admin"
	"github.com/boson-chat/admin/internal/services/updates"
)

type fakeUpdates struct{ applied []string }

func (f *fakeUpdates) Status(context.Context) *updates.Status { return &updates.Status{Available: 2} }
func (f *fakeUpdates) Check(context.Context) *updates.Status  { return &updates.Status{Available: 2} }
func (f *fakeUpdates) Preview(updates.ApplyRequest) (*updates.Preview, error) {
	return &updates.Preview{Summary: "Nobody is disconnected."}, nil
}
func (f *fakeUpdates) Apply(_ context.Context, actor string, _ updates.ApplyRequest, token string) (*updates.Run, error) {
	f.applied = append(f.applied, "apply:"+actor)
	return &updates.Run{ID: "r1", Actor: actor}, nil
}
func (f *fakeUpdates) Rollback(_ context.Context, actor string, _ updates.RollbackRequest, _ string) (*updates.Run, error) {
	f.applied = append(f.applied, "rollback:"+actor)
	return &updates.Run{ID: "r2", Actor: actor}, nil
}

// Seeing updates needs services.manage; applying and rolling back also
// settings.manage. The applying admin's name goes with the run.
func TestUpdatesPermissions(t *testing.T) {
	h := newHarness(t)
	fu := &fakeUpdates{}
	h.h.Updates = fu
	h.fake.Roles = append(h.fake.Roles,
		rolesFixture("rol_view", "Viewer", 300, admin.PermPanel, admin.PermServicesView),
		rolesFixture("rol_ops", "Ops", 310, admin.PermPanel, admin.PermServicesManage))
	h.fake.AddUser("usr_view", "viewer", "user", "viewer password")
	h.fake.SetRoles("usr_view", "rol_view")
	h.fake.AddUser("usr_ops", "ops", "user", "ops password")
	h.fake.SetRoles("usr_ops", "rol_ops")

	c, _ := h.loginAs("viewer", "viewer password")
	if w := h.do("GET", "/api/updates", nil, withCookie(c)); w.Code != 403 {
		t.Fatalf("services.view only: %d %s", w.Code, w.Body)
	}
	c, csrf := h.loginAs("ops", "ops password")
	opts := []reqOpt{withCookie(c), withHeader(CSRFHeader, csrf)}
	if w := h.do("GET", "/api/updates", nil, opts...); w.Code != 200 {
		t.Fatalf("services.manage sees updates: %d %s", w.Code, w.Body)
	}
	if w := h.do("POST", "/api/updates/plan", map[string]any{"mode": "all"}, opts...); w.Code != 200 {
		t.Fatalf("plan: %d %s", w.Code, w.Body)
	}
	for _, p := range []string{"/api/updates/apply", "/api/updates/rollback"} {
		w := h.do("POST", p, map[string]any{}, opts...)
		if w.Code != 403 || errCode(t, w.Body.String()) != "missing_permission" {
			t.Fatalf("%s without settings.manage: %d %s", p, w.Code, w.Body)
		}
	}
	// No CSRF token: refused before anything runs.
	if w := h.do("POST", "/api/updates/apply", map[string]any{}, withCookie(c)); w.Code != 403 {
		t.Fatalf("no csrf: %d", w.Code)
	}
	c, csrf = h.login()
	opts = []reqOpt{withCookie(c), withHeader(CSRFHeader, csrf)}
	var w *httptest.ResponseRecorder
	if w = h.do("POST", "/api/updates/apply", map[string]any{"mode": "all"}, opts...); w.Code != 202 {
		t.Fatalf("admin applies: %d %s", w.Code, w.Body)
	}
	if w = h.do("POST", "/api/updates/rollback", map[string]any{"run_id": "r1"}, opts...); w.Code != 202 {
		t.Fatalf("admin rolls back: %d %s", w.Code, w.Body)
	}
	if len(fu.applied) != 2 || fu.applied[0] != "apply:root" || fu.applied[1] != "rollback:root" {
		t.Fatalf("applied %v", fu.applied)
	}
}

func TestUpdatesOff(t *testing.T) {
	h := newHarness(t)
	c, _ := h.login()
	if w := h.do("GET", "/api/updates", nil, withCookie(c)); w.Code != 503 {
		t.Fatalf("off: %d %s", w.Code, w.Body)
	}
}
