package updates

import (
	"encoding/json"
	"time"
)

// Mode is how a component gets a new version.
type Mode string

const (
	// ModeImage: its image is a *_IMAGE variable in the stack's .env; the
	// panel sets it, pulls and recreates the service.
	ModeImage Mode = "image"
	// ModeStack: the version is pinned in the stack's compose.yaml (or the
	// stack itself): it arrives with a stack update (git pull), which the
	// panel shows but doesn't apply.
	ModeStack Mode = "stack"
)

// Impact is what applying a component's update does to the people using the
// network. This table is the one place that says it: the UI, the summary
// line and the "without disconnecting users" filter all read it.
type Impact struct {
	// Restart: its containers are recreated.
	Restart bool `json:"restart"`
	// Disruptive: something people are doing right now breaks (IRC
	// connections, calls, uploads). "Apply without disconnecting users"
	// leaves these out.
	Disruptive bool `json:"disruptive"`
	// ChatDisconnect: every chat (IRC) user is disconnected.
	ChatDisconnect bool `json:"chat_disconnect"`
	// DropsCalls: ongoing voice or video calls drop.
	DropsCalls bool `json:"drops_calls"`
	// Downtime is the usual gap while the new container starts.
	Downtime time.Duration `json:"-"`
	// Who: plain words, for the admin deciding when to apply.
	Who string `json:"who"`
}

// Component is one part of a Boson install with its own releases.
type Component struct {
	ID   string
	Name string
	// What it is, in a few words.
	About string
	// Repo is the GitHub repository whose releases are its changelog.
	Repo string
	// Image is the registry repository (no tag) the stack pulls.
	Image string
	// EnvVar is the stack .env variable holding the full image reference.
	EnvVar string
	// Services are the compose services recreated, in order. The first one
	// with a container tells the installed version.
	Services []string
	// Pre are one-off compose services run to completion first (migrations).
	Pre []string
	// NeverRestart are compose services an update must never touch.
	NeverRestart []string
	Mode         Mode
	Impact       Impact
	// Databases are the stack Postgres databases it owns (and migrates).
	Databases []string
	// Volumes are compose volumes (without the project prefix) backed up
	// before an update and restored on a rollback that restores data.
	Volumes []string
	// AddOn: not part of the base stack; shown only when installed.
	AddOn bool
	// Self: this admin panel (updated last, through a hand-off).
	Self bool
}

