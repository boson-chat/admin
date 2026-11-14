package bosonapi

import (
	"context"
	"net/http"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
)

// Nick claims: the api's internal listener (http://api:3001), authorised
// with its INTERNAL_API_TOKEN in X-Internal-Token.
//
//	GET  /internal/nick-claims/status
//	POST /internal/nick-claims/test   {"inbound": {host, port, tls, user, protocol, password}}

// NickClaimEndpoints names what the panel needs from the api.
const NickClaimEndpoints = "GET /internal/nick-claims/status and POST /internal/nick-claims/test"

// NickClaimInbound is the mailbox as the api reports it (never the password).
type NickClaimInbound struct {
	Protocol string `json:"protocol,omitempty"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	TLS      bool   `json:"tls"`
	User     string `json:"user"`
	// PasswordSet: NICK_CLAIM_POP3_PASSWORD is set in the api's env. Nil
	// when the api doesn't say.
	PasswordSet *bool `json:"password_set,omitempty"`
}

// NickClaimWorker is the POP3 worker's health.
type NickClaimWorker struct {
	Running             bool       `json:"running"`
	LastDrainAt         *time.Time `json:"last_drain_at"`
	LastOKAt            *time.Time `json:"last_ok_at"`
	LastError           string     `json:"last_error"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
}

// NickClaimStatus is GET /internal/nick-claims/status: the settings the
// api is using (file, then env, then defaults) and how the worker is doing.
type NickClaimStatus struct {
	Enabled       bool             `json:"enabled"`
	EmailDomain   string           `json:"email_domain"`
	Inbound       NickClaimInbound `json:"inbound"`
	Worker        NickClaimWorker  `json:"worker"`
	PendingClaims int64            `json:"pending_claims"`
	// ConfigError: the api rejected its settings and runs with the feature off.
	ConfigError string `json:"config_error,omitempty"`
}

// NickClaimTestInbound is the (unsaved) mailbox to test. Nil fields use
// the api's current value; the api reuses its stored password only for
// the host it already uses.
type NickClaimTestInbound struct {
	Protocol *string `json:"protocol,omitempty"`
	Host     *string `json:"host,omitempty"`
	Port     *int    `json:"port,omitempty"`
	TLS      *bool   `json:"tls,omitempty"`
	User     *string `json:"user,omitempty"`
	Password *string `json:"password,omitempty"`
}

// NickClaimTestRequest is POST /internal/nick-claims/test.
type NickClaimTestRequest struct {
	Inbound *NickClaimTestInbound `json:"inbound,omitempty"`
}

// NickClaimTestResult is the test's outcome.
type NickClaimTestResult struct {
	OK       bool   `json:"ok"`
	Message  string `json:"message"`
	Messages int    `json:"messages"`
}

type NickClaimClientImpl interface {
	Status(ctx context.Context) (*NickClaimStatus, error)
	Test(ctx context.Context, actor string, req NickClaimTestRequest) (*NickClaimTestResult, error)
}

type NickClaimClient struct {
	c, slow *Client
	token   string
}

// NickClaimTestTimeout: the api gives a mailbox 15 s to answer.
const NickClaimTestTimeout = 25 * time.Second

func NewNickClaimClient(baseURL, token string, timeout time.Duration, observe Observer) NickClaimClientImpl {
	name := "the api service"
	return &NickClaimClient{c: newClient(baseURL, name, timeout, observe),
		slow: newClient(baseURL, name, max(timeout, NickClaimTestTimeout), observe), token: token}
}

func (n *NickClaimClient) headers(actor string) map[string]string {
	h := map[string]string{"X-Internal-Token": n.token}
	if actor != "" {
		h["X-Acting-User"] = actor
	}
	return h
}

// explain turns the api's internal-listener refusals into what to fix.
func (n *NickClaimClient) explain(err error) error {
	e, ok := apperr.As(err)
	if !ok {
		return err
	}
	switch {
	case e.Code == apperr.CodeUnsupported:
		return apperr.Unsupported("nick registration needs an api release with " + NickClaimEndpoints)
	case e.Code == "boson_api_unavailable":
		return apperr.New(http.StatusBadGateway, "nick_claim_api_unreachable",
			"the api service isn't reachable from the admin panel (BOSON_API_INTERNAL_URL)")
	case e.Status == http.StatusServiceUnavailable && e.Code == "internal_api_disabled":
		return apperr.New(http.StatusServiceUnavailable, "nick_claim_api_token",
			"the api service has no internal token: set API_INTERNAL_TOKEN in .env and recreate api and admin")
	case e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden:
		return apperr.New(http.StatusBadGateway, "nick_claim_api_token",
			"the api service refused the panel's internal token: BOSON_API_INTERNAL_TOKEN on admin must match the api's INTERNAL_API_TOKEN")
	}
	return err
}

func (n *NickClaimClient) Status(ctx context.Context) (*NickClaimStatus, error) {
	var out NickClaimStatus
	if err := n.c.callH(ctx, "nick_claims.status", http.MethodGet, "/internal/nick-claims/status", "", n.headers(""), nil, &out); err != nil {
		return nil, n.explain(err)
	}
	return &out, nil
}

func (n *NickClaimClient) Test(ctx context.Context, actor string, req NickClaimTestRequest) (*NickClaimTestResult, error) {
	var out NickClaimTestResult
	if err := n.slow.callH(ctx, "nick_claims.test", http.MethodPost, "/internal/nick-claims/test", "", n.headers(actor), req, &out); err != nil {
		return nil, n.explain(err)
	}
	return &out, nil
}
