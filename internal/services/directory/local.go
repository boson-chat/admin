package directory

import (
	"context"
	"net/http"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/bosonapi"
	"github.com/boson-chat/admin/internal/logger"
)

// The stack's own directory (the stack's `directory` profile,
// DIRECTORY_LOCAL_URL), reached over the compose network with the
// operator token DIRECTORY_INTERNAL_TOKEN:
//
//   - its peers, and the switch for the built-in main peer (directory.boson.chat
//     by default): on, the stack's directory also shows every network listed
//     there (each re-verified against its own DNS);
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
	// runs with DIRECTORY_INCLUDE_MAIN=false (it doesn't create it then).
	MainPeer    *bosonapi.Peer  `json:"main_peer,omitempty"`
	Peers       []bosonapi.Peer `json:"peers"`
	Error       string          `json:"error,omitempty"`
	Unsupported string          `json:"unsupported,omitempty"`
}

type LocalServiceImpl interface {
	Federation(ctx context.Context) *Federation
	SetIncludeMain(ctx context.Context, actor string, on bool) (*Federation, error)
	OwnedNetworks(ctx context.Context, authUserID string) ([]bosonapi.Server, error)
}

type LocalService struct{ o LocalOptions }

func NewLocalService(o LocalOptions) LocalServiceImpl { return &LocalService{o: o} }

func (l *LocalService) Federation(ctx context.Context) *Federation {
	f := &Federation{URL: l.o.URL, Peers: []bosonapi.Peer{}}
	peers, err := l.o.Client.Peers(ctx)
	switch {
	case apperr.IsUnsupported(err):
		f.Unsupported = errMsg(err)
	case err != nil:
		f.Error = errMsg(err)
	default:
		f.Peers = peers
		for i := range peers {
			if peers[i].Builtin {
				f.MainPeer = &peers[i]
				break
			}
		}
	}
	return f
}

var errNoMainPeer = apperr.New(http.StatusConflict, "main_peer_missing",
	"the stack's directory has no built-in main peer because it runs with DIRECTORY_INCLUDE_MAIN=false; set it to true in .env and recreate the directory service")

// SetIncludeMain switches the built-in main peer on or off. It is logged
// here and, through X-Acting-User, in the directory's own audit log.
func (l *LocalService) SetIncludeMain(ctx context.Context, actor string, on bool) (*Federation, error) {
	f := l.Federation(ctx)
	if f.Unsupported != "" {
		return nil, apperr.Unsupported(f.Unsupported)
	}
	if f.Error != "" {
		return nil, apperr.New(http.StatusBadGateway, "directory_unavailable", f.Error)
	}
	if f.MainPeer == nil {
		return nil, errNoMainPeer
	}
	p, err := l.o.Client.SetPeerEnabled(ctx, actor, f.MainPeer.ID, on)
	ev := logger.FromCtx(ctx).Info()
	if err != nil {
		ev = logger.FromCtx(ctx).Warn().Err(err)
	}
	ev.Str("actor", actor).Str("action", "directory.include_main").Bool("enabled", on).
		Str("peer", f.MainPeer.URL).Msg("directory action")
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

func (l *LocalService) OwnedNetworks(ctx context.Context, authUserID string) ([]bosonapi.Server, error) {
	return l.o.Client.OwnedNetworks(ctx, authUserID)
}
