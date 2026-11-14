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
	radius := 4
	bundle := &theme.Bundle{
		Theme: theme.Theme{Version: 1, Base: "light", Colors: map[string]string{"accent": "#7c3aed"}, Radius: &radius},
		Images: map[string]theme.BundleImage{
			"logo":                 {ContentType: "image/svg+xml", Data: []byte("<svg/>"), Hash: "a"},
			"panes.messages.image": {ContentType: "image/webp", Data: []byte("RIFF"), Hash: "b"},
		},
	}
	var brandingErr error
	d := NewDirectoryService(Options{Client: bosonapi.NewDirectoryClient(srv.URL, time.Second, nil), Registration: true,
		Secret: []byte("0123456789abcdef0123456789abcdef"), Hostname: "irc.example.org",
		Branding: func(context.Context) (*theme.Bundle, error) { return bundle, brandingErr },
	}).(*DirectoryService)
	ctx := context.Background()

	l, err := d.Submit(ctx, "root", bosonapi.Submission{Hostname: "irc.example.org", Port: 6697, Name: "Example"})
	if err != nil {
		t.Fatal(err)
	}
	id := l.Network.ID
	if l.BrandingSync == nil || !l.BrandingSync.OK || l.BrandingSync.Images != 2 {
		t.Fatalf("sync: %+v", l.BrandingSync)
	}
	if f.themes[id]["base"] != "light" || f.themes[id]["colors"].(map[string]any)["accent"] != "#7c3aed" || f.themes[id]["logo"] != nil {
		t.Fatalf("theme: %v", f.themes[id])
	}
	if f.themeImages[id]["logo"] != "image/svg+xml" || f.themeImages[id]["pane-messages"] != "image/webp" || len(f.themeImages[id]) != 2 {
		t.Fatalf("images: %v", f.themeImages[id])
	}

	// An edit with nothing new re-sends no images.
	puts := f.imagePuts
	l, err = d.Update(ctx, "root", "irc.example.org", id, bosonapi.Submission{Name: "Renamed"})
	if err != nil || !l.BrandingSync.OK || f.imagePuts != puts || l.BrandingSync.Images != 0 {
		t.Fatalf("update: %v %+v puts %d→%d", err, l.BrandingSync, puts, f.imagePuts)
	}

	// A removed image is deleted; a changed one re-sent.
	delete(bundle.Images, "logo")
	bundle.Images["panes.messages.image"] = theme.BundleImage{ContentType: "image/webp", Data: []byte("RIFF2"), Hash: "c"}
	res, err := d.PushBranding(ctx, "root")
	if err != nil || !res.OK || res.Images != 1 || f.themeImages[id]["logo"] != "" || f.imagePuts != puts+1 {
		t.Fatalf("push: %v %+v %v", err, res, f.themeImages[id])
	}

	// logo_wide isn't known to this directory: reported, the rest goes on.
	bundle.Images["logo_wide"] = theme.BundleImage{ContentType: "image/webp", Data: []byte("RIFF"), Hash: "d"}
	res, _ = d.PushBranding(ctx, "root")
	if res.OK || res.Error == "" || res.Unsupported {
		t.Fatalf("logo_wide: %+v", res)
	}

	// An older directory without the theme API: unsupported, not an error,
	// and the listing edit itself still succeeds.
	f.noTheme = true
	d.pushed = map[string]map[string]string{}
	l, err = d.Update(ctx, "root", "irc.example.org", id, bosonapi.Submission{Name: "Again"})
	if err != nil || l.Network.Name != "Again" || l.BrandingSync == nil || !l.BrandingSync.Unsupported || l.BrandingSync.OK {
		t.Fatalf("old directory: %v %+v", err, l.BrandingSync)
	}

	// No stack config file: nothing to push.
	brandingErr = apperr.New(503, "config_file_off", "off")
	if l, _ := d.Update(ctx, "root", "irc.example.org", id, bosonapi.Submission{Name: "x"}); l.BrandingSync != nil {
		t.Fatalf("config off: %+v", l.BrandingSync)
	}
	brandingErr = errors.New("disk on fire")
	if l, _ := d.Update(ctx, "root", "irc.example.org", id, bosonapi.Submission{Name: "x"}); l.BrandingSync == nil || l.BrandingSync.Error == "" {
		t.Fatalf("bundle error: %+v", l.BrandingSync)
	}
}

func TestPushBrandingWithoutListing(t *testing.T) {
	_, srv := newFakeV1(t)
	calls := 0
	d := NewDirectoryService(Options{Client: bosonapi.NewDirectoryClient(srv.URL, time.Second, nil), Registration: true,
		Secret: []byte("k"), Hostname: "irc.example.org",
		Branding: func(context.Context) (*theme.Bundle, error) { calls++; return &theme.Bundle{}, nil },
	})
	if res, err := d.PushBranding(context.Background(), "root"); res != nil || err != nil || calls != 0 {
		t.Fatalf("%v %v %d", res, err, calls)
	}
	off := NewDirectoryService(Options{Client: bosonapi.NewDirectoryClient(srv.URL, time.Second, nil), Secret: []byte("k"), Hostname: "irc.example.org",
		Branding: func(context.Context) (*theme.Bundle, error) { calls++; return &theme.Bundle{}, nil }})
	if res, err := off.PushBranding(context.Background(), "root"); res != nil || err != nil || calls != 0 {
		t.Fatalf("registration off: %v %v", res, err)
	}
}
