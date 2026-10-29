// Package work is the unit of work of standard.md section 4: one wide event per CLI command
// run (command.run) and per handled HTTP request (request.handled), a span that continues the
// caller's trace, attributes gathered while the unit runs, TRACEPARENT for child processes and
// job.progress for long jobs. It works without lifecycle.Run and without tracing.Setup; with
// tracing off the span is a no-op and the line carries no trace ids of its own.
//
//	ctx, end := work.Command(ctx, log, "index rebuild", flags)
//	work.Add(ctx, "index.size", n)
//	end(err)
//
//	srv := &http.Server{Handler: work.HTTP(log)(mux)}
//
// Outgoing calls pass the trace on without a wrapper of this library: an HTTP client uses
// go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp,
//
//	client := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
//
// and a child process gets Env(ctx) appended to its environment.
package work

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/AndreiD108/d108-go/logging"
)

// Add puts one attribute on the current unit of work: every later line written with ctx and
// the final event carry it. Outside a unit it does nothing.
func Add(ctx context.Context, key string, value any) {
	logging.AttrSetFrom(ctx).Add(slog.Any(key, value))
}

// AddAll puts attrs on the current unit of work. See Add.
func AddAll(ctx context.Context, attrs ...slog.Attr) {
	logging.AttrSetFrom(ctx).Add(attrs...)
}

// spanAttrs turns slog attributes into span attributes through logging.Flatten, so the span
// carries the same keys and values as the event line. The exception.* keys are left out: the
// span records the error as an exception event.
func spanAttrs(attrs []slog.Attr) []attribute.KeyValue {
	flat := logging.Flatten(attrs...)
	out := make([]attribute.KeyValue, 0, len(flat))
	for _, f := range flat {
		if strings.HasPrefix(f.Key, "exception.") {
			continue
		}
		switch v := f.Value.(type) {
		case string:
			out = append(out, attribute.String(f.Key, v))
		case int64:
			out = append(out, attribute.Int64(f.Key, v))
		case uint64:
			if v > math.MaxInt64 {
				out = append(out, attribute.String(f.Key, strconv.FormatUint(v, 10)))
			} else {
				out = append(out, attribute.Int64(f.Key, int64(v)))
			}
		case float64:
			out = append(out, attribute.Float64(f.Key, v))
		case bool:
			out = append(out, attribute.Bool(f.Key, v))
		default:
			out = append(out, attribute.String(f.Key, fmt.Sprint(v)))
		}
	}
	return out
}

// endSpan puts attrs on span, records err when there is one and ends the span.
func endSpan(span trace.Span, attrs []slog.Attr, err error, description string) {
	span.SetAttributes(spanAttrs(attrs)...)
	if err != nil {
		span.RecordError(err)
	}
	if description != "" {
		span.SetStatus(codes.Error, description)
	}
	span.End()
}
