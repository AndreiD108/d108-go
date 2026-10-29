package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/AndreiD108/d108-go/cli"
	"github.com/AndreiD108/d108-go/cli/clitest"
	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/identity"
	"github.com/AndreiD108/d108/logcheck"
)

type hit struct {
	Channel string `json:"channel"`
	Text    string `json:"text"`
}

// tree builds the sample tool: a group with a leaf of three formats, leaves that fail in
// every way the library must handle, and the built-in doctor.
func tree(t *testing.T) *cobra.Command {
	t.Helper()
	id := identity.Identity{Name: "tool", Version: "test", InstanceID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", PID: os.Getpid()}
	root := cli.Root(id, cli.Short("A sample tool"))
	msg := cli.Group(root, "messages", "Read and search messages")
	search := cli.Command(msg, "search", "Search messages", func(_ context.Context, args []string, out cli.Output) error {
		hits := []hit{{"general", args[0] + " one"}, {"ops", args[0] + " two"}}
		switch out.Format() {
		case cli.JSON:
			return out.JSON(map[string]any{"hits": hits, "count": len(hits)})
		case cli.JSONL:
			for _, h := range hits {
				if err := out.JSONL(h); err != nil {
					return err
				}
			}
			return nil
		case cli.Text:
			for _, h := range hits {
				if err := out.Text("%-8s %s\n", h.Channel, h.Text); err != nil {
					return err
				}
			}
		}
		return nil
	}, cli.Formats(cli.JSON, cli.JSONL, cli.Text))
	search.Args = cobra.ExactArgs(1)
	search.Flags().Bool("force", false, "ignore the cache")
	search.Flags().Int("limit", 20, "hits to return")

	cli.Command(root, "plain", "A leaf without a Formats declaration", func(_ context.Context, _ []string, out cli.Output) error {
		return out.JSON(map[string]string{"format": string(out.Format())})
	})
	cli.Command(root, "twice", "Writes two JSON objects", func(_ context.Context, _ []string, out cli.Output) error {
		if err := out.JSON(map[string]int{"n": 1}); err != nil {
			return err
		}
		return out.JSON(map[string]int{"n": 2})
	})
	fail := cli.Command(root, "fail", "Returns the classed error named by --class", func(_ context.Context, args []string, _ cli.Output) error {
		return failure.New(failure.Class(args[0]), "it failed", "try again", "code", 42)
	})
	fail.Args = cobra.ExactArgs(1)
	cli.Command(root, "plainerr", "Returns an unclassed error", func(context.Context, []string, cli.Output) error {
		return errors.New("disk on fire")
	})
	cli.Command(root, "boom", "Panics", func(context.Context, []string, cli.Output) error {
		panic("boom value")
	})
	cli.Command(root, "wait", "Returns when ctx is cancelled", func(ctx context.Context, _ []string, out cli.Output) error {
		<-ctx.Done()
		return ctx.Err()
	})
	need := cli.Command(root, "need", "Has a required flag", func(context.Context, []string, cli.Output) error { return nil })
	need.Flags().String("id", "", "the id")
	_ = need.MarkFlagRequired("id")
	filled := cli.Command(root, "filled", "A hook fills the required flag", func(_ context.Context, _ []string, out cli.Output) error {
		return out.JSON(map[string]string{"id": "from-hook"})
	})
	filled.Flags().String("id", "", "the id")
	_ = filled.MarkFlagRequired("id")
	filled.PreRun = func(cmd *cobra.Command, _ []string) { _ = cmd.Flags().Set("id", "from-hook") }
	hook := cli.Command(root, "hook", "Fails in PreRunE with the error named by its argument", func(context.Context, []string, cli.Output) error {
		return nil
	})
	hook.Args = cobra.ExactArgs(1)
	hook.PreRunE = func(_ *cobra.Command, args []string) error {
		switch args[0] {
		case "cancel":
			return context.Canceled
		case "classed":
			return failure.New(failure.Auth, "no token", "log in")
		}
		return errors.New("hook broke")
	}
	cli.Command(root, "stdin", "Echoes stdin", func(ctx context.Context, _ []string, out cli.Output) error {
		b, err := io.ReadAll(cli.Stdin(ctx))
		if err != nil {
			return err
		}
		return out.Text("%s\n", b)
	}, cli.Formats(cli.Text))
	where := cli.Command(root, "where [thing]", "Reports what the runner sees through ctx", func(ctx context.Context, _ []string, out cli.Output) error {
		if _, err := io.WriteString(cli.Stderr(ctx), "note\n"); err != nil {
			return err
		}
		limit, _ := cli.Flags(ctx).GetInt("limit")
		return out.JSON(map[string]any{"path": cli.CommandPath(ctx), "limit": limit, "changed": cli.Flags(ctx).Changed("limit")})
	})
	where.Flags().Int("limit", 20, "hits to return")
	cli.Command(root, "mine", "Fails with its own details.command", func(context.Context, []string, cli.Output) error {
		return failure.New(failure.Conflict, "taken", "pick another", "command", "custom")
	})
	cli.Command(root, "secret", "Reads a secret", func(ctx context.Context, _ []string, out cli.Output) error {
		s, err := cli.ReadSecret(ctx, "Key: ")
		if err != nil {
			return err
		}
		return out.JSON(map[string]string{"secret": s})
	})
	return root
}

func envelope(t *testing.T, stderr string) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal([]byte(stderr), &env); err != nil {
		t.Fatalf("stderr is not one JSON envelope: %v: %q", err, stderr)
	}
	for _, key := range []string{"class", "message", "next_step"} {
		if _, ok := env[key].(string); !ok {
			t.Errorf("envelope lacks %s: %v", key, env)
		}
	}
	return env
}

