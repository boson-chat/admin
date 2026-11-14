package branding

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/configfile"
	"github.com/boson-chat/admin/internal/services/stackconfig"
	"github.com/boson-chat/admin/internal/theme"
)

const handWritten = `version: 1
# Our look.
branding:
  # set from the panel
  theme:
    base: dark
other: {kept: true} # unknown section
`

func newService(t *testing.T, content string, email authapi.EmailClientImpl) (*BrandingService, *stackconfig.Service, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "boson.yaml")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := stackconfig.New(stackconfig.Options{Store: configfile.Open(path, configfile.Options{Header: stackconfig.FileHeader})})
	return NewBrandingService(Options{Config: cfg, Email: email}).(*BrandingService), cfg, path
}

func files(t *testing.T, dir string) []string {
	t.Helper()
	ents, _ := os.ReadDir(dir)
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

var nameRe = regexp.MustCompile(`^pane-messages-[0-9a-f]{16}\.webp$`)

func TestUploadReplaceRemove(t *testing.T) {
	ctx := context.Background()
	b, cfg, path := newService(t, handWritten, nil)
	dir := filepath.Join(filepath.Dir(path), "branding")

	img, err := b.Upload(ctx, "root", "", "panes.messages.image", pngOf(t, 32, 16))
	if err != nil {
		t.Fatal(err)
	}
	if !nameRe.MatchString(img.File) || img.URL != "/api/branding/assets/"+img.File || img.Width != 32 || img.ContentType != "image/webp" {
		t.Fatalf("%+v", img)
	}
	if !theme.FileRe.MatchString(img.File) {
		t.Fatalf("FileRe doesn't match %s", img.File)
	}
	raw, _ := os.ReadFile(path)
	for _, want := range []string{"# Our look.", "# set from the panel", "kept: true", "# unknown section", "panes.messages.image: " + img.File, "by root at"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("file lost %q:\n%s", want, raw)
		}
	}
	if got := files(t, dir); len(got) != 1 || got[0] != img.File {
		t.Fatalf("dir: %v", got)
	}
	if fi, _ := os.Stat(filepath.Join(dir, img.File)); fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", fi.Mode())
	}

	// Same image again: same name, nothing removed.
	again, err := b.Upload(ctx, "root", "", "panes.messages.image", pngOf(t, 32, 16))
	if err != nil || again.File != img.File || len(files(t, dir)) != 1 {
		t.Fatalf("again: %v %v %v", err, again, files(t, dir))
	}

	// A new image replaces the file; the old one is removed.
	next, err := b.Upload(ctx, "root", "", "panes.messages.image", pngOf(t, 8, 8))
	if err != nil || next.File == img.File {
		t.Fatalf("replace: %v %+v", err, next)
	}
	if got := files(t, dir); len(got) != 1 || got[0] != next.File {
		t.Fatalf("old file kept: %v", got)
	}

	// A logo in another slot doesn't touch it.
	logo, err := b.Upload(ctx, "root", "", "logo", []byte(okSVG))
	if err != nil || !strings.HasPrefix(logo.File, "logo-") || !strings.HasSuffix(logo.File, ".svg") {
		t.Fatalf("logo: %v %+v", err, logo)
	}
	v := cfg.View(func(string) bool { return true })
	var imgs any
	for _, k := range v.Keys {
		if k.Key.Key == stackconfig.KeyBrandingImages {
			imgs = k.Value
		}
	}
	m, _ := imgs.(map[string]string)
	if m["logo"] != logo.File || m["panes.messages.image"] != next.File || len(v.Issues) != 0 {
		t.Fatalf("view images: %#v issues %v", imgs, v.Issues)
	}

	a, err := b.Asset(logo.File)
	if err != nil || a.ContentType != "image/svg+xml" || !a.Immutable || string(a.Data) != okSVG {
		t.Fatalf("asset: %v %+v", err, a)
	}
	for _, bad := range []string{"../boson.yaml", ".hidden.webp", "boson.yaml", "nope-0123456789abcdef.webp", ""} {
		if _, err := b.Asset(bad); err == nil {
			t.Errorf("asset %q served", bad)
		}
	}

	if err := b.Remove(ctx, "root", "", "panes.messages.image"); err != nil {
		t.Fatal(err)
	}
	if got := files(t, dir); len(got) != 1 || got[0] != logo.File {
		t.Fatalf("after remove: %v", got)
	}
	if err := b.Remove(ctx, "root", "", "logo"); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	if strings.Contains(string(raw), "images:") || !strings.Contains(string(raw), "# set from the panel") {
		t.Fatalf("after removing everything:\n%s", raw)
	}
	if err := b.Remove(ctx, "root", "", "logo"); err != nil {
		t.Fatalf("removing an empty slot: %v", err)
	}
}

