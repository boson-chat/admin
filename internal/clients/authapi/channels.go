package authapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// RequiresRoleChannels is the auth release with role channels and channel
// access (the role channels + auto-join contract).
const RequiresRoleChannels = "v1.16.0"

const roleChannelsWhat = "Role channels"

// EveryoneRole is the role ID auth takes for channels everyone gets (the
// built-in Member role).
const EveryoneRole = "everyone"

// RoleChannel is one channel a role (or everyone) lists. Channel is the
// casefolded #name.
type RoleChannel struct {
	Channel  string `json:"channel"`
	Autojoin bool   `json:"autojoin"`
}

// ChannelAccess marks a channel private (joinable by the roles that list
// it, plus opers) or public.
type ChannelAccess struct {
	Channel string `json:"channel"`
	Private bool   `json:"private"`
}

// channelList reads {"channels": [...]} or a bare array.
func channelList[T any](raw json.RawMessage) ([]T, error) {
	out := []T{}
	if len(raw) == 0 || string(raw) == "null" {
		return out, nil
	}
	if raw[0] == '[' {
		err := json.Unmarshal(raw, &out)
		return out, err
	}
	var env struct {
		Channels []T `json:"channels"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	if env.Channels != nil {
		out = env.Channels
	}
	return out, nil
}

func (c *AuthClient) channels(ctx context.Context, op, method, path, at string, body any, decode func(json.RawMessage) error) error {
	var raw json.RawMessage
	if err := c.do(ctx, op, method, path, at, body, &raw); err != nil {
		return feature(err, roleChannelsWhat, RequiresRoleChannels)
	}
	if err := decode(raw); err != nil {
		return feature(err, roleChannelsWhat, RequiresRoleChannels)
	}
	return nil
}

func roleChannelsPath(id string) string { return "/v1/admin/roles/" + url.PathEscape(id) + "/channels" }

// RoleChannels is GET /v1/admin/roles/{id}/channels (id may be "everyone").
func (c *AuthClient) RoleChannels(ctx context.Context, at, roleID string) ([]RoleChannel, error) {
	var out []RoleChannel
	err := c.channels(ctx, "admin.roles.channels.get", http.MethodGet, roleChannelsPath(roleID), at, nil,
		func(raw json.RawMessage) (err error) { out, err = channelList[RoleChannel](raw); return })
	return out, err
}

// SetRoleChannels is PUT /v1/admin/roles/{id}/channels: the whole list.
func (c *AuthClient) SetRoleChannels(ctx context.Context, at, roleID string, list []RoleChannel) ([]RoleChannel, error) {
	if list == nil {
		list = []RoleChannel{}
	}
	var out []RoleChannel
	err := c.channels(ctx, "admin.roles.channels.set", http.MethodPut, roleChannelsPath(roleID), at,
		map[string]any{"channels": list},
		func(raw json.RawMessage) (err error) { out, err = channelList[RoleChannel](raw); return })
	if err == nil && len(out) == 0 && len(list) > 0 {
		out = list // a 204 or an empty echo: the list as sent
	}
	return out, err
}

// ChannelAccessList is GET /v1/admin/channel-access.
func (c *AuthClient) ChannelAccessList(ctx context.Context, at string) ([]ChannelAccess, error) {
	var out []ChannelAccess
	err := c.channels(ctx, "admin.channel_access.get", http.MethodGet, "/v1/admin/channel-access", at, nil,
		func(raw json.RawMessage) (err error) { out, err = channelList[ChannelAccess](raw); return })
	return out, err
}

// SetChannelAccess is PUT /v1/admin/channel-access: the whole list.
func (c *AuthClient) SetChannelAccess(ctx context.Context, at string, list []ChannelAccess) ([]ChannelAccess, error) {
	if list == nil {
		list = []ChannelAccess{}
	}
	var out []ChannelAccess
	err := c.channels(ctx, "admin.channel_access.set", http.MethodPut, "/v1/admin/channel-access", at,
		map[string]any{"channels": list},
		func(raw json.RawMessage) (err error) { out, err = channelList[ChannelAccess](raw); return })
	if err == nil && len(out) == 0 && len(list) > 0 {
		out = list
	}
	return out, err
}
