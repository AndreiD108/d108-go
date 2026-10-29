package tracing

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/goleak"

	"github.com/AndreiD108/d108-go/identity"
)

func restoreGlobals(t *testing.T) {
	t.Helper()
	tp, prop, eh := otel.GetTracerProvider(), otel.GetTextMapPropagator(), otel.GetErrorHandler()
	t.Cleanup(func() {
		otel.SetTracerProvider(tp)
		otel.SetTextMapPropagator(prop)
		otel.SetErrorHandler(eh)
	})
}

func TestSetupOffWithoutEndpoint(t *testing.T) {
	defer goleak.VerifyNone(t)
	restoreGlobals(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	otel.SetTracerProvider(noop.NewTracerProvider())

	shutdown := Setup(context.Background(), identity.New("tracetest"))
	if Enabled() {
		t.Error("Enabled with the variable unset")
	}
	if _, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider); ok {
		t.Error("an SDK provider was installed")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

func TestSetupOnWithEndpoint(t *testing.T) {
	restoreGlobals(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")

	shutdown := Setup(context.Background(), identity.New("tracetest"))
	if !Enabled() {
		t.Error("not Enabled with the variable set")
	}
	if _, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider); !ok {
		t.Fatalf("provider is %T", otel.GetTracerProvider())
	}
	fields := otel.GetTextMapPropagator().Fields()
	if len(fields) != 3 || fields[0] != "traceparent" || fields[1] != "tracestate" || fields[2] != "baggage" {
		t.Errorf("propagator fields %v", fields)
	}
	_, span := otel.Tracer("t").Start(context.Background(), "x")
	span.End()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Nothing listens on port 1, so the flush fails; the shutdown must still return in time.
	start := time.Now()
	_ = shutdown(ctx)
	if time.Since(start) > 3*time.Second {
		t.Errorf("shutdown took %v", time.Since(start))
	}
}
