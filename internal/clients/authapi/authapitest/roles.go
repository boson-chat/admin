package authapitest

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
)

// Built-in role IDs in the fake.
const (
	RoleOwner     = "rol_owner"
	RoleAdmin     = "rol_admin"
	RoleModerator = "rol_moderator"
	RoleMember    = "rol_member"
)

// AdminPermissions are the panel permissions the fake's Admin role has
// (everything but roles.owner).
var AdminPermissions = []string{
	"admin.panel", "audit.view", "directory.manage", "invites.manage", "roles.manage",
	"services.manage", "services.view", "settings.manage", "sso.manage",
	"users.manage", "users.moderate", "users.view",
}

// FakePermissions is the fake's GET /v1/admin/permissions.
var FakePermissions = func() []authapi.Permission {
	out := []authapi.Permission{}
	for _, p := range append(slices.Clone(AdminPermissions), "roles.owner") {
		out = append(out, authapi.Permission{ID: p, Group: "admin", Label: p, Description: "desc " + p})
	}
	for _, p := range []string{"channel.create", "voice.create", "voice.manage_any", "voice.bypass_locks"} {
		out = append(out, authapi.Permission{ID: p, Group: "network", Label: p})
	}
	for _, p := range []string{"irc.kill", "irc.ban", "irc.samode"} {
		out = append(out, authapi.Permission{ID: p, Group: "irc", Label: p})
	}
	return out
}()

func strp(s string) *string { return &s }

func defaultRoles() []authapi.Role {
	at := time.Unix(1700000000, 0).UTC()
	all := []string{}
	for _, p := range FakePermissions {
		all = append(all, p.ID)
	}
	return []authapi.Role{
		{ID: RoleOwner, Name: "Owner", Color: strp("#d97706"), Position: 100, Builtin: strp("owner"), Permissions: all,
			IRC: authapi.RoleIRC{WhoisLine: "is the network owner", Vhost: "{username}.owner.boson.chat", Oper: true}, Hoist: true, CreatedAt: &at},
		{ID: RoleAdmin, Name: "Admin", Color: strp("#ef4444"), Position: 90, Builtin: strp("admin"),
			Permissions: append(slices.Clone(AdminPermissions), "irc.kill", "irc.ban"),
			IRC:         authapi.RoleIRC{WhoisLine: "is a network administrator", Vhost: "{username}.staff.boson.chat", Oper: true}, Hoist: true, CreatedAt: &at},
		{ID: RoleModerator, Name: "Moderator", Color: strp("#3b82f6"), Position: 50, Builtin: strp("moderator"),
			Permissions: []string{"admin.panel", "users.view", "users.moderate", "voice.manage_any"},
			IRC:         authapi.RoleIRC{WhoisLine: "is a moderator", Oper: true}, CreatedAt: &at},
		{ID: RoleMember, Name: "Member", Position: 0, Builtin: strp("member"),
			Permissions: []string{"channel.create", "voice.create"}, CreatedAt: &at},
	}
}

// SetRoles replaces a user's explicit roles (Member is implicit).
func (f *Fake) SetRoles(userID string, roleIDs ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.UserRoles[userID] = slices.Clone(roleIDs)
}

func (f *Fake) roleLocked(id string) (int, *authapi.Role) {
	for i := range f.Roles {
		if f.Roles[i].ID == id {
			return i, &f.Roles[i]
		}
	}
	return -1, nil
}

func (f *Fake) rolesSupported() bool { return !f.Legacy && !f.NoRoles }

// decorateLocked adds roles and effective permissions to a copy of u.
func (f *Fake) decorateLocked(u *authapi.User) {
	if !f.rolesSupported() {
		return
	}
	refs := []authapi.RoleRef{}
	perms := map[string]bool{}
	ids := append(slices.Clone(f.UserRoles[u.ID]), RoleMember)
	for _, id := range ids {
		_, r := f.roleLocked(id)
		if r == nil {
			continue
		}
		if r.ID != RoleMember {
			refs = append(refs, authapi.RoleRef{ID: r.ID, Name: r.Name, Color: r.Color, Position: r.Position})
		}
		for _, p := range r.Permissions {
			perms[p] = true
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Position > refs[j].Position })
	list := make([]string, 0, len(perms))
	for p := range perms {
		list = append(list, p)
	}
	sort.Strings(list)
	u.Roles, u.Permissions = &refs, &list
}

