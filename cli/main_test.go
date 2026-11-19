package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain removes the directories the binary builders create.
func TestMain(m *testing.M) {
	// Fixtures must not inherit a repository from above the OS temporary root.
	ceiling := os.TempDir()
	if existing := os.Getenv("GIT_CEILING_DIRECTORIES"); existing != "" {
		ceiling += string(os.PathListSeparator) + existing
	}
	if err := os.Setenv("GIT_CEILING_DIRECTORIES", ceiling); err != nil {
		panic(err)
	}
	code := m.Run()
	for _, bin := range []string{facetBinaryPath, versionedPath} {
		if bin != "" {
			os.RemoveAll(filepath.Dir(bin))
		}
	}
	os.Exit(code)
}
