package admin

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/session"
)

// PermissionList is GET /api/permissions. Source is "auth" when the auth
// service listed them, "panel" when it predates roles and the panel's
// Catalog is shown instead (Requires says which release has roles).
type PermissionList struct {
	Permissions []authapi.Permission `json:"permissions"`
	Source      string               `json:"source"`
	Requires    string               `json:"requires,omitempty"`
}

func (s *AdminService) ListPermissions(ctx context.Context, sess *session.Session) (*PermissionList, error) {
	var out []authapi.Permission
	err := s.call(ctx, sess, func(at string) (err error) { out, err = s.Client.ListPermissions(ctx, at); return })
	if apperr.IsUnsupported(err) {
		return &PermissionList{Permissions: Catalog, Source: "panel", Requires: authapi.RequiresRoles}, nil
	}
	if err != nil {
		return nil, err
	}
	return &PermissionList{Permissions: out, Source: "auth"}, nil
}

func (s *AdminService) ListRoles(ctx context.Context, sess *session.Session) ([]authapi.Role, error) {
	var out []authapi.Role
	err := s.call(ctx, sess, func(at string) (err error) { out, err = s.Client.ListRoles(ctx, at); return })
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(out, func(a, b authapi.Role) int { return b.Position - a.Position })
	return out, nil
}

var (
	colorRe = regexp.MustCompile(`^#[0-9a-f]{6}$`)
	permRe  = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
	// auth's role name rule: ASCII letters, digits, space, - and _.
	nameRe = regexp.MustCompile(`^[A-Za-z0-9 _-]{1,32}$`)
	// Ergo's default vhost rule, which every rendering must pass.
	vhostCharsRe = regexp.MustCompile(`^[0-9A-Za-z.\-_/]+$`)
)

const (
	maxWhoisLine = 120
	// Ergo's vhost max-length; checked with a 30-character username.
	maxVhost            = 64
	longestUsername     = 30
	usernamePlaceholder = "{username}"
)

// VhostProblem explains why a vhost template isn't one auth accepts, or "".
// Mirrors auth: static characters [0-9A-Za-z.-_/] plus {username}, at most
// 64 bytes with a 30-character username, no leading or trailing . or /, no
// .. or //.
func VhostProblem(t string) string {
	sample := strings.ReplaceAll(t, usernamePlaceholder, strings.Repeat("u", longestUsername))
	static := strings.ReplaceAll(t, usernamePlaceholder, "u")
	switch {
	case !vhostCharsRe.MatchString(static):
		return "a vhost uses letters, digits, . - _ / and {username} only"
	case len(sample) > maxVhost:
		return "the vhost is too long: at most 64 characters with a 30-character username"
	case strings.HasPrefix(static, ".") || strings.HasPrefix(static, "/") || strings.HasSuffix(static, ".") || strings.HasSuffix(static, "/"):
		return "a vhost can't start or end with . or /"
	case strings.Contains(static, "..") || strings.Contains(static, "//"):
		return "a vhost can't contain .. or //"
	}
	return ""
}

// validateRole normalises and checks a create (Name required) or patch.
func validateRole(in *authapi.RoleInput, create bool) error {
	if in.Name != nil {
		*in.Name = strings.Join(strings.Fields(*in.Name), " ")
		if !nameRe.MatchString(*in.Name) {
			return apperr.Invalid("a role name is 1–32 letters, digits, spaces, - or _")
		}
	} else if create {
		return apperr.Invalid("a role needs a name")
	}
	if in.Color != nil {
		*in.Color = strings.ToLower(strings.TrimSpace(*in.Color))
		if *in.Color == "" {
			in.Color, in.ClearColor = nil, true
		} else if !colorRe.MatchString(*in.Color) {
			return apperr.Invalid("the colour must look like #d97706")
		}
	}
	if in.Permissions != nil {
		seen := make([]string, 0, len(*in.Permissions))
		for _, p := range *in.Permissions {
			p = strings.TrimSpace(p)
			if !permRe.MatchString(p) {
				return apperr.Invalid("“" + p + "” isn't a permission")
			}
			if !slices.Contains(seen, p) {
				seen = append(seen, p)
			}
		}
		slices.Sort(seen)
		*in.Permissions = seen
	}
	if irc := in.IRC; irc != nil {
		// Empty clears (sent as null): the default WHOIS line, no vhost.
		if irc.WhoisLine != nil {
			w := strings.TrimSpace(*irc.WhoisLine)
			switch {
			case w == "":
				irc.WhoisLine = nil
			case len([]rune(w)) > maxWhoisLine:
				return apperr.Invalid("the WHOIS line is at most 120 characters")
			case strings.IndexFunc(w, unicode.IsControl) >= 0:
				return apperr.Invalid("the WHOIS line can't contain control characters or line breaks")
			default:
				irc.WhoisLine = &w
			}
		}
		if irc.Vhost != nil {
			v := strings.TrimSpace(*irc.Vhost)
			if v == "" {
				irc.Vhost = nil
			} else if p := VhostProblem(v); p != "" {
				return apperr.Invalid(p)
			} else {
				irc.Vhost = &v
			}
		}
	}
	return nil
}

