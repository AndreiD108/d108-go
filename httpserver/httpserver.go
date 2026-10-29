// Package httpserver wraps http.Server with the request middleware of work (one
// request.handled per request, a span, panic recovery), GET /healthz, optional CORS and
// optional pprof. /healthz and /debug/pprof/ sit outside the middleware, so health checks and
// profiling write no lines.
//
//	srv := httpserver.New(log, routes, httpserver.Addr("127.0.0.1:8080"), httpserver.CORS("https://app.example"))
//	lifecycle.OnStop(ctx, "http", func() error { return srv.Shutdown(context.Background()) })
//	return srv.ListenAndServe(ctx)
package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"strings"
	"time"

	"github.com/AndreiD108/d108-go/work"
)

// ShutdownGrace bounds the graceful stop ListenAndServe starts when its context is cancelled;
// connections still open after it are closed.
const ShutdownGrace = 10 * time.Second

// Option changes how New builds the server.
type Option func(*Server)

// Addr sets the listen address (default "127.0.0.1:8080").
func Addr(addr string) Option { return func(s *Server) { s.Server.Addr = addr } }

// CORS allows browsers at origins to call the API: Access-Control-Allow-Origin for a matching
// Origin header, and a 204 answer to preflight requests. "*" allows every origin.
func CORS(origins ...string) Option { return func(s *Server) { s.cors = origins } }

// Pprof serves net/http/pprof under /debug/pprof/, outside the request middleware.
func Pprof() Option { return func(s *Server) { s.pprof = true } }

// Server is an http.Server with the standard's request handling around the app's handler.
type Server struct {
	*http.Server
	cors  []string
	pprof bool
}

// New builds the server: handler behind work.HTTP(log) and CORS, GET /healthz answering
// "ok", ReadHeaderTimeout 10 s and IdleTimeout 2 min. Every other field of the embedded
// http.Server may be set before ListenAndServe.
func New(log *slog.Logger, handler http.Handler, opts ...Option) *Server {
	s := &Server{Server: &http.Server{
		Addr:              "127.0.0.1:8080",
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}}
	for _, opt := range opts {
		opt(s)
	}
	app := handler
	if len(s.cors) > 0 {
		app = cors(s.cors, app)
	}
	var debug http.Handler
	if s.pprof {
		mux := http.NewServeMux()
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		debug = mux
	}
	logged := work.HTTP(log)(app)
	// Dispatch by hand, not through a mux, so r.Pattern stays empty for a plain handler
	// and the app's own mux sets the one http.route reports.
	s.Server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/healthz" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("ok\n"))
		case debug != nil && strings.HasPrefix(r.URL.Path, "/debug/pprof/"):
			debug.ServeHTTP(w, r)
		default:
			logged.ServeHTTP(w, r)
		}
	})
	return s
}

// ListenAndServe serves until Shutdown is called or ctx is cancelled, and returns nil in both
// cases. Request contexts carry ctx's values (logger, lifecycle) but not its cancellation, so a
// stop lets running requests finish. On ctx cancellation it calls Shutdown with ShutdownGrace
// and returns when that stop has finished: running requests are done or the grace has passed
// and their connections are closed.
func (s *Server) ListenAndServe(ctx context.Context) error {
	base := context.WithoutCancel(ctx)
	s.Server.BaseContext = func(net.Listener) context.Context { return base }
	stopped := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(stopped)
		sctx, cancel := context.WithTimeout(base, ShutdownGrace)
		defer cancel()
		_ = s.Shutdown(sctx)
	})
	err := s.Server.ListenAndServe()
	if !stop() {
		// The stop function ran or is running: wait for its Shutdown to finish.
		<-stopped
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown stops accepting connections and waits for running requests until ctx ends; when
// ctx ends first the remaining connections are closed and ctx's error is returned.
func (s *Server) Shutdown(ctx context.Context) error {
	err := s.Server.Shutdown(ctx)
	if err != nil && ctx.Err() != nil {
		_ = s.Server.Close()
	}
	return err
}

func cors(origins []string, next http.Handler) http.Handler {
	allowed := func(origin string) bool {
		for _, o := range origins {
			if o == "*" || o == origin {
				return true
			}
		}
		return false
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && allowed(origin) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				if req := r.Header.Get("Access-Control-Request-Headers"); req != "" {
					h.Set("Access-Control-Allow-Headers", req)
				}
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
