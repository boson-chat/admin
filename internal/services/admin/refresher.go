package admin

import (
	"context"
	"crypto/sha256"
	"sync"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
)

// Refresher exchanges refresh tokens at most once each.
//
// The auth service rotates the refresh token on every use and treats a
// second use of the same token as theft: it revokes the whole session. A
// browser easily sends several requests at once with the same cookie (the
// overview polls while a table loads), and a request that was in flight
// during a refresh still carries the old cookie. So concurrent refreshes of
// one token share a single upstream call, and the result is remembered for
// a short while so late requests with the old cookie get the new tokens too.
//
// The memory is per process; with several replicas, sticky sessions keep
// the window small (see README).
type Refresher struct {
	client authapi.AuthClientImpl
	// Remember is how long a finished exchange is replayed to late callers.
	remember time.Duration
	timeout  time.Duration
	now      func() time.Time

	mu    sync.Mutex
	calls map[[32]byte]*refreshCall
}

type refreshCall struct {
	done   chan struct{}
	tokens *authapi.Tokens
	err    error
	at     time.Time
}

func NewRefresher(client authapi.AuthClientImpl, remember, timeout time.Duration, now func() time.Time) *Refresher {
	if now == nil {
		now = time.Now
	}
	return &Refresher{client: client, remember: remember, timeout: timeout, now: now, calls: map[[32]byte]*refreshCall{}}
}

// Refresh returns new tokens for refreshToken. shared is true when the
// result came from another caller's exchange.
func (r *Refresher) Refresh(ctx context.Context, refreshToken string) (tokens *authapi.Tokens, shared bool, err error) {
	key := sha256.Sum256([]byte(refreshToken))
	r.mu.Lock()
	r.pruneLocked()
	if c, ok := r.calls[key]; ok {
		r.mu.Unlock()
		select {
		case <-c.done:
			return c.tokens, true, c.err
		case <-ctx.Done():
			return nil, true, ctx.Err()
		}
	}
	c := &refreshCall{done: make(chan struct{})}
	r.calls[key] = c
	r.mu.Unlock()

	// The exchange must finish even if this caller goes away: once the auth
	// service has rotated the token, the old one is dead.
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.timeout)
	defer cancel()
	c.tokens, c.err = r.client.Refresh(cctx, refreshToken)

	r.mu.Lock()
	c.at = r.now()
	if c.err != nil && !definitive(c.err) {
		// Transport trouble: let the next request try again.
		delete(r.calls, key)
	}
	r.mu.Unlock()
	close(c.done)
	return c.tokens, false, c.err
}

// definitive reports whether the auth service gave a real answer (as
// opposed to being unreachable), which is then safe to replay.
func definitive(err error) bool {
	e, ok := apperr.As(err)
	return ok && e.Status >= 400 && e.Status < 500 && e.Status != 429
}

func (r *Refresher) pruneLocked() {
	cutoff := r.now().Add(-r.remember)
	for k, c := range r.calls {
		select {
		case <-c.done:
			if c.at.Before(cutoff) {
				delete(r.calls, k)
			}
		default:
		}
	}
}
