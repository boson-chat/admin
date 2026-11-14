// Package directory lists this network in the boson network directory
// (directory.boson.chat by default, github.com/boson-chat/directory) so boson
// clients can discover it, and, when the stack runs its own directory,
// manages that one's federation (local.go).
//
// A self-hosted network has its own auth service, so its admins have no
// boson.chat account. The panel registers the network as itself, proving
// it twice:
//
//   - a management key, derived here from the panel's secret and the
//     hostname (HMAC-SHA256), sent as "Authorization: Network <key>"; the
//     directory stores only its hash, so only this panel can edit the
//     listing, and nothing needs storing on this side;
//   - the DNS TXT record _boson.<hostname> = "boson-verify=<token>", the same
//     check the directory runs for every listing (and re-runs daily).
//
// Registration off (admin.directory_registration: false in the stack config
// file, else DIRECTORY_REGISTRATION=false) leaves the page only reporting
// whether the network is listed.
package directory

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/bosonapi"
	"github.com/boson-chat/admin/internal/logger"
	"github.com/boson-chat/admin/internal/theme"
)

type Options struct {
	Client bosonapi.DirectoryClientImpl
	APIURL string
	// Registration turns network registration (the owner API) on.
	Registration bool
	// Live, when set, gives the current registration switch and hostname
	// (admin.directory_registration / admin.directory_hostname in the
	// stack config file, else the env) and wins over Registration/Hostname.
	Live func() (registration bool, hostname string)
	// Secret derives the management keys (DIRECTORY_KEY, else SESSION_KEY).
	Secret []byte
	// Defaults for the form (the public IRC host).
	Hostname string
	Port     int
	// Branding, when set, is the stack's theme and images: pushed to the
	// listing after every submit and edit, and by PushBranding.
	Branding func(ctx context.Context) (*theme.Bundle, error)
}

// Status is GET /api/directory.
type Status struct {
	APIURL       string `json:"api_url"`
	Registration bool   `json:"registration"`
	Hostname     string `json:"hostname"`
	Port         int    `json:"port"`
	// Public is the verified public listing for Hostname, if any.
	Public      *bosonapi.Server `json:"public,omitempty"`
	PublicError string           `json:"public_error,omitempty"`
	// Listing is this panel's own listing (any status), if registration is on.
	Listing      *bosonapi.Listing `json:"listing,omitempty"`
	ListingError string            `json:"listing_error,omitempty"`
	// Unsupported: the directory doesn't have the /v1 owner API (an older
	// directory, or DIRECTORY_API_URL still points at the boson API).
	Unsupported string `json:"unsupported,omitempty"`
}

type DirectoryServiceImpl interface {
	Status(ctx context.Context, hostname string) *Status
	Submit(ctx context.Context, actor string, s bosonapi.Submission) (*bosonapi.Listing, error)
	Update(ctx context.Context, actor, hostname, id string, s bosonapi.Submission) (*bosonapi.Listing, error)
	Verify(ctx context.Context, actor, hostname, id string) (*bosonapi.VerifyResult, error)
	Regenerate(ctx context.Context, actor, hostname, id string) (*bosonapi.Listing, error)
	Unlist(ctx context.Context, actor, hostname, id string) error
	// PushBranding pushes the stack's theme to this network's listing, if
	// it has one (after a branding change). nil, nil: nothing to push to.
	PushBranding(ctx context.Context, actor string) (*bosonapi.BrandingSync, error)
}

type DirectoryService struct {
	o Options

	mu sync.Mutex
	// pushed: per listing, what the directory last accepted (theme JSON
	// hash and image hashes by slot), so unchanged images aren't re-sent.
	pushed map[string]map[string]string
}

func NewDirectoryService(o Options) DirectoryServiceImpl {
	if o.Port == 0 {
		o.Port = 6697
	}
	return &DirectoryService{o: o, pushed: map[string]map[string]string{}}
}

func (d *DirectoryService) registration() bool {
	if d.o.Live != nil {
		on, _ := d.o.Live()
		return on
	}
	return d.o.Registration
}

func (d *DirectoryService) hostname() string {
	if d.o.Live != nil {
		_, h := d.o.Live()
		return h
	}
	return d.o.Hostname
}

