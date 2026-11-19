// @facet-ignore-file
package facet

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Named mutation: mark features green from a successful global process exit.
// Without a per-file report, the feature must remain unmeasured.
func TestExecutionNeverInfersFilePassFromProcessPass(t *testing.T) {
	cfg, reg := discoveryFixture(t, "printf 'tests/a.go\\n'")
	writeDiscoveryRecord(t, reg, "tests/a.go")
	runner := cfg.Runners["go"]
	runner.Command = "printf 'real stdout'; printf 'real stderr' >&2"
	cfg.Runners["go"] = runner
	var out, errOut bytes.Buffer
	result, err := ExecuteTests(context.Background(), cfg, reg, "", false, &out, &errOut)
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != "real stdout" || errOut.String() != "real stderr" {
		t.Fatalf("runner output changed: %q %q", out.String(), errOut.String())
	}
	if len(result.Features) != 1 || result.Features[0].State != "unmeasured" {
		t.Fatalf("invented pass: %+v", result)
	}
}

// Named mutation: read the old report without clearing its freshness. The
// unchanged passing file would turn a command which writes nothing green.
func TestExecutionRejectsStaleJUnit(t *testing.T) {
	cfg, reg := discoveryFixture(t, "printf 'tests/a.go\\n'")
	writeDiscoveryRecord(t, reg, "tests/a.go")
	runner := cfg.Runners["go"]
	runner.Command = "true"
	runner.Report = "junit"
	runner.ReportFile = "report.xml"
	cfg.Runners["go"] = runner
	stale := []byte(`<testsuite tests="1"><testcase file="tests/a.go" name="old"/></testsuite>`)
	os.WriteFile(filepath.Join(reg.Root, "report.xml"), stale, 0644)
	result, err := ExecuteTests(context.Background(), cfg, reg, "", false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Features[0].State != "unmeasured" {
		t.Fatalf("stale report passed: %+v", result)
	}
	restored, err := os.ReadFile(filepath.Join(reg.Root, "report.xml"))
	if err != nil || !bytes.Equal(restored, stale) {
		t.Fatal("old report was lost")
	}
}

// Named mutation: discard the command exit status. The per-file report can be
// present and green, but a failing runner must still keep its actual exit 7.
func TestExecutionPreservesRunnerFailureAndFreshFileResults(t *testing.T) {
	cfg, reg := discoveryFixture(t, "printf 'tests/a.go\\n'")
	writeDiscoveryRecord(t, reg, "tests/a.go")
	runner := cfg.Runners["go"]
	runner.Command = `printf '%s' '<testsuite tests="1"><testcase file="tests/a.go" name="fresh"/></testsuite>' > report.xml; exit 7`
	runner.Report = "junit"
	runner.ReportFile = "report.xml"
	cfg.Runners["go"] = runner
	result, err := ExecuteTests(context.Background(), cfg, reg, "", false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode() != 7 || result.Features[0].State != "passed" {
		t.Fatalf("runner/file evidence lost: %+v", result)
	}
}

// Named mutation: map skipped, incomplete or unbound JUnit evidence to passed.
// The same existing path first passes a complete fresh report, then must stop.
func TestJUnitFileEvidenceIsConservative(t *testing.T) {
	cfg, reg := discoveryFixture(t, "printf 'tests/a.go\\n'")
	_ = cfg
	files := []string{"tests/a.go"}
	good := `<testsuites tests="1"><testsuite tests="1"><testcase file="tests/a.go" name="fresh"/></testsuite></testsuites>`
	results, err := parseJUnitResults(reg.Root, reg.Root, []byte(good), files)
	if err != nil || results["tests/a.go"] != TestPassed {
		t.Fatalf("valid fresh fixture rejected: %v %v", results, err)
	}
	cases := []struct{ xml, state string }{
		{`<testsuite tests="1"><testcase file="tests/a.go"><failure/></testcase></testsuite>`, TestFailed},
		{`<testsuite tests="1"><testcase file="tests/a.go"><skipped/></testcase></testsuite>`, TestUnmeasured},
		{`<testsuite tests="2"><testcase file="tests/a.go"/></testsuite>`, TestUnmeasured},
		{`<testsuites tests="2"><testsuite tests="1"><testcase file="tests/a.go"/></testsuite></testsuites>`, TestUnmeasured},
		{`<testsuite><testcase classname="tests/a.go"/></testsuite>`, ""},
		{`<testsuite tests="1" failures="1"><testcase file="tests/a.go"/></testsuite>`, TestUnmeasured},
		{`<testsuite tests="1" skipped="1"><testcase file="tests/a.go"/></testsuite>`, TestUnmeasured},
	}
	for _, tc := range cases {
		results, err := parseJUnitResults(reg.Root, reg.Root, []byte(tc.xml), files)
		if err != nil || results["tests/a.go"] != tc.state {
			t.Fatalf("invented JUnit state for %s: %v %v", tc.xml, results, err)
		}
	}
	if _, err := parseJUnitResults(reg.Root, reg.Root, []byte("broken xml"), files); err == nil {
		t.Fatal("malformed report accepted")
	}
}

// Named mutation: assign package-level pass to every test file. Only observed
// declared test functions with package completion can supply per-file results.
func TestGoJSONNeedsActualFileTestEvents(t *testing.T) {
	cfg, reg := discoveryFixture(t, "printf 'tests/a.go\\n'")
	_ = cfg
	os.WriteFile(filepath.Join(reg.Root, "go.mod"), []byte("module example.test/project\n"), 0644)
	os.WriteFile(filepath.Join(reg.Root, "tests/a.go"), []byte("package tests\nimport \"testing\"\nfunc TestOne(t *testing.T) {}\nfunc TestTwo(t *testing.T) {}\n"), 0644)
	files := []string{"tests/a.go"}
	one := `{"Action":"pass","Package":"example.test/project/tests","Test":"TestOne"}` + "\n"
	two := `{"Action":"pass","Package":"example.test/project/tests","Test":"TestTwo"}` + "\n"
	end := `{"Action":"pass","Package":"example.test/project/tests"}` + "\n"
	results, err := parseGoJSONResults(reg.Root, []byte(one+two+end), files)
	if err != nil || results["tests/a.go"] != TestPassed {
		t.Fatalf("valid Go fixture rejected: %v %v", results, err)
	}
	for _, data := range []string{end, one + end, one + two, one + `{"Action":"skip","Package":"example.test/project/tests","Test":"TestTwo"}` + "\n" + end} {
		results, err := parseGoJSONResults(reg.Root, []byte(data), files)
		if err != nil || results["tests/a.go"] != TestUnmeasured {
			t.Fatalf("incomplete Go evidence passed: %v %v", results, err)
		}
	}
	fail := `{"Action":"fail","Package":"example.test/project/tests","Test":"TestOne"}` + "\n"
	results, err = parseGoJSONResults(reg.Root, []byte(fail+one+two+end), files)
	if err != nil || results["tests/a.go"] != TestFailed {
		t.Fatalf("failure overwritten: %v %v", results, err)
	}
	original, err := os.ReadFile(filepath.Join(reg.Root, "tests/a.go"))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(reg.Root, "tests/a.go"), append(original, []byte("func FuzzSeed(f *testing.F) {}\n")...), 0644)
	results, err = parseGoJSONResults(reg.Root, []byte(one+two+end), files)
	if err != nil || results["tests/a.go"] != TestUnmeasured {
		t.Fatalf("unobserved Fuzz declaration ignored: %v %v", results, err)
	}
}

// Named mutation: let report_file escape through a parent symlink. The command
// must not execute or replace an unrelated external report in that case.
func TestExecutionRejectsExternalReportBeforeCommand(t *testing.T) {
	cfg, reg := discoveryFixture(t, "printf 'tests/a.go\\n'")
	writeDiscoveryRecord(t, reg, "tests/a.go")
	outside := t.TempDir()
	original := []byte("external report")
	os.WriteFile(filepath.Join(outside, "report.xml"), original, 0644)
	os.Symlink(outside, filepath.Join(reg.Root, "outside"))
	runner := cfg.Runners["go"]
	runner.Command = "touch executed"
	runner.Report = "junit"
	runner.ReportFile = "outside/report.xml"
	cfg.Runners["go"] = runner
	if _, err := ExecuteTests(context.Background(), cfg, reg, "", false, nil, nil); err == nil {
		t.Fatal("external report accepted")
	}
	if _, err := os.Stat(filepath.Join(reg.Root, "executed")); !os.IsNotExist(err) {
		t.Fatal("unsafe command executed")
	}
	data, err := os.ReadFile(filepath.Join(outside, "report.xml"))
	if err != nil || !bytes.Equal(data, original) {
		t.Fatal("external report modified")
	}
}

// Named mutation: turn a missing fresh report from the second runner into a
// pass. A shared file measured by only one of two declared runners is incomplete.
func TestExecutionPartialMeasurementNeverTurnsGreen(t *testing.T) {
	cfg, reg := discoveryFixture(t, "printf 'tests/a.go\\n'")
	writeDiscoveryRecord(t, reg, "tests/a.go")
	cfg.Runners["go"] = Runner{Command: `printf '%s' '<testsuite><testcase file="tests/a.go"/></testsuite>' > report.xml`, List: "printf 'tests/a.go\\n'", Report: "junit", ReportFile: "report.xml"}
	cfg.Runners["other"] = Runner{Command: "true", List: "printf 'tests/a.go\\n'"}
	result, err := ExecuteTests(context.Background(), cfg, reg, "", false, nil, nil)
	if err != nil || result.Features[0].State != TestUnmeasured {
		t.Fatalf("partial measurement passed: %+v %v", result, err)
	}
}

// Named mutation: remove the report transaction lock. A second command which
// emits no report can then read a fresh report generated by the first command.
func TestExecutionFreshReportsAreIsolatedAcrossConcurrentRuns(t *testing.T) {
	cfg, reg := discoveryFixture(t, "printf 'tests/a.go\\n'")
	writeDiscoveryRecord(t, reg, "tests/a.go")
	cfg.Runners["go"] = Runner{Command: `touch first-entered; while [ ! -f release-first ]; do sleep 0.01; done; printf '%s' '<testsuite><testcase file="tests/a.go"/></testsuite>' > report.xml`, List: "printf 'tests/a.go\\n'", Report: "junit", ReportFile: "report.xml"}
	secondCfg := *cfg
	secondCfg.Runners = map[string]Runner{"go": {Command: `touch second-entered; while [ ! -f release-second ]; do sleep 0.01; done`, List: "printf 'tests/a.go\\n'", Report: "junit", ReportFile: "report.xml"}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	type outcome struct {
		result TestExecution
		err    error
	}
	firstDone, secondDone := make(chan outcome, 1), make(chan outcome, 1)
	go func() { r, e := ExecuteTests(ctx, cfg, reg, "", false, nil, nil); firstDone <- outcome{r, e} }()
	waitMarker := func(name string) {
		t.Helper()
		deadline := time.After(time.Second)
		for {
			if _, err := os.Stat(filepath.Join(reg.Root, name)); err == nil {
				return
			}
			select {
			case <-deadline:
				t.Fatal("runner never entered: " + name)
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	waitMarker("first-entered")
	go func() { r, e := ExecuteTests(ctx, &secondCfg, reg, "", false, nil, nil); secondDone <- outcome{r, e} }()
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(reg.Root, "second-entered")); !os.IsNotExist(err) {
		t.Error("second command entered before first report transaction finished")
	}
	os.WriteFile(filepath.Join(reg.Root, "release-first"), nil, 0644)
	first := <-firstDone
	if first.err != nil || first.result.Features[0].State != TestPassed {
		t.Fatalf("first report not measured: %+v", first)
	}
	waitMarker("second-entered")
	os.WriteFile(filepath.Join(reg.Root, "release-second"), nil, 0644)
	second := <-secondDone
	if second.err != nil || second.result.Features[0].State != TestUnmeasured {
		t.Fatalf("second command stole first report: %+v", second)
	}
}

// Named mutation: drop bounded process-group cancellation during execution.
// Interrupted commands cannot reuse the old report or leak backup artifacts.
func TestExecutionCancellationRestoresReportAndReturnsPromptly(t *testing.T) {
	cfg, reg := discoveryFixture(t, "printf 'tests/a.go\\n'")
	writeDiscoveryRecord(t, reg, "tests/a.go")
	cfg.Runners["go"] = Runner{Command: "sleep 2 & wait", List: "printf 'tests/a.go\\n'", Report: "junit", ReportFile: "report.xml"}
	old := []byte("old report")
	path := filepath.Join(reg.Root, "report.xml")
	os.WriteFile(path, old, 0644)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	result, err := ExecuteTests(ctx, cfg, reg, "", false, nil, nil)
	if err != nil || result.ExitCode() == 0 || result.Features[0].State != TestUnmeasured {
		t.Fatalf("cancelled execution passed: %+v %v", result, err)
	}
	if elapsed := time.Since(start); elapsed > 750*time.Millisecond {
		t.Fatalf("execution cancellation waited %s", elapsed)
	}
	restored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(restored, old) {
		t.Fatal("cancelled execution lost previous report")
	}
	leftovers, _ := filepath.Glob(filepath.Join(reg.Root, ".facet-report-previous-*"))
	if len(leftovers) > 0 {
		t.Fatalf("report backups leaked: %v", leftovers)
	}
}

// Named mutation: compare report/test paths lexically instead of file identity.
// An internal symlink alias or hardlink would otherwise replace test source.
func TestExecutionReportCannotReplaceAliasedTestSource(t *testing.T) {
	for _, kind := range []string{"directory-symlink", "hardlink", "no-list"} {
		t.Run(kind, func(t *testing.T) {
			cfg, reg := discoveryFixture(t, "printf 'tests/a.go\\n'")
			writeDiscoveryRecord(t, reg, "tests/a.go")
			original, err := os.ReadFile(filepath.Join(reg.Root, "tests/a.go"))
			if err != nil {
				t.Fatal(err)
			}
			report := "alias/a.go"
			if kind != "hardlink" {
				if err := os.Symlink("tests", filepath.Join(reg.Root, "alias")); err != nil {
					t.Fatal(err)
				}
			} else {
				report = "report.xml"
				if err := os.Link(filepath.Join(reg.Root, "tests/a.go"), filepath.Join(reg.Root, report)); err != nil {
					t.Fatal(err)
				}
			}
			cfg.Runners["go"] = Runner{Command: "touch executed; printf corrupted > " + report, List: "printf 'tests/a.go\\n'", Report: "junit", ReportFile: report}
			if kind == "no-list" {
				runner := cfg.Runners["go"]
				runner.List = ""
				cfg.Runners["go"] = runner
			}
			if _, err := ExecuteTests(context.Background(), cfg, reg, "", false, nil, nil); err == nil {
				t.Fatal("aliased source report was accepted")
			}
			if _, err := os.Stat(filepath.Join(reg.Root, "executed")); !os.IsNotExist(err) {
				t.Fatal("destructive report command was executed")
			}
			after, err := os.ReadFile(filepath.Join(reg.Root, "tests/a.go"))
			if err != nil || !bytes.Equal(after, original) {
				t.Fatal("aliased source was destroyed")
			}
		})
	}
}

// Named mutation: parse only the first XML root. A later failing suite would
// be silently ignored while the first passing suite incorrectly turns green.
func TestJUnitRejectsTrailingDocumentAndData(t *testing.T) {
	cfg, reg := discoveryFixture(t, "printf 'tests/a.go\\n'")
	_ = cfg
	first := `<testsuite tests="1"><testcase file="tests/a.go"/></testsuite>`
	results, err := parseJUnitResults(reg.Root, reg.Root, []byte(first+" \n<!-- trailing comment -->"), []string{"tests/a.go"})
	if err != nil || results["tests/a.go"] != TestPassed {
		t.Fatalf("well-formed prerequisite failed: %v %v", results, err)
	}
	for _, extra := range []string{`<testsuite tests="1"><testcase file="tests/a.go"><failure/></testcase></testsuite>`, "garbage"} {
		if _, err := parseJUnitResults(reg.Root, reg.Root, []byte(first+extra), []string{"tests/a.go"}); err == nil {
			t.Fatalf("trailing evidence ignored: %s", extra)
		}
	}
}

// Named mutation: leave missing or skipped file evidence unexplained even
// though the fresh XML is valid. The runner must report the measurement gap.
func TestExecutionExplainsMissingAndIncompleteFileEvidence(t *testing.T) {
	for _, body := range []string{
		`<testsuite><testcase classname="tests/a.go" name="one"/></testsuite>`,
		`<testsuite><testcase file="tests/a.go" name="one"><skipped/></testcase></testsuite>`,
	} {
		cfg, reg := discoveryFixture(t, "printf 'tests/a.go\\n'")
		r := cfg.Runners["go"]
		r.Command = "printf '%s' '" + body + "' > report.xml"
		r.Report, r.ReportFile = "junit", "report.xml"
		cfg.Runners["go"] = r
		result, err := ExecuteTests(context.Background(), cfg, reg, "", false, io.Discard, io.Discard)
		if err != nil || len(result.Runners) != 1 || !strings.Contains(result.Runners[0].ReportProblem, "tests/a.go") {
			t.Fatalf("Evidence gap has no file-specific reason: %+v %v", result, err)
		}
		if result.Runners[0].Results["tests/a.go"] == TestPassed {
			t.Fatalf("Missing evidence invented a pass: %+v", result)
		}
	}
}

// Named mutation: check feature states only. Unclaimed baseline files and
// a runner without measured discovery would then escape strict CI policy.
func TestStrictEvidenceIncludesRunnerFilesAndDiscovery(t *testing.T) {
	result := TestExecution{
		Discovery: TestDiscovery{Runners: []DiscoveredRunner{{Name: "local", Measured: true, Files: []string{"tests/a.go"}}}},
		Runners:   []RunnerExecution{{Name: "local", Results: map[string]string{"tests/a.go": TestPassed}}},
	}
	if result.HasUnmeasured() {
		t.Fatal("Complete runner evidence was rejected")
	}
	delete(result.Runners[0].Results, "tests/a.go")
	if !result.HasUnmeasured() {
		t.Fatal("Unclaimed runner file without evidence was accepted")
	}
	result.Runners[0].Results["tests/a.go"] = TestPassed
	result.Discovery.Runners[0].Measured = false
	if !result.HasUnmeasured() {
		t.Fatal("Missing discovery was accepted")
	}
}

// Named mutation: in FileCounts let a later passed overwrite an earlier failed
// (drop the rank comparison); the failed count turns red.
func TestFileCountsCombineRunnersWorstFirstAndNeverInventAPass(t *testing.T) {
	r := TestExecution{
		Discovery: TestDiscovery{Runners: []DiscoveredRunner{
			{Name: "a", Measured: true, Files: []string{"x", "y", "z"}},
			{Name: "b", Measured: true, Files: []string{"x"}},
			{Name: "c", Measured: false, Files: []string{"w"}},
		}},
		Runners: []RunnerExecution{
			{Name: "a", Results: map[string]string{"x": TestFailed, "y": TestPassed}},
			{Name: "b", Results: map[string]string{"x": TestPassed}},
			{Name: "c", Results: map[string]string{"w": TestPassed}},
		},
	}
	got := r.FileCounts()
	// x failed in one runner and passed in the other: failed. y passed. z has
	// no result: unmeasured. w sits in an unmeasured discovery: unmeasured
	// even though a result names it.
	if got != (TestFileCounts{Passed: 1, Failed: 1, Unmeasured: 2}) {
		t.Fatalf("unexpected counts %+v", got)
	}
	if !got.Measured() {
		t.Fatal("a run with a passed file is measured")
	}
	if (TestFileCounts{Unmeasured: 3}).Measured() {
		t.Fatal("a run with nothing but unmeasured files has no failure count worth printing")
	}
}

// Named mutation: replace the worst-first merge of same-named runners by an
// overwrite (`results = run.Results`); the measured failure becomes a pass.
func TestSameNamedRunnersMergeWorstFirst(t *testing.T) {
	r := TestExecution{
		Discovery: TestDiscovery{Runners: []DiscoveredRunner{{Name: "go", Measured: true, Files: []string{"x"}}}},
		Runners: []RunnerExecution{
			{Name: "go", Results: map[string]string{"x": TestFailed}},
			{Name: "go", Results: map[string]string{"x": TestPassed}},
		},
	}
	if got := r.FileCounts(); got != (TestFileCounts{Failed: 1}) {
		t.Fatalf("a later pass erased a measured failure: %+v", got)
	}
}
