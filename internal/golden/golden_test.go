// Package golden runs every case of the contract's corpus through logging and compares the
// written line with the expected object.
package golden

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AndreiD108/d108/logcheck"
	"go.opentelemetry.io/otel/trace"

	"github.com/AndreiD108/d108-go/identity"
	"github.com/AndreiD108/d108-go/logging"
)

type corpus struct {
	Context struct {
		Name       string `json:"service.name"`
		Version    string `json:"service.version"`
		InstanceID string `json:"service.instance.id"`
		Now        string `json:"now"`
		MinLevel   string `json:"min_level"`
		PID        int    `json:"process.pid"`
	} `json:"context"`
	Cases []struct {
		Name     string          `json:"name"`
		Call     call            `json:"call"`
		Expected json.RawMessage `json:"expected"`
		Checks   []string        `json:"checks"`
	} `json:"cases"`
}

type call struct {
	Level string            `json:"level"`
	Msg   string            `json:"msg"`
	Event *string           `json:"event"`
	Attrs []json.RawMessage `json:"attrs"`
	Error *errorSpec        `json:"error"`
	Span  *struct {
		TraceID string `json:"trace_id"`
		SpanID  string `json:"span_id"`
	} `json:"span"`
	Run map[string]any `json:"run"`
}

type errorSpec struct {
	Type    string  `json:"type"`
	Message string  `json:"message"`
	Stack   *string `json:"stack"`
}

func load(t *testing.T) corpus {
	t.Helper()
	raw, err := os.ReadFile("cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var c corpus
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCorpus(t *testing.T) {
	c := load(t)
	t.Setenv("LOG_LEVEL", c.Context.MinLevel)
	t.Setenv("GOLDEN_LOG_LEVEL", "")
	now, err := time.Parse(time.RFC3339Nano, c.Context.Now)
	if err != nil {
		t.Fatal(err)
	}
	id := identity.Identity{Name: c.Context.Name, Version: c.Context.Version, InstanceID: c.Context.InstanceID, PID: c.Context.PID}
	var buf bytes.Buffer
	log := logging.New(id, logging.Writer(&buf), logging.Now(func() time.Time { return now }))

	if len(c.Cases) != 16 {
		t.Fatalf("corpus has %d cases, want 16", len(c.Cases))
	}
	for _, tc := range c.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			buf.Reset()
			ctx := context.Background()
			if tc.Call.Run != nil {
				runCase(ctx, log, tc.Call.Run)
			} else {
				ctx = spanContext(t, ctx, tc.Call)
				level := parseLevel(t, tc.Call.Level)
				attrs := attrsOf(t, tc.Call.Attrs)
				if tc.Call.Event != nil {
					attrs = append([]slog.Attr{logging.Event(*tc.Call.Event)}, attrs...)
				}
				if tc.Call.Error != nil {
					attrs = append(attrs, slog.Any("error", errorOf(t, tc.Call.Error)))
				}
				log.LogAttrs(ctx, level, tc.Call.Msg, attrs...)
			}
			line := buf.Bytes()

			if string(tc.Expected) == "null" {
				if len(line) != 0 {
					t.Fatalf("expected nothing, got %s", line)
				}
				return
			}
			if len(line) == 0 {
				t.Fatal("nothing was written")
			}
			if problems := logcheck.Check(line); problems != nil {
				t.Errorf("logcheck: %+v", problems)
			}
			if !bytes.HasPrefix(line, []byte(`{"timestamp":`)) {
				t.Errorf("timestamp is not the first key: %.60s", line)
			}
			if bytes.Count(line, []byte("\n")) != 1 || line[len(line)-1] != '\n' {
				t.Errorf("the output must be one line ending in a newline: %q", line)
			}
			got := decode(t, line)
			if string(tc.Expected) == `"see checks"` {
				checkOversized(t, line, got, tc.Call)
				return
			}
			want := decode(t, tc.Expected)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("line differs\n got: %v\nwant: %v", got, want)
			}
		})
	}
}

func decode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return m
}

func parseLevel(t *testing.T, s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	case "fatal":
		return logging.LevelFatal
	}
	t.Fatalf("unknown level %q", s)
	return 0
}

func spanContext(t *testing.T, ctx context.Context, c call) context.Context {
	if c.Span == nil {
		return ctx
	}
	traceID, err := trace.TraceIDFromHex(c.Span.TraceID)
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex(c.Span.SpanID)
	if err != nil {
		t.Fatal(err)
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled})
	return trace.ContextWithSpanContext(ctx, sc)
}

// attrsOf turns the corpus's [key, value] pairs into slog attributes: an object is a group,
// {"repeat", "count"} a repeated string, an array and null are passed as Any.
func attrsOf(t *testing.T, pairs []json.RawMessage) []slog.Attr {
	var attrs []slog.Attr
	for _, p := range pairs {
		var pair []json.RawMessage
		if err := json.Unmarshal(p, &pair); err != nil || len(pair) != 2 {
			t.Fatalf("bad attr pair %s", p)
		}
		var key string
		if err := json.Unmarshal(pair[0], &key); err != nil {
			t.Fatal(err)
		}
		attrs = append(attrs, attrOf(t, key, pair[1]))
	}
	return attrs
}

func attrOf(t *testing.T, key string, raw json.RawMessage) slog.Attr {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return attrValue(t, key, v)
}

