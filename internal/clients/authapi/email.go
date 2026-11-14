package authapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
)

// The auth service's email endpoints on its internal listener (branding
// contract, section 2): the admin panel renders a preview of a template
// and sends test emails. They're authorised like the rest of auth's
// internal API, by INTERNAL_API_TOKEN in X-Internal-Token, and name the
// panel user acting (X-Acting-Account, X-Acting-Via: admin) for auth's
// audit log; the panel checks settings.manage before calling.
//
//	POST /internal/email/preview {template, subject?, body?} → {subject, html, text}
//	POST /internal/email/test    {template, to}              → {sent?, message?}

// EmailPreviewRequest renders template; Subject and Body, when set, stand in
// for the saved wording (unsaved edits).
type EmailPreviewRequest struct {
	Template string  `json:"template"`
	Subject  *string `json:"subject,omitempty"`
	Body     *string `json:"body,omitempty"`
}

// EmailPreview is the rendered email.
type EmailPreview struct {
	Subject string `json:"subject"`
	HTML    string `json:"html"`
	Text    string `json:"text"`
}

// EmailTestRequest sends template to To.
type EmailTestRequest struct {
	Template string `json:"template"`
	To       string `json:"to"`
}

// EmailTestResult is auth's answer to a test send.
type EmailTestResult struct {
	Sent    bool   `json:"sent"`
	Message string `json:"message,omitempty"`
}

type EmailClientImpl interface {
	Preview(ctx context.Context, actor string, in EmailPreviewRequest) (*EmailPreview, error)
	SendTest(ctx context.Context, actor string, in EmailTestRequest) (*EmailTestResult, error)
}

// EmailEndpoints is what the Emails page needs from auth.
const EmailEndpoints = "POST /internal/email/preview and /internal/email/test on its internal listener"

// EmailClient calls auth's internal listener (AUTH_INTERNAL_URL).
type EmailClient struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
	Observe Observer
}

func NewEmailClient(baseURL, token string, timeout time.Duration, observe Observer) EmailClientImpl {
	return &EmailClient{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: &http.Client{Timeout: timeout}, Observe: observe}
}

func (c *EmailClient) Preview(ctx context.Context, actor string, in EmailPreviewRequest) (*EmailPreview, error) {
	var out EmailPreview
	if err := c.post(ctx, "email.preview", "/internal/email/preview", actor, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *EmailClient) SendTest(ctx context.Context, actor string, in EmailTestRequest) (*EmailTestResult, error) {
	var out EmailTestResult
	if err := c.post(ctx, "email.test", "/internal/email/test", actor, in, &out); err != nil {
		return nil, err
	}
	if !out.Sent && out.Message == "" {
		out.Sent = true // a 2xx without a body: sent
	}
	return &out, nil
}

func (c *EmailClient) post(ctx context.Context, op, path, actor string, body, out any) error {
	start := time.Now()
	status, err := c.roundTrip(ctx, path, actor, body, out)
	if c.Observe != nil {
		c.Observe(op, status, err, time.Since(start))
	}
	return err
}

func (c *EmailClient) roundTrip(ctx context.Context, path, actor string, body, out any) (int, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "boson-admin")
	req.Header.Set("X-Internal-Token", c.Token)
	req.Header.Set("X-Acting-Via", "admin")
	if actor != "" {
		req.Header.Set("X-Acting-Account", actor)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, apperr.New(http.StatusBadGateway, "upstream_unavailable", "the auth service's internal listener is unreachable")
	}
	defer func() { _ = res.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(res.Body, maxResponse))
	if res.StatusCode >= 400 {
		return res.StatusCode, emailError(res, data)
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return res.StatusCode, apperr.New(http.StatusBadGateway, "upstream_error", "the auth service sent an email response the panel can't read")
		}
	}
	return res.StatusCode, nil
}

func emailError(res *http.Response, data []byte) error {
	var eb errorBody
	_ = json.Unmarshal(data, &eb)
	code := eb.Error
	if code == "" {
		code = eb.Code
	}
	switch {
	case (res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusMethodNotAllowed) && (code == "" || code == "not_found"):
		return apperr.Unsupported("this auth release doesn't have " + EmailEndpoints)
	case res.StatusCode == http.StatusServiceUnavailable && code == "internal_api_disabled":
		return apperr.New(http.StatusServiceUnavailable, code, "the auth service's internal API is off: set INTERNAL_API_TOKEN on auth and the admin panel")
	case res.StatusCode == http.StatusUnauthorized:
		return apperr.New(http.StatusBadGateway, "internal_token_rejected", "the auth service rejected the panel's INTERNAL_API_TOKEN")
	case res.StatusCode == http.StatusTooManyRequests:
		msg := "too many test emails"
		if ra := res.Header.Get("Retry-After"); ra != "" {
			msg += "; try again in " + ra + " s"
		}
		return apperr.New(http.StatusTooManyRequests, "rate_limited", msg)
	case res.StatusCode >= 500:
		msg := "the auth service couldn't handle the email request"
		if eb.Message != "" && len(eb.Message) < 200 {
			msg += ": " + eb.Message
		}
		return apperr.New(http.StatusBadGateway, "upstream_error", msg)
	}
	msg := eb.Message
	if msg == "" {
		msg = http.StatusText(res.StatusCode)
	}
	if code == "" {
		code = "upstream_" + strconv.Itoa(res.StatusCode)
	}
	return apperr.New(res.StatusCode, code, msg)
}
