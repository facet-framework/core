// @facet: registry/integrity/runner-test-execution
// @facet-role: primary

package facet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/facet-framework/core/internal/spec"
)

// TestPassed identifies positive execution evidence for a file or feature.
const TestPassed = "passed"

// TestFailed identifies failed execution evidence for a file or feature.
const TestFailed = "failed"

// TestUnmeasured identifies a file or feature without usable execution evidence.
const TestUnmeasured = "unmeasured"

// RunnerExecution reports a runner's exit code and file-level test states;
// ReportProblem explains why its report could not provide usable evidence.
type RunnerExecution struct {
	Name          string            `json:"name"`
	Exit          int               `json:"exit_code"`
	Results       map[string]string `json:"files"`
	ReportProblem string            `json:"report_problem,omitempty"`
}

// FeatureTestResult combines the execution states of a record's referenced test
// files and the directly bound specification scenarios.
type FeatureTestResult struct {
	ID        string               `json:"id"`
	State     string               `json:"state"`
	Files     map[string]string    `json:"files"`
	Scenarios []ScenarioTestResult `json:"scenarios,omitempty"`
}

// TestSourceSnapshot is the state a repository was in when a test run started:
// its revision, a hash of the content the run read from it, and whether its
// working tree had uncommitted changes. A run records one per source
// repository of the registry's source view, and one for the registry's own
// records.
type TestSourceSnapshot struct {
	Revision    string `json:"revision"`
	ContentHash string `json:"content_hash"`
	Dirty       bool   `json:"dirty"`
}

// TestExecution combines discovery, runner results and derived feature states
// for one registry's test run.
type TestExecution struct {
	InputProblem       string                        `json:"input_problem,omitempty"`
	RegistryIdentity   string                        `json:"registry_identity,omitempty"`
	RegistryRepository string                        `json:"registry_repository,omitempty"`
	RegistrySnapshot   *TestSourceSnapshot           `json:"registry_snapshot,omitempty"`
	SourceSnapshots    map[string]TestSourceSnapshot `json:"source_snapshots,omitempty"`
	SourceRevisions    map[string]string             `json:"source_revisions,omitempty"`
	RecordHashes       map[string]string             `json:"record_hashes,omitempty"`
	// Head is the commit the tests ran on and Dirty says whether the tree had
	// uncommitted or untracked changes when they started. Both are measured
	// before any runner starts and are left out where git could not be asked,
	// so a report without them says "not measured" instead of "clean".
	Head      string              `json:"head,omitempty"`
	Dirty     *bool               `json:"dirty,omitempty"`
	Discovery TestDiscovery       `json:"discovery"`
	Runners   []RunnerExecution   `json:"runners"`
	Features  []FeatureTestResult `json:"features"`
}

// TestFileCounts tallies a run's test files by their combined state.
type TestFileCounts struct {
	Passed     int `json:"passed"`
	Failed     int `json:"failed"`
	Unmeasured int `json:"unmeasured"`
}

// Measured reports whether any file produced usable evidence. A run where
// everything is unmeasured has no failure count worth printing: zero failed of
// nothing measured is not "zero failing".
func (c TestFileCounts) Measured() bool { return c.Passed+c.Failed > 0 }

// FileCounts combines every discovered file across runners: failed wins over
// unmeasured, which wins over passed, and a file no report mentioned is
// unmeasured. Files of a runner whose discovery was not measured are
// unmeasured too.
func (r TestExecution) FileCounts() TestFileCounts {
	combined := map[string]string{}
	rank := map[string]int{TestPassed: 0, TestUnmeasured: 1, TestFailed: 2}
	merge := func(file, state string) {
		if state != TestPassed && state != TestFailed {
			state = TestUnmeasured
		}
		if prev, ok := combined[file]; !ok || rank[state] > rank[prev] {
			combined[file] = state
		}
	}
	for _, d := range r.Discovery.Runners {
		// Runners of one name are merged worst-first, never overwritten: a
		// later "passed" must not erase an earlier measured failure.
		results := map[string]string{}
		for _, run := range r.Runners {
			if run.Name != d.Name {
				continue
			}
			for file, state := range run.Results {
				if state != TestPassed && state != TestFailed {
					state = TestUnmeasured
				}
				if prev, ok := results[file]; !ok || rank[state] > rank[prev] {
					results[file] = state
				}
			}
		}
		for _, file := range d.Files {
			if !d.Measured {
				merge(file, TestUnmeasured)
				continue
			}
			merge(file, results[file])
		}
	}
	var counts TestFileCounts
	for _, state := range combined {
		switch state {
		case TestPassed:
			counts.Passed++
		case TestFailed:
			counts.Failed++
		default:
			counts.Unmeasured++
		}
	}
	return counts
}

