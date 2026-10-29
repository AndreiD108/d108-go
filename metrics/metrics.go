// Package metrics turns the OTel metric SDK on when OTEL_EXPORTER_OTLP_ENDPOINT is set and
// leaves it a no-op otherwise. Apps make instruments with otel.Meter(name) as usual; the
// resource is the identity's, the same as tracing's.
//
//	shutdown := metrics.Setup(ctx, id)
//	defer shutdown(ctx)
package metrics

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/AndreiD108/d108-go/identity"
	"github.com/AndreiD108/d108-go/internal/otelres"
)

// ExportInterval is how often the periodic reader sends the collected metrics.
const ExportInterval = 60 * time.Second

// Setup installs the global MeterProvider when OTEL_EXPORTER_OTLP_ENDPOINT is set: OTLP over
// gRPC to that endpoint and a periodic reader every ExportInterval. The returned function
// collects once more, flushes and stops the provider within ctx's deadline; it returns nil when
// the SDK is off. Setup never fails: when the exporter cannot be built the SDK stays off and
// the error goes through the OTel error handler.
func Setup(ctx context.Context, id identity.Identity) func(context.Context) error {
	if !otelres.Enabled() {
		return func(context.Context) error { return nil }
	}
	otelres.SetErrorHandler(ctx)
	exp, err := otlpmetricgrpc.New(ctx)
	if err != nil {
		otel.Handle(err)
		return func(context.Context) error { return nil }
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(ExportInterval))),
		sdkmetric.WithResource(otelres.New(id)),
	)
	otel.SetMeterProvider(mp)
	return mp.Shutdown
}

// Enabled reports whether Setup turns the SDK on in this environment.
func Enabled() bool { return otelres.Enabled() }
