package work

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/identity"
	"github.com/AndreiD108/d108-go/logging"
)

func newLog(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_OUTPUT", "")
	var buf bytes.Buffer
	return logging.New(identity.New("worktest"), logging.Writer(&buf)), &buf
}

// lines decodes every JSON line in buf, numbers as json.Number.
func lines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, raw := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(raw) == 0 {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
		out = append(out, m)
	}
	return out
}

func lastLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	all := lines(t, buf)
	if len(all) == 0 {
		t.Fatal("nothing was written")
	}
	return all[len(all)-1]
}

func want(t *testing.T, got map[string]any, key string, value any) {
	t.Helper()
	g, ok := got[key]
	if !ok {
		t.Errorf("%s missing in %v", key, got)
		return
	}
	if n, isNum := g.(json.Number); isNum {
		g = n.String()
	}
	if s, isStr := value.(string); isStr {
		if g != s {
			t.Errorf("%s = %v, want %v", key, g, value)
		}
		return
	}
	if g != value {
		t.Errorf("%s = %v, want %v", key, g, value)
	}
}

func absent(t *testing.T, got map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if v, ok := got[k]; ok {
			t.Errorf("%s = %v, want absent", k, v)
		}
	}
}

// withTracer installs an in-memory exporter as the global provider for the test.
func withTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})
	return exp
}

func TestCommandRunOK(t *testing.T) {
	log, buf := newLog(t)
	ctx, end := Command(context.Background(), log, "index rebuild", []string{"--force", "--verbose"})
	Add(ctx, "index.size", 12)
	log.InfoContext(ctx, "working")
	end(nil)

	all := lines(t, buf)
	if len(all) != 2 {
		t.Fatalf("got %d lines, want 2: %s", len(all), buf.String())
	}
	want(t, all[0], "body", "working")
	want(t, all[0], "command", "index rebuild")
	want(t, all[0], "index.size", "12")
	absent(t, all[0], "trace_id", "span_id")

	run := all[1]
	want(t, run, "event_name", "command.run")
	want(t, run, "severity_text", "INFO")
	want(t, run, "command", "index rebuild")
	want(t, run, "args.flags", "--force --verbose")
	want(t, run, "exit_code", "0")
	want(t, run, "outcome", "ok")
	want(t, run, "index.size", "12")
	if _, ok := run["duration_ms"].(json.Number); !ok {
		t.Errorf("duration_ms = %v", run["duration_ms"])
	}
	absent(t, run, "error.type", "exception.type", "exception.message", "trace_id")
}

func TestCommandRunError(t *testing.T) {
	log, buf := newLog(t)
	_, end := Command(context.Background(), log, "sync", nil)
	end(failure.New(failure.Remote, "slack said no", "retry"))
	run := lastLine(t, buf)
	want(t, run, "severity_text", "ERROR")
	want(t, run, "args.flags", "")
	want(t, run, "exit_code", "7")
	want(t, run, "outcome", "error")
	want(t, run, "error.type", "remote")
	want(t, run, "exception.type", "failure.Error")
	want(t, run, "exception.message", "remote: slack said no")
}

func TestCommandRunUnclassifiedError(t *testing.T) {
	log, buf := newLog(t)
	_, end := Command(context.Background(), log, "sync", nil)
	end(errors.New("boom"))
	run := lastLine(t, buf)
	want(t, run, "exit_code", "1")
	want(t, run, "error.type", "internal")
	want(t, run, "exception.message", "boom")
}

func TestCommandRunCancelled(t *testing.T) {
	log, buf := newLog(t)
	_, end := Command(context.Background(), log, "tail", nil)
	end(context.Canceled)
	run := lastLine(t, buf)
	want(t, run, "severity_text", "WARN")
	want(t, run, "exit_code", "130")
	want(t, run, "outcome", "cancelled")
	want(t, run, "error.type", "cancelled")
	absent(t, run, "exception.type", "exception.message")
}

func TestCommandEndOnce(t *testing.T) {
	log, buf := newLog(t)
	_, end := Command(context.Background(), log, "x", nil)
	end(nil)
	end(errors.New("again"))
	if n := len(lines(t, buf)); n != 1 {
		t.Fatalf("got %d lines, want 1", n)
	}
}

func TestAddOutsideUnitDoesNothing(t *testing.T) {
	log, buf := newLog(t)
	ctx := context.Background()
	Add(ctx, "k", "v")
	AddAll(ctx, slog.String("k2", "v2"))
	log.InfoContext(ctx, "x")
	absent(t, lastLine(t, buf), "k", "k2")
}

