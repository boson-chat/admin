package nickclaim

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/bosonapi"
	"github.com/boson-chat/admin/internal/services/stackconfig"
)

type fakeClient struct {
	status    *bosonapi.NickClaimStatus
	statusErr error
	calls     int
	sent      *bosonapi.NickClaimTestRequest
	actor     string
}

func (f *fakeClient) Status(context.Context) (*bosonapi.NickClaimStatus, error) {
	f.calls++
	return f.status, f.statusErr
}

func (f *fakeClient) Test(_ context.Context, actor string, req bosonapi.NickClaimTestRequest) (*bosonapi.NickClaimTestResult, error) {
	f.sent, f.actor = &req, actor
	return &bosonapi.NickClaimTestResult{OK: true, Message: "Connected", Messages: 1}, nil
}

func ptr[T any](v T) *T { return &v }

func TestStatus(t *testing.T) {
	ctx := context.Background()
	v := New(Options{}).Status(ctx)
	if v.Configured || v.ErrorCode != "nick_claim_api_off" || !strings.Contains(v.Error, "isn't reachable from the admin panel") || v.PasswordEnv != PasswordEnv {
		t.Fatalf("off: %+v", v)
	}
	f := &fakeClient{status: &bosonapi.NickClaimStatus{Enabled: true, EmailDomain: "nick.example.com"}}
	if v := New(Options{Client: f}).Status(ctx); !v.Configured || v.Status == nil || v.Error != "" {
		t.Fatalf("on: %+v", v)
	}
	f.status, f.statusErr = nil, apperr.New(502, "nick_claim_api_unreachable", "the api service isn't reachable")
	if v := New(Options{Client: f}).Status(ctx); v.Status != nil || v.ErrorCode != "nick_claim_api_unreachable" {
		t.Fatalf("unreachable: %+v", v)
	}
	f.statusErr = errors.New("boom")
	if v := New(Options{Client: f}).Status(ctx); v.ErrorCode != "nick_claim_status_failed" || v.Error != "boom" {
		t.Fatalf("plain error: %+v", v)
	}
}

func TestTestValidates(t *testing.T) {
	ctx := context.Background()
	if _, err := New(Options{}).Test(ctx, "root", TestRequest{}); err != ErrOff {
		t.Fatalf("off: %v", err)
	}
	f := &fakeClient{}
	s := New(Options{Client: f})
	for name, in := range map[string]*TestInbound{
		stackconfig.KeyNickClaimHost:     {Host: ptr("not a host!")},
		stackconfig.KeyNickClaimPort:     {Port: ptr(70000)},
		stackconfig.KeyNickClaimProtocol: {Protocol: ptr("imap")},
		stackconfig.KeyNickClaimUser:     {User: ptr("")},
		"password":                       {Password: ptr("two\nlines")},
	} {
		_, err := s.Test(ctx, "root", TestRequest{Inbound: in})
		if e, ok := apperr.As(err); !ok || e.Status != 400 || e.Field != name {
			t.Errorf("%s: %v", name, err)
		}
	}
	if f.sent != nil {
		t.Fatal("an invalid test reached the api")
	}

	_, err := s.Test(ctx, "root", TestRequest{Inbound: &TestInbound{Host: ptr(" POP.Example.com. "), Port: ptr(995), TLS: ptr(true), User: ptr(" reg@example.com "), Password: ptr("")}})
	if err != nil || f.actor != "root" {
		t.Fatalf("test: %v", err)
	}
	in := f.sent.Inbound
	if *in.Host != "pop.example.com" || *in.User != "reg@example.com" || *in.Port != 995 || !*in.TLS || in.Password != nil {
		t.Fatalf("cleaned: %+v", in)
	}
	// A password for another server goes along as it is.
	if _, err := s.Test(ctx, "root", TestRequest{Inbound: &TestInbound{Password: ptr(" s3cret ")}}); err != nil || *f.sent.Inbound.Password != " s3cret " {
		t.Fatalf("password: %v", err)
	}
	// No inbound: the api tests what it uses.
	if _, err := s.Test(ctx, "root", TestRequest{}); err != nil || f.sent.Inbound != nil {
		t.Fatalf("empty: %v %+v", err, f.sent)
	}
}

func lookup(m map[string]any) stackconfig.Lookup {
	return func(k string) (any, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestCheck(t *testing.T) {
	ctx := context.Background()
	const (
		on     = stackconfig.KeyNickClaimEnabled
		domain = stackconfig.KeyNickClaimEmailDomain
		host   = stackconfig.KeyNickClaimHost
	)
	none := lookup(nil)
	full := lookup(map[string]any{on: true, domain: "nick.example.com", host: "pop.example.com"})

	s := New(Options{})
	if err := s.Check(ctx, lookup(map[string]any{on: false}), none); err != nil {
		t.Fatalf("off needs nothing: %v", err)
	}
	if err := s.Check(ctx, full, none); err != nil {
		t.Fatalf("all set: %v", err)
	}
	err := s.Check(ctx, lookup(map[string]any{on: true}), none)
	if e, ok := apperr.As(err); !ok || e.Status != 400 || e.Field != domain ||
		!strings.Contains(e.Message, "email domain and a mail server") {
		t.Fatalf("nothing set: %v", err)
	}
	err = s.Check(ctx, lookup(map[string]any{on: true, domain: "nick.example.com"}), none)
	if e, ok := apperr.As(err); !ok || e.Field != host || strings.Contains(e.Message, "email domain") {
		t.Fatalf("no host: %v", err)
	}

	// Keys absent from the file fall back to the api's env defaults.
	f := &fakeClient{status: &bosonapi.NickClaimStatus{EmailDomain: "env.example.com", Inbound: bosonapi.NickClaimInbound{Host: "pop.env.example.com"}}}
	s = New(Options{Client: f})
	if err := s.Check(ctx, lookup(map[string]any{on: true}), none); err != nil || f.calls != 1 {
		t.Fatalf("env defaults: %v (%d calls)", err, f.calls)
	}
	// …but not when the change removes the key from the file (the api
	// reports the file's value then).
	err = s.Check(ctx, lookup(map[string]any{on: true, domain: "nick.example.com"}), lookup(map[string]any{host: "pop.example.com"}))
	if e, ok := apperr.As(err); !ok || e.Field != host {
		t.Fatalf("host removed: %v", err)
	}
	// An empty value is as good as none.
	err = s.Check(ctx, lookup(map[string]any{on: true, domain: " ", host: "pop.example.com"}), none)
	if e, ok := apperr.As(err); !ok || e.Field != domain {
		t.Fatalf("blank domain: %v", err)
	}
	// The api down: nothing to fall back on.
	f.status, f.statusErr = nil, errors.New("down")
	if err := s.Check(ctx, lookup(map[string]any{on: true}), none); err == nil {
		t.Fatal("api down: allowed")
	}
}
