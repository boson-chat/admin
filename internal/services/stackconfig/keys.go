package stackconfig

import (
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/boson-chat/admin/config"
	"github.com/boson-chat/admin/internal/services/admin"
	"github.com/boson-chat/admin/internal/theme"
	"gopkg.in/yaml.v3"
)

// Kind is a key's value type.
type Kind string

const (
	KindBool     Kind = "bool"
	KindString   Kind = "string"
	KindInt      Kind = "int"
	KindEnum     Kind = "enum"
	KindList     Kind = "list"     // list of strings
	KindRooms    Kind = "rooms"    // voice.rooms: [{id, name}]
	KindDuration Kind = "duration" // 10m, 1h
	KindText     Kind = "text"     // multi-line string (email bodies)
	KindTheme    Kind = "theme"    // branding.theme: a boson-theme/1 document
	KindImages   Kind = "images"   // branding.images: slot → file (written by uploads)
)

// Key is one setting in the file (schema v1, the stack's config contract).
type Key struct {
	Key  string `json:"key"`
	Kind Kind   `json:"kind"`
	// Enum values, int bounds, list length.
	Enum     []string `json:"enum,omitempty"`
	Min      int      `json:"min,omitempty"`
	Max      int      `json:"max,omitempty"`
	MaxItems int      `json:"max_items,omitempty"`
	// Perm edits it (on top of admin.panel).
	Perm string `json:"perm"`
	// Services are the compose services that read it ("*" = all).
	Services []string `json:"services"`
	// Env is the environment variable that stays its default.
	Env string `json:"env,omitempty"`
	// Restart: only takes effect after the service restarts.
	Restart bool                         `json:"restart,omitempty"`
	item    func(string) (string, error) // list item / string check; returns the cleaned value
}

var (
	hostRe  = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	labelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	roomRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
)

// Hostname checks a public DNS name (not an IP).
func Hostname(s string) (string, error) {
	s = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
	if net.ParseIP(s) != nil || !hostRe.MatchString(s) {
		return "", fmt.Errorf("%q must be a DNS name like irc.example.org (not an IP address)", s)
	}
	return s, nil
}

// dialHost: a DNS name, a compose service name (traefik) or an IP.
func dialHost(s string) error {
	if _, err := netip.ParseAddr(strings.Trim(s, "[]")); err == nil {
		return nil
	}
	if hostRe.MatchString(s) || labelRe.MatchString(s) {
		return nil
	}
	return fmt.Errorf("%q isn't a hostname or IP address", s)
}

func port(s string) error {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("port %q must be a number from 1 to 65535", s)
	}
	return nil
}

// TLSHost checks host:port[@dial-host:dial-port] (TLS_CHECK_HOSTS).
func TLSHost(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	hp, dial, hasDial := strings.Cut(s, "@")
	check := func(v string) error {
		i := strings.LastIndex(v, ":")
		if i <= 0 || i == len(v)-1 {
			return fmt.Errorf("%q must be host:port, like irc.example.org:6697", v)
		}
		if err := dialHost(strings.Trim(v[:i], "[]")); err != nil {
			return err
		}
		return port(v[i+1:])
	}
	if err := check(hp); err != nil {
		return "", err
	}
	if hasDial {
		if err := check(dial); err != nil {
			return "", fmt.Errorf("after @: %w", err)
		}
	}
	return s, nil
}

func httpsOrigin(s string) (string, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "/")
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.User != nil {
		return "", fmt.Errorf("%q must be an origin like https://directory.example.com", s)
	}
	return s, nil
}

func httpsURL(s string) (string, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "/")
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return "", fmt.Errorf("%q must be an https:// URL", s)
	}
	return s, nil
}

func cidr(s string) (string, error) {
	s = strings.TrimSpace(s)
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked().String(), nil
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return a.String(), nil
	}
	return "", fmt.Errorf("%q must be an IP address or CIDR like 10.0.0.0/8", s)
}

var ergoDurRe = regexp.MustCompile(`^(\d+[wdhms])+$`)

// ergoDuration: Ergo's durations, like 7d or 1w2d, or 0 (never).
func ergoDuration(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s != "0" && !ergoDurRe.MatchString(s) {
		return "", fmt.Errorf("%q must be a duration like 7d, 30d or 1w (0 = keep forever)", s)
	}
	return s, nil
}

