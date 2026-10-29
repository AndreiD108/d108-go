// Package cli builds the Cobra tree of a tool by standard.md section 9.2: Root makes the root
// with the persistent --format flag and the doctor subcommand, Group makes a command that
// only holds subcommands, Command makes a leaf. Execute runs the tree with injected streams:
// each leaf runs inside work.Command, so one command.run line ends the run; a returned error
// or a panic becomes the error envelope on stderr; the classed error comes back to the caller
// so lifecycle.Run exits with its code.
//
//	func main() {
//		id := identity.New("slack-cli")
//		root := cli.Root(id, cli.Short("Slack from the terminal"))
//		msg := cli.Group(root, "messages", "Read and search messages")
//		cli.Command(msg, "search [query]", "Search messages", search, cli.Formats(cli.JSON, cli.JSONL, cli.Text))
//		lifecycle.Run(id, func(ctx context.Context, _ *slog.Logger) error {
//			return cli.Execute(ctx, root, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
//		}, lifecycle.Heartbeat(false))
//	}
//
// The second argument of Command is the Use line: the name, then the argument synopsis. stdout
// carries the result and nothing else. Help, the error envelope and the app's prompts and
// progress go to stderr; the runner reaches the streams through Stdin, Stderr and ReadSecret,
// its flags through Flags and its own path through CommandPath, all from ctx. Log lines go
// where logging sends them (section 5).
package cli

import (
	"context"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/AndreiD108/d108-go/doctor"
	"github.com/AndreiD108/d108-go/identity"
)

// FormatFlag is the name of the persistent output flag.
const FormatFlag = "format"

// Option configures Root.
type Option func(*cobra.Command)

// Short sets the one-line description shown at the top of the root's help.
func Short(text string) Option { return func(c *cobra.Command) { c.Short = text } }

// Long sets the description shown by `tool --help` instead of Short.
func Long(text string) Option { return func(c *cobra.Command) { c.Long = text } }

// The root's Annotations keys: the identity (Cobra's context is replaced on every Execute, so
// it lives in the strings Annotations hold) and the mark of a tree that ran.
const (
	annName     = "cli.identity.name"
	annVersion  = "cli.identity.version"
	annInstance = "cli.identity.instance_id"
	annPID      = "cli.identity.pid"
	annPrefix   = "cli.identity.prefix"
	annExecuted = "cli.executed"
	annHelp     = "cli.help"
)

// Root makes the root command of a tool from its identity: the tool's name is id.Name. It
// carries the persistent --format flag, the doctor subcommand and the help command; Cobra's
// completion command is off. Run with no subcommand is a usage error that lists the commands,
// and so is `help` with a command that does not exist.
func Root(id identity.Identity, opts ...Option) *cobra.Command {
	root := &cobra.Command{
		Use:               id.Name,
		SilenceErrors:     true,
		SilenceUsage:      true,
		DisableAutoGenTag: true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		Args:              cobra.ArbitraryArgs,
		RunE:              needsSubcommand,
		Annotations: map[string]string{
			annName: id.Name, annVersion: id.Version, annInstance: id.InstanceID,
			annPID: strconv.Itoa(id.PID), annPrefix: id.Prefix,
		},
	}
	root.SetUsageTemplate(usageTemplate)
	root.SetFlagErrorFunc(flagError)
	root.SetHelpCommand(helpCommand())
	root.PersistentFlags().String(FormatFlag, "", "output format; each command's help lists the formats it accepts, the first is its default")
	for _, opt := range opts {
		opt(root)
	}
	Command(root, "doctor", "Report where the logs go and why, the level, the agent, tracing and the settings",
		func(ctx context.Context, _ []string, out Output) error { return runDoctor(ctx, id, out) },
		Formats(Text, JSON))
	return root
}

// IdentityOf returns the identity the tree's root was made with; the zero Identity for a
// tree not made by Root.
func IdentityOf(cmd *cobra.Command) identity.Identity {
	a := cmd.Root().Annotations
	pid, _ := strconv.Atoi(a[annPID])
	return identity.Identity{Name: a[annName], Version: a[annVersion], InstanceID: a[annInstance], PID: pid, Prefix: a[annPrefix]}
}

// Group makes a command that only holds subcommands, at any depth. Run without one of them it
// is a usage error that lists them.
func Group(parent *cobra.Command, name, short string) *cobra.Command {
	g := &cobra.Command{Use: name, Short: short, Args: cobra.ArbitraryArgs, RunE: needsSubcommand}
	parent.AddCommand(g)
	return g
}

// needsSubcommand is the RunE of the root and of groups: a usage error naming the subcommands.
func needsSubcommand(cmd *cobra.Command, args []string) error {
	return incomplete(cmd, args)
}

// helpCommand replaces Cobra's help command, which answers an unknown topic with "Unknown
// help topic" and exit 0. This one writes the help of the named command to stderr; a name
// that is not a subcommand of the deepest command found is the usage error needsSubcommand
// gives for it, with details.commands.
func helpCommand() *cobra.Command {
	return &cobra.Command{
		Use:         "help [command]",
		Short:       "Help about any command",
		Annotations: map[string]string{annHelp: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			target, rest, err := cmd.Root().Find(args)
			if err != nil || target == nil {
				target, rest = cmd.Root(), args
			}
			if len(rest) > 0 && target.HasSubCommands() {
				return incomplete(target, rest)
			}
			return target.Help()
		},
	}
}

// pathOf returns the command's path below the root ("messages search"), "" for the root.
func pathOf(cmd *cobra.Command) string {
	return strings.TrimPrefix(strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()), " ")
}

// runDoctor is the doctor leaf: the report as text or as JSON.
func runDoctor(ctx context.Context, id identity.Identity, out Output) error {
	report := doctor.Build(ctx, id)
	if out.Format() == JSON {
		return out.JSON(report)
	}
	return out.Text("%s", report.Text())
}

// usageTemplate is Cobra's default with a Formats block after the usage lines.
const usageTemplate = `Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{with index .Annotations "cli.formats"}}

Formats:
  {{.}}{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

Available Commands:{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

Additional Commands:{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`
