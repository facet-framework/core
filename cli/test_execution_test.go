// @facet: registry/integrity/runner-test-execution
// @facet-role: related
package cli

import (
	"bytes"
	"encoding/json"
	"github.com/facet-framework/core/registry"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func executionCLIRecord(t *testing.T, root, name, file string) {
	t.Helper()
	dir := filepath.Join(root, "docs/facets/features/execution/tests")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	data := "id: execution/tests/" + name + "\nverification:\n  unit_tests:\n    - id: " + file + "\n      proves: [one]\n"
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}
func executionCLI(t *testing.T, root string, flags ...string) ([]byte, []byte, int) {
	t.Helper()
	args := append([]string{"--root", root, "test"}, flags...)
	command := exec.Command(facetBinary(t), args...)
	var out, stderr bytes.Buffer
	command.Stdout = &out
	command.Stderr = &stderr
	err := command.Run()
	code := 0
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	return out.Bytes(), stderr.Bytes(), code
}

// Named mutation: convert the CLI's typed runner exit to os.Exit(1), or print a
// FACET diagnostic in runner-only mode. Actual bytes and exit 7 must survive.
func TestBinaryExecutionPreservesRawStreamsAndExactExit(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "tests"), 0755)
	os.WriteFile(filepath.Join(root, "tests/a.go"), []byte("package tests"), 0644)
	executionCLIRecord(t, root, "sample", "tests/a.go")
	os.WriteFile(filepath.Join(root, ".facetrc.yaml"), []byte("runners:\n  local:\n    list: printf 'tests/a.go\\n'\n    command: printf OUT; printf ERR >&2; exit 7\n"), 0644)
	out, stderr, code := executionCLI(t, root, "--only", "runner")
	if code != 7 || string(out) != "OUT" || string(stderr) != "ERR" {
		t.Fatalf("raw runner changed: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
	out, stderr, code = executionCLI(t, root, "--json")
	var reports []facet.TestExecution
	if err := json.Unmarshal(out, &reports); err != nil {
		t.Fatalf("runner corrupted JSON: %s %v", out, err)
	}
	if code != 7 || len(reports) != 1 || reports[0].Features[0].State != facet.TestUnmeasured || !strings.Contains(string(stderr), "OUTERR") {
		t.Fatalf("JSON lost runner evidence: %s %s exit%d", out, stderr, code)
	}
	out, stderr, code = executionCLI(t, root, "--only", "facet")
	if code != 7 || strings.Contains(string(out), "OUT") || strings.Contains(string(stderr), "ERR") {
		t.Fatalf("facet-only leaked runner output: %s %s exit%d", out, stderr, code)
	}
}

// Named mutation: infer Go file results from package success, ignore a failing
// file, or reuse a stale report. This invokes a real go test -json runner through
// the delivered facet binary and then changes only the failing test body.
func TestBinaryExecutionMeasuresRealGoFilesAndNeverReusesOldReport(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "tests"), 0755)
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/execution\ngo 1.25.0\n"), 0644)
	os.WriteFile(filepath.Join(root, "tests/good_test.go"), []byte("package tests\nimport \"testing\"\nfunc TestGood(t *testing.T) {}\n"), 0644)
	badPath := filepath.Join(root, "tests/bad_test.go")
	os.WriteFile(badPath, []byte("package tests\nimport \"testing\"\nfunc TestBad(t *testing.T) { t.Fatal(\"real failing test\") }\n"), 0644)
	executionCLIRecord(t, root, "good", "tests/good_test.go")
	executionCLIRecord(t, root, "bad", "tests/bad_test.go")
	config := `runners:
  local:
    list: "printf '%s\\n' tests/good_test.go tests/bad_test.go"
    command: go test -json ./... > result.json
    report: go-json
    report_file: result.json
`
	os.WriteFile(filepath.Join(root, ".facetrc.yaml"), []byte(config), 0644)
	read := func() (map[string]string, int) {
		out, stderr, code := executionCLI(t, root, "--json", "--only", "facet")
		var reports []facet.TestExecution
		if err := json.Unmarshal(out, &reports); err != nil {
			t.Fatalf("invalid binary result %s %s: %v", out, stderr, err)
		}
		states := map[string]string{}
		for _, f := range reports[0].Features {
			states[f.ID] = f.State
		}
		return states, code
	}
	states, code := read()
	if code != 1 || states["execution/tests/bad"] != facet.TestFailed || states["execution/tests/good"] != facet.TestPassed {
		t.Fatalf("Go file evidence or runner failure lost: %v exit%d", states, code)
	}
	// Both package completion and each declared test root were observed. The
	// passing sibling is measured even while the runner as a whole fails.
	os.WriteFile(badPath, []byte("package tests\nimport \"testing\"\nfunc TestBad(t *testing.T) {}\n"), 0644)
	states, code = read()
	if code != 0 || states["execution/tests/bad"] != facet.TestPassed || states["execution/tests/good"] != facet.TestPassed {
		t.Fatalf("fresh Go success not measured: %v exit%d", states, code)
	}
	os.WriteFile(filepath.Join(root, ".facetrc.yaml"), []byte(strings.Replace(config, "go test -json ./... > result.json", "true", 1)), 0644)
	states, code = read()
	if code != 0 || states["execution/tests/bad"] != facet.TestUnmeasured || states["execution/tests/good"] != facet.TestUnmeasured {
		t.Fatalf("stale Go report reused: %v exit%d", states, code)
	}
	os.WriteFile(filepath.Join(root, ".facetrc.yaml"), []byte(config), 0644)
	os.WriteFile(badPath, []byte("package tests\nfunc TestBad( broken syntax\n"), 0644)
	states, code = read()
	if code != 1 || states["execution/tests/bad"] != facet.TestUnmeasured || states["execution/tests/good"] != facet.TestUnmeasured {
		t.Fatalf("uncompiled files received outcomes: %v exit%d", states, code)
	}
}