func text(max int) func(string) (string, error) {
	return func(s string) (string, error) {
		s = strings.TrimSpace(s)
		if s == "" || len([]rune(s)) > max || strings.ContainsAny(s, "\r\n\x00") {
			return "", fmt.Errorf("must be 1–%d characters on one line", max)
		}
		return s, nil
	}
}

func optionalText(max int) func(string) (string, error) {
	return func(s string) (string, error) {
		s = strings.TrimSpace(s)
		if len([]rune(s)) > max || strings.ContainsAny(s, "\r\n\x00") {
			return "", fmt.Errorf("must be at most %d characters on one line", max)
		}
		return s, nil
	}
}

func multiline(max int) func(string) (string, error) {
	return func(s string) (string, error) {
		s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
		if s == "" || len([]rune(s)) > max || strings.ContainsAny(s, "\r\x00") {
			return "", fmt.Errorf("must be 1–%d characters", max)
		}
		return s, nil
	}
}

func color(s string) (string, error) { return theme.Color(s) }

// EmailTemplates are the emails auth sends whose wording can be changed.
var EmailTemplates = []string{"verification", "password_reset", "account_setup", "invite"}

func emailKeys() []Key {
	out := []Key{
		{Key: "auth.email.sender_name", Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{auth}, item: optionalText(64)},
		{Key: "auth.email.accent", Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{auth}, item: color},
		{Key: "auth.email.footer", Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{auth}, item: optionalText(200)},
		{Key: "auth.email.logo", Kind: KindBool, Perm: admin.PermSettingsManage, Services: []string{auth}},
	}
	for _, t := range EmailTemplates {
		out = append(out,
			Key{Key: "auth.email.templates." + t + ".subject", Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{auth}, item: text(200)},
			Key{Key: "auth.email.templates." + t + ".body", Kind: KindText, Max: 5000, Perm: admin.PermSettingsManage, Services: []string{auth}, item: multiline(5000)},
		)
	}
	return out
}

// Branding keys.
const (
	KeyBrandingTheme  = "branding.theme"
	KeyBrandingImages = "branding.images"
)

const (
	auth      = "auth"
	ergo      = "ergo"
	voice     = "quicvoiced"
	dir       = "directory"
	self      = "admin"
	everybody = "*"
)

