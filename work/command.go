package work

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/logging"
)

const tracerName = "github.com/AndreiD108/d108-go/work"

// Outcome values of command.run and request.handled.
const (
	OutcomeOK        = "ok"
	OutcomeError     = "error"
	OutcomeCancelled = "cancelled"
)

// Command opens the unit of work of one CLI command run. name is the subcommand path
// ("index rebuild"); flags are the flag names given, logged as args.flags. The span continues
// the trace of a parent span in ctx, else of TRACEPARENT in the environment, else it is a root.
// The returned context carries the span, command and args.flags, and the attribute set Add
// writes to. end writes command.run and ends the span; a second call does nothing. A nil log
// uses the logger in ctx (logging.FromContext).
func Command(ctx context.Context, log *slog.Logger, name string, flags []string) (context.Context, func(err error)) {
	if log == nil {
		log = logging.FromContext(ctx)
	}
	ctx = parentFromEnv(ctx)
	ctx, span := otel.Tracer(tracerName).Start(ctx, name, trace.WithSpanKind(trace.SpanKindInternal))
	ctx = logging.WithAttrs(ctx, slog.String("command", name), slog.String("args.flags", strings.Join(flags, " ")))
	ctx, _ = logging.WithAttrSet(ctx)
	start := time.Now()
	var once sync.Once
	end := func(err error) {
		once.Do(func() {
			outcome, level := outcomeOf(err)
			attrs := []slog.Attr{
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.Int("exit_code", failure.ExitCode(err)),
				slog.String("outcome", outcome),
			}
			if err != nil {
				attrs = append(attrs, slog.String("error.type", string(failure.ClassOf(err))))
			}
			line := append([]slog.Attr{logging.Event("command.run")}, attrs...)
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

// outcomeOf maps the error a unit ended with to its outcome and severity: nil is ok at INFO,
// a cancelled or terminated class is cancelled at WARN, anything else is error at ERROR.
func outcomeOf(err error) (string, slog.Level) {
	switch {
	case err == nil:
		return OutcomeOK, slog.LevelInfo
	case failure.Is(err, failure.Cancelled), failure.Is(err, failure.Terminated):
		return OutcomeCancelled, slog.LevelWarn
	default:
		return OutcomeError, slog.LevelError
	}
}
