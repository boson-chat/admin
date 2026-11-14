package handlers

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/clients/authapi/authapitest"
	"github.com/boson-chat/admin/internal/services/admin"
)

// loginAs signs in as username/password and returns the cookie and CSRF token.
func (h *harness) loginAs(username, password string) (*http.Cookie, string) {
	h.t.Helper()
	w := h.do("POST", "/api/login", map[string]string{"login": username, "password": password}, withHeader(LoginHeader, "x"))
	if w.Code != 200 {
		h.t.Fatalf("login %s: %d %s", username, w.Code, w.Body)
	}
	var v struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	return sessionCookie(w), v.CSRFToken
}

func (h *harness) moderator() (*http.Cookie, string) {
	h.fake.AddUser("usr_mod", "mod", "user", "mod password")
	h.fake.SetRoles("usr_mod", authapitest.RoleModerator)
	return h.loginAs("mod", "mod password")
}

func errCode(t *testing.T, body string) string {
	t.Helper()
	var e struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal([]byte(body), &e)
	return e.Error
}

// Every gated route answers 403 missing_permission without its permission
// and gets past the gate with it.
func TestGatingTable(t *testing.T) {
	h := newHarness(t)
	c, csrf := h.moderator()
	modPerms := []string{admin.PermPanel, admin.PermUsersView, admin.PermUsersModerate}
	path := strings.NewReplacer("{id}", "usr_bob", "{name}", "boson-auth-1", "{action}", "restart", "{user}", "usr_bob")
	routes := h.h.routes()
	if len(routes) < 40 {
		t.Fatalf("only %d routes", len(routes))
	}
	for _, rt := range routes {
		method, p, _ := strings.Cut(rt.Pattern, " ")
		p = path.Replace(p)
		var body any
		if method != "GET" && method != "DELETE" {
			body = map[string]any{}
		}
		w := h.do(method, p, body, withCookie(c), withHeader(CSRFHeader, csrf))
		code := errCode(t, w.Body.String())
		allowed := rt.Perm == "" || slices.Contains(modPerms, rt.Perm)
		switch {
		case !allowed && (w.Code != 403 || code != "missing_permission"):
			t.Errorf("%s: want 403 missing_permission, got %d %s", rt.Pattern, w.Code, w.Body)
		case allowed && code == "missing_permission":
			t.Errorf("%s: moderator refused: %s", rt.Pattern, w.Body)
		case !allowed && !strings.Contains(w.Body.String(), rt.Perm):
			t.Errorf("%s: message doesn't name %s: %s", rt.Pattern, rt.Perm, w.Body)
		}
		if sc := sessionCookie(w); sc != nil && sc.MaxAge == -1 {
			t.Fatalf("%s: a missing permission ended the session", rt.Pattern)
		}
	}
	if len(h.fake.Emails) != 0 {
		t.Fatalf("a refused route did something: %v", h.fake.Emails)
	}
}

func TestSessionCarriesPermissions(t *testing.T) {
	h := newHarness(t)
	c, _ := h.moderator()
	w := h.do("GET", "/api/session", nil, withCookie(c))
	var v sessionView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != 200 {
		t.Fatalf("session: %d %s", w.Code, w.Body)
	}
	if !v.RolesSupported || !slices.Contains(v.Permissions, "users.view") || slices.Contains(v.Permissions, "users.manage") {
		t.Fatalf("permissions: %+v", v)
	}
	if len(v.Roles) != 1 || v.Roles[0].Name != "Moderator" {
		t.Fatalf("roles: %+v", v.Roles)
	}
}

