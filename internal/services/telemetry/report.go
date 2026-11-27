package telemetry

import (
	"regexp"
	"slices"
	"strings"
)

// Report is the install report, schema 1 (the stack's docs/telemetry.md).
// Every field is sent; nil pointers are JSON null where the schema allows
// it. Nothing in it names a person, a server or a network.
type Report struct {
	Schema     int               `json:"schema"`
	InstallID  string            `json:"install_id"`
	Reason     string            `json:"reason"`
	Stack      Stack             `json:"stack"`
	Services   map[string]string `json:"services"`
	Profiles   []string          `json:"profiles"`
	Install    Install           `json:"install"`
	Counts     Counts            `json:"counts"`
	Features   Features          `json:"features"`
	Settings   Settings          `json:"settings"`
	Health     Health            `json:"health"`
	UptimeDays *int              `json:"uptime_days"`
}

type Stack struct {
	Version string `json:"version"`
}

type Install struct {
	Mode string `json:"mode"`
	IRCd string `json:"ircd"`
	Arch string `json:"arch"`
}

type Counts struct {
	Accounts   *string `json:"accounts"`
	Active7d   *string `json:"active_7d"`
	Active30d  *string `json:"active_30d"`
	IRCPeak24h *string `json:"irc_peak_24h"`
	Channels   *string `json:"channels"`
}

type Features struct {
	VoiceMinutes24h *string `json:"voice_minutes_24h"`
	VideoMinutes24h *string `json:"video_minutes_24h"`
	FilesUploaded   *string `json:"files_uploaded_24h"`
	PushSent24h     *string `json:"push_sent_24h"`
}

type Settings struct {
	Registration    string   `json:"registration"`
	GuestAccess     bool     `json:"guest_access"`
	GuestFirst      *bool    `json:"guest_first"`
	SMTP            bool     `json:"smtp"`
	SSO             []string `json:"sso"`
	FederationPeers string   `json:"federation_peers"`
	DirectoryListed bool     `json:"directory_listed"`
}

type Health struct {
	Updates  Updates                  `json:"updates"`
	Services map[string]ServiceHealth `json:"services"`
	Host     *Host                    `json:"host"`
	Healthy  bool                     `json:"healthy"`
}

type Updates struct {
	Attempts  int      `json:"attempts"`
	Failures  int      `json:"failures"`
	Rollbacks int      `json:"rollbacks"`
	Errors    []string `json:"errors"`
}

type ServiceHealth struct {
	Restarts int    `json:"restarts"`
	State    string `json:"state"`
}

type Host struct {
	CPUP95     *float64 `json:"cpu_p95"`
	CPUMax     *float64 `json:"cpu_max"`
	MemP95     *float64 `json:"mem_p95"`
	DiskUsed   *float64 `json:"disk_used"`
	LoadPerCPU *float64 `json:"load_per_cpu"`
}

// Reasons.
const (
	ReasonDaily    = "daily"
	ReasonSetup    = "setup"
	ReasonUpdate   = "update"
	ReasonRollback = "rollback"
)

// Enums of schema 1.
var (
	// ServiceNames is the service enum: compose services the report may name.
	ServiceNames = []string{"traefik", "certsync", "ergo", "roleserv", "evrd", "evrd-links", "postgres",
		"auth", "quicvoiced", "quicvideod", "victoriametrics", "node-exporter", "admin",
		"docker-proxy", "docker-proxy-updates", "api", "directory", "files", "pushrelay",
		"minio", "webroot", "wellknown", "web", "webircgateway", "telemetry",
		"victoriametrics-telemetry"}
	ProfileNames = []string{"irc", "voice", "video", "auth", "api", "directory", "files", "minio",
		"metrics", "admin", "updates", "webroot", "wellknown", "web", "bridge", "pushrelay", "telemetry"}
	UpdateErrors  = []string{"preflight", "backup", "pull", "migrate", "recreate", "health", "checks", "self_update", "rollback", "timeout", "other"}
	ServiceStates = []string{"running", "restarting", "unhealthy", "exited", "created"}
)

// serviceProfile is the compose profile each service of the stack's
// compose.yaml belongs to ("" = always on: traefik, certsync).
var serviceProfile = map[string]string{
	"ergo": "irc", "roleserv": "irc", "evrd": "bridge", "evrd-links": "bridge",
	"postgres": "auth", "auth": "auth", "quicvoiced": "voice", "quicvideod": "video",
	"victoriametrics": "metrics", "node-exporter": "metrics", "admin": "admin", "docker-proxy": "admin",
	"docker-proxy-updates": "updates", "api": "api", "directory": "directory", "files": "files",
	"pushrelay": "pushrelay", "minio": "minio", "webroot": "webroot", "wellknown": "wellknown",
	"web": "web", "webircgateway": "web", "telemetry": "telemetry", "victoriametrics-telemetry": "telemetry",
}

