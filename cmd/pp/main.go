// pp - Procyon Park CLI
// Loads the embedded Maggie image and runs the ProcyonPark entry point.
package main

import (
	_ "embed"
	"flag"
	"fmt"
	"os"

	"github.com/chazu/maggie/compiler"
	"github.com/chazu/maggie/vm"
)

//go:embed procyon-park.image
var embeddedImage []byte

func main() {
	verbose := flag.Bool("v", false, "Verbose output")
	version := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *version {
		fmt.Println("pp (procyon-park) v0.1.0")
		os.Exit(0)
	}

	// Create VM and load embedded image
	vmInst := vm.NewVM()
	if err := vmInst.LoadImageFromBytes(embeddedImage); err != nil {
		fmt.Fprintf(os.Stderr, "Error loading image: %v\n", err)
		os.Exit(1)
	}
	vmInst.ReRegisterNilPrimitives()
	vmInst.ReRegisterBooleanPrimitives()
	vmInst.UseGoCompiler(compiler.Compile)

	if *verbose {
		fmt.Printf("Loaded image (%d bytes)\n", len(embeddedImage))
	}

	// Run the entry point: ProcyonPark::Main.start
	class := vmInst.Classes.Lookup("Main")
	if class == nil {
		class = vmInst.Classes.LookupInNamespace("ProcyonPark", "Main")
	}
	if class == nil {
		fmt.Fprintf(os.Stderr, "Error: ProcyonPark::Main class not found in image\n")
		os.Exit(1)
	}

	classValue := vmInst.Symbols.SymbolValue("Main")
	result := vmInst.Send(classValue, "start", nil)

	if *verbose {
		if vm.IsStringValue(result) {
			fmt.Println(vmInst.Registry().GetStringContent(result))
		}
	}
}