// GitTreeState asks git for the checked-out commit and whether the tree has
// changes. It fails where root is not inside a repository or has no commit.
func GitTreeState(root string) (head string, dirty bool, err error) {
	out, err := gitOutput(root, nil, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", false, err
	}
	head = strings.TrimSpace(string(out))
	status, err := gitOutput(root, nil, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return "", false, err
	}
	return head, len(strings.TrimSpace(string(status))) > 0, nil
}

// HasUnmeasuredSpec reports unavailable specification execution dependencies.
func (r TestExecution) HasUnmeasuredSpec() bool {
	for _, feature := range r.Features {
		for _, scenario := range feature.Scenarios {
			if scenario.State == TestUnmeasured {
				return true
			}
		}
	}
	return false
}

// HasUnmeasured reports gaps in both feature claims and runner file evidence.
func (r TestExecution) HasUnmeasured() bool {
	for _, feature := range r.Features {
		if feature.State == TestUnmeasured {
			return true
		}
	}
	for _, discovered := range r.Discovery.Runners {
		if !discovered.Measured {
			return true
		}
		var execution *RunnerExecution
		for index := range r.Runners {
			if r.Runners[index].Name == discovered.Name {
				execution = &r.Runners[index]
				break
			}
		}
		if execution == nil {
			return true
		}
		for _, file := range discovered.Files {
			if state := execution.Results[file]; state == "" || state == TestUnmeasured {
				return true
			}
		}
	}
	return false
}

// ExitCode preserves the first nonzero runner exit in sorted registry order.
// Drift and measured file failures use 1 only when all runner exits were zero.
func (r TestExecution) ExitCode() int {
	for _, runner := range r.Runners {
		if runner.Exit != 0 {
			return runner.Exit
		}
	}
	if r.InputProblem != "" {
		return 1
	}
	for _, runner := range r.Runners {
		for _, state := range runner.Results {
			if state == TestFailed {
				return 1
			}
		}
	}
	if r.Discovery.Failed() {
		return 1
	}
	for _, f := range r.Features {
		if f.State == TestFailed {
			return 1
		}
		for _, scenario := range f.Scenarios {
			if scenario.State == TestDiverged {
				return 1
			}
		}
	}
	return 0
}

// reportPath resolves even a not-yet-created report while preventing symlink
// escapes through an existing ancestor. Its eventual contents are rechecked.
func reportPath(root, cwd, path string) (string, error) {
	full := filepath.Join(cwd, path)
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	full, err = filepath.Abs(full)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("report path escapes repository: %s", path)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	ancestor := full
	for {
		_, err = os.Lstat(ancestor)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		next := filepath.Dir(ancestor)
		if next == ancestor {
			return "", err
		}
		ancestor = next
	}
	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", err
	}
	rel, err = filepath.Rel(realRoot, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("report path resolves outside repository: %s", path)
	}
	if info, err := os.Lstat(full); err == nil && !info.Mode().IsRegular() {
		return "", fmt.Errorf("report must be a regular file: %s", path)
	}
	return full, nil
}