func TestCommandContinuesTraceparentFromEnv(t *testing.T) {
	exp := withTracer(t)
	const parent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
	t.Setenv(EnvTraceparent, parent)
	t.Setenv(EnvTracestate, "vendor=x")
	log, buf := newLog(t)

	ctx, end := Command(context.Background(), log, "child", nil)
	log.InfoContext(ctx, "inside")
	end(nil)

	all := lines(t, buf)
	for _, l := range all {
		want(t, l, "trace_id", "0af7651916cd43dd8448eb211c80319c")
		if l["span_id"] == "b7ad6b7169203331" || l["span_id"] == "" {
			t.Errorf("span_id = %v, want a child span", l["span_id"])
		}
	}
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans", len(spans))
	}
	sp := spans[0]
	if sp.Parent.SpanID().String() != "b7ad6b7169203331" || !sp.Parent.IsRemote() {
		t.Errorf("parent = %v", sp.Parent)
	}
	if sp.SpanContext.TraceState().String() != "vendor=x" {
		t.Errorf("tracestate = %q", sp.SpanContext.TraceState().String())
	}
	if sp.SpanContext.SpanID().String() != all[1]["span_id"] {
		t.Errorf("span %s, line %v", sp.SpanContext.SpanID(), all[1]["span_id"])
	}
}

func TestCommandSpanCarriesLineAttributes(t *testing.T) {
	exp := withTracer(t)
	t.Setenv(EnvTraceparent, "")
	log, buf := newLog(t)
	outer := logging.WithAttrs(context.Background(), slog.String("inherited", "yes"))
	ctx, end := Command(outer, log, "index rebuild", []string{"--force"})
	Add(ctx, "index.size", 12)
	Add(ctx, "took", 1500*time.Millisecond)
	Add(ctx, "ids", []int{1, 2})
	Add(ctx, "cfg", struct{ Mode string }{"fast"})
	AddAll(ctx, slog.Group("db", slog.String("name", "main")), slog.Group("", slog.String("inline", "i")), slog.Bool("dry", true))
	end(failure.New(failure.Timeout, "slow", "wait"))

	run := lastLine(t, buf)
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans", len(spans))
	}
	sp := spans[0]
	if sp.Name != "index rebuild" || sp.SpanKind != trace.SpanKindInternal {
		t.Errorf("name %q kind %v", sp.Name, sp.SpanKind)
	}
	if sp.Parent.IsValid() {
		t.Errorf("want a root span, parent %v", sp.Parent)
	}
	attrs := map[string]string{}
	for _, kv := range sp.Attributes {
		attrs[string(kv.Key)] = kv.Value.Emit()
	}
	for _, key := range []string{"inherited", "command", "args.flags", "index.size", "took", "ids", "cfg.Mode", "db.name", "inline", "dry", "duration_ms", "exit_code", "outcome", "error.type"} {
		lineVal, ok := run[key]
		if !ok {
			t.Errorf("line lacks %s", key)
			continue
		}
		var lineStr string
		switch v := lineVal.(type) {
		case json.Number:
			lineStr = v.String()
		case bool:
			lineStr = "true"
		default:
			lineStr = v.(string)
		}
		if attrs[key] != lineStr {
			t.Errorf("span %s = %q, line %q", key, attrs[key], lineStr)
		}
	}
	if _, ok := attrs["exception.type"]; ok {
		t.Error("exception.type is a span attribute; the exception event carries it")
	}
	if sp.Status.Code.String() != "Error" || !strings.Contains(sp.Status.Description, "slow") {
		t.Errorf("status %v", sp.Status)
	}
	if len(sp.Events) != 1 || sp.Events[0].Name != "exception" {
		t.Errorf("events %v", sp.Events)
	}
}

func TestSpanAttrsKeepLargeUnsigned(t *testing.T) {
	got := map[string]string{}
	for _, kv := range spanAttrs([]slog.Attr{slog.Uint64("big", math.MaxUint64), slog.Uint64("small", 7), slog.Uint64("edge", math.MaxInt64)}) {
		got[string(kv.Key)] = kv.Value.Emit()
	}
	if got["big"] != "18446744073709551615" || got["small"] != "7" || got["edge"] != "9223372036854775807" {
		t.Errorf("span values %v", got)
	}
}

func TestOutcomeOf(t *testing.T) {
	for _, tc := range []struct {
		err     error
		outcome string
		level   slog.Level
	}{
		{nil, OutcomeOK, slog.LevelInfo},
		{context.Canceled, OutcomeCancelled, slog.LevelWarn},
		{failure.New(failure.Terminated, "stopped", "rerun"), OutcomeCancelled, slog.LevelWarn},
		{context.DeadlineExceeded, OutcomeError, slog.LevelError},
		{errors.New("x"), OutcomeError, slog.LevelError},
	} {
		o, l := outcomeOf(tc.err)
		if o != tc.outcome || l != tc.level {
			t.Errorf("%v: %s %v, want %s %v", tc.err, o, l, tc.outcome, tc.level)
		}
	}
}
