// Package cli implements the hunch command line.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Exit codes.
const (
	exitKept     = 0 // at least one record kept
	exitNoneKept = 1 // no records kept
	exitUsage    = 2 // usage or config error
	exitAuth     = 3 // missing or rejected API key
	exitAPI      = 4 // API error after retries
	exitPartial  = 5 // some records failed (--keep-going)
)

// exitError carries an exit code out of a command.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func usageErrorf(format string, args ...any) error {
	return &exitError{exitUsage, fmt.Errorf(format, args...)}
}

// silentExit ends the command with a code and no message.
type silentExit int

func (e silentExit) Error() string { return fmt.Sprintf("exit %d", int(e)) }

// env is the process environment the CLI runs in, so tests can substitute it.
type env struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	getenv func(string) string
	// forceInteractive lets tests drive prompts without a terminal.
	forceInteractive bool
}

func (e *env) isTerminal(v any) bool {
	f, ok := v.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// interactive reports whether stdin is a terminal we can prompt on.
func (e *env) interactive() bool {
	return e.forceInteractive || e.isTerminal(e.stdin)
}

func (e *env) color(w io.Writer) bool {
	return e.isTerminal(w) && e.getenv("NO_COLOR") == "" && e.getenv("TERM") != "dumb"
}

// Main runs hunch and returns its exit code.
func Main(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return run(ctx, args, &env{stdin: stdin, stdout: stdout, stderr: stderr, getenv: os.Getenv})
}

func run(ctx context.Context, args []string, e *env) int {
	cfg, err := loadConfig(e)
	if err != nil {
		fmt.Fprintf(e.stderr, "hunch: %v\n", err)
		return exitUsage
	}
	root := newRootCmd(e, cfg)
	root.SetArgs(args)
	root.SetIn(e.stdin)
	root.SetOut(e.stdout)
	root.SetErr(e.stderr)
	err = root.ExecuteContext(ctx)
	if err == nil {
		return exitKept
	}
	var se silentExit
	if errors.As(err, &se) {
		return int(se)
	}
	fmt.Fprintf(e.stderr, "hunch: %v\n", err)
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	return exitUsage
}

func newRootCmd(e *env, cfg *config) *cobra.Command {
	o := newOptions(cfg, e)
	root := &cobra.Command{
		Use:   "hunch [flags] <question> [file...]",
		Short: "Ask fast, typed judgments about each input record",
		Long: `hunch is a Unix filter that asks TypeSafe's System One model a typed question
about each input record and emits JSONL your next command can filter, sort or
route. One record in, one annotated record out.

With no subcommand, hunch asks a yes/no question (same as "hunch yes").
"hunch @name" runs a question set saved as a preset in the config file.`,
		Example: `  kubectl logs api-7f | hunch "Is this an authentication failure?" --min 0.8 --raw
  hunch pick "Which team?" -c billing,technical,sales --jsonpath '$.body' < tickets.jsonl
  hunch score "How frustrated?" -l calm,frustrated,furious --top 10 reviews.txt
  git diff --cached | hunch --in whole "Does this add secrets?" --max 0.3 >/dev/null`,
		Args:              cobra.ArbitraryArgs,
		SilenceUsage:      true,
		SilenceErrors:     true,
		Version:           version(),
		CompletionOptions: cobra.CompletionOptions{HiddenDefaultCmd: true},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				_ = cmd.Help()
				return silentExit(exitUsage)
			}
			if name, ok := strings.CutPrefix(args[0], "@"); ok {
				p, ok := cfg.Presets[name]
				if !ok {
					return usageErrorf("no preset %q in %s", name, cfg.path)
				}
				if err := o.resolve(cmd); err != nil {
					return err
				}
				s, err := p.spec(o)
				if err != nil {
					return &exitError{exitUsage, fmt.Errorf("preset %q: %w", name, err)}
				}
				return execute(cmd.Context(), e, o, s, args[1:])
			}
			return runYes(cmd, e, o, args)
		},
	}
	root.SetVersionTemplate("hunch {{.Version}}\n")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &exitError{exitUsage, err}
	})
	addIOFlags(root, o)
	addRangeFlags(root, o)
	addNoulFlags(root, o)

	root.AddCommand(
		newYesCmd(e, o),
		newPickCmd(e, o),
		newScoreCmd(e, o),
		newAskCmd(e, o),
		newModelsCmd(e, o),
		newAuthCmd(e, o),
		newVersionCmd(e),
	)
	return root
}
