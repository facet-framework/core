// Command facet is the FACET Core CLI: the registry, linter, hooks and MCP
// server, without a browser application or embedded front-end assets.
package main

import (
	"os"

	"github.com/facet-framework/core/cli"
)

// version is set by -ldflags -X main.version.
var version = "dev"

func main() {
	os.Exit(cli.Run(cli.NewCommand(cli.Options{Version: version})))
}
