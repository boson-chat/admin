package admin

import (
	"context"
	"net/http"
	"slices"
	"strings"
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

// Catalog is auth 1.5.0's permission list (GET /v1/admin/permissions).
// The panel renders the live list; this copy is only for auth < 1.5.0,
// where role=admin is granted all of it.
var Catalog = []authapi.Permission{
	{ID: PermPanel, Group: authapi.GroupAdmin, Label: "Open the admin panel", Description: "Sign in to the admin panel and see roles and settings."},
	{ID: PermUsersView, Group: authapi.GroupAdmin, Label: "View users", Description: "List and search users, see their sessions and roles."},
	{ID: PermUsersManage, Group: authapi.GroupAdmin, Label: "Manage users", Description: "Create and edit users, disable and enable them, send password resets and verification emails."},
	{ID: PermUsersModerate, Group: authapi.GroupAdmin, Label: "Moderate users", Description: "Disable and enable users and revoke their sessions."},
	{ID: PermInvitesManage, Group: authapi.GroupAdmin, Label: "Manage invites", Description: "Create, list and revoke invite codes."},
	{ID: PermSettingsManage, Group: authapi.GroupAdmin, Label: "Manage network settings", Description: "Change registration, email verification and channel creation settings."},
	{ID: PermSSOManage, Group: authapi.GroupAdmin, Label: "Manage SSO", Description: "Add, edit and remove OIDC and SAML identity providers."},
	{ID: PermServicesView, Group: authapi.GroupAdmin, Label: "View services", Description: "See the status of network services (admin panel)."},
	{ID: PermServicesManage, Group: authapi.GroupAdmin, Label: "Manage services", Description: "Control network services (admin panel)."},
	{ID: PermAuditView, Group: authapi.GroupAdmin, Label: "View audit log", Description: "Read the audit log."},
	{ID: PermRolesManage, Group: authapi.GroupAdmin, Label: "Manage roles", Description: "Create, edit, delete and assign roles below your own."},
	{ID: PermRolesOwner, Group: authapi.GroupAdmin, Label: "Manage the Owner role", Description: "Edit the Owner role and grant or remove it (ownership transfer)."},
	{ID: PermDirectoryManage, Group: authapi.GroupAdmin, Label: "Manage the directory", Description: "Edit the public channel directory (admin panel)."},
	{ID: "channel.create", Group: authapi.GroupNetwork, Label: "Create channels", Description: "Register IRC text channels."},
	{ID: "voice.create", Group: authapi.GroupNetwork, Label: "Create voice channels", Description: "Create voice channels."},
	{ID: "voice.manage_any", Group: authapi.GroupNetwork, Label: "Manage any voice channel", Description: "Moderate, edit and delete any voice channel."},
	{ID: "voice.bypass_locks", Group: authapi.GroupNetwork, Label: "Bypass voice locks", Description: "Join locked or full voice channels."},
	{ID: "irc.kill", Group: authapi.GroupIRC, Label: "IRC: Kill", Description: "Disconnect users (KILL, NickServ LOGOUT of other users' sessions)."},
	{ID: "irc.ban", Group: authapi.GroupIRC, Label: "IRC: Ban", Description: "Ban IPs, CIDRs and masks and suspend accounts (UBAN, DLINE, KLINE)."},
	{ID: "irc.nofakelag", Group: authapi.GroupIRC, Label: "IRC: No fakelag", Description: "Exempt from fakelag message-rate limits."},
	{ID: "irc.relaymsg", Group: authapi.GroupIRC, Label: "IRC: Relay messages", Description: "Use RELAYMSG in any channel."},
	{ID: "irc.vhosts", Group: authapi.GroupIRC, Label: "IRC: Vhosts", Description: "Set and remove other users' vhosts (HostServ)."},
	{ID: "irc.sajoin", Group: authapi.GroupIRC, Label: "IRC: SAJOIN", Description: "Join arbitrary channels, including private ones, and force-join users."},
	{ID: "irc.samode", Group: authapi.GroupIRC, Label: "IRC: SAMODE", Description: "Change arbitrary channel and user modes (also needed to kick without op)."},
	{ID: "irc.snomasks", Group: authapi.GroupIRC, Label: "IRC: Snomasks", Description: "Subscribe to arbitrary server notice masks."},
	{ID: "irc.roleplay", Group: authapi.GroupIRC, Label: "IRC: Roleplay", Description: "Use the (deprecated) roleplay commands in any channel."},
	{ID: "irc.rehash", Group: authapi.GroupIRC, Label: "IRC: Rehash", Description: "Reload the IRC server configuration at runtime."},
	{ID: "irc.accreg", Group: authapi.GroupIRC, Label: "IRC: Account registrations", Description: "Modify or unregister arbitrary accounts (NickServ)."},
	{ID: "irc.chanreg", Group: authapi.GroupIRC, Label: "IRC: Channel registrations", Description: "Modify or unregister arbitrary channel registrations (ChanServ)."},
	{ID: "irc.history", Group: authapi.GroupIRC, Label: "IRC: History", Description: "Modify or delete history messages."},
	{ID: "irc.defcon", Group: authapi.GroupIRC, Label: "IRC: DEFCON", Description: "Use DEFCON to restrict server capabilities."},
	{ID: "irc.massmessage", Group: authapi.GroupIRC, Label: "IRC: Mass message", Description: "Message every user on the server."},
	{ID: "irc.metadata", Group: authapi.GroupIRC, Label: "IRC: Metadata", Description: "Modify arbitrary metadata on channels and users."},
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

// CanAny reports whether the account has at least one of perms.
func (a *Access) CanAny(perms ...string) bool {
	for _, p := range perms {
		if a.set[p] {
			return true
		}
	}
	return false
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

// MissingPermission is a 403 that leaves the panel session alone. perm
// may list alternatives as "a|b".
func MissingPermission(perm string) error {
	alts := strings.Split(perm, "|")
	labels := make([]string, 0, len(alts))
	for _, id := range alts {
		label := id
		for _, p := range Catalog {
			if p.ID == id {
				label = p.Label
				break
			}
		}
		labels = append(labels, "“"+label+"” ("+id+")")
	}
	return apperr.New(http.StatusForbidden, "missing_permission", "you need the "+strings.Join(labels, " or the ")+" permission for this")
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
