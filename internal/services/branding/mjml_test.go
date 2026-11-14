package branding

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
)

const (
	layoutMJML = "<mjml><mj-body><mj-section><mj-column><mj-text>{{ content }}</mj-text></mj-column></mj-section></mj-body></mjml>"
	layoutHTML = "<!doctype html><html><body><div>{{ content }}</div></body></html>"
	inviteMJML = `<mjml><mj-body><mj-button href="{{link}}">Join</mj-button></mj-body></mjml>`
	inviteHTML = `<!doctype html><html><body><a href="{{link}}">Join</a></body></html>`
)

func emailDirFiles(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	slices.Sort(out)
	return out
}

func field(err error) string {
	e, _ := apperr.As(err)
	if e == nil {
		return ""
	}
	return e.Field
}

func TestCheckEmailHTML(t *testing.T) {
	for _, c := range []struct {
		part, field, doc, want string
	}{
		{"layout", "html", layoutHTML, ""},
		{"layout", "html", "<p>no content</p>", "html"},
		{"layout", "mjml", "<mjml>{{content}}{{ content }}</mjml>", "mjml"},
		{"layout", "layout_html", "", "layout_html"},
		{"invite", "html", inviteHTML, ""},
		{"invite", "html", "<p>{{content}}</p>", "html"},
		{"invite", "html", "<p>{{link}}\x00</p>", "html"},
		{"invite", "mjml", strings.Repeat("x", MaxEmailFileBytes) + "{{link}}", "mjml"},
	} {
		err := CheckEmailHTML(c.part, c.field, c.doc)
		if got := field(err); got != c.want || (c.want == "") != (err == nil) {
			t.Errorf("%s %s %.30q: %v", c.part, c.field, c.doc, err)
		}
	}
}

func TestEmailMJMLSaveAndRemove(t *testing.T) {
	ctx := context.Background()
	b, cfg, path := newService(t, "version: 1\n# keep me\n", nil)
	dir := filepath.Join(filepath.Dir(path), "emails")

	if _, err := b.SaveEmailMJML(ctx, "root", "", "nope", layoutMJML, layoutHTML); err == nil {
		t.Fatal("unknown part saved")
	}
	if _, err := b.SaveEmailMJML(ctx, "root", "", "layout", layoutMJML, "<p>none</p>"); field(err) != "html" {
		t.Fatalf("layout without {{content}}: %v", err)
	}
	if _, err := b.SaveEmailMJML(ctx, "root", "", "invite", inviteMJML, "<p>no link</p>"); field(err) != "html" {
		t.Fatalf("override without {{link}}: %v", err)
	}
	if got := files(t, dir); len(got) != 0 {
		t.Fatalf("rejected saves wrote %v", got)
	}

	src, err := b.SaveEmailMJML(ctx, "root", "", "layout", layoutMJML, layoutHTML)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(src.MJMLFile, "layout-") || !strings.HasSuffix(src.MJMLFile, ".mjml") ||
		strings.TrimSuffix(src.MJMLFile, ".mjml") != strings.TrimSuffix(src.HTMLFile, ".html") {
		t.Fatalf("%+v", src)
	}
	y, _ := os.ReadFile(path)
	if !strings.Contains(string(y), "mjml: emails/"+src.MJMLFile) || !strings.Contains(string(y), "html: emails/"+src.HTMLFile) ||
		!strings.Contains(string(y), "# keep me") {
		t.Fatalf("file:\n%s", y)
	}
	if got := files(t, dir); !slices.Equal(got, []string{src.HTMLFile, src.MJMLFile}) {
		t.Fatalf("files %v", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, src.HTMLFile)); string(b) != layoutHTML {
		t.Fatalf("html: %s", b)
	}
	v := cfg.View(func(string) bool { return true })
	for _, k := range v.Keys {
		if k.Key.Key == "auth.email.layout.html" && (!k.Set || k.Value != "emails/"+src.HTMLFile) {
			t.Fatalf("view: %+v", k)
		}
	}
	got, err := b.EmailMJML(ctx, "layout")
	if err != nil || !got.Set || got.MJML != layoutMJML {
		t.Fatalf("%v %+v", err, got)
	}

	// A new version replaces the files; the old ones go.
	src2, err := b.SaveEmailMJML(ctx, "root", "", "layout", strings.Replace(layoutMJML, "mj-text>", "mj-text >", 1), layoutHTML+" ")
	if err != nil {
		t.Fatal(err)
	}
	if got := files(t, dir); !slices.Equal(got, []string{src2.HTMLFile, src2.MJMLFile}) {
		t.Fatalf("after replace: %v", got)
	}
	// Saving the same thing again keeps it.
	if _, err := b.SaveEmailMJML(ctx, "root", "", "layout", strings.Replace(layoutMJML, "mj-text>", "mj-text >", 1), layoutHTML+" "); err != nil {
		t.Fatal(err)
	}
	if got := files(t, dir); len(got) != 2 {
		t.Fatalf("same save: %v", got)
	}

	// A per-email override sits beside the layout.
	inv, err := b.SaveEmailMJML(ctx, "root", "", "invite", inviteMJML, inviteHTML)
	if err != nil {
		t.Fatal(err)
	}
	y, _ = os.ReadFile(path)
	if !strings.Contains(string(y), "invite:") || !strings.Contains(string(y), "emails/"+inv.HTMLFile) {
		t.Fatalf("file:\n%s", y)
	}

	// A stale ETag writes nothing and leaves no files behind.
	if _, err := b.SaveEmailMJML(ctx, "root", `"stale"`, "verification", inviteMJML, inviteHTML+"<!-- v -->"); err == nil {
		t.Fatal("stale save")
	}
	if got := files(t, dir); len(got) != 4 {
		t.Fatalf("stale save left %v", got)
	}

	if err := b.RemoveEmailMJML(ctx, "root", "", "layout"); err != nil {
		t.Fatal(err)
	}
	if got := files(t, dir); !slices.Equal(got, []string{inv.HTMLFile, inv.MJMLFile}) {
		t.Fatalf("after remove: %v", got)
	}
	y, _ = os.ReadFile(path)
	if strings.Contains(string(y), "layout") {
		t.Fatalf("layout still in the file:\n%s", y)
	}
	if got, err := b.EmailMJML(ctx, "layout"); err != nil || got.Set {
		t.Fatalf("%v %+v", err, got)
	}
	// Removing what isn't there is fine.
	if err := b.RemoveEmailMJML(ctx, "root", "", "layout"); err != nil {
		t.Fatal(err)
	}

	// A missing file the config names is reported.
	_ = os.Remove(filepath.Join(dir, inv.MJMLFile))
	if _, err := b.EmailMJML(ctx, "invite"); err == nil {
		t.Fatal("missing file read")
	}
}

