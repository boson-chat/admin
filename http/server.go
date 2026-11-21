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
	"time"

	"github.com/boson-chat/admin/config"
	"github.com/boson-chat/admin/http/handlers"
	"github.com/boson-chat/admin/http/middleware"
	"github.com/boson-chat/admin/internal/clients/authapi"
	"github.com/boson-chat/admin/internal/clients/bosonapi"
	"github.com/boson-chat/admin/internal/clients/dockerapi"
	"github.com/boson-chat/admin/internal/clients/health"
	"github.com/boson-chat/admin/internal/clients/ircprobe"
	"github.com/boson-chat/admin/internal/clients/promql"
	"github.com/boson-chat/admin/internal/clients/tlsprobe"
	"github.com/boson-chat/admin/internal/configfile"
	"github.com/boson-chat/admin/internal/logger"
	"github.com/boson-chat/admin/internal/services/admin"
	"github.com/boson-chat/admin/internal/services/branding"
	"github.com/boson-chat/admin/internal/services/containers"
	"github.com/boson-chat/admin/internal/services/directory"
	"github.com/boson-chat/admin/internal/services/files"
	"github.com/boson-chat/admin/internal/services/hostmetrics"
	"github.com/boson-chat/admin/internal/services/nickclaim"
	"github.com/boson-chat/admin/internal/services/overview"
	"github.com/boson-chat/admin/internal/services/stackconfig"
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
		})
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
	if bc.DirectoryLocalURL != "" {
		h.LocalDirectory = directory.NewLocalService(directory.LocalOptions{
			Client: bosonapi.NewOperatorClient(bc.DirectoryLocalURL, bc.DirectoryInternalToken, up.Timeout, upstream("directory_local")),
			URL:    bc.DirectoryLocalURL,
		})
		h.Features.DirectoryLocal = true
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
