package branding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/configfile"
	"github.com/boson-chat/admin/internal/logger"
	"github.com/boson-chat/admin/internal/services/stackconfig"
	"gopkg.in/yaml.v3"
)

// Raw MJML editing (branding contract, section 6). The panel's browser
// compiles the MJML (mjml-browser); the BFF keeps both the source (for
// re-editing) and the compiled HTML (what auth sends) as
// config/emails/<part>-<hash>.{mjml,html}, named in boson.yaml:
//
//	auth.email.layout.{mjml,html}               the layout every email is wrapped in
//	auth.email.templates.<t>.{mjml,html}        one email's whole document
//
// Values are written as emails/<file>.

// MaxEmailFileBytes caps each MJML source and compiled HTML file.
const MaxEmailFileBytes = 512 << 10

// EmailParts are the MJML parts: the layout, then each template.
var EmailParts = append([]string{stackconfig.EmailLayout}, stackconfig.EmailTemplates...)

// EmailSource is a part's saved MJML (Set false: none, auth uses Boson's).
type EmailSource struct {
	Part     string `json:"part"`
	Set      bool   `json:"set"`
	MJML     string `json:"mjml,omitempty"`
	MJMLFile string `json:"mjml_file,omitempty"`
	HTMLFile string `json:"html_file,omitempty"`
}

var (
	contentRe = regexp.MustCompile(`\{\{\s*content\s*\}\}`)
	linkRe    = regexp.MustCompile(`\{\{\s*link\s*\}\}`)
	errPart   = apperr.Invalid("unknown email part; one of " + strings.Join(EmailParts, ", "))
)

func checkPart(part string) error {
	if !slices.Contains(EmailParts, part) {
		return errPart
	}
	return nil
}

// CheckEmailHTML checks one part's document (MJML source or compiled
// HTML) for its required placeholder: the layout holds {{content}}
// exactly once, an email's own override {{link}}. field names the request
// field in the error.
func CheckEmailHTML(part, field, doc string) error {
	what := "the compiled HTML"
	if field == "mjml" {
		what = "the MJML"
	}
	if len(doc) > MaxEmailFileBytes {
		return apperr.InvalidField(field, fmt.Sprintf("%s is too large (512 KB at most)", what))
	}
	if !utf8.ValidString(doc) || strings.ContainsRune(doc, 0) {
		return apperr.InvalidField(field, what+" must be UTF-8 text")
	}
	if strings.TrimSpace(doc) == "" {
		return apperr.InvalidField(field, what+" is empty")
	}
	if part == stackconfig.EmailLayout {
		if n := len(contentRe.FindAllStringIndex(doc, -1)); n != 1 {
			return apperr.InvalidField(field, fmt.Sprintf("the layout must contain {{content}} exactly once (%s has it %d times): auth puts each email's text there", what, n))
		}
		return nil
	}
	if !linkRe.MatchString(doc) {
		return apperr.InvalidField(field, what+" must contain {{link}}: it's the link the email is for")
	}
	return nil
}

// emailFiles reads part's two file values from a root mapping (nil-safe);
// invalid ones read as "".
func emailFiles(root *yaml.Node, part string) (mjml, html string) {
	get := func(ext string) string {
		_, n := configfile.Lookup(root, configfile.Split(stackconfig.EmailFileKey(part, ext)))
		if n == nil || n.Kind != yaml.ScalarNode {
			return ""
		}
		name, err := stackconfig.EmailFileName(n.Value)
		if err != nil {
			return ""
		}
		return name
	}
	return get("mjml"), get("html")
}

// emailFilesInUse is every email file the config names.
func emailFilesInUse(root *yaml.Node) []string {
	var out []string
	for _, p := range EmailParts {
		m, h := emailFiles(root, p)
		out = append(out, m, h)
	}
	return out
}

func (b *BrandingService) emailsOn() bool {
	return b.o.Config != nil && b.o.Config.Enabled() && b.o.EmailsDir != ""
}

func emailHash(mjml, html string) string {
	h := sha256.New()
	h.Write([]byte(mjml))
	h.Write([]byte{0})
	h.Write([]byte(html))
	return hex.EncodeToString(h.Sum(nil)[:8])
}

func (b *BrandingService) EmailMJML(_ context.Context, part string) (*EmailSource, error) {
	if !b.emailsOn() {
		return nil, errOff
	}
	if err := checkPart(part); err != nil {
		return nil, err
	}
	out := &EmailSource{Part: part}
	snap := b.o.Config.Snapshot()
	if snap == nil {
		return out, nil
	}
	m, h := emailFiles(snap.Root(), part)
	if m == "" {
		return out, nil
	}
	data, err := os.ReadFile(filepath.Join(b.o.EmailsDir, m))
	if err != nil {
		return nil, apperr.New(http.StatusConflict, "email_file_missing", "the config names "+stackconfig.EmailsPrefix+m+" but the panel can't read it; save the MJML again or remove it")
	}
	if len(data) > MaxEmailFileBytes {
		return nil, apperr.New(http.StatusConflict, "email_file_too_large", stackconfig.EmailsPrefix+m+" is larger than 512 KB")
	}
	out.Set, out.MJML, out.MJMLFile, out.HTMLFile = true, string(data), m, h
	return out, nil
}

