package authapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
)

// BrandingEndpoint is the auth service's public theme document
// (auth >= 1.10.0): boson-theme/1 with absolute image URLs on its public
// host, or 404 no_branding when the stack config file has no branding.
const BrandingEndpoint = "GET /v1/branding (auth 1.10.0 or newer)"

type BrandingClientImpl interface {
	// Document is GET /v1/branding as JSON; ok is false when the network
	// has no branding.
	Document(ctx context.Context) (doc map[string]any, ok bool, err error)
}

type BrandingClient struct {
	BaseURL string
	HTTP    *http.Client
	Observe Observer
}

func NewBrandingClient(baseURL string, timeout time.Duration, observe Observer) BrandingClientImpl {
	return &BrandingClient{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: timeout}, Observe: observe}
}

func (c *BrandingClient) Document(ctx context.Context) (map[string]any, bool, error) {
	start := time.Now()
	doc, ok, status, err := c.get(ctx)
	if c.Observe != nil {
		c.Observe("branding.document", status, err, time.Since(start))
	}
	return doc, ok, err
}

func (c *BrandingClient) get(ctx context.Context) (map[string]any, bool, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/branding", nil)
	if err != nil {
		return nil, false, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "boson-admin")
	res, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, 0, ctx.Err()
		}
		return nil, false, 0, apperr.ErrUpstream
	}
	defer func() { _ = res.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode == http.StatusNotFound {
		var eb errorBody
		_ = json.Unmarshal(data, &eb)
		if eb.Error == "no_branding" || eb.Code == "no_branding" {
			return nil, false, res.StatusCode, nil
		}
		return nil, false, res.StatusCode, apperr.Unsupported("the auth service doesn't serve " + BrandingEndpoint)
	}
	if res.StatusCode != http.StatusOK {
		return nil, false, res.StatusCode, apperr.New(http.StatusBadGateway, "upstream_error", "the auth service couldn't serve the branding document")
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil || doc == nil {
		return nil, false, res.StatusCode, apperr.New(http.StatusBadGateway, "upstream_error", "the auth service sent a branding document the panel can't read")
	}
	return doc, true, res.StatusCode, nil
}