func TestUploadErrors(t *testing.T) {
	ctx := context.Background()
	b, _, path := newService(t, "version: 1\n", nil)
	dir := filepath.Join(filepath.Dir(path), "branding")
	if _, err := b.Upload(ctx, "root", "", "favicon", pngOf(t, 2, 2)); err == nil {
		t.Fatal("unknown slot accepted")
	}
	if _, err := b.Upload(ctx, "root", "", "logo", []byte("<svg><script/></svg>")); err == nil {
		t.Fatal("script accepted")
	}
	// A stale If-Match: nothing written, the new file cleaned up.
	if _, err := b.Upload(ctx, "root", `"stale"`, "logo", pngOf(t, 2, 2)); err == nil {
		t.Fatal("stale If-Match accepted")
	} else if e, _ := apperr.As(err); e == nil || e.Status != 412 {
		t.Fatalf("stale: %v", err)
	}
	if got := files(t, dir); len(got) != 0 {
		t.Fatalf("left behind: %v", got)
	}
	// A broken file on disk is never overwritten.
	_ = os.WriteFile(path, []byte("version: 1\nbranding: [\n"), 0o644)
	if _, err := b.Upload(ctx, "root", "", "logo", pngOf(t, 2, 2)); err == nil {
		t.Fatal("wrote over a broken file")
	}
	if got := files(t, dir); len(got) != 0 {
		t.Fatalf("left behind: %v", got)
	}

	off := NewBrandingService(Options{Config: stackconfig.New(stackconfig.Options{})})
	if _, err := off.Upload(ctx, "root", "", "logo", pngOf(t, 2, 2)); err == nil {
		t.Fatal("upload without a config file")
	}
}

// A hand-written image entry outside the panel's naming is still served,
// and an invalid one doesn't block uploads.
func TestHandWrittenImages(t *testing.T) {
	b, _, path := newService(t, "version: 1\nbranding:\n  images:\n    logo: logo.svg\n    bogus: x.png\n", nil)
	dir := filepath.Join(filepath.Dir(path), "branding")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "logo.svg"), []byte(okSVG), 0o644)
	a, err := b.Asset("logo.svg")
	if err != nil || a.Immutable {
		t.Fatalf("%v %+v", err, a)
	}
	img, err := b.Upload(context.Background(), "root", "", "login_background", jpegOf(t, 16, 9))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "logo: logo.svg") || !strings.Contains(string(raw), "login_background: "+img.File) {
		t.Fatalf("%s", raw)
	}
}

