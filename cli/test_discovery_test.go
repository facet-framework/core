// @facet: registry/integrity/runner-test-discovery
// @facet-role: related
package cli

import (
	"encoding/json"
	"github.com/facet-framework/core/registry"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Named mutation: remove newTestCmd from the root command registration. The
// binary still builds; the real user entry point stops discovering test files.
// Named mutation: execute Runner.Command during discovery. The marker appears.
func TestBinaryTestDiscoveryChecksDriftWithoutExecutingTests(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "tests"), 0755)
	os.WriteFile(filepath.Join(root, "tests/a.go"), []byte("package tests"), 0644)
	os.WriteFile(filepath.Join(root, ".facetrc.yaml"), []byte("runners:\n  go:\n    command: touch executed\n    list: printf 'tests/a.go\\n'\n"), 0644)
	run := func(flags ...string) ([]facet.TestDiscovery, error) {
		args := append([]string{"--root", root, "test", "--json"}, flags...)
		cmd := exec.Command(facetBinary(t), args...)
		output, err := cmd.Output()
		var reports []facet.TestDiscovery
		if e := json.Unmarshal(output, &reports); e != nil {
			t.Fatalf("invalid JSON %q: %v (exit %v)", output, e, err)
		}
		return reports, err
	}
	reports, err := run("--check")
	if err == nil || len(reports) != 1 || !reports[0].Failed() {
		t.Fatalf("ratchet passed: %+v %v", reports, err)
	}
	if _, err := os.Stat(filepath.Join(root, "executed")); !os.IsNotExist(err) {
		t.Fatal("discovery executed the test command")
	}
	os.MkdirAll(filepath.Join(root, "docs/facets"), 0755)
	os.WriteFile(filepath.Join(root, "docs/facets/test-baseline.yaml"), []byte("runners:\n  go: [tests/a.go]\n"), 0644)
	reports, err = run("--list")
	if err != nil || reports[0].Failed() || len(reports[0].Runners[0].Files) != 1 {
		t.Fatalf("baseline rejected: %+v %v", reports, err)
	}
	bare := exec.Command(facetBinary(t), "--root", root, "test")
	if err := bare.Run(); err != nil {
		t.Fatalf("bare test execution failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "executed")); err != nil {
		t.Fatal("bare test did not execute its command")
	}
}

// Named mutation: remove the baseline transaction lock. Two independently
// invoked binaries then revive the stale path removed by the other process.
func TestBinaryBaselineUpdatesCannotReintroduceRemovedPaths(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "tests"), 0755)
	os.MkdirAll(filepath.Join(root, "docs/facets"), 0755)
	for _, name := range []string{"a.go", "b.go"} {
		os.WriteFile(filepath.Join(root, "tests", name), []byte("package tests"), 0644)
	}
	config := `runners:
  first:
    command: true
    list: "touch first-entered; while [ ! -f release-first ]; do sleep 0.01; done; printf 'tests/a.go\\n'"
  second:
    command: true
    list: "touch second-entered; printf 'tests/b.go\\n'"
`
	os.WriteFile(filepath.Join(root, ".facetrc.yaml"), []byte(config), 0644)
	baselinePath := filepath.Join(root, "docs/facets/test-baseline.yaml")
	os.WriteFile(baselinePath, []byte("runners:\n  first: [tests/a.go, tests/stale-first.go]\n  second: [tests/b.go, tests/stale-second.go]\n"), 0600)
	binary := facetBinary(t)
	first := exec.Command(binary, "--root", root, "test", "--check", "--runner", "first", "--update-baseline")
	second := exec.Command(binary, "--root", root, "test", "--check", "--runner", "second", "--update-baseline")
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { os.WriteFile(filepath.Join(root, "release-first"), nil, 0644) }()
	deadline := time.After(2 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "first-entered")); err == nil {
			break
		}
		select {
		case <-deadline:
			first.Process.Kill()
			t.Fatal("first binary never entered discovery")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := second.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(root, "second-entered")); !os.IsNotExist(err) {
		t.Error("second binary read the baseline before first committed")
	}
	os.WriteFile(filepath.Join(root, "release-first"), nil, 0644)
	if err := first.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := second.Wait(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(baselinePath)
	if err != nil || strings.Contains(string(data), "stale-") {
		t.Fatalf("removed baseline paths reintroduced: %s %v", data, err)
	}
	if !strings.Contains(string(data), "tests/a.go") || !strings.Contains(string(data), "tests/b.go") {
		t.Fatalf("live baseline paths lost: %s", data)
	}
}