func (b *BrandingService) SaveEmailMJML(ctx context.Context, actor, ifMatch, part, mjml, html string) (*EmailSource, error) {
	if !b.emailsOn() {
		return nil, errOff
	}
	if err := checkPart(part); err != nil {
		return nil, err
	}
	err := CheckEmailHTML(part, "mjml", mjml)
	if err == nil {
		err = CheckEmailHTML(part, "html", html)
	}
	if err != nil {
		logger.FromCtx(ctx).Info().Str("actor", actor).Str("action", "email.mjml.save").Str("part", part).
			Str("outcome", "rejected").Err(err).Msg("email MJML")
		return nil, err
	}
	if err := os.MkdirAll(b.o.EmailsDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", b.o.EmailsDir, err)
	}
	hash := emailHash(mjml, html)
	names := [2]string{part + "-" + hash + ".mjml", part + "-" + hash + ".html"}
	var wrote []string
	for i, data := range []string{mjml, html} {
		path := filepath.Join(b.o.EmailsDir, names[i])
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := writeAtomic(path, []byte(data)); err != nil {
			b.removeEmailFiles(ctx, wrote, nil)
			return nil, err
		}
		wrote = append(wrote, names[i])
	}
	var old []string
	var after []string
	mk, hk := stackconfig.EmailFileKey(part, "mjml"), stackconfig.EmailFileKey(part, "html")
	err = b.o.Config.Write(ctx, actor, ifMatch, "email.mjml.save", []string{mk, hk},
		func(root *yaml.Node) ([]string, error) {
			om, oh := emailFiles(root, part)
			old = []string{om, oh}
			for i, k := range []string{mk, hk} {
				if err := configfile.Set(root, configfile.Split(k), stackconfig.EmailsPrefix+names[i]); err != nil {
					return nil, apperr.Invalid(err.Error())
				}
			}
			after = emailFilesInUse(root)
			return []string{
				fmt.Sprintf("%s: %s → %s%s (%d bytes)", mk, orNone(om), stackconfig.EmailsPrefix, names[0], len(mjml)),
				fmt.Sprintf("%s: %s → %s%s (%d bytes)", hk, orNone(oh), stackconfig.EmailsPrefix, names[1], len(html)),
			}, nil
		})
	if err != nil {
		var inUse []string
		if snap := b.o.Config.Snapshot(); snap != nil {
			inUse = emailFilesInUse(snap.Root())
		}
		b.removeEmailFiles(ctx, wrote, inUse)
		return nil, err
	}
	b.removeEmailFiles(ctx, old, after)
	return &EmailSource{Part: part, Set: true, MJML: mjml, MJMLFile: names[0], HTMLFile: names[1]}, nil
}

func (b *BrandingService) RemoveEmailMJML(ctx context.Context, actor, ifMatch, part string) error {
	if !b.emailsOn() {
		return errOff
	}
	if err := checkPart(part); err != nil {
		return err
	}
	var old, after []string
	mk, hk := stackconfig.EmailFileKey(part, "mjml"), stackconfig.EmailFileKey(part, "html")
	err := b.o.Config.Write(ctx, actor, ifMatch, "email.mjml.remove", []string{mk, hk},
		func(root *yaml.Node) ([]string, error) {
			var changes []string
			for _, k := range []string{mk, hk} {
				if _, n := configfile.Lookup(root, configfile.Split(k)); n != nil {
					if n.Kind == yaml.ScalarNode {
						if name, err := stackconfig.EmailFileName(n.Value); err == nil {
							old = append(old, name)
						}
					}
					configfile.Unset(root, configfile.Split(k))
					changes = append(changes, k+": "+n.Value+" → (none)")
				}
			}
			after = emailFilesInUse(root)
			return changes, nil
		})
	if err != nil {
		return err
	}
	b.removeEmailFiles(ctx, old, after)
	return nil
}

// removeEmailFiles deletes files no key names any more.
func (b *BrandingService) removeEmailFiles(ctx context.Context, files, inUse []string) {
	for _, f := range files {
		if f == "" || f != filepath.Base(f) || strings.HasPrefix(f, ".") || slices.Contains(inUse, f) {
			continue
		}
		if err := os.Remove(filepath.Join(b.o.EmailsDir, f)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			logger.FromCtx(ctx).Warn().Err(err).Str("file", f).Msg("removing a replaced email file")
		}
	}
}

func (b *BrandingService) DefaultMJML(ctx context.Context, actor string, with TokenCall, part string) (*EmailSource, error) {
	if b.o.Email == nil {
		return nil, errEmailOff
	}
	if err := checkPart(part); err != nil {
		return nil, err
	}
	var src string
	err := with(func(at string) error {
		var err error
		src, err = b.o.Email.DefaultMJML(ctx, at, part)
		return err
	})
	if err != nil {
		return nil, err
	}
	if len(src) > MaxEmailFileBytes {
		return nil, apperr.New(http.StatusBadGateway, "upstream_error", "the auth service's default MJML is larger than 512 KB")
	}
	return &EmailSource{Part: part, MJML: src}, nil
}

// checkPreviewHTML checks the unsaved compiled HTML a preview carries.
func checkPreviewHTML(in *authapi.EmailPreviewRequest) error {
	if in.LayoutHTML != nil {
		if err := CheckEmailHTML(stackconfig.EmailLayout, "layout_html", *in.LayoutHTML); err != nil {
			return err
		}
	}
	if in.HTML != nil {
		if err := CheckEmailHTML(in.Template, "html", *in.HTML); err != nil {
			return err
		}
	}
	return nil
}
