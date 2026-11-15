package admin

import (
	"context"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/logger"
	"github.com/boson-chat/admin/internal/session"
)

const (
	// MaxChannelLen is Ergo's default limits.channellen (bytes, with the #).
	MaxChannelLen = 64
	// MaxRoleChannels bounds one role's list and the channel-access list.
	MaxRoleChannels = 100
)

// NormalizeChannel trims and casefolds a channel name and checks it is one
// Ergo takes: # then 1–63 bytes without spaces, commas or control
// characters.
func NormalizeChannel(s string) (string, error) {
	c := strings.ToLower(strings.TrimSpace(s))
	switch {
	case c == "":
		return "", apperr.Invalid("a channel needs a name, like #staff")
	case !strings.HasPrefix(c, "#"):
		return "", apperr.Invalid("“" + s + "” isn't a channel: it starts with #, like #staff")
	case len(c) == 1:
		return "", apperr.Invalid("a channel needs a name after the #")
	case len(c) > MaxChannelLen:
		return "", apperr.Invalid("“" + s + "” is too long: a channel name is at most 64 characters")
	case !utf8.ValidString(c):
		return "", apperr.Invalid("“" + s + "” isn't valid text")
	case strings.IndexFunc(c, func(r rune) bool { return r == ',' || unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0:
		return "", apperr.Invalid("“" + s + "” can't contain spaces, commas or control characters")
	}
	return c, nil
}

func cleanRoleChannels(in []authapi.RoleChannel) ([]authapi.RoleChannel, error) {
	if len(in) > MaxRoleChannels {
		return nil, apperr.Invalid("a role lists at most 100 channels")
	}
	out := make([]authapi.RoleChannel, 0, len(in))
	for _, c := range in {
		name, err := NormalizeChannel(c.Channel)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(out, func(o authapi.RoleChannel) bool { return o.Channel == name }) {
			return nil, apperr.Invalid(name + " is listed twice")
		}
		out = append(out, authapi.RoleChannel{Channel: name, Autojoin: c.Autojoin})
	}
	slices.SortFunc(out, func(a, b authapi.RoleChannel) int { return strings.Compare(a.Channel, b.Channel) })
	return out, nil
}

func cleanChannelAccess(in []authapi.ChannelAccess) ([]authapi.ChannelAccess, error) {
	if len(in) > MaxRoleChannels*10 {
		return nil, apperr.Invalid("too many channels")
	}
	out := make([]authapi.ChannelAccess, 0, len(in))
	for _, c := range in {
		name, err := NormalizeChannel(c.Channel)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(out, func(o authapi.ChannelAccess) bool { return o.Channel == name }) {
			return nil, apperr.Invalid(name + " is listed twice")
		}
		out = append(out, authapi.ChannelAccess{Channel: name, Private: c.Private})
	}
	slices.SortFunc(out, func(a, b authapi.ChannelAccess) int { return strings.Compare(a.Channel, b.Channel) })
	return out, nil
}

// channelRole: the panel names the Member role's channels "everyone", as auth does.
func (s *AdminService) channelRole(ctx context.Context, sess *session.Session, id string) string {
	if id == authapi.EveryoneRole {
		return id
	}
	if r, err := s.GetRole(ctx, sess, id); err == nil && r.IsBuiltin(authapi.BuiltinMember) {
		return authapi.EveryoneRole
	}
	return id
}

func (s *AdminService) RoleChannels(ctx context.Context, sess *session.Session, roleID string) ([]authapi.RoleChannel, error) {
	return s.roleChannels(ctx, sess, s.channelRole(ctx, sess, roleID))
}

func (s *AdminService) roleChannels(ctx context.Context, sess *session.Session, id string) ([]authapi.RoleChannel, error) {
	var out []authapi.RoleChannel
	err := s.call(ctx, sess, func(at string) (err error) { out, err = s.Client.RoleChannels(ctx, at, id); return })
	return out, rolesFriendly(err)
}

func (s *AdminService) SetRoleChannels(ctx context.Context, sess *session.Session, roleID string, list []authapi.RoleChannel) ([]authapi.RoleChannel, error) {
	clean, err := cleanRoleChannels(list)
	if err != nil {
		return nil, err
	}
	id := s.channelRole(ctx, sess, roleID)
	before, _ := s.roleChannels(ctx, sess, id)
	var out []authapi.RoleChannel
	err = s.call(ctx, sess, func(at string) (err error) { out, err = s.Client.SetRoleChannels(ctx, at, id, clean); return })
	logChange(ctx, sess, "roles.channels.update", err, "role", id, roleChannelChanges(before, clean))
	return out, rolesFriendly(err)
}

func (s *AdminService) ChannelAccess(ctx context.Context, sess *session.Session) ([]authapi.ChannelAccess, error) {
	var out []authapi.ChannelAccess
	err := s.call(ctx, sess, func(at string) (err error) { out, err = s.Client.ChannelAccessList(ctx, at); return })
	return out, rolesFriendly(err)
}

func (s *AdminService) SetChannelAccess(ctx context.Context, sess *session.Session, list []authapi.ChannelAccess) ([]authapi.ChannelAccess, error) {
	clean, err := cleanChannelAccess(list)
	if err != nil {
		return nil, err
	}
	before, _ := s.ChannelAccess(ctx, sess)
	var out []authapi.ChannelAccess
	err = s.call(ctx, sess, func(at string) (err error) { out, err = s.Client.SetChannelAccess(ctx, at, clean); return })
	logChange(ctx, sess, "channel_access.update", err, "", "", accessChanges(before, clean))
	return out, rolesFriendly(err)
}

// logChange writes the panel's own record of a role-channel write (auth
// audits it too, as role_channels_updated / channel_access_updated).
func logChange(ctx context.Context, sess *session.Session, action string, err error, k, v string, changes []string) {
	ev := logger.FromCtx(ctx).Info()
	outcome := "success"
	if err != nil {
		ev, outcome = logger.FromCtx(ctx).Warn().Err(err), "error"
	}
	if k != "" {
		ev = ev.Str(k, v)
	}
	ev.Str("actor", sess.Snapshot().Username).Str("action", action).Str("outcome", outcome).
		Strs("changes", changes).Msg("role channels change")
}

func roleChannelChanges(before, after []authapi.RoleChannel) []string {
	var out []string
	find := func(l []authapi.RoleChannel, name string) (authapi.RoleChannel, bool) {
		i := slices.IndexFunc(l, func(c authapi.RoleChannel) bool { return c.Channel == name })
		if i < 0 {
			return authapi.RoleChannel{}, false
		}
		return l[i], true
	}
	for _, a := range after {
		b, ok := find(before, a.Channel)
		switch {
		case !ok:
			out = append(out, "+"+a.Channel+autojoinWord(a.Autojoin))
		case b.Autojoin != a.Autojoin:
			out = append(out, a.Channel+autojoinWord(b.Autojoin)+" →"+autojoinWord(a.Autojoin))
		}
	}
	for _, b := range before {
		if _, ok := find(after, b.Channel); !ok {
			out = append(out, "-"+b.Channel)
		}
	}
	return out
}

func autojoinWord(on bool) string {
	if on {
		return " (auto-join)"
	}
	return " (no auto-join)"
}

func accessChanges(before, after []authapi.ChannelAccess) []string {
	word := func(p bool) string {
		if p {
			return "private"
		}
		return "public"
	}
	was := map[string]bool{}
	for _, b := range before {
		was[b.Channel] = b.Private
	}
	var out []string
	for _, a := range after {
		if p, ok := was[a.Channel]; !ok || p != a.Private {
			out = append(out, a.Channel+": "+word(ok && p)+" → "+word(a.Private))
		}
		delete(was, a.Channel)
	}
	for _, b := range before {
		if _, ok := was[b.Channel]; ok && b.Private {
			out = append(out, b.Channel+": private → public")
		}
	}
	return out
}

// ChannelRole is one role (or everyone) on the channels overview.
type ChannelRole struct {
	ID       string                `json:"id"`
	Name     string                `json:"name"`
	Color    *string               `json:"color"`
	Position int                   `json:"position"`
	Everyone bool                  `json:"everyone,omitempty"`
	Channels []authapi.RoleChannel `json:"channels"`
}

// ChannelGrant is a role that lists a channel.
type ChannelGrant struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Color    *string `json:"color"`
	Autojoin bool    `json:"autojoin"`
}

// ChannelEntry is one channel on the overview: listed by a role or private.
type ChannelEntry struct {
	Channel string         `json:"channel"`
	Private bool           `json:"private"`
	Roles   []ChannelGrant `json:"roles"`
}

// ChannelOverview is GET /api/channels: every role's channels and every
// channel's access, joined up.
type ChannelOverview struct {
	Roles    []ChannelRole  `json:"roles"`
	Channels []ChannelEntry `json:"channels"`
}

func (s *AdminService) ChannelOverview(ctx context.Context, sess *session.Session) (*ChannelOverview, error) {
	roles, err := s.ListRoles(ctx, sess)
	if err != nil {
		return nil, err
	}
	access, err := s.ChannelAccess(ctx, sess)
	if err != nil {
		return nil, err
	}
	ov := &ChannelOverview{Roles: []ChannelRole{}, Channels: []ChannelEntry{}}
	for _, r := range roles {
		cr := ChannelRole{ID: r.ID, Name: r.Name, Color: r.Color, Position: r.Position}
		id := r.ID
		if r.IsBuiltin(authapi.BuiltinMember) {
			cr.ID, cr.Name, cr.Everyone, id = authapi.EveryoneRole, "Everyone", true, authapi.EveryoneRole
		}
		if cr.Channels, err = s.roleChannels(ctx, sess, id); err != nil {
			return nil, err
		}
		ov.Roles = append(ov.Roles, cr)
	}
	idx := map[string]int{}
	entry := func(name string) *ChannelEntry {
		i, ok := idx[name]
		if !ok {
			i = len(ov.Channels)
			idx[name] = i
			ov.Channels = append(ov.Channels, ChannelEntry{Channel: name, Roles: []ChannelGrant{}})
		}
		return &ov.Channels[i]
	}
	for _, a := range access {
		entry(a.Channel).Private = a.Private
	}
	for _, r := range ov.Roles {
		for _, c := range r.Channels {
			e := entry(c.Channel)
			e.Roles = append(e.Roles, ChannelGrant{ID: r.ID, Name: r.Name, Color: r.Color, Autojoin: c.Autojoin})
		}
	}
	slices.SortFunc(ov.Channels, func(a, b ChannelEntry) int { return strings.Compare(a.Channel, b.Channel) })
	return ov, nil
}
