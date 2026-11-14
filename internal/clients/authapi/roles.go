package authapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// RequiresRoles is the auth release with roles and permissions.
const RequiresRoles = "v1.5.0"

const rolesWhat = "Roles and permissions"

// Built-in roles. Member is everyone's implicit role.
const (
	BuiltinOwner     = "owner"
	BuiltinAdmin     = "admin"
	BuiltinModerator = "moderator"
	BuiltinMember    = "member"
)

// Permission groups.
const (
	GroupAdmin   = "admin"
	GroupNetwork = "network"
	GroupIRC     = "irc"
)

// Permission is one entry of GET /v1/admin/permissions.
type Permission struct {
	ID          string `json:"id"`
	Group       string `json:"group"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// RoleIRC is how a role shows up on IRC: each role is an Ergo oper class.
type RoleIRC struct {
	// WhoisLine is the oper class's /whois line (RPL_WHOISOPERATOR).
	WhoisLine string `json:"whois_line"`
	// Vhost is a template such as "{username}.staff.boson.chat"; empty = none.
	Vhost string `json:"vhost"`
	// Oper: holders are opered up (get the role's irc.* capabilities) on login.
	Oper bool `json:"oper"`
}

// Role is a network-wide role.
type Role struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Color *string `json:"color"`
	// Position: higher is more senior.
	Position int `json:"position"`
	// Builtin is owner | admin | moderator | member, or nil for custom roles.
	Builtin     *string    `json:"builtin"`
	Permissions []string   `json:"permissions"`
	IRC         RoleIRC    `json:"irc"`
	Hoist       bool       `json:"hoist"`
	Mentionable bool       `json:"mentionable"`
	MemberCount int        `json:"member_count"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
}

// IsBuiltin reports whether the role is the given built-in (any built-in when kind is "").
func (r *Role) IsBuiltin(kind string) bool {
	return r.Builtin != nil && (kind == "" || *r.Builtin == kind)
}

// RoleRef is the short form of a role on users and /v1/me.
type RoleRef struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Color    *string `json:"color"`
	Position int     `json:"position"`
}

// RoleInput creates (Name required) or patches (non-nil fields) a role.
// ClearColor sends color: null.
type RoleInput struct {
	Name        *string   `json:"name,omitempty"`
	Color       *string   `json:"color,omitempty"`
	ClearColor  bool      `json:"clear_color,omitempty"`
	Permissions *[]string `json:"permissions,omitempty"`
	IRC         *RoleIRC  `json:"irc,omitempty"`
	Hoist       *bool     `json:"hoist,omitempty"`
	Mentionable *bool     `json:"mentionable,omitempty"`
}

// MarshalJSON writes the auth service's shape: color is a string or null.
func (in RoleInput) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	if in.Name != nil {
		m["name"] = *in.Name
	}
	switch {
	case in.ClearColor:
		m["color"] = nil
	case in.Color != nil:
		m["color"] = *in.Color
	}
	if in.Permissions != nil {
		m["permissions"] = *in.Permissions
	}
	if in.IRC != nil {
		m["irc"] = in.IRC
	}
	if in.Hoist != nil {
		m["hoist"] = *in.Hoist
	}
	if in.Mentionable != nil {
		m["mentionable"] = *in.Mentionable
	}
	return json.Marshal(m)
}

// UnmarshalJSON accepts the panel's own API: color null clears it.
func (in *RoleInput) UnmarshalJSON(b []byte) error {
	type plain RoleInput
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if c, ok := raw["color"]; ok && string(c) == "null" {
		p.Color, p.ClearColor = nil, true
	}
	*in = RoleInput(p)
	return nil
}

// MemberPage is GET /v1/admin/roles/{id}/members.
type MemberPage struct {
	Users      []User `json:"users"`
	NextCursor string `json:"next_cursor,omitempty"`
}

func rolePath(id string) string { return "/v1/admin/roles/" + url.PathEscape(id) }

