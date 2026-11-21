package stackconfig

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/boson-chat/admin/internal/services/admin"
)

func TestNetworkChannelsRoundTrip(t *testing.T) {
	s, path := newSvc(t, `version: 1
irc:
  plaintext: false
  # network channels, kept by RoleServ
  channels:
    # the front door
    - name: "#Lobby"
      topic: Old topic
      x-note: keep me   # a key the panel doesn't know
      autojoin: false
    - name: "#gone"
  hand_edited: yes
`)
	chans, ok := s.NetworkChannels()
	if !ok || !reflect.DeepEqual(chans, []NetworkChannel{{Name: "#Lobby", Topic: "Old topic"}, {Name: "#gone"}}) {
		t.Fatalf("read %v %v", chans, ok)
	}
	_, err := s.Update(context.Background(), "alice", "", Change{Set: map[string]any{
		KeyNetworkChannels: []any{
			map[string]any{"name": "#lobby", "topic": " Welcome to Boson ", "autojoin": true},
			map[string]any{"name": "#help", "topic": ""},
		},
		KeyChannelFounder: "thelion",
		KeyAutojoinGuests: false,
	}}, all)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	file := string(b)
	for _, want := range []string{
		"# network channels, kept by RoleServ",
		"# the front door",
		`- name: "#lobby"`,
		"topic: Welcome to Boson",
		"x-note: keep me # a key the panel doesn't know",
		"autojoin: true",
		`- name: "#help"`,
		"hand_edited: yes",
		"channel_founder: thelion",
		"autojoin_guests: false",
	} {
		if !strings.Contains(file, want) {
			t.Errorf("missing %q in:\n%s", want, file)
		}
	}
	if strings.Contains(file, "#gone") || strings.Contains(file, "Old topic") || strings.Count(file, "topic:") != 1 {
		t.Errorf("stale entries in:\n%s", file)
	}
	// Key order of the kept entry stays.
	if i, j := strings.Index(file, "x-note"), strings.Index(file, "autojoin: true"); i < 0 || j < i {
		t.Errorf("order changed:\n%s", file)
	}
	chans, _ = s.NetworkChannels()
	want := []NetworkChannel{{Name: "#lobby", Topic: "Welcome to Boson", Autojoin: true}, {Name: "#help"}}
	if !reflect.DeepEqual(chans, want) {
		t.Fatalf("after write %v", chans)
	}
	aj, names := ChannelNames(chans)
	if !reflect.DeepEqual(aj, []string{"#lobby"}) || !reflect.DeepEqual(names, []string{"#lobby", "#help"}) {
		t.Fatalf("names %v %v", aj, names)
	}

	// Writing an empty list keeps the key (no channels), unset removes it.
	if _, err := s.Update(context.Background(), "alice", "", Change{Set: map[string]any{KeyNetworkChannels: []any{}}}, all); err != nil {
		t.Fatal(err)
	}
	if chans, ok := s.NetworkChannels(); !ok || len(chans) != 0 {
		t.Fatalf("empty %v", chans)
	}
	v := s.View(all)
	for _, k := range v.Keys {
		if k.Key.Key == KeyNetworkChannels && (!k.Set || !reflect.DeepEqual(k.Value, []NetworkChannel{})) {
			t.Fatalf("view %+v", k)
		}
	}
}

func TestNetworkChannelsBareNamesAndNewFile(t *testing.T) {
	s, _ := newSvc(t, "version: 1\nirc:\n  channels: [\"#lobby\"]\n")
	if chans, _ := s.NetworkChannels(); !reflect.DeepEqual(chans, []NetworkChannel{{Name: "#lobby"}}) {
		t.Fatalf("bare %v", chans)
	}
	s2, path2 := newSvc(t, "")
	if _, err := s2.Update(context.Background(), "alice", "", Change{Set: map[string]any{
		KeyNetworkChannels: []any{map[string]any{"name": "#lobby", "autojoin": true}},
	}}, all); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path2)
	if !strings.Contains(string(b), "irc:\n    channels:\n        - name: \"#lobby\"\n          autojoin: true\n") &&
		!strings.Contains(string(b), "irc:\n  channels:\n    - name: \"#lobby\"\n      autojoin: true\n") {
		t.Fatalf("new file:\n%s", b)
	}
}

