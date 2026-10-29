package paths

import (
	"testing"
)

func fake(goos string, env map[string]string) resolver {
	return resolver{
		app:    "slack-cli",
		prefix: "SLACK_CLI_",
		goos:   goos,
		home:   "/home/u",
		getenv: func(k string) string { return env[k] },
		uid:    1000,
	}
}

func TestLinuxDefaults(t *testing.T) {
	got := fake("linux", nil).resolve()
	want := Paths{
		Config:  "/home/u/.config/slack-cli",
		Data:    "/home/u/.local/share/slack-cli",
		State:   "/home/u/.local/state/slack-cli",
		Logs:    "/home/u/.local/state/slack-cli/logs",
		Cache:   "/home/u/.cache/slack-cli",
		Runtime: "/tmp/slack-cli-1000",
	}
	if got != want {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestDarwinDefaults(t *testing.T) {
	got := fake("darwin", nil).resolve()
	want := Paths{
		Config:  "/home/u/Library/Application Support/slack-cli/config",
		Data:    "/home/u/Library/Application Support/slack-cli/data",
		State:   "/home/u/Library/Application Support/slack-cli/state",
		Logs:    "/home/u/Library/Logs/slack-cli",
		Cache:   "/home/u/Library/Caches/slack-cli",
		Runtime: "/tmp/slack-cli-1000",
	}
	if got != want {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestResolutionOrder(t *testing.T) {
	env := map[string]string{
		"SLACK_CLI_CONFIG_DIR": "/cfg",
		"XDG_CONFIG_HOME":      "/xdg/config",
		"XDG_STATE_HOME":       "/xdg/state",
		"XDG_RUNTIME_DIR":      "/run/user/1000",
		"TMPDIR":               "/var/tmp",
	}
	got := fake("linux", env).resolve()
	if got.Config != "/cfg" {
		t.Errorf("app variable must win: %q", got.Config)
	}
	if got.State != "/xdg/state/slack-cli" {
		t.Errorf("XDG_STATE_HOME: %q", got.State)
	}
	if got.Logs != "/xdg/state/slack-cli/logs" {
		t.Errorf("Logs must follow State on Linux: %q", got.Logs)
	}
	if got.Runtime != "/run/user/1000/slack-cli" {
		t.Errorf("XDG_RUNTIME_DIR: %q", got.Runtime)
	}

	env["SLACK_CLI_LOGS_DIR"] = "/var/log/slack"
	got = fake("darwin", env).resolve()
	if got.Logs != "/var/log/slack" {
		t.Errorf("LOGS_DIR override on macOS: %q", got.Logs)
	}
	if got.Runtime != "/var/tmp/slack-cli" {
		t.Errorf("TMPDIR on macOS: %q", got.Runtime)
	}
	if got.State != "/xdg/state/slack-cli" {
		t.Errorf("XDG applies on macOS too: %q", got.State)
	}
}

func TestForUsesTheEnvironment(t *testing.T) {
	t.Setenv("HOME", "/home/x")
	t.Setenv("MY_TOOL_DATA_DIR", "/srv/data")
	p := For("my-tool")
	if p.Data != "/srv/data" {
		t.Errorf("Data = %q", p.Data)
	}
	p = For("my-tool", Prefix("MT_"))
	if p.Data == "/srv/data" {
		t.Error("a custom prefix must not read MY_TOOL_DATA_DIR")
	}
}
