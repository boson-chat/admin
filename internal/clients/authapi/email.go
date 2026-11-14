package authapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
)

// The auth service's email endpoints on its internal listener (branding
// contract, section 2): the admin panel renders a preview of a template
// and sends test emails. They're authorised like the rest of auth's
// admin API: the signed-in admin's Bearer access token, which needs
// settings.manage (auth checks it, the panel checks it first too).
// INTERNAL_API_TOKEN, when set, goes along in X-Internal-Token.
//
//	POST /internal/email/preview {template, subject?, body?, email?, layout_html?, html?}
//	                             → {subject, html, text, from?, warnings?}
//	POST /internal/email/test    {template, to}              → {sent?, message?}
//	GET  /internal/email/default-mjml?part=layout|<template> → the MJML source

// EmailPreviewRequest renders template; Subject, Body and Email's fields,
// when set, stand in for the saved config (unsaved edits).
type EmailPreviewRequest struct {
	Template string          `json:"template"`
	Subject  *string         `json:"subject,omitempty"`
	Body     *string         `json:"body,omitempty"`
	Email    *EmailOverrides `json:"email,omitempty"`
	// LayoutHTML and HTML are compiled MJML the admin hasn't saved: the
	// layout every email is wrapped in, and this template's own override.
	LayoutHTML *string `json:"layout_html,omitempty"`
	HTML       *string `json:"html,omitempty"`
}

// EmailOverrides are unsaved auth.email.* settings for a preview. An auth
// older than the MJML layout (1.10.x) ignores them and renders the saved
// ones; it also leaves EmailPreview.From empty, which is how the panel
// tells.
type EmailOverrides struct {
	SenderName *string `json:"sender_name,omitempty"`
	Accent     *string `json:"accent,omitempty"`
	Footer     *string `json:"footer,omitempty"`
	Logo       *bool   `json:"logo,omitempty"`
}

// EmailPreview is the rendered email. From ("Name <addr>") is empty from
// an auth that doesn't take overrides.
type EmailPreview struct {
	Subject string `json:"subject"`
	HTML    string `json:"html"`
	Text    string `json:"text"`
	From    string `json:"from,omitempty"`
	// Warnings are auth's notes on the custom HTML (an unknown
	// {{placeholder}}, say).
	Warnings []Warning `json:"warnings,omitempty"`
}

// Warning is one preview warning: auth sends a string or {message}.
type Warning string

func (w *Warning) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*w = Warning(s)
		return nil
	}
	var o struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return err
	}
	*w = Warning(o.Message)
	return nil
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
	// accessToken is the signed-in admin's.
	Preview(ctx context.Context, accessToken string, in EmailPreviewRequest) (*EmailPreview, error)
	SendTest(ctx context.Context, accessToken string, in EmailTestRequest) (*EmailTestResult, error)
	// DefaultMJML is Boson's MJML source for part ("layout" or a template).
	DefaultMJML(ctx context.Context, accessToken, part string) (string, error)
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

func (c *EmailClient) Preview(ctx context.Context, accessToken string, in EmailPreviewRequest) (*EmailPreview, error) {
	var out EmailPreview
	if err := c.post(ctx, "email.preview", "/internal/email/preview", accessToken, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *EmailClient) SendTest(ctx context.Context, accessToken string, in EmailTestRequest) (*EmailTestResult, error) {
	var out EmailTestResult
	if err := c.post(ctx, "email.test", "/internal/email/test", accessToken, in, &out); err != nil {
		return nil, err
	}
	if !out.Sent && out.Message == "" {
		out.Sent = true // a 2xx without a body: sent
	}
	return &out, nil
}

// DefaultMJML takes a JSON {mjml} answer or the source as the body.
func (c *EmailClient) DefaultMJML(ctx context.Context, accessToken, part string) (string, error) {
	start := time.Now()
	status, data, ct, err := c.do(ctx, http.MethodGet, "/internal/email/default-mjml?part="+url.QueryEscape(part), accessToken, nil)
	if err == nil && ct == "application/json" {
		var out struct {
			MJML string `json:"mjml"`
		}
		if json.Unmarshal(data, &out) != nil || out.MJML == "" {
			err = apperr.New(http.StatusBadGateway, "upstream_error", "the auth service sent a default MJML answer the panel can't read")
		}
		data = []byte(out.MJML)
	}
	if c.Observe != nil {
		c.Observe("email.default_mjml", status, err, time.Since(start))
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (c *EmailClient) post(ctx context.Context, op, path, accessToken string, body, out any) error {
	start := time.Now()
	status, err := c.roundTrip(ctx, path, accessToken, body, out)
	if c.Observe != nil {
		c.Observe(op, status, err, time.Since(start))
	}
	return err
}

func (c *EmailClient) roundTrip(ctx context.Context, path, accessToken string, body, out any) (int, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	status, data, _, err := c.do(ctx, http.MethodPost, path, accessToken, b)
	if err != nil {
		return status, err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return status, apperr.New(http.StatusBadGateway, "upstream_error", "the auth service sent an email response the panel can't read")
		}
	}
	return status, nil
}

// do sends one request (body: JSON, nil for none) and returns the status,
// the body of a 2xx answer and its media type.
func (c *EmailClient) do(ctx context.Context, method, path, accessToken string, body []byte) (int, []byte, string, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return 0, nil, "", err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "boson-admin")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if c.Token != "" {
		req.Header.Set("X-Internal-Token", c.Token)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, "", ctx.Err()
		}
		return 0, nil, "", apperr.New(http.StatusBadGateway, "upstream_unavailable", "the auth service's internal listener is unreachable")
	}
	defer func() { _ = res.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(res.Body, maxResponse))
	if res.StatusCode >= 400 {
		return res.StatusCode, nil, "", emailError(res, data)
	}
	mt, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	return res.StatusCode, data, mt, nil
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
		// The access token expired: the caller refreshes and retries
		// (IsUnauthorized).
		return apperr.New(http.StatusUnauthorized, "unauthorized", "the auth service didn't accept the session")
	case res.StatusCode == http.StatusForbidden:
		return apperr.New(http.StatusForbidden, "missing_permission", "the auth service says your roles don't include settings.manage")
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
	e := apperr.New(res.StatusCode, code, msg)
	if res.StatusCode == http.StatusBadRequest && previewField(eb.Field) {
		e.Field = eb.Field
	}
	return e
}

// previewField reports whether f is a preview request field the panel
// knows (so a stray value from auth never lands on the wrong input).
func previewField(f string) bool {
	switch f {
	case "template", "subject", "body", "email.sender_name", "email.accent", "email.footer", "email.logo", "layout_html", "html":
		return true
	}
	return false
}
