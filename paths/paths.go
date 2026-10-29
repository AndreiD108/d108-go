// Package paths resolves the six per-app directories of standard.md section 9.3 on Linux
// and macOS. Resolution for each: the app's own variable (<APP>_CONFIG_DIR and so on), then
// the XDG variable, then the platform default. The package does not create the directories.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/AndreiD108/d108-go/identity"
)

// Paths holds the six directories of one app.
type Paths struct {
	// Config holds settings and secrets; it moves with the user to a new machine.
	Config string
	// Data holds what the app cannot regenerate; it moves with the user.
	Data string
	// State holds what the app rebuilds over time; it stays on the machine.
	State string
	// Logs holds the log files of standard.md section 5.
	Logs string
	// Cache holds what the app can refetch.
	Cache string
	// Runtime holds sockets, pid and lock files; cleared at logout or reboot.
	Runtime string
}

// Option changes how For resolves.
type Option func(*resolver)

// Prefix sets the environment prefix without its trailing underscore ("SLACK_CLI"), the form
// identity.Prefix takes, when it differs from the one derived from the app name:
// paths.For(id.Name, paths.Prefix(id.Prefix)). Empty keeps the derived prefix. A prefix with
// a trailing underscore is a programming error and panics, so "SLACK_CLI_" cannot read
// SLACK_CLI__CONFIG_DIR silently.
func Prefix(prefix string) Option {
	if strings.HasSuffix(prefix, "_") {
		panic(fmt.Sprintf("paths.Prefix(%q): give the prefix without the trailing underscore", prefix))
	}
	return func(r *resolver) {
		if prefix != "" {
			r.prefix = prefix + "_"
		}
	}
}

type resolver struct {
	app    string
	prefix string
	goos   string
	home   string
	getenv func(string) string
	uid    int
}

// For returns the directories of app.
func For(app string, opts ...Option) Paths {
	r := resolver{
		app:    app,
		prefix: identity.EnvPrefixFor(app),
		goos:   runtime.GOOS,
		getenv: os.Getenv,
		uid:    os.Getuid(),
	}
	for _, opt := range opts {
		opt(&r)
	}
	r.home = r.getenv("HOME")
	if r.home == "" {
		r.home = os.TempDir()
	}
	return r.resolve()
}

func (r resolver) resolve() Paths {
	var p Paths
	if r.goos == "darwin" {
		support := filepath.Join(r.home, "Library", "Application Support", r.app)
		p.Config = r.pick("CONFIG_DIR", "XDG_CONFIG_HOME", filepath.Join(support, "config"))
		p.Data = r.pick("DATA_DIR", "XDG_DATA_HOME", filepath.Join(support, "data"))
		p.State = r.pick("STATE_DIR", "XDG_STATE_HOME", filepath.Join(support, "state"))
		p.Logs = r.pick("LOGS_DIR", "", filepath.Join(r.home, "Library", "Logs", r.app))
		p.Cache = r.pick("CACHE_DIR", "XDG_CACHE_HOME", filepath.Join(r.home, "Library", "Caches", r.app))
		p.Runtime = r.pick("RUNTIME_DIR", "TMPDIR", r.tmpRuntime())
	} else {
		p.Config = r.pick("CONFIG_DIR", "XDG_CONFIG_HOME", filepath.Join(r.home, ".config", r.app))
		p.Data = r.pick("DATA_DIR", "XDG_DATA_HOME", filepath.Join(r.home, ".local", "share", r.app))
		p.State = r.pick("STATE_DIR", "XDG_STATE_HOME", filepath.Join(r.home, ".local", "state", r.app))
		p.Logs = r.pick("LOGS_DIR", "", filepath.Join(p.State, "logs"))
		p.Cache = r.pick("CACHE_DIR", "XDG_CACHE_HOME", filepath.Join(r.home, ".cache", r.app))
		p.Runtime = r.pick("RUNTIME_DIR", "XDG_RUNTIME_DIR", r.tmpRuntime())
	}
	return p
}

// pick applies the resolution order: the app variable, the base variable with the app name
// appended, then the default.
func (r resolver) pick(appVar, baseVar, def string) string {
	if v := r.getenv(r.prefix + appVar); v != "" {
		return v
	}
	if baseVar != "" {
		if v := r.getenv(baseVar); v != "" {
			return filepath.Join(v, r.app)
		}
	}
	return def
}

func (r resolver) tmpRuntime() string {
	return filepath.Join("/tmp", r.app+"-"+strconv.Itoa(r.uid))
}

// TestEnv returns the variables that put every directory of an app under dir, as NAME=value
// strings for exec.Cmd.Env or t.Setenv: <PREFIX>_CONFIG_DIR=<dir>/config, _DATA_DIR=<dir>/data,
// _STATE_DIR=<dir>/state, _LOGS_DIR=<dir>/logs, _CACHE_DIR=<dir>/cache, _RUNTIME_DIR=<dir>/run,
// plus LOG_OUTPUT=stderr so the run writes no log file. prefix is without the trailing
// underscore ("SLACK_CLI"). It does not clear what the parent environment carries: an
// inherited <PREFIX>_LOG_OUTPUT wins over LOG_OUTPUT, and OTEL_* and TRACEPARENT would send
// the run's spans and trace to the real agent; a harness that builds a child environment
// strips those first.
func TestEnv(prefix, dir string) []string {
	if strings.HasSuffix(prefix, "_") {
		panic(fmt.Sprintf("paths.TestEnv(%q): give the prefix without the trailing underscore", prefix))
	}
	return []string{
		prefix + "_CONFIG_DIR=" + filepath.Join(dir, "config"),
		prefix + "_DATA_DIR=" + filepath.Join(dir, "data"),
		prefix + "_STATE_DIR=" + filepath.Join(dir, "state"),
		prefix + "_LOGS_DIR=" + filepath.Join(dir, "logs"),
		prefix + "_CACHE_DIR=" + filepath.Join(dir, "cache"),
		prefix + "_RUNTIME_DIR=" + filepath.Join(dir, "run"),
		"LOG_OUTPUT=stderr",
	}
}
