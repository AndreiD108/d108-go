// Package tracing turns the OTel trace SDK on when OTEL_EXPORTER_OTLP_ENDPOINT is set and
// leaves it a no-op otherwise. The resource comes from the identity, so spans and lines share
// service.name, service.version and service.instance.id.
//
//	shutdown := tracing.Setup(ctx, id)
//	defer shutdown(ctx)
package tracing

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/AndreiD108/d108-go/identity"
	"github.com/AndreiD108/d108-go/internal/otelres"
)

// Setup installs the global TracerProvider and the W3C propagators when
// OTEL_EXPORTER_OTLP_ENDPOINT is set: OTLP over gRPC to that endpoint (the exporter reads the
// OTEL_EXPORTER_OTLP_* variables), a batch processor, the sampler from OTEL_TRACES_SAMPLER
// (default: parent-based, always on). SDK errors go to the logger in ctx as WARN lines. The
// returned function flushes and stops the provider within ctx's deadline; it returns nil when
// the SDK is off. Setup never fails: when the exporter cannot be built the SDK stays off and
// the error is logged through the same handler.
func Setup(ctx context.Context, id identity.Identity) func(context.Context) error {
	if !otelres.Enabled() {
		return func(context.Context) error { return nil }
	}
	otelres.SetErrorHandler(ctx)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	exp, err := otlptracegrpc.New(ctx)
	if err != nil {
		otel.Handle(err)
		return func(context.Context) error { return nil }
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(otelres.New(id)),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown
}

// Enabled reports whether Setup turns the SDK on in this environment.
func Enabled() bool { return otelres.Enabled() }
