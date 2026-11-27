package stackconfig

import (
	"encoding/json"
	"fmt"
	"math"
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
	KindChannels Kind = "channels" // irc.channels: [{name, topic, autojoin}]
	KindDuration Kind = "duration" // 10m, 1h
	KindText     Kind = "text"     // multi-line string (email bodies)
	KindTheme    Kind = "theme"    // branding.theme: a boson-theme/1 document
	KindImages   Kind = "images"   // branding.images: slot → file (written by uploads)
	// KindEmailFile is an email's MJML source or compiled HTML file in
	// config/emails/, written by PUT /api/emails/mjml/{part}.
	KindEmailFile Kind = "email_file"
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
	intOK   func(int) error              // extra int check (after Min/Max)
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
		{Key: EmailFileKey(EmailLayout, "mjml"), Kind: KindEmailFile, Perm: admin.PermSettingsManage, Services: []string{auth}},
		{Key: EmailFileKey(EmailLayout, "html"), Kind: KindEmailFile, Perm: admin.PermSettingsManage, Services: []string{auth}},
	}
	for _, t := range EmailTemplates {
		out = append(out,
			Key{Key: "auth.email.templates." + t + ".subject", Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{auth}, item: text(200)},
			Key{Key: "auth.email.templates." + t + ".body", Kind: KindText, Max: 5000, Perm: admin.PermSettingsManage, Services: []string{auth}, item: multiline(5000)},
			Key{Key: EmailFileKey(t, "mjml"), Kind: KindEmailFile, Perm: admin.PermSettingsManage, Services: []string{auth}},
			Key{Key: EmailFileKey(t, "html"), Kind: KindEmailFile, Perm: admin.PermSettingsManage, Services: []string{auth}},
		)
	}
	return out
}

// EmailLayout is the MJML part every email without its own override is
// wrapped in; the other parts are EmailTemplates.
const EmailLayout = "layout"

// EmailFileKey is the config key naming part's MJML source (ext "mjml")
// or compiled HTML (ext "html") file.
func EmailFileKey(part, ext string) string {
	if part == EmailLayout {
		return "auth.email.layout." + ext
	}
	return "auth.email.templates." + part + "." + ext
}

// EmailsPrefix starts every email file value: the files live in
// config/emails/ (auth's BOSON_EMAILS_DIR).
const EmailsPrefix = "emails/"

// EmailFileName checks an email file value (emails/<name>: a plain .mjml
// or .html name, no traversal) and returns the bare file name.
func EmailFileName(v string) (string, error) {
	name := strings.TrimPrefix(v, EmailsPrefix)
	if name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") || len(name) > 128 ||
		(!strings.HasSuffix(name, ".mjml") && !strings.HasSuffix(name, ".html")) {
		return "", fmt.Errorf("%q must be a .mjml or .html file in config/emails/, like emails/layout-0123abcd.html", v)
	}
	return name, nil
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
	api       = "api"
	files     = "files"
	everybody = "*"
)

