//go:build !windows

// @facet-ignore-file
package facet

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Named mutation: use os.WriteFile on the baseline instead of publishing a
// complete replacement. RLIMIT_FSIZE makes that truncate the reviewed file.
func TestBaselineFailedWritePreservesPreviousContent(t *testing.T) {
	if os.Getenv("FACET_BASELINE_WRITE_CHILD") == "1" {
		signal.Ignore(syscall.SIGXFSZ)
		limit := syscall.Rlimit{Cur: 64, Max: 64}
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(os.Getenv("FACET_BASELINE_WRITE_ROOT"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = CheckTests(context.Background(), cfg, cfg.All()[0], "", true); err == nil {
			t.Fatal("limited write unexpectedly succeeded")
		}
		return
	}
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "tests"), 0755)
	os.MkdirAll(filepath.Join(root, "docs/facets"), 0755)
	entries := []string{}
	for i := range 20 {
		name := "tests/" + strings.Repeat("a", i+1) + ".go"
		os.WriteFile(filepath.Join(root, name), []byte("package tests"), 0644)
		entries = append(entries, name)
	}
	config := "runners:\n  local:\n    command: true\n    list: printf '%s\\n' " + strings.Join(entries, " ") + "\n"
	os.WriteFile(filepath.Join(root, ConfigName), []byte(config), 0644)
	before := []byte("runners:\n  local:\n    - tests/stale.go\n    - " + strings.Join(entries, "\n    - ") + "\n")
	path := filepath.Join(root, "docs/facets/test-baseline.yaml")
	os.WriteFile(path, before, 0600)
	cmd := exec.Command(os.Args[0], "-test.run=^TestBaselineFailedWritePreservesPreviousContent$")
	cmd.Env = append(os.Environ(), "FACET_BASELINE_WRITE_CHILD=1", "FACET_BASELINE_WRITE_ROOT="+root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("write failure child: %v %s", err, output)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed update destroyed baseline: before=%d after=%d err=%v", len(before), len(after), err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(root, "docs/facets/.facet-test-baseline-*"))
	if len(leftovers) > 0 {
		t.Fatalf("failed write leaked temporary files: %v", leftovers)
	}
}
