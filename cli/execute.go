package cli

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/AndreiD108/d108-go/failure"
	"github.com/AndreiD108/d108-go/logging"
	"github.com/AndreiD108/d108-go/work"
)

// execution is the state of one Execute call, carried in ctx to the leaf's RunE.
type execution struct {
	log    *slog.Logger
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	// hooksDone is set when the command's hooks have run; ran when its runner was reached.
	// classify reads them to tell a hook error from Cobra's own checks.
	hooksDone, ran bool
}

type executionKey struct{}

func executionOf(ctx context.Context) *execution {
	ex, _ := ctx.Value(executionKey{}).(*execution)
	return ex
}

// leafKey carries the leaf whose runner is running, for Flags and CommandPath.
type leafKey struct{}

func leafOf(ctx context.Context) *cobra.Command {
	cmd, _ := ctx.Value(leafKey{}).(*cobra.Command)
	return cmd
}

// Stdin returns the input stream Execute was given, for a runner that reads its input; the
// ctx is the runner's. Outside Execute it is os.Stdin.
func Stdin(ctx context.Context) io.Reader {
	if ex := executionOf(ctx); ex != nil {
		return ex.stdin
	}
	return os.Stdin
}

// Stderr returns the error stream Execute was given, for a runner's prompts, progress and
// diagnostics; stdout stays the result. Outside Execute it is os.Stderr.
func Stderr(ctx context.Context) io.Writer {
	if ex := executionOf(ctx); ex != nil {
		return ex.stderr
	}
	return os.Stderr
}

// Flags returns the flag set of the leaf whose runner holds ctx, for Changed, GetString and
// the rest, so a runner needs no reference to its own command. Outside a runner it is nil.
func Flags(ctx context.Context) *pflag.FlagSet {
	if cmd := leafOf(ctx); cmd != nil {
		return cmd.Flags()
	}
	return nil
}

// CommandPath returns the full path of the leaf whose runner holds ctx, with the tool's name
// ("sift auth set-key"), for a next step that names the command to run. Outside a runner it
// is "".
func CommandPath(ctx context.Context) string {
	if cmd := leafOf(ctx); cmd != nil {
		return cmd.CommandPath()
	}
	return ""
}

// Execute runs the tree once with the given streams and arguments (os.Args[1:] in main). The
// result goes to stdout; help and the error envelope go to stderr. It returns nil on success,
// else the classed error after writing the envelope: main returns it from the function
// lifecycle.Run runs, and Run exits with failure.ExitCode of it. The logger comes from ctx
// (logging.FromContext); Run puts it there. A cancelled ctx ends the command with the class
// cancelled (exit 130) once the runner returns ctx.Err(). A Cobra error before any runner
// ran (unknown command or flag, wrong argument count) is a usage error with Cobra's message.
// Every envelope carries details.command, the path below the root as command.run has it,
// unless the error set its own. A tree runs once, because Cobra keeps the flag values and
// the leaf contexts of a run in the tree: a second Execute on the same root panics; tests
// build a tree per run.
func Execute(ctx context.Context, root *cobra.Command, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if root.Annotations[annExecuted] != "" {
		panic("cli.Execute: this tree already ran; build a new tree for each run")
	}
	root.Annotations[annExecuted] = "true"
	wrapValidation(root)
	ex := &execution{log: logging.FromContext(ctx), stdin: stdin, stdout: stdout, stderr: stderr}
	root.SetIn(stdin)
	root.SetOut(stderr)
	root.SetErr(stderr)
	root.SetArgs(args)
	cmd, err := root.ExecuteContextC(context.WithValue(ctx, executionKey{}, ex))
	if err == nil {
		return nil
	}
	if cmd == nil {
		cmd = root
	}
	if !ex.ran {
		err = classify(cmd, ex, err)
	}
	err = withCommand(cmd, err)
	if !ex.ran && cmd != root && !isKitCommand(cmd) {
		_, end := work.Command(ctx, ex.log, pathOf(cmd), flagsOf(cmd))
		end(err)
	}
	_ = failure.Write(stderr, err)
	return err
}

// withCommand adds details.command, the command's path below the root, to an error that has
// no such key; an error that set its own keeps it. The result wraps err, so errors.Is still
// sees the cause.
func withCommand(cmd *cobra.Command, err error) error {
	e := failure.From(err)
	if _, ok := e.Details["command"]; ok {
		return err
	}
	details := make([]any, 0, 2*(len(e.Details)+1))
	for k, v := range e.Details {
		details = append(details, k, v)
	}
	details = append(details, "command", pathOf(cmd))
	return failure.Wrap(err, e.Class, e.Message, e.NextStep, details...)
}

// isKitCommand reports whether cmd is Cobra's plumbing rather than one of the tool's
// commands: the help command and the hidden __complete command. Neither is a unit of work,
// so no command.run line is written for them.
func isKitCommand(cmd *cobra.Command) bool {
	return cmd.Annotations[annHelp] != "" || cmd.Name() == cobra.ShellCompRequestCmd
}
