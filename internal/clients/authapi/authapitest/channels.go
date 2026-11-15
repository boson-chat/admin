package authapitest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
)

// Audit events the fake records for role channel writes.
const (
	EventRoleChannels  = "role_channels_updated"
	EventChannelAccess = "channel_access_updated"
)

func (f *Fake) channelsGate(at string) (int, bool, error) {
	if f.NoRoleChannels || !f.rolesSupported() {
		return 0, false, apperr.Unsupported("Role channels need a newer auth service (auth ≥ " + authapi.RequiresRoleChannels + ")")
	}
	top, owner, _, err := f.rolesGate(at)
	return top, owner, err
}

func (f *Fake) auditLocked(at, event string, meta map[string]any) {
	uid := f.access[at]
	b, _ := json.Marshal(meta)
	f.n++
	f.Audit = append(f.Audit, authapi.AuditEntry{ID: fmt.Sprintf("aud_%d", f.n), UserID: &uid, Event: event,
		Meta: b, At: time.Now().UTC()})
}

// RoleChannels: like auth, "everyone" is accepted; unknown roles are a 404.
func (f *Fake) RoleChannels(_ context.Context, at, roleID string) ([]authapi.RoleChannel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, _, err := f.channelsGate(at); err != nil {
		return nil, err
	}
	if roleID != authapi.EveryoneRole {
		if _, r := f.roleLocked(roleID); r == nil {
			return nil, apperr.New(http.StatusNotFound, "role_not_found", "no such role")
		}
	}
	out := slices.Clone(f.RoleChans[roleID])
	if out == nil {
		out = []authapi.RoleChannel{}
	}
	return out, nil
}

func (f *Fake) SetRoleChannels(_ context.Context, at, roleID string, list []authapi.RoleChannel) ([]authapi.RoleChannel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	top, owner, err := f.channelsGate(at)
	if err != nil {
		return nil, err
	}
	if roleID != authapi.EveryoneRole {
		_, r := f.roleLocked(roleID)
		if r == nil {
			return nil, apperr.New(http.StatusNotFound, "role_not_found", "no such role")
		}
		if !owner && !r.IsBuiltin(authapi.BuiltinMember) && r.Position >= top {
			return nil, errHierarchy
		}
	}
	if f.RoleChans == nil {
		f.RoleChans = map[string][]authapi.RoleChannel{}
	}
	f.RoleChans[roleID] = slices.Clone(list)
	f.auditLocked(at, EventRoleChannels, map[string]any{"role_id": roleID, "channels": list})
	return slices.Clone(list), nil
}

func (f *Fake) ChannelAccessList(_ context.Context, at string) ([]authapi.ChannelAccess, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, _, err := f.channelsGate(at); err != nil {
		return nil, err
	}
	out := slices.Clone(f.ChanAccess)
	if out == nil {
		out = []authapi.ChannelAccess{}
	}
	return out, nil
}

func (f *Fake) SetChannelAccess(_ context.Context, at string, list []authapi.ChannelAccess) ([]authapi.ChannelAccess, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, _, err := f.channelsGate(at); err != nil {
		return nil, err
	}
	f.ChanAccess = slices.Clone(list)
	f.auditLocked(at, EventChannelAccess, map[string]any{"channels": list})
	return slices.Clone(list), nil
}
