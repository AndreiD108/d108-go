// Package otelres builds the OTel resource tracing and metrics share, and holds their common
// on/off switch.
package otelres

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"

	"github.com/AndreiD108/d108-go/identity"
	"github.com/AndreiD108/d108-go/logging"
)

// EndpointVar is the variable that turns the trace and metric SDKs on: the OTLP endpoint of
// the agent, as a URL ("http://127.0.0.1:4317"). Unset means both SDKs stay no-ops.
const EndpointVar = "OTEL_EXPORTER_OTLP_ENDPOINT"

// Enabled reports whether EndpointVar is set.
func Enabled() bool { return os.Getenv(EndpointVar) != "" }

// New returns the resource for id: service.name, service.version, service.instance.id and
// process.pid over the SDK's defaults (telemetry.sdk.*, OTEL_RESOURCE_ATTRIBUTES).
func New(id identity.Identity) *resource.Resource {
	// Schemaless, so the merge keeps the SDK's schema URL instead of failing on a mismatch.
	own := resource.NewSchemaless(
		semconv.ServiceName(id.Name),
		semconv.ServiceVersion(id.Version),
		semconv.ServiceInstanceID(id.InstanceID),
		semconv.ProcessPID(id.PID),
	)
	res, err := resource.Merge(resource.Default(), own)
	if err != nil {
		return own
	}
	return res
}

// SetErrorHandler sends the SDK's own errors (an export that failed) to the logger in ctx as
// WARN lines instead of the SDK's stderr print.
func SetErrorHandler(ctx context.Context) {
	log := logging.FromContext(ctx)
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		log.LogAttrs(context.Background(), slog.LevelWarn, "otel sdk error", slog.Any("error", err))
	}))
}