// actorLocked is the caller with roles, for hierarchy checks.
func (f *Fake) actorLocked(at string) (top int, owner bool, perms map[string]bool, err error) {
	u, err := f.authLocked(at, true)
	if err != nil {
		return 0, false, nil, err
	}
	c := *u
	f.decorateLocked(&c)
	perms = map[string]bool{}
	for _, p := range *c.Permissions {
		perms[p] = true
	}
	for _, r := range *c.Roles {
		top = max(top, r.Position)
		if r.ID == RoleOwner {
			owner = true
		}
	}
	return top, owner, perms, nil
}

var (
	errHierarchy = apperr.New(http.StatusForbidden, "role_hierarchy", "role is not below yours")
	errNotHeld   = apperr.New(http.StatusForbidden, "permission_not_held", "cannot grant a permission you lack")
)

func (f *Fake) rolesGate(at string) (int, bool, map[string]bool, error) {
	if !f.rolesSupported() {
		return 0, false, nil, apperr.Unsupported("Roles and permissions need a newer auth service (auth ≥ v1.5.0)")
	}
	top, owner, perms, err := f.actorLocked(at)
	if err != nil {
		return 0, false, nil, err
	}
	if !owner && !perms["roles.manage"] {
		return 0, false, nil, apperr.New(http.StatusForbidden, "forbidden", "roles.manage required")
	}
	return top, owner, perms, nil
}

func (f *Fake) ListPermissions(_ context.Context, at string) ([]authapi.Permission, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.authLocked(at, true); err != nil {
		return nil, err
	}
	if !f.rolesSupported() {
		return nil, apperr.Unsupported("Roles and permissions need a newer auth service (auth ≥ v1.5.0)")
	}
	return slices.Clone(FakePermissions), nil
}

func (f *Fake) countLocked(id string) int {
	if id == RoleMember {
		return len(f.Users)
	}
	n := 0
	for _, ids := range f.UserRoles {
		if slices.Contains(ids, id) {
			n++
		}
	}
	return n
}

func (f *Fake) ListRoles(_ context.Context, at string) ([]authapi.Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, _, _, err := f.rolesGate(at); err != nil {
		return nil, err
	}
	out := slices.Clone(f.Roles)
	for i := range out {
		out[i].MemberCount = f.countLocked(out[i].ID)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Position > out[j].Position })
	return out, nil
}

func applyRole(r *authapi.Role, in authapi.RoleInput) {
	if in.Name != nil {
		r.Name = *in.Name
	}
	if in.ClearColor {
		r.Color = nil
	} else if in.Color != nil {
		c := *in.Color
		r.Color = &c
	}
	if in.Permissions != nil {
		r.Permissions = slices.Clone(*in.Permissions)
	}
	if in.IRC != nil {
		r.IRC = *in.IRC
	}
	if in.Hoist != nil {
		r.Hoist = *in.Hoist
	}
	if in.Mentionable != nil {
		r.Mentionable = *in.Mentionable
	}
	now := time.Now().UTC()
	r.UpdatedAt = &now
}

func grants(in authapi.RoleInput, owner bool, perms map[string]bool) error {
	if owner || in.Permissions == nil {
		return nil
	}
	for _, p := range *in.Permissions {
		if !perms[p] {
			return errNotHeld
		}
	}
	return nil
}

func (f *Fake) CreateRole(_ context.Context, at string, in authapi.RoleInput) (*authapi.Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	top, owner, perms, err := f.rolesGate(at)
	if err != nil {
		return nil, err
	}
	if err := grants(in, owner, perms); err != nil {
		return nil, err
	}
	f.n++
	r := authapi.Role{ID: fmt.Sprintf("rol_%d", f.n), Position: 1, Permissions: []string{}}
	// New roles go straight above Member, below the creator.
	for i := range f.Roles {
		if f.Roles[i].ID != RoleMember && f.Roles[i].Position < top {
			f.Roles[i].Position++
		}
	}
	applyRole(&r, in)
	f.Roles = append(f.Roles, r)
	return &r, nil
}