// Keys is the schema the panel edits. Other keys in the file are kept as
// they are.
var Keys = append([]Key{
	{Key: "network.name", Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{everybody}, Env: "NETWORK_NAME", item: text(64)},

	{Key: "auth.registration", Kind: KindEnum, Enum: []string{"open", "invite", "closed"}, Perm: admin.PermSettingsManage, Services: []string{auth}, Env: "REGISTRATION"},
	{Key: "auth.require_email_verification", Kind: KindBool, Perm: admin.PermSettingsManage, Services: []string{auth}, Env: "REQUIRE_EMAIL_VERIFICATION"},
	{Key: "auth.guest_access", Kind: KindBool, Perm: admin.PermSettingsManage, Services: []string{auth}},
	{Key: "auth.channel_creation", Kind: KindEnum, Enum: []string{"registered", "admins"}, Perm: admin.PermSettingsManage, Services: []string{auth}, Env: "IRC_CHANNEL_CREATION"},
	{Key: "auth.voice_channel_creation", Kind: KindEnum, Enum: []string{"admins", "founders", "registered"}, Perm: admin.PermSettingsManage, Services: []string{auth}, Env: "VOICE_CHANNEL_CREATION"},
	{Key: "auth.channel_auto_register", Kind: KindBool, Perm: admin.PermSettingsManage, Services: []string{auth}},
	{Key: "auth.channel_expiry_days", Kind: KindInt, Min: 0, Max: 3650, Perm: admin.PermSettingsManage, Services: []string{auth}},
	{Key: "auth.cors_allowed_origins", Kind: KindList, MaxItems: 20, Perm: admin.PermSettingsManage, Services: []string{auth}, Env: "AUTH_CORS_ALLOWED_ORIGINS", item: httpsOrigin},
	{Key: "auth.smtp.host", Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{auth}, Env: "SMTP_HOST", item: func(s string) (string, error) {
		s = strings.TrimSpace(s)
		return s, dialHost(s)
	}},
	{Key: "auth.smtp.port", Kind: KindInt, Min: 1, Max: 65535, Perm: admin.PermSettingsManage, Services: []string{auth}, Env: "SMTP_PORT"},
	{Key: "auth.smtp.from", Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{auth}, Env: "SMTP_FROM", item: text(254)},
	{Key: "auth.smtp.tls", Kind: KindEnum, Enum: []string{"starttls", "tls", "none"}, Perm: admin.PermSettingsManage, Services: []string{auth}, Env: "SMTP_TLS"},

	// irc.plaintext_port is left to hand edits: compose publishes the
	// container's 6667, so changing it here would silently break the mapping.
	{Key: "irc.plaintext", Kind: KindBool, Perm: admin.PermServicesManage, Services: []string{ergo}, Env: "IRC_PLAINTEXT", Restart: true},
	{Key: "irc.require_sasl_exempt", Kind: KindList, MaxItems: 20, Perm: admin.PermServicesManage, Services: []string{ergo}, Env: "IRC_REQUIRE_SASL_EXEMPT", item: cidr},
	{Key: "irc.websocket", Kind: KindBool, Perm: admin.PermServicesManage, Services: []string{ergo}, Env: "IRC_WEBSOCKET"},
	{Key: "irc.websocket_origins", Kind: KindList, MaxItems: 20, Perm: admin.PermServicesManage, Services: []string{ergo}, Env: "IRC_WEBSOCKET_ORIGINS", item: httpsOrigin},
	{Key: "irc.oauthbearer", Kind: KindBool, Perm: admin.PermServicesManage, Services: []string{ergo}, Env: "IRC_OAUTHBEARER"},
	{Key: "irc.persistent_history", Kind: KindBool, Perm: admin.PermServicesManage, Services: []string{ergo}, Env: "IRC_PERSISTENT_HISTORY", Restart: true},
	{Key: "irc.history_expire", Kind: KindString, Perm: admin.PermServicesManage, Services: []string{ergo}, item: ergoDuration},
	{Key: "irc.log_level", Kind: KindEnum, Enum: []string{"debug", "info", "warn", "error"}, Perm: admin.PermServicesManage, Services: []string{ergo}},
	{Key: "irc.roles_poll_seconds", Kind: KindInt, Min: 1, Max: 3600, Perm: admin.PermServicesManage, Services: []string{ergo}, Env: "ROLES_POLL_SECONDS"},
	{Key: "irc.settings_poll_seconds", Kind: KindInt, Min: 1, Max: 3600, Perm: admin.PermServicesManage, Services: []string{ergo}, Env: "SETTINGS_POLL_SECONDS"},

	{Key: "voice.rooms", Kind: KindRooms, MaxItems: 50, Perm: admin.PermSettingsManage, Services: []string{voice}, Env: "VOICE_ROOMS"},
	{Key: "voice.rooms_strict", Kind: KindBool, Perm: admin.PermSettingsManage, Services: []string{voice}},
	{Key: "voice.max_rooms_per_user", Kind: KindInt, Min: 0, Max: 1000, Perm: admin.PermSettingsManage, Services: []string{voice}},
	{Key: "voice.max_rooms", Kind: KindInt, Min: 0, Max: 100000, Perm: admin.PermSettingsManage, Services: []string{voice}},
	{Key: "voice.max_participants", Kind: KindInt, Min: 0, Max: 100000, Perm: admin.PermSettingsManage, Services: []string{voice}},

	{Key: "directory.name", Kind: KindString, Perm: admin.PermDirectoryManage, Services: []string{dir}, Env: "DIRECTORY_NAME", item: text(64)},
	{Key: "directory.operator", Kind: KindString, Perm: admin.PermDirectoryManage, Services: []string{dir}, Env: "DIRECTORY_OPERATOR", item: optionalText(64)},
	{Key: "directory.contact", Kind: KindString, Perm: admin.PermDirectoryManage, Services: []string{dir}, Env: "DIRECTORY_CONTACT", item: optionalText(254)},
	{Key: "directory.include_main", Kind: KindBool, Perm: admin.PermDirectoryManage, Services: []string{dir}, Env: "DIRECTORY_INCLUDE_MAIN"},
	{Key: "directory.main_peer_url", Kind: KindString, Perm: admin.PermDirectoryManage, Services: []string{dir}, Env: "DIRECTORY_MAIN_PEER", item: httpsURL},
	{Key: "directory.sync_every", Kind: KindDuration, Min: 5 * 60, Max: 24 * 3600, Perm: admin.PermDirectoryManage, Services: []string{dir}},
	{Key: "directory.peers", Kind: KindList, MaxItems: 20, Perm: admin.PermDirectoryManage, Services: []string{dir}, Env: "DIRECTORY_PEERS", item: httpsURL},

	{Key: KeyDirectoryRegistration, Kind: KindBool, Perm: admin.PermDirectoryManage, Services: []string{self}, Env: "DIRECTORY_REGISTRATION"},
	{Key: KeyDirectoryHostname, Kind: KindString, Perm: admin.PermDirectoryManage, Services: []string{self}, Env: "DIRECTORY_HOSTNAME", item: Hostname},
	{Key: KeyTLSCheckHosts, Kind: KindList, MaxItems: 20, Perm: admin.PermServicesManage, Services: []string{self}, Env: "TLS_CHECK_HOSTS", item: TLSHost},

	// Branding (auth serves it at /v1/branding; the panel pushes it to the
	// directory listing). Images are written by the Branding page's uploads.
	{Key: KeyBrandingTheme, Kind: KindTheme, Perm: admin.PermSettingsManage, Services: []string{auth}},
	{Key: KeyBrandingImages, Kind: KindImages, Perm: admin.PermSettingsManage, Services: []string{auth}},
}, emailKeys()...)

