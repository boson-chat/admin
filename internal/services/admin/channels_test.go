package admin

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/boson-chat/admin/internal/apperr"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/clients/authapi/authapitest"
)

func TestNormalizeChannel(t *testing.T) {
	ok := map[string]string{
		"#Staff":                      "#staff",
		"  #mods ":                    "#mods",
		"#a":                          "#a",
		"#ünï-côdé":                   "#ünï-côdé",
		"#" + strings.Repeat("x", 63): "#" + strings.Repeat("x", 63),
	}
	for in, want := range ok {
		if got, err := NormalizeChannel(in); err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "staff", "#", "&local", "#a b", "#a,b", "#a\x07", "#" + strings.Repeat("x", 64)} {
		if _, err := NormalizeChannel(bad); err == nil {
			t.Errorf("%q: accepted", bad)
		} else if !isInvalid(err) {
			t.Errorf("%q: %v isn't a 400", bad, err)
		}
	}
}

func TestSetRoleChannelsCleansAndSaves(t *testing.T) {
	f, svc, _ := setup(t)
	s := login(t, svc)
	ctx := context.Background()
	out, err := svc.SetRoleChannels(ctx, s, authapitest.RoleModerator, []authapi.RoleChannel{
		{Channel: "#Staff", Autojoin: true}, {Channel: " #mods", Autojoin: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []authapi.RoleChannel{{Channel: "#mods"}, {Channel: "#staff", Autojoin: true}}
	if !slices.Equal(out, want) || !slices.Equal(f.RoleChans[authapitest.RoleModerator], want) {
		t.Fatalf("saved %+v / %+v", out, f.RoleChans)
	}
	if got, _ := svc.RoleChannels(ctx, s, authapitest.RoleModerator); !slices.Equal(got, want) {
		t.Fatalf("read back %+v", got)
	}
	// auth audits the write
	if e := f.Audit[len(f.Audit)-1]; e.Event != authapitest.EventRoleChannels || !strings.Contains(string(e.Meta), "#staff") {
		t.Fatalf("audit %+v", e)
	}
	// duplicates (after folding) and bad names are refused before auth
	n := len(f.Audit)
	if _, err := svc.SetRoleChannels(ctx, s, authapitest.RoleModerator, []authapi.RoleChannel{{Channel: "#a"}, {Channel: "#A"}}); !isInvalid(err) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := svc.SetRoleChannels(ctx, s, authapitest.RoleModerator, []authapi.RoleChannel{{Channel: "staff"}}); !isInvalid(err) {
		t.Fatalf("no #: %v", err)
	}
	if len(f.Audit) != n {
		t.Fatal("an invalid list reached auth")
	}
	many := make([]authapi.RoleChannel, MaxRoleChannels+1)
	for i := range many {
		many[i].Channel = "#c" + strings.Repeat("x", i%60) + string(rune('a'+i%26))
	}
	if _, err := svc.SetRoleChannels(ctx, s, authapitest.RoleModerator, many); !isInvalid(err) {
		t.Fatalf("too many: %v", err)
	}
}

func TestMemberRoleIsEveryone(t *testing.T) {
	f, svc, _ := setup(t)
	s := login(t, svc)
	ctx := context.Background()
	if _, err := svc.SetRoleChannels(ctx, s, authapitest.RoleMember, []authapi.RoleChannel{{Channel: "#lobby", Autojoin: true}}); err != nil {
		t.Fatal(err)
	}
	if len(f.RoleChans[authapi.EveryoneRole]) != 1 || f.RoleChans[authapitest.RoleMember] != nil {
		t.Fatalf("member's channels went to %+v", f.RoleChans)
	}
	got, err := svc.RoleChannels(ctx, s, authapi.EveryoneRole)
	if err != nil || len(got) != 1 || got[0].Channel != "#lobby" {
		t.Fatalf("everyone: %+v %v", got, err)
	}
}

func TestRoleChannelsHierarchyAndOldAuth(t *testing.T) {
	f, svc, _ := setup(t)
	s := login(t, svc)
	ctx := context.Background()
	// Admin can't change Owner's channels (auth's role_hierarchy, reworded)
	_, err := svc.SetRoleChannels(ctx, s, authapitest.RoleOwner, []authapi.RoleChannel{{Channel: "#x"}})
	if e, ok := apperr.As(err); !ok || e.Code != "role_hierarchy" || !strings.Contains(e.Message, "below your own") {
		t.Fatalf("hierarchy: %v", err)
	}
	f.NoRoleChannels = true
	if _, err := svc.RoleChannels(ctx, s, authapitest.RoleModerator); !apperr.IsUnsupported(err) {
		t.Fatalf("old auth: %v", err)
	}
	if _, err := svc.ChannelOverview(ctx, s); !apperr.IsUnsupported(err) {
		t.Fatalf("old auth overview: %v", err)
	}
}

func TestChannelAccessAndOverview(t *testing.T) {
	f, svc, _ := setup(t)
	s := login(t, svc)
	ctx := context.Background()
	f.RoleChans = map[string][]authapi.RoleChannel{
		authapitest.RoleModerator: {{Channel: "#mods", Autojoin: true}, {Channel: "#staff", Autojoin: true}},
		authapitest.RoleAdmin:     {{Channel: "#staff", Autojoin: false}},
		authapi.EveryoneRole:      {{Channel: "#lobby", Autojoin: true}},
	}
	out, err := svc.SetChannelAccess(ctx, s, []authapi.ChannelAccess{{Channel: "#Staff", Private: true}, {Channel: "#secret", Private: true}})
	if err != nil || len(out) != 2 || out[1].Channel != "#staff" {
		t.Fatalf("access: %+v %v", out, err)
	}
	if e := f.Audit[len(f.Audit)-1]; e.Event != authapitest.EventChannelAccess {
		t.Fatalf("audit %+v", e)
	}
	ov, err := svc.ChannelOverview(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, c := range ov.Channels {
		names = append(names, c.Channel)
	}
	if strings.Join(names, ",") != "#lobby,#mods,#secret,#staff" {
		t.Fatalf("channels %v", names)
	}
	staff := ov.Channels[3]
	if !staff.Private || len(staff.Roles) != 2 || staff.Roles[0].Name != "Admin" || staff.Roles[0].Autojoin || !staff.Roles[1].Autojoin {
		t.Fatalf("staff %+v", staff)
	}
	if len(ov.Channels[2].Roles) != 0 || !ov.Channels[2].Private {
		t.Fatalf("secret %+v", ov.Channels[2])
	}
	last := ov.Roles[len(ov.Roles)-1]
	if !last.Everyone || last.ID != authapi.EveryoneRole || last.Name != "Everyone" || len(last.Channels) != 1 {
		t.Fatalf("everyone %+v", last)
	}
	if _, err := svc.SetChannelAccess(ctx, s, []authapi.ChannelAccess{{Channel: "nope"}}); !isInvalid(err) {
		t.Fatalf("bad name: %v", err)
	}
}

func TestChangeDescriptions(t *testing.T) {
	got := roleChannelChanges(
		[]authapi.RoleChannel{{Channel: "#a", Autojoin: true}, {Channel: "#b"}},
		[]authapi.RoleChannel{{Channel: "#a"}, {Channel: "#c", Autojoin: true}},
	)
	if strings.Join(got, "; ") != "#a (auto-join) → (no auto-join); +#c (auto-join); -#b" {
		t.Fatalf("role changes: %q", got)
	}
	got = accessChanges(
		[]authapi.ChannelAccess{{Channel: "#a", Private: true}, {Channel: "#b", Private: true}},
		[]authapi.ChannelAccess{{Channel: "#a", Private: true}, {Channel: "#c", Private: true}},
	)
	if strings.Join(got, "; ") != "#c: public → private; #b: private → public" {
		t.Fatalf("access changes: %q", got)
	}
}

func isInvalid(err error) bool {
	e, ok := apperr.As(err)
	return ok && e.Status == 400
}
