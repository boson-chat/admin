package directory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/bosonapi"
)

// TestAgainstDirectory runs the panel's directory code against a real
// boson directory (github.com/boson-chat/directory) started with
// ENV=development DNS_VERIFIER=fake and an operator token:
//
//	DIRECTORY_TEST_URL=http://127.0.0.1:8080 DIRECTORY_TEST_TOKEN=… go test -run TestAgainstDirectory ./internal/services/directory/
func TestAgainstDirectory(t *testing.T) {
	base, token := os.Getenv("DIRECTORY_TEST_URL"), os.Getenv("DIRECTORY_TEST_TOKEN")
	if base == "" || token == "" {
		t.Skip("DIRECTORY_TEST_URL / DIRECTORY_TEST_TOKEN not set")
	}
	ctx := context.Background()
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	host := "irc-" + hex.EncodeToString(b) + ".ci.boson.chat"
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	d := NewDirectoryService(Options{Client: bosonapi.NewDirectoryClient(base, 10*time.Second, nil), Registration: true,
		Secret: secret, Hostname: host})

	st := d.Status(ctx, "")
	if st.Listing != nil || st.Public != nil || st.Unsupported != "" || st.ListingError != "" || st.PublicError != "" {
		t.Fatalf("before: %+v", st)
	}
	l, err := d.Submit(ctx, "root", bosonapi.Submission{Hostname: host, Port: 6697, Name: "Live test", Languages: []string{"en"}})
	if err != nil || l.Network.ID == "" || l.Network.VerificationStatus != "pending" || l.Verification == nil ||
		l.Verification.RecordName != "_boson."+host || len(l.Verification.RecordValue) < len("boson-verify=x") || l.Network.RegisteredAt == nil {
		t.Fatalf("submit: %v %+v %+v", err, l, l.Verification)
	}
	id := l.Network.ID
	if again, err := d.Submit(ctx, "root", bosonapi.Submission{Hostname: host, Port: 6697, Name: "Live test"}); err != nil || again.Network.ID != id {
		t.Fatalf("idempotent submit: %v %+v", err, again)
	}
	other := NewDirectoryService(Options{Client: bosonapi.NewDirectoryClient(base, 10*time.Second, nil), Registration: true, Secret: []byte("other")})
	if _, err := other.Submit(ctx, "eve", bosonapi.Submission{Hostname: host, Port: 6697, Name: "Mine"}); err == nil {
		t.Fatal("another key took the hostname")
	} else if e, _ := apperr.As(err); e.Code != "hostname_taken" {
		t.Fatalf("other key: %v", err)
	}
	st = d.Status(ctx, host)
	if st.Listing == nil || st.Listing.Network.ID != id || st.Public != nil || st.Listing.Verification == nil {
		t.Fatalf("pending status: %+v", st)
	}
	r, err := d.Verify(ctx, "root", host, id)
	if err != nil || !r.Success || r.Network == nil || r.Network.VerificationStatus != "verified" || len(r.Report.Results) == 0 {
		t.Fatalf("verify: %v %+v", err, r)
	}
	st = d.Status(ctx, host)
	if st.Public == nil || st.Public.ID != id || st.Listing.Network.LastVerifiedAt == nil {
		t.Fatalf("verified status: %+v", st)
	}
	if l, err := d.Update(ctx, "root", host, id, bosonapi.Submission{Name: "Live renamed"}); err != nil || l.Network.Name != "Live renamed" {
		t.Fatalf("update: %v %+v", err, l)
	}
	if l, err := d.Regenerate(ctx, "root", host, id); err != nil || l.Network.VerificationStatus != "pending" || l.Verification == nil {
		t.Fatalf("regenerate: %v %+v", err, l)
	}
	if err := other.Unlist(ctx, "eve", host, id); err == nil {
		t.Fatal("foreign key deleted")
	}
	if err := d.Unlist(ctx, "root", host, id); err != nil {
		t.Fatalf("unlist: %v", err)
	}
	if st := d.Status(ctx, host); st.Listing != nil || st.Public != nil {
		t.Fatalf("after unlist: %+v", st)
	}

	// Operator API.
	loc := NewLocalService(LocalOptions{Client: bosonapi.NewOperatorClient(base, token, 10*time.Second, nil), URL: base})
	nets, err := loc.OwnedNetworks(ctx, "usr_nobody")
	if err != nil || len(nets) != 0 {
		t.Fatalf("owned: %v %+v", err, nets)
	}
	fed := loc.Federation(ctx)
	if fed.Unsupported != "" {
		t.Logf("peers API missing (a directory without federation): %s", fed.Unsupported)
		return
	}
	if fed.Error != "" {
		t.Fatalf("federation: %+v", fed)
	}
	if fed.Settings != nil {
		liveSettings(t, loc, fed)
		return
	}
	if fed.MainPeer == nil {
		t.Logf("no builtin peer (DIRECTORY_INCLUDE_MAIN=false or a directory without federation)")
		if _, err := loc.SetIncludeMain(ctx, "root", true); err == nil {
			t.Fatal("switch without a main peer")
		}
		return
	}
	was := fed.MainPeer.Enabled
	fed, err = loc.SetIncludeMain(ctx, "root", !was)
	if err != nil || fed.MainPeer.Enabled == was {
		t.Fatalf("toggle: %v %+v", err, fed)
	}
	if again := loc.Federation(ctx); again.MainPeer == nil || again.MainPeer.Enabled == was {
		t.Fatalf("toggle didn't stick: %+v", again)
	}
	if fed, err = loc.SetIncludeMain(ctx, "root", was); err != nil || fed.MainPeer.Enabled != was {
		t.Fatalf("toggle back: %v %+v", err, fed)
	}
	bad := NewLocalService(LocalOptions{Client: bosonapi.NewOperatorClient(base, "wrong", 10*time.Second, nil)})
	if f := bad.Federation(ctx); f.Error == "" {
		t.Fatalf("wrong token accepted: %+v", f)
	}
}

// liveSettings exercises the directory >= 1.4.0 settings API: include_main
// both ways (also with no builtin peer yet), name, and restores both.
func liveSettings(t *testing.T, loc LocalServiceImpl, fed *Federation) {
	t.Helper()
	ctx := context.Background()
	st := fed.Settings
	if st.IsMain {
		if _, err := loc.SetIncludeMain(ctx, "root", true); err == nil {
			t.Fatal("include_main on the main directory")
		}
	} else {
		was := st.Settings.IncludeMain
		f, err := loc.SetIncludeMain(ctx, "root", !was)
		if err != nil || f.Settings.Settings.IncludeMain == was || (f.MainPeer != nil) == was {
			t.Fatalf("toggle include_main: %v %+v", err, f.Settings)
		}
		if f, err = loc.SetIncludeMain(ctx, "root", was); err != nil || f.Settings.Settings.IncludeMain != was {
			t.Fatalf("toggle back: %v %+v", err, f.Settings)
		}
	}
	name, orig := "Live test directory", st.Settings.Name
	f, err := loc.UpdateSettings(ctx, "root", SettingsUpdate{Name: &name})
	if err != nil || f.Settings.Settings.Name != name {
		t.Fatalf("rename: %v %+v", err, f)
	}
	if _, err := loc.UpdateSettings(ctx, "root", SettingsUpdate{Name: &orig}); err != nil {
		t.Fatalf("restore name: %v", err)
	}
	if _, err := loc.CheckPeer(ctx, "http://127.0.0.1:1"); err == nil {
		t.Fatal("unreachable peer checked fine")
	}
}
