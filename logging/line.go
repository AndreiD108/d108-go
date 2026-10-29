package logging

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/otel/trace"
)

// MaxKeyBytes is the longest key written; longer keys are dropped (standard.md section 2.3).
const MaxKeyBytes = 128

// LevelFatal is the level written as FATAL (severity_number 21).
const LevelFatal = slog.LevelError + 4

// fixedKeys are the keys of standard.md section 2.1. A call-site attribute with one of
// these keys is written as attr.<key>.
var fixedKeys = map[string]bool{
	"timestamp": true, "severity_number": true, "severity_text": true, "event_name": true,
	"body": true, "service.name": true, "service.version": true, "service.instance.id": true,
	"process.pid": true, "trace_id": true, "span_id": true,
}

// eventName is the value type logging.Event makes. The handler recognises it by type, so a
// plain string under the key event_name stays a call-site attribute.
type eventName string

// Event marks a record as the event name: log.LogAttrs(ctx, slog.LevelInfo, "",
// logging.Event("job.progress"), ...). The key event_name is fixed; this is the one way to
// set it.
func Event(name string) slog.Attr {
	return slog.Any("event_name", eventName(name))
}

// stackTracer is the interface an error implements to give exception.stacktrace.
type stackTracer interface {
	StackTrace() string
}

// field is one key of the line. val is a string, int64, uint64, float64, bool or
// json.Number.
type field struct {
	key string
	val any
}

// line is the ordered set of keys of one log line, timestamp first.
type line struct {
	fields []field
	index  map[string]int
	event  string
}

func newLine() *line {
	return &line{fields: make([]field, 0, 24), index: make(map[string]int, 24)}
}

// set adds or replaces a key; a replaced key keeps its position and takes the new value.
func (l *line) set(key string, val any) {
	if i, ok := l.index[key]; ok {
		l.fields[i].val = val
		return
	}
	l.index[key] = len(l.fields)
	l.fields = append(l.fields, field{key, val})
}

// unsetLast removes key when it is the last field added.
func (l *line) unsetLast(key string) {
	if i, ok := l.index[key]; ok && i == len(l.fields)-1 {
		l.fields = l.fields[:i]
		delete(l.index, key)
	}
}

func severity(level slog.Level) (int64, string) {
	switch {
	case level < slog.LevelInfo:
		return 5, "DEBUG"
	case level < slog.LevelWarn:
		return 9, "INFO"
	case level < slog.LevelError:
		return 13, "WARN"
	case level < LevelFatal:
		return 17, "ERROR"
	default:
		return 21, "FATAL"
	}
}

// build makes the line for one record: the fixed keys, then the handler's attributes, the
// context's, and the record's, in that order, so a later key with the same name wins.
func (h *handler) build(ctx context.Context, r slog.Record, now time.Time) *line {
	l := newLine()
	num, text := severity(r.Level)
	l.set("timestamp", now.UTC().Format("2006-01-02T15:04:05.000000Z07:00"))
	l.set("severity_number", num)
	l.set("severity_text", text)
	l.set("body", r.Message)
	l.set("service.name", h.id.Name)
	l.set("service.version", h.id.Version)
	l.set("service.instance.id", h.id.InstanceID)
	l.set("process.pid", int64(h.id.PID))
	if ctx != nil {
		if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
			l.set("trace_id", sc.TraceID().String())
			l.set("span_id", sc.SpanID().String())
		}
	}
	for _, p := range h.pre {
		l.add(p.prefix, p.attr)
	}
	for _, a := range Attrs(ctx) {
		l.add("", a)
	}
	r.Attrs(func(a slog.Attr) bool {
		l.add(h.prefix, a)
		return true
	})
	if l.event != "" {
		l.setEvent(l.event)
	}
	return l
}

// setEvent puts event_name right after severity_text, where readers expect it.
func (l *line) setEvent(name string) {
	if _, ok := l.index["event_name"]; ok {
		return
	}
	const at = 3
	l.fields = append(l.fields, field{})
	copy(l.fields[at+1:], l.fields[at:])
	l.fields[at] = field{"event_name", name}
	for k, i := range l.index {
		if i >= at {
			l.index[k] = i + 1
		}
	}
	l.index["event_name"] = at
}

// add flattens one attribute into the line under prefix.
func (l *line) add(prefix string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	switch a.Value.Kind() {
	case slog.KindGroup:
		attrs := a.Value.Group()
		if len(attrs) == 0 {
			return
		}
		sub := prefix
		if a.Key != "" {
			sub = prefix + a.Key + "."
		}
		for _, ga := range attrs {
			l.add(sub, ga)
		}
		return
	case slog.KindAny:
		switch v := a.Value.Any().(type) {
		case eventName:
			l.event = string(v)
			return
		case error:
			l.addError(v)
			return
		case nil:
			return
		}
	}
	if a.Key == "" {
		return
	}
	key := prefix + a.Key
	switch a.Value.Kind() {
	case slog.KindString:
		l.put(key, a.Value.String())
	case slog.KindInt64:
		l.put(key, a.Value.Int64())
	case slog.KindUint64:
		l.put(key, a.Value.Uint64())
	case slog.KindFloat64:
		l.put(key, a.Value.Float64())
	case slog.KindBool:
		l.put(key, a.Value.Bool())
	case slog.KindDuration:
		l.put(key, a.Value.Duration().String())
	case slog.KindTime:
		l.put(key, a.Value.Time().UTC().Format(time.RFC3339Nano))
	default:
		l.addAny(key, a.Value.Any())
	}
}

