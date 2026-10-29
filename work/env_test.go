package work

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/AndreiD108/d108-go/identity"
	"github.com/AndreiD108/d108-go/logging"
)

const childMarker = "D108_WORK_CHILD"

func TestEnvWithoutSpanIsNil(t *testing.T) {
	if env := Env(context.Background()); env != nil {
		t.Errorf("Env = %v, want nil", env)
	}
}

func TestEnvCarriesTraceparentAndTracestate(t *testing.T) {
	withTracer(t)
	ts, _ := trace.TraceState{}.Insert("vendor", "x")
	parent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: trace.FlagsSampled,
		TraceState: ts,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), parent)
	env := Env(ctx)
	if len(env) != 2 || env[0] != "TRACEPARENT=00-0102030405060708090a0b0c0d0e0f10-0102030405060708-01" || env[1] != "TRACESTATE=vendor=x" {
		t.Errorf("Env = %v", env)
	}
}

// TestEnvRoundTripThroughChildProcess runs the test binary as a child with work.Env in its
// environment and checks the child's command.run continues the parent's trace.
func TestEnvRoundTripThroughChildProcess(t *testing.T) {
	if os.Getenv(childMarker) != "" {
		t.Skip("child")
	}
	withTracer(t)
	t.Setenv(EnvTraceparent, "")
	log, _ := newLog(t)
	ctx, end := Command(context.Background(), log, "parent", nil)
	defer end(nil)
	parentSC := trace.SpanContextFromContext(ctx)

	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperChild$", "-test.v")
	cmd.Env = append(os.Environ(), childMarker+"=1")
	cmd.Env = append(cmd.Env, Env(ctx)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("child: %v\n%s\n%s", err, out, stderr.String())
	}
	var line map[string]any
	for _, raw := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(raw, `{"timestamp"`) {
			var buf bytes.Buffer
			buf.WriteString(raw)
			line = lastLine(t, &buf)
		}
	}
	if line == nil {
		t.Fatalf("no line in child output:\n%s", out)
	}
	want(t, line, "event_name", "command.run")
	want(t, line, "command", "child")
	want(t, line, "trace_id", parentSC.TraceID().String())
	if line["span_id"] == parentSC.SpanID().String() {
		t.Errorf("child span_id equals the parent's %s", parentSC.SpanID())
	}
}

// TestHelperChild is the child of TestEnvRoundTripThroughChildProcess; it writes one
// command.run to stdout with its own in-memory tracer, so its span is a real child.
func TestHelperChild(t *testing.T) {
	if os.Getenv(childMarker) == "" {
		t.Skip("parent")
	}
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(tracetest.NewInMemoryExporter())))
	log := logging.New(identity.New("worktest"), logging.Writer(os.Stdout))
	_, end := Command(context.Background(), log, "child", nil)
	end(nil)
}
