// Package http wires the panel's dependencies and runs its listeners.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	nethttp "net/http"
	"net/url"
	"os"
	"runtime"
	"time"

	"github.com/boson-chat/admin/config"
	"github.com/boson-chat/admin/http/handlers"
	"github.com/boson-chat/admin/http/middleware"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/clients/bosonapi"
	"github.com/boson-chat/admin/internal/clients/dockerapi"
	"github.com/boson-chat/admin/internal/clients/health"
	"github.com/boson-chat/admin/internal/clients/ircprobe"
	"github.com/boson-chat/admin/internal/clients/ircstats"
	"github.com/boson-chat/admin/internal/clients/promql"
	"github.com/boson-chat/admin/internal/clients/tlsprobe"
	"github.com/boson-chat/admin/internal/configfile"
	"github.com/boson-chat/admin/internal/logger"
	"github.com/boson-chat/admin/internal/services/admin"
	"github.com/boson-chat/admin/internal/services/branding"
	"github.com/boson-chat/admin/internal/services/containers"
	"github.com/boson-chat/admin/internal/services/directory"
	"github.com/boson-chat/admin/internal/services/federation"
	"github.com/boson-chat/admin/internal/services/files"
	"github.com/boson-chat/admin/internal/services/hostmetrics"
	"github.com/boson-chat/admin/internal/services/nickclaim"
	"github.com/boson-chat/admin/internal/services/overview"
	"github.com/boson-chat/admin/internal/services/stackconfig"
	"github.com/boson-chat/admin/internal/services/telemetry"
	"github.com/boson-chat/admin/internal/services/updates"
	"github.com/boson-chat/admin/internal/session"
	"github.com/boson-chat/admin/metrics"
)

// SessionCookie is the base name of the panel's session cookie.
const SessionCookie = "boson_admin"

type Options struct {
	Metrics        *metrics.Metrics
	MetricsHandler nethttp.Handler
	UI             fs.FS
	// SessionKey overrides SESSION_KEY (dev: an ephemeral key).
	SessionKey []byte
}

// App is the wired application.
type App struct {
	Public   nethttp.Handler
	Internal nethttp.Handler
	Config   config.Config
}

