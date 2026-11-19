// Fixtures here contain example @facet: tags; the file exercises a command
// rather than implementing a feature, so it opts out of scanning.
// @facet-ignore-file

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// calibrateFixture writes a registry that is outside every band at once: one
// domain, one capability, and records carrying a single scenario each.
func calibrateFixture(t *testing.T, n int) string {
	t.Helper()
	root := t.TempDir()
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("everything/everything/feature-%d", i)
		write(t, filepath.Join(root, "docs/facets/features/everything/everything", fmt.Sprintf("feature-%d.yaml", i)),
			"schema_version: \"0.1\"\nid: "+id+"\nlifecycle: active\n"+
				"intent:\n  summary: |\n    Does something a customer can name.\n"+
				"contract:\n  scenarios:\n    - id: only-one\n      statement: |\n"+
				"        Given a customer\n        When they act\n        Then something happens\n"+
				"lineage:\n  owner:\n    team: billing\n  created_at: \"2026-09-11\"\n")
		write(t, filepath.Join(root, fmt.Sprintf("src/feature-%d.ts", i)),
			"// @facet: "+id+"\n// @facet-role: primary\nexport function run() {}\n")
	}
	return root
}

// runCalibrate executes the real command through the same cobra wiring the
// binary uses, and returns stdout together with the error the process would
// have exited on.
func runCalibrate(t *testing.T, args ...string) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	root := newRootCmd()
	root.SetArgs(append([]string{"calibrate"}, args...))
	runErr := root.Execute()

	w.Close()
	os.Stdout = old
	out, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(out), runErr
}

// calibration-never-fails-a-run. A registry outside every band at once still
// exits zero: a report that could fail a build would be answered by tuning the
// band rather than by moving the records, which is the one outcome this command
// must not produce.
func TestCalibrateNeverFailsARun(t *testing.T) {
	root := calibrateFixture(t, 20)

	out, err := runCalibrate(t, "--root", root)
	if err != nil {
		t.Fatalf("calibrate must exit zero whatever the shape: %v\n%s", err, out)
	}
	if !strings.Contains(out, "scenario granularity") {
		t.Fatalf("the fixture is outside every band — want a reading, got:\n%s", out)
	}

	jsonOut, jsonErr := runCalibrate(t, "--root", root, "--json")
	if jsonErr != nil {
		t.Fatalf("--json must exit zero too: %v\n%s", jsonErr, jsonOut)
	}
	var report calibrateReport
	if decErr := json.Unmarshal([]byte(jsonOut), &report); decErr != nil {
		t.Fatalf("--json must emit parseable JSON: %v\n%s", decErr, jsonOut)
	}
	if len(report.Registries) != 1 {
		t.Fatalf("want one registry in the report, got %d", len(report.Registries))
	}
	if report.Registries[0].Readings == nil {
		t.Fatal("readings must be an array on the wire, never null")
	}
}
