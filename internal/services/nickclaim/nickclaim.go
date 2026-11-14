// Package nickclaim is the Nick registration page: the api's nick claims
// (auto-registering users' nicks with NickServ on other IRC networks
// through a catch-all mailbox, docs/nick-claim.md in the stack repo).
//
// The settings are nick_claim.* keys of the stack config file (saved
// through stackconfig, which checks and logs them). This service reads the
// api's internal status, runs its mailbox test with unsaved values, and
// checks that the feature isn't switched on without a domain and a mail
// server.
package nickclaim

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/bosonapi"
	"github.com/boson-chat/admin/internal/logger"
	"github.com/boson-chat/admin/internal/services/stackconfig"
)

// PasswordEnv is where the mailbox password lives (never the config file).
const PasswordEnv = "NICK_CLAIM_POP3_PASSWORD"

// ErrOff: BOSON_API_INTERNAL_URL isn't set.
var ErrOff = apperr.New(http.StatusServiceUnavailable, "nick_claim_api_off",
	"the api service isn't reachable from the admin panel: set BOSON_API_INTERNAL_URL (http://api:3001) and BOSON_API_INTERNAL_TOKEN on the admin service")

type Options struct {
	// Client is nil without BOSON_API_INTERNAL_URL.
	Client bosonapi.NickClaimClientImpl
	Now    func() time.Time
}

type ServiceImpl interface {
	Status(ctx context.Context) *View
	Test(ctx context.Context, actor string, in TestRequest) (*bosonapi.NickClaimTestResult, error)
	// Check is a stackconfig.CheckFunc for the nick_claim section.
	Check(ctx context.Context, after, before stackconfig.Lookup) error
}

type Service struct{ o Options }

func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{o: o}
}