// Build wires clients → services → handlers.
func Build(ctx context.Context, cfg config.Config, o Options) (*App, error) {
	m := o.Metrics
	if m == nil {
		m = metrics.Noop()
	}
	up := cfg.UpstreamConfig
	observe := func(op string, status int, err error, d time.Duration) {
		m.Upstream(context.Background(), "auth", op, status, err, d)
	}
	authClient := authapi.NewAuthClient(up.AuthURL, up.Timeout, observe)
	refresher := admin.NewRefresher(authClient, 2*time.Minute, up.Timeout*2, nil)
	adminSvc := admin.NewAdminService(authClient, refresher, m, nil)

	// The stack config file (BOSON_CONFIG): runtime settings, env = defaults.
	var store *configfile.Store
	if path := cfg.StackConfig.Path; path != "" {
		store = configfile.Open(path, configfile.Options{Header: stackconfig.FileHeader})
		if _, p := store.Current(); p != nil {
			logger.FromCtx(ctx).Error().Str("file", path).Str("error", p.Error()).Msg("config file has an error: using the env defaults until it's fixed")
		}
		store.OnChange(func(snap *configfile.Snapshot) {
			logger.FromCtx(ctx).Info().Str("file", path).Time("file_mtime", snap.ModTime).Msg("config reloaded")
		})
		go store.Watch(ctx, cfg.StackConfig.Poll, func(err error) {
			logger.FromCtx(ctx).Warn().Err(err).Str("file", path).Msg("watching the config file")
		})
	}
	statusTargets, _ := cfg.StackConfig.Statuses()
	stack := stackconfig.New(stackconfig.Options{
		Store: store,
		Defaults: stackconfig.Defaults{
			DirectoryRegistration: cfg.BosonConfig.DirectoryRegistration,
			DirectoryHostname:     cfg.BosonConfig.DirectoryHostname,
			TLSCheckHosts:         config.SplitList(up.TLSCheckHosts),
		},
		Targets: statusTargets,
		Client:  &nethttp.Client{Timeout: min(up.Timeout, 3*time.Second)},
		Metrics: m,
	})

	targets, _ := up.Health()
	versions, _ := up.Versions()
	irc, _ := up.IRC()
	probes, _ := up.PublicProbes()
	// The overview's services: the compose project's containers (through the
	// socket proxy) with the built-in probe map, else the env lists alone.
	var discover overview.Containers
	if dc := cfg.DockerConfig; dc.ProxyURL != "" {
		discover = dockerapi.NewDockerClient(dc.ProxyURL, min(up.Timeout, 3*time.Second), func(op string, status int, err error, d time.Duration) {
			m.Upstream(context.Background(), "docker", op, status, err, d)
		})
	}
	var prom promql.PromClientImpl
	var promRange promql.RangeClient
	if up.MetricsURL != "" {
		c := &promql.PromClient{BaseURL: up.MetricsURL, HTTP: &nethttp.Client{Timeout: up.Timeout}}
		prom, promRange = c, c
	}
	overviewSvc := overview.NewOverviewService(overview.Options{
		Health:   health.NewHealthChecker(min(up.Timeout, 3*time.Second)),
		TLS:      tlsprobe.NewTLSProber(min(up.Timeout, 5*time.Second)),
		Prom:     prom,
		Targets:  targets,
		Versions: versions,
		CertsFn:  func() []config.TLSTarget { return stack.Admin().TLSTargets },
		Grafana:  up.GrafanaURL,
		Version:  cfg.AppConfig.Version,
		IRCProbe: ircprobe.NewIRCProber(min(up.Timeout, 5*time.Second)),
		IRC:      irc,
		Edge:     up.EdgeName,
		Public:   probes,
		Docker:   discover,
		Project:  cfg.DockerConfig.Project,
		Known: overview.Builtins(overview.Hosts{Auth: up.AuthHost, Voice: up.VoiceHost, IRC: up.IRCHostname(),
			API: up.APIHost, Directory: up.DirectoryHost, Files: up.FilesHost, Video: up.VideoHost, Web: up.WebHost}),
	})
	connect := overviewSvc.PublicOrigins()

	key := o.SessionKey
	if key == nil {
		var err error
		if key, err = cfg.SessionConfig.DecodeKey(); err != nil {
			return nil, err
		}
	}
	codec, err := session.NewCodec(key, SessionCookie, cfg.SessionConfig.CookieSecure, cfg.SessionConfig.MaxAge, nil)
	if err != nil {
		return nil, err
	}
	origin, err := cfg.HTTPConfig.Origin()
	if err != nil {
		return nil, err
	}
	trusted, err := cfg.HTTPConfig.TrustedProxyPrefixes()
	if err != nil {
		return nil, err
	}
	spa, err := handlers.NewSPA(o.UI)
	if err != nil {
		return nil, fmt.Errorf("loading UI: %w", err)
	}

	h := &handlers.Handlers{
		Admin: adminSvc, Overview: overviewSvc, Sessions: codec, Origin: origin, Config: stack,
		HostMetrics: hostmetrics.New(hostmetrics.Options{Prom: promRange}),
		Features:    handlers.Features{Version: cfg.AppConfig.Version, GrafanaURL: up.GrafanaURL, Metrics: prom != nil},
	}
	upstream := func(name string) func(op string, status int, err error, d time.Duration) {
		return func(op string, status int, err error, d time.Duration) {
			m.Upstream(context.Background(), name, op, status, err, d)
		}
	}
	if dc := cfg.DockerConfig; dc.ProxyURL != "" {
		self, _ := os.Hostname()
		h.Containers = containers.NewContainersService(containers.Options{
			Client:  dockerapi.NewDockerClient(dc.ProxyURL, dc.Timeout, upstream("docker")),
			Project: dc.Project, Protected: dc.ProtectedServices(), SelfID: self, Metrics: m,
		})
		h.Features.Services = true
	}
	bc := cfg.BosonConfig
	if bc.APIURL != "" {
		h.Profiles = bosonapi.NewProfileClient(bc.APIURL, up.Timeout, upstream("boson_api"))
		h.Features.Profiles = true
		spa.ImgOrigins = append(spa.ImgOrigins, originOf(bc.CDNURL))
	}
	var email authapi.EmailClientImpl
	if up.AuthInternalURL != "" {
		email = authapi.NewEmailClient(up.AuthInternalURL, up.InternalAPIToken, max(up.Timeout, 20*time.Second), upstream("auth_internal"))
	}
	brandingSvc := branding.NewBrandingService(branding.Options{Config: stack, Dir: cfg.StackConfig.BrandingDir, EmailsDir: cfg.StackConfig.EmailsDir, Email: email,
		Public: authapi.NewBrandingClient(up.AuthURL, up.Timeout, observe)})
	h.Branding = brandingSvc
	if bc.DirectoryURL != "" {
		// The network channels (irc.channels) go on the listing.
		channels := func() ([]string, []string, bool) {
			chans, ok := stack.NetworkChannels()
			aj, all := stackconfig.ChannelNames(chans)
			return aj, all, ok
		}
		secret := []byte(bc.DirectoryKey)
		if len(secret) == 0 {
			secret = key
		}
		h.Directory = directory.NewDirectoryService(directory.Options{
			Client: bosonapi.NewDirectoryClient(bc.DirectoryURL, max(up.Timeout, 20*time.Second), upstream("directory")),
			APIURL: bc.DirectoryURL, Registration: bc.DirectoryRegistration, Secret: secret,
			Hostname: bc.DirectoryHostname,
			Live: func() (bool, string) {
				a := stack.Admin()
				return a.DirectoryRegistration, a.DirectoryHostname
			},
			Branding: brandingSvc.DirectoryDocument,
			Channels: channels,
		})
		if store != nil {
			kick := directory.WatchChannels(ctx, h.Directory, channels)
			store.OnChange(func(*configfile.Snapshot) { kick() })
		}
		h.Features.Directory = true // DirectoryRegistration: Handlers.features()
		spa.ImgOrigins = append(spa.ImgOrigins, originOf(bc.CDNURL))
	}
	// Nick registration: the api's internal listener (status, mailbox test).
	var nickClient bosonapi.NickClaimClientImpl
	if bc.APIInternalURL != "" {
		nickClient = bosonapi.NewNickClaimClient(bc.APIInternalURL, bc.APIInternalToken, up.Timeout, upstream("boson_api_internal"))
	}
	nickSvc := nickclaim.New(nickclaim.Options{Client: nickClient})
	h.NickClaims = nickSvc
	stack.AddCheck("nick_claim.", nickSvc.Check)
	// Uploads: the files service's internal listener (status, moderation, usage).
	var filesClient bosonapi.FilesClientImpl
	if bc.FilesInternalURL != "" {
		filesClient = bosonapi.NewFilesClient(bc.FilesInternalURL, bc.FilesInternalToken, up.Timeout, upstream("files_internal"))
	}
	filesSvc := files.New(files.Options{Client: filesClient})
	h.Files = filesSvc
	stack.AddCheck("files.", filesSvc.Check)
	// Federation: auth's federation admin API, the auth.federation config
	// keys, and this server's boson.json.
	fedClient, _ := authClient.(authapi.FederationClient)
	// auth's release (its VERSION_TARGETS entry, by default
	// AUTH_INTERNAL_URL/metrics): the block wording depends on it.
	var authVersion func(context.Context) (string, error)
	for _, t := range versions {
		if t.Name == "auth" {
			hc, u := health.NewHealthChecker(min(up.Timeout, 3*time.Second)), t.URL
			authVersion = func(ctx context.Context) (string, error) { return hc.Version(ctx, u) }
			break
		}
	}
	fedSvc := federation.New(federation.Options{AuthVersion: authVersion, Client: fedClient, Tokens: adminSvc, Config: stack,
		Home: &federation.HomeChecker{Domain: cfg.HomeDomain(), URL: bc.WellKnownURL, HTTP: &nethttp.Client{Timeout: min(up.Timeout, 5*time.Second)}}})
	h.Federation = fedSvc
	stack.AddCheck("auth.federation.", fedSvc.Check)
	if bc.DirectoryLocalURL != "" {
		h.LocalDirectory = directory.NewLocalService(directory.LocalOptions{
			Client: bosonapi.NewOperatorClient(bc.DirectoryLocalURL, bc.DirectoryInternalToken, up.Timeout, upstream("directory_local")),
			URL:    bc.DirectoryLocalURL,
		})
		h.Features.DirectoryLocal = true
	}
	tel := buildTelemetry(cfg, h, stack, authClient, prom, irc, filesClient, upstream)
	h.Telemetry = tel
	if store != nil {
		store.OnChange(func(*configfile.Snapshot) { tel.Kick() }) // admin.telemetry turned on
	}
	if uc := cfg.UpdatesConfig; uc.Enabled {
		h.Updates = buildUpdates(ctx, cfg, irc, upstream, tel.Notify)
		h.Features.Updates = true
	}
	tel.Start(ctx)
	if !tel.Enabled() {
		logger.FromCtx(ctx).Info().Bool("env_off", tel.Locked()).Msg("usage & health stats are off")
	}
	mux := nethttp.NewServeMux()
	h.Register(mux)
	mux.Handle("/", spa)
	public := middleware.Chain(mux,
		middleware.TrustedClientIP(trusted),
		middleware.Observe(m),
		middleware.SecurityHeaders(spa.CSP(connect...)),
	)

	imux := nethttp.NewServeMux()
	imux.HandleFunc("GET /healthz", func(w nethttp.ResponseWriter, _ *nethttp.Request) { _, _ = w.Write([]byte("ok")) })
	// The stack contract's config status (the panel itself).
	imux.HandleFunc("GET /internal/config/status", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(stack.Own())
	})
	imux.HandleFunc("GET /version", func(w nethttp.ResponseWriter, _ *nethttp.Request) { _, _ = w.Write([]byte(cfg.AppConfig.Version)) })
	if o.MetricsHandler != nil {
		imux.Handle("GET /metrics", o.MetricsHandler)
	}
	return &App{Public: public, Internal: imux, Config: cfg}, nil
}

func originOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// Serve runs the public and internal listeners until ctx is cancelled.
func Serve(ctx context.Context, app *App) error {
	log := logger.FromCtx(ctx)
	servers := []*nethttp.Server{{
		Addr: app.Config.HTTPConfig.Addr, Handler: app.Public,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second,
		BaseContext: func(_ net.Listener) context.Context { return ctx },
	}}
	if app.Config.HTTPConfig.InternalAddr != "" {
		servers = append(servers, &nethttp.Server{Addr: app.Config.HTTPConfig.InternalAddr, Handler: app.Internal, ReadHeaderTimeout: 10 * time.Second})
	}
	errc := make(chan error, len(servers))
	for i, srv := range servers {
		name := "public"
		if i == 1 {
			name = "internal"
		}
		go func() {
			log.Info().Str("addr", srv.Addr).Str("listener", name).Msg("HTTP listening")
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, nethttp.ErrServerClosed) {
				errc <- fmt.Errorf("%s listener: %w", name, err)
			}
		}()
	}
	var err error
	select {
	case <-ctx.Done():
	case err = <-errc:
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, srv := range servers {
		_ = srv.Shutdown(sctx)
	}
	return err
}

// buildUpdates wires the Updates page: GitHub releases, the image registry,
// installed versions from the socket proxy, and (with the updates proxy)
// helper jobs that apply them.
func buildUpdates(ctx context.Context, cfg config.Config, irc *config.IRCTarget,
	upstream func(string) func(string, int, error, time.Duration), onFinish func(string)) *updates.Service {
	uc, up, dc := cfg.UpdatesConfig, cfg.UpstreamConfig, cfg.DockerConfig
	reg := updates.NewOCIRegistry(10 * time.Second)
	reg.Override = uc.RegistryURL
	o := updates.Options{
		Releases: updates.NewGitHub(uc.GitHubURL, uc.GitHubToken, 15*time.Second),
		Registry: reg, Project: dc.Project, SelfVersion: cfg.AppConfig.Version, StackVersion: uc.StackVersion,
		Interval: uc.Interval, StateDir: uc.StateDir, BackupRoot: uc.BackupDir, Keep: uc.BackupKeep,
		MinFree: uc.MinFreeMB << 20, UtilImage: uc.UtilImage, HealthTimeout: uc.HealthTimeout,
		OnFinish: onFinish,
	}
	if dc.ProxyURL != "" {
		o.Docker = dockerapi.NewDockerClient(dc.ProxyURL, dc.Timeout, upstream("docker"))
	}
	switch {
	case uc.DockerProxyURL == "":
		o.ApplyOff = "updates are shown but not applied here: the stack's updates profile (UPDATES_DOCKER_PROXY_URL) is off"
	case dc.ProxyURL == "":
		o.ApplyOff = "applying updates needs DOCKER_PROXY_URL too (to see what runs)"
	default:
		self, _ := os.Hostname()
		o.Jobs = &updates.Helper{
			Client:  dockerapi.NewJobsClient(uc.DockerProxyURL, 2*time.Minute, upstream("docker_updates")),
			Project: dc.Project, SelfID: self, Image: uc.HelperImage, BackupRoot: uc.BackupDir,
		}
	}
	checks := updates.DefaultHTTPChecks()
	over, _ := uc.Checks()
	for _, t := range over {
		for i := range checks {
			if checks[i].ID == t.Name {
				checks[i].URL = t.URL
			}
		}
	}
	v := &updates.StackVerifier{HTTP: &nethttp.Client{Timeout: 5 * time.Second}, AuthURL: up.AuthURL, Checks: checks,
		IRC: ircprobe.NewIRCProber(10 * time.Second)}
	if irc != nil {
		v.IRCAddr, v.IRCServer, v.IRCTLS = irc.Address, irc.ServerName, irc.TLS
	}
	o.Verifier = v
	svc := updates.New(o)
	svc.Start(ctx)
	return svc
}

