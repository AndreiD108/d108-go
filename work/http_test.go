package work

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestHTTPRequestHandledOK(t *testing.T) {
	log, buf := newLog(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /search/{q}", func(w http.ResponseWriter, r *http.Request) {
		Add(r.Context(), "hits", 3)
		log.InfoContext(r.Context(), "searching")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("x"))
	})
	h := HTTP(log)(mux)

	req := httptest.NewRequest(http.MethodGet, "/search/abc", nil)
	req.Header.Set(CallerIDHeader, "session-7")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Errorf("status %d", rec.Code)
	}
	all := lines(t, buf)
	if len(all) != 2 {
		t.Fatalf("got %d lines: %s", len(all), buf.String())
	}
	want(t, all[0], "body", "searching")
	want(t, all[0], "caller.id", "session-7")
	run := all[1]
	want(t, run, "event_name", "request.handled")
	want(t, run, "severity_text", "INFO")
	want(t, run, "http.request.method", "GET")
	want(t, run, "http.route", "/search/{q}")
	want(t, run, "http.response.status_code", "202")
	want(t, run, "outcome", "ok")
	want(t, run, "caller.id", "session-7")
	want(t, run, "hits", "3")
	if _, ok := run["duration_ms"].(json.Number); !ok {
		t.Errorf("duration_ms = %v", run["duration_ms"])
	}
	absent(t, run, "exception.type", "error.type", "trace_id")
}

func TestHTTPPanicIs500AndError(t *testing.T) {
	log, buf := newLog(t)
	h := HTTP(log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		var m map[string]int
		m["x"] = 1
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/do", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status %d", rec.Code)
	}
	run := lastLine(t, buf)
	want(t, run, "severity_text", "ERROR")
	want(t, run, "http.response.status_code", "500")
	want(t, run, "outcome", "error")
	want(t, run, "error.type", "internal")
	want(t, run, "exception.type", "work.PanicError")
	absent(t, run, "http.route")
	if msg, _ := run["exception.message"].(string); !strings.HasPrefix(msg, "panic: assignment to entry in nil map") {
		t.Errorf("exception.message = %q", msg)
	}
	if st, _ := run["exception.stacktrace"].(string); !strings.Contains(st, "http_test.go") {
		t.Errorf("exception.stacktrace lacks the panic site: %.200s", st)
	}
}

// expectAbort runs h and returns after it panicked with http.ErrAbortHandler.
func expectAbort(t *testing.T, h http.Handler, rec *httptest.ResponseRecorder, req *http.Request) {
	t.Helper()
	defer func() {
		if v := recover(); v != http.ErrAbortHandler {
			t.Errorf("recovered %v, want http.ErrAbortHandler", v)
		}
	}()
	h.ServeHTTP(rec, req)
	t.Error("the handler returned without aborting")
}

func TestHTTPPanicAfterWriteLogsThenAborts(t *testing.T) {
	log, buf := newLog(t)
	h := HTTP(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		panic("late")
	}))
	rec := httptest.NewRecorder()
	expectAbort(t, h, rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status %d", rec.Code)
	}
	run := lastLine(t, buf)
	want(t, run, "http.response.status_code", "200")
	want(t, run, "outcome", "error")
	want(t, run, "exception.message", "panic: late")
}

func TestHTTPAbortHandlerIsLoggedThenPropagates(t *testing.T) {
	exp := withTracer(t)
	log, buf := newLog(t)
	h := HTTP(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		panic(http.ErrAbortHandler)
	}))
	expectAbort(t, h, httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	run := lastLine(t, buf)
	want(t, run, "event_name", "request.handled")
	want(t, run, "severity_text", "WARN")
	want(t, run, "outcome", "cancelled")
	absent(t, run, "exception.type")
	if spans := exp.GetSpans(); len(spans) != 1 {
		t.Errorf("got %d ended spans, want 1", len(spans))
	}
}

func TestHTTPInformationalStatusIsNotFinal(t *testing.T) {
	log, buf := newLog(t)
	h := HTTP(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusEarlyHints)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	run := lastLine(t, buf)
	want(t, run, "http.response.status_code", "500")
	want(t, run, "outcome", "error")
}

// cloningRouter matches like chi: it clones the request before the handler sees it, so the
// pattern it sets is invisible to a middleware outside it unless the handler reports it.
func cloningRouter(pattern string, inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r2 := r.WithContext(r.Context())
		r2.Pattern = pattern
		defer func() { SetRoute(r2.Context(), r2.Pattern) }()
		inner.ServeHTTP(w, r2)
	})
}