func (c *AuthClient) ListPermissions(ctx context.Context, at string) ([]Permission, error) {
	var out struct {
		Permissions []Permission `json:"permissions"`
	}
	if err := c.do(ctx, "admin.permissions.list", http.MethodGet, "/v1/admin/permissions", at, nil, &out); err != nil {
		return nil, feature(err, rolesWhat, RequiresRoles)
	}
	if out.Permissions == nil {
		out.Permissions = []Permission{}
	}
	return out.Permissions, nil
}

func (c *AuthClient) ListRoles(ctx context.Context, at string) ([]Role, error) {
	var out struct {
		Roles []Role `json:"roles"`
	}
	if err := c.do(ctx, "admin.roles.list", http.MethodGet, "/v1/admin/roles", at, nil, &out); err != nil {
		return nil, feature(err, rolesWhat, RequiresRoles)
	}
	if out.Roles == nil {
		out.Roles = []Role{}
	}
	return out.Roles, nil
}

type roleEnvelope struct {
	Role Role `json:"role"`
}

func (c *AuthClient) CreateRole(ctx context.Context, at string, in RoleInput) (*Role, error) {
	var out roleEnvelope
	if err := c.do(ctx, "admin.roles.create", http.MethodPost, "/v1/admin/roles", at, in, &out); err != nil {
		return nil, feature(err, rolesWhat, RequiresRoles)
	}
	return &out.Role, nil
}

func (c *AuthClient) UpdateRole(ctx context.Context, at, id string, in RoleInput) (*Role, error) {
	var out roleEnvelope
	if err := c.do(ctx, "admin.roles.update", http.MethodPatch, rolePath(id), at, in, &out); err != nil {
		return nil, feature(err, rolesWhat, RequiresRoles)
	}
	return &out.Role, nil
}

func (c *AuthClient) DeleteRole(ctx context.Context, at, id string) error {
	return feature(c.do(ctx, "admin.roles.delete", http.MethodDelete, rolePath(id), at, nil, nil), rolesWhat, RequiresRoles)
}

func (c *AuthClient) ReorderRoles(ctx context.Context, at string, order []string) ([]Role, error) {
	var out struct {
		Roles []Role `json:"roles"`
	}
	if err := c.do(ctx, "admin.roles.reorder", http.MethodPost, "/v1/admin/roles/reorder", at,
		map[string]any{"order": order}, &out); err != nil {
		return nil, feature(err, rolesWhat, RequiresRoles)
	}
	return out.Roles, nil // may be nil: the caller re-lists
}

func (c *AuthClient) RoleMembers(ctx context.Context, at, id, cursor string, limit int) (*MemberPage, error) {
	q := url.Values{}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out struct {
		Users      []User          `json:"users"`
		NextCursor json.RawMessage `json:"next_cursor"`
	}
	if err := c.do(ctx, "admin.roles.members", http.MethodGet, rolePath(id)+"/members?"+q.Encode(), at, nil, &out); err != nil {
		return nil, feature(err, rolesWhat, RequiresRoles)
	}
	p := &MemberPage{Users: out.Users}
	if p.Users == nil {
		p.Users = []User{}
	}
	_ = json.Unmarshal(out.NextCursor, &p.NextCursor) // null → ""
	return p, nil
}

func (c *AuthClient) SetUserRoles(ctx context.Context, at, userID string, roleIDs []string) ([]RoleRef, error) {
	if roleIDs == nil {
		roleIDs = []string{}
	}
	var out struct {
		Roles []RoleRef `json:"roles"`
	}
	if err := c.do(ctx, "admin.users.set_roles", http.MethodPut, "/v1/admin/users/"+url.PathEscape(userID)+"/roles", at,
		map[string]any{"role_ids": roleIDs}, &out); err != nil {
		return nil, feature(err, rolesWhat, RequiresRoles)
	}
	if out.Roles == nil {
		out.Roles = []RoleRef{}
	}
	return out.Roles, nil
}
