// Package logging writes the JSON line of standard.md section 2 through log/slog: output
// selection by environment, level, dated files with retention and a size cap, the 64 KiB
// line limit, text rendering on stderr, and per-work attributes read from the context.
//
//	id := identity.New("slack-sync")
//	log := logging.New(id, logging.KeepDays(14), logging.MaxFileBytes(100<<20))
//	log.InfoContext(ctx, "listening", "addr", ":8080")
//	log.LogAttrs(ctx, slog.LevelInfo, "", logging.Event("job.progress"), slog.String("job.name", "reindex"))
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/AndreiD108/d108-go/identity"
)

// Option changes how New builds the logger.
type Option func(*options)

type options struct {
	keepDays     int
	maxFileBytes int64
	output       string
	dir          string
	now          func() time.Time
	writer       io.Writer
	level        *slog.Level
}

// KeepDays sets how many days of files the logger keeps (default 14); 0 keeps every file.
func KeepDays(n int) Option { return func(o *options) { o.keepDays = n } }

// MaxFileBytes sets the size cap of a day's file (default 100 MB); 0 removes the cap.
func MaxFileBytes(n int64) Option { return func(o *options) { o.maxFileBytes = n } }

// Output forces the output (OutputStdout, OutputFile or OutputStderr) over the environment.
func Output(name string) Option { return func(o *options) { o.output = name } }

// Dir sets the log folder instead of the one paths resolves.
func Dir(path string) Option { return func(o *options) { o.dir = path } }

// Now sets the clock; timestamps, file dates and retention follow it.
func Now(now func() time.Time) Option { return func(o *options) { o.now = now } }

// Writer sends the JSON lines to w and nowhere else; for tests.
func Writer(w io.Writer) Option { return func(o *options) { o.writer = w } }

// Level forces the minimum level over the environment; for tests.
func Level(l slog.Level) Option { return func(o *options) { o.level = &l } }

// handler is the slog.Handler. Clones made by WithAttrs and WithGroup share the sink.
type handler struct {
	id    identity.Identity
	level slog.Level
	// settings is what the logger does, for SettingsOf: the output after options and the
	// fallback, the level, the folder.
	settings Settings
	now      func() time.Time
	sink     sink
	text     bool
	prefix   string
	pre      []prefixed
}

// prefixed is an attribute given to WithAttrs with the group prefix open at that time.
type prefixed struct {
	prefix string
	attr   slog.Attr
}

// New builds the logger for id. It never fails: when the log file cannot be opened the
// lines go to stderr as text, and the first line says why (log.fallback); a bad LOG_LEVEL or
// LOG_OUTPUT is ignored the same way. Check reports those as errors for a start-up refusal.
func New(id identity.Identity, opts ...Option) *slog.Logger {
	o := options{keepDays: 14, maxFileBytes: 100 << 20}
	for _, opt := range opts {
		opt(&o)
	}
	settings := Resolve(id)
	if o.output != "" {
		settings.Output, settings.OutputSource = o.output, "logging.Output option"
	}
	if o.dir != "" {
		settings.Dir = o.dir
	}
	if o.level != nil {
		settings.Level, settings.LevelSource = *o.level, "logging.Level option"
	}
	h := &handler{id: id, level: settings.Level, now: o.now}
	var fallback []slog.Attr
	switch {
	case o.writer != nil:
		h.sink = writerSink{o.writer}
		settings.Output, settings.OutputSource = "writer", "logging.Writer option"
	case settings.Output == OutputStderr:
		h.sink, h.text = writerSink{os.Stderr}, true
	case settings.Output == OutputStdout:
		h.sink = writerSink{os.Stdout}
	default:
		fs, err := openFileSink(settings.Dir, id.Name, o.maxFileBytes, o.keepDays, h.clock, h.eventLine)
		if err != nil {
			h.sink, h.text = writerSink{os.Stderr}, true
			fallback = []slog.Attr{slog.String("reason", "cannot open the log file; writing to stderr"), slog.Any("error", err)}
			settings.Output, settings.OutputSource = OutputStderr, "fallback: cannot open the log file: "+err.Error()
		} else {
			h.sink = fs
		}
	}
	h.settings = settings
	log := slog.New(h)
	if fallback != nil {
		log.LogAttrs(context.Background(), slog.LevelWarn, "", append([]slog.Attr{Event("log.fallback")}, fallback...)...)
	}
	if settings.Err != nil {
		log.LogAttrs(context.Background(), slog.LevelWarn, "",
			Event("log.fallback"), slog.String("reason", "ignored a bad setting"), slog.Any("error", settings.Err))
	}
	return log
}

// SettingsOf returns what a logger made by New does: the output it writes to and why, the
// level and its source, the folder. ok is false for a logger of another handler.
func SettingsOf(log *slog.Logger) (s Settings, ok bool) {
	h, ok := log.Handler().(*handler)
	if !ok {
		return Settings{}, false
	}
	return h.settings, true
}

func (h *handler) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

// eventLine makes a line for an event the sink writes about itself (log.capped, log.dropped).
func (h *handler) eventLine(level slog.Level, event string, attrs ...slog.Attr) []byte {
	r := slog.NewRecord(h.clock(), level, "", 0)
	r.AddAttrs(append([]slog.Attr{Event(event)}, attrs...)...)
	root := &handler{id: h.id, level: h.level, now: h.now, sink: h.sink, text: h.text}
	b, _ := root.render(context.Background(), r)
	return b
}

// Enabled reports whether level is at or above the configured minimum.
func (h *handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

// Handle writes one line.
func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	b, m := h.render(ctx, r)
	h.sink.write(b, m)
	return nil
}

func (h *handler) render(ctx context.Context, r slog.Record) ([]byte, meta) {
	now := r.Time
	if h.now != nil || now.IsZero() {
		now = h.clock()
	}
	l := h.build(ctx, r, now)
	b, _ := l.truncate()
	if h.text {
		b = l.text(now)
	}
	return b, meta{level: r.Level, event: l.event}
}

// WithAttrs returns a handler whose lines all carry attrs, under the current group.
func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.pre = make([]prefixed, 0, len(h.pre)+len(attrs))
	c.pre = append(c.pre, h.pre...)
	for _, a := range attrs {
		c.pre = append(c.pre, prefixed{h.prefix, a})
	}
	return &c
}

// WithGroup returns a handler whose later attributes are prefixed with name and a dot.
func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	c := *h
	c.prefix = h.prefix + name + "."
	return &c
}
