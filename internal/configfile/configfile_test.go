package configfile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const example = `# boson stack config
# second header line

version: 1
auth:
  registration: invite # who may sign up
  # A comment above a key.
  channel_expiry_days: 30
future_service:
  knob: [1, 2, 3]

# ---- reference ----
# voice:
#   rooms_strict: true
`

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newStore(t *testing.T, content string) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "boson.yaml")
	if content != "" {
		write(t, path, content)
	}
	at := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	return Open(path, Options{Header: "# header for a new file", Now: func() time.Time { return at }}), path
}

func set(key string, v any) Edit {
	return func(root *yaml.Node) error { return Set(root, Split(key), v) }
}

func TestUpdateKeepsCommentsOrderAndUnknownKeys(t *testing.T) {
	s, path := newStore(t, example)
	snap, err := s.Update("", "alice", func(root *yaml.Node) error {
		if err := Set(root, Split("auth.registration"), "closed"); err != nil {
			return err
		}
		if err := Set(root, Split("admin.tls_check_hosts"), []string{"irc.example.org:6697"}); err != nil {
			return err
		}
		Unset(root, Split("auth.channel_expiry_days"))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	out := string(got)
	for _, want := range []string{
		"# boson stack config\n# second header line\n",
		"registration: closed # who may sign up",
		"# A comment above a key.", // moved, not lost
		"future_service:\n  knob: [1, 2, 3]",
		"admin:\n  tls_check_hosts:\n    - irc.example.org:6697",
		"# ---- reference ----\n# voice:\n#   rooms_strict: true",
		"version: 1 # last saved in the admin panel by alice at 2026-11-14T12:00:00Z",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "channel_expiry_days") {
		t.Errorf("unset key still there:\n%s", out)
	}
	if strings.Index(out, "auth:") > strings.Index(out, "future_service:") {
		t.Errorf("order changed:\n%s", out)
	}
	if snap.SavedBy != "alice" || !snap.SavedAt.Equal(time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("saved by %q at %v", snap.SavedBy, snap.SavedAt)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("temp files left behind: %v", entries)
	}
}

func TestUpdateCreatesFileWithHeaderAndVersion(t *testing.T) {
	s, path := newStore(t, "")
	if snap, _ := s.Current(); snap.Exists {
		t.Fatal("missing file reported as existing")
	}
	if _, err := s.Update("", "bob", set("admin.directory_registration", false)); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	want := "# header for a new file\n\nversion: 1 # last saved in the admin panel by bob at 2026-11-14T12:00:00Z\nadmin:\n  directory_registration: false\n"
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestKeepsModeOfExistingFile(t *testing.T) {
	s, path := newStore(t, "version: 1\n")
	_ = os.Chmod(path, 0o640)
	if _, err := s.Update("", "a", set("network.name", "X")); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
}

func TestIfMatch(t *testing.T) {
	s, _ := newStore(t, "version: 1\n")
	snap, _ := s.Current()
	if _, err := s.Update(`"stale"`, "a", set("network.name", "X")); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale etag: %v", err)
	}
	snap2, err := s.Update(snap.ETag, "a", set("network.name", "X"))
	if err != nil {
		t.Fatal(err)
	}
	if snap2.ETag == snap.ETag {
		t.Fatal("etag didn't change")
	}
	if _, err := s.Update(snap.ETag, "a", set("network.name", "Y")); !errors.Is(err, ErrConflict) {
		t.Fatalf("old etag after a save: %v", err)
	}
}

func TestParseErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		line int
		msg  string
	}{
		"syntax":    {"version: 1\nauth:\n  registration: open\n bad: [\n", 3, "expected key"},
		"tabs":      {"version: 1\nauth:\n\tregistration: open\n", 3, "cannot start any token"},
		"list root": {"- a\n- b\n", 1, "top level must be a mapping"},
		"version":   {"version: 2\n", 1, "version must be 1"},
		"duplicate": {"version: 1\nauth:\n  registration: open\n  registration: closed\n", 4, "auth.registration is set twice"},
		"two docs":  {"version: 1\n---\nversion: 1\n", 2, "more than one YAML document"},
	} {
		t.Run(name, func(t *testing.T) {
			_, p := Parse([]byte(tc.src))
			if p == nil {
				t.Fatal("no error")
			}
			if p.Line != tc.line || !strings.Contains(p.Message, tc.msg) {
				t.Fatalf("got %+v", p)
			}
		})
	}
	for _, ok := range []string{"", "# only comments\n", "version: 1\n", "auth:\n"} {
		if _, p := Parse([]byte(ok)); p != nil {
			t.Errorf("%q: %v", ok, p)
		}
	}
}