// Named mutation: ignore --fail-unmeasured, replace a runner's exit with one,
// or send summary text into JSON/raw streams. Assert the delivered binary.
func TestBinaryStrictEvidenceAndActionableSummary(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "tests"), 0755)
	os.WriteFile(filepath.Join(root, "tests/a.go"), []byte("package tests"), 0644)
	executionCLIRecord(t, root, "sample", "tests/a.go")
	config := "runners:\n  local:\n    list: printf 'tests/a.go\\n'\n    command: true\n"
	path := filepath.Join(root, ".facetrc.yaml")
	os.WriteFile(path, []byte(config), 0644)
	out, stderr, code := executionCLI(t, root)
	if code != 0 || !strings.Contains(string(out), "tests/a.go") || !strings.Contains(string(out), "1 unmeasured") || !strings.Contains(string(out), "report and report_file") {
		t.Fatalf("Missing actionable output: %s %s exit %d", out, stderr, code)
	}
	out, stderr, code = executionCLI(t, root, "--fail-unmeasured", "--json")
	if code != 1 || !json.Valid(out) {
		t.Fatalf("Strict JSON evidence failed: %s %s exit %d", out, stderr, code)
	}
	os.WriteFile(path, []byte(strings.Replace(config, "command: true", "command: exit 7", 1)), 0644)
	out, stderr, code = executionCLI(t, root, "--fail-unmeasured", "--only", "runner")
	if code != 7 || len(out) != 0 || len(stderr) != 0 {
		t.Fatalf("Strict mode replaced runner exit or output: %s %s exit %d", out, stderr, code)
	}
	out, stderr, code = executionCLI(t, root, "--fail-unmeasured", "--check")
	if code == 0 || !strings.Contains(string(stderr), "execution") {
		t.Fatalf("Strict evidence accepted discovery-only mode: %s %s exit %d", out, stderr, code)
	}
	os.WriteFile(path, []byte(config+"  other:\n    list: printf 'tests/a.go\\n'\n    command: exit 7\n"), 0644)
	out, stderr, code = executionCLI(t, root, "--fail-unmeasured", "--json")
	if code != 7 || !json.Valid(out) {
		t.Fatalf("Early unmeasured evidence masked later runner failure: %s %s exit %d", out, stderr, code)
	}

}

// Named mutation: keep an earlier registry's drift exit one when a later
// runner exits seven. A real runner failure must take precedence over policy.
func TestBinaryMultiRegistryPreservesRunnerExitOverEarlierDrift(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "tests"), 0755)
	os.WriteFile(filepath.Join(root, "tests/a.go"), []byte("package tests"), 0644)
	config := `registries:
  first:
    records: docs/first
    runners:
      local:
        command: true
        list: printf 'tests/a.go\n'
  second:
    records: docs/second
    runners:
      local:
        command: exit 7
        list: printf 'tests/a.go\n'
`
	os.WriteFile(filepath.Join(root, ".facetrc.yaml"), []byte(config), 0644)
	for _, flags := range [][]string{{"--json"}, {"--json", "--fail-unmeasured"}} {
		out, stderr, code := executionCLI(t, root, flags...)
		var reports []facet.TestExecution
		if err := json.Unmarshal(out, &reports); err != nil {
			t.Fatal(err)
		}
		if code != 7 || len(reports) != 2 || !reports[0].Discovery.Failed() || reports[1].Runners[0].Exit != 7 {
			t.Fatalf("Earlier drift replaced runner exit: %s %s exit %d", out, stderr, code)
		}
	}
}

