package directory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/bosonapi"
)

// fakeDirectory serves the live public search and the proposed
// registration API, keyed by the management key's header.
func fakeDirectory(t *testing.T, registration bool) (*httptest.Server, *map[string]string) {
	keys := map[string]string{} // id -> key
	var listing *bosonapi.Server
	mux := http.NewServeMux()
	mux.HandleFunc("GET /servers", func(w http.ResponseWriter, r *http.Request) {
		out := []bosonapi.Server{{ID: "x", Hostname: "irc.other.org", Name: "Other", VerificationStatus: "verified"}}
		if listing != nil && listing.VerificationStatus == "verified" {
			out = append(out, *listing)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"servers": out, "count": len(out)})
	})
	if registration {
		auth := func(w http.ResponseWriter, r *http.Request) bool {
			key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Network ")
			if !ok || (r.PathValue("id") != "" && keys[r.PathValue("id")] != key) {
				w.WriteHeader(403)
				_, _ = w.Write([]byte(`{"error":"bad_key"}`))
				return false
			}
			return true
		}
		ver := &bosonapi.Verification{RecordName: "_boson.irc.example.org", RecordValue: "boson-verify=tok"}
		mux.HandleFunc("GET /directory/networks/lookup", func(w http.ResponseWriter, r *http.Request) {
			if !auth(w, r) {
				return
			}
			if listing == nil || keys[listing.ID] != strings.TrimPrefix(r.Header.Get("Authorization"), "Network ") {
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`{"error":"not_found"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(bosonapi.Listing{Network: *listing, Verification: ver})
		})
		mux.HandleFunc("POST /directory/networks", func(w http.ResponseWriter, r *http.Request) {
			if !auth(w, r) {
				return
			}
			var s bosonapi.Submission
			_ = json.NewDecoder(r.Body).Decode(&s)
			listing = &bosonapi.Server{ID: "n1", Hostname: s.Hostname, Port: s.Port, TLS: *s.TLS, Name: s.Name, VerificationStatus: "pending"}
			keys["n1"] = strings.TrimPrefix(r.Header.Get("Authorization"), "Network ")
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(bosonapi.Listing{Network: *listing, Verification: ver})
		})
		mux.HandleFunc("POST /directory/networks/{id}/verify", func(w http.ResponseWriter, r *http.Request) {
			if !auth(w, r) {
				return
			}
			listing.VerificationStatus = "verified"
			_, _ = w.Write([]byte(`{"success":true,"report":{"success":true,"results":{"cloudflare":{"outcome":"match"},"google":{"outcome":"match"},"quad9":{"outcome":"match"}}}}`))
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &keys
}

func TestRegistrationFlow(t *testing.T) {
	srv, keys := fakeDirectory(t, true)
	d := NewDirectoryService(Options{Client: bosonapi.NewDirectoryClient(srv.URL, time.Second, nil), Registration: true,
		Secret: []byte("0123456789abcdef0123456789abcdef"), Hostname: "irc.example.org"}).(*DirectoryService)
	ctx := context.Background()
	st := d.Status(ctx, "")
	if st.Listing != nil || st.Public != nil || st.Unsupported != "" || st.Hostname != "irc.example.org" {
		t.Fatalf("before: %+v", st)
	}
	if _, err := d.Submit(ctx, "root", bosonapi.Submission{Hostname: "1.2.3.4", Port: 6697, Name: "x"}); err == nil {
		t.Fatal("IP accepted")
	}
	l, err := d.Submit(ctx, "root", bosonapi.Submission{Hostname: "IRC.Example.org.", Port: 6697, Name: "Example"})
	if err != nil || l.Verification.RecordName != "_boson.irc.example.org" || l.Network.Hostname != "irc.example.org" {
		t.Fatalf("submit: %v %+v", err, l)
	}
	if (*keys)["n1"] != d.Key("irc.example.org") || d.Key("irc.example.org") == d.Key("irc.other.org") {
		t.Fatal("management key not derived per hostname")
	}
	st = d.Status(ctx, "irc.example.org")
	if st.Listing == nil || st.Listing.Network.VerificationStatus != "pending" || st.Public != nil {
		t.Fatalf("pending: %+v", st)
	}
	r, err := d.Verify(ctx, "root", "irc.example.org", "n1")
	if err != nil || !r.Success || r.Report.Results["quad9"].Outcome != "match" {
		t.Fatalf("verify: %v %+v", err, r)
	}
	st = d.Status(ctx, "irc.example.org")
	if st.Public == nil || st.Public.ID != "n1" {
		t.Fatalf("listed: %+v", st)
	}
	// Another panel (different secret) can't touch it.
	other := NewDirectoryService(Options{Client: bosonapi.NewDirectoryClient(srv.URL, time.Second, nil), Registration: true, Secret: []byte("x")})
	if _, err := other.Verify(ctx, "eve", "irc.example.org", "n1"); err == nil {
		t.Fatal("foreign key accepted")
	}
}

func TestRegistrationNotLive(t *testing.T) {
	srv, _ := fakeDirectory(t, false)
	c := bosonapi.NewDirectoryClient(srv.URL, time.Second, nil)
	on := NewDirectoryService(Options{Client: c, Registration: true, Secret: []byte("s"), Hostname: "irc.other.org"})
	st := on.Status(context.Background(), "")
	if st.Unsupported == "" || st.Public == nil || st.Public.Name != "Other" {
		t.Fatalf("unsupported but publicly listed: %+v", st)
	}
	off := NewDirectoryService(Options{Client: c, Hostname: "irc.example.org"})
	if _, err := off.Submit(context.Background(), "root", bosonapi.Submission{Hostname: "irc.example.org", Port: 6697, Name: "x"}); err == nil {
		t.Fatal("submit with registration off")
	} else if e, _ := apperr.As(err); e.Code != "directory_registration_off" {
		t.Fatalf("got %v", err)
	}
}