// The admin panel's own keys.
const (
	KeyDirectoryRegistration = "admin.directory_registration"
	KeyDirectoryHostname     = "admin.directory_hostname"
	KeyTLSCheckHosts         = "admin.tls_check_hosts"
)

// KeyByName finds a key of the schema.
func KeyByName(name string) (Key, bool) {
	i := slices.IndexFunc(Keys, func(k Key) bool { return k.Key == name })
	if i < 0 {
		return Key{}, false
	}
	return Keys[i], true
}

// Room is one static voice room.
type Room struct {
	ID   string `json:"id" yaml:"id"`
	Name string `json:"name" yaml:"name"`
}

// Clean validates a value from the API (JSON-decoded: bool, float64,
// string, []any, map[string]any) and returns what goes in the file.
func (k Key) Clean(v any) (any, error) {
	fail := func(format string, a ...any) (any, error) {
		return nil, fmt.Errorf("%s: "+format, append([]any{k.Key}, a...)...)
	}
	switch k.Kind {
	case KindBool:
		b, ok := v.(bool)
		if !ok {
			return fail("must be true or false")
		}
		return b, nil
	case KindInt:
		var n int
		switch x := v.(type) {
		case float64:
			if x != float64(int(x)) {
				return fail("must be a whole number")
			}
			n = int(x)
		case int:
			n = x
		default:
			return fail("must be a number")
		}
		if n < k.Min || n > k.Max {
			return fail("must be from %d to %d", k.Min, k.Max)
		}
		return n, nil
	case KindEnum:
		s, ok := v.(string)
		if !ok || !slices.Contains(k.Enum, s) {
			return fail("must be one of %s", strings.Join(k.Enum, ", "))
		}
		return s, nil
	case KindString:
		s, ok := v.(string)
		if !ok {
			return fail("must be text")
		}
		if k.item != nil {
			c, err := k.item(s)
			if err != nil {
				return fail("%v", err)
			}
			return c, nil
		}
		return s, nil
	case KindText:
		s, ok := v.(string)
		if !ok {
			return fail("must be text")
		}
		c, err := k.item(s)
		if err != nil {
			return fail("%v", err)
		}
		return c, nil
	case KindTheme:
		t, err := theme.Clean(v)
		if err != nil {
			return fail("%v", err)
		}
		return t, nil
	case KindImages:
		m, err := theme.CleanImages(v)
		if err != nil {
			return fail("%v", err)
		}
		return m, nil
	case KindDuration:
		s, ok := v.(string)
		d, err := time.ParseDuration(strings.TrimSpace(s))
		if !ok || err != nil {
			return fail("must be a duration like 10m or 1h")
		}
		if d < time.Duration(k.Min)*time.Second || d > time.Duration(k.Max)*time.Second {
			return fail("must be between %s and %s", time.Duration(k.Min)*time.Second, time.Duration(k.Max)*time.Second)
		}
		return strings.TrimSpace(s), nil
	case KindList:
		items, ok := v.([]any)
		if !ok {
			if ss, ok2 := v.([]string); ok2 {
				for _, s := range ss {
					items = append(items, s)
				}
				ok = true
			}
		}
		if !ok {
			return fail("must be a list")
		}
		if len(items) > k.MaxItems {
			return fail("at most %d entries", k.MaxItems)
		}
		out := make([]string, 0, len(items))
		for i, it := range items {
			s, ok := it.(string)
			if !ok {
				return fail("entry %d must be text", i+1)
			}
			c, err := k.item(s)
			if err != nil {
				return fail("entry %d: %v", i+1, err)
			}
			if slices.Contains(out, c) {
				return fail("%s is listed twice", c)
			}
			out = append(out, c)
		}
		return out, nil
	case KindRooms:
		items, ok := v.([]any)
		if !ok {
			if rs, ok2 := v.([]Room); ok2 {
				for _, r := range rs {
					items = append(items, map[string]any{"id": r.ID, "name": r.Name})
				}
				ok = true
			}
		}
		if !ok {
			return fail("must be a list of rooms")
		}
		if len(items) > k.MaxItems {
			return fail("at most %d rooms", k.MaxItems)
		}
		out := make([]Room, 0, len(items))
		for i, it := range items {
			m, ok := it.(map[string]any)
			if !ok {
				return fail("room %d must have an id and a name", i+1)
			}
			id, _ := m["id"].(string)
			name, _ := m["name"].(string)
			id, name = strings.TrimSpace(id), strings.TrimSpace(name)
			if !roomRe.MatchString(id) {
				return fail("room %d: the id must be 1–32 lowercase letters, digits, - or _ (like general)", i+1)
			}
			if name == "" || len([]rune(name)) > 64 {
				return fail("room %d: the name must be 1–64 characters", i+1)
			}
			if slices.ContainsFunc(out, func(r Room) bool { return r.ID == id }) {
				return fail("the room id %s is used twice", id)
			}
			out = append(out, Room{ID: id, Name: name})
		}
		return out, nil
	}
	return fail("unknown type")
}