// View is GET /api/nick-claims.
type View struct {
	// Configured: BOSON_API_INTERNAL_URL is set.
	Configured  bool   `json:"configured"`
	PasswordEnv string `json:"password_env"`
	// Status is the api's answer (nil when it couldn't be read).
	Status *bosonapi.NickClaimStatus `json:"status,omitempty"`
	// Error / ErrorCode: why Status is missing.
	Error     string    `json:"error,omitempty"`
	ErrorCode string    `json:"error_code,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// Status never fails: the page shows the form with whatever went wrong.
func (s *Service) Status(ctx context.Context) *View {
	v := &View{Configured: s.o.Client != nil, PasswordEnv: PasswordEnv, CheckedAt: s.o.Now().UTC()}
	if s.o.Client == nil {
		v.Error, v.ErrorCode = ErrOff.Message, ErrOff.Code
		return v
	}
	st, err := s.o.Client.Status(ctx)
	if err != nil {
		v.Error, v.ErrorCode = err.Error(), "nick_claim_status_failed"
		if e, ok := apperr.As(err); ok {
			v.Error, v.ErrorCode = e.Message, e.Code
		}
		return v
	}
	v.Status = st
	return v
}

// TestInbound is the form's (unsaved) mailbox. Absent fields use the
// api's current values.
type TestInbound struct {
	Protocol *string `json:"protocol"`
	Host     *string `json:"host"`
	Port     *int    `json:"port"`
	TLS      *bool   `json:"tls"`
	User     *string `json:"user"`
	// Password is for this test only (a mail server other than the one
	// in use); it is never saved or logged.
	Password *string `json:"password"`
}

// TestRequest is POST /api/nick-claims/test.
type TestRequest struct {
	Inbound *TestInbound `json:"inbound"`
}

const maxPassword = 1024

func clean(in *TestInbound) (*bosonapi.NickClaimTestInbound, error) {
	if in == nil {
		return nil, nil
	}
	out := &bosonapi.NickClaimTestInbound{Port: in.Port, TLS: in.TLS}
	str := func(key string, v *string) (*string, error) {
		if v == nil {
			return nil, nil
		}
		k, _ := stackconfig.KeyByName(key)
		c, err := k.Clean(*v)
		if err != nil {
			return nil, apperr.InvalidField(key, err.Error())
		}
		s := c.(string)
		return &s, nil
	}
	var err error
	if out.Protocol, err = str(stackconfig.KeyNickClaimProtocol, in.Protocol); err != nil {
		return nil, err
	}
	if out.Host, err = str(stackconfig.KeyNickClaimHost, in.Host); err != nil {
		return nil, err
	}
	if out.User, err = str(stackconfig.KeyNickClaimUser, in.User); err != nil {
		return nil, err
	}
	if in.Port != nil && (*in.Port < 1 || *in.Port > 65535) {
		return nil, apperr.InvalidField(stackconfig.KeyNickClaimPort, stackconfig.KeyNickClaimPort+": must be from 1 to 65535")
	}
	if in.Password != nil {
		p := *in.Password
		if p == "" {
			// An empty box means "use the stored one".
		} else if utf8.RuneCountInString(p) > maxPassword || strings.ContainsAny(p, "\r\n\x00") {
			return nil, apperr.InvalidField("password", "the password must be at most 1024 characters on one line")
		} else {
			out.Password = &p
		}
	}
	return out, nil
}

// Test asks the api to sign in to the mailbox (and count its messages)
// with the given settings. It never reads or deletes mail.
func (s *Service) Test(ctx context.Context, actor string, in TestRequest) (*bosonapi.NickClaimTestResult, error) {
	if s.o.Client == nil {
		return nil, ErrOff
	}
	inbound, err := clean(in.Inbound)
	if err != nil {
		return nil, err
	}
	res, err := s.o.Client.Test(ctx, actor, bosonapi.NickClaimTestRequest{Inbound: inbound})
	ev := logger.FromCtx(ctx).Info()
	outcome := "error"
	switch {
	case err != nil:
		ev = logger.FromCtx(ctx).Warn().Err(err)
	case res.OK:
		outcome = "success"
	default:
		outcome = "failed"
	}
	host := ""
	if inbound != nil && inbound.Host != nil {
		host = *inbound.Host
	}
	ev.Str("actor", actor).Str("action", "nick_claim.test").Str("outcome", outcome).
		Str("host", host).Bool("own_password", inbound != nil && inbound.Password != nil).
		Msg("nick registration mailbox test")
	return res, err
}

// statusTimeout bounds the api lookup a save makes for env defaults.
const statusTimeout = 3 * time.Second

// Check refuses to switch nick registration on without an email domain
// and a mail server. A key absent from the file falls back to the api's
// env default, which only the api knows: it's asked (best effort).
func (s *Service) Check(ctx context.Context, after, before stackconfig.Lookup) error {
	if v, _ := after(stackconfig.KeyNickClaimEnabled); v != true {
		return nil
	}
	var st *bosonapi.NickClaimStatus
	asked := false
	envValue := func(key string) string {
		if !asked && s.o.Client != nil {
			asked = true
			c, cancel := context.WithTimeout(ctx, statusTimeout)
			defer cancel()
			st, _ = s.o.Client.Status(c)
		}
		if st == nil {
			return ""
		}
		if key == stackconfig.KeyNickClaimEmailDomain {
			return st.EmailDomain
		}
		return st.Inbound.Host
	}
	has := func(key string) bool {
		if v, ok := after(key); ok {
			str, _ := v.(string)
			return strings.TrimSpace(str) != ""
		}
		// Unset in the file now. If it was in the file before, the api's
		// value is that one, not its env default: don't trust it.
		if _, was := before(key); was {
			return false
		}
		return envValue(key) != ""
	}
	var missing []string
	field := ""
	for _, k := range []struct{ key, what string }{
		{stackconfig.KeyNickClaimEmailDomain, "an email domain"},
		{stackconfig.KeyNickClaimHost, "a mail server (POP3 host)"},
	} {
		if !has(k.key) {
			missing = append(missing, k.what)
			if field == "" {
				field = k.key
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return apperr.InvalidField(field, "nick registration can't be turned on without "+strings.Join(missing, " and "))
}