// The theme goes through PATCH /api/config's path and comes back the same.
func TestThemeRoundTrip(t *testing.T) {
	ctx := context.Background()
	_, cfg, path := newService(t, handWritten, nil)
	in := map[string]any{
		"version": 1.0, "base": "light",
		"colors": map[string]any{"accent": "#7C3AED", "bg": "#ffffff", "nonsense": "#000000"},
		"nick":   map[string]any{"saturation": 50.0, "lightness": 40.0},
		"radius": 4.0, "font": "system",
		"panes": map[string]any{
			"messages": map[string]any{"image": "https://x.example/a.webp", "dim": 0.7, "blur": 8.0, "position": "top"},
			"servers":  map[string]any{"bg": "#0b0b10"},
			"members":  map[string]any{},
		},
		"logo":  "https://evil.example/logo.png",
		"extra": true,
	}
	all := func(string) bool { return true }
	if _, err := cfg.Update(ctx, "root", "", stackconfig.Change{Set: map[string]any{stackconfig.KeyBrandingTheme: in}}, all); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	s := string(raw)
	for _, bad := range []string{"nonsense", "evil", "x.example", "extra", "members"} {
		if strings.Contains(s, bad) {
			t.Fatalf("stored %q:\n%s", bad, s)
		}
	}
	if !strings.Contains(s, "accent: '#7c3aed'") && !strings.Contains(s, `accent: "#7c3aed"`) {
		t.Fatalf("accent:\n%s", s)
	}
	if !strings.Contains(s, "# Our look.") || !strings.Contains(s, "kept: true") {
		t.Fatalf("comments:\n%s", s)
	}
	// Read back by a fresh store (as after a restart).
	cfg2 := stackconfig.New(stackconfig.Options{Store: configfile.Open(path, configfile.Options{})})
	v, ok := cfg2.FileValue(stackconfig.KeyBrandingTheme)
	if !ok {
		t.Fatalf("not read back:\n%s", s)
	}
	th := v.(theme.Theme)
	if th.Base != "light" || th.Colors["accent"] != "#7c3aed" || *th.Radius != 4 || th.Font != "system" ||
		th.Nick.Saturation != 50 || th.Panes["messages"].Position != "top" || *th.Panes["messages"].Dim != 0.7 ||
		*th.Panes["messages"].Blur != 8 || th.Panes["servers"].BG != "#0b0b10" || th.Panes["messages"].Image != "" {
		t.Fatalf("%+v", th)
	}

	// Invalid values are refused with the field named, and the file is untouched.
	before, _ := os.ReadFile(path)
	for _, bad := range []map[string]any{
		{"base": "neon"},
		{"colors": map[string]any{"fg": "red"}},
		{"radius": 13.0},
		{"panes": map[string]any{"messages": map[string]any{"dim": 1.5}}},
		{"panes": map[string]any{"messages": map[string]any{"blur": 25.0}}},
		{"panes": map[string]any{"messages": map[string]any{"position": "middle"}}},
		{"nick": map[string]any{"saturation": 101.0, "lightness": 1.0}},
		{"font": "comic"},
		{"version": 2.0},
	} {
		if _, err := cfg.Update(ctx, "root", "", stackconfig.Change{Set: map[string]any{stackconfig.KeyBrandingTheme: bad}}, all); err == nil ||
			!strings.HasPrefix(err.Error(), "invalid_request: branding.theme:") {
			t.Errorf("%v: %v", bad, err)
		}
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("a refused theme changed the file")
	}
	// branding.images can't be set through PATCH.
	if _, err := cfg.Update(ctx, "root", "", stackconfig.Change{Set: map[string]any{stackconfig.KeyBrandingImages: map[string]any{"logo": "x.svg"}}}, all); err == nil {
		t.Fatal("images set through PATCH")
	}
	if _, err := cfg.Update(ctx, "root", "", stackconfig.Change{Unset: []string{stackconfig.KeyBrandingImages}}, all); err == nil {
		t.Fatal("images unset through PATCH")
	}
}

func TestEmailKeysRoundTrip(t *testing.T) {
	ctx := context.Background()
	_, cfg, path := newService(t, "version: 1\n", nil)
	all := func(string) bool { return true }
	body := "Hi {{username}},\r\n\r\nConfirm: {{link}}\n\nExpires in {{expires}}."
	set := map[string]any{
		"auth.email.sender_name":                    "Example Chat",
		"auth.email.accent":                         "#7C3AED",
		"auth.email.footer":                         "Example Chat · example.com",
		"auth.email.logo":                           true,
		"auth.email.templates.verification.subject": "Confirm your {{network}} account",
		"auth.email.templates.verification.body":    body,
	}
	if _, err := cfg.Update(ctx, "root", "", stackconfig.Change{Set: set}, all); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "body: |-") {
		t.Fatalf("body isn't a block scalar:\n%s", raw)
	}
	v, _ := cfg.FileValue("auth.email.templates.verification.body")
	if v != "Hi {{username}},\n\nConfirm: {{link}}\n\nExpires in {{expires}}." {
		t.Fatalf("body %q", v)
	}
	if v, _ := cfg.FileValue("auth.email.accent"); v != "#7c3aed" {
		t.Fatalf("accent %v", v)
	}
	for k, bad := range map[string]any{
		"auth.email.accent":                        "purple",
		"auth.email.templates.invite.subject":      "two\nlines",
		"auth.email.templates.invite.body":         strings.Repeat("x", 5001),
		"auth.email.templates.password_reset.body": "  ",
		"auth.email.templates.nonsense.subject":    "x",
		"auth.email.sender_name":                   strings.Repeat("n", 65),
	} {
		if _, err := cfg.Update(ctx, "root", "", stackconfig.Change{Set: map[string]any{k: bad}}, all); err == nil {
			t.Errorf("%s accepted %q", k, bad)
		}
	}
	// Reset to default = unset subject and body.
	if _, err := cfg.Update(ctx, "root", "", stackconfig.Change{Unset: []string{"auth.email.templates.verification.subject", "auth.email.templates.verification.body"}}, all); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	if strings.Contains(string(raw), "templates") || !strings.Contains(string(raw), "sender_name: Example Chat") {
		t.Fatalf("after reset:\n%s", raw)
	}
}

