package work

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/logging"
)

// RequestOption configures Request.
type RequestOption func(*requestOptions)

type requestOptions struct {
	traceparent string
	caller      string
}

// Traceparent continues the trace of the W3C traceparent string the request carried. Empty
// or malformed, the request starts a root span and the value is dropped.
func Traceparent(s string) RequestOption {
	return func(o *requestOptions) { o.traceparent = s }
}

// Caller puts caller.id on every line of the request and on request.handled. Empty adds
// nothing.
func Caller(id string) RequestOption {
	return func(o *requestOptions) { o.caller = id }
}

// Request opens the unit of work of one request a non-HTTP server handles (a socket or RPC
// request). method is the operation's name ("get-plot"): it names the span and is rpc.method
// on every line of the request. The span continues the trace of Traceparent; without one,
// or with one that does not parse, it is a root, whatever span ctx carries. The returned
// context carries the span, rpc.method,
// caller.id from Caller, and the attribute set Add writes to. end writes request.handled with
// rpc.method, duration_ms, outcome (ok, error, cancelled, by the error's class as Command
// classes it: INFO, ERROR, WARN), error.type when err is not nil and exception.* on outcome
// error, and ends the span; a second call does nothing. A nil log uses the logger in ctx.
//
// The outcome is the app's reading of the request: a refusal the app answers with its own
// code is end(nil) with the code added through Add. Request recovers no panic; the reply to
// the client on a panic is the app's.
func Request(ctx context.Context, log *slog.Logger, method string, opts ...RequestOption) (context.Context, func(err error)) {
	var o requestOptions
	for _, opt := range opts {
		opt(&o)
	}
	if log == nil {
		log = logging.FromContext(ctx)
	}
	// The request's parent is the traceparent it carried or nothing: a span in ctx (the
	// server's own) must not become its parent, so ctx keeps its values and cancellation but
	// loses the span.
	ctx = trace.ContextWithSpanContext(ctx, trace.SpanContext{})
	if o.traceparent != "" {
		ctx = w3c.Extract(ctx, propagation.MapCarrier{"traceparent": o.traceparent})
	}
	ctx, span := otel.Tracer(tracerName).Start(ctx, method, trace.WithSpanKind(trace.SpanKindServer))
	ctx = logging.WithAttrs(ctx, slog.String("rpc.method", method))
	ctx, set := logging.WithAttrSet(ctx)
	if o.caller != "" {
		set.Add(slog.String("caller.id", o.caller))
	}
	start := time.Now()
	var once sync.Once
	end := func(err error) {
		once.Do(func() {
			outcome, level := outcomeOf(err)
			attrs := []slog.Attr{
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.String("outcome", outcome),
			}
			if err != nil {
				attrs = append(attrs, slog.String("error.type", string(failure.ClassOf(err))))
			}
			line := append([]slog.Attr{logging.Event("request.handled")}, attrs...)
			var spanErr error
			description := ""
			if outcome == OutcomeError {
				line = append(line, slog.Any("error", err))
				spanErr, description = err, err.Error()
			}
			log.LogAttrs(ctx, level, "", line...)
			endSpan(span, append(logging.Attrs(ctx), attrs...), spanErr, description)
		})
	}
	return ctx, end
}
