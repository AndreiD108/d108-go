package work

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/logging"
)

// CallerIDHeader is the request header whose value becomes caller.id on every line of the
// request and on request.handled.
const CallerIDHeader = "Caller-Id"

// PanicError is the error a recovered panic becomes: exception.type work.PanicError,
// exception.message "panic: <value>", exception.stacktrace the goroutine's stack at recovery.
type PanicError struct {
	Value any
	Stack string
}

// NewPanicError wraps a recovered panic value with the current stack.
func NewPanicError(value any) *PanicError {
	return &PanicError{Value: value, Stack: string(debug.Stack())}
}

func (e *PanicError) Error() string { return fmt.Sprintf("panic: %v", e.Value) }

// StackTrace gives the handler exception.stacktrace.
func (e *PanicError) StackTrace() string { return e.Stack }

// Unwrap exposes a panic value that is itself an error.
func (e *PanicError) Unwrap() error {
	err, _ := e.Value.(error)
	return err
}

// HTTP returns the request middleware: it continues the trace of the traceparent header, opens
// a server span, puts caller.id from the Caller-Id header on the request's attribute set,
// recovers a panic into a 500, and writes request.handled with http.request.method, http.route,
// http.response.status_code, duration_ms and outcome. outcome is error for a status of 500 or
// more or a panic (ERROR), cancelled when the client went away before the handler finished or
// the handler panicked with http.ErrAbortHandler (WARN), ok otherwise (INFO). A nil log uses
// the logger in the request context.
//
// http.route is the matched pattern: r.Pattern after the handler ran (net/http ServeMux sets
// it on the request the middleware passed), else what the handler side reported with
// SetRoute. A router that clones the request before matching (chi) hides its r.Pattern from
// a middleware outside it; inside such a router, or from a handler, call SetRoute.
//
// A panic is written as request.handled with exception.* and ends the span. When the
// response had not started, the client gets a 500. When it had, the connection is aborted
// after the line is written (the middleware panics with http.ErrAbortHandler), so the client
// sees a cut response, not a complete one. http.ErrAbortHandler itself is passed on the same
// way after its line.
func HTTP(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ctx := w3c.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			ctx, span := otel.Tracer(tracerName).Start(ctx, r.Method, trace.WithSpanKind(trace.SpanKindServer))
			ctx, set := logging.WithAttrSet(ctx)
			if id := r.Header.Get(CallerIDHeader); id != "" {
				set.Add(slog.String("caller.id", id))
			}
			slot := &routeSlot{}
			ctx = context.WithValue(ctx, routeKey{}, slot)
			r = r.WithContext(ctx)
			rw := &responseWriter{ResponseWriter: w}
			l := log
			if l == nil {
				l = logging.FromContext(ctx)
			}

			var panicErr *PanicError
			aborted := false
			func() {
				defer func() {
					v := recover()
					if v == nil {
						return
					}
					if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
						aborted = true
						return
					}
					panicErr = NewPanicError(v)
					if !rw.wrote {
						http.Error(rw, "internal server error", http.StatusInternalServerError)
					} else {
						aborted = true
					}
				}()
				next.ServeHTTP(rw, r)
			}()

			route := routeOf(r.Pattern)
			if route == "" {
				route = slot.get()
			}
			status := rw.status
			if status == 0 {
				status = http.StatusOK
			}
			attrs := []slog.Attr{
				slog.String("http.request.method", r.Method),
				slog.Int("http.response.status_code", status),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			}
			if route != "" {
				attrs = append(attrs, slog.String("http.route", route))
			}
			outcome, level := OutcomeOK, slog.LevelInfo
			var spanErr error
			description := ""
			switch {
			case panicErr != nil:
				outcome, level = OutcomeError, slog.LevelError
				attrs = append(attrs, slog.String("error.type", string(failure.Internal)))
				spanErr, description = panicErr, panicErr.Error()
			case status >= http.StatusInternalServerError:
				outcome, level = OutcomeError, slog.LevelError
				description = http.StatusText(status)
			case aborted, r.Context().Err() != nil:
				outcome, level = OutcomeCancelled, slog.LevelWarn
			}
			attrs = append(attrs, slog.String("outcome", outcome))
			line := append([]slog.Attr{logging.Event("request.handled")}, attrs...)
			if panicErr != nil {
				line = append(line, slog.Any("error", panicErr))
			}
			l.LogAttrs(ctx, level, "", line...)

			name := r.Method
			if route != "" {
				name += " " + route
			}
			span.SetName(name)
			endSpan(span, append(logging.Attrs(ctx), attrs...), spanErr, description)
			if aborted {
				panic(http.ErrAbortHandler)
			}
		})
	}
}

type routeKey struct{}

// routeSlot carries the route a handler reports with SetRoute back to the middleware; it
// lives in the context, which a request clone keeps.
type routeSlot struct {
	mu      sync.Mutex
	pattern string
}

func (s *routeSlot) get() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pattern
}

// SetRoute reports the matched route pattern of the request whose context is ctx, for
// routers that do not set r.Pattern on the request the middleware sees. Report in a defer,
// so a panicking handler still gets its route on the line. From a chi router:
//
//	r.Use(func(next http.Handler) http.Handler {
//		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//			defer func() { work.SetRoute(r.Context(), chi.RouteContext(r.Context()).RoutePattern()) }()
//			next.ServeHTTP(w, r)
//		})
//	})
//
// Outside a request handled by HTTP it does nothing.
func SetRoute(ctx context.Context, pattern string) {
	s, _ := ctx.Value(routeKey{}).(*routeSlot)
	if s == nil {
		return
	}
	s.mu.Lock()
	s.pattern = pattern
	s.mu.Unlock()
}

// routeOf strips the method prefix a net/http ServeMux pattern carries ("GET /search" to
// "/search"), so http.route reads the same for every router.
func routeOf(pattern string) string {
	method, rest, ok := strings.Cut(pattern, " ")
	if !ok || method == "" || method != strings.ToUpper(method) {
		return pattern
	}
	return rest
}

// responseWriter records the status and keeps Flush, Hijack and Unwrap, so streaming
// handlers and http.ResponseController keep working through the middleware.
type responseWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

// WriteHeader records the first final status; informational codes (1xx) pass through and
// leave the final status open.
func (w *responseWriter) WriteHeader(code int) {
	if !w.wrote && code >= http.StatusOK {
		w.status = code
		w.wrote = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.status = http.StatusOK
		w.wrote = true
	}
	return w.ResponseWriter.Write(b)
}

func (w *responseWriter) Flush() {
	w.wrote = true
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		w.wrote = true
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
