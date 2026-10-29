package work

import (
	"context"
	"os"
	"strings"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Trace context travels between processes in these variables (W3C traceparent and
// tracestate, upper case as environment names).
const (
	EnvTraceparent = "TRACEPARENT"
	EnvTracestate  = "TRACESTATE"
)

// The W3C propagator is used directly, so continuing a trace does not depend on
// tracing.Setup having run.
var w3c = propagation.TraceContext{}

// Env returns TRACEPARENT= (and TRACESTATE= when set) for a child process:
//
//	cmd.Env = append(os.Environ(), work.Env(ctx)...)
//
// It returns nil when ctx has no valid span.
func Env(ctx context.Context) []string {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return nil
	}
	c := propagation.MapCarrier{}
	w3c.Inject(ctx, c)
	var env []string
	if v := c.Get("traceparent"); v != "" {
		env = append(env, EnvTraceparent+"="+v)
	}
	if v := c.Get("tracestate"); v != "" {
		env = append(env, EnvTracestate+"="+v)
	}
	return env
}

// parentFromEnv returns ctx with the span context of TRACEPARENT in the environment, when ctx
// has no valid span of its own and the variable holds a valid header.
func parentFromEnv(ctx context.Context) context.Context {
	if trace.SpanContextFromContext(ctx).IsValid() {
		return ctx
	}
	tp := strings.TrimSpace(os.Getenv(EnvTraceparent))
	if tp == "" {
		return ctx
	}
	c := propagation.MapCarrier{"traceparent": tp}
	if ts := strings.TrimSpace(os.Getenv(EnvTracestate)); ts != "" {
		c.Set("tracestate", ts)
	}
	return w3c.Extract(ctx, c)
}
