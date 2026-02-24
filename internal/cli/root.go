package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// Global flag values, set by persistent flags on the root command.
var (
	flagSocket  string
	flagOutput  string
	flagQuiet   bool
	flagVerbose bool
	flagNoColor bool
)

// Version is set at build time.
var Version = "dev"

// rootCmd is the top-level command for pp.
var rootCmd = &cobra.Command{
	Use:     "pp",
	Short:   "Procyon Park — stigmergic multi-agent orchestrator",
	Long:    "pp is the CLI for Procyon Park, a stigmergic multi-agent system.",
	Version: Version,
	// Silence Cobra's built-in error/usage printing so we control output.
	SilenceErrors: true,
	SilenceUsage:  true,
	// Root with no subcommand prints help.
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVar(&flagSocket, "socket", "", "daemon Unix socket path (default: ~/.procyon-park/daemon.sock)")
	pf.StringVarP(&flagOutput, "output", "o", "text", "output format: text or json")
	pf.BoolVarP(&flagQuiet, "quiet", "q", false, "suppress non-essential output")
	pf.BoolVarP(&flagVerbose, "verbose", "v", false, "verbose output")
	pf.BoolVar(&flagNoColor, "no-color", false, "disable colored output")
}

// Execute runs the root command and returns a process exit code.
func Execute() int {
	if err := rootCmd.Execute(); err != nil {
		// Map known error types to semantic exit codes.
		code := exitCodeFromError(err)
		if code == ExitUsage {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			rootCmd.Usage() //nolint:errcheck
		} else if !flagQuiet {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		return code
	}
	return ExitSuccess
}

// AddCommand registers a subcommand on the root command.
func AddCommand(cmds ...*cobra.Command) {
	rootCmd.AddCommand(cmds...)
}

// SocketPath returns the resolved daemon socket path. If --socket was not
// set, it returns the default path under ~/.procyon-park/.
func SocketPath() string {
	if flagSocket != "" {
		return flagSocket
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".procyon-park", "daemon.sock")
	}
	return filepath.Join(home, ".procyon-park", "daemon.sock")
}

// EnsureDaemon connects to the daemon, starting it if necessary.
func EnsureDaemon() error {
	return ensureDaemon(SocketPath())
}

// Quiet returns true if --quiet is set.
func Quiet() bool { return flagQuiet }

// Verbose returns true if --verbose is set.
func Verbose() bool { return flagVerbose }

// OutputJSON returns true if --output=json.
func OutputJSON() bool { return flagOutput == "json" }

// NoColor returns true if --no-color is set.
func NoColor() bool { return flagNoColor }

// exitCodeFromError maps an error to a semantic exit code.
func exitCodeFromError(err error) int {
	if err == nil {
		return ExitSuccess
	}
	// Check for our ExitError type.
	if ee, ok := err.(*ExitErr); ok {
		return ee.Code
	}
	return ExitError
}

// ExitErr wraps an error with a specific exit code.
type ExitErr struct {
	Code int
	Err  error
}

func (e *ExitErr) Error() string { return e.Err.Error() }
func (e *ExitErr) Unwrap() error { return e.Err }

// NewExitErr creates an ExitErr with the given code and error.
func NewExitErr(code int, err error) *ExitErr {
	return &ExitErr{Code: code, Err: err}
}

// ExecuteArgs sets the command arguments and runs Execute. This is used for
// testing and backward-compatible dispatch.
func ExecuteArgs(args []string) int {
	rootCmd.SetArgs(args)
	return Execute()
}