// Named mutation: delete the GitTreeState call at the top of ExecuteTests; the
// JSON then carries no head and this test turns red. A second one: make
// GitTreeState report dirty=false always; the dirty half turns red.
func TestBinaryJSONNamesTheCommitAndTheTreeItWasMeasuredOn(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "tests"), 0755)
	os.WriteFile(filepath.Join(root, "tests/a.go"), []byte("package tests"), 0644)
	executionCLIRecord(t, root, "sample", "tests/a.go")
	os.WriteFile(filepath.Join(root, ".facetrc.yaml"), []byte("runners:\n  local:\n    list: printf 'tests/a.go\\n'\n    command: true\n"), 0644)
	read := func() []facet.TestExecution {
		out, stderr, _ := executionCLI(t, root, "--json", "--only", "facet")
		var reports []facet.TestExecution
		if err := json.Unmarshal(out, &reports); err != nil || len(reports) != 1 {
			t.Fatalf("invalid binary result %s %s: %v", out, stderr, err)
		}
		return reports
	}

	// Outside a repository nothing can be said, and nothing is: absent, not
	// an empty head and not a clean tree.
	if r := read()[0]; r.Head != "" || r.Dirty != nil {
		t.Fatalf("a tree without git must carry no head and no dirty flag: %+v", r)
	}

	git(t, root, "init", "-q")
	git(t, root, "config", "user.email", "test@example.invalid")
	git(t, root, "config", "user.name", "Test")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "first")
	head := strings.TrimSpace(git(t, root, "rev-parse", "HEAD"))
	if r := read()[0]; r.Head != head || r.Dirty == nil || *r.Dirty {
		t.Fatalf("a clean tree must name its commit %s and be clean: %+v", head, r)
	}

	os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("x"), 0644)
	if r := read()[0]; r.Head != head || r.Dirty == nil || !*r.Dirty {
		t.Fatalf("an untracked file makes the tree dirty: %+v", r)
	}
}

// Named mutation: move the GitTreeState call in ExecuteTests below the runner
// loop. The runner here writes an untracked file, so a late measurement would
// report dirty:true.
func TestBinaryMeasuresTheTreeBeforeAnyRunnerStarts(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "tests"), 0755)
	os.WriteFile(filepath.Join(root, "tests/a.go"), []byte("package tests"), 0644)
	executionCLIRecord(t, root, "sample", "tests/a.go")
	os.WriteFile(filepath.Join(root, ".facetrc.yaml"), []byte("runners:\n  local:\n    list: printf 'tests/a.go\\n'\n    command: touch written-by-the-runner.txt\n"), 0644)
	git(t, root, "init", "-q")
	git(t, root, "config", "user.email", "test@example.invalid")
	git(t, root, "config", "user.name", "Test")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "first")
	out, stderr, _ := executionCLI(t, root, "--json", "--only", "facet")
	var reports []facet.TestExecution
	if err := json.Unmarshal(out, &reports); err != nil || len(reports) != 1 {
		t.Fatalf("invalid binary result %s %s: %v", out, stderr, err)
	}
	if _, err := os.Stat(filepath.Join(root, "written-by-the-runner.txt")); err != nil {
		t.Fatalf("precondition: the runner must have written its file: %v", err)
	}
	if reports[0].Dirty == nil || *reports[0].Dirty {
		t.Fatalf("head and dirty must describe the tree before the runners started: %+v", reports[0])
	}
}

func TestBinaryFeaturePrefixOverridesConfiguration(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "tests"), 0755)
	os.WriteFile(filepath.Join(root, "tests/a.go"), []byte("package tests"), 0644)
	os.WriteFile(filepath.Join(root, "tests/b.go"), []byte("package tests"), 0644)
	executionCLIRecord(t, root, "one", "tests/a.go")
	executionCLIRecord(t, root, "two", "tests/b.go")
	os.WriteFile(filepath.Join(root, ".facetrc.yaml"), []byte("test_feature_prefix: execution/tests/one\nrunners:\n  local:\n    list: printf 'tests/a.go\\ntests/b.go\\n'\n    command: 'true'\n"), 0644)
	for _, tc := range []struct {
		flags  []string
		want   int
		prefix string
	}{
		{[]string{"--json"}, 1, "execution/tests/one"},
		{[]string{"--json", "--feature-prefix", "execution/tests/two"}, 1, "execution/tests/two"},
		{[]string{"--json", "--feature-prefix", ""}, 2, ""},
	} {
		out, _, _ := executionCLI(t, root, tc.flags...)
		var report []facet.TestExecution
		if err := json.Unmarshal(out, &report); err != nil {
			t.Fatalf("%s: %v", out, err)
		}
		if len(report) != 1 || len(report[0].Features) != tc.want || report[0].Discovery.FeaturePrefix != tc.prefix {
			t.Fatalf("scope override: %s", out)
		}
	}
	out, _, code := executionCLI(t, root, "--check", "--json", "--feature-prefix", "missing/")
	if code == 0 {
		t.Fatalf("unmatched discovery scope passed: %s", out)
	}
}
