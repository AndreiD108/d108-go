package logging

import (
	"strconv"
	"strings"
	"time"
)

// longValueBytes is the length from which a value gets its own line in text rendering.
const longValueBytes = 80

// text renders the line for a terminal: local time, severity, the event name or the body,
// then key=value attributes; long values and exception.stacktrace on their own indented
// lines; service.* and process.pid omitted.
func (l *line) text(now time.Time) []byte {
	var b strings.Builder
	b.WriteString(now.In(time.Local).Format("15:04:05.000"))
	b.WriteByte(' ')
	b.WriteString(l.str("severity_text"))
	b.WriteByte(' ')
	body := l.str("body")
	if l.event != "" {
		b.WriteString(l.event)
	} else {
		b.WriteString(body)
	}
	var own []field
	if l.event != "" && body != "" {
		b.WriteString(" body=")
		b.WriteString(quote(body))
	}
	for _, f := range l.fields {
		switch f.key {
		case "timestamp", "severity_number", "severity_text", "event_name", "body",
			"service.name", "service.version", "service.instance.id", "process.pid":
			continue
		}
		s := plain(f.val)
		if f.key == "exception.stacktrace" || len(s) > longValueBytes {
			own = append(own, field{f.key, s})
			continue
		}
		b.WriteByte(' ')
		b.WriteString(f.key)
		b.WriteByte('=')
		if _, isString := f.val.(string); isString {
			s = quote(s)
		}
		b.WriteString(s)
	}
	for _, f := range own {
		b.WriteString("\n    ")
		b.WriteString(f.key)
		b.WriteByte('=')
		b.WriteString(strings.ReplaceAll(f.val.(string), "\n", "\n        "))
	}
	b.WriteByte('\n')
	return []byte(b.String())
}

func (l *line) str(key string) string {
	if i, ok := l.index[key]; ok {
		return plain(l.fields[i].val)
	}
	return ""
}

func plain(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return string(appendValue(nil, v))
}

// quote wraps a string value when it is empty or holds a space, a quote, an equals sign or a
// control character.
func quote(s string) string {
	if s == "" {
		return `""`
	}
	for _, r := range s {
		if r <= ' ' || r == '"' || r == '=' || r == 0x7f {
			return strconv.Quote(s)
		}
	}
	return s
}
