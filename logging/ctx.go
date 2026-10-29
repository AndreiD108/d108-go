package logging

import (
	"context"
	"log/slog"
	"sync"
)

type attrsKey struct{}

type setKey struct{}

type loggerKey struct{}

// WithAttrs returns a context whose lines all carry attrs. The handler reads them on every
// record logged with that context, after the logger's own attributes and before the record's,
// so a record attribute with the same key wins. Calls accumulate: a child context keeps the
// parent's attributes and adds its own.
func WithAttrs(ctx context.Context, attrs ...slog.Attr) context.Context {
	if len(attrs) == 0 {
		return ctx
	}
	parent := fixedAttrs(ctx)
	merged := make([]slog.Attr, 0, len(parent)+len(attrs))
	merged = append(merged, parent...)
	merged = append(merged, attrs...)
	return context.WithValue(ctx, attrsKey{}, merged)
}

// Attrs returns a copy of the attributes in ctx, oldest first: those given to WithAttrs, then
// those added to the AttrSet of the unit of work (WithAttrSet). A later key wins in the line.
func Attrs(ctx context.Context) []slog.Attr {
	if ctx == nil {
		return nil
	}
	fixed := fixedAttrs(ctx)
	set := AttrSetFrom(ctx)
	if set == nil {
		return append([]slog.Attr(nil), fixed...)
	}
	return set.appendTo(append(make([]slog.Attr, 0, len(fixed)+4), fixed...))
}

func fixedAttrs(ctx context.Context) []slog.Attr {
	attrs, _ := ctx.Value(attrsKey{}).([]slog.Attr)
	return attrs
}

// An AttrSet holds the attributes a unit of work gathers while it runs. work.Command and
// work.HTTP put one in the context with WithAttrSet; work.Add appends to it; every line written
// with that context carries what the set holds at that moment. A set made inside another unit
// keeps the outer set's attributes in front of its own.
type AttrSet struct {
	parent *AttrSet
	mu     sync.Mutex
	attrs  []slog.Attr
}

// WithAttrSet returns a context carrying a new, empty AttrSet and the set itself.
func WithAttrSet(ctx context.Context) (context.Context, *AttrSet) {
	s := &AttrSet{parent: AttrSetFrom(ctx)}
	return context.WithValue(ctx, setKey{}, s), s
}

// AttrSetFrom returns the AttrSet in ctx, or nil when no unit of work is open.
func AttrSetFrom(ctx context.Context) *AttrSet {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(setKey{}).(*AttrSet)
	return s
}

// Add appends attrs to the set. Safe for concurrent use.
func (s *AttrSet) Add(attrs ...slog.Attr) {
	if s == nil || len(attrs) == 0 {
		return
	}
	s.mu.Lock()
	s.attrs = append(s.attrs, attrs...)
	s.mu.Unlock()
}

// Attrs returns a copy of the set's attributes, the outer sets' first.
func (s *AttrSet) Attrs() []slog.Attr {
	if s == nil {
		return nil
	}
	return s.appendTo(nil)
}

func (s *AttrSet) appendTo(dst []slog.Attr) []slog.Attr {
	if s.parent != nil {
		dst = s.parent.appendTo(dst)
	}
	s.mu.Lock()
	dst = append(dst, s.attrs...)
	s.mu.Unlock()
	return dst
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

// Flat is one key of a line as the line would carry it: Value is a string, int64, uint64,
// float64, bool or json.Number.
type Flat struct {
	Key   string
	Value any
}

// Flatten applies the line rules of section 2 to attrs and returns the keys in line order:
// groups and objects flatten with dots, an array becomes its JSON text, durations and times
// become strings, a fixed key becomes attr.<key>, a later key replaces an earlier one. An
// error value becomes exception.type and exception.message. work uses it so a span carries
// the same attributes as the event line.
func Flatten(attrs ...slog.Attr) []Flat {
	l := newLine()
	for _, a := range attrs {
		l.add("", a)
	}
	out := make([]Flat, 0, len(l.fields))
	for _, f := range l.fields {
		out = append(out, Flat{f.key, f.val})
	}
	return out
}
