package identity

import (
	"os"
	"regexp"
	"runtime/debug"
	"testing"
)

func TestNewFillsEveryField(t *testing.T) {
	id := New("slack-cli")
	if id.Name != "slack-cli" {
		t.Errorf("Name = %q", id.Name)
	}
	if !regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`).MatchString(id.InstanceID) {
		t.Errorf("InstanceID %q is not a ULID", id.InstanceID)
	}
	if id.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", id.PID, os.Getpid())
	}
	if id.Version == "" {
		t.Error("Version is empty")
	}
	if New("slack-cli").InstanceID == id.InstanceID {
		t.Error("two identities share an instance id")
	}
}

func TestEnvPrefix(t *testing.T) {
	if got := New("slack-cli").EnvPrefix(); got != "SLACK_CLI_" {
		t.Errorf("derived prefix = %q", got)
	}
	if got := New("slack-cli", Prefix("SLACK")).EnvPrefix(); got != "SLACK_" {
		t.Errorf("set prefix = %q", got)
	}
}

func TestNewRejectsBadNames(t *testing.T) {
	for _, name := range []string{"", "Slack", "slack_cli", "-x", "a.b", string(make([]byte, 64))} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("New(%q) did not panic", name)
				}
			}()
			New(name)
		}()
	}
}

func TestVersionResolution(t *testing.T) {
	cases := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{"module version", &debug.BuildInfo{Main: debug.Module{Version: "v1.4.2"}}, "v1.4.2"},
		{"devel falls through to vcs", &debug.BuildInfo{
			Main:     debug.Module{Version: "(devel)"},
			Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc1234567890"}, {Key: "vcs.modified", Value: "true"}},
		}, "abc1234-dirty"},
		{"clean tree", &debug.BuildInfo{
			Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc1234567890"}, {Key: "vcs.modified", Value: "false"}},
		}, "abc1234"},
		{"nothing", &debug.BuildInfo{}, "dev"},
	}
	for _, c := range cases {
		if got := versionFromBuildInfo(c.info); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	version = "1.4.2"
	defer func() { version = "" }()
	if got := resolveVersion(); got != "1.4.2" {
		t.Errorf("ldflags version: got %q", got)
	}
}