func (f *Fake) UpdateRole(_ context.Context, at, id string, in authapi.RoleInput) (*authapi.Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	top, owner, perms, err := f.rolesGate(at)
	if err != nil {
		return nil, err
	}
	_, r := f.roleLocked(id)
	if r == nil {
		return nil, apperr.New(http.StatusNotFound, "role_not_found", "no such role")
	}
	if !owner && r.Position >= top {
		return nil, errHierarchy
	}
	if err := grants(in, owner, perms); err != nil {
		return nil, err
	}
	applyRole(r, in)
	c := *r
	c.MemberCount = f.countLocked(id)
	return &c, nil
}

func (f *Fake) DeleteRole(_ context.Context, at, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	top, owner, _, err := f.rolesGate(at)
	if err != nil {
		return err
	}
	i, r := f.roleLocked(id)
	if r == nil {
		return apperr.New(http.StatusNotFound, "role_not_found", "no such role")
	}
	if r.Builtin != nil {
		return apperr.New(http.StatusConflict, "role_builtin", "built-in roles cannot be deleted")
	}
	if !owner && r.Position >= top {
		return errHierarchy
	}
	f.Roles = append(f.Roles[:i], f.Roles[i+1:]...)
	for uid, ids := range f.UserRoles {
		f.UserRoles[uid] = slices.DeleteFunc(ids, func(s string) bool { return s == id })
	}
	return nil
}

func (f *Fake) ReorderRoles(_ context.Context, at string, order []string) ([]authapi.Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	top, owner, _, err := f.rolesGate(at)
	if err != nil {
		return nil, err
	}
	if len(order) != len(f.Roles) {
		return nil, apperr.Invalid("order must list every role")
	}
	cur := slices.Clone(f.Roles)
	sort.SliceStable(cur, func(i, j int) bool { return cur[i].Position > cur[j].Position })
	for i, id := range order {
		_, r := f.roleLocked(id)
		if r == nil {
			return nil, apperr.Invalid("unknown role " + id)
		}
		// Roles at or above the actor's keep their place.
		if !owner && r.Position >= top && cur[i].ID != id {
			return nil, errHierarchy
		}
	}
	if order[len(order)-1] != RoleMember {
		return nil, apperr.Invalid("member stays last")
	}
	n := len(order)
	for i, id := range order {
		_, r := f.roleLocked(id)
		r.Position = (n - 1 - i) * 10
	}
	f.LastOrder = slices.Clone(order)
	return nil, nil // like a 204: the panel re-lists
}

func (f *Fake) RoleMembers(_ context.Context, at, id, cursor string, limit int) (*authapi.MemberPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, _, _, err := f.rolesGate(at); err != nil {
		return nil, err
	}
	if _, r := f.roleLocked(id); r == nil {
		return nil, apperr.New(http.StatusNotFound, "role_not_found", "no such role")
	}
	ids := []string{}
	for uid := range f.Users {
		if id == RoleMember || slices.Contains(f.UserRoles[uid], id) {
			ids = append(ids, uid)
		}
	}
	sort.Strings(ids)
	start := 0
	if cursor != "" {
		_, _ = fmt.Sscanf(cursor, "%d", &start)
	}
	if limit <= 0 {
		limit = 50
	}
	p := &authapi.MemberPage{Users: []authapi.User{}}
	for i := start; i < len(ids); i++ {
		if len(p.Users) == limit {
			p.NextCursor = fmt.Sprint(i)
			break
		}
		u := *f.Users[ids[i]]
		f.decorateLocked(&u)
		p.Users = append(p.Users, u)
	}
	return p, nil
}

func (f *Fake) SetUserRoles(_ context.Context, at, userID string, roleIDs []string) ([]authapi.RoleRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	top, owner, _, err := f.rolesGate(at)
	if err != nil {
		return nil, err
	}
	u, ok := f.Users[userID]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	// Roles at or above the actor's can't be added or removed.
	before, after := f.UserRoles[userID], roleIDs
	for _, id := range append(slices.Clone(before), after...) {
		if slices.Contains(before, id) == slices.Contains(after, id) {
			continue
		}
		_, r := f.roleLocked(id)
		if r == nil {
			return nil, apperr.Invalid("unknown role " + id)
		}
		if r.ID == RoleMember {
			return nil, apperr.New(http.StatusBadRequest, "member_role_implicit", "member is implicit")
		}
		if !owner && r.Position >= top {
			return nil, errHierarchy
		}
	}
	f.UserRoles[userID] = slices.Clone(roleIDs)
	c := *u
	f.decorateLocked(&c)
	return *c.Roles, nil
}