// A file broken by hand keeps the last good snapshot in use, refuses
// saves (so the broken file isn't overwritten), and recovers once fixed.
func TestCorruptFileKeepsLastGoodAndRecovers(t *testing.T) {
	s, path := newStore(t, "version: 1\nnetwork:\n  name: Good\n")
	var changes int
	s.OnChange(func(*Snapshot) { changes++ })
	write(t, path, "version: 1\nnetwork:\n  name: [unclosed\n")
	if s.Reload() {
		t.Fatal("a broken file was taken")
	}
	snap, p := s.Current()
	if p == nil || p.Line == 0 {
		t.Fatalf("problem %+v", p)
	}
	if _, v := Lookup(snap.Root(), Split("network.name")); v == nil || v.Value != "Good" {
		t.Fatalf("last good lost: %v", v)
	}
	if raw, _ := s.Disk(); !strings.Contains(string(raw), "unclosed") {
		t.Fatal("Disk should show the broken file")
	}
	_, err := s.Update("", "a", set("network.name", "Other"))
	var inv *InvalidError
	if !errors.As(err, &inv) {
		t.Fatalf("save over a broken file: %v", err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "unclosed") {
		t.Fatal("the broken file was overwritten")
	}
	write(t, path, "version: 1\nnetwork:\n  name: Fixed\n")
	if !s.Reload() {
		t.Fatal("fixed file not taken")
	}
	snap, p = s.Current()
	if _, v := Lookup(snap.Root(), Split("network.name")); p != nil || v.Value != "Fixed" {
		t.Fatalf("after fix: %v %v", p, v)
	}
	if changes != 1 {
		t.Fatalf("OnChange calls: %d", changes)
	}
}

// A file that's broken at startup leaves the defaults in use.
func TestCorruptAtStartup(t *testing.T) {
	s, _ := newStore(t, "version: 1\nauth: [\n")
	snap, p := s.Current()
	if p == nil || snap.Root() != nil {
		t.Fatalf("snap %+v problem %v", snap, p)
	}
}

// Concurrent saves from several admins (tabs) are serialised: none is lost.
func TestConcurrentUpdates(t *testing.T) {
	s, path := newStore(t, example)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			if _, err := s.Update("", fmt.Sprintf("admin%d", i), set(fmt.Sprintf("test.key%d", i), i)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	raw, _ := os.ReadFile(path)
	doc, p := Parse(raw)
	if p != nil {
		t.Fatal(p)
	}
	for i := range 20 {
		if _, v := Lookup(doc.Content[0], Split(fmt.Sprintf("test.key%d", i))); v == nil {
			t.Errorf("key%d lost", i)
		}
	}
	if !strings.Contains(string(raw), "who may sign up") {
		t.Error("comments lost")
	}
}

// A hand edit made after the last reload isn't clobbered by a save.
func TestUpdateRereadsDisk(t *testing.T) {
	s, path := newStore(t, "version: 1\n")
	write(t, path, "version: 1\n# hand edit\nirc:\n  plaintext: true\n")
	if _, err := s.Update("", "a", set("network.name", "X")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "plaintext: true") || !strings.Contains(string(b), "# hand edit") {
		t.Fatalf("hand edit lost:\n%s", b)
	}
}

func TestWatchPicksUpRenames(t *testing.T) {
	s, path := newStore(t, "version: 1\n")
	got := make(chan string, 4)
	s.OnChange(func(snap *Snapshot) {
		_, v := Lookup(snap.Root(), Split("network.name"))
		if v != nil {
			got <- v.Value
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Watch(ctx, 200*time.Millisecond, nil)
	time.Sleep(50 * time.Millisecond)
	// An editor's save: write elsewhere, rename over.
	tmp := path + ".swp"
	write(t, tmp, "version: 1\nnetwork:\n  name: Renamed\n")
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	select {
	case v := <-got:
		if v != "Renamed" {
			t.Fatalf("got %q", v)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("change not picked up")
	}
}

func TestSetIntoEmptySectionAndStyles(t *testing.T) {
	doc, _ := Parse([]byte("version: 1\nauth:\nvoice:\n  rooms: [{id: a, name: A}] # static\nnetwork:\n  name: \"Quoted\"\n"))
	root := doc.Content[0]
	if err := Set(root, Split("auth.registration"), "open"); err != nil {
		t.Fatal(err)
	}
	_ = Set(root, Split("voice.rooms"), []map[string]string{{"id": "b", "name": "B"}})
	_ = Set(root, Split("network.name"), "New")
	if err := Set(root, Split("voice.rooms.x"), 1); err == nil {
		t.Fatal("set under a list")
	}
	b, _ := yaml.Marshal(doc)
	out := string(b)
	for _, want := range []string{"auth:\n    registration: open", "rooms: [{id: b, name: B}] # static", `name: "New"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	Unset(root, Split("auth.registration"))
	b, _ = yaml.Marshal(doc)
	if strings.Contains(string(b), "auth") {
		t.Errorf("empty section left:\n%s", b)
	}
}
