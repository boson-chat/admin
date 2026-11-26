package authapitest

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
)

// Audit events the fake records for federation actions.
const (
	EventFederationSettings = "federation_settings_updated"
	EventFederationBan      = "federation_identity_banned"
	EventFederationUnban    = "federation_identity_unbanned"
	EventFederationRevoke   = "federation_domain_sessions_revoked"
)

var _ authapi.FederationClient = (*Fake)(nil)

// FederationFake is the fake's federation state. Zero value: auth without
// federation (EnableFederation turns it on).
type FederationFake struct {
	On bool
	// Status is what GET /v1/admin/federation answers (Editable = PATCH works).
	Status  authapi.FederationStatus
	Remotes []authapi.RemoteUser
	// Revoked counts revoke-sessions calls per domain.
	Revoked map[string]int
	// Managed: auth reads the stack config file (PATCH → 409
	// settings_managed) although Status says editable.
	Managed bool
}

// EnableFederation makes the fake an auth with the federation API, its
// default settings and a few remote users.
func (f *Fake) EnableFederation(editable bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	seen := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	f.Fed = FederationFake{On: true, Revoked: map[string]int{},
		Status: authapi.FederationStatus{
			FederationSettings: authapi.FederationSettings{Mode: authapi.FederationDirectory, Allow: []string{}, Block: []string{},
				DirectoryURL: "https://directory.boson.chat", RemoteVoice: true},
			Sources:  map[string]string{"mode": "default", "allow": "default", "block": "default", "directory_url": "default", "remote_upload_quota": "default", "remote_voice": "default"},
			Editable: editable, Issuer: "https://auth.example.org", Domain: "example.org", RemoteRole: "Remote",
		},
		Remotes: []authapi.RemoteUser{
			{ID: "usr_fed_alice", Identity: "alice@boson.chat", Handle: "alice", HomeDomain: "boson.chat", FirstSeenAt: seen.Add(-72 * time.Hour), LastSeenAt: &seen, Sessions: 2},
			{ID: "usr_fed_bob", Identity: "bob@chat.example.net", Handle: "bob", HomeDomain: "chat.example.net", FirstSeenAt: seen.Add(-24 * time.Hour), LastSeenAt: &seen, Sessions: 1},
			{ID: "usr_fed_alice2", Identity: "alice@chat.example.net", Handle: "alice-chatexamplenet", HomeDomain: "chat.example.net", FirstSeenAt: seen.Add(-time.Hour), Sessions: 1},
		},
	}
}

func (f *Fake) fedLocked(at string) error {
	if !f.Fed.On {
		return apperr.Unsupported("Federation needs a newer auth service (auth ≥ " + authapi.RequiresFederation + ")")
	}
	return f.adminLocked(at)
}

func (f *Fake) fedStatusLocked() *authapi.FederationStatus {
	st := f.Fed.Status
	st.Allow, st.Block = slices.Clone(st.Allow), slices.Clone(st.Block)
	st.RemoteUsers = len(f.Fed.Remotes)
	return &st
}

func (f *Fake) FederationStatus(_ context.Context, at string) (*authapi.FederationStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fedLocked(at); err != nil {
		return nil, err
	}
	return f.fedStatusLocked(), nil
}