// Catalog is every part of a Boson install the panel knows. Order is the
// order updates apply in (the panel itself last).
var Catalog = []Component{
	{
		ID: "stack", Name: "Stack", About: "compose files, Traefik, config layout",
		Repo: "boson-chat/stack", Mode: ModeStack,
		Impact: Impact{Restart: false, Who: "Applied on the server with git pull and docker compose up; what restarts depends on what changed."},
	},
	{
		ID: "ergo", Name: "Ergo", About: "IRC server (and RoleServ)",
		Repo: "ergochat/ergo", Image: "harbor.floret.dev/boson/stack-ergo",
		Services: []string{"ergo", "roleserv"}, Mode: ModeStack, Volumes: []string{"ergo_db", "roleserv_data"},
		Impact: Impact{Restart: true, Disruptive: true, ChatDisconnect: true, Downtime: 10 * time.Second,
			Who: "Everyone on IRC is disconnected briefly; clients reconnect automatically."},
	},
	{
		ID: "auth", Name: "Auth", About: "accounts, sign-in, tokens",
		Repo: "boson-chat/auth", Image: "harbor.floret.dev/boson/auth", EnvVar: "AUTH_IMAGE",
		Services: []string{"auth"}, Mode: ModeImage, Databases: []string{"auth"},
		Impact: Impact{Restart: true, Downtime: 5 * time.Second,
			Who: "Sign-ins and token refresh pause for a few seconds; people already connected stay connected."},
	},
	{
		ID: "api", Name: "API", About: "boson backend: profiles, avatars, nick claims",
		Repo: "boson-chat/backend", Image: "harbor.floret.dev/boson/backend", EnvVar: "API_IMAGE",
		Services: []string{"api"}, Pre: []string{"api-migrate"}, Mode: ModeImage, Databases: []string{"api"},
		Impact: Impact{Restart: true, Downtime: 5 * time.Second,
			Who: "Profiles, avatars and nick claims pause for a few seconds; chat is not affected."},
	},
	{
		ID: "web", Name: "Web client", About: "the browser app",
		Repo: "boson-chat/boson", Image: "harbor.floret.dev/boson/web", EnvVar: "WEB_IMAGE",
		Services: []string{"web"}, Mode: ModeImage,
		Impact: Impact{Restart: true, Downtime: 2 * time.Second,
			Who: "Users get the new version the next time they load the page; nobody is disconnected."},
	},
	{
		ID: "voice", Name: "Voice", About: "quicvoice: voice rooms",
		Repo: "boson-chat/quicvoice", Image: "harbor.floret.dev/boson/quicvoice", EnvVar: "QUICVOICE_IMAGE",
		Services: []string{"quicvoiced"}, Mode: ModeImage, Volumes: []string{"voice_data"},
		Impact: Impact{Restart: true, Disruptive: true, DropsCalls: true, Downtime: 5 * time.Second,
			Who: "Ongoing voice calls drop; people rejoin once it's back."},
	},
	{
		ID: "video", Name: "Video", About: "quicvideo: video relay",
		Repo: "boson-chat/quicvideo", Image: "harbor.floret.dev/boson/quicvideo", EnvVar: "QUICVIDEO_IMAGE",
		Services: []string{"quicvideod"}, Mode: ModeImage,
		Impact: Impact{Restart: true, Disruptive: true, DropsCalls: true, Downtime: 5 * time.Second,
			Who: "Ongoing video calls and streams drop; people rejoin once it's back."},
	},
	{
		ID: "files", Name: "Files", About: "uploads",
		Repo: "boson-chat/files", Image: "harbor.floret.dev/boson/files", EnvVar: "FILES_IMAGE",
		Services: []string{"files"}, Mode: ModeImage, Databases: []string{"files"},
		Impact: Impact{Restart: true, Disruptive: true, Downtime: 5 * time.Second,
			Who: "Uploads in progress fail and have to be sent again; downloads pause for a few seconds."},
	},
	{
		ID: "directory", Name: "Directory", About: "the network directory",
		Repo: "boson-chat/directory", Image: "harbor.floret.dev/boson/directory", EnvVar: "DIRECTORY_IMAGE",
		Services: []string{"directory"}, Mode: ModeImage, Databases: []string{"directory"}, Volumes: []string{"directory_media"},
		Impact: Impact{Restart: true, Downtime: 5 * time.Second,
			Who: "The directory website and API are unavailable for a few seconds; chat is not affected."},
	},
	{
		ID: "webircgateway", Name: "Web IRC gateway", About: "WebSocket gateway for the web client",
		Repo: "boson-chat/webircgateway", Image: "harbor.floret.dev/boson/stack-webircgateway",
		Services: []string{"webircgateway"}, Mode: ModeStack,
		Impact: Impact{Restart: true, Disruptive: true, Downtime: 5 * time.Second,
			Who: "Web client users are disconnected from IRC briefly and reconnect automatically."},
	},
	{
		// The bridge in the add-on install mode (the stack's bridge
		// profile): only shown when the stack runs an evrd service. Both
		// evrd and its links holder run ghcr.io/4evrd/evrd, set separately
		// (EVRD_IMAGE / EVRD_LINKS_IMAGE): the links holder keeps every IRC
		// connection open across evrd restarts, so an update only moves
		// EVRD_IMAGE and never recreates evrd-links. bridge-sync is built in
		// the stack repo and pinned in compose.yaml: it comes with a stack
		// update.
		ID: "evrd", Name: "evrd", About: "IRC bridge (add-on)",
		Repo: "4evrd/evrd", Image: "ghcr.io/4evrd/evrd", EnvVar: "EVRD_IMAGE",
		Services: []string{"evrd"}, NeverRestart: []string{"evrd-links"}, Mode: ModeImage, AddOn: true,
		Volumes: []string{"bridge_data"},
		Impact: Impact{Restart: true, Downtime: 5 * time.Second,
			Who: "The bridge restarts; its IRC connections survive because the links holder (evrd-links) is never restarted."},
	},
	{
		ID: "admin", Name: "Admin panel", About: "this panel",
		Repo: "boson-chat/admin", Image: "harbor.floret.dev/boson/admin", EnvVar: "ADMIN_IMAGE",
		Services: []string{"admin"}, Mode: ModeImage, Self: true,
		Impact: Impact{Restart: true, Downtime: 5 * time.Second,
			Who: "This panel reloads (it updates itself last); nobody else is affected."},
	},
}

// ByID finds a catalog component.
func ByID(id string) (Component, bool) {
	for _, c := range Catalog {
		if c.ID == id {
			return c, true
		}
	}
	return Component{}, false
}

// PostgresClients maps each stack database to the compose services that use
// it: a data restore stops them first.
func PostgresClients() map[string][]string {
	m := map[string][]string{}
	for _, c := range Catalog {
		for _, db := range c.Databases {
			m[db] = append(m[db], c.Services...)
		}
	}
	return m
}

// MarshalJSON adds the downtime in seconds.
func (i Impact) MarshalJSON() ([]byte, error) {
	type plain Impact
	return json.Marshal(struct {
		plain
		DowntimeSeconds int `json:"downtime_seconds"`
	}{plain(i), int(i.Downtime.Seconds())})
}
