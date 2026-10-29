package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AndreiD108/d108-go/identity"
	"github.com/AndreiD108/d108-go/logging"
)

func newLog(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	t.Setenv("LOG_LEVEL", "")
	var buf bytes.Buffer
	return logging.New(identity.New("srvtest"), logging.Writer(&buf)), &buf
}

func lines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, raw := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(raw) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
		out = append(out, m)
	}
	return out
}

func get(t *testing.T, h http.Handler, method, path string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealthzOutsideTheMiddleware(t *testing.T) {
	log, buf := newLog(t)
	srv := New(log, http.NotFoundHandler())
	rec := get(t, srv.Handler, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok\n" {
		t.Errorf("%d %q", rec.Code, rec.Body.String())
	}
	if n := len(lines(t, buf)); n != 0 {
		t.Errorf("/healthz wrote %d lines", n)
	}
	if rec := get(t, srv.Handler, http.MethodPost, "/healthz", nil); rec.Code != http.StatusNotFound {
		t.Errorf("POST /healthz = %d, want the app's 404", rec.Code)
	}
}

func TestPanicIs500WithErrorLine(t *testing.T) {
	log, buf := newLog(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic("kaboom") })
	srv := New(log, mux)
	rec := get(t, srv.Handler, http.MethodGet, "/boom", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status %d", rec.Code)
	}
	all := lines(t, buf)
	if len(all) != 1 {
		t.Fatalf("got %d lines", len(all))
	}
	l := all[0]
	if l["event_name"] != "request.handled" || l["severity_text"] != "ERROR" || l["outcome"] != "error" ||
		l["http.route"] != "/boom" || l["exception.type"] != "work.PanicError" || l["exception.message"] != "panic: kaboom" {
		t.Errorf("line: %v", l)
	}
	if _, ok := l["exception.stacktrace"]; !ok {
		t.Error("no exception.stacktrace")
	}
}

func TestPlainHandlerHasNoRoute(t *testing.T) {
	log, buf := newLog(t)
	srv := New(log, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	get(t, srv.Handler, http.MethodGet, "/anything", nil)
	l := lines(t, buf)[0]
	if _, ok := l["http.route"]; ok {
		t.Errorf("http.route = %v for a handler without patterns", l["http.route"])
	}
}

func TestCORS(t *testing.T) {
	log, _ := newLog(t)
	app := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	srv := New(log, app, CORS("https://app.example"))

	rec := get(t, srv.Handler, http.MethodGet, "/x", http.Header{"Origin": {"https://app.example"}})
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example" || rec.Header().Get("Vary") != "Origin" {
		t.Errorf("allowed origin headers: %v", rec.Header())
	}
	rec = get(t, srv.Handler, http.MethodGet, "/x", http.Header{"Origin": {"https://evil.example"}})
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("other origin allowed: %v", rec.Header())
	}
	rec = get(t, srv.Handler, http.MethodOptions, "/x", http.Header{
		"Origin":                         {"https://app.example"},
		"Access-Control-Request-Method":  {"POST"},
		"Access-Control-Request-Headers": {"Content-Type, Caller-Id"},
	})
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Headers") != "Content-Type, Caller-Id" ||
		!strings.Contains(rec.Header().Get("Access-Control-Allow-Methods"), "POST") {
		t.Errorf("preflight: %d %v", rec.Code, rec.Header())
	}
	star := New(log, app, CORS("*"))
	rec = get(t, star.Handler, http.MethodGet, "/x", http.Header{"Origin": {"https://any.example"}})
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://any.example" {
		t.Errorf("star: %v", rec.Header())
	}
}

func TestPprofOnlyWhenAsked(t *testing.T) {
	log, buf := newLog(t)
	off := New(log, http.NotFoundHandler())
	if rec := get(t, off.Handler, http.MethodGet, "/debug/pprof/", nil); rec.Code != http.StatusNotFound {
		t.Errorf("pprof served without the option: %d", rec.Code)
	}
	on := New(log, http.NotFoundHandler(), Pprof())
	if rec := get(t, on.Handler, http.MethodGet, "/debug/pprof/", nil); rec.Code != http.StatusOK {
		t.Errorf("pprof index: %d", rec.Code)
	}
	if n := len(lines(t, buf)); n != 1 {
		t.Errorf("want 1 line (the 404 through the app), got %d", n)
	}
}

func TestListenAndServeStopsOnContextCancel(t *testing.T) {
	log, _ := newLog(t)
	srv := New(log, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if logging.FromContext(r.Context()) != log {
			t.Error("request context lacks the logger")
		}
		w.WriteHeader(http.StatusOK)
	}), Addr("127.0.0.1:0"))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv.Addr = ln.Addr().String()
	_ = ln.Close()

	ctx, cancel := context.WithCancel(logging.NewContext(context.Background(), log))
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe(ctx) }()

	var resp *http.Response
	for range 50 {
		resp, err = http.Get("http://" + srv.Addr + "/")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("ListenAndServe: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ListenAndServe did not return after cancel")
	}
}

func TestShutdownBeforeServeIsNil(t *testing.T) {
	log, _ := newLog(t)
	srv := New(log, http.NotFoundHandler())
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Error(err)
	}
	if err := srv.ListenAndServe(context.Background()); err != nil {
		t.Errorf("ListenAndServe after Shutdown: %v", err)
	}
}

// TestListenAndServeWaitsForRunningRequests holds a request open across the cancel and checks
// ListenAndServe returns only after the handler finished.
func TestListenAndServeWaitsForRunningRequests(t *testing.T) {
	log, _ := newLog(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var finished atomic.Bool
	srv := New(log, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		finished.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv.Addr = ln.Addr().String()
	_ = ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe(ctx) }()
	go func() {
		for range 50 {
			resp, err := http.Get("http://" + srv.Addr + "/")
			if err == nil {
				_ = resp.Body.Close()
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	<-entered

	cancel()
	select {
	case <-done:
		t.Fatal("ListenAndServe returned while a request was running")
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if err != nil || !finished.Load() {
			t.Errorf("err %v, finished %v", err, finished.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ListenAndServe did not return after the request finished")
	}
}