// Keys is the schema the panel edits. Other keys in the file are kept as
// they are.
var Keys = append([]Key{
	{Key: "network.name", Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{everybody}, Env: "NETWORK_NAME", item: text(64)},

	{Key: "auth.registration", Kind: KindEnum, Enum: []string{"open", "invite", "closed"}, Perm: admin.PermSettingsManage, Services: []string{auth}, Env: "REGISTRATION"},
	{Key: "auth.require_email_verification", Kind: KindBool, Perm: admin.PermSettingsManage, Services: []string{auth}, Env: "REQUIRE_EMAIL_VERIFICATION"},
	{Key: "auth.guest_access", Kind: KindBool, Perm: admin.PermSettingsManage, Services: []string{auth}},
	{Key: "auth.guest_voice", Kind: KindBool, Perm: admin.PermSettingsManage, Services: []string{auth}},
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
	// NickServ account backend only (accounts.backend: nickserv): whether
	// Boson sign-up registers a NickServ account, and what sign-up says
	// when it doesn't ("" = auth's default: register on IRC).
	{Key: KeyNickServRegistration, Kind: KindBool, Perm: admin.PermSettingsManage, Services: []string{auth}, Env: "NICKSERV_REGISTRATION"},
	{Key: KeyNickServSignupMessage, Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{auth}, Env: "NICKSERV_SIGNUP_MESSAGE", item: optionalText(500)},

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

	// Network channels (the network channels contract): auth reads them
	// and RoleServ gets them from auth's feed.
	{Key: KeyNetworkChannels, Kind: KindChannels, MaxItems: MaxNetworkChannels, Perm: admin.PermSettingsManage, Services: []string{auth}},
	{Key: KeyChannelFounder, Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{auth}, item: accountName},
	{Key: KeyAutojoinGuests, Kind: KindBool, Perm: admin.PermSettingsManage, Services: []string{auth}},

	// Role channels (the contract's roles.channels): auth serves them in
	// its feeds and RoleServ in the Ergo helper acts on them.
	{Key: KeyRoleChannelsAutojoin, Kind: KindBool, Perm: admin.PermSettingsManage, Services: []string{auth, ergo}},
	{Key: KeyRoleChannelsRejoin, Kind: KindEnum, Enum: []string{RejoinLogin, RejoinOnce}, Perm: admin.PermSettingsManage, Services: []string{auth, ergo}},

	{Key: "voice.rooms", Kind: KindRooms, MaxItems: 50, Perm: admin.PermSettingsManage, Services: []string{voice}, Env: "VOICE_ROOMS"},
	{Key: "voice.rooms_strict", Kind: KindBool, Perm: admin.PermSettingsManage, Services: []string{voice}},
	{Key: "voice.max_rooms_per_user", Kind: KindInt, Min: 0, Max: 1000, Perm: admin.PermSettingsManage, Services: []string{voice}},
	{Key: "voice.max_rooms", Kind: KindInt, Min: 0, Max: 100000, Perm: admin.PermSettingsManage, Services: []string{voice}},
	{Key: "voice.max_participants", Kind: KindInt, Min: 0, Max: 100000, Perm: admin.PermSettingsManage, Services: []string{voice}},
	// quicvoiced ≥ 1.13: the voice bitrate cap in rooms without their own
	// (voice.rooms[].max_kbps); 0 = no cap.
	{Key: "voice.max_kbps", Kind: KindInt, Min: 0, Max: MaxKbps, Perm: admin.PermSettingsManage, Services: []string{voice}, intOK: kbps},

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

	// Nick registration (the api's nick claims, docs/nick-claim.md). The
	// mailbox password stays in .env (NICK_CLAIM_POP3_PASSWORD).
	{Key: KeyNickClaimEnabled, Kind: KindBool, Perm: admin.PermSettingsManage, Services: []string{api}, Env: "NICK_CLAIM_ENABLED"},
	{Key: KeyNickClaimEmailDomain, Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{api}, Env: "NICK_CLAIM_EMAIL_DOMAIN", item: emailDomain},
	{Key: KeyNickClaimRateLimit, Kind: KindInt, Min: 1, Max: 10000, Perm: admin.PermSettingsManage, Services: []string{api}, Env: "NICK_CLAIM_RATE_LIMIT_PER_HOUR"},
	{Key: KeyNickClaimProtocol, Kind: KindEnum, Enum: []string{"pop3"}, Perm: admin.PermSettingsManage, Services: []string{api}},
	{Key: KeyNickClaimHost, Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{api}, Env: "POP3_HOST", item: MailHost},
	{Key: KeyNickClaimPort, Kind: KindInt, Min: 1, Max: 65535, Perm: admin.PermSettingsManage, Services: []string{api}, Env: "POP3_PORT"},
	{Key: KeyNickClaimTLS, Kind: KindBool, Perm: admin.PermSettingsManage, Services: []string{api}, Env: "POP3_TLS"},
	{Key: KeyNickClaimUser, Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{api}, Env: "POP3_USER", item: text(254)},

	// Uploads (the files service, docs/files.md). auth also reads
	// files.enabled (files_url in its /v1/config). S3 keys stay in .env
	// (FILES_S3_ACCESS_KEY / FILES_S3_SECRET_KEY).
	{Key: KeyFilesEnabled, Kind: KindBool, Perm: admin.PermSettingsManage, Services: []string{files, auth}, Env: "FILES_ENABLED"},
	{Key: KeyFilesRetention, Kind: KindDuration, Min: 60, Max: 365 * 24 * 3600, Perm: admin.PermSettingsManage, Services: []string{files}},
	{Key: KeyFilesWindow, Kind: KindDuration, Min: 60, Max: 365 * 24 * 3600, Perm: admin.PermSettingsManage, Services: []string{files}},
	{Key: KeyFilesAccountQuota, Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{files}, item: sizeBetween(MinQuota, MaxSize)},
	{Key: KeyFilesAccountMaxFile, Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{files}, item: sizeBetween(MinFile, MaxSize)},
	{Key: KeyFilesGuestEnabled, Kind: KindBool, Perm: admin.PermSettingsManage, Services: []string{files}},
	{Key: KeyFilesGuestQuota, Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{files}, item: sizeBetween(MinQuota, MaxSize)},
	{Key: KeyFilesGuestMaxFile, Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{files}, item: sizeBetween(MinFile, MaxSize)},
	{Key: KeyFilesMinFree, Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{files}, item: sizeBetween(0, MaxSize)},
	{Key: KeyFilesCapacity, Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{files}, item: sizeBetween(0, MaxSize)},
	// The storage driver and its s3 settings apply after a restart of files.
	{Key: KeyFilesDriver, Kind: KindEnum, Enum: []string{"local", "s3"}, Perm: admin.PermSettingsManage, Services: []string{files}, Restart: true},
	{Key: KeyFilesS3Endpoint, Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{files}, Restart: true, item: s3Endpoint},
	{Key: KeyFilesS3Bucket, Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{files}, Restart: true, item: s3Bucket},
	{Key: KeyFilesS3Region, Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{files}, Restart: true, item: s3Region},
	{Key: KeyFilesS3PathStyle, Kind: KindBool, Perm: admin.PermSettingsManage, Services: []string{files}, Restart: true},
	{Key: KeyFilesS3PublicBaseURL, Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{files}, Restart: true, item: httpsURL},

	// Federation (auth docs/federation.md): which other Boson servers' users
	// may sign in here with their home account, and what they get. auth
	// reads them all: it enforces remote_voice on voice and call tokens, and
	// pushes remote_upload_quota to the files service as each remote user's
	// quota override at their next sign-in (files doesn't read this key).
	{Key: KeyFederationMode, Kind: KindEnum, Enum: []string{"off", "allowlist", "directory", "open"}, Perm: admin.PermSettingsManage, Services: []string{auth}, Env: "FEDERATION_MODE"},
	{Key: KeyFederationAllow, Kind: KindList, MaxItems: MaxFederationDomains, Perm: admin.PermSettingsManage, Services: []string{auth}, Env: "FEDERATION_ALLOW", item: FederationDomain},
	{Key: KeyFederationBlock, Kind: KindList, MaxItems: MaxFederationDomains, Perm: admin.PermSettingsManage, Services: []string{auth}, Env: "FEDERATION_BLOCK", item: FederationDomain},
	{Key: KeyFederationDirectory, Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{auth}, Env: "FEDERATION_DIRECTORY_URL", item: httpsURL},
	{Key: KeyFederationUploadQuota, Kind: KindString, Perm: admin.PermSettingsManage, Services: []string{auth}, Env: "FEDERATION_REMOTE_UPLOAD_QUOTA", item: sizeBetween(0, MaxSize)},
	{Key: KeyFederationRemoteVoice, Kind: KindBool, Perm: admin.PermSettingsManage, Services: []string{auth}, Env: "FEDERATION_REMOTE_VOICE"},

	// Branding (auth serves it at /v1/branding; the panel pushes it to the
	// directory listing). Images are written by the Branding page's uploads.
	{Key: KeyBrandingTheme, Kind: KindTheme, Perm: admin.PermSettingsManage, Services: []string{auth}},
	{Key: KeyBrandingImages, Kind: KindImages, Perm: admin.PermSettingsManage, Services: []string{auth}},
}, emailKeys()...)

