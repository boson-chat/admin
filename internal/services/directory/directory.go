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
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/bosonapi"
	"github.com/boson-chat/admin/internal/logger"
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
	// Branding, when set, is the stack's theme document (auth's
	// /v1/branding, absolute image URLs): pushed to the listing after
	// every submit and edit, and by PushBranding. ok=false: no branding.
	Branding func(ctx context.Context) (doc map[string]any, ok bool, err error)
	// Channels, when set, is the stack's network channels (irc.channels):
	// the auto-join ones and all of them, sent on every submit and edit and
	// by PushChannels. ok=false: unknown (no config file), not sent.
	Channels func() (autojoin, all []string, ok bool)
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
	// PushChannels sends the network channels to this network's listing,
	// if it has one and they differ. nil, nil: nothing to push to.
	PushChannels(ctx context.Context, actor string) (*bosonapi.ChannelsSync, error)
}

type DirectoryService struct {
	o Options

	mu sync.Mutex
	// pushed: per listing, the hash of the document the directory last
	// accepted ("-" for cleared), so an unchanged theme isn't re-sent.
	pushed map[string]string
}

func NewDirectoryService(o Options) DirectoryServiceImpl {
	if o.Port == 0 {
		o.Port = 6697
	}
	return &DirectoryService{o: o, pushed: map[string]string{}}
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
	key := d.Key(s.Hostname)
	l, cs, err := d.withChannels(&s, func(s bosonapi.Submission) (*bosonapi.Listing, error) { return d.o.Client.Submit(ctx, key, s) })
	d.log(ctx, actor, "submit", s.Hostname, err)
	if err == nil && l != nil {
		l.ChannelsSync = cs
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
	key := d.Key(hostname)
	l, cs, err := d.withChannels(&s, func(s bosonapi.Submission) (*bosonapi.Listing, error) { return d.o.Client.Update(ctx, key, id, s) })
	d.log(ctx, actor, "update", hostname, err)
	if err == nil && l != nil {
		l.ChannelsSync = cs
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

// syncBranding puts the stack's theme document on the listing (the
// directory hot-links auth's image URLs), or clears it when the network
// has no branding. A directory without the theme API is reported, not an
// error.
func (d *DirectoryService) syncBranding(ctx context.Context, actor, hostname, id string) *bosonapi.BrandingSync {
	if d.o.Branding == nil || id == "" {
		return nil
	}
	doc, ok, err := d.o.Branding(ctx)
	if err != nil {
		return &bosonapi.BrandingSync{Unsupported: apperr.IsUnsupported(err), Error: errMsg(err)}
	}
	sum := "-"
	if ok {
		b, _ := json.Marshal(doc)
		h := sha256.Sum256(b)
		sum = base64.RawURLEncoding.EncodeToString(h[:])
	}
	res := &bosonapi.BrandingSync{OK: true, Cleared: !ok}
	d.mu.Lock()
	same := d.pushed[id] == sum
	d.mu.Unlock()
	if same {
		return res
	}
	key := d.Key(hostname)
	if ok {
		err = d.o.Client.PutTheme(ctx, key, id, doc)
	} else {
		err = d.o.Client.DeleteTheme(ctx, key, id)
	}
	if err != nil {
		res = &bosonapi.BrandingSync{Unsupported: apperr.IsUnsupported(err), Error: errMsg(err)}
		logger.FromCtx(ctx).Warn().Err(err).Str("actor", actor).Str("action", "directory.branding").Str("hostname", hostname).Msg("directory action")
		return res
	}
	d.mu.Lock()
	d.pushed[id] = sum
	d.mu.Unlock()
	d.log(ctx, actor, "branding", hostname, nil)
	return res
}

// ---- network channels ----

// MaxListedChannels: the directory keeps at most this many of each list.
const MaxListedChannels = 50

// channels is the stack's network channels for the listing (each list cut
// to what the directory keeps). ok=false: not known, don't send.
func (d *DirectoryService) channels() (autojoin, all []string, ok bool) {
	if d.o.Channels == nil {
		return nil, nil, false
	}
	autojoin, all, ok = d.o.Channels()
	if !ok {
		return nil, nil, false
	}
	if autojoin == nil {
		autojoin = []string{}
	}
	if all == nil {
		all = []string{}
	}
	return autojoin[:min(len(autojoin), MaxListedChannels)], all[:min(len(all), MaxListedChannels)], true
}

// withChannels sends s with the network channels. A directory that
// refuses them (400) gets s again without them, so a channel problem never
// blocks the listing edit; one that ignores them (older) is unsupported.
func (d *DirectoryService) withChannels(s *bosonapi.Submission, send func(bosonapi.Submission) (*bosonapi.Listing, error)) (*bosonapi.Listing, *bosonapi.ChannelsSync, error) {
	aj, all, ok := d.channels()
	if !ok {
		l, err := send(*s)
		return l, nil, err
	}
	s.AutojoinChannels, s.Channels = &aj, &all
	l, err := send(*s)
	if e, isErr := apperr.As(err); isErr && e.Status == http.StatusBadRequest {
		s.AutojoinChannels, s.Channels = nil, nil
		l, err2 := send(*s)
		return l, &bosonapi.ChannelsSync{Error: e.Message}, err2
	}
	if err != nil || l == nil {
		return l, nil, err
	}
	return l, channelsSync(l), nil
}

func channelsSync(l *bosonapi.Listing) *bosonapi.ChannelsSync {
	if !l.Network.HasChannels {
		return &bosonapi.ChannelsSync{Unsupported: true}
	}
	return &bosonapi.ChannelsSync{OK: true}
}

// channelsDiffer: the listing doesn't have these channels (or the fields).
func channelsDiffer(l *bosonapi.Server, autojoin, all []string) bool {
	return !l.HasChannels || !slices.Equal(l.AutojoinChannels, autojoin) || !slices.Equal(l.Channels, all)
}

func (d *DirectoryService) PushChannels(ctx context.Context, actor string) (*bosonapi.ChannelsSync, error) {
	aj, all, ok := d.channels()
	if !ok || !d.registration() {
		return nil, nil
	}
	host := d.hostname()
	if host == "" {
		return nil, nil
	}
	key := d.Key(host)
	l, err := d.o.Client.Lookup(ctx, key, host)
	if err != nil || l == nil || l.Network.ID == "" {
		return nil, err
	}
	if !channelsDiffer(&l.Network, aj, all) {
		return &bosonapi.ChannelsSync{OK: true}, nil
	}
	l, err = d.o.Client.Update(ctx, key, l.Network.ID, bosonapi.Submission{AutojoinChannels: &aj, Channels: &all})
	if err != nil {
		logger.FromCtx(ctx).Warn().Err(err).Str("actor", actor).Str("action", "directory.channels").Str("hostname", host).Msg("directory action")
		return &bosonapi.ChannelsSync{Unsupported: apperr.IsUnsupported(err), Error: errMsg(err)}, nil
	}
	d.log(ctx, actor, "channels", host, nil)
	return channelsSync(l), nil
}

// WatchChannels keeps the listing's channels in step with the config file:
// it pushes once at start and again after each kick (call kick on every
// config change, a panel save or a hand edit) when the names changed since
// the last push the directory took. Pushes run one at a time until ctx ends.
func WatchChannels(ctx context.Context, d DirectoryServiceImpl, names func() (autojoin, all []string, ok bool)) (kick func()) {
	kicks := make(chan struct{}, 1)
	kick = func() {
		select {
		case kicks <- struct{}{}:
		default:
		}
	}
	go func() {
		var last string
		for {
			select {
			case <-ctx.Done():
				return
			case <-kicks:
			}
			aj, all, ok := names()
			if !ok {
				continue
			}
			cur := strings.Join(aj, ",") + "|" + strings.Join(all, ",")
			if cur == last {
				continue
			}
			pctx, cancel := context.WithTimeout(ctx, time.Minute)
			res, err := d.PushChannels(pctx, "config-file")
			cancel()
			switch {
			case err != nil:
				logger.FromCtx(ctx).Warn().Err(err).Msg("pushing network channels to the directory")
			case res != nil && (res.OK || res.Unsupported):
				last = cur
			case res != nil:
				logger.FromCtx(ctx).Warn().Str("error", res.Error).Msg("pushing network channels to the directory")
			}
		}
	}()
	kick()
	return kick
}
