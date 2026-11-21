package stackconfig

import (
	"fmt"
	"strings"

	"github.com/boson-chat/admin/internal/configfile"
	"gopkg.in/yaml.v3"
)

// Network channels (the network channels contract, section 1): channels
// RoleServ keeps created and registered to the network, some of which
// everyone is joined to.
//
//	irc:
//	  channels:
//	    - name: "#lobby"
//	      topic: Welcome to Boson
//	      autojoin: true
//	  channel_founder: thelion
//	  autojoin_guests: true
//
// auth is the only reader; RoleServ gets them from auth's feed.
const (
	KeyNetworkChannels = "irc.channels"
	KeyChannelFounder  = "irc.channel_founder"
	KeyAutojoinGuests  = "irc.autojoin_guests"

	MaxNetworkChannels = 100
	MaxTopic           = 300
)

// NetworkChannel is one entry of irc.channels.
type NetworkChannel struct {
	// Name as written (#, then 1–63 printable ASCII without spaces or
	// commas); compared case-insensitively.
	Name     string `json:"name" yaml:"name"`
	Topic    string `json:"topic,omitempty" yaml:"topic,omitempty"`
	Autojoin bool   `json:"autojoin" yaml:"autojoin"`
}

// FoldChannel is a channel name as Ergo matches it (ASCII casefolding).
func FoldChannel(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// ChannelName checks a network channel name and returns it trimmed.
func ChannelName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "#") || len(s) < 2 || len(s) > 64 {
		return "", fmt.Errorf("%q must be # and then 1–63 characters, like #lobby", s)
	}
	for i := 1; i < len(s); i++ {
		if c := s[i]; c <= ' ' || c > '~' || c == ',' {
			return "", fmt.Errorf("%q may only use printable ASCII after the # (no spaces or commas)", s)
		}
	}
	return s, nil
}

// accountName checks irc.channel_founder: an account name as Ergo takes
// it (no spaces, no IRC specials, not a channel).
func accountName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 64 || strings.ContainsAny(s, " ,*?!@:$%#&\t\r\n\x00") {
		return "", fmt.Errorf("%q must be an account name like thelion", s)
	}
	return s, nil
}

// cleanChannels validates irc.channels from the API's JSON shape.
func (k Key) cleanChannels(v any) ([]NetworkChannel, error) {
	var items []any
	switch x := v.(type) {
	case []any:
		items = x
	case []NetworkChannel:
		for _, c := range x {
			items = append(items, map[string]any{"name": c.Name, "topic": c.Topic, "autojoin": c.Autojoin})
		}
	default:
		return nil, fmt.Errorf("must be a list of channels")
	}
	if len(items) > k.MaxItems {
		return nil, fmt.Errorf("at most %d channels", k.MaxItems)
	}
	out := make([]NetworkChannel, 0, len(items))
	seen := map[string]bool{}
	for i, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("channel %d must have a name", i+1)
		}
		raw, _ := m["name"].(string)
		name, err := ChannelName(raw)
		if err != nil {
			return nil, fmt.Errorf("channel %d: %v", i+1, err)
		}
		f := FoldChannel(name)
		if seen[f] {
			return nil, fmt.Errorf("%s is listed twice", name)
		}
		seen[f] = true
		var topic string
		switch t := m["topic"].(type) {
		case nil:
		case string:
			topic = strings.TrimSpace(t)
		default:
			return nil, fmt.Errorf("%s: the topic must be text", name)
		}
		if len([]rune(topic)) > MaxTopic || strings.ContainsAny(topic, "\r\n\x00") {
			return nil, fmt.Errorf("%s: the topic must be at most %d characters on one line", name, MaxTopic)
		}
		var aj bool
		switch a := m["autojoin"].(type) {
		case nil:
		case bool:
			aj = a
		default:
			return nil, fmt.Errorf("%s: autojoin must be true or false", name)
		}
		out = append(out, NetworkChannel{Name: name, Topic: topic, Autojoin: aj})
	}
	return out, nil
}

// channelsFromNode reads irc.channels from the file into the API's shape.
// A bare string entry is a channel with no topic, not auto-joined.
func channelsFromNode(n *yaml.Node) ([]any, error) {
	if n.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("must be a list of {name, topic, autojoin}")
	}
	items := make([]any, 0, len(n.Content))
	for _, c := range n.Content {
		if c.Kind == yaml.ScalarNode {
			items = append(items, map[string]any{"name": c.Value})
			continue
		}
		var m map[string]any
		if c.Kind != yaml.MappingNode || c.Decode(&m) != nil {
			return nil, fmt.Errorf("must be a list of {name, topic, autojoin}")
		}
		items = append(items, m)
	}
	return items, nil
}

// channelsNode is chans as a YAML list. Entries that were in old (matched
// by folded name) keep their comments, key order and keys the panel
// doesn't know; topic goes when it's empty.
func channelsNode(old *yaml.Node, chans []NetworkChannel) *yaml.Node {
	prev := map[string]*yaml.Node{}
	if old != nil && old.Kind == yaml.SequenceNode {
		for _, c := range old.Content {
			if c.Kind != yaml.MappingNode {
				continue
			}
			if _, v := configfile.Lookup(c, []string{"name"}); v != nil && v.Kind == yaml.ScalarNode {
				prev[FoldChannel(strings.TrimSpace(v.Value))] = c
			}
		}
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, ch := range chans {
		item := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		if p := prev[FoldChannel(ch.Name)]; p != nil {
			cp := *p
			cp.Content = append([]*yaml.Node(nil), p.Content...)
			item = &cp
		}
		_ = configfile.SetNode(item, []string{"name"}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: ch.Name, Style: yaml.DoubleQuotedStyle})
		if ch.Topic != "" {
			_ = configfile.Set(item, []string{"topic"}, ch.Topic)
		} else {
			configfile.Unset(item, []string{"topic"})
		}
		_ = configfile.Set(item, []string{"autojoin"}, ch.Autojoin)
		seq.Content = append(seq.Content, item)
	}
	return seq
}

// setValue writes a cleaned value at name, list-of-maps values merged
// into what's there.
func setValue(root *yaml.Node, name string, v any) error {
	path := configfile.Split(name)
	if chans, ok := v.([]NetworkChannel); ok {
		_, old := configfile.Lookup(root, path)
		return configfile.SetNode(root, path, channelsNode(old, chans))
	}
	return configfile.Set(root, path, v)
}

// NetworkChannels is irc.channels as the file has it. ok is false without
// the config file (nothing is known); an unset or invalid key is none.
func (s *Service) NetworkChannels() (chans []NetworkChannel, ok bool) {
	if !s.Enabled() {
		return nil, false
	}
	v, _ := s.FileValue(KeyNetworkChannels)
	chans, _ = v.([]NetworkChannel)
	return chans, true
}

// ChannelNames splits network channels into the auto-join ones and all
// of them, as written, for the directory listing.
func ChannelNames(chans []NetworkChannel) (autojoin, all []string) {
	autojoin, all = []string{}, []string{}
	for _, c := range chans {
		all = append(all, c.Name)
		if c.Autojoin {
			autojoin = append(autojoin, c.Name)
		}
	}
	return autojoin, all
}