// NickServ account backend keys (auth.nickserv).
const (
	KeyNickServRegistration  = "auth.nickserv.registration"
	KeyNickServSignupMessage = "auth.nickserv.signup_message"
)

// Federation keys (auth's federation section).
const (
	KeyFederationMode        = "auth.federation.mode"
	KeyFederationAllow       = "auth.federation.allow"
	KeyFederationBlock       = "auth.federation.block"
	KeyFederationDirectory   = "auth.federation.directory_url"
	KeyFederationUploadQuota = "auth.federation.remote_upload_quota"
	KeyFederationRemoteVoice = "auth.federation.remote_voice"
	// MaxFederationDomains bounds the allow and block lists (auth's limit).
	MaxFederationDomains = 1000
)

// FederationDomain checks a home domain (boson_domain) as auth takes it in
// the allow and block lists: a DNS name like boson.chat, or *.example.org
// for it and every subdomain; lowercased.
func FederationDomain(s string) (string, error) {
	t := strings.TrimSpace(s)
	base, wild := strings.CutPrefix(t, "*.")
	d, err := Hostname(base)
	if err != nil {
		return "", fmt.Errorf("%q must be a domain like boson.chat, or *.example.org for it and its subdomains (no https://, no path)", t)
	}
	if wild {
		return "*." + d, nil
	}
	return d, nil
}

