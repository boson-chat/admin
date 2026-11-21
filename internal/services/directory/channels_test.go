package directory

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/boson-chat/admin/internal/clients/bosonapi"
)

func TestChannelsSentWithTheListing(t *testing.T) {
	f, srv := newFakeV1(t)
	f.channelsAPI = true
	aj, all, known := []string{"#lobby"}, []string{"#lobby", "#help"}, true
	d := NewDirectoryService(Options{Client: bosonapi.NewDirectoryClient(srv.URL, time.Second, nil), Registration: true,
		Secret: []byte("0123456789abcdef0123456789abcdef"), Hostname: "irc.example.org",
		Channels: func() ([]string, []string, bool) { return aj, all, known },
	})
	ctx := context.Background()

	l, err := d.Submit(ctx, "root", bosonapi.Submission{Hostname: "irc.example.org", Port: 6697, Name: "Example"})
	if err != nil {
		t.Fatal(err)
	}
	id := l.Network.ID
	if l.ChannelsSync == nil || !l.ChannelsSync.OK || !slices.Equal(l.Network.AutojoinChannels, aj) || !slices.Equal(l.Network.Channels, all) {
		t.Fatalf("submit: %+v %+v", l.ChannelsSync, l.Network)
	}

	// Unchanged: PushChannels doesn't patch.
	n := f.channelPatches
	if res, err := d.PushChannels(ctx, "root"); err != nil || !res.OK || f.channelPatches != n {
		t.Fatalf("unchanged: %v %+v %d", err, res, f.channelPatches-n)
	}
	// Changed: patched with just the channel fields.
	aj, all = []string{}, []string{"#help"}
	if res, err := d.PushChannels(ctx, "root"); err != nil || !res.OK || f.channelPatches != n+1 {
		t.Fatalf("changed: %v %+v", err, res)
	}
	if got := f.nets[id]["channels"].([]string); !slices.Equal(got, all) || len(f.nets[id]["autojoin_channels"].([]string)) != 0 || f.nets[id]["name"] != "Example" {
		t.Fatalf("listing %v", f.nets[id])
	}

	// The wire shape: string arrays, empty ones included.
	b, _ := json.Marshal(bosonapi.Submission{AutojoinChannels: &aj, Channels: &all})
	if string(b) != `{"autojoin_channels":[],"channels":["#help"]}` {
		t.Fatalf("wire %s", b)
	}
	if b, _ := json.Marshal(bosonapi.Submission{Name: "x"}); string(b) != `{"name":"x"}` {
		t.Fatalf("no channels %s", b)
	}

	// Each list is cut to the directory's 50.
	all = nil
	for i := range 60 {
		all = append(all, fmt.Sprintf("#c%d", i))
	}
	if _, err := d.PushChannels(ctx, "root"); err != nil || len(f.nets[id]["channels"].([]string)) != MaxListedChannels {
		t.Fatalf("cut: %v %d", err, len(f.nets[id]["channels"].([]string)))
	}

	// A directory that refuses them: the edit still goes through, without.
	f.rejectChannels = true
	aj = []string{"#x"}
	l, err = d.Update(ctx, "root", "irc.example.org", id, bosonapi.Submission{Name: "Renamed"})
	if err != nil || l.Network.Name != "Renamed" || l.ChannelsSync == nil || l.ChannelsSync.OK || l.ChannelsSync.Error == "" {
		t.Fatalf("rejected: %v %+v", err, l.ChannelsSync)
	}
	if res, err := d.PushChannels(ctx, "root"); err != nil || res.OK || res.Error == "" {
		t.Fatalf("push rejected: %v %+v", err, res)
	}
	f.rejectChannels = false

	// No config file: the fields aren't sent and nothing is pushed.
	known = false
	n = f.channelPatches
	if _, err := d.Update(ctx, "root", "irc.example.org", id, bosonapi.Submission{Name: "Again"}); err != nil || f.channelPatches != n {
		t.Fatalf("unknown: %v %d", err, f.channelPatches-n)
	}
	if res, err := d.PushChannels(ctx, "root"); res != nil || err != nil {
		t.Fatalf("unknown push: %v %v", res, err)
	}
}

func TestChannelsOlderDirectory(t *testing.T) {
	f, srv := newFakeV1(t) // ignores the fields
	d := NewDirectoryService(Options{Client: bosonapi.NewDirectoryClient(srv.URL, time.Second, nil), Registration: true,
		Secret: []byte("0123456789abcdef0123456789abcdef"), Hostname: "irc.example.org",
		Channels: func() ([]string, []string, bool) { return []string{"#lobby"}, []string{"#lobby"}, true },
	})
	ctx := context.Background()
	l, err := d.Submit(ctx, "root", bosonapi.Submission{Hostname: "irc.example.org", Port: 6697, Name: "Example"})
	if err != nil || l.ChannelsSync == nil || !l.ChannelsSync.Unsupported || f.channelPatches != 1 {
		t.Fatalf("submit: %v %+v", err, l.ChannelsSync)
	}
	if res, err := d.PushChannels(ctx, "root"); err != nil || !res.Unsupported {
		t.Fatalf("push: %v %+v", err, res)
	}
	// No listing: nothing to push to.
	d2 := NewDirectoryService(Options{Client: bosonapi.NewDirectoryClient(srv.URL, time.Second, nil), Registration: true,
		Secret: []byte("other-secret-0123456789abcdef0123"), Hostname: "irc.other.org",
		Channels: func() ([]string, []string, bool) { return nil, nil, true },
	})
	if res, err := d2.PushChannels(ctx, "root"); res != nil || err != nil {
		t.Fatalf("no listing: %v %v", res, err)
	}
}

type countingDir struct {
	DirectoryServiceImpl
	pushes chan []string
	res    *bosonapi.ChannelsSync
}

func (c *countingDir) PushChannels(context.Context, string) (*bosonapi.ChannelsSync, error) {
	r := c.res // read before handing over, so the test may change it after
	c.pushes <- nil
	return r, nil
}

func TestWatchChannels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &countingDir{pushes: make(chan []string, 10), res: &bosonapi.ChannelsSync{OK: true}}
	names := make(chan []string, 10)
	cur := []string{"#lobby"}
	names <- cur
	get := func() ([]string, []string, bool) {
		select {
		case n := <-names:
			cur = n
		default:
		}
		return cur, cur, true
	}
	kick := WatchChannels(ctx, c, get)
	wait := func(want bool) {
		t.Helper()
		select {
		case <-c.pushes:
			if !want {
				t.Fatal("pushed without a change")
			}
		case <-time.After(200 * time.Millisecond):
			if want {
				t.Fatal("not pushed")
			}
		}
	}
	wait(true) // at start
	kick()
	wait(false) // same names
	names <- []string{"#help"}
	kick()
	wait(true)
	// A failed push is retried at the next kick.
	c.res = &bosonapi.ChannelsSync{Error: "down"}
	names <- []string{"#x"}
	kick()
	wait(true)
	kick()
	wait(true)
}