// buildTelemetry wires the usage & health stats report (the stack's
// docs/telemetry.md): where each value comes from. h.Updates and
// h.Directory are read when a report is built.
func buildTelemetry(cfg config.Config, h *handlers.Handlers, stack *stackconfig.Service, auth authapi.AuthClientImpl,
	prom promql.PromClientImpl, irc *config.IRCTarget, files bosonapi.FilesClientImpl,
	upstream func(string) func(string, int, error, time.Duration)) *telemetry.Service {
	dc, us := cfg.DockerConfig, cfg.UsageStats
	src := telemetry.Sources{
		Project: dc.Project, SelfVersion: cfg.AppConfig.Version, GOARCH: runtime.GOARCH,
		PublicConfig: auth.PublicConfig,
		Config:       stack.FileValue,
		StackVersion: func(ctx context.Context) string {
			if h.Updates != nil {
				for _, c := range h.Updates.Status(ctx).Components {
					if c.ID == "stack" && c.Installed != "" {
						return c.Installed
					}
				}
			}
			return cfg.UpdatesConfig.StackVersion
		},
		Runs: func(ctx context.Context) []updates.RunSummary {
			if h.Updates == nil {
				return nil
			}
			return h.Updates.Status(ctx).History
		},
		DirectoryListed: func(ctx context.Context) bool {
			if h.Directory == nil {
				return false
			}
			st := h.Directory.Status(ctx, "")
			return st != nil && st.Public != nil
		},
	}
	if dc.ProxyURL != "" {
		d := dockerapi.NewDockerClient(dc.ProxyURL, min(dc.Timeout, 10*time.Second), upstream("docker"))
		src.Docker = d
		// host_size: GET /info, where the stack's docker-proxy allows it.
		if ic, ok := d.(interface {
			Info(context.Context) (*dockerapi.Info, error)
		}); ok {
			src.DockerInfo = ic.Info
		}
	}
	if prom != nil {
		src.Prom = prom
	}
	if irc != nil {
		p := &ircprobe.IRCProber{Timeout: 10 * time.Second}
		t := *irc
		src.IRC = func(ctx context.Context) *ircprobe.Result {
			r := p.Counts(ctx, t.Address, t.ServerName, t.TLS)
			return &r
		}
	}
	if files != nil {
		src.Files = files
	}
	if up := cfg.UpstreamConfig; up.AuthInternalURL != "" && up.InternalAPIToken != "" {
		src.AuthStats = authapi.NewStatsClient(up.AuthInternalURL, up.InternalAPIToken, 10*time.Second).Stats
	}
	if us.ErgoAPIURL != "" {
		src.Ergo = ircstats.NewErgo(us.ErgoAPIURL, us.ErgoAPIToken, 10*time.Second)
	}
	if us.BridgeAPIURL != "" {
		// up to 5 s while evrd asks the network for LUSERS
		src.Bridge = ircstats.NewBridge(us.BridgeAPIURL, us.BridgeToken, 10*time.Second)
	}
	return telemetry.New(telemetry.Options{
		Dir: us.Dir, URL: telemetry.Endpoint(us.URL), EnvOff: telemetry.Off(us.Telemetry),
		FileOn:     func() bool { return stack.Admin().Telemetry },
		Collector:  &telemetry.Collector{S: src},
		Auth:       auth,
		HTTP:       &nethttp.Client{Timeout: telemetry.SendTimeout},
		StartDelay: us.StartDelay,
	})
}
