package cli

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newRootCmd() *cobra.Command { return NewCommand(Options{}) }

// Capture the package's initial directory before tests temporarily change it.
// Runtime source paths cannot locate fixtures in binaries built with -trimpath.
var cliFixtureRoot, cliFixtureRootErr = findCLIModuleRoot()

func findCLIModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no module root above CLI test directory")
		}
		dir = parent
	}
}

// cliModuleRoot returns the module root captured before any test changed the
// working directory.
func cliModuleRoot(t *testing.T) string {
	t.Helper()
	if cliFixtureRootErr != nil {
		t.Fatal(cliFixtureRootErr)
	}
	return cliFixtureRoot
}

// cliCommandPackage returns the directory of the Core command: the package
// under cmd/ whose main.go imports nothing outside the standard library except
// this CLI package. A module may hold further commands that wrap the CLI with
// more dependencies; the tests that build or inspect "the Core command" must
// pick the one that adds nothing, whatever its directory is called.
func cliCommandPackage(t *testing.T) string {
	t.Helper()
	cmdDir := filepath.Join(cliModuleRoot(t), "cmd")
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		t.Fatalf("reading %s: %v", cmdDir, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		mainFile := filepath.Join(cmdDir, entry.Name(), "main.go")
		parsed, err := parser.ParseFile(token.NewFileSet(), mainFile, nil, parser.ImportsOnly)
		if err != nil {
			continue
		}
		var nonStandard []string
		for _, spec := range parsed.Imports {
			importPath, _ := strconv.Unquote(spec.Path.Value)
			if strings.Contains(strings.SplitN(importPath, "/", 2)[0], ".") {
				nonStandard = append(nonStandard, importPath)
			}
		}
		if len(nonStandard) == 1 && path.Base(nonStandard[0]) == "cli" {
			return filepath.Join(cmdDir, entry.Name())
		}
	}
	t.Fatalf("no command under %s imports only the CLI package", cmdDir)
	return ""
}
