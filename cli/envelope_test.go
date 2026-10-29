package cli_test

import (
	"context"
	"strings"
	"testing"

	"github.com/AndreiD108/d108-go/cli/clitest"
)

func TestEveryEnvelopeNamesTheCommand(t *testing.T) {
	cases := map[string]struct {
		args    []string
		command string
	}{
		"app error":  {[]string{"fail", "remote"}, "fail"},
		"unclassed":  {[]string{"plainerr"}, "plainerr"},
		"panic":      {[]string{"boom"}, "boom"},
		"hook error": {[]string{"hook", "classed"}, "hook"},
		"own key":    {[]string{"mine"}, "custom"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, stderr := clitest.Run(t, tree(t), "", c.args...)
			if details := envelope(t, stderr)["details"].(map[string]any); details["command"] != c.command {
				t.Errorf("details %v, want command %q", details, c.command)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, stderr := clitest.RunContext(ctx, t, tree(t), "", "wait")
	if details := envelope(t, stderr)["details"].(map[string]any); details["command"] != "wait" {
		t.Errorf("cancelled: details %v", details)
	}
}

func TestHelpOfAnUnknownCommandIsUsage(t *testing.T) {
	exit, stdout, stderr := clitest.Run(t, tree(t), "", "help", "nothing")
	if exit != 2 || stdout != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", exit, stdout, stderr)
	}
	env := envelope(t, stderr)
	details := env["details"].(map[string]any)
	commands, _ := details["commands"].(map[string]any)
	if env["class"] != "usage" || details["command"] != "" || details["given"] != "nothing" || commands["doctor"] == nil {
		t.Errorf("envelope %v", env)
	}
	if len(clitest.Lines(t)) != 0 {
		t.Errorf("help wrote lines: %v", clitest.Lines(t))
	}
	exit, _, stderr = clitest.Run(t, tree(t), "", "help", "messages", "nothing")
	details = envelope(t, stderr)["details"].(map[string]any)
	commands, _ = details["commands"].(map[string]any)
	if exit != 2 || details["command"] != "messages" || commands["search"] != "Search messages" {
		t.Errorf("exit %d, details %v", exit, details)
	}
	exit, _, stderr = clitest.Run(t, tree(t), "", "help", "messages", "search", "extra")
	if exit != 0 || !strings.Contains(stderr, "Search messages") {
		t.Errorf("help of a leaf with extra words: exit %d, %q", exit, stderr)
	}
}

func TestCompleteWithoutArgsIsUsage(t *testing.T) {
	exit, stdout, stderr := clitest.Run(t, tree(t), "", "__complete")
	if exit != 2 || stdout != "" || envelope(t, stderr)["class"] != "usage" {
		t.Errorf("exit %d, stdout %q, stderr %q", exit, stdout, stderr)
	}
	if len(clitest.Lines(t)) != 0 {
		t.Errorf("__complete wrote lines: %v", clitest.Lines(t))
	}
}
