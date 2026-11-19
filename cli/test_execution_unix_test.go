//go:build !windows

// @facet: registry/integrity/runner-test-execution
// @facet-role: related
package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Named mutation: remove signal.NotifyContext from the CLI. SIGINT would exit
// before cleanup, leaving the user's old report stranded in its backup file.
func TestBinaryExecutionInterruptRestoresOldReport(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "tests"), 0755)
	os.WriteFile(filepath.Join(root, "tests/a.go"), []byte("package tests"), 0644)
	executionCLIRecord(t, root, "sample", "tests/a.go")
	config := `runners:
  local:
    list: "printf 'tests/a.go\n'"
    command: touch entered; sleep 2 & wait
    report: junit
    report_file: report.xml
`
	os.WriteFile(filepath.Join(root, ".facetrc.yaml"), []byte(config), 0644)
	old := []byte("previous report")
	path := filepath.Join(root, "report.xml")
	os.WriteFile(path, old, 0600)
	cmd := exec.Command(facetBinary(t), "--root", root, "test", "--only", "facet")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "entered")); err == nil {
			break
		}
		select {
		case <-deadline:
			cmd.Process.Kill()
			t.Fatal("command never entered")
		case <-time.After(5 * time.Millisecond):
		}
	}
	started := time.Now()
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("interrupted binary passed")
	}
	if elapsed := time.Since(started); elapsed > 750*time.Millisecond {
		t.Fatalf("interrupt waited %s", elapsed)
	}
	restored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(restored, old) {
		t.Fatal("interrupt stranded previous report")
	}
	backups, _ := filepath.Glob(filepath.Join(root, ".facet-report-previous-*"))
	if len(backups) > 0 {
		t.Fatalf("interrupt left backups: %v", backups)
	}
}

// Named mutation: normalize signal termination to exit 1. The actual runner's
// SIGTERM termination must retain the shell convention exit 143.
func TestBinaryExecutionPreservesSignalExit(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "tests"), 0755)
	os.WriteFile(filepath.Join(root, "tests/a.go"), []byte("package tests"), 0644)
	executionCLIRecord(t, root, "sample", "tests/a.go")
	os.WriteFile(filepath.Join(root, ".facetrc.yaml"), []byte("runners:\n  local:\n    list: printf 'tests/a.go\\n'\n    command: kill -TERM $$\n"), 0644)
	_, _, code := executionCLI(t, root, "--only", "runner")
	if code != 143 {
		t.Fatalf("signal exit became %d", code)
	}
}
