package directory

import (
	"context"
	"net/http"
	"strings"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/bosonapi"
	"github.com/boson-chat/admin/internal/logger"
)

// The stack's own directory (the stack's `directory` profile,
// DIRECTORY_LOCAL_URL), reached over the compose network with the
// operator token DIRECTORY_INTERNAL_TOKEN:
//
//   - its runtime settings (directory >= 1.4.0): name, operator, contact,
//     whether it includes the boson main directory (directory.boson.chat
//     by default; on, the stack's directory also shows every network listed
//     there, each re-verified against its own DNS). The directory's env
//     values are only defaults; changes made here are stored in its
//     database and apply at once, so nobody edits .env;
//   - its peers: add (checked first), enable/disable, remove, sync now;
//   - which networks a user of this stack's auth service owns there.

type LocalOptions struct {
	Client bosonapi.OperatorClientImpl
	// URL is shown on the page (http://directory:8080 in the stack).
	URL string
}

// Federation is GET /api/directory/federation.
type Federation struct {
	URL string `json:"url"`
	// MainPeer is the built-in main directory peer; nil when the directory
	// has none (include_main off, or it is the main directory itself).
	MainPeer *bosonapi.Peer  `json:"main_peer,omitempty"`
	Peers    []bosonapi.Peer `json:"peers"`
	// Settings are the directory's runtime settings; nil on directories
	// before 1.4.0 (SettingsUnsupported says so) or when they failed to load.
	Settings            *bosonapi.DirectorySettings `json:"settings,omitempty"`
	SettingsUnsupported string                      `json:"settings_unsupported,omitempty"`
	SettingsError       string                      `json:"settings_error,omitempty"`
	Error               string                      `json:"error,omitempty"`
	Unsupported         string                      `json:"unsupported,omitempty"`
}

// SettingsUpdate is PATCH /api/directory/settings. Nil fields stay.
type SettingsUpdate struct {
	Name     *string `json:"name"`
	Operator *string `json:"operator"`
	Contact  *string `json:"contact"`
	// SyncEvery is a duration such as 10m or 1h.
	SyncEvery *string `json:"sync_every"`
}

type LocalServiceImpl interface {
	Federation(ctx context.Context) *Federation
	SetIncludeMain(ctx context.Context, actor string, on bool) (*Federation, error)
	UpdateSettings(ctx context.Context, actor string, u SettingsUpdate) (*Federation, error)
	CheckPeer(ctx context.Context, rawURL string) (*bosonapi.PeerCheck, error)
	AddPeer(ctx context.Context, actor, rawURL string) (*Federation, error)
	SetPeerEnabled(ctx context.Context, actor, id string, on bool) (*Federation, error)
	RemovePeer(ctx context.Context, actor, id string) (*Federation, error)
	SyncPeer(ctx context.Context, actor, id string) (*Federation, *bosonapi.SyncResult, error)
	OwnedNetworks(ctx context.Context, authUserID string) ([]bosonapi.Server, error)
}

type LocalService struct{ o LocalOptions }

func NewLocalService(o LocalOptions) LocalServiceImpl { return &LocalService{o: o} }

// NeedsSettings is shown for directories without the settings API.
const NeedsSettings = "needs directory 1.4.0 or newer"

func (l *LocalService) Federation(ctx context.Context) *Federation {
	f := &Federation{URL: l.o.URL, Peers: []bosonapi.Peer{}}
	peers, err := l.o.Client.Peers(ctx)
	switch {
	case apperr.IsUnsupported(err):
		f.Unsupported = errMsg(err)
		return f
	case err != nil:
		f.Error = errMsg(err)
		return f
	}
	f.Peers = peers
	for i := range peers {
		if peers[i].Builtin {
			f.MainPeer = &peers[i]
			break
		}
	}
	st, err := l.o.Client.Settings(ctx)
	switch {
	case apperr.IsUnsupported(err):
		f.SettingsUnsupported = errMsg(err)
	case err != nil:
		f.SettingsError = errMsg(err)
	default:
		f.Settings = st
	}
	return f
}

func (l *LocalService) usable(ctx context.Context) (*Federation, error) {
	f := l.Federation(ctx)
	if f.Unsupported != "" {
		return nil, apperr.Unsupported(f.Unsupported)
	}
	if f.Error != "" {
		return nil, apperr.New(http.StatusBadGateway, "directory_unavailable", f.Error)
	}
	return f, nil
}

var (
	errNoMainPeer = apperr.New(http.StatusConflict, "main_peer_missing",
		"the stack's directory has no built-in main peer, and switching it on from here "+NeedsSettings+
			" (older releases: set DIRECTORY_INCLUDE_MAIN=true and recreate the directory service)")
	errIsMain = apperr.New(http.StatusConflict, "is_main_directory",
		"this is the boson main directory itself; other directories include it, so there is nothing to include")
	errNoSettings = apperr.Unsupported("changing the directory's settings " + NeedsSettings)
)

