package work

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/logging"
)

func TestRequestHandledOK(t *testing.T) {
	log, buf := newLog(t)
	ctx, end := Request(context.Background(), log, "get-plot", Caller("sess-1"))
	Add(ctx, "plottingroom.code", "not_found")
	log.InfoContext(ctx, "looking")
	end(nil)

	all := lines(t, buf)
	if len(all) != 2 {
		t.Fatalf("got %d lines, want 2: %s", len(all), buf.String())
	}
	want(t, all[0], "body", "looking")
	want(t, all[0], "rpc.method", "get-plot")
	want(t, all[0], "caller.id", "sess-1")
	absent(t, all[0], "trace_id", "span_id")

	run := all[1]
	want(t, run, "event_name", "request.handled")
	want(t, run, "severity_text", "INFO")
	want(t, run, "rpc.method", "get-plot")
	want(t, run, "caller.id", "sess-1")
	want(t, run, "outcome", "ok")
	want(t, run, "plottingroom.code", "not_found")
	if _, ok := run["duration_ms"].(json.Number); !ok {
		t.Errorf("duration_ms = %v", run["duration_ms"])
	}
	absent(t, run, "exit_code", "error.type", "exception.type", "exception.message", "http.request.method")
}

func TestRequestHandledError(t *testing.T) {
	log, buf := newLog(t)
	_, end := Request(context.Background(), log, "render")
	end(failure.New(failure.Remote, "renderer said no", "retry"))
	run := lastLine(t, buf)
	want(t, run, "severity_text", "ERROR")
	want(t, run, "outcome", "error")
	want(t, run, "error.type", "remote")
	want(t, run, "exception.type", "failure.Error")
	want(t, run, "exception.message", "remote: renderer said no")
	absent(t, run, "caller.id", "exit_code")

	_, end = Request(context.Background(), log, "render")
	end(errors.New("boom"))
	run = lastLine(t, buf)
	want(t, run, "error.type", "internal")
	want(t, run, "exception.message", "boom")
}

func TestRequestHandledCancelled(t *testing.T) {
	log, buf := newLog(t)
	_, end := Request(context.Background(), log, "stream")
	end(failure.Wrap(context.Canceled, failure.Cancelled, "stream cut by the stop", "rerun"))
	run := lastLine(t, buf)
	want(t, run, "severity_text", "WARN")
	want(t, run, "outcome", "cancelled")
	want(t, run, "error.type", "cancelled")
	absent(t, run, "exception.type", "exception.message")
}

func TestRequestEndOnceAndNilLog(t *testing.T) {
	log, buf := newLog(t)
	ctx := logging.NewContext(context.Background(), log)
	_, end := Request(ctx, nil, "x")
	end(nil)
	end(errors.New("again"))
	if n := len(lines(t, buf)); n != 1 {
		t.Fatalf("got %d lines, want 1", n)
	}
}

func TestRequestContinuesTraceparent(t *testing.T) {
	exp := withTracer(t)
	log, buf := newLog(t)
	const parent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
	ctx, end := Request(context.Background(), log, "get-plot", Traceparent(parent))
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
	if sp.Name != "get-plot" || sp.SpanKind != trace.SpanKindServer {
		t.Errorf("name %q kind %v", sp.Name, sp.SpanKind)
	}
	if sp.Parent.SpanID().String() != "b7ad6b7169203331" || !sp.Parent.IsRemote() {
		t.Errorf("parent = %v", sp.Parent)
	}
	if sp.SpanContext.SpanID().String() != all[1]["span_id"] {
		t.Errorf("span %s, line %v", sp.SpanContext.SpanID(), all[1]["span_id"])
	}
}

// A request without a usable traceparent is a root even inside the server's own span: the
// server's ctx keeps its values but does not parent the request.
func TestRequestBadTraceparentIsRoot(t *testing.T) {
	exp := withTracer(t)
	log, buf := newLog(t)
	type key struct{}
	serverCtx, serverSpan := otel.Tracer(tracerName).Start(context.WithValue(context.Background(), key{}, "kept"), "server")
	defer serverSpan.End()
	for _, tp := range []string{"", "garbage", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331"} {
		ctx, end := Request(serverCtx, log, "get-plot", Traceparent(tp), Caller("c"))
		if ctx.Value(key{}) != "kept" {
			t.Error("the request ctx lost the server ctx's values")
		}
		Add(ctx, "k", 1)
		end(failure.New(failure.Timeout, "slow", "wait"))
	}
	spans := exp.GetSpans()
	if len(spans) != 3 {
		t.Fatalf("got %d spans", len(spans))
	}
	for _, sp := range spans {
		if sp.Parent.IsValid() || sp.SpanContext.TraceID() == serverSpan.SpanContext().TraceID() {
			t.Errorf("want a root span, parent %v", sp.Parent)
		}
		attrs := map[string]string{}
		for _, kv := range sp.Attributes {
			attrs[string(kv.Key)] = kv.Value.Emit()
		}
		if attrs["rpc.method"] != "get-plot" || attrs["caller.id"] != "c" || attrs["k"] != "1" || attrs["outcome"] != "error" || attrs["error.type"] != "timeout" || attrs["duration_ms"] == "" {
			t.Errorf("span attrs %v", attrs)
		}
		if _, ok := attrs["exception.type"]; ok {
			t.Error("exception.type is a span attribute; the exception event carries it")
		}
		if sp.Status.Code.String() != "Error" || len(sp.Events) != 1 || sp.Events[0].Name != "exception" {
			t.Errorf("status %v events %v", sp.Status, sp.Events)
		}
	}
	run := lastLine(t, buf)
	want(t, run, "trace_id", spans[2].SpanContext.TraceID().String())
	absent(t, run, "traceparent")
}