// FederationDomainMatches: does entry (a domain or *.domain) cover domain?
func FederationDomainMatches(entry, domain string) bool {
	if base, ok := strings.CutPrefix(entry, "*."); ok {
		return domain == base || strings.HasSuffix(domain, "."+base)
	}
	return entry == domain
}

// Nick registration keys (the api's nick_claim section).
const (
	KeyNickClaimEnabled     = "nick_claim.enabled"
	KeyNickClaimEmailDomain = "nick_claim.email_domain"
	KeyNickClaimRateLimit   = "nick_claim.rate_limit_per_hour"
	KeyNickClaimProtocol    = "nick_claim.inbound.protocol"
	KeyNickClaimHost        = "nick_claim.inbound.host"
	KeyNickClaimPort        = "nick_claim.inbound.port"
	KeyNickClaimTLS         = "nick_claim.inbound.tls"
	KeyNickClaimUser        = "nick_claim.inbound.user"
)

// emailDomain: the domain in the reg+…@ addresses (a DNS name).
func emailDomain(s string) (string, error) {
	d, err := Hostname(s)
	if err != nil {
		return "", fmt.Errorf("%q must be a domain like nick.example.com", strings.TrimSpace(s))
	}
	return d, nil
}

// MailHost checks a mail server's host: a DNS name, a compose service
// name or an IP address.
func MailHost(s string) (string, error) {
	s = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
	if s == "" {
		return "", fmt.Errorf("the mail server can't be empty")
	}
	if err := dialHost(s); err != nil {
		return "", fmt.Errorf("%q isn't a hostname or IP address, like pop3.example.com", s)
	}
	return s, nil
}

// Role channel keys: auto-join members to their roles' channels (default
// off), and whether someone who left one is joined again at every login
// (login, the default) or only when first granted (once).
const (
	KeyRoleChannelsAutojoin = "roles.channels.autojoin"
	KeyRoleChannelsRejoin   = "roles.channels.rejoin"
	RejoinLogin             = "login"
	RejoinOnce              = "once"
)

// Uploads keys (the files service's files: section).
const (
	KeyFilesEnabled         = "files.enabled"
	KeyFilesRetention       = "files.retention"
	KeyFilesWindow          = "files.window"
	KeyFilesAccountQuota    = "files.account.quota"
	KeyFilesAccountMaxFile  = "files.account.max_file"
	KeyFilesGuestEnabled    = "files.guest.enabled"
	KeyFilesGuestQuota      = "files.guest.quota"
	KeyFilesGuestMaxFile    = "files.guest.max_file"
	KeyFilesDriver          = "files.storage.driver"
	KeyFilesMinFree         = "files.storage.min_free"
	KeyFilesCapacity        = "files.storage.capacity"
	KeyFilesS3Endpoint      = "files.storage.s3.endpoint"
	KeyFilesS3Bucket        = "files.storage.s3.bucket"
	KeyFilesS3Region        = "files.storage.s3.region"
	KeyFilesS3PathStyle     = "files.storage.s3.path_style"
	KeyFilesS3PublicBaseURL = "files.storage.s3.public_base_url"
)

// Size bounds for the files: limits.
const (
	MinFile  int64 = 1e3  // 1KB
	MinQuota int64 = 1e6  // 1MB
	MaxSize  int64 = 1e14 // 100TB
)

// Size units as the files service reads them: KB/MB/GB/TB are powers of
// 1000, KiB/MiB/GiB/TiB powers of 1024, no unit = bytes.
var sizeUnits = map[string]float64{
	"": 1, "b": 1,
	"k": 1e3, "kb": 1e3, "m": 1e6, "mb": 1e6, "g": 1e9, "gb": 1e9, "t": 1e12, "tb": 1e12,
	"kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40,
}

// ParseSize reads a size like 5GB, 500 MB, 1.5gb or 25MiB.
func ParseSize(s string) (int64, error) {
	t := strings.TrimSpace(s)
	i := 0
	for i < len(t) && (t[i] >= '0' && t[i] <= '9' || t[i] == '.') {
		i++
	}
	num, unit := t[:i], strings.ToLower(strings.TrimSpace(t[i:]))
	mult, ok := sizeUnits[unit]
	f, err := strconv.ParseFloat(num, 64)
	if num == "" || !ok || err != nil {
		return 0, fmt.Errorf("%q must be a size like 100MB or 5GB", t)
	}
	v := f * mult
	if v > float64(MaxSize)*2 {
		return 0, fmt.Errorf("%q is too big", t)
	}
	return int64(math.Round(v)), nil
}

