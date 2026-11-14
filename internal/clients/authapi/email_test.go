package authapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
)

func TestEmailClient(t *testing.T) {
	var got http.Header
	var body map[string]any
	mode := "ok"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch mode {
		case "old":
			http.NotFound(w, r)
		case "unauth":
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
		case "limited":
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":"rate_limited"}`))
		case "bad":
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"invalid_request","message":"unknown template"}`))
		default:
			if r.URL.Path == "/internal/email/preview" {
				_, _ = w.Write([]byte(`{"subject":"Confirm","html":"<p>x</p>","text":"x"}`))
				return
			}
			w.WriteHeader(202)
		}
	}))
	defer srv.Close()
	c := NewEmailClient(srv.URL+"/", "tok", time.Second, nil)
	ctx := context.Background()
	s := "Hi"
	p, err := c.Preview(ctx, "at-1", EmailPreviewRequest{Template: "invite", Subject: &s})
	if err != nil || p.Subject != "Confirm" || p.HTML != "<p>x</p>" {
		t.Fatalf("%v %+v", err, p)
	}
	if got.Get("Authorization") != "Bearer at-1" || got.Get("X-Internal-Token") != "tok" ||
		body["template"] != "invite" || body["subject"] != "Hi" || body["body"] != nil {
		t.Fatalf("%v %v", got, body)
	}
	r, err := c.SendTest(ctx, "root", EmailTestRequest{Template: "invite", To: "a@example.com"})
	if err != nil || !r.Sent || body["to"] != "a@example.com" {
		t.Fatalf("%v %+v", err, r)
	}
	mode = "old"
	if _, err := c.Preview(ctx, "root", EmailPreviewRequest{Template: "invite"}); !apperr.IsUnsupported(err) {
		t.Fatalf("old auth: %v", err)
	}
	mode = "limited"
	if _, err := c.SendTest(ctx, "root", EmailTestRequest{Template: "invite", To: "a@example.com"}); err == nil {
		t.Fatal("429")
	} else if e, _ := apperr.As(err); e.Status != 429 || e.Message != "too many test emails; try again in 30 s" {
		t.Fatalf("429: %v", err)
	}
	mode = "unauth"
	if _, err := c.Preview(ctx, "old-token", EmailPreviewRequest{Template: "invite"}); !IsUnauthorized(err) {
		t.Fatalf("401 must read as unauthorized (refresh + retry): %v", err)
	}
	mode = "bad"
	if _, err := c.Preview(ctx, "root", EmailPreviewRequest{Template: "x"}); err == nil {
		t.Fatal("400")
	} else if e, _ := apperr.As(err); e.Status != 400 || e.Message != "unknown template" {
		t.Fatalf("400: %v", err)
	}
}

func TestBrandingDocument(t *testing.T) {
	mode := "ok"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/branding" {
			http.NotFound(w, r)
			return
		}
		switch mode {
		case "none":
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":"no_branding","message":"no branding"}`))
		case "old":
			http.NotFound(w, r)
		default:
			_, _ = w.Write([]byte(`{"version":1,"base":"dark","logo":"https://auth.example.com/v1/branding/assets/logo.svg"}`))
		}
	}))
	defer srv.Close()
	c := NewBrandingClient(srv.URL, time.Second, nil)
	doc, ok, err := c.Document(context.Background())
	if err != nil || !ok || doc["base"] != "dark" {
		t.Fatalf("%v %v %v", err, ok, doc)
	}
	mode = "none"
	if doc, ok, err := c.Document(context.Background()); doc != nil || ok || err != nil {
		t.Fatalf("none: %v %v %v", doc, ok, err)
	}
	mode = "old"
	if _, _, err := c.Document(context.Background()); !apperr.IsUnsupported(err) {
		t.Fatalf("old: %v", err)
	}
}
