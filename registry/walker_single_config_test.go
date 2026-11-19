// The fixtures below name @facet: tags belonging to other files, so this file
// opts itself out of anchor scanning: @facet-ignore-file
package facet

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Count configuration sites in source: behaviour of one walker cannot prove
// that all walkers share it. A copied configuration can silently skip anchors
// and produce plausible but contradictory repository counts.
func TestTheWalkerIsConfiguredInExactlyOnePlace(t *testing.T) {

	options := []string{
		"IgnoreGitIgnore",
		"IgnoreIgnoreFile",
		"CustomIgnore",
		"IncludeHidden",
		"ExcludeDirectory",
	}

	// Audit every production Go file in this module. Walking from the module
	// root covers both Core and its in-module consumers without assuming that
	// any consumer lives beside the registry package.
	root := SourceTestPath(t)
	var sources []string
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "vendor" || entry.Name() == "node_modules" || entry.Name() == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
			sources = append(sources, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	type site struct {
		file string
		line int
		text string
	}
	var constructors, configured []site

	for _, path := range sources {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			code := line
			// Mentions in comments do not configure a walker.
			if idx := strings.Index(code, "//"); idx >= 0 {
				code = code[:idx]
			}
			here := site{file: path, line: i + 1, text: strings.TrimSpace(line)}
			if strings.Contains(code, "gocodewalker.NewFileWalker(") {
				constructors = append(constructors, here)
			}
			for _, opt := range options {
				if strings.Contains(code, "."+opt+" =") {
					configured = append(configured, here)
					break
				}
			}
		}
	}

	// Both counts are checked. A walker constructed without setting anything
	// yields a second answer, and so does setting options on a walker
	// constructed elsewhere; either check alone would let the other through.
	if len(constructors) != 1 {
		t.Errorf("a walker is constructed in %d places, want 1 (NewSourceWalker): %+v", len(constructors), constructors)
	}
	for _, s := range configured {
		if !strings.HasSuffix(s.file, "scan.go") {
			t.Errorf("%s:%d configures a walker outside NewSourceWalker: %s", s.file, s.line, s.text)
		}
	}

	for _, opt := range options {
		found := false
		for _, s := range configured {
			if strings.Contains(s.text, "."+opt+" =") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no walker sets %s — the shared walker must carry every option the scan depends on", opt)
		}
	}
}