// FormatSize writes bytes as the files service does: the largest decimal
// unit that divides it (5GB, 1500MB), else a binary one (25MiB), else bytes.
func FormatSize(n int64) string {
	for _, u := range []struct {
		s string
		v int64
	}{{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"KB", 1e3}, {"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}} {
		if n >= u.v && n%u.v == 0 {
			return strconv.FormatInt(n/u.v, 10) + u.s
		}
	}
	return strconv.FormatInt(n, 10)
}

// sizeBetween checks a size and writes it in the file's form (5GB).
func sizeBetween(lo, hi int64) func(string) (string, error) {
	return func(s string) (string, error) {
		n, err := ParseSize(s)
		if err != nil {
			return "", err
		}
		if n < lo || n > hi {
			return "", fmt.Errorf("must be from %s to %s", FormatSize(lo), FormatSize(hi))
		}
		return FormatSize(n), nil
	}
}

// s3Endpoint: an http(s) base URL, like https://<account>.r2.cloudflarestorage.com
// or http://minio:9000 inside the stack.
func s3Endpoint(s string) (string, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "/")
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" {
		return "", fmt.Errorf("%q must be a URL like https://s3.example.com or http://minio:9000 (keys go in .env, not here)", s)
	}
	return s, nil
}

var bucketRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

func s3Bucket(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !bucketRe.MatchString(s) || strings.Contains(s, "..") {
		return "", fmt.Errorf("%q must be a bucket name: 3–63 lowercase letters, digits, dots or dashes", s)
	}
	return s, nil
}

var regionRe = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

func s3Region(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !regionRe.MatchString(s) {
		return "", fmt.Errorf("%q must be a region like auto or us-east-1", s)
	}
	return s, nil
}

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
	// MaxKbps caps the voice bitrate in this room (kbit/s); 0 = the
	// section's voice.max_kbps.
	MaxKbps int `json:"max_kbps,omitempty" yaml:"max_kbps,omitempty"`
}

// Voice bitrate caps (kbit/s): quicvoiced takes 0 (none) or MinKbps..MaxKbps.
const (
	MinKbps = 6
	MaxKbps = 510
)

func kbps(n int) error {
	if n != 0 && (n < MinKbps || n > MaxKbps) {
		return fmt.Errorf("must be 0 (no cap) or %d–%d kbit/s", MinKbps, MaxKbps)
	}
	return nil
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
		if k.intOK != nil {
			if err := k.intOK(n); err != nil {
				return fail("%v", err)
			}
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
	case KindEmailFile:
		s, ok := v.(string)
		if !ok {
			return fail("must be a file name")
		}
		if _, err := EmailFileName(s); err != nil {
			return fail("%v", err)
		}
		return s, nil
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
	case KindChannels:
		out, err := k.cleanChannels(v)
		if err != nil {
			return fail("%v", err)
		}
		return out, nil
	case KindRooms:
		items, ok := v.([]any)
		if !ok {
			if rs, ok2 := v.([]Room); ok2 {
				for _, r := range rs {
					items = append(items, roomItem(r))
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
			var kb int
			switch x := m["max_kbps"].(type) {
			case nil:
			case float64:
				kb = int(x)
				if x != float64(kb) {
					return fail("room %d: the bitrate cap must be a whole number of kbit/s", i+1)
				}
			case int:
				kb = x
			default:
				return fail("room %d: the bitrate cap must be a number of kbit/s", i+1)
			}
			if err := kbps(kb); err != nil {
				return fail("room %d: the bitrate cap %v", i+1, err)
			}
			out = append(out, Room{ID: id, Name: name, MaxKbps: kb})
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
	case KindString, KindEnum, KindDuration, KindText, KindEmailFile:
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
	case KindChannels:
		items, err := channelsFromNode(n)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", k.Key, err)
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
			items[i] = roomItem(r)
		}
		raw = items
	}
	v, err := k.Clean(raw)
	if err != nil {
		return nil, err
	}
	return jsonShape(v), nil
}

// roomItem is r as the API's JSON shape (max_kbps only when set).
func roomItem(r Room) map[string]any {
	m := map[string]any{"id": r.ID, "name": r.Name}
	if r.MaxKbps != 0 {
		m["max_kbps"] = float64(r.MaxKbps)
	}
	return m
}

// jsonShape: what the API returns for a cleaned value.
func jsonShape(v any) any {
	switch x := v.(type) {
	case []Room:
		if x == nil {
			return []Room{}
		}
	case []NetworkChannel:
		if x == nil {
			return []NetworkChannel{}
		}
	case []string:
		if x == nil {
			return []string{}
		}
	}
	return v
}