func TestLoginNeedsAdminPanel(t *testing.T) {
	h := newHarness(t)
	// A custom role without admin.panel doesn't open the panel…
	h.fake.Roles = append(h.fake.Roles, rolesFixture("rol_dj", "DJ", 20, "voice.manage_any"))
	h.fake.AddUser("usr_dj", "dj", "user", "dj password")
	h.fake.SetRoles("usr_dj", "rol_dj")
	w := h.do("POST", "/api/login", map[string]string{"login": "dj", "password": "dj password"}, withHeader(LoginHeader, "x"))
	if w.Code != 403 || errCode(t, w.Body.String()) != "not_admin" || sessionCookie(w) != nil {
		t.Fatalf("no admin.panel: %d %s", w.Code, w.Body)
	}
	if h.fake.LogoutCalls.Load() != 1 {
		t.Fatal("the refused session was left behind")
	}
	// …admin.panel alone does, and every page but the panel itself is closed.
	h.fake.Roles = append(h.fake.Roles, rolesFixture("rol_helper", "Helper", 30, "admin.panel"))
	h.fake.SetRoles("usr_dj", "rol_helper")
	c, _ := h.loginAs("dj", "dj password")
	if w := h.do("GET", "/api/users", nil, withCookie(c)); w.Code != 403 {
		t.Fatalf("users with admin.panel only: %d", w.Code)
	}
	if w := h.do("GET", "/api/permissions", nil, withCookie(c)); w.Code != 200 || !strings.Contains(w.Body.String(), `"source":"auth"`) {
		t.Fatalf("permissions: %d %s", w.Code, w.Body)
	}
	// Losing admin.panel mid-session signs out on the next fresh check.
	h.fake.SetRoles("usr_dj")
	w = h.do("GET", "/api/session", nil, withCookie(c))
	if w.Code != 403 || errCode(t, w.Body.String()) != "not_admin" {
		t.Fatalf("session after losing admin.panel: %d %s", w.Code, w.Body)
	}
	if sc := sessionCookie(w); sc == nil || sc.MaxAge != -1 {
		t.Fatal("cookie kept after losing admin.panel")
	}
}