func TestNetworkChannelsValidation(t *testing.T) {
	k, _ := KeyByName(KeyNetworkChannels)
	if k.Perm != admin.PermSettingsManage || k.Services[0] != "auth" {
		t.Fatalf("key %+v", k)
	}
	ch := func(name string, extra ...any) map[string]any {
		m := map[string]any{"name": name}
		for i := 0; i+1 < len(extra); i += 2 {
			m[extra[i].(string)] = extra[i+1]
		}
		return m
	}
	for _, good := range []string{"#a", "#lobby", "#Boson-Dev", "#" + strings.Repeat("x", 63), "##chat", "#a[b]"} {
		if _, err := k.Clean([]any{ch(good)}); err != nil {
			t.Errorf("%q: %v", good, err)
		}
	}
	for name, bad := range map[string][]any{
		"no hash":      {ch("lobby")},
		"just hash":    {ch("#")},
		"too long":     {ch("#" + strings.Repeat("x", 64))},
		"space":        {ch("#a b")},
		"comma":        {ch("#a,b")},
		"non-ascii":    {ch("#café")},
		"control":      {ch("#a\x07")},
		"duplicate":    {ch("#Lobby"), ch("#lobby")},
		"long topic":   {ch("#a", "topic", strings.Repeat("t", 301))},
		"topic lines":  {ch("#a", "topic", "a\nb")},
		"topic type":   {ch("#a", "topic", 5.0)},
		"autojoin str": {ch("#a", "autojoin", "yes")},
		"not a map":    {"#a"},
	} {
		if _, err := k.Clean(bad); err == nil {
			t.Errorf("%s: accepted %v", name, bad)
		}
	}
	if _, err := k.Clean(map[string]any{}); err == nil {
		t.Error("a map accepted")
	}
	many := make([]any, MaxNetworkChannels+1)
	for i := range many {
		many[i] = ch("#c" + strings.Repeat("x", i%50) + string(rune('a'+i%26)) + string(rune('a'+i/26)))
	}
	if _, err := k.Clean(many); err == nil || !strings.Contains(err.Error(), "at most 100") {
		t.Errorf("101 channels: %v", err)
	}
	if v, err := k.Clean([]any{ch("#a", "topic", strings.Repeat("é", 300))}); err != nil || len(v.([]NetworkChannel)) != 1 {
		t.Errorf("300 rune topic: %v", err)
	}

	f, _ := KeyByName(KeyChannelFounder)
	if v, err := f.Clean(" thelion "); err != nil || v != "thelion" {
		t.Errorf("founder: %v %v", v, err)
	}
	for _, bad := range []string{"", "two words", "#chan", "a@b", strings.Repeat("a", 65)} {
		if _, err := f.Clean(bad); err == nil {
			t.Errorf("founder %q accepted", bad)
		}
	}
	g, _ := KeyByName(KeyAutojoinGuests)
	if _, err := g.Clean("true"); err == nil {
		t.Error("autojoin_guests took a string")
	}
}

func TestNetworkChannelsInvalidInFile(t *testing.T) {
	s, _ := newSvc(t, "version: 1\nirc:\n  channels:\n    - name: lobby\n")
	if chans, ok := s.NetworkChannels(); !ok || chans != nil {
		t.Fatalf("invalid %v %v", chans, ok)
	}
	v := s.View(all)
	found := false
	for _, is := range v.Issues {
		found = found || (is.Key == KeyNetworkChannels && is.Line == 4)
	}
	if !found {
		t.Fatalf("issues %+v", v.Issues)
	}
	off := New(Options{})
	if _, ok := off.NetworkChannels(); ok {
		t.Fatal("no config file: ok")
	}
}
