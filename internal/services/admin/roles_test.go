package admin

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/clients/authapi/authapitest"
)

func TestAccessCacheAndTTL(t *testing.T) {
	f, svc, now := setup(t)
	s := login(t, svc)
	ctx := context.Background()
	calls := f.MeCalls.Load() // login reads /v1/me once
	for range 3 {
		a, err := svc.Access(ctx, s, false)
		if err != nil || !a.Can(PermUsersView, PermRolesManage) || a.Can(PermRolesOwner) || a.Legacy {
			t.Fatalf("access: %v %+v", err, a)
		}
	}
	if n := f.MeCalls.Load() - calls; n != 0 {
		t.Fatalf("cached access asked /v1/me %d times", n)
	}
	// fresh always asks
	if _, err := svc.Access(ctx, s, true); err != nil || f.MeCalls.Load()-calls != 1 {
		t.Fatalf("fresh: %v", err)
	}
	// a permission lost upstream shows once the cache runs out
	f.Roles[1].Permissions = []string{PermPanel}
	if a, _ := svc.Access(ctx, s, false); !a.Can(PermUsersView) {
		t.Fatal("cache dropped early")
	}
	*now = now.Add(AccessTTL)
	if a, _ := svc.Access(ctx, s, false); a.Can(PermUsersView) {
		t.Fatal("stale permissions after the TTL")
	}
	// losing admin.panel is ErrNotAdmin
	f.SetRoles("usr_admin")
	if _, err := svc.Access(ctx, s, true); !errors.Is(err, apperr.ErrNotAdmin) {
		t.Fatalf("no admin.panel: %v", err)
	}
}

func TestAccessFor(t *testing.T) {
	perms := []string{PermPanel}
	roles := []authapi.RoleRef{}
	cases := []struct {
		name   string
		u      authapi.User
		legacy bool
		panel  bool
		n      int
	}{
		{"legacy admin", authapi.User{Role: RoleAdmin}, true, true, len(Catalog)},
		{"legacy user", authapi.User{Role: RoleUser}, true, false, 0},
		{"roles: permissions decide, not role", authapi.User{Role: RoleAdmin, Roles: &roles, Permissions: &[]string{}}, false, false, 0},
		{"roles: admin.panel", authapi.User{Role: RoleUser, Roles: &roles, Permissions: &perms}, false, true, 1},
	}
	for _, c := range cases {
		a := accessFor(&c.u)
		if a.Legacy != c.legacy || a.Can(PermPanel) != c.panel || len(a.Permissions) != c.n {
			t.Errorf("%s: %+v", c.name, a)
		}
	}
}

func TestValidateRole(t *testing.T) {
	str := func(s string) *string { return &s }
	bad := []authapi.RoleInput{
		{},                 // create without a name
		{Name: str("   ")}, // blank
		{Name: str(strings.Repeat("x", 33))},
		{Name: str("x"), Color: str("red")},
		{Name: str("x"), Color: str("#12345")},
		{Name: str("x"), Permissions: &[]string{"Users View"}},
		{Name: str("x"), IRC: &authapi.RoleIRC{WhoisLine: "two\nlines"}},
		{Name: str("x"), IRC: &authapi.RoleIRC{WhoisLine: strings.Repeat("w", 121)}},
		{Name: str("x"), IRC: &authapi.RoleIRC{Vhost: "nodots"}},
		{Name: str("x"), IRC: &authapi.RoleIRC{Vhost: "{user}.staff.example.com"}},
		{Name: str("x"), IRC: &authapi.RoleIRC{Vhost: "a..b"}},
		{Name: str("x"), IRC: &authapi.RoleIRC{Vhost: "has space.example.com"}},
	}
	for i, in := range bad {
		if err := validateRole(&in, true); err == nil {
			t.Errorf("case %d accepted: %+v", i, in)
		}
	}
	in := authapi.RoleInput{Name: str(" Night  shift "), Color: str(" #AABBCC "), Permissions: &[]string{"users.view", "admin.panel", "users.view"},
		IRC: &authapi.RoleIRC{WhoisLine: " is on nights ", Vhost: "{USERNAME}.Night.Example.com"}}
	if err := validateRole(&in, true); err != nil {
		t.Fatal(err)
	}
	if *in.Name != "Night shift" || *in.Color != "#aabbcc" || strings.Join(*in.Permissions, ",") != "admin.panel,users.view" ||
		in.IRC.WhoisLine != "is on nights" || in.IRC.Vhost != "{username}.night.example.com" {
		t.Fatalf("normalised: %+v %+v", in, in.IRC)
	}
	empty := authapi.RoleInput{Color: str("")}
	if err := validateRole(&empty, false); err != nil || !empty.ClearColor {
		t.Fatalf("empty colour should clear: %v %+v", err, empty)
	}
}

func TestRoleMembership(t *testing.T) {
	f, svc, _ := setup(t)
	s := login(t, svc)
	ctx := context.Background()
	refs, err := svc.AddRoleMember(ctx, s, authapitest.RoleModerator, "usr_bob")
	if err != nil || len(refs) != 1 || refs[0].ID != authapitest.RoleModerator {
		t.Fatalf("add: %v %+v", err, refs)
	}
	if refs, err = svc.AddRoleMember(ctx, s, authapitest.RoleModerator, "usr_bob"); err != nil || len(refs) != 1 {
		t.Fatalf("add again is a no-op: %v %+v", err, refs)
	}
	if refs, err = svc.RemoveRoleMember(ctx, s, authapitest.RoleModerator, "usr_bob"); err != nil || len(refs) != 0 {
		t.Fatalf("remove: %v %+v", err, refs)
	}
	f.NoRoles = true
	if _, err := svc.AddRoleMember(ctx, s, authapitest.RoleModerator, "usr_bob"); !apperr.IsUnsupported(err) {
		t.Fatalf("before roles: %v", err)
	}
	if list, err := svc.ListPermissions(ctx, s); err != nil || list.Source != "panel" || len(list.Permissions) != len(Catalog) {
		t.Fatalf("catalog fallback: %v %+v", err, list)
	}
}