type fakeEmail struct {
	preview authapi.EmailPreviewRequest
	test    authapi.EmailTestRequest
	actor   string
	html    string
	err     error
}

func (f *fakeEmail) Preview(_ context.Context, actor string, in authapi.EmailPreviewRequest) (*authapi.EmailPreview, error) {
	f.actor, f.preview = actor, in
	if f.err != nil {
		return nil, f.err
	}
	return &authapi.EmailPreview{Subject: "Confirm", HTML: f.html, Text: "plain"}, nil
}

func (f *fakeEmail) SendTest(_ context.Context, actor string, in authapi.EmailTestRequest) (*authapi.EmailTestResult, error) {
	f.actor, f.test = actor, in
	return &authapi.EmailTestResult{Sent: true}, f.err
}

// tok is WithToken for a session whose access token is at-1.
func tok(fn func(string) error) error { return fn("at-1") }

func TestEmailPreviewInlinesImages(t *testing.T) {
	ctx := context.Background()
	fe := &fakeEmail{}
	b, _, _ := newService(t, "version: 1\n", fe)
	logo, err := b.Upload(ctx, "root", "", "logo", []byte(okSVG))
	if err != nil {
		t.Fatal(err)
	}
	fe.html = `<img src="https://auth.example.com/v1/branding/assets/` + logo.File + `?v=abc&amp;x=1" alt=""><img src='https://auth.example.com/v1/branding/assets/gone-0123456789abcdef.webp'><a href="https://example.com">x</a>`
	subj := "Hello {{network}}"
	p, err := b.Preview(ctx, "root", tok, authapi.EmailPreviewRequest{Template: "verification", Subject: &subj})
	if err != nil {
		t.Fatal(err)
	}
	if fe.actor != "at-1" || fe.preview.Template != "verification" || *fe.preview.Subject != subj {
		t.Fatalf("sent %+v", fe.preview)
	}
	if !strings.Contains(p.HTML, `src="data:image/svg+xml;base64,`) || strings.Contains(p.HTML, logo.File) {
		t.Fatalf("not inlined: %s", p.HTML)
	}
	if !strings.Contains(p.HTML, "gone-0123456789abcdef.webp") || !strings.Contains(p.HTML, `href="https://example.com"`) {
		t.Fatalf("touched other URLs: %s", p.HTML)
	}

	if _, err := b.Preview(ctx, "root", tok, authapi.EmailPreviewRequest{Template: "welcome"}); err == nil {
		t.Fatal("unknown template")
	}
	long := strings.Repeat("x", 5001)
	if _, err := b.Preview(ctx, "root", tok, authapi.EmailPreviewRequest{Template: "invite", Body: &long}); err == nil {
		t.Fatal("long body")
	}

	if _, err := b.SendTest(ctx, "root", tok, authapi.EmailTestRequest{Template: "invite", To: " me@example.com "}); err != nil || fe.test.To != "me@example.com" {
		t.Fatalf("test: %v %+v", err, fe.test)
	}
	for _, to := range []string{"", "nope", "a@b.c, d@e.f", "Me <me@example.com>", "me@example.com\r\nBcc: x@y.z"} {
		if _, err := b.SendTest(ctx, "root", tok, authapi.EmailTestRequest{Template: "invite", To: to}); err == nil {
			t.Errorf("to %q accepted", to)
		}
	}

	off, _, _ := newService(t, "version: 1\n", nil)
	if _, err := off.Preview(ctx, "root", tok, authapi.EmailPreviewRequest{Template: "invite"}); err == nil || off.EmailEnabled() {
		t.Fatal("preview without auth's internal API")
	}
}

