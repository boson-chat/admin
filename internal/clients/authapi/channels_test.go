package authapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
)

func TestRoleChannelsWireFormat(t *testing.T) {
	var got []string
	mux := http.NewServeMux()
	record := func(r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.RequestURI()+" "+strings.TrimSpace(string(b)))
	}
	mux.HandleFunc("GET /v1/admin/roles/{id}/channels", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if r.PathValue("id") == "everyone" { // a bare array is read too
			_, _ = w.Write([]byte(`[{"channel":"#lobby","autojoin":true}]`))
			return
		}
		_, _ = w.Write([]byte(`{"channels":[{"channel":"#staff","autojoin":true,"created_at":"2026-11-15T00:00:00Z"}]}`))
	})
	mux.HandleFunc("PUT /v1/admin/roles/{id}/channels", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v1/admin/channel-access", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_, _ = w.Write([]byte(`{"channels":null}`))
	})
	mux.HandleFunc("PUT /v1/admin/channel-access", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_, _ = w.Write([]byte(`{"channels":[{"channel":"#staff","private":true}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := NewAuthClient(srv.URL, time.Second, nil)
	ctx := context.Background()

	if l, err := c.RoleChannels(ctx, "t", "rol_mods"); err != nil || len(l) != 1 || l[0] != (RoleChannel{"#staff", true}) {
		t.Fatalf("role channels: %+v %v", l, err)
	}
	if l, err := c.RoleChannels(ctx, "t", EveryoneRole); err != nil || len(l) != 1 || l[0].Channel != "#lobby" {
		t.Fatalf("everyone: %+v %v", l, err)
	}
	if l, err := c.SetRoleChannels(ctx, "t", "rol_mods", []RoleChannel{{"#staff", false}}); err != nil || len(l) != 1 {
		t.Fatalf("set (204): %+v %v", l, err)
	}
	if l, err := c.SetRoleChannels(ctx, "t", "rol_mods", nil); err != nil || l == nil || len(l) != 0 {
		t.Fatalf("clear: %+v %v", l, err)
	}
	if l, err := c.ChannelAccessList(ctx, "t"); err != nil || l == nil || len(l) != 0 {
		t.Fatalf("access: %+v %v", l, err)
	}
	if l, err := c.SetChannelAccess(ctx, "t", []ChannelAccess{{"#staff", true}}); err != nil || !l[0].Private {
		t.Fatalf("set access: %+v %v", l, err)
	}
	want := []string{
		"GET /v1/admin/roles/rol_mods/channels ",
		"GET /v1/admin/roles/everyone/channels ",
		`PUT /v1/admin/roles/rol_mods/channels {"channels":[{"channel":"#staff","autojoin":false}]}`,
		`PUT /v1/admin/roles/rol_mods/channels {"channels":[]}`,
		"GET /v1/admin/channel-access ",
		`PUT /v1/admin/channel-access {"channels":[{"channel":"#staff","private":true}]}`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestRoleChannelsOnOlderAuth(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	c := NewAuthClient(srv.URL, time.Second, nil)
	_, err := c.RoleChannels(context.Background(), "t", "rol_mods")
	if !apperr.IsUnsupported(err) || !strings.Contains(err.Error(), RequiresRoleChannels) {
		t.Fatalf("want unsupported, got %v", err)
	}
	if _, err := c.SetChannelAccess(context.Background(), "t", nil); !apperr.IsUnsupported(err) {
		t.Fatalf("want unsupported, got %v", err)
	}
}
