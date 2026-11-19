//go:build ignore

// gen writes payload.txtar from the canonical files of the module this
// package is built in. Run it through 'go generate' in this directory.
package main

import (
	"fmt"
	"os"

	"github.com/facet-framework/core/cli/scaffold"
)

func main() {
	root, err := scaffold.FindModuleRoot(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	data, err := scaffold.Generate(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile("payload.txtar", data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
