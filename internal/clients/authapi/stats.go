package authapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
)

// Stats are auth's account counts (GET /internal/v1/stats on its internal
// listener, auth >= the release with it): no admin token needed, only
// INTERNAL_API_TOKEN in X-Internal-Token.
type Stats struct {
	Accounts  int64 `json:"accounts"`
	Active7d  int64 `json:"active_7d"`
	Active30d int64 `json:"active_30d"`
}

// StatsClient asks auth's internal listener for Stats.
type StatsClient struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// NewStatsClient is a StatsClient for auth's internal listener.
func NewStatsClient(baseURL, token string, timeout time.Duration) *StatsClient {
	return &StatsClient{BaseURL: baseURL, Token: token, HTTP: &http.Client{Timeout: timeout}}
}

// Stats reads the account counts. An auth without the endpoint (404/405)
// is apperr.Unsupported.
func (c *StatsClient) Stats(ctx context.Context) (*Stats, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/internal/v1/stats", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "boson-admin")
	req.Header.Set("X-Internal-Token", c.Token)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	switch {
	case res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusMethodNotAllowed:
		return nil, apperr.Unsupported("this auth release doesn't have GET /internal/v1/stats")
	case res.StatusCode != http.StatusOK:
		return nil, apperr.New(http.StatusBadGateway, "upstream_error", "auth's internal stats answered "+res.Status)
	}
	var st Stats
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return &st, nil
}
