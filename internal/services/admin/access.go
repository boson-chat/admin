package admin

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/session"
)

// Panel permissions (auth >= 1.5.0). Each page and BFF endpoint needs one.
const (
	PermPanel           = "admin.panel"
	PermUsersView       = "users.view"
	PermUsersManage     = "users.manage"
	PermUsersModerate   = "users.moderate"
	PermInvitesManage   = "invites.manage"
	PermSettingsManage  = "settings.manage"
	PermSSOManage       = "sso.manage"
	PermServicesView    = "services.view"
	PermServicesManage  = "services.manage"
	PermAuditView       = "audit.view"
	PermRolesManage     = "roles.manage"
	PermRolesOwner      = "roles.owner"
	PermDirectoryManage = "directory.manage"
)

// Catalog is the permission list the panel knows. It labels permissions
// when the auth service predates roles, and on auth < 1.5.0 it is what
// role=admin is granted (everything).
var Catalog = []authapi.Permission{
	{ID: PermPanel, Group: authapi.GroupAdmin, Label: "Open the admin panel", Description: "Sign in to this panel. Every other admin permission needs it."},
	{ID: PermUsersView, Group: authapi.GroupAdmin, Label: "View users", Description: "See accounts, their sessions, linked sign-ins and recent activity."},
	{ID: PermUsersManage, Group: authapi.GroupAdmin, Label: "Manage users", Description: "Create accounts and send password resets and verification emails."},
	{ID: PermUsersModerate, Group: authapi.GroupAdmin, Label: "Moderate users", Description: "Disable and enable accounts and revoke their sessions."},
	{ID: PermInvitesManage, Group: authapi.GroupAdmin, Label: "Manage invites", Description: "Create, list and revoke registration invites."},
	{ID: PermSettingsManage, Group: authapi.GroupAdmin, Label: "Manage network settings", Description: "Change registration and who may create channels and voice rooms."},
	{ID: PermSSOManage, Group: authapi.GroupAdmin, Label: "Manage sign-in providers", Description: "Add, edit and remove OpenID Connect and SAML providers."},
	{ID: PermServicesView, Group: authapi.GroupAdmin, Label: "View services", Description: "See the stack's health, containers and their logs."},
	{ID: PermServicesManage, Group: authapi.GroupAdmin, Label: "Control services", Description: "Restart, stop and start the stack's containers."},
	{ID: PermAuditView, Group: authapi.GroupAdmin, Label: "View the audit log", Description: "Read every sign-in and admin action."},
	{ID: PermRolesManage, Group: authapi.GroupAdmin, Label: "Manage roles", Description: "Create, edit, reorder and assign roles below their own highest role."},
	{ID: PermRolesOwner, Group: authapi.GroupAdmin, Label: "Manage the Owner role", Description: "Assign and edit the Owner role. Give it to as few people as possible."},
	{ID: PermDirectoryManage, Group: authapi.GroupAdmin, Label: "Manage the directory listing", Description: "List, edit and unlist the network in the boson.chat directory."},
	{ID: "channel.create", Group: authapi.GroupNetwork, Label: "Create text channels", Description: "Start a channel and become its founder, whatever the network setting says."},
	{ID: "voice.create", Group: authapi.GroupNetwork, Label: "Create voice rooms", Description: "Add a voice room to a channel they are in."},
	{ID: "voice.manage_any", Group: authapi.GroupNetwork, Label: "Moderate any voice room", Description: "Mute, move and remove people in every voice room."},
	{ID: "voice.bypass_locks", Group: authapi.GroupNetwork, Label: "Join locked voice rooms", Description: "Enter voice rooms that are locked or full."},
	{ID: "irc.kill", Group: authapi.GroupIRC, Label: "Kill", Description: "Disconnect anyone from IRC (/KILL)."},
	{ID: "irc.ban", Group: authapi.GroupIRC, Label: "Network bans", Description: "Set and remove network-wide bans (/DLINE, /KLINE)."},
	{ID: "irc.samode", Group: authapi.GroupIRC, Label: "Override channel modes", Description: "Change modes on any channel (/SAMODE)."},
	{ID: "irc.sajoin", Group: authapi.GroupIRC, Label: "Force joins", Description: "Join themselves or others to any channel (/SAJOIN)."},
	{ID: "irc.vhosts", Group: authapi.GroupIRC, Label: "Manage vhosts", Description: "Set and approve virtual hosts for other users."},
	{ID: "irc.nofakelag", Group: authapi.GroupIRC, Label: "No flood limits", Description: "Skip Ergo's fake lag, for bots and bridges."},
	{ID: "irc.history", Group: authapi.GroupIRC, Label: "Delete history", Description: "Delete messages from channel history."},
}

