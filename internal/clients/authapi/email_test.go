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
	p, err := c.Preview(ctx, "root", EmailPreviewRequest{Template: "invite", Subject: &s})
	if err != nil || p.Subject != "Confirm" || p.HTML != "<p>x</p>" {
		t.Fatalf("%v %+v", err, p)
	}
	if got.Get("X-Internal-Token") != "tok" || got.Get("X-Acting-Account") != "root" || got.Get("X-Acting-Via") != "admin" ||
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
	mode = "bad"
	if _, err := c.Preview(ctx, "root", EmailPreviewRequest{Template: "x"}); err == nil {
		t.Fatal("400")
	} else if e, _ := apperr.As(err); e.Status != 400 || e.Message != "unknown template" {
		t.Fatalf("400: %v", err)
	}
}
