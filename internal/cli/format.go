package cli

import (
	"os"
	"path/filepath"

	"github.com/chazu/procyon-park/internal/output"
)

// OutputFormat returns the resolved output format based on the --output flag
// and TTY detection.
func OutputFormat() (output.Format, error) {
	return output.ResolveFormat(flagOutput, os.Stdout)
}

// DataDir returns the data directory. If --socket was set, derive from socket
// path; otherwise return the default ~/.procyon-park.
func DataDir() string {
	if flagSocket != "" {
		// Derive data dir from socket path parent.
		return filepath.Dir(flagSocket)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".procyon-park"
	}
	return filepath.Join(home, ".procyon-park")
}
