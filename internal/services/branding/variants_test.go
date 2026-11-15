package branding

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/boson-chat/admin/internal/configfile"
	"github.com/boson-chat/admin/internal/services/stackconfig"
	"github.com/boson-chat/admin/internal/theme"
)

func TestThemeVariantsRoundTrip(t *testing.T) {
	ctx := context.Background()
	_, cfg, path := newService(t, handWritten, nil)
	all := func(string) bool { return true }
	set := func(v map[string]any) error {
		_, err := cfg.Update(ctx, "root", "", stackconfig.Change{Set: map[string]any{stackconfig.KeyBrandingTheme: v}}, all)
		return err
	}
	in := map[string]any{
		"version": 1.0, "base": "dark",
		"colors": map[string]any{"accent": "#8b5cf6"},
		"panes":  map[string]any{"messages": map[string]any{"dim": 0.7}},
		"variants": map[string]any{
			"light": map[string]any{
				"colors": map[string]any{"accent": "#6D28D9", "bg": "#ffffff", "nonsense": "#000000"},
				"nick":   map[string]any{"saturation": 60.0, "lightness": 38.0},
				"panes":  map[string]any{"messages": map[string]any{"dim": 0.3, "image": "https://evil.example/a.webp"}, "members": map[string]any{}},
				"radius": 9.0,
			},
			// Same as base: dropped. Unknown names too.
			"dark":  map[string]any{"colors": map[string]any{"accent": "#000000"}},
			"sepia": map[string]any{},
		},
	}
	if err := set(in); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	s := string(raw)
	for _, bad := range []string{"nonsense", "evil", "sepia", "members", "radius", "'#000000'"} {
		if strings.Contains(s, bad) {
			t.Fatalf("stored %q:\n%s", bad, s)
		}
	}
	cfg2 := stackconfig.New(stackconfig.Options{Store: configfile.Open(path, configfile.Options{})})
	v, ok := cfg2.FileValue(stackconfig.KeyBrandingTheme)
	if !ok {
		t.Fatalf("not read back:\n%s", s)
	}
	th := v.(theme.Theme)
	light, ok := th.Variants["light"]
	if !ok || len(th.Variants) != 1 || light.Colors["accent"] != "#6d28d9" || light.Colors["bg"] != "#ffffff" ||
		light.Nick.Lightness != 38 || *light.Panes["messages"].Dim != 0.3 || light.Panes["messages"].Image != "" ||
		th.Colors["accent"] != "#8b5cf6" {
		t.Fatalf("%+v\n%s", th, s)
	}

	// An empty variant (Boson's built-in light) is kept.
	if err := set(map[string]any{"base": "dark", "variants": map[string]any{"light": map[string]any{}}}); err != nil {
		t.Fatal(err)
	}
	v, _ = stackconfig.New(stackconfig.Options{Store: configfile.Open(path, configfile.Options{})}).FileValue(stackconfig.KeyBrandingTheme)
	if _, ok := v.(theme.Theme).Variants["light"]; !ok {
		raw, _ := os.ReadFile(path)
		t.Fatalf("empty variant dropped:\n%s", raw)
	}

	// Invalid variant values are refused with the field named.
	before, _ := os.ReadFile(path)
	for bad, want := range map[string]map[string]any{
		"variants.light.colors.fg":        {"variants": map[string]any{"light": map[string]any{"colors": map[string]any{"fg": "red"}}}},
		"variants.light.nick":             {"variants": map[string]any{"light": map[string]any{"nick": map[string]any{"saturation": 101.0, "lightness": 1.0}}}},
		"variants.light.panes.messages":   {"variants": map[string]any{"light": map[string]any{"panes": map[string]any{"messages": map[string]any{"dim": 1.5}}}}},
		"variants.light.panes.members":    {"variants": map[string]any{"light": map[string]any{"panes": map[string]any{"members": map[string]any{"position": "middle"}}}}},
		"variants.light must be":          {"variants": map[string]any{"light": "yes"}},
		"variants must be":                {"variants": []any{"light"}},
		"variants.dark.panes.servers: bg": {"base": "light", "variants": map[string]any{"dark": map[string]any{"panes": map[string]any{"servers": map[string]any{"bg": "#12345"}}}}},
	} {
		err := set(want)
		if err == nil || !strings.Contains(err.Error(), "branding.theme: "+bad) {
			t.Errorf("%s: %v", bad, err)
		}
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("a refused theme changed the file")
	}
}

func TestVariantImageSlots(t *testing.T) {
	ctx := context.Background()
	b, _, path := newService(t, handWritten, nil)
	img, err := b.Upload(ctx, "root", "", "variants.light.panes.messages.image", pngOf(t, 16, 16))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^pane-messages-light-[0-9a-f]{16}\.webp$`).MatchString(img.File) || !theme.FileRe.MatchString(img.File) {
		t.Fatalf("%+v", img)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "variants.light.panes.messages.image: "+img.File) {
		t.Fatalf("%s", raw)
	}
	if a, err := b.Asset(img.File); err != nil || !a.Immutable {
		t.Fatalf("asset: %v", err)
	}
	if err := b.Remove(ctx, "root", "", "variants.light.panes.messages.image"); err != nil {
		t.Fatal(err)
	}
	if got := files(t, filepath.Join(filepath.Dir(path), "branding")); len(got) != 0 {
		t.Fatalf("left %v", got)
	}
	for _, bad := range []string{"variants.light.logo", "variants.sepia.panes.messages.image", "variants.light.panes.sidebar.image"} {
		if _, err := b.Upload(ctx, "root", "", bad, pngOf(t, 4, 4)); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if theme.FileStem("variants.dark.panes.servers.image") != "pane-servers-dark" || theme.SlotVariant("panes.servers.image") != "" {
		t.Fatal("stems")
	}
}
