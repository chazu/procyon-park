package main

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/chazu/maggie/compiler"
	"github.com/chazu/maggie/vm"
	"github.com/chazu/procyon-park/internal/cli"
	"github.com/spf13/cobra"
)

//go:embed procyon-park.image
var embeddedImage []byte

func init() {
	cli.AddCommand(runImageCmd)
}

// runImageCmd loads and executes the embedded Maggie image.
var runImageCmd = &cobra.Command{
	Use:   "run-image",
	Short: "Run the embedded Maggie image",
	RunE: func(cmd *cobra.Command, args []string) error {
		vmInst := vm.NewVM()
		if err := vmInst.LoadImageFromBytes(embeddedImage); err != nil {
			return cli.NewExitErr(cli.ExitError, fmt.Errorf("load image: %w", err))
		}
		vmInst.ReRegisterNilPrimitives()
		vmInst.ReRegisterBooleanPrimitives()
		vmInst.UseGoCompiler(compiler.Compile)

		if cli.Verbose() {
			fmt.Printf("Loaded image (%d bytes)\n", len(embeddedImage))
		}

		class := vmInst.Classes.Lookup("Main")
		if class == nil {
			class = vmInst.Classes.LookupInNamespace("ProcyonPark", "Main")
		}
		if class == nil {
			return cli.NewExitErr(cli.ExitNotFound,
				fmt.Errorf("ProcyonPark::Main class not found in image"))
		}

		classValue := vmInst.Symbols.SymbolValue("Main")
		result := vmInst.Send(classValue, "start", nil)

		if cli.Verbose() {
			if vm.IsStringValue(result) {
				fmt.Println(vmInst.Registry().GetStringContent(result))
			}
		}

		return nil
	},
}

// checkImageEmbed verifies the image is available (for tests).
func checkImageEmbed() bool {
	return len(embeddedImage) > 0
}

// Ensure stderr is available.
var _ = os.Stderr