// Access is what the signed-in account may do in the panel.
type Access struct {
	User *authapi.User
	// Permissions are the effective permissions, sorted.
	Permissions []string
	// Legacy: the auth service has no roles (< 1.5.0); role=admin grants
	// every permission and anything else none.
	Legacy bool
	set    map[string]bool
}

// Can reports whether the account has every one of perms.
func (a *Access) Can(perms ...string) bool {
	for _, p := range perms {
		if !a.set[p] {
			return false
		}
	}
	return true
}

func accessFor(u *authapi.User) *Access {
	a := &Access{User: u, set: map[string]bool{}}
	switch {
	case u.Permissions != nil:
		for _, p := range *u.Permissions {
			a.set[p] = true
		}
	case u.Roles == nil && u.Role == RoleAdmin:
		a.Legacy = true
		for _, p := range Catalog {
			a.set[p.ID] = true
		}
	default:
		a.Legacy = u.Roles == nil
	}
	a.Permissions = make([]string, 0, len(a.set))
	for p := range a.set {
		a.Permissions = append(a.Permissions, p)
	}
	slices.Sort(a.Permissions)
	return a
}

// ErrNoPanel ends the panel session: the account can't open the panel.
var ErrNoPanel = apperr.ErrNotAdmin

// MissingPermission is a 403 that leaves the panel session alone.
func MissingPermission(perm string) error {
	label := perm
	for _, p := range Catalog {
		if p.ID == perm {
			label = p.Label
			break
		}
	}
	return apperr.New(http.StatusForbidden, "missing_permission", "you need the “"+label+"” permission ("+perm+") for this")
}

// AccessTTL is how long a permission check is reused for one panel session.
const AccessTTL = 10 * time.Second

type accessEntry struct {
	a  *Access
	at time.Time
}

// accessCache remembers each panel session's permissions for AccessTTL,
// so a page that fires a handful of calls asks /v1/me once.
type accessCache struct {
	mu sync.Mutex
	m  map[string]accessEntry
}

func (c *accessCache) get(key string, now time.Time) *Access {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || now.Sub(e.at) >= AccessTTL || now.Before(e.at) {
		return nil
	}
	return e.a
}

func (c *accessCache) put(key string, a *Access, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]accessEntry{}
	}
	if len(c.m) >= 1024 {
		for k, e := range c.m {
			if now.Sub(e.at) >= AccessTTL {
				delete(c.m, k)
			}
		}
	}
	c.m[key] = accessEntry{a: a, at: now}
}

func (c *accessCache) drop(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, key)
}

// Access returns the signed-in account's permissions, re-checked against
// /v1/me at most every AccessTTL (always when fresh is set: Docker and
// directory calls aren't authorised by the auth service). An account
// without admin.panel gets ErrNotAdmin, which ends the panel session.
func (s *AdminService) Access(ctx context.Context, sess *session.Session, fresh bool) (*Access, error) {
	key := sess.Snapshot().SessionID
	if !fresh {
		if a := s.access.get(key, s.Now()); a != nil {
			return a, nil
		}
	}
	var u *authapi.User
	if err := s.call(ctx, sess, func(at string) (err error) { u, err = s.Client.Me(ctx, at); return }); err != nil {
		return nil, err
	}
	a := accessFor(u)
	if !a.Can(PermPanel) {
		s.access.drop(key)
		return nil, ErrNoPanel
	}
	s.access.put(key, a, s.Now())
	return a, nil
}

// Forget drops the cached permissions of a panel session (after a role change).
func (s *AdminService) Forget(sess *session.Session) {
	s.access.drop(sess.Snapshot().SessionID)
}
