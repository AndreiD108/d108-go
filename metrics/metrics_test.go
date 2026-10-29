package metrics

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.uber.org/goleak"

	"github.com/AndreiD108/d108-go/identity"
)

func restoreGlobals(t *testing.T) {
	t.Helper()
	mp, eh := otel.GetMeterProvider(), otel.GetErrorHandler()
	t.Cleanup(func() {
		otel.SetMeterProvider(mp)
		otel.SetErrorHandler(eh)
	})
}

func TestSetupOffWithoutEndpoint(t *testing.T) {
	defer goleak.VerifyNone(t)
	restoreGlobals(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	otel.SetMeterProvider(noop.NewMeterProvider())

	shutdown := Setup(context.Background(), identity.New("metrictest"))
	if Enabled() {
		t.Error("Enabled with the variable unset")
	}
	if _, ok := otel.GetMeterProvider().(*sdkmetric.MeterProvider); ok {
		t.Error("an SDK provider was installed")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

func TestSetupOnWithEndpoint(t *testing.T) {
	restoreGlobals(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")

	shutdown := Setup(context.Background(), identity.New("metrictest"))
	if _, ok := otel.GetMeterProvider().(*sdkmetric.MeterProvider); !ok {
		t.Fatalf("provider is %T", otel.GetMeterProvider())
	}
	counter, err := otel.Meter("t").Int64Counter("hits")
	if err != nil {
		t.Fatal(err)
	}
	counter.Add(context.Background(), 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	_ = shutdown(ctx)
	if time.Since(start) > 3*time.Second {
		t.Errorf("shutdown took %v", time.Since(start))
	}
}
