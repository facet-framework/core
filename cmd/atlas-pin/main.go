// Command atlas-pin converts release metadata for scripts/atlas-pin.sh.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/facet-framework/core/internal/atlaslauncher"
)

func main() {
	unsigned := flag.Bool("unsigned", false, "convert attested legacy metadata")
	flag.Parse()
	if flag.NArg() != 3 {
		fmt.Fprintln(os.Stderr, "usage: atlas-pin [-unsigned] version manifest signature")
		os.Exit(2)
	}
	if err := run(flag.Arg(0), flag.Arg(1), flag.Arg(2), *unsigned); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(version, manifestPath, signaturePath string, unsigned bool) error {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var sig []byte
	if !unsigned {
		sig, err = os.ReadFile(signaturePath)
		if err != nil {
			return err
		}
	}
	pin, err := atlaslauncher.PinJSONForRelease(data, sig, version, unsigned)
	if err != nil {
		return err
	}
	_, err = fmt.Println(string(pin))
	return err
}