// Buckets, in order.
var Buckets = []string{"0", "1", "2-10", "11-50", "51-100", "101-500", "501-1k", "1k-5k", "5k+"}

// Bucket puts a count in its bucket: never an exact number.
func Bucket(n int64) string {
	switch {
	case n <= 0:
		return "0"
	case n == 1:
		return "1"
	case n <= 10:
		return "2-10"
	case n <= 50:
		return "11-50"
	case n <= 100:
		return "51-100"
	case n <= 500:
		return "101-500"
	case n <= 1000:
		return "501-1k"
	case n <= 5000:
		return "1k-5k"
	}
	return "5k+"
}

// bucketCap is the count past which more doesn't change the bucket: list
// walks stop there.
const bucketCap = 5001

func bucketPtr(n int64) *string {
	b := Bucket(n)
	return &b
}

var (
	versionRe = regexp.MustCompile(`^[0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,6}(-[0-9A-Za-z.]{1,32})?$`)
	xyRe      = regexp.MustCompile(`^[0-9]{1,4}\.[0-9]{1,4}$`)
	numRe     = regexp.MustCompile(`^[0-9]{1,4}(\.[0-9]{1,6}){1,2}`)
	// A suffix that is a pre-release (kept): rc.1, beta2, alpha, dev.3 …
	preRe = regexp.MustCompile(`^(?i:alpha|beta|rc|pre|preview|dev|next|canary)[0-9.]*$`)
)

// Unknown is the version of a latest/edge/digest-only image.
const Unknown = "unknown"

// NormaliseVersion turns an image tag into a schema version: a leading v
// and a -alpine style suffix that isn't a pre-release are dropped, X.Y
// becomes X.Y.0, anything else is "unknown".
func NormaliseVersion(tag string) string {
	t := strings.TrimSpace(tag)
	if i := strings.IndexByte(t, '+'); i >= 0 { // build metadata isn't allowed
		t = t[:i]
	}
	t = strings.TrimPrefix(strings.TrimPrefix(t, "v"), "V")
	core, suffix, hasSuffix := strings.Cut(t, "-")
	if !numRe.MatchString(core) || numRe.FindString(core) != core {
		return Unknown
	}
	if xyRe.MatchString(core) {
		core += ".0"
	}
	out := core
	if hasSuffix && preRe.MatchString(suffix) {
		out += "-" + suffix
	}
	if !versionRe.MatchString(out) {
		return Unknown
	}
	return out
}

// errorClass maps a failed run's step to the update error enum (never
// the error text).
func errorClass(kind, step, errText string) string {
	if kind == "rollback" {
		return "rollback"
	}
	switch step {
	case "preflight":
		return "preflight"
	case "backup":
		return "backup"
	case "stage", "pull":
		return "pull"
	case "configure":
		return "migrate"
	case "recreate":
		return "recreate"
	case "health":
		return "health"
	case "verify":
		return "checks"
	case "self":
		return "self_update"
	}
	if e := strings.ToLower(errText); strings.Contains(e, "timed out") || strings.Contains(e, "deadline exceeded") {
		return "timeout"
	}
	return "other"
}

// ssoKind maps an auth sign-in provider kind to the schema's kinds.
func ssoKind(kind string) string {
	switch k := strings.ToLower(strings.TrimSpace(kind)); k {
	case "oidc", "saml", "oauth2":
		return k
	case "oauth", "github", "discord", "gitlab":
		return "oauth2"
	case "google", "microsoft", "azure", "azuread", "okta", "keycloak", "authentik", "apple":
		return "oidc"
	}
	return "other"
}

// ircdFrom classifies RPL_MYINFO's server version ("UnrealIRCd-6.1.8").
func ircdFrom(version string) string {
	v := strings.ToLower(version)
	switch {
	case v == "":
		return Unknown
	case strings.HasPrefix(v, "ergo") || strings.HasPrefix(v, "oragono"):
		return "ergo"
	case strings.HasPrefix(v, "unreal"):
		return "unrealircd"
	case strings.HasPrefix(v, "inspircd"):
		return "inspircd"
	case strings.HasPrefix(v, "ngircd"):
		return "ngircd"
	case strings.HasPrefix(v, "solanum"):
		return "solanum"
	case strings.HasPrefix(v, "charybdis"):
		return "charybdis"
	case strings.HasPrefix(v, "ircd-hybrid") || strings.HasPrefix(v, "hybrid"):
		return "hybrid"
	}
	return "other"
}

func archOf(goarch string) string {
	switch goarch {
	case "amd64", "arm64":
		return goarch
	}
	return "other"
}

// sortedUnique sorts and de-duplicates.
func sortedUnique(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}