func TestEmailPreviewOverrides(t *testing.T) {
	ctx := context.Background()
	fe := &fakeEmail{}
	b, _, _ := newService(t, "version: 1\n", fe)
	name, accent, footer, logo := "  Example Chat ", "#7C3AED", " Example · example.com ", false
	in := authapi.EmailPreviewRequest{Template: "invite", Email: &authapi.EmailOverrides{SenderName: &name, Accent: &accent, Footer: &footer, Logo: &logo}}
	if _, err := b.Preview(ctx, "root", tok, in); err != nil {
		t.Fatal(err)
	}
	o := fe.preview.Email
	if o == nil || *o.SenderName != "Example Chat" || *o.Accent != "#7c3aed" || *o.Footer != "Example · example.com" || *o.Logo {
		t.Fatalf("sent %+v", o)
	}
	str := func(s string) *string { return &s }
	for field, ov := range map[string]authapi.EmailOverrides{
		"email.accent":      {Accent: str("purple")},
		"email.sender_name": {SenderName: str(strings.Repeat("x", 65))},
		"email.footer":      {Footer: str("two\nlines")},
	} {
		fe.preview = authapi.EmailPreviewRequest{}
		_, err := b.Preview(ctx, "root", tok, authapi.EmailPreviewRequest{Template: "invite", Email: &ov})
		if e, ok := apperr.As(err); !ok || e.Status != 400 || e.Field != field {
			t.Errorf("%s: %v", field, err)
		}
		if fe.preview.Template != "" {
			t.Errorf("%s: auth called", field)
		}
	}
	long := strings.Repeat("x", 201)
	if _, err := b.Preview(ctx, "root", tok, authapi.EmailPreviewRequest{Template: "invite", Subject: &long}); err == nil {
		t.Fatal("long subject")
	} else if e, _ := apperr.As(err); e.Field != "subject" {
		t.Fatalf("subject field: %+v", e)
	}
	// auth's own field error comes through untouched.
	fe.err = apperr.InvalidField("email.footer", "auth says no")
	if _, err := b.Preview(ctx, "root", tok, authapi.EmailPreviewRequest{Template: "invite"}); err == nil {
		t.Fatal("auth error")
	} else if e, _ := apperr.As(err); e.Field != "email.footer" || e.Message != "auth says no" {
		t.Fatalf("auth field error: %+v", e)
	}
}

type fakePublic struct {
	doc map[string]any
	ok  bool
	err error
}

func (f fakePublic) Document(context.Context) (map[string]any, bool, error) {
	return f.doc, f.ok, f.err
}

// The directory gets auth's document, with absent images sent as "" so
// the listing drops them.
func TestDirectoryDocument(t *testing.T) {
	ctx := context.Background()
	logo := "https://auth.example.com/v1/branding/assets/logo-0123456789abcdef.svg?v=1"
	b := NewBrandingService(Options{Public: fakePublic{ok: true, doc: map[string]any{
		"version": 1.0, "base": "dark", "logo": logo,
		"panes": map[string]any{"messages": map[string]any{"image": "https://auth.example.com/m.webp", "dim": 0.7}},
	}}})
	doc, ok, err := b.DirectoryDocument(ctx)
	if err != nil || !ok || doc["logo"] != logo || doc["logo_wide"] != "" || doc["login_background"] != "" {
		t.Fatalf("%v %v %v", err, ok, doc)
	}
	panes := doc["panes"].(map[string]any)
	if panes["messages"].(map[string]any)["image"] != "https://auth.example.com/m.webp" || panes["messages"].(map[string]any)["dim"] != 0.7 ||
		panes["servers"].(map[string]any)["image"] != "" {
		t.Fatalf("panes %v", panes)
	}
	none := NewBrandingService(Options{Public: fakePublic{}})
	if doc, ok, err := none.DirectoryDocument(ctx); doc != nil || ok || err != nil {
		t.Fatalf("no branding: %v %v %v", doc, ok, err)
	}
	if _, _, err := NewBrandingService(Options{}).DirectoryDocument(ctx); err == nil {
		t.Fatal("no auth client")
	}
}
