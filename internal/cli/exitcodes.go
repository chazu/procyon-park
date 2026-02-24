// Package cli provides the Cobra-based command-line interface for procyon-park.
package cli

// Semantic exit codes for the pp CLI.
const (
	ExitSuccess     = 0   // Command completed successfully.
	ExitError       = 1   // General error.
	ExitUsage       = 2   // Invalid usage (bad flags, missing args).
	ExitConnection  = 3   // Cannot connect to daemon.
	ExitTimeout     = 4   // Operation timed out.
	ExitNotFound    = 5   // Requested resource not found.
	ExitInterrupted = 130 // Interrupted by signal (128 + SIGINT=2).
)