// Key is the management key for hostname.
func (d *DirectoryService) Key(hostname string) string {
	m := hmac.New(sha256.New, d.o.Secret)
	m.Write([]byte("boson-admin directory key v1\x00" + strings.ToLower(hostname)))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func errMsg(err error) string {
	if e, ok := apperr.As(err); ok {
		return e.Message
	}
	return "the directory didn't answer"
}

func (d *DirectoryService) Status(ctx context.Context, hostname string) *Status {
	if hostname == "" {
		hostname = d.hostname()
	}
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	reg := d.registration()
	st := &Status{APIURL: d.o.APIURL, Registration: reg, Hostname: hostname, Port: d.o.Port}
	if hostname == "" {
		return st
	}
	if reg {
		l, err := d.o.Client.Lookup(ctx, d.Key(hostname), hostname)
		switch {
		case apperr.IsUnsupported(err):
			st.Unsupported = errMsg(err)
		case err != nil:
			st.ListingError = errMsg(err)
		default:
			st.Listing = l
		}
	}
	if st.Listing == nil || st.Listing.Network.VerificationStatus == "verified" {
		name := ""
		if st.Listing != nil {
			name = st.Listing.Network.Name
		}
		p, err := d.o.Client.FindPublic(ctx, hostname, name)
		if err != nil {
			st.PublicError = errMsg(err)
		}
		st.Public = p
	}
	return st
}

var (
	hostRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	tagRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,23}$`)
	langRe = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})?$`)
)

func optionalHTTPS(v *string, name string) error {
	if v == nil || *v == "" {
		return nil
	}
	*v = strings.TrimSpace(*v)
	u, err := url.Parse(*v)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return apperr.Invalid(name + " must be an https:// URL")
	}
	return nil
}

func clean(s *bosonapi.Submission, create bool) error {
	if create {
		s.Hostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s.Hostname), "."))
		if net.ParseIP(s.Hostname) != nil || !hostRe.MatchString(s.Hostname) {
			return apperr.Invalid("the IRC hostname must be a public DNS name like irc.example.org (not an IP address); its TXT record proves you run it")
		}
		if s.Port < 1 || s.Port > 65535 {
			return apperr.Invalid("the port must be between 1 and 65535")
		}
	} else {
		s.Hostname, s.Port, s.TLS = "", 0, nil // fixed once listed
	}
	s.Name = strings.TrimSpace(s.Name)
	if (create || s.Name != "") && (s.Name == "" || len([]rune(s.Name)) > 64) {
		return apperr.Invalid("the name must be 1–64 characters")
	}
	if s.Description != nil {
		*s.Description = strings.TrimSpace(*s.Description)
		if len([]rune(*s.Description)) > 500 {
			return apperr.Invalid("the description must be at most 500 characters")
		}
	}
	if len(s.Tags) > 8 {
		return apperr.Invalid("at most 8 tags")
	}
	for i, t := range s.Tags {
		s.Tags[i] = strings.ToLower(strings.TrimSpace(t))
		if !tagRe.MatchString(s.Tags[i]) {
			return apperr.Invalid("tags are lowercase words like gaming or open-source")
		}
	}
	if len(s.Languages) > 5 {
		return apperr.Invalid("at most 5 languages")
	}
	for _, l := range s.Languages {
		if !langRe.MatchString(l) {
			return apperr.Invalid("languages are codes like en or pt-BR")
		}
	}
	if err := optionalHTTPS(s.WebsiteURL, "the website"); err != nil {
		return err
	}
	return optionalHTTPS(s.AuthURL, "the sign-in URL")
}

var errOff = apperr.New(http.StatusConflict, "directory_registration_off",
	"directory registration is off; switch it on in the panel (Directory page) to list this network")

func (d *DirectoryService) log(ctx context.Context, actor, action, hostname string, err error) {
	ev := logger.FromCtx(ctx).Info()
	if err != nil {
		ev = logger.FromCtx(ctx).Warn().Err(err)
	}
	ev.Str("actor", actor).Str("action", "directory."+action).Str("hostname", hostname).Msg("directory action")
}

func (d *DirectoryService) Submit(ctx context.Context, actor string, s bosonapi.Submission) (*bosonapi.Listing, error) {
	if !d.registration() {
		return nil, errOff
	}
	if err := clean(&s, true); err != nil {
		return nil, err
	}
	if s.TLS == nil {
		t := true
		s.TLS = &t
	}
	l, err := d.o.Client.Submit(ctx, d.Key(s.Hostname), s)
	d.log(ctx, actor, "submit", s.Hostname, err)
	if err == nil && l != nil {
		l.BrandingSync = d.syncBranding(ctx, actor, s.Hostname, l.Network.ID)
	}
	return l, err
}

func (d *DirectoryService) Update(ctx context.Context, actor, hostname, id string, s bosonapi.Submission) (*bosonapi.Listing, error) {
	if !d.registration() {
		return nil, errOff
	}
	if err := clean(&s, false); err != nil {
		return nil, err
	}
	l, err := d.o.Client.Update(ctx, d.Key(hostname), id, s)
	d.log(ctx, actor, "update", hostname, err)
	if err == nil && l != nil {
		l.BrandingSync = d.syncBranding(ctx, actor, hostname, id)
	}
	return l, err
}