// commandRun returns the one command.run line of the test's runs.
func commandRun(t *testing.T) map[string]any {
	t.Helper()
	var found map[string]any
	for _, line := range clitest.Lines(t) {
		if line["event_name"] == "command.run" {
			if found != nil {
				t.Fatalf("two command.run lines: %v and %v", found, line)
			}
			found = line
		}
	}
	if found == nil {
		t.Fatalf("no command.run line in %v", clitest.Lines(t))
	}
	return found
}

func TestDefaultFormatIsTheFirstDeclared(t *testing.T) {
	exit, stdout, stderr := clitest.Run(t, tree(t), "", "messages", "search", "deploy")
	if exit != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q", exit, stderr)
	}
	var result struct {
		Hits  []hit `json:"hits"`
		Count int   `json:"count"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil || result.Count != 2 {
		t.Fatalf("stdout %q: %v", stdout, err)
	}
	if strings.Count(stdout, "\n") != 1 {
		t.Errorf("json is one line: %q", stdout)
	}
	line := commandRun(t)
	if line["command"] != "messages search" || line["exit_code"] != float64(0) || line["outcome"] != "ok" || line["args.flags"] != "" {
		t.Errorf("command.run: %v", line)
	}
}

func TestUndeclaredLeafIsJSONOnly(t *testing.T) {
	exit, stdout, _ := clitest.Run(t, tree(t), "", "plain")
	if exit != 0 || stdout != `{"format":"json"}`+"\n" {
		t.Fatalf("exit %d, stdout %q", exit, stdout)
	}
	exit, stdout, stderr := clitest.Run(t, tree(t), "", "plain", "--format", "text")
	if exit != 2 || stdout != "" {
		t.Fatalf("exit %d, stdout %q", exit, stdout)
	}
	env := envelope(t, stderr)
	if env["class"] != "usage" {
		t.Errorf("class %v", env["class"])
	}
	details := env["details"].(map[string]any)
	if formats, _ := details["formats"].([]any); len(formats) != 1 || formats[0] != "json" {
		t.Errorf("details.formats %v", details["formats"])
	}
}

func TestFormatSelected(t *testing.T) {
	exit, stdout, _ := clitest.Run(t, tree(t), "", "messages", "search", "--format", "jsonl", "--force", "deploy")
	if exit != 0 {
		t.Fatal(exit)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], `{"channel":"general"`) {
		t.Errorf("jsonl: %q", stdout)
	}
	line := commandRun(t)
	if line["args.flags"] != "--force --format" {
		t.Errorf("args.flags %v", line["args.flags"])
	}

	exit, stdout, _ = clitest.Run(t, tree(t), "", "messages", "search", "deploy", "--format=text")
	if exit != 0 || !strings.HasPrefix(stdout, "general  deploy one\n") {
		t.Errorf("text: exit %d, %q", exit, stdout)
	}
}

func TestFormatRefused(t *testing.T) {
	exit, stdout, stderr := clitest.Run(t, tree(t), "", "messages", "search", "--format", "xml", "deploy")
	if exit != 2 || stdout != "" {
		t.Fatalf("exit %d, stdout %q", exit, stdout)
	}
	env := envelope(t, stderr)
	if env["class"] != "usage" || !strings.Contains(env["next_step"].(string), "json (default), jsonl, text") {
		t.Errorf("envelope %v", env)
	}
	line := commandRun(t)
	if line["outcome"] != "error" || line["error.type"] != "usage" || line["exit_code"] != float64(2) {
		t.Errorf("command.run %v", line)
	}
	exit, stdout, stderr = clitest.Run(t, tree(t), "", "messages", "search", "--format=", "deploy")
	if exit != 2 || stdout != "" || envelope(t, stderr)["class"] != "usage" {
		t.Errorf("empty --format: exit %d, stdout %q, stderr %q", exit, stdout, stderr)
	}
}

func TestJSONTwiceIsInternal(t *testing.T) {
	exit, stdout, stderr := clitest.Run(t, tree(t), "", "twice")
	if exit != 1 || stdout != `{"n":1}`+"\n" {
		t.Fatalf("exit %d, stdout %q", exit, stdout)
	}
	if envelope(t, stderr)["class"] != "internal" {
		t.Error(stderr)
	}
}

func TestEnvelopePerClass(t *testing.T) {
	for _, class := range []failure.Class{failure.Internal, failure.Usage, failure.Auth, failure.Permission,
		failure.NotFound, failure.Conflict, failure.Remote, failure.Timeout, failure.Cancelled, failure.Terminated} {
		t.Run(string(class), func(t *testing.T) {
			exit, stdout, stderr := clitest.Run(t, tree(t), "", "fail", string(class))
			if exit != failure.Code(class) || stdout != "" {
				t.Fatalf("exit %d, stdout %q", exit, stdout)
			}
			env := envelope(t, stderr)
			if env["class"] != string(class) || env["message"] != "it failed" || env["next_step"] != "try again" {
				t.Errorf("envelope %v", env)
			}
			if env["details"].(map[string]any)["code"] != float64(42) {
				t.Errorf("details %v", env["details"])
			}
			line := commandRun(t)
			if line["exit_code"] != float64(failure.Code(class)) || line["error.type"] != string(class) {
				t.Errorf("command.run %v", line)
			}
		})
	}
}

func TestUnclassedErrorIsInternal(t *testing.T) {
	exit, _, stderr := clitest.Run(t, tree(t), "", "plainerr")
	env := envelope(t, stderr)
	if exit != 1 || env["class"] != "internal" || env["details"].(map[string]any)["cause"] != "disk on fire" {
		t.Errorf("exit %d, %v", exit, env)
	}
	line := commandRun(t)
	if line["exception.message"] != "disk on fire" {
		t.Errorf("command.run %v", line)
	}
}

func TestPanicIsInternal(t *testing.T) {
	exit, stdout, stderr := clitest.Run(t, tree(t), "", "boom")
	if exit != 1 || stdout != "" {
		t.Fatalf("exit %d, stdout %q", exit, stdout)
	}
	env := envelope(t, stderr)
	if env["class"] != "internal" || !strings.Contains(env["details"].(map[string]any)["cause"].(string), "boom value") {
		t.Errorf("envelope %v", env)
	}
	line := commandRun(t)
	if line["outcome"] != "error" || line["exit_code"] != float64(1) {
		t.Errorf("command.run %v", line)
	}
	if st, _ := line["exception.stacktrace"].(string); !strings.Contains(st, "cli_test.go") {
		t.Errorf("stacktrace %q", st)
	}
}

func TestCancelledIs130(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	exit, stdout, stderr := clitest.RunContext(ctx, t, tree(t), "", "wait")
	if exit != 130 || stdout != "" || envelope(t, stderr)["class"] != "cancelled" {
		t.Fatalf("exit %d, stdout %q, stderr %q", exit, stdout, stderr)
	}
	line := commandRun(t)
	if line["outcome"] != "cancelled" || line["exit_code"] != float64(130) || line["severity_text"] != "WARN" {
		t.Errorf("command.run %v", line)
	}
}

func TestCobraUsageErrors(t *testing.T) {
	cases := map[string]struct {
		args    []string
		command string
		run     bool
	}{
		"unknown flag":      {[]string{"messages", "search", "--nope", "x"}, "messages search", true},
		"missing argument":  {[]string{"messages", "search"}, "messages search", true},
		"unknown command":   {[]string{"nothing"}, "", false},
		"bare root":         {nil, "", false},
		"bare group":        {[]string{"messages"}, "messages", true},
		"unknown sub":       {[]string{"messages", "nothing"}, "messages", true},
		"flag needs value":  {[]string{"messages", "search", "x", "--limit"}, "messages search", true},
		"flag value parse":  {[]string{"messages", "search", "x", "--limit", "many"}, "messages search", true},
		"root flag no leaf": {[]string{"--format", "json"}, "", false},
		"required flag":     {[]string{"need"}, "need", true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			exit, stdout, stderr := clitest.Run(t, tree(t), "", c.args...)
			if exit != 2 || stdout != "" {
				t.Fatalf("exit %d, stdout %q, stderr %q", exit, stdout, stderr)
			}
			env := envelope(t, stderr)
			if env["class"] != "usage" || !strings.Contains(env["next_step"].(string), "--help") {
				t.Errorf("envelope %v", env)
			}
			if env["details"].(map[string]any)["command"] != c.command {
				t.Errorf("details.command %v, want %q", env["details"], c.command)
			}
			if !c.run {
				if len(clitest.Lines(t)) != 0 {
					t.Errorf("lines for a root-level usage error: %v", clitest.Lines(t))
				}
				return
			}
			line := commandRun(t)
			if line["command"] != c.command || line["error.type"] != "usage" || line["exit_code"] != float64(2) {
				t.Errorf("command.run %v", line)
			}
		})
	}
}

func TestHookMaySetARequiredFlag(t *testing.T) {
	exit, stdout, stderr := clitest.Run(t, tree(t), "", "filled")
	if exit != 0 || stdout != `{"id":"from-hook"}`+"\n" {
		t.Errorf("exit %d, stdout %q, stderr %q", exit, stdout, stderr)
	}
	if line := commandRun(t); line["args.flags"] != "--id" {
		t.Errorf("command.run %v", line)
	}
}

func TestHookErrorsKeepTheirClass(t *testing.T) {
	for arg, want := range map[string]failure.Class{"cancel": failure.Cancelled, "classed": failure.Auth, "plain": failure.Internal} {
		t.Run(arg, func(t *testing.T) {
			exit, _, stderr := clitest.Run(t, tree(t), "", "hook", arg)
			env := envelope(t, stderr)
			if exit != failure.Code(want) || env["class"] != string(want) {
				t.Errorf("exit %d, envelope %v", exit, env)
			}
			if line := commandRun(t); line["error.type"] != string(want) || line["command"] != "hook" {
				t.Errorf("command.run %v", line)
			}
		})
	}
}

func TestIncompleteListsCommands(t *testing.T) {
	_, _, stderr := clitest.Run(t, tree(t), "", "messages")
	details := envelope(t, stderr)["details"].(map[string]any)
	commands, _ := details["commands"].(map[string]any)
	if commands["search"] != "Search messages" {
		t.Errorf("details.commands %v", details)
	}
	_, _, stderr = clitest.Run(t, tree(t), "")
	commands, _ = envelope(t, stderr)["details"].(map[string]any)["commands"].(map[string]any)
	if _, ok := commands["doctor"]; !ok || len(commands) < 5 {
		t.Errorf("root commands %v", commands)
	}
}

func TestHelpGoesToStderr(t *testing.T) {
	exit, stdout, stderr := clitest.Run(t, tree(t), "", "messages", "search", "--help")
	if exit != 0 || stdout != "" {
		t.Fatalf("exit %d, stdout %q", exit, stdout)
	}
	for _, want := range []string{"Formats:\n  json (default), jsonl, text", "--force", "--format string"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("help lacks %q:\n%s", want, stderr)
		}
	}
	if len(clitest.Lines(t)) != 0 {
		t.Errorf("help wrote lines: %v", clitest.Lines(t))
	}
	exit, stdout, stderr = clitest.Run(t, tree(t), "", "help", "messages")
	if exit != 0 || stdout != "" || !strings.Contains(stderr, "search") {
		t.Errorf("help command: exit %d, stdout %q, stderr %q", exit, stdout, stderr)
	}
	exit, _, stderr = clitest.Run(t, tree(t), "", "--help")
	if exit != 0 || !strings.Contains(stderr, "A sample tool") || strings.Contains(stderr, "completion") {
		t.Errorf("root help: exit %d, %q", exit, stderr)
	}
}

func TestDoctor(t *testing.T) {
	exit, stdout, stderr := clitest.Run(t, tree(t), "", "doctor")
	if exit != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q", exit, stderr)
	}
	for _, want := range []string{"program\n", "  name         tool", "logging\n", "  output  ", "  level   ", "telemetry\n", "  agent  ", "  tracing  "} {
		if !strings.Contains(stdout, want) {
			t.Errorf("doctor text lacks %q:\n%s", want, stdout)
		}
	}
	if line := commandRun(t); line["command"] != "doctor" {
		t.Errorf("command.run %v", line)
	}
}

func TestDoctorJSON(t *testing.T) {
	exit, stdout, _ := clitest.Run(t, tree(t), "", "doctor", "--format", "json")
	var report map[string]map[string]map[string]any
	if err := json.Unmarshal([]byte(stdout), &report); err != nil || exit != 0 {
		t.Fatalf("doctor json: %v, exit %d, %q", err, exit, stdout)
	}
	if report["program"]["instance_id"]["value"] != "01ARZ3NDEKTSV4RRFFQ69G5FAV" || report["logging"]["level"]["source"] == "" {
		t.Errorf("report %v", report)
	}
	if !strings.HasPrefix(stdout, `{"program":{"name":`) {
		t.Errorf("sections out of order: %q", stdout)
	}
}

func TestTreeRunsOnce(t *testing.T) {
	root := tree(t)
	clitest.Run(t, root, "", "plain")
	defer func() {
		if recover() == nil {
			t.Error("second run on the same tree did not panic")
		}
	}()
	clitest.Run(t, root, "", "plain")
}

func TestLinesPassLogcheck(t *testing.T) {
	clitest.Run(t, tree(t), "", "messages", "search", "--force", "deploy")
	clitest.Run(t, tree(t), "", "boom")
	clitest.Run(t, tree(t), "", "messages", "search", "--format", "xml", "deploy")
	clitest.Run(t, tree(t), "", "fail", "remote")
	lines := clitest.Raw(t)
	if len(lines) != 4 {
		t.Fatalf("%d lines: %s", len(lines), lines)
	}
	for _, raw := range lines {
		if problems := logcheck.Check(raw); problems != nil {
			t.Errorf("%v: %s", problems, raw)
		}
	}
}

func TestStdinReachesTheRunner(t *testing.T) {
	_, stdout, _ := clitest.Run(t, tree(t), "hello", "stdin")
	if stdout != "hello\n" {
		t.Errorf("stdout %q", stdout)
	}
}