func TestEmailMJMLOff(t *testing.T) {
	b := NewBrandingService(Options{})
	if _, err := b.SaveEmailMJML(context.Background(), "root", "", "layout", layoutMJML, layoutHTML); err == nil {
		t.Fatal("saved without a config file")
	}
	if _, err := b.DefaultMJML(context.Background(), "root", tok, "layout"); err == nil {
		t.Fatal("default without auth")
	}
}

func TestDefaultMJMLAndPreviewHTML(t *testing.T) {
	ctx := context.Background()
	fe := &fakeEmail{}
	b, _, _ := newService(t, "version: 1\n", fe)
	src, err := b.DefaultMJML(ctx, "root", tok, "invite")
	if err != nil || src.MJML != "<mjml><!-- invite --></mjml>" || fe.actor != "at-1" {
		t.Fatalf("%v %+v %s", err, src, fe.actor)
	}
	if _, err := b.DefaultMJML(ctx, "root", tok, "../x"); err == nil {
		t.Fatal("bad part")
	}
	bad := "<p>nothing</p>"
	if _, err := b.Preview(ctx, "root", tok, authapi.EmailPreviewRequest{Template: "invite", LayoutHTML: &bad}); field(err) != "layout_html" {
		t.Fatalf("layout_html: %v", err)
	}
	if _, err := b.Preview(ctx, "root", tok, authapi.EmailPreviewRequest{Template: "invite", HTML: &bad}); field(err) != "html" {
		t.Fatalf("html: %v", err)
	}
	ok := layoutHTML
	if _, err := b.Preview(ctx, "root", tok, authapi.EmailPreviewRequest{Template: "invite", LayoutHTML: &ok}); err != nil || *fe.preview.LayoutHTML != ok {
		t.Fatalf("%v", err)
	}
}
