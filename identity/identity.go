// Package identity names the running program once: service.name, service.version,
// service.instance.id and the pid. Logging and tracing both read the same Identity, so the
// three values never differ between the two (standard.md section 9).
package identity

import (
	"crypto/rand"
	"fmt"
	"os"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/oklog/ulid/v2"
)

// version is set at link time by release builds:
//
//	-ldflags "-X github.com/AndreiD108/d108-go/identity.version=1.4.2"
var version string

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Identity is the program's name, version, instance id and pid. Tests build it as a struct
// literal; programs call New.
type Identity struct {
	// Name is service.name: lowercase, [a-z0-9-], at most 63 bytes, stable across versions.
	Name string
	// Version is service.version: the build version, or "dev".
	Version string
	// InstanceID is service.instance.id: one ULID per process.
	InstanceID string
	// PID is process.pid.
	PID int
	// Prefix is the environment prefix without the trailing underscore ("SLACK_CLI"). Empty
	// means derived from Name. Read it through EnvPrefix.
	Prefix string
}

// Option changes how New fills the Identity.
type Option func(*Identity)

// Prefix sets the environment prefix (without the trailing underscore) when it must differ
// from the one derived from the name.
func Prefix(prefix string) Option {
	return func(id *Identity) { id.Prefix = prefix }
}

// New makes the Identity for this process. It panics when name is not a valid service.name,
// because every line and span would be rejected by the contract.
func New(name string, opts ...Option) Identity {
	if !namePattern.MatchString(name) || len(name) > 63 {
		panic(fmt.Sprintf("identity.New(%q): service.name must match ^[a-z0-9][a-z0-9-]*$ and be at most 63 bytes", name))
	}
	id := Identity{
		Name:       name,
		Version:    resolveVersion(),
		InstanceID: ulid.MustNew(ulid.Now(), rand.Reader).String(),
		PID:        os.Getpid(),
	}
	for _, opt := range opts {
		opt(&id)
	}
	return id
}

// EnvPrefix returns the prefix of the program's environment variables with its trailing
// underscore: "SLACK_CLI_" for the name "slack-cli", or Prefix plus "_" when Prefix is set.
func (id Identity) EnvPrefix() string {
	if id.Prefix != "" {
		return id.Prefix + "_"
	}
	return EnvPrefixFor(id.Name)
}

// EnvPrefixFor derives the environment prefix from a service name by the rule of standard.md
// section 5: upper case, "-" as "_", plus a trailing underscore.
func EnvPrefixFor(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_"
}

// resolveVersion follows standard.md section 9.4: the linker variable, then the module
// version from `go install pkg@version`, then the VCS revision with "-dirty", then "dev".
func resolveVersion() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	return versionFromBuildInfo(info)
}

func versionFromBuildInfo(info *debug.BuildInfo) string {
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	var revision string
	modified := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if revision == "" {
		return "dev"
	}
	if len(revision) > 7 {
		revision = revision[:7]
	}
	if modified {
		revision += "-dirty"
	}
	return revision
}