// lockTestReport serialises test runs that write the same report: freshness is
// a transaction across CLI invocations too. It resolves the lock identity
// through the existing ancestor even before report directories exist.
func lockTestReport(ctx context.Context, path string) (func(), error) {
	ancestor := filepath.Dir(path)
	for {
		_, err := os.Lstat(ancestor)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		next := filepath.Dir(ancestor)
		if next == ancestor {
			return nil, err
		}
		ancestor = next
	}
	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(ancestor, path)
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte(filepath.Join(resolved, rel)))
	file, err := os.OpenFile(filepath.Join(os.TempDir(), fmt.Sprintf("facet-test-report-%x.lock", key)), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = acquireTestLock(ctx, file); err != nil {
		file.Close()
		return nil, err
	}
	return func() { releaseTestLock(file); file.Close() }, nil
}

// prepareReport isolates previous output. The cleanup restores it only if the
// command produced no replacement, so old reports can never be read as fresh.
func prepareReport(path string) (func() error, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return func() error { return nil }, nil
	} else if err != nil {
		return nil, err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".facet-report-previous-*")
	if err != nil {
		return nil, err
	}
	backup := temp.Name()
	if err = temp.Close(); err != nil {
		os.Remove(backup)
		return nil, err
	}
	if err = os.Rename(path, backup); err != nil {
		os.Remove(backup)
		return nil, err
	}
	return func() error {
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			return os.Rename(backup, path)
		}
		return os.Remove(backup)
	}, nil
}

func executeRunner(ctx context.Context, root, name string, r Runner, files, protectedFiles []string, specFiles []*specTestFile, stdout, stderr io.Writer) (execution RunnerExecution, err error) {
	execution = RunnerExecution{Name: name, Results: map[string]string{}}
	cwd := filepath.Join(root, r.Cwd)
	// Recheck cwd immediately before executing; discovery may have been absent.
	resolved, e := filepath.EvalSymlinks(cwd)
	if e != nil {
		return execution, e
	}
	realRoot, e := filepath.EvalSymlinks(root)
	if e != nil {
		return execution, e
	}
	rel, e := filepath.Rel(realRoot, resolved)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return execution, fmt.Errorf("runner %q cwd escapes repository", name)
	}
	var report string
	if r.Report != "" {
		report, e = reportPath(root, cwd, r.ReportFile)
		if e != nil {
			return execution, e
		}
		reportInfo, reportStatErr := os.Stat(report)
		for _, file := range protectedFiles {
			testPath := filepath.Join(root, filepath.FromSlash(file))
			testInfo, testErr := os.Stat(testPath)
			if os.IsNotExist(testErr) {
				continue
			}
			if testErr != nil {
				return execution, testErr
			}
			if report == testPath || reportStatErr == nil && os.SameFile(reportInfo, testInfo) {
				return execution, fmt.Errorf("report path overlaps discovered test file %s", file)
			}
		}
		unlock, e := lockTestReport(ctx, report)
		if e != nil {
			return execution, e
		}
		defer unlock()
		// Another invocation may have finished while this one waited for its lock.
		if _, e = reportPath(root, cwd, r.ReportFile); e != nil {
			return execution, e
		}
		cleanup, e := prepareReport(report)
		if e != nil {
			return execution, e
		}
		defer func() {
			if e := cleanup(); e != nil && err == nil {
				err = e
			}
		}()
	}
	command := testListCommand(ctx, r.Command, specTestPaths(specFiles)...)
	command.Dir = cwd
	command.Stdout = stdout
	command.Stderr = stderr
	if e = command.Run(); e != nil {
		if exit, ok := e.(*exec.ExitError); ok {
			execution.Exit = runnerExitCode(exit)
		} else {
			execution.Exit = 1
		}
		if ctx.Err() != nil {
			execution.ReportProblem = ctx.Err().Error()
		}
	}
	if r.Report == "" {
		execution.ReportProblem = "file results not measured: runner declares no report; configure report and report_file for fresh per-file evidence"
		return execution, nil
	}
	if _, e = reportPath(root, cwd, r.ReportFile); e != nil {
		execution.ReportProblem = e.Error()
		return execution, nil
	}
	data, e := os.ReadFile(report)
	if e != nil {
		execution.ReportProblem = "fresh report not available: " + e.Error()
		return execution, nil
	}
	if r.Report == "junit" {
		execution.Results, e = parseJUnitResults(root, cwd, data, files)
	} else if r.Report == "go-json" {
		execution.Results, e = parseGoJSONResults(root, data, files)
	} else if r.Report == "cucumber-json" {
		execution.Results, e = parseCucumberResults(root, cwd, data, files, specFiles...)
	}
	if e != nil {
		execution.Results = map[string]string{}
		execution.ReportProblem = "file results not measured: " + e.Error()
	} else {
		missing, incomplete := []string{}, []string{}
		for _, file := range files {
			switch execution.Results[file] {
			case "":
				missing = append(missing, file)
			case TestUnmeasured:
				incomplete = append(incomplete, file)
			}
		}
		problems := []string{}
		if len(missing) > 0 {
			problems = append(problems, "fresh report has no bound evidence for: "+strings.Join(missing, ", "))
			if r.Report == "junit" {
				problems = append(problems, "configure explicit file attributes on JUnit testcase or testsuite; classname and test names are not file paths")
			}
		}
		if len(incomplete) > 0 {
			problems = append(problems, "skipped or incomplete file evidence for: "+strings.Join(incomplete, ", "))
		}
		if len(files) == 0 && len(specFiles) == 0 {
			problems = append(problems, "file results not measured: discovery supplied no files; configure a successful list command")
		}
		execution.ReportProblem = strings.Join(problems, "; ")
	}
	return execution, nil
}