// Auth < 1.5.0: role=admin is every permission; roles say what they need.
func TestLegacyAuthFallback(t *testing.T) {
	h := newHarness(t)
	h.fake.NoRoles = true
	c, _ := h.login()
	w := h.do("GET", "/api/session", nil, withCookie(c))
	var v sessionView
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	if v.RolesSupported || len(v.Permissions) != len(admin.Catalog) {
		t.Fatalf("legacy session: %d %+v", w.Code, v)
	}
	for _, p := range []string{"/api/users", "/api/settings", "/api/audit", "/api/invites", "/api/services"} {
		if w := h.do("GET", p, nil, withCookie(c)); w.Code != 200 {
			t.Errorf("legacy admin %s: %d %s", p, w.Code, w.Body)
		}
	}
	w = h.do("GET", "/api/roles", nil, withCookie(c))
	if w.Code != 501 || !strings.Contains(w.Body.String(), "auth ≥ v1.5.0") {
		t.Fatalf("legacy roles: %d %s", w.Code, w.Body)
	}
	w = h.do("GET", "/api/permissions", nil, withCookie(c))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"source":"panel"`) || !strings.Contains(w.Body.String(), `"requires":"v1.5.0"`) {
		t.Fatalf("legacy permissions: %d %s", w.Code, w.Body)
	}
	w = h.do("POST", "/api/login", map[string]string{"login": "bob", "password": "bob password"}, withHeader(LoginHeader, "x"))
	if w.Code != 403 || errCode(t, w.Body.String()) != "not_admin" {
		t.Fatalf("legacy non-admin: %d %s", w.Code, w.Body)
	}
}

func TestRoleEndpoints(t *testing.T) {
	h := newHarness(t)
	c, csrf := h.login()
	auth := []reqOpt{withCookie(c), withHeader(CSRFHeader, csrf)}

	w := h.do("GET", "/api/roles", nil, auth...)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"builtin":"owner"`) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if w := h.do("POST", "/api/roles", map[string]any{"name": "DJ", "color": "orange"}, auth...); w.Code != 400 {
		t.Fatalf("bad colour: %d %s", w.Code, w.Body)
	}
	if w := h.do("POST", "/api/roles", map[string]any{"name": "DJ", "irc": map[string]any{"vhost": "bad host!"}}, auth...); w.Code != 400 {
		t.Fatalf("bad vhost: %d %s", w.Code, w.Body)
	}
	w = h.do("POST", "/api/roles", map[string]any{"name": "  DJ   crew ", "color": "#AA00FF", "permissions": []string{"irc.kill", "irc.kill"},
		"irc": map[string]any{"whois_line": "is on the decks", "vhost": "{username}.dj.boson.chat", "oper": false}}, auth...)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"name":"DJ crew"`) || !strings.Contains(w.Body.String(), `"color":"#aa00ff"`) ||
		!strings.Contains(w.Body.String(), `"permissions":["irc.kill"]`) {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var created struct {
		Role struct {
			ID string `json:"id"`
		} `json:"role"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	id := created.Role.ID

	w = h.do("PATCH", "/api/roles/"+id, map[string]any{"color": nil, "hoist": true}, auth...)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"color":null`) || !strings.Contains(w.Body.String(), `"hoist":true`) {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	// Admins can't grant what they lack (the Admin role has no roles.owner).
	w = h.do("PATCH", "/api/roles/"+id, map[string]any{"permissions": []string{"roles.owner"}}, auth...)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "permission you don't have") {
		t.Fatalf("grant roles.owner: %d %s", w.Code, w.Body)
	}
	// …nor edit roles at or above their own.
	w = h.do("PATCH", "/api/roles/"+authapitest.RoleOwner, map[string]any{"name": "Boss"}, auth...)
	if w.Code != 403 || errCode(t, w.Body.String()) != "role_hierarchy" || sessionCookie(w) != nil && sessionCookie(w).MaxAge == -1 {
		t.Fatalf("edit owner: %d %s", w.Code, w.Body)
	}

	if w := h.do("PUT", "/api/roles/"+id+"/members/usr_bob", nil, auth...); w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"DJ crew"`) {
		t.Fatalf("add member: %d %s", w.Code, w.Body)
	}
	w = h.do("GET", "/api/roles/"+id+"/members", nil, auth...)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"username":"bob"`) {
		t.Fatalf("members: %d %s", w.Code, w.Body)
	}
	w = h.do("GET", "/api/users/usr_bob", nil, auth...)
	if !strings.Contains(w.Body.String(), `"permissions":["channel.create","irc.kill","voice.create"]`) {
		t.Fatalf("bob's effective permissions: %s", w.Body)
	}
	if w := h.do("DELETE", "/api/roles/"+id+"/members/usr_bob", nil, auth...); w.Code != 200 || w.Body.String() != "{\"roles\":[]}\n" {
		t.Fatalf("remove member: %d %s", w.Code, w.Body)
	}
	if w := h.do("PUT", "/api/users/usr_bob/roles", map[string]any{}, auth...); w.Code != 400 {
		t.Fatalf("roles without role_ids: %d", w.Code)
	}
	if w := h.do("PUT", "/api/users/usr_bob/roles", map[string]any{"role_ids": []string{authapitest.RoleOwner}}, auth...); w.Code != 403 {
		t.Fatalf("assign owner as admin: %d %s", w.Code, w.Body)
	}
	if w := h.do("PUT", "/api/users/usr_bob/roles", map[string]any{"role_ids": []string{id, authapitest.RoleModerator}}, auth...); w.Code != 200 ||
		!strings.Contains(w.Body.String(), `"Moderator"`) {
		t.Fatalf("set roles: %d %s", w.Code, w.Body)
	}

	order := []string{authapitest.RoleOwner, authapitest.RoleAdmin, id, authapitest.RoleModerator, authapitest.RoleMember}
	w = h.do("POST", "/api/roles/reorder", map[string]any{"order": order}, auth...)
	if w.Code != 200 || strings.Join(h.fake.LastOrder, ",") != strings.Join(order, ",") || !strings.Contains(w.Body.String(), `"roles":[{"id":"rol_owner"`) {
		t.Fatalf("reorder: %d %s %v", w.Code, w.Body, h.fake.LastOrder)
	}
	if w := h.do("POST", "/api/roles/reorder", map[string]any{"order": []string{id, id}}, auth...); w.Code != 400 {
		t.Fatalf("duplicate order: %d", w.Code)
	}

	w = h.do("DELETE", "/api/roles/"+authapitest.RoleModerator, nil, auth...)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "Built-in roles can't be deleted") {
		t.Fatalf("delete builtin: %d %s", w.Code, w.Body)
	}
	if w := h.do("DELETE", "/api/roles/"+id, nil, auth...); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
}

// A role change by the signed-in admin applies to their own next request,
// not after the permission cache runs out.
func TestOwnRoleChangeDropsCache(t *testing.T) {
	h := newHarness(t)
	h.fake.AddUser("usr_owner", "boss", "user", "boss password")
	h.fake.SetRoles("usr_owner", authapitest.RoleOwner)
	c, csrf := h.loginAs("boss", "boss password")
	if w := h.do("GET", "/api/audit", nil, withCookie(c)); w.Code != 200 {
		t.Fatalf("owner audit: %d", w.Code)
	}
	if w := h.do("PUT", "/api/users/usr_owner/roles", map[string]any{"role_ids": []string{authapitest.RoleModerator}},
		withCookie(c), withHeader(CSRFHeader, csrf)); w.Code != 200 {
		t.Fatalf("self-demote: %d %s", w.Code, w.Body)
	}
	if w := h.do("GET", "/api/audit", nil, withCookie(c)); w.Code != 403 {
		t.Fatalf("audit after self-demotion: %d", w.Code)
	}
}

func rolesFixture(id, name string, pos int, perms ...string) authapi.Role {
	return authapi.Role{ID: id, Name: name, Position: pos, Permissions: perms}
}