// addError writes an error as exception.type, exception.message and, when the error carries
// one, exception.stacktrace. The attribute's own key is not used.
func (l *line) addError(err error) {
	l.put("exception.type", strings.TrimPrefix(reflect.TypeOf(err).String(), "*"))
	l.put("exception.message", err.Error())
	if st, ok := err.(stackTracer); ok {
		if s := st.StackTrace(); s != "" {
			l.put("exception.stacktrace", s)
		}
	}
}

// addAny converts any other Go value: an object flattens with dots, an array becomes its
// JSON text, a scalar stays a scalar, null is left out (standard.md section 2.2).
func (l *line) addAny(key string, v any) {
	l.addPlain(key, plainValue(reflect.ValueOf(v), 0))
}

// addPlain flattens the output of plainValue into the line.
func (l *line) addPlain(key string, v any) {
	switch t := v.(type) {
	case nil:
		return
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			l.addPlain(key+"."+k, t[k])
		}
	case []any:
		b, err := json.Marshal(t)
		if err != nil {
			l.put(key, "!"+err.Error())
			return
		}
		l.put(key, string(b))
	default:
		l.put(key, t)
	}
}

// put stores one scalar, applying the key rules: too long is dropped, a fixed key becomes
// attr.<key>, a float that JSON cannot carry becomes a string.
func (l *line) put(key string, val any) {
	if len(key) > MaxKeyBytes {
		return
	}
	if fixedKeys[key] {
		key = "attr." + key
	}
	if f, ok := val.(float64); ok {
		switch {
		case math.IsNaN(f):
			val = "NaN"
		case math.IsInf(f, 1):
			val = "+Inf"
		case math.IsInf(f, -1):
			val = "-Inf"
		}
	}
	l.set(key, val)
}

// json serializes the line as one JSON object and a newline.
func (l *line) json() []byte {
	buf := make([]byte, 0, 512)
	buf = append(buf, '{')
	for i, f := range l.fields {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = appendString(buf, f.key)
		buf = append(buf, ':')
		buf = appendValue(buf, f.val)
	}
	buf = append(buf, '}', '\n')
	return buf
}

func appendValue(buf []byte, val any) []byte {
	switch v := val.(type) {
	case string:
		return appendString(buf, v)
	case int64:
		return strconv.AppendInt(buf, v, 10)
	case uint64:
		return strconv.AppendUint(buf, v, 10)
	case float64:
		return appendFloat(buf, v)
	case bool:
		return strconv.AppendBool(buf, v)
	case json.Number:
		return append(buf, v.String()...)
	}
	return append(buf, "null"...)
}

// appendFloat follows encoding/json: shortest form, exponent only for very large or small.
func appendFloat(buf []byte, f float64) []byte {
	abs := math.Abs(f)
	format := byte('f')
	if abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		format = 'e'
	}
	buf = strconv.AppendFloat(buf, f, format, -1, 64)
	if format == 'e' {
		n := len(buf)
		if n >= 4 && buf[n-4] == 'e' && buf[n-3] == '-' && buf[n-2] == '0' {
			buf[n-2] = buf[n-1]
			buf = buf[:n-1]
		}
	}
	return buf
}

const hex = "0123456789abcdef"

// appendString writes s as a JSON string. Only the quote, the backslash and control
// characters are escaped; UTF-8 is written as is; invalid bytes become U+FFFD.
func appendString(buf []byte, s string) []byte {
	buf = append(buf, '"')
	start := 0
	for i := 0; i < len(s); {
		b := s[i]
		if b < utf8.RuneSelf {
			if b >= 0x20 && b != '"' && b != '\\' {
				i++
				continue
			}
			buf = append(buf, s[start:i]...)
			switch b {
			case '"', '\\':
				buf = append(buf, '\\', b)
			case '\n':
				buf = append(buf, '\\', 'n')
			case '\r':
				buf = append(buf, '\\', 'r')
			case '\t':
				buf = append(buf, '\\', 't')
			default:
				buf = append(buf, '\\', 'u', '0', '0', hex[b>>4], hex[b&0xF])
			}
			i++
			start = i
			continue
		}
		c, size := utf8.DecodeRuneInString(s[i:])
		if c == utf8.RuneError && size == 1 {
			buf = append(buf, s[start:i]...)
			buf = append(buf, "�"...)
			i++
			start = i
			continue
		}
		i += size
	}
	buf = append(buf, s[start:]...)
	return append(buf, '"')
}