// FromNode reads a value from the file as the API's JSON shape and checks
// it. Errors carry the node's line and column.
func (k Key) FromNode(n *yaml.Node) (any, error) {
	var raw any
	switch k.Kind {
	case KindBool:
		var b bool
		if n.Kind != yaml.ScalarNode || n.Decode(&b) != nil {
			return nil, fmt.Errorf("%s: must be true or false", k.Key)
		}
		raw = b
	case KindInt:
		var i int
		if n.Kind != yaml.ScalarNode || n.Decode(&i) != nil {
			return nil, fmt.Errorf("%s: must be a whole number", k.Key)
		}
		raw = float64(i)
	case KindTheme, KindImages:
		var m any
		if n.Kind != yaml.MappingNode || n.Decode(&m) != nil {
			return nil, fmt.Errorf("%s: must be a mapping", k.Key)
		}
		// YAML numbers and maps into the API's JSON shape.
		b, err := json.Marshal(m)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", k.Key, err)
		}
		if err := json.Unmarshal(b, &raw); err != nil {
			return nil, fmt.Errorf("%s: %v", k.Key, err)
		}
	case KindString, KindEnum, KindDuration, KindText:
		var s string
		if n.Kind != yaml.ScalarNode || n.Tag == "!!null" || n.Decode(&s) != nil {
			return nil, fmt.Errorf("%s: must be text", k.Key)
		}
		raw = s
	case KindList:
		var l []string
		if n.Kind == yaml.ScalarNode && n.Tag == "!!str" {
			// A comma-separated string, as in the env var.
			l = config.SplitList(n.Value)
		} else if n.Kind != yaml.SequenceNode || n.Decode(&l) != nil {
			return nil, fmt.Errorf("%s: must be a list", k.Key)
		}
		items := make([]any, len(l))
		for i, s := range l {
			items[i] = s
		}
		raw = items
	case KindRooms:
		var rs []Room
		if n.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("%s: must be a list of {id, name}", k.Key)
		}
		for _, c := range n.Content {
			var r Room
			switch {
			case c.Kind == yaml.ScalarNode: // a bare id
				r = Room{ID: c.Value, Name: c.Value}
			case c.Decode(&r) != nil:
				return nil, fmt.Errorf("%s: must be a list of {id, name}", k.Key)
			}
			rs = append(rs, r)
		}
		items := make([]any, len(rs))
		for i, r := range rs {
			items[i] = map[string]any{"id": r.ID, "name": r.Name}
		}
		raw = items
	}
	v, err := k.Clean(raw)
	if err != nil {
		return nil, err
	}
	return jsonShape(v), nil
}

// jsonShape: what the API returns for a cleaned value.
func jsonShape(v any) any {
	switch x := v.(type) {
	case []Room:
		if x == nil {
			return []Room{}
		}
	case []string:
		if x == nil {
			return []string{}
		}
	}
	return v
}
