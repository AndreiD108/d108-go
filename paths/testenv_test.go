package paths_test

import (
	"context"
	"strings"
	"testing"

	"github.com/AndreiD108/d108-go/config"
	"github.com/AndreiD108/d108-go/identity"
	"github.com/AndreiD108/d108-go/logging"
	"github.com/AndreiD108/d108-go/paths"
)

func setenv(t *testing.T, env []string) {
	t.Helper()
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		t.Setenv(name, value)
	}
}

func TestTestEnvPutsEveryDirectoryUnderDir(t *testing.T) {
	env := paths.TestEnv("MY_TOOL", "/tmp/x")
	want := []string{
		"MY_TOOL_CONFIG_DIR=/tmp/x/config", "MY_TOOL_DATA_DIR=/tmp/x/data", "MY_TOOL_STATE_DIR=/tmp/x/state",
		"MY_TOOL_LOGS_DIR=/tmp/x/logs", "MY_TOOL_CACHE_DIR=/tmp/x/cache", "MY_TOOL_RUNTIME_DIR=/tmp/x/run", "LOG_OUTPUT=stderr",
	}
	if strings.Join(env, "\n") != strings.Join(want, "\n") {
		t.Errorf("TestEnv = %q", env)
	}
	setenv(t, env)
	p := paths.For("my-tool")
	if p.Config != "/tmp/x/config" || p.Data != "/tmp/x/data" || p.State != "/tmp/x/state" || p.Logs != "/tmp/x/logs" || p.Cache != "/tmp/x/cache" || p.Runtime != "/tmp/x/run" {
		t.Errorf("For = %+v", p)
	}
	id := identity.New("my-tool")
	if s := logging.Resolve(id); s.Output != logging.OutputStderr || s.OutputSource != "LOG_OUTPUT" {
		t.Errorf("output %s from %s, want stderr from LOG_OUTPUT", s.Output, s.OutputSource)
	}
}

// The helper sets LOG_OUTPUT only; the app's own variable, inherited from the parent, wins.
func TestTestEnvDoesNotClearTheAppVariable(t *testing.T) {
	t.Setenv("MY_TOOL_LOG_OUTPUT", "file")
	setenv(t, paths.TestEnv("MY_TOOL", t.TempDir()))
	if s := logging.Resolve(identity.New("my-tool")); s.Output != logging.OutputFile || s.OutputSource != "MY_TOOL_LOG_OUTPUT" {
		t.Errorf("output %s from %s; the inherited MY_TOOL_LOG_OUTPUT must win", s.Output, s.OutputSource)
	}
}

func TestPrefixFormIsWithoutTheUnderscore(t *testing.T) {
	t.Setenv("HOME", "/home/x")
	t.Setenv("MT_DATA_DIR", "/srv/mt")
	t.Setenv("MY_TOOL_DATA_DIR", "/srv/my-tool")
	if p := paths.For("my-tool", paths.Prefix("MT")); p.Data != "/srv/mt" {
		t.Errorf("Data = %q with Prefix(\"MT\")", p.Data)
	}
	if p := paths.For("my-tool", paths.Prefix("")); p.Data != "/srv/my-tool" {
		t.Errorf("Data = %q with Prefix(\"\"), want the derived prefix", p.Data)
	}
	id := identity.New("my-tool", identity.Prefix("MT"))
	if p := paths.For(id.Name, paths.Prefix(id.Prefix)); p.Data != "/srv/mt" {
		t.Errorf("Data = %q through the identity's prefix", p.Data)
	}
	for name, fn := range map[string]func(){
		"paths.Prefix":  func() { paths.Prefix("MT_") },
		"paths.TestEnv": func() { paths.TestEnv("MT_", "/tmp/x") },
		"config.Prefix": func() { config.Prefix("MT_") },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s with a trailing underscore must panic", name)
				}
			}()
			fn()
		}()
	}
}

type foreign struct {
	Workers int `json:"workers" env:"WORKERS"`
}

func TestConfigPrefixForm(t *testing.T) {
	dir := t.TempDir()
	setenv(t, paths.TestEnv("MY_TOOL", dir))
	t.Setenv("MT_WORKERS", "5")
	t.Setenv("MY_TOOL_WORKERS", "7")
	var cfg foreign
	if err := config.Load(context.Background(), identity.New("my-tool"), &cfg, config.Prefix("MT")); err != nil || cfg.Workers != 5 {
		t.Errorf("Workers = %d, err %v; want MT_WORKERS", cfg.Workers, err)
	}
}
