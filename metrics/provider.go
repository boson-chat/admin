package metrics

import (
	"context"
	"errors"
	"fmt"
	nethttp "net/http"
	"strings"

	"github.com/boson-chat/admin/config"
	"github.com/boson-chat/admin/internal/logger"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// Provider owns the OTel meter provider and its exporters.
type Provider struct {
	mp      *sdkmetric.MeterProvider
	handler nethttp.Handler
	otlp    bool
}

// NewProvider builds the meter provider: a Prometheus reader served by
// Handler (GET /metrics on the internal listener), an OTLP/HTTP periodic
// reader when an endpoint is configured, and Go runtime metrics.
func NewProvider(ctx context.Context, cfg config.Config) (*Provider, error) {
	log := logger.FromCtx(ctx)
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		log.Warn().Err(err).Msg("otel")
	}))
	res, err := resource.New(ctx,
		resource.WithSchemaURL(semconv.SchemaURL),
		resource.WithAttributes(
			semconv.ServiceName("boson-admin"),
			semconv.ServiceVersion(cfg.AppConfig.Version),
			semconv.DeploymentEnvironmentNameKey.String(cfg.AppConfig.Env),
			attribute.String("deployment.environment", cfg.AppConfig.Env),
		),
		resource.WithHost(),
		resource.WithTelemetrySDK(),
		resource.WithFromEnv(),
	)
	if err != nil && !errors.Is(err, resource.ErrPartialResource) {
		return nil, fmt.Errorf("metrics: resource: %w", err)
	}
	reg := prometheus.NewRegistry()
	promReader, err := otelprom.New(otelprom.WithRegisterer(reg), otelprom.WithoutScopeInfo())
	if err != nil {
		return nil, fmt.Errorf("metrics: prometheus exporter: %w", err)
	}
	opts := []sdkmetric.Option{
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(promReader),
		sdkmetric.WithExemplarFilter(exemplar.AlwaysOffFilter),
	}
	p := &Provider{handler: promhttp.HandlerFor(reg, promhttp.HandlerOpts{})}
	if endpoint, ok, err := otlpEndpoint(cfg.TelemetryConfig); err != nil {
		return nil, err
	} else if ok {
		exp, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(endpoint))
		if err != nil {
			return nil, fmt.Errorf("metrics: otlp http exporter: %w", err)
		}
		opts = append(opts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp)))
		p.otlp = true
	}
	p.mp = sdkmetric.NewMeterProvider(opts...)
	if err := runtime.Start(runtime.WithMeterProvider(p.mp)); err != nil {
		_ = p.mp.Shutdown(ctx)
		return nil, fmt.Errorf("metrics: runtime: %w", err)
	}
	return p, nil
}

func (p *Provider) MeterProvider() metric.MeterProvider { return p.mp }

// Handler serves the Prometheus exposition.
func (p *Provider) Handler() nethttp.Handler { return p.handler }

// OTLP reports whether OTLP push is on.
func (p *Provider) OTLP() bool { return p.otlp }

// Shutdown flushes pending OTLP exports and stops all readers.
func (p *Provider) Shutdown(ctx context.Context) error { return p.mp.Shutdown(ctx) }

// otlpEndpoint returns the OTLP/HTTP metrics URL, if push is on. Only
// http/protobuf is supported (the panel's metric volume is tiny).
func otlpEndpoint(c config.TelemetryConfig) (string, bool, error) {
	if c.OTLPEndpoint == "" && c.OTLPMetricsEndpoint == "" {
		return "", false, nil
	}
	if strings.EqualFold(strings.TrimSpace(c.MetricsExporter), "none") {
		return "", false, nil
	}
	proto := c.OTLPMetricsProtocol
	if proto == "" {
		proto = c.OTLPProtocol
	}
	if proto != "" && proto != "http/protobuf" {
		return "", false, fmt.Errorf("metrics: OTLP protocol %q is not supported (use http/protobuf)", proto)
	}
	if c.OTLPMetricsEndpoint != "" {
		return c.OTLPMetricsEndpoint, true, nil
	}
	return strings.TrimRight(c.OTLPEndpoint, "/") + "/v1/metrics", true, nil
}