type executionError struct {
	code  int
	cause error
}

func (e executionError) Error() string { return e.cause.Error() }
func (e executionError) Unwrap() error { return e.cause }
func (e executionError) ExitCode() int { return e.code }

// ExecuteTests runs commands verbatim after discovery. Only positively bound
// fresh per-file evidence can mark a feature passed; exit 0 supplies no evidence.
func ExecuteTests(ctx context.Context, cfg *Config, reg Registry, selected string, update bool, stdout, stderr io.Writer) (result TestExecution, err error) {
	result = TestExecution{Runners: []RunnerExecution{}, Features: []FeatureTestResult{}}
	if reg.SourceView != nil {
		if e := reg.SourceView.Validate(); e != nil {
			return result, e
		}
		result.SourceRevisions = map[string]string{}
		result.SourceSnapshots = map[string]TestSourceSnapshot{}
		for _, src := range reg.SourceView.Ordered() {
			if e := src.ValidateWorkingTree(); e != nil {
				return result, e
			}
			result.SourceRevisions[src.Repository] = src.Revision
			result.SourceSnapshots[src.Repository] = TestSourceSnapshot{Revision: src.Revision, ContentHash: src.ContentHash, Dirty: src.Dirty}
		}
		result.RegistryRepository = reg.SourceView.RegistryRepository
		result.RegistryIdentity = "known"
		if result.RegistryRepository == "" {
			result.RegistryIdentity = "unknown"
		}
		var e error
		result.RecordHashes, e = registryRecordHashes(reg)
		if e != nil {
			return result, e
		}
		ids := make([]string, 0, len(result.RecordHashes))
		for id := range result.RecordHashes {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		h := sha256.New()
		for _, id := range ids {
			fmt.Fprintf(h, "%s\x00%s\n", id, result.RecordHashes[id])
		}
		if revision, dirty, e := GitTreeState(reg.Root); e == nil {
			result.RegistrySnapshot = &TestSourceSnapshot{Revision: revision, Dirty: dirty, ContentHash: fmt.Sprintf("%x", h.Sum(nil))}
		}
	}
	// Before anything runs: a runner may write files, and the report must say
	// what the tree looked like when the tests started.
	if head, dirty, e := GitTreeState(reg.Root); e == nil {
		result.Head, result.Dirty = head, &dirty
	}
	defer func() {
		if err != nil {
			code := result.ExitCode()
			if code == 0 {
				code = 1
			}
			err = executionError{code, err}
		}
	}()
	// A successful report is valid only for the exact inputs measured above.
	configBefore, _ := os.ReadFile(filepath.Join(reg.Root, ConfigName))
	records, err := testScopeFeatures(cfg, reg)
	if err != nil {
		return result, err
	}
	hasSpec := false
	for _, rec := range records {
		if len(specifiedScenarios(rec)) > 0 {
			hasSpec = true
			break
		}
	}
	var lockBefore []byte
	if hasSpec {
		lockBefore, _ = os.ReadFile(filepath.Join(reg.Root, spec.LockName))
	}
	validateInputs := func() error {
		if hasSpec {
			lockAfter, _ := os.ReadFile(filepath.Join(reg.Root, spec.LockName))
			if !bytes.Equal(lockBefore, lockAfter) {
				return fmt.Errorf("specification lock changed during execution")
			}
		}
		if reg.SourceView == nil {
			return nil
		}
		if e := reg.SourceView.Validate(); e != nil {
			return fmt.Errorf("test inputs changed during execution: %w", e)
		}
		hashes, e := registryRecordHashes(reg)
		if e != nil {
			return fmt.Errorf("registry inputs changed during execution: %w", e)
		}
		if !reflect.DeepEqual(hashes, result.RecordHashes) {
			return fmt.Errorf("registry record inputs changed during execution")
		}
		configAfter, _ := os.ReadFile(filepath.Join(reg.Root, ConfigName))
		if !bytes.Equal(configBefore, configAfter) {
			return fmt.Errorf("registry configuration inputs changed during execution")
		}
		if result.RegistrySnapshot != nil {
			revision, _, e := GitTreeState(reg.Root)
			if e != nil || revision != result.RegistrySnapshot.Revision {
				return fmt.Errorf("registry revision changed during execution")
			}
		}
		return nil
	}
	rejectChanged := func(e error) error {
		result.InputProblem = e.Error()
		for i := range result.Runners {
			result.Runners[i].ReportProblem = e.Error()
			for path := range result.Runners[i].Results {
				result.Runners[i].Results[path] = TestUnmeasured
			}
		}
		result.Features = nil
		return e
	}
	discovery, err := checkTests(ctx, cfg, reg, selected, update, true, hasSpec)
	result.Discovery = discovery
	if e := validateInputs(); e != nil {
		return result, rejectChanged(e)
	}
	if err != nil {
		return result, err
	}
	root, err := filepath.Abs(reg.Root)
	if err != nil {
		return result, err
	}
	runners := cfg.Runners
	if cfg.MultiRegistry() {
		runners = cfg.Registries[reg.Name].Runners
	}
	var specPlan specTestPlan
	var specResults map[string][]*ScenarioTestResult
	if hasSpec {
		var cleanup func()
		specPlan, specResults, cleanup = prepareSpecTests(ctx, reg, records, selected)
		defer cleanup()
	}
	protected := map[string]bool{ConfigName: true, "go.mod": true}
	if hasSpec {
		protected[spec.LockName] = true
	}
	for _, d := range discovery.Runners {
		for _, p := range d.Files {
			protected[p] = true
		}
	}
	// Scope limits reporting, never the source/report-path safety boundary.
	protectedRecords, err := reg.LoadFeatures()
	if err != nil {
		return result, err
	}
	for _, rec := range protectedRecords {
		if rel, e := filepath.Rel(root, rec.Path); e == nil {
			protected[filepath.ToSlash(rel)] = true
		}
		verification, _ := rec.Fields["verification"].(map[string]any)
		for _, kind := range verificationKinds {
			entries, _ := verification[kind].([]any)
			for _, entry := range entries {
				ref, _ := entry.(map[string]any)
				id, _ := ref["id"].(string)
				if path, ok := verificationPath(id); ok {
					if rel, e := selectedTestReference(reg, path); e == nil {
						protected[rel] = true
					}
				}
			}
		}
	}
	protectedFiles := make([]string, 0, len(protected))
	for p := range protected {
		protectedFiles = append(protectedFiles, p)
	}
	sort.Strings(protectedFiles)
	combined := map[string]string{}
	incomplete := map[string]bool{}
	for _, d := range discovery.Runners {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		r := runners[d.Name]
		runnerRoot, repository, e := selectedRunnerRoot(reg, r)
		if e != nil {
			return result, e
		}
		runnerFiles := d.Files
		runnerProtected := protectedFiles
		if repository != "" {
			runnerFiles = nil
			runnerProtected = []string{ConfigName, "go.mod"}
			for _, ref := range d.Files {
				src, rel, e := reg.SourceView.ResolveReference(ref)
				if e != nil {
					return result, e
				}
				if src.Repository != repository {
					return result, fmt.Errorf("runner %q references another repository", d.Name)
				}
				runnerFiles = append(runnerFiles, rel)
			}
			for _, ref := range protectedFiles {
				src, rel, e := reg.SourceView.ResolveReference(ref)
				if e == nil && src.Repository == repository {
					runnerProtected = append(runnerProtected, rel)
				}
			}
		}
		execution, e := executeRunner(ctx, runnerRoot, d.Name, r, runnerFiles, runnerProtected, specPlan[d.Name], stdout, stderr)
		markSpecReportUnavailable(specPlan[d.Name], execution.ReportProblem)
		if repository != "" {
			portable := map[string]string{}
			for path, state := range execution.Results {
				qualified, e := reg.SourceView.Qualify(repository, path)
				if e != nil {
					return result, e
				}
				portable[qualified] = state
			}
			execution.Results = portable
		}
		result.Runners = append(result.Runners, execution)
		if changed := validateInputs(); changed != nil {
			return result, rejectChanged(changed)
		}
		if e != nil {
			return result, e
		}
		if !d.Measured {
			continue
		}
		for _, file := range d.Files {
			state := execution.Results[file]
			if state == "" {
				state = TestUnmeasured
			}
			if state == TestUnmeasured {
				incomplete[file] = true
			}
			if state == TestFailed || combined[file] != TestFailed {
				combined[file] = state
			}
		}
	}
	if e := validateInputs(); e != nil {
		return result, rejectChanged(e)
	}
	for _, rec := range records {
		feature := FeatureTestResult{ID: rec.ID, State: TestUnmeasured, Files: map[string]string{}}
		verification, _ := rec.Fields["verification"].(map[string]any)
		for _, kind := range verificationKinds {
			entries, _ := verification[kind].([]any)
			for _, entry := range entries {
				ref, _ := entry.(map[string]any)
				id, _ := ref["id"].(string)
				if path, ok := verificationPath(id); ok {
					rel, e := selectedTestReference(reg, path)
					if e != nil {
						feature.Files[filepath.ToSlash(filepath.Clean(path))] = TestUnmeasured
						continue
					}
					state := combined[rel]
					if state == "" || incomplete[rel] && state != TestFailed {
						state = TestUnmeasured
					}
					feature.Files[rel] = state
				}
			}
		}
		for _, scenario := range specResults[rec.ID] {
			feature.Scenarios = append(feature.Scenarios, *scenario)
		}
		hasScenarioEvidence := slices.ContainsFunc(feature.Scenarios, func(s ScenarioTestResult) bool {
			return s.State != TestProvenance
		})
		if len(feature.Files) > 0 || hasScenarioEvidence {
			feature.State = TestPassed
			for _, state := range feature.Files {
				if state == TestFailed {
					feature.State = TestFailed
					break
				}
				if state == TestUnmeasured {
					feature.State = TestUnmeasured
				}
			}
		}
		if len(feature.Scenarios) > 0 && len(feature.Files) == 0 && !hasScenarioEvidence {
			feature.State = TestProvenance
		}
		for _, scenario := range feature.Scenarios {
			switch scenario.State {
			case TestFailed:
				feature.State = TestFailed
			case TestPassed, TestProvenance:
			default:
				if feature.State != TestFailed {
					feature.State = TestUnmeasured
				}
			}
		}
		result.Features = append(result.Features, feature)
	}
	sort.Slice(result.Features, func(i, j int) bool { return result.Features[i].ID < result.Features[j].ID })
	if e := validateInputs(); e != nil {
		return result, rejectChanged(e)
	}
	// Ratchet cleanup is our own metadata write, after all evidence input guards.
	if discovery.applyBaseline != nil {
		if e := discovery.applyBaseline(); e != nil {
			return result, rejectChanged(e)
		}
	}
	return result, nil
}