// roleFriendly rewrites the auth service's role errors for the panel.
var roleFriendly = map[string]string{
	"builtin_role":        "Built-in roles can't be deleted.",
	"role_name_taken":     "Another role already has that name (or one that only differs in case, spaces or dashes).",
	"role_hierarchy":      "You can only change roles, and people, below your own highest role.",
	"permission_not_held": "You can't grant a permission you don't have yourself.",
	"last_owner":          "This would leave the network without an Owner.",
}

func rolesFriendly(err error) error {
	if e, ok := apperr.As(err); ok && e.Status < 500 {
		if msg, ok := roleFriendly[e.Code]; ok {
			return apperr.New(e.Status, e.Code, msg)
		}
	}
	return err
}

func (s *AdminService) GetRole(ctx context.Context, sess *session.Session, id string) (*authapi.Role, error) {
	var out *authapi.Role
	err := s.call(ctx, sess, func(at string) (err error) { out, err = s.Client.GetRole(ctx, at, id); return })
	return out, err
}

func (s *AdminService) CreateRole(ctx context.Context, sess *session.Session, in authapi.RoleInput) (*authapi.Role, error) {
	if err := validateRole(&in, true); err != nil {
		return nil, err
	}
	var out *authapi.Role
	err := s.call(ctx, sess, func(at string) (err error) { out, err = s.Client.CreateRole(ctx, at, in); return })
	s.Forget(sess)
	return out, rolesFriendly(err)
}

func (s *AdminService) UpdateRole(ctx context.Context, sess *session.Session, id string, in authapi.RoleInput) (*authapi.Role, error) {
	if err := validateRole(&in, false); err != nil {
		return nil, err
	}
	if in == (authapi.RoleInput{}) {
		return nil, apperr.Invalid("nothing to update")
	}
	var out *authapi.Role
	err := s.call(ctx, sess, func(at string) (err error) { out, err = s.Client.UpdateRole(ctx, at, id, in); return })
	s.Forget(sess)
	return out, rolesFriendly(err)
}

func (s *AdminService) DeleteRole(ctx context.Context, sess *session.Session, id string) error {
	err := s.call(ctx, sess, func(at string) error { return s.Client.DeleteRole(ctx, at, id) })
	s.Forget(sess)
	return rolesFriendly(err)
}

func (s *AdminService) ReorderRoles(ctx context.Context, sess *session.Session, order []string) ([]authapi.Role, error) {
	if len(order) == 0 {
		return nil, apperr.Invalid("order is the list of role IDs, most senior first")
	}
	seen := map[string]bool{}
	for _, id := range order {
		if id == "" || seen[id] {
			return nil, apperr.Invalid("order lists each role ID once")
		}
		seen[id] = true
	}
	var out []authapi.Role
	err := s.call(ctx, sess, func(at string) (err error) { out, err = s.Client.ReorderRoles(ctx, at, order); return })
	s.Forget(sess)
	if err != nil {
		return nil, rolesFriendly(err)
	}
	if out == nil {
		return s.ListRoles(ctx, sess)
	}
	slices.SortStableFunc(out, func(a, b authapi.Role) int { return b.Position - a.Position })
	return out, nil
}

func (s *AdminService) RoleMembers(ctx context.Context, sess *session.Session, id, cursor string, limit int) (*authapi.MemberPage, error) {
	var out *authapi.MemberPage
	err := s.call(ctx, sess, func(at string) (err error) { out, err = s.Client.RoleMembers(ctx, at, id, cursor, limit); return })
	return out, err
}

func (s *AdminService) SetUserRoles(ctx context.Context, sess *session.Session, userID string, roleIDs []string) ([]authapi.RoleRef, error) {
	ids := make([]string, 0, len(roleIDs))
	for _, id := range roleIDs {
		if id = strings.TrimSpace(id); id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	var out []authapi.RoleRef
	err := s.call(ctx, sess, func(at string) (err error) { out, err = s.Client.SetUserRoles(ctx, at, userID, ids); return })
	s.Forget(sess)
	return out, rolesFriendly(err)
}

// changeMember reads the user's roles and writes them back with roleID
// added or removed.
func (s *AdminService) changeMember(ctx context.Context, sess *session.Session, roleID, userID string, add bool) ([]authapi.RoleRef, error) {
	u, err := s.GetUser(ctx, sess, userID)
	if err != nil {
		return nil, err
	}
	if u.Roles == nil {
		return nil, apperr.Unsupported(rolesNeed)
	}
	ids := make([]string, 0, len(*u.Roles)+1)
	has := false
	for _, r := range *u.Roles {
		if r.ID == roleID {
			has = true
			if !add {
				continue
			}
		}
		ids = append(ids, r.ID)
	}
	if add && !has {
		ids = append(ids, roleID)
	}
	if add == has {
		return *u.Roles, nil // nothing to do
	}
	return s.SetUserRoles(ctx, sess, userID, ids)
}

const rolesNeed = "Roles and permissions need a newer auth service (auth ≥ " + authapi.RequiresRoles + ")"

func (s *AdminService) AddRoleMember(ctx context.Context, sess *session.Session, roleID, userID string) ([]authapi.RoleRef, error) {
	return s.changeMember(ctx, sess, roleID, userID, true)
}

func (s *AdminService) RemoveRoleMember(ctx context.Context, sess *session.Session, roleID, userID string) ([]authapi.RoleRef, error) {
	return s.changeMember(ctx, sess, roleID, userID, false)
}