func (f *Fake) UpdateFederation(_ context.Context, at string, p authapi.FederationPatch) (*authapi.FederationStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fedLocked(at); err != nil {
		return nil, err
	}
	if !f.Fed.Status.Editable || f.Fed.Managed {
		return nil, apperr.New(http.StatusConflict, "settings_managed", "federation settings come from the stack config file")
	}
	st := &f.Fed.Status
	set := func(name string) { st.Sources[name] = "database" }
	if p.Mode != nil {
		st.Mode = *p.Mode
		set("mode")
	}
	if p.Allow != nil {
		st.Allow = slices.Clone(*p.Allow)
		set("allow")
	}
	if p.Block != nil {
		st.Block = slices.Clone(*p.Block)
		set("block")
	}
	if p.DirectoryURL != nil {
		st.DirectoryURL = *p.DirectoryURL
		set("directory_url")
	}
	if p.RemoteUploadQuota != nil {
		st.RemoteUploadQuota = *p.RemoteUploadQuota
		set("remote_upload_quota")
	}
	if p.RemoteVoice != nil {
		st.RemoteVoice = *p.RemoteVoice
		set("remote_voice")
	}
	// null: back to the env value (the fake's env is the defaults).
	for _, n := range p.Reset {
		switch n {
		case "mode":
			st.Mode = authapi.FederationDirectory
		case "allow":
			st.Allow = []string{}
		case "block":
			st.Block = []string{}
		case "directory_url":
			st.DirectoryURL = "https://directory.boson.chat"
		case "remote_upload_quota":
			st.RemoteUploadQuota = ""
		case "remote_voice":
			st.RemoteVoice = true
		default:
			return nil, apperr.InvalidField(n, "unknown field")
		}
		st.Sources[n] = "default"
	}
	by := "admin"
	now := time.Now().UTC()
	st.UpdatedAt, st.UpdatedBy = &now, &by
	f.auditLocked(at, EventFederationSettings, map[string]any{})
	return f.fedStatusLocked(), nil
}

func (f *Fake) RemoteUsers(_ context.Context, at string, q authapi.RemoteUserQuery) (*authapi.RemoteUserPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fedLocked(at); err != nil {
		return nil, err
	}
	out := []authapi.RemoteUser{}
	for _, u := range f.Fed.Remotes {
		if q.Domain != "" && u.HomeDomain != q.Domain {
			continue
		}
		if q.Q != "" && !strings.Contains(u.Identity+" "+u.Handle, strings.ToLower(q.Q)) {
			continue
		}
		out = append(out, u)
	}
	return &authapi.RemoteUserPage{Users: out, Total: len(out)}, nil
}

func (f *Fake) remoteLocked(id string) (*authapi.RemoteUser, error) {
	i := slices.IndexFunc(f.Fed.Remotes, func(u authapi.RemoteUser) bool { return u.ID == id })
	if i < 0 {
		return nil, apperr.New(http.StatusNotFound, "not_found", "no such remote user")
	}
	return &f.Fed.Remotes[i], nil
}

func (f *Fake) BanRemoteUser(_ context.Context, at, id, reason string) (*authapi.RemoteUser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fedLocked(at); err != nil {
		return nil, err
	}
	u, err := f.remoteLocked(id)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	u.Banned, u.BannedAt, u.BanReason, u.Sessions = true, &now, reason, 0
	f.auditLocked(at, EventFederationBan, map[string]any{"identity": u.Identity, "reason": reason})
	c := *u
	return &c, nil
}

func (f *Fake) UnbanRemoteUser(_ context.Context, at, id string) (*authapi.RemoteUser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fedLocked(at); err != nil {
		return nil, err
	}
	u, err := f.remoteLocked(id)
	if err != nil {
		return nil, err
	}
	u.Banned, u.BannedAt, u.BanReason = false, nil, ""
	f.auditLocked(at, EventFederationUnban, map[string]any{"identity": u.Identity})
	c := *u
	return &c, nil
}

func (f *Fake) RevokeDomainSessions(_ context.Context, at, domain string) (*authapi.RevokedSessions, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fedLocked(at); err != nil {
		return nil, err
	}
	n := 0
	for i := range f.Fed.Remotes {
		if f.Fed.Remotes[i].HomeDomain == domain {
			n += f.Fed.Remotes[i].Sessions
			f.Fed.Remotes[i].Sessions = 0
		}
	}
	f.Fed.Revoked[domain]++
	f.auditLocked(at, EventFederationRevoke, map[string]any{"domain": domain, "revoked": n})
	return &authapi.RevokedSessions{Domain: domain, Revoked: n}, nil
}
