package logging

import (
	"context"
	"log/slog"
)

type attrsKey struct{}

type loggerKey struct{}

// WithAttrs returns a context whose lines all carry attrs. The handler reads them on every
// record logged with that context, after the logger's own attributes and before the record's,
// so a record attribute with the same key wins. Calls accumulate: a child context keeps the
// parent's attributes and adds its own.
func WithAttrs(ctx context.Context, attrs ...slog.Attr) context.Context {
	if len(attrs) == 0 {
		return ctx
	}
	parent := Attrs(ctx)
	merged := make([]slog.Attr, 0, len(parent)+len(attrs))
	merged = append(merged, parent...)
	merged = append(merged, attrs...)
	return context.WithValue(ctx, attrsKey{}, merged)
}

// Attrs returns a copy of the attributes WithAttrs put in ctx, oldest first.
func Attrs(ctx context.Context) []slog.Attr {
	if ctx == nil {
		return nil
	}
	attrs, _ := ctx.Value(attrsKey{}).([]slog.Attr)
	return append([]slog.Attr(nil), attrs...)
}

// NewContext returns a context carrying log. lifecycle.Run sets it; cli.Execute reads it.
func NewContext(ctx context.Context, log *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, log)
}

// FromContext returns the logger NewContext put in ctx, or slog.Default when there is none.
func FromContext(ctx context.Context) *slog.Logger {
	if ctx != nil {
		if log, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
			return log
		}
	}
	return slog.Default()
}
