package cli

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"

	"github.com/AndreiD108/d108-go/failure"
)

// classify classes an error Cobra returned before any runner ran, by where it came from.
// Flag parsing and Args already produced usage failures through flagError and
// wrapValidation; incomplete made its own; those pass. After the hooks ran (PersistentPreRun,
// PreRun) Cobra checks required flags and flag groups, so an unclassed error then is a usage
// error with Cobra's message. Before that point it came from a hook and is classed as any
// runner error: cancelled, timeout or internal unless the hook classed it. The hidden
// __complete command has no hooks of ours (Cobra adds it at run time, after wrapValidation),
// so its only error, the Args check, is a usage error too.
func classify(cmd *cobra.Command, ex *execution, err error) error {
	if ex.hooksDone || cmd.Name() == cobra.ShellCompRequestCmd {
		return usage(cmd, err)
	}
	return failure.From(err)
}

// flagError turns a flag parse error (unknown flag, missing or bad value) into a usage
// failure with Cobra's message.
func flagError(cmd *cobra.Command, err error) error {
	return usage(cmd, err)
}

// wrapValidation prepares every command of the tree for classify: its Args check classes a
// failure as usage, and its PreRun hook marks the end of the hooks. Cobra's order stays:
// flags, Args, persistent pre-run hooks, pre-run, required flags, flag groups, the run.
func wrapValidation(cmd *cobra.Command) {
	args := cmd.Args
	cmd.Args = func(c *cobra.Command, a []string) error {
		if args != nil {
			if err := args(c, a); err != nil {
				return usage(c, err)
			}
		}
		return nil
	}
	preE, pre := cmd.PreRunE, cmd.PreRun
	cmd.PreRun = nil
	cmd.PreRunE = func(c *cobra.Command, a []string) error {
		if preE != nil {
			if err := preE(c, a); err != nil {
				return err
			}
		} else if pre != nil {
			pre(c, a)
		}
		if ex := executionOf(c.Context()); ex != nil {
			ex.hooksDone = true
		}
		return nil
	}
	for _, child := range cmd.Commands() {
		wrapValidation(child)
	}
}

// usage is a usage failure with Cobra's message and the command's help as the next step; a
// failure that already has a class passes through.
func usage(cmd *cobra.Command, err error) error {
	if _, ok := errors.AsType[*failure.Error](err); ok {
		return err
	}
	return failure.Wrap(err, failure.Usage, err.Error(), helpStep(cmd), "command", pathOf(cmd))
}

// incomplete is the usage error of a root or group run without a leaf: the subcommand is
// missing or unknown. details.commands lists the available subcommands with their one-line
// descriptions, so the caller can pick without a second run.
func incomplete(cmd *cobra.Command, args []string) error {
	message := "`" + cmd.CommandPath() + "` needs a subcommand"
	details := []any{"command", pathOf(cmd), "commands", subcommands(cmd)}
	if len(args) > 0 {
		message = "`" + cmd.CommandPath() + "` has no subcommand " + args[0]
		details = append(details, "given", args[0])
	}
	return failure.New(failure.Usage, message, "use one of the commands in details.commands; "+helpStep(cmd), details...)
}

func helpStep(cmd *cobra.Command) string {
	return "run `" + cmd.CommandPath() + " --help` for the usage"
}

// subcommands maps each available subcommand name to its Short, the help command excluded.
func subcommands(cmd *cobra.Command) map[string]string {
	m := map[string]string{}
	for _, c := range cmd.Commands() {
		if c.IsAvailableCommand() {
			m[c.Name()] = strings.TrimSpace(c.Short)
		}
	}
	return m
}