func TestHTTPRouteFromSetRouteThroughClone(t *testing.T) {
	exp := withTracer(t)
	log, buf := newLog(t)
	h := HTTP(log)(cloningRouter("/items/{id}", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/items/9", nil))
	want(t, lastLine(t, buf), "http.route", "/items/{id}")
	if name := exp.GetSpans()[0].Name; name != "GET /items/{id}" {
		t.Errorf("span name %q", name)
	}
	SetRoute(context.Background(), "/ignored")
}

func TestHTTPRouteFromSetRouteSurvivesPanic(t *testing.T) {
	log, buf := newLog(t)
	h := HTTP(log)(cloningRouter("/items/{id}", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/items/9", nil))
	run := lastLine(t, buf)
	want(t, run, "http.route", "/items/{id}")
	want(t, run, "http.response.status_code", "500")
	want(t, run, "outcome", "error")
}

func TestHTTPStatus5xxIsError(t *testing.T) {
	log, buf := newLog(t)
	h := HTTP(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	run := lastLine(t, buf)
	want(t, run, "severity_text", "ERROR")
	want(t, run, "outcome", "error")
	absent(t, run, "exception.type", "error.type")
}

func TestHTTPClientGoneIsCancelled(t *testing.T) {
	log, buf := newLog(t)
	ctx, cancel := context.WithCancel(context.Background())
	h := HTTP(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		cancel()
		w.WriteHeader(http.StatusOK)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
	run := lastLine(t, buf)
	want(t, run, "severity_text", "WARN")
	want(t, run, "outcome", "cancelled")
}

func TestHTTPNoWriteIs200(t *testing.T) {
	log, buf := newLog(t)
	h := HTTP(log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	want(t, lastLine(t, buf), "http.response.status_code", "200")
}

func TestHTTPContinuesTraceparentHeader(t *testing.T) {
	exp := withTracer(t)
	log, buf := newLog(t)
	var inside trace.SpanContext
	h := HTTP(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inside = trace.SpanContextFromContext(r.Context())
		log.InfoContext(r.Context(), "in")
	}))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	h.ServeHTTP(httptest.NewRecorder(), req)

	for _, l := range lines(t, buf) {
		want(t, l, "trace_id", "0af7651916cd43dd8448eb211c80319c")
		want(t, l, "span_id", inside.SpanID().String())
	}
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans", len(spans))
	}
	sp := spans[0]
	if sp.Name != "GET" || sp.SpanKind != trace.SpanKindServer || sp.Parent.SpanID().String() != "b7ad6b7169203331" {
		t.Errorf("span %q %v parent %v", sp.Name, sp.SpanKind, sp.Parent)
	}
}

func TestHTTPSpanNameAndAttributes(t *testing.T) {
	exp := withTracer(t)
	log, _ := newLog(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /items/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) })
	req := httptest.NewRequest(http.MethodPost, "/items/9", nil)
	req.Header.Set(CallerIDHeader, "c1")
	HTTP(log)(mux).ServeHTTP(httptest.NewRecorder(), req)

	sp := exp.GetSpans()[0]
	if sp.Name != "POST /items/{id}" {
		t.Errorf("name %q", sp.Name)
	}
	attrs := map[string]string{}
	for _, kv := range sp.Attributes {
		attrs[string(kv.Key)] = kv.Value.Emit()
	}
	for k, v := range map[string]string{"http.request.method": "POST", "http.route": "/items/{id}", "http.response.status_code": "201", "outcome": "ok", "caller.id": "c1"} {
		if attrs[k] != v {
			t.Errorf("%s = %q, want %q", k, attrs[k], v)
		}
	}
}

func TestResponseWriterKeepsFlusher(t *testing.T) {
	log, _ := newLog(t)
	flushed := false
	h := HTTP(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, ok := w.(http.Flusher); !ok {
			t.Error("not a Flusher")
		}
		http.NewResponseController(w).Flush()
		flushed = true
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !flushed || !rec.Flushed {
		t.Error("flush did not reach the recorder")
	}
}

func TestRouteOf(t *testing.T) {
	for in, out := range map[string]string{
		"GET /search":            "/search",
		"/_/api/comments/{id}":   "/_/api/comments/{id}",
		"":                       "",
		"example.com/x":          "example.com/x",
		"DELETE example.com/x/y": "example.com/x/y",
	} {
		if got := routeOf(in); got != out {
			t.Errorf("routeOf(%q) = %q, want %q", in, got, out)
		}
	}
}
