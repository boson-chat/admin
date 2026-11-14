package directory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/bosonapi"
	"github.com/boson-chat/admin/internal/theme"
)

func TestBrandingPushedWithTheListing(t *testing.T) {
	f, srv := newFakeV1(t)
	doc := theme.ForDirectory(map[string]any{
		"version": 1.0, "base": "light", "colors": map[string]any{"accent": "#7c3aed"},
		"logo": "https://auth.example.com/v1/branding/assets/logo-0123456789abcdef.svg?v=1",
	})
	has := true
	var docErr error
	d := NewDirectoryService(Options{Client: bosonapi.NewDirectoryClient(srv.URL, time.Second, nil), Registration: true,
		Secret: []byte("0123456789abcdef0123456789abcdef"), Hostname: "irc.example.org",
		Branding: func(context.Context) (map[string]any, bool, error) { return doc, has, docErr },
	}).(*DirectoryService)
	ctx := context.Background()

	l, err := d.Submit(ctx, "root", bosonapi.Submission{Hostname: "irc.example.org", Port: 6697, Name: "Example"})
	if err != nil {
		t.Fatal(err)
	}
	id := l.Network.ID
	if l.BrandingSync == nil || !l.BrandingSync.OK || f.themePuts != 1 {
		t.Fatalf("sync: %+v puts %d", l.BrandingSync, f.themePuts)
	}
	got := f.themes[id]
	if got["base"] != "light" || got["logo"] != doc["logo"] || got["login_background"] != "" ||
		got["panes"].(map[string]any)["messages"].(map[string]any)["image"] != "" {
		t.Fatalf("theme: %v", got)
	}

	// Nothing new: not re-sent.
	l, err = d.Update(ctx, "root", "irc.example.org", id, bosonapi.Submission{Name: "Renamed"})
	if err != nil || !l.BrandingSync.OK || f.themePuts != 1 {
		t.Fatalf("update: %v %+v puts %d", err, l.BrandingSync, f.themePuts)
	}
	// A change is pushed by PushBranding.
	doc["base"] = "dark"
	res, err := d.PushBranding(ctx, "root")
	if err != nil || !res.OK || f.themePuts != 2 || f.themes[id]["base"] != "dark" {
		t.Fatalf("push: %v %+v", err, res)
	}
	// The directory refuses a value: reported with its message.
	doc["logo"] = "http://auth.localhost/logo.svg"
	res, _ = d.PushBranding(ctx, "root")
	if res.OK || res.Error == "" || res.Unsupported {
		t.Fatalf("invalid: %+v", res)
	}
	// No branding any more: the listing's theme is cleared.
	has = false
	res, _ = d.PushBranding(ctx, "root")
	if !res.OK || !res.Cleared || f.themes[id] != nil {
		t.Fatalf("clear: %+v %v", res, f.themes[id])
	}
	if res, _ := d.PushBranding(ctx, "root"); !res.OK {
		t.Fatalf("clear again: %+v", res)
	}

	// An imported listing: 409 not_local, explained.
	has, doc["logo"] = true, ""
	f.imported = map[string]bool{id: true}
	res, _ = d.PushBranding(ctx, "root")
	if res.OK || res.Error == "" {
		t.Fatalf("not local: %+v", res)
	}
	f.imported = nil

	// An older directory without the theme API: unsupported, not an error,
	// and the listing edit itself still succeeds.
	f.noTheme = true
	d.pushed = map[string]string{}
	l, err = d.Update(ctx, "root", "irc.example.org", id, bosonapi.Submission{Name: "Again"})
	if err != nil || l.Network.Name != "Again" || l.BrandingSync == nil || !l.BrandingSync.Unsupported || l.BrandingSync.OK {
		t.Fatalf("old directory: %v %+v", err, l.BrandingSync)
	}

	// auth can't serve the document: reported.
	f.noTheme = false
	docErr = apperr.Unsupported("old auth")
	if l, _ := d.Update(ctx, "root", "irc.example.org", id, bosonapi.Submission{Name: "x"}); l.BrandingSync == nil || !l.BrandingSync.Unsupported {
		t.Fatalf("old auth: %+v", l.BrandingSync)
	}
	docErr = errors.New("down")
	if l, _ := d.Update(ctx, "root", "irc.example.org", id, bosonapi.Submission{Name: "x"}); l.BrandingSync == nil || l.BrandingSync.Error == "" {
		t.Fatalf("auth down: %+v", l.BrandingSync)
	}
}

func TestPushBrandingWithoutListing(t *testing.T) {
	_, srv := newFakeV1(t)
	calls := 0
	src := func(context.Context) (map[string]any, bool, error) { calls++; return map[string]any{}, true, nil }
	d := NewDirectoryService(Options{Client: bosonapi.NewDirectoryClient(srv.URL, time.Second, nil), Registration: true,
		Secret: []byte("k"), Hostname: "irc.example.org", Branding: src})
	if res, err := d.PushBranding(context.Background(), "root"); res != nil || err != nil || calls != 0 {
		t.Fatalf("%v %v %d", res, err, calls)
	}
	off := NewDirectoryService(Options{Client: bosonapi.NewDirectoryClient(srv.URL, time.Second, nil), Secret: []byte("k"), Hostname: "irc.example.org", Branding: src})
	if res, err := off.PushBranding(context.Background(), "root"); res != nil || err != nil || calls != 0 {
		t.Fatalf("registration off: %v %v", res, err)
	}
}