func (l *LocalService) logAction(ctx context.Context, actor, action string, err error, kv ...string) {
	ev := logger.FromCtx(ctx).Info()
	if err != nil {
		ev = logger.FromCtx(ctx).Warn().Err(err)
	}
	ev = ev.Str("actor", actor).Str("action", "directory."+action)
	for i := 0; i+1 < len(kv); i += 2 {
		ev = ev.Str(kv[i], kv[i+1])
	}
	ev.Msg("directory action")
}

// SetIncludeMain switches the built-in main peer on or off: through the
// directory's include_main setting (>= 1.4.0, works whether or not the
// peer exists yet), else by enabling/disabling the existing builtin peer.
// It is logged here and, through X-Acting-User, in the directory's own
// audit log.
func (l *LocalService) SetIncludeMain(ctx context.Context, actor string, on bool) (*Federation, error) {
	f, err := l.usable(ctx)
	if err != nil {
		return nil, err
	}
	onS := "false"
	if on {
		onS = "true"
	}
	if f.Settings != nil {
		if f.Settings.IsMain {
			return nil, errIsMain
		}
		_, err := l.o.Client.UpdateSettings(ctx, actor, map[string]any{"include_main": on})
		l.logAction(ctx, actor, "include_main", err, "enabled", onS)
		if err != nil {
			return nil, err
		}
		return l.Federation(ctx), nil
	}
	if f.MainPeer == nil {
		return nil, errNoMainPeer
	}
	p, err := l.o.Client.SetPeerEnabled(ctx, actor, f.MainPeer.ID, on)
	l.logAction(ctx, actor, "include_main", err, "enabled", onS, "peer", f.MainPeer.URL)
	if err != nil {
		return nil, err
	}
	for i := range f.Peers {
		if f.Peers[i].ID == p.ID {
			f.Peers[i] = *p
			f.MainPeer = &f.Peers[i]
		}
	}
	return f, nil
}

func (l *LocalService) UpdateSettings(ctx context.Context, actor string, u SettingsUpdate) (*Federation, error) {
	patch := map[string]any{}
	set := func(k string, v *string) {
		if v != nil {
			patch[k] = strings.TrimSpace(*v)
		}
	}
	set("name", u.Name)
	set("operator", u.Operator)
	set("contact", u.Contact)
	set("sync_every", u.SyncEvery)
	if len(patch) == 0 {
		return nil, apperr.Invalid("nothing to change")
	}
	if n, ok := patch["name"].(string); ok && n == "" {
		return nil, apperr.Invalid("the directory needs a name")
	}
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	_, err := l.o.Client.UpdateSettings(ctx, actor, patch)
	l.logAction(ctx, actor, "settings", err, "fields", strings.Join(keys, ","))
	if apperr.IsUnsupported(err) {
		return nil, errNoSettings
	}
	if err != nil {
		return nil, err
	}
	return l.Federation(ctx), nil
}

// peerURL normalises what an operator typed (a bare host gets https://).
func peerURL(raw string) (string, error) {
	u := strings.TrimRight(strings.TrimSpace(raw), "/")
	if u == "" {
		return "", apperr.Invalid("enter the directory's address, like https://directory.example.org")
	}
	if !strings.Contains(u, "://") {
		u = "https://" + u
	}
	return u, nil
}

func (l *LocalService) CheckPeer(ctx context.Context, rawURL string) (*bosonapi.PeerCheck, error) {
	u, err := peerURL(rawURL)
	if err != nil {
		return nil, err
	}
	return l.o.Client.CheckPeer(ctx, u)
}

func (l *LocalService) AddPeer(ctx context.Context, actor, rawURL string) (*Federation, error) {
	u, err := peerURL(rawURL)
	if err != nil {
		return nil, err
	}
	_, err = l.o.Client.AddPeer(ctx, actor, u)
	l.logAction(ctx, actor, "peer_add", err, "url", u)
	if err != nil {
		return nil, err
	}
	return l.Federation(ctx), nil
}

func (l *LocalService) SetPeerEnabled(ctx context.Context, actor, id string, on bool) (*Federation, error) {
	_, err := l.o.Client.SetPeerEnabled(ctx, actor, id, on)
	onS := "false"
	if on {
		onS = "true"
	}
	l.logAction(ctx, actor, "peer_enable", err, "peer", id, "enabled", onS)
	if err != nil {
		return nil, err
	}
	return l.Federation(ctx), nil
}

func (l *LocalService) RemovePeer(ctx context.Context, actor, id string) (*Federation, error) {
	err := l.o.Client.DeletePeer(ctx, actor, id)
	l.logAction(ctx, actor, "peer_remove", err, "peer", id)
	if err != nil {
		return nil, err
	}
	return l.Federation(ctx), nil
}

func (l *LocalService) SyncPeer(ctx context.Context, actor, id string) (*Federation, *bosonapi.SyncResult, error) {
	_, res, err := l.o.Client.SyncPeer(ctx, actor, id)
	l.logAction(ctx, actor, "peer_sync", err, "peer", id)
	if err != nil {
		return nil, nil, err
	}
	return l.Federation(ctx), res, nil
}

func (l *LocalService) OwnedNetworks(ctx context.Context, authUserID string) ([]bosonapi.Server, error) {
	return l.o.Client.OwnedNetworks(ctx, authUserID)
}
