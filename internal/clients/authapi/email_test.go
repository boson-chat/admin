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

// The email overrides go to auth as sent; a newer auth's from comes back;
// a 400 naming a field keeps the field (an unknown one is dropped).
func TestEmailPreviewOverrides(t *testing.T) {
	var body map[string]any
	reply := `{"subject":"S","html":"<p>x</p>","text":"x","from":"Example <no-reply@example.com>"}`
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = nil
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	defer srv.Close()
	c := NewEmailClient(srv.URL, "", time.Second, nil)
	ctx := context.Background()
	name, accent, footer, logo := "Example", "#7c3aed", "", false
	p, err := c.Preview(ctx, "at", EmailPreviewRequest{Template: "invite",
		Email: &EmailOverrides{SenderName: &name, Accent: &accent, Footer: &footer, Logo: &logo}})
	if err != nil || p.From != "Example <no-reply@example.com>" {
		t.Fatalf("%v %+v", err, p)
	}
	em, _ := body["email"].(map[string]any)
	if em["sender_name"] != "Example" || em["accent"] != "#7c3aed" || em["footer"] != "" || em["logo"] != false {
		t.Fatalf("email overrides: %v", body)
	}
	if _, err := c.Preview(ctx, "at", EmailPreviewRequest{Template: "invite"}); err != nil {
		t.Fatal(err)
	} else if _, ok := body["email"]; ok {
		t.Fatalf("no overrides must send no email object: %v", body)
	}
	// auth 1.10.x: no from.
	reply = `{"subject":"S","html":"<p>x</p>","text":"x"}`
	if p, err := c.Preview(ctx, "at", EmailPreviewRequest{Template: "invite"}); err != nil || p.From != "" {
		t.Fatalf("old auth: %v %+v", err, p)
	}
	status = 400
	reply = `{"error":"invalid_request","message":"accent must be #rrggbb","field":"email.accent"}`
	_, err = c.Preview(ctx, "at", EmailPreviewRequest{Template: "invite"})
	if e, ok := apperr.As(err); !ok || e.Status != 400 || e.Field != "email.accent" || e.Message != "accent must be #rrggbb" {
		t.Fatalf("field error: %v %+v", err, e)
	}
	reply = `{"error":"invalid_request","message":"no","field":"something.else"}`
	if _, err := c.Preview(ctx, "at", EmailPreviewRequest{Template: "invite"}); err == nil {
		t.Fatal("400")
	} else if e, _ := apperr.As(err); e.Field != "" {
		t.Fatalf("unknown field kept: %q", e.Field)
	}
}

// The default MJML comes as JSON {mjml} or as the source itself; the
// unsaved HTML goes along with a preview and warnings come back.
func TestEmailDefaultMJMLAndCustomHTML(t *testing.T) {
	var got *http.Request
	var body map[string]any
	ct, reply, status := "application/json", `{"mjml":"<mjml>a</mjml>"}`, 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		body = nil
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	defer srv.Close()
	c := NewEmailClient(srv.URL, "tok", time.Second, nil)
	ctx := context.Background()
	src, err := c.DefaultMJML(ctx, "at-1", "password_reset")
	if err != nil || src != "<mjml>a</mjml>" || got.Method != "GET" || got.URL.Path != "/internal/email/default-mjml" ||
		got.URL.Query().Get("part") != "password_reset" || got.Header.Get("Authorization") != "Bearer at-1" || got.Header.Get("X-Internal-Token") != "tok" {
		t.Fatalf("%v %q %v", err, src, got)
	}
	ct, reply = "application/xml; charset=utf-8", "<mjml>raw</mjml>"
	if src, err := c.DefaultMJML(ctx, "at", "layout"); err != nil || src != "<mjml>raw</mjml>" {
		t.Fatalf("%v %q", err, src)
	}
	ct, reply = "application/json", `{"nope":1}`
	if _, err := c.DefaultMJML(ctx, "at", "layout"); err == nil {
		t.Fatal("unreadable JSON")
	}
	ct, reply, status = "application/json", `{"error":"not_found"}`, 404
	if _, err := c.DefaultMJML(ctx, "at", "layout"); !apperr.IsUnsupported(err) {
		t.Fatalf("old auth: %v", err)
	}

	status = 200
	reply = `{"subject":"S","html":"<p>x</p>","text":"x","from":"E <a@b.c>","warnings":["{{nope}} isn't a placeholder",{"message":"second"}]}`
	h := "<p>{{content}}</p>"
	p, err := c.Preview(ctx, "at", EmailPreviewRequest{Template: "invite", LayoutHTML: &h})
	if err != nil || len(p.Warnings) != 2 || p.Warnings[0] != "{{nope}} isn't a placeholder" || p.Warnings[1] != "second" {
		t.Fatalf("%v %+v", err, p)
	}
	if body["layout_html"] != h || body["html"] != nil {
		t.Fatalf("%v", body)
	}
	status = 400
	reply = `{"error":"invalid_request","message":"no {{content}}","field":"layout_html"}`
	if _, err := c.Preview(ctx, "at", EmailPreviewRequest{Template: "invite"}); err == nil {
		t.Fatal("400")
	} else if e, _ := apperr.As(err); e.Field != "layout_html" {
		t.Fatalf("field: %+v", e)
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