func (d *DirectoryService) Verify(ctx context.Context, actor, hostname, id string) (*bosonapi.VerifyResult, error) {
	if !d.registration() {
		return nil, errOff
	}
	r, err := d.o.Client.Verify(ctx, d.Key(hostname), id)
	d.log(ctx, actor, "verify", hostname, err)
	return r, err
}

func (d *DirectoryService) Regenerate(ctx context.Context, actor, hostname, id string) (*bosonapi.Listing, error) {
	if !d.registration() {
		return nil, errOff
	}
	l, err := d.o.Client.RegenerateToken(ctx, d.Key(hostname), id)
	d.log(ctx, actor, "regenerate_token", hostname, err)
	return l, err
}

func (d *DirectoryService) Unlist(ctx context.Context, actor, hostname, id string) error {
	if !d.registration() {
		return errOff
	}
	err := d.o.Client.Unlist(ctx, d.Key(hostname), id)
	d.log(ctx, actor, "unlist", hostname, err)
	return err
}

// ---- branding ----

func (d *DirectoryService) PushBranding(ctx context.Context, actor string) (*bosonapi.BrandingSync, error) {
	if d.o.Branding == nil || !d.registration() {
		return nil, nil
	}
	host := d.hostname()
	if host == "" {
		return nil, nil
	}
	l, err := d.o.Client.Lookup(ctx, d.Key(host), host)
	if err != nil || l == nil || l.Network.ID == "" {
		return nil, err
	}
	return d.syncBranding(ctx, actor, host, l.Network.ID), nil
}

// syncBranding pushes the theme (JSON, without image URLs) and each image
// slot to the listing: changed images are uploaded, removed ones deleted.
// A directory without the theme API is reported, not an error.
func (d *DirectoryService) syncBranding(ctx context.Context, actor, hostname, id string) *bosonapi.BrandingSync {
	if d.o.Branding == nil || id == "" {
		return nil
	}
	b, err := d.o.Branding(ctx)
	if err != nil {
		if e, ok := apperr.As(err); ok && e.Code == "config_file_off" {
			return nil // no stack config file: no branding to push
		}
		return &bosonapi.BrandingSync{Error: errMsg(err)}
	}
	key := d.Key(hostname)
	d.mu.Lock()
	last := d.pushed[id]
	d.mu.Unlock()
	next := map[string]string{}
	res := &bosonapi.BrandingSync{}
	fail := func(err error) *bosonapi.BrandingSync {
		if apperr.IsUnsupported(err) {
			res.Unsupported, res.Error = true, errMsg(err)
		} else {
			res.Error = errMsg(err)
		}
		logger.FromCtx(ctx).Warn().Err(err).Str("actor", actor).Str("action", "directory.branding").Str("hostname", hostname).Msg("directory action")
		return res
	}

	themeJSON, _ := json.Marshal(b.Theme)
	sum := sha256.Sum256(themeJSON)
	next["theme"] = base64.RawURLEncoding.EncodeToString(sum[:])
	if last == nil || last["theme"] != next["theme"] {
		if err := d.o.Client.PutTheme(ctx, key, id, b.Theme); err != nil {
			return fail(err)
		}
	}
	var problems []string
	for _, slot := range theme.Slots {
		ds := theme.DirectorySlot(slot)
		img, has := b.Images[slot]
		switch {
		case has && last != nil && last[slot] == img.Hash:
			next[slot] = img.Hash
		case has:
			if err := d.o.Client.PutThemeImage(ctx, key, id, ds, img.ContentType, img.Data); err != nil {
				if apperr.IsUnsupported(err) {
					return fail(err)
				}
				problems = append(problems, ds+": "+errMsg(err))
				continue
			}
			next[slot] = img.Hash
			res.Images++
		case last == nil || last[slot] != "":
			if err := d.o.Client.DeleteThemeImage(ctx, key, id, ds); err != nil {
				if apperr.IsUnsupported(err) {
					return fail(err)
				}
				// logo_wide is a panel addition a directory may not know.
				if slot != "logo_wide" {
					problems = append(problems, ds+": "+errMsg(err))
				}
			}
		}
	}
	d.mu.Lock()
	d.pushed[id] = next
	d.mu.Unlock()
	if len(problems) > 0 {
		res.Error = "some images weren't accepted: " + strings.Join(problems, "; ")
		logger.FromCtx(ctx).Warn().Str("actor", actor).Str("action", "directory.branding").Str("hostname", hostname).Strs("problems", problems).Msg("directory action")
		return res
	}
	res.OK = true
	d.log(ctx, actor, "branding", hostname, nil)
	return res
}
