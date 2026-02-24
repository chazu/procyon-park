// pp - Procyon Park CLI
// Loads the embedded Maggie image and runs the ProcyonPark entry point.
// Supports subcommands: 'pp daemon run|stop|status'.
package main

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/chazu/maggie/compiler"
	"github.com/chazu/maggie/vm"
)

//go:embed procyon-park.image
var embeddedImage []byte

func main() {
	os.Exit(run(os.Args[1:]))
}

// run is the real entry point, returning an exit code for testability.
func run(args []string) int {
	// Check for subcommands before flag parsing
	if len(args) > 0 {
		switch args[0] {
		case "daemon":
			return handleDaemon(args[1:])
		case "--version", "-version", "version":
			fmt.Println("pp (procyon-park) v0.1.0")
			return 0
		case "--help", "-help", "help":
			printUsage()
			return 0
		}
	}

	// Default: run the embedded Maggie image
	verbose := false
	for _, arg := range args {
		switch arg {
		case "-v", "--verbose":
			verbose = true
		case "--version", "-version":
			fmt.Println("pp (procyon-park) v0.1.0")
			return 0
		}
	}

	return runImage(verbose)
}

// runImage loads and executes the embedded Maggie image.
func runImage(verbose bool) int {
	vmInst := vm.NewVM()
	if err := vmInst.LoadImageFromBytes(embeddedImage); err != nil {
		fmt.Fprintf(os.Stderr, "Error loading image: %v\n", err)
		return 1
	}
	vmInst.ReRegisterNilPrimitives()
	vmInst.ReRegisterBooleanPrimitives()
	vmInst.UseGoCompiler(compiler.Compile)

	if verbose {
		fmt.Printf("Loaded image (%d bytes)\n", len(embeddedImage))
	}

	// Run the entry point: ProcyonPark::Main.start
	class := vmInst.Classes.Lookup("Main")
	if class == nil {
		class = vmInst.Classes.LookupInNamespace("ProcyonPark", "Main")
	}
	if class == nil {
		fmt.Fprintf(os.Stderr, "Error: ProcyonPark::Main class not found in image\n")
		return 1
	}

	classValue := vmInst.Symbols.SymbolValue("Main")
	result := vmInst.Send(classValue, "start", nil)

	if verbose {
		if vm.IsStringValue(result) {
			fmt.Println(vmInst.Registry().GetStringContent(result))
		}
	}

	return 0
}

// printUsage prints the top-level help text.
func printUsage() {
	fmt.Print(`Usage: pp [command] [options]

Commands:
  daemon    Manage the background daemon
  help      Show this help message
  version   Print version and exit

Run without a command to execute the embedded Maggie image.

Options:
  -v, --verbose   Verbose output
  --version       Print version and exit
  --help          Show this help message

Daemon subcommands:
  pp daemon run      Start the daemon in the foreground
  pp daemon stop     Stop the running daemon
  pp daemon status   Check if the daemon is running
`)
}
