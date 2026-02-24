// pp - Procyon Park CLI
// Uses Cobra for command routing with JSON-RPC IPC to the daemon.
package main

import (
	"os"

	"github.com/chazu/procyon-park/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
