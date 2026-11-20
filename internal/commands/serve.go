package commands

import (
	"context"
	"crypto/rand"
	"os/signal"
	"syscall"
	"time"

	"github.com/boson-chat/admin/config"
	adminhttp "github.com/boson-chat/admin/http"
	"github.com/boson-chat/admin/internal/logger"
	"github.com/boson-chat/admin/metrics"
	"github.com/boson-chat/admin/ui"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve the admin UI and API",
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := config.LoadConfig()
		if err != nil {
			return err
		}
		logger.Logger(
			logger.WithServerName("admin"),
			logger.WithVersion(cfg.AppConfig.Version),
			logger.WithEnvironment(cfg.AppConfig.Env),
			logger.WithLevel(cfg.AppConfig.LogLevel),
		)
		ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		log := logger.FromCtx(ctx)

		mp, err := metrics.NewProvider(ctx, cfg)
		if err != nil {
			return err
		}
		defer func() {
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = mp.Shutdown(sctx)
		}()
		m, err := metrics.New(mp.MeterProvider())
		if err != nil {
			return err
		}

		opts := adminhttp.Options{Metrics: m, MetricsHandler: mp.Handler(), UI: ui.FS()}
		if cfg.SessionConfig.Key == "" {
			// Only reachable with ENV=development (Validate refuses otherwise).
			opts.SessionKey = make([]byte, 32)
			_, _ = rand.Read(opts.SessionKey)
			log.Warn().Msg("SESSION_KEY is empty: using a random key, sessions end on restart (dev only)")
		}
		if !cfg.SessionConfig.CookieSecure {
			log.Warn().Msg("COOKIE_SECURE=false: the session cookie is sent over plain http (dev only)")
		}
		if cfg.UpstreamConfig.MetricsURL == "" {
			log.Info().Msg("METRICS_URL is empty: the overview and the Metrics page show no live metrics")
		}
		app, err := adminhttp.Build(ctx, cfg, opts)
		if err != nil {
			return err
		}
		return adminhttp.Serve(ctx, app)
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}
