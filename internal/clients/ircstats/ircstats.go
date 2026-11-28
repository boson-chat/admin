// Package ircstats asks the IRC side how big the network is, without
// connecting to IRC: Ergo's HTTP API (the irc profile) and the bridge's
// (evrd) managed API (the bridge profile). Counts only.
package ircstats

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Counts is the network's size now; nil = unknown.
type Counts struct {
	Users    *int
	Channels *int // registered channels (Ergo); channels formed (bridge)
}

// Ergo is Ergo's HTTP API (api.enabled, a bearer token).
type Ergo struct {
	URL   string
	Token string
	HTTP  *http.Client
}

// NewErgo is an Ergo client.
func NewErgo(url, token string, timeout time.Duration) *Ergo {
	return &Ergo{URL: url, Token: token, HTTP: &http.Client{Timeout: timeout}}
}

// Counts is POST /v1/status: users.total, and registered_channels (the
// stack's patched Ergo; nil on an image without it).
func (e *Ergo) Counts(ctx context.Context) (*Counts, error) {
	var st struct {
		Success bool `json:"success"`
		Users   *struct {
			Total *int `json:"total"`
		} `json:"users"`
		RegisteredChannels *int `json:"registered_channels"`
	}
	if err := call(ctx, e.HTTP, http.MethodPost, e.URL+"/v1/status", e.Token, []byte("{}"), &st); err != nil {
		return nil, err
	}
	if !st.Success {
		return nil, fmt.Errorf("ergo /v1/status: not successful")
	}
	c := &Counts{Channels: nonNeg(st.RegisteredChannels)}
	if st.Users != nil {
		c.Users = nonNeg(st.Users.Total)
	}
	return c, nil
}

// Bridge is evrd's managed API (managed_token as a bearer token).
type Bridge struct {
	URL   string
	Token string
	HTTP  *http.Client
}

// NewBridge is a Bridge client.
func NewBridge(url, token string, timeout time.Duration) *Bridge {
	return &Bridge{URL: url, Token: token, HTTP: &http.Client{Timeout: timeout}}
}

// Counts is GET /api/managed/stats: the network's users and channels from
// LUSERS (null when evrd hasn't heard them). An evrd without the endpoint
// is an error.
func (b *Bridge) Counts(ctx context.Context) (*Counts, error) {
	var st struct {
		Users    *int `json:"users"`
		Channels *int `json:"channels"`
	}
	if err := call(ctx, b.HTTP, http.MethodGet, b.URL+"/api/managed/stats", b.Token, nil, &st); err != nil {
		return nil, err
	}
	return &Counts{Users: nonNeg(st.Users), Channels: nonNeg(st.Channels)}, nil
}

func nonNeg(n *int) *int {
	if n == nil || *n < 0 {
		return nil
	}
	return n
}

func call(ctx context.Context, hc *http.Client, method, url, token string, body []byte, out any) error {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "boson-admin")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s: HTTP %d", method, req.URL.Path, res.StatusCode)
	}
	return json.Unmarshal(data, out)
}