func attrValue(t *testing.T, key string, v any) slog.Attr {
	switch x := v.(type) {
	case map[string]any:
		if s, ok := x["repeat"].(string); ok && len(x) == 2 {
			n, _ := x["count"].(json.Number).Int64()
			return slog.String(key, strings.Repeat(s, int(n)))
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		// The corpus lists group members in a fixed order; a map loses it, so sort by the
		// order they appear in the expected flattening (size before ok).
		sortGroupKeys(keys)
		members := make([]any, 0, len(keys))
		for _, k := range keys {
			members = append(members, attrValue(t, k, x[k]))
		}
		return slog.Group(key, members...)
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return slog.Int64(key, i)
		}
		f, _ := x.Float64()
		return slog.Float64(key, f)
	case string:
		return slog.String(key, x)
	case bool:
		return slog.Bool(key, x)
	default:
		return slog.Any(key, v)
	}
}

func sortGroupKeys(keys []string) {
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
}

// errorOf makes a Go error whose type name and message match the corpus.
func errorOf(t *testing.T, spec *errorSpec) error {
	var err error
	switch spec.Type {
	case "net.OpError":
		err = &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("i/o timeout")}
	default:
		t.Fatalf("the corpus uses an error type this test does not make: %q", spec.Type)
	}
	if err.Error() != spec.Message {
		t.Fatalf("error message %q, corpus wants %q", err.Error(), spec.Message)
	}
	return err
}

// runCase writes the wide event the way the work package ends a unit of work.
func runCase(ctx context.Context, log *slog.Logger, run map[string]any) {
	var attrs []slog.Attr
	outcome := "ok"
	var errSpec *errorSpec
	if e, ok := run["error"].(map[string]any); ok {
		outcome = "error"
		errSpec = &errorSpec{Type: e["type"].(string), Message: e["message"].(string)}
	}
	switch run["kind"] {
	case "command":
		flags := run["flags"].([]any)
		parts := make([]string, len(flags))
		for i, f := range flags {
			parts[i] = f.(string)
		}
		attrs = append(attrs, logging.Event("command.run"),
			slog.String("command", run["command"].(string)),
			slog.String("args.flags", strings.Join(parts, " ")),
			number("duration_ms", run["duration_ms"]),
			number("exit_code", run["exit_code"]),
			slog.String("outcome", outcome))
	case "request":
		attrs = append(attrs, logging.Event("request.handled"),
			slog.String("http.request.method", run["http.request.method"].(string)),
			slog.String("http.route", run["http.route"].(string)),
			number("http.response.status_code", run["http.response.status_code"]),
			number("duration_ms", run["duration_ms"]),
			slog.String("outcome", outcome),
			slog.String("caller.id", run["caller.id"].(string)))
	}
	level := slog.LevelInfo
	if errSpec != nil {
		level = slog.LevelError
		attrs = append(attrs, slog.String("error.type", errSpec.Type),
			slog.Any("error", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("i/o timeout")}))
	}
	log.LogAttrs(ctx, level, "", attrs...)
}

func number(key string, v any) slog.Attr {
	i, _ := v.(json.Number).Int64()
	return slog.Int64(key, i)
}

func checkOversized(t *testing.T, line []byte, got map[string]any, c call) {
	if len(line) > 64<<10 {
		t.Errorf("line is %d bytes, over 65536", len(line))
	}
	if got["small"] != "keep" {
		t.Errorf("small = %v", got["small"])
	}
	if got["log.truncated"] != true {
		t.Errorf("log.truncated = %v", got["log.truncated"])
	}
	for _, k := range []string{"timestamp", "severity_number", "severity_text", "body", "service.name", "service.version", "service.instance.id", "process.pid"} {
		if _, ok := got[k]; !ok {
			t.Errorf("fixed key %s missing", k)
		}
	}
	if got["body"] != c.Msg {
		t.Errorf("body = %v", got["body"])
	}
	payload, _ := got["payload"].(string)
	input := strings.Repeat("a", 100000)
	head, rest, ok := strings.Cut(payload, "…[")
	if !ok {
		t.Fatalf("payload has no sentinel: %.80s", payload)
	}
	count, tail, ok := strings.Cut(rest, " bytes cut]…")
	if !ok {
		t.Fatalf("payload sentinel is malformed: %.80s", rest)
	}
	if !strings.HasPrefix(input, head) || !strings.HasSuffix(input, tail) {
		t.Error("head or tail is not a prefix or suffix of the input")
	}
	var n int
	if _, err := json.Number(count).Int64(); err != nil {
		t.Errorf("count %q is not a number", count)
	} else {
		v, _ := json.Number(count).Int64()
		n = int(v)
	}
	if n != len(input)-len(head)-len(tail) {
		t.Errorf("N = %d, want %d", n, len(input)-len(head)-len(tail))
	}
	if len(payload) < 256 {
		t.Errorf("payload kept %d bytes, want at least 256", len(payload))
	}
}

// TestCopiesMatchContract fails when the copies here differ from the contract repo next to
// this one; `make sync-contract` refreshes them. It is skipped when that repo is absent.
func TestCopiesMatchContract(t *testing.T) {
	for _, pair := range [][2]string{
		{"cases.json", "../../../d108/golden/cases.json"},
		{"log-line.schema.json", "../../../d108/schema/log-line.schema.json"},
	} {
		source, err := os.ReadFile(pair[1])
		if err != nil {
			t.Skipf("contract repo not found: %v", err)
		}
		copy, err := os.ReadFile(pair[0])
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(source, copy) {
			t.Errorf("%s differs from %s; run make sync-contract", pair[0], pair[1])
		}
	}
}
