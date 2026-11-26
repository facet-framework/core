// Fixtures below contain example @facet: tags; this file demonstrates the
// syntax rather than implementing a feature.
// @facet-ignore-file

package cli

import (
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/facet-framework/core/registry"
)

// The real binary exits 0 on a run whose only findings are shared-claims
// info, and 1 once an error stands beside them, with the info still reported.
func TestLintExitsZeroOnSharedClaimsAlone(t *testing.T) {
	bin := facetBinary(t)
	root := t.TempDir()
	record := func(id string) string {
		return "schema_version: \"0.1\"\nid: " + id + "\nlifecycle: active\ncontract:\n  scenarios:\n    - id: basic\n      statement: |\n        Given two integers\n        When one is subtracted from the other\n        Then the difference is returned\n"
	}
	writeFile(t, filepath.Join(root, "docs/facets/features/arithmetic/calculation/difference.yaml"), record("arithmetic/calculation/difference"))
	writeFile(t, filepath.Join(root, "docs/facets/features/arithmetic/calculation/subtraction.yaml"), record("arithmetic/calculation/subtraction"))
	writeFile(t, filepath.Join(root, "src/difference.js"), "// @facet: arithmetic/calculation/difference\n// @facet-role: primary\nexport function difference() {}\n")
	writeFile(t, filepath.Join(root, "src/subtraction.js"), "// @facet: arithmetic/calculation/subtraction\n// @facet-role: primary\nexport function subtraction() {}\n")

	run := func() (int, facet.Report) {
		t.Helper()
		cmd := exec.Command(bin, "lint", "--json", "--root", root, "--today", "2026-11-24",
			"--skip", "schema,knowledge,created-at-history,proves,verification-targets,no-scenarios")
		out, err := cmd.Output()
		code := 0
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		var rep facet.Report
		if err := json.Unmarshal(out, &rep); err != nil {
			t.Fatalf("invalid lint JSON: %s %v", out, err)
		}
		return code, rep
	}
	count := func(rep facet.Report, severity facet.Severity) int {
		n := 0
		for _, f := range rep.Findings {
			if f.Severity == severity {
				n++
			}
		}
		return n
	}

	code, rep := run()
	if count(rep, facet.SeverityInfo) != 1 || count(rep, facet.SeverityError) != 0 || rep.SharedClaims == nil || len(rep.SharedClaims.Scenarios) != 1 {
		t.Fatalf("premise: one identical-scenarios info and no error, got %+v", rep.Findings)
	}
	if code != 0 {
		t.Fatalf("an info-only run exited %d", code)
	}

	writeFile(t, filepath.Join(root, "src/orphan.js"), "// @facet: arithmetic/calculation/unowned\nexport function orphan() {}\n")
	code, rep = run()
	if count(rep, facet.SeverityError) == 0 || count(rep, facet.SeverityInfo) != 1 {
		t.Fatalf("premise: an orphan error beside the info, got %+v", rep.Findings)
	}
	if code != 1 {
		t.Fatalf("a run with an error exited %d", code)
	}
}
