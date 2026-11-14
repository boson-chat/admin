package authapi

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
)

// TestRolesAgainstAuth checks the roles API of a real auth service (>= 1.5.0)
// signed in as an Owner (AUTH_TEST_OWNER / AUTH_TEST_PASSWORD) and, when
// AUTH_TEST_ADMIN is an Admin with the same password, the hierarchy.
func TestRolesAgainstAuth(t *testing.T) {
	base, owner := os.Getenv("AUTH_TEST_URL"), os.Getenv("AUTH_TEST_OWNER")
	if base == "" || owner == "" {
		t.Skip("AUTH_TEST_URL / AUTH_TEST_OWNER not set")
	}
	c := NewAuthClient(base, 10*time.Second, nil)
	ctx := context.Background()
	pw := os.Getenv("AUTH_TEST_PASSWORD")
	tok, err := c.Login(ctx, owner, pw)
	if err != nil {
		t.Fatal(err)
	}
	at := tok.AccessToken
	sfx := fmt.Sprint(time.Now().UnixNano() % 1_000_000)

	me, err := c.Me(ctx, at)
	if err != nil || me.Roles == nil || me.Permissions == nil || !slices.Contains(*me.Permissions, "roles.owner") {
		t.Fatalf("me: %v %+v", err, me)
	}
	perms, err := c.ListPermissions(ctx, at)
	if err != nil || len(perms) < 30 || perms[0].ID != "admin.panel" {
		t.Fatalf("permissions: %v %d", err, len(perms))
	}
	roles, err := c.ListRoles(ctx, at)
	if err != nil || len(roles) < 4 || !roles[0].IsBuiltin(BuiltinOwner) || !roles[len(roles)-1].IsBuiltin(BuiltinMember) {
		t.Fatalf("roles: %v %+v", err, roles)
	}

	name, color, whois, vhost := "IT "+sfx, "#1abc9c", "runs the integration tests", "{username}.it.boson.chat"
	perm := []string{"users.view", "irc.samode"}
	r, err := c.CreateRole(ctx, at, RoleInput{Name: &name, Color: &color, Permissions: &perm, IRC: &RoleIRC{WhoisLine: &whois, Vhost: &vhost}})
	if err != nil || r.Slug != "it-"+sfx || !r.IRC.Oper || r.Position != 1 || r.IRC.Vhost == nil || *r.IRC.Vhost != vhost {
		t.Fatalf("create: %v %+v", err, r)
	}
	defer func() { _ = c.DeleteRole(ctx, at, r.ID) }()
	if _, err := c.CreateRole(ctx, at, RoleInput{Name: &name}); err == nil {
		t.Fatal("duplicate name accepted")
	} else if e, _ := apperr.As(err); e.Code != "role_name_taken" {
		t.Fatalf("role_name_taken: %v", err)
	}
	r, err = c.UpdateRole(ctx, at, r.ID, RoleInput{ClearColor: true, IRC: &RoleIRC{}})
	if err != nil || r.Color != nil || r.IRC.WhoisLine != nil || r.IRC.Vhost != nil {
		t.Fatalf("clear: %v %+v %+v", err, r, r.IRC)
	}
	if got, err := c.GetRole(ctx, at, r.ID); err != nil || got.ID != r.ID {
		t.Fatalf("get: %v %+v", err, got)
	}

	// Give the role to a fresh user, list members, reorder, take it away.
	upw := "integration pw " + sfx
	u, err := c.CreateUser(ctx, at, CreateUser{Username: "itr" + sfx, Role: "user", Password: &upw})
	if err != nil {
		t.Fatal(err)
	}
	refs, err := c.SetUserRoles(ctx, at, u.User.ID, []string{r.ID, roles[len(roles)-1].ID}) // Member is ignored
	if err != nil || len(refs) != 1 || refs[0].ID != r.ID {
		t.Fatalf("set roles: %v %+v", err, refs)
	}
	got, err := c.GetUser(ctx, at, u.User.ID)
	if err != nil || got.Roles == nil || len(*got.Roles) != 1 || !slices.Contains(*got.Permissions, "irc.samode") || !slices.Contains(*got.Permissions, "channel.create") {
		t.Fatalf("user: %v %+v", err, got)
	}
	mp, err := c.RoleMembers(ctx, at, r.ID, "", 10)
	if err != nil || len(mp.Users) != 1 || mp.Users[0].Username != u.User.Username || mp.Users[0].AssignedAt == nil {
		t.Fatalf("members: %v %+v", err, mp)
	}
	mod := ""
	for _, x := range roles {
		if x.IsBuiltin(BuiltinModerator) {
			mod = x.ID
		}
	}
	after, err := c.ReorderRoles(ctx, at, []string{r.ID, mod})
	if err != nil {
		t.Fatalf("reorder: %v", err)
	}
	ids := []string{}
	for _, x := range after {
		ids = append(ids, x.ID)
	}
	if slices.Index(ids, r.ID) > slices.Index(ids, mod) {
		t.Fatalf("reorder didn't put the role above Moderator: %v", ids)
	}
	_, _ = c.ReorderRoles(ctx, at, []string{mod, r.ID})

	if err := c.DeleteRole(ctx, at, roles[0].ID); err == nil {
		t.Fatal("deleted Owner")
	} else if e, _ := apperr.As(err); e.Code != "builtin_role" {
		t.Fatalf("builtin_role: %v", err)
	}

	// An Admin can't touch the Admin role or grant roles.owner.
	if admin := os.Getenv("AUTH_TEST_ADMIN"); admin != "" {
		atok, err := c.Login(ctx, admin, pw)
		if err != nil {
			t.Fatal(err)
		}
		n := "Boss"
		if _, err := c.UpdateRole(ctx, atok.AccessToken, roles[1].ID, RoleInput{Name: &n}); err == nil {
			t.Fatal("admin edited Admin")
		} else if e, _ := apperr.As(err); e.Code != "role_hierarchy" && e.Code != "invalid_request" {
			t.Fatalf("admin edit Admin: %v", err)
		}
		p := []string{"roles.owner"}
		if _, err := c.UpdateRole(ctx, atok.AccessToken, r.ID, RoleInput{Permissions: &p}); err == nil {
			t.Fatal("admin granted roles.owner")
		} else if e, _ := apperr.As(err); e.Code != "permission_not_held" {
			t.Fatalf("permission_not_held: %v", err)
		}
	}
	page, err := c.QueryAudit(ctx, at, AuditQuery{Event: "role_created,role_updated,user_roles_changed", Limit: 5})
	if err != nil || len(page.Entries) == 0 || !strings.HasPrefix(page.Entries[0].Event, "role_") && page.Entries[0].Event != "user_roles_changed" {
		t.Fatalf("audit: %v %+v", err, page)
	}
}
