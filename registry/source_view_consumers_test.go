// @facet-ignore-file
package facet

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/facet-framework/core/internal/sourceview"
)

func consumerView(t *testing.T, root, checkout string) *sourceview.ResolvedView {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".facet"), 0755); err != nil {
		t.Fatal(err)
	}
	// Quoted paths keep Windows drive colons and spaces ordinary YAML scalars.
	data := "bindings:\n  selected:\n    repository: product\n    checkout: " + strings.ReplaceAll(checkout, "\\", "/") + "\n"
	if err := os.WriteFile(filepath.Join(root, ".facet/sources.local.yaml"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	view, err := sourceview.Resolve(root, sourceview.Config{Repositories: map[string]sourceview.Repository{"product": {}}, DefaultView: "dev", Views: map[string]sourceview.View{"dev": {Sources: map[string]sourceview.Selection{"product": {Binding: "selected"}}}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func TestSelectedViewScansOnlyBoundWorktreeAndPreservesPortableIdentity(t *testing.T) {
	repo, _ := gitProject(t, map[string]string{"src/item.go": "package item\n// @facet: tasks/create/quick-add\nfunc Earlier(){}\n"}, nil)
	alternate := filepath.Join(t.TempDir(), "alternate")
	cmd := exec.Command("git", "-C", repo, "worktree", "add", "--detach", alternate, "HEAD")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("worktree: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(repo, "src/item.go"), []byte("package item\n// @facet: tasks/create/later\nfunc Later(){}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitDir, err := GitCommonDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	private := PrivateAnchors{Path: "src/item.go", Blocks: []AnchorBlock{{OriginWorktree: repo, Lines: []string{"// @facet: tasks/create/later-private"}, Before: "func Later(){}", Occurrence: 1}}}
	if err := SavePrivateAnchors(gitDir, private); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(SidecarPath(gitDir, private.Path))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	view := consumerView(t, root, alternate)
	anchors, files, err := ScanWithFiles(root, ScanOptions{View: view})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 || anchors[0].ID != "tasks/create/quick-add" || anchors[0].Reference != "product:src/item.go" {
		t.Fatalf("wrong selected anchors: %+v", anchors)
	}
	after, err := os.ReadFile(SidecarPath(gitDir, private.Path))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("view scan mutated another version's private anchors")
	}
	if len(files) != 1 || files[0].Reference != "product:src/item.go" {
		t.Fatalf("wrong files: %+v", files)
	}
	if err := os.WriteFile(filepath.Join(alternate, "src/item.go"), []byte("package changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ScanWithFiles(root, ScanOptions{View: view}); err == nil {
		t.Fatal("changed selection silently rescanned")
	}
}

func TestSelectedRunnerDiscoveryUsesBoundProductNotRegistry(t *testing.T) {
	repo, _ := gitProject(t, map[string]string{"test/check.go": "package checks\n"}, nil)
	root, reg := gitProject(t, map[string]string{quickAddRecord: driftRecord}, nil)
	reg.SourceView = consumerView(t, root, repo)
	cfg := &Config{Runners: map[string]Runner{"unit": {Repository: "product", List: "printf 'test/check.go\\n'", Command: "true"}}}
	result, err := CheckTests(context.Background(), cfg, reg, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Runners) != 1 || !result.Runners[0].Measured || len(result.Runners[0].Files) != 1 || result.Runners[0].Files[0] != "product:test/check.go" {
		t.Fatalf("wrong discovery: %+v", result)
	}
	execution, err := ExecuteTests(context.Background(), cfg, reg, "", false, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if execution.SourceRevisions["product"] == "" || execution.SourceSnapshots["product"].ContentHash == "" || execution.RecordHashes["tasks/create/quick-add"] == "" {
		t.Fatalf("missing provenance: %+v", execution)
	}
	if err := os.WriteFile(filepath.Join(repo, "test/check.go"), []byte("package different\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckTests(context.Background(), cfg, reg, "", false); err == nil {
		t.Fatal("discovery ran changed checkout")
	}
}

func TestSelectedRepositoriesKeepIdenticalRelativePathsDistinct(t *testing.T) {
	a, _ := gitProject(t, map[string]string{"src/item.go": "package a\n// @facet: tasks/create/one\n"}, nil)
	b, _ := gitProject(t, map[string]string{"src/item.go": "package b\n// @facet: tasks/create/two\n"}, nil)
	root := t.TempDir()
	if e := os.MkdirAll(filepath.Join(root, ".facet"), 0755); e != nil {
		t.Fatal(e)
	}
	local := "bindings:\n  a:\n    repository: one\n    checkout: " + a + "\n  b:\n    repository: two\n    checkout: " + b + "\n"
	if e := os.WriteFile(filepath.Join(root, ".facet/sources.local.yaml"), []byte(local), 0600); e != nil {
		t.Fatal(e)
	}
	view, e := sourceview.Resolve(root, sourceview.Config{Repositories: map[string]sourceview.Repository{"one": {}, "two": {}}, DefaultView: "dev", Views: map[string]sourceview.View{"dev": {Sources: map[string]sourceview.Selection{"one": {Binding: "a"}, "two": {Binding: "b"}}}}}, "")
	if e != nil {
		t.Fatal(e)
	}
	anchors, _, e := ScanWithFiles(root, ScanOptions{View: view})
	if e != nil {
		t.Fatal(e)
	}
	if len(anchors) != 2 || anchors[0].Reference != "one:src/item.go" || anchors[1].Reference != "two:src/item.go" {
		t.Fatalf("colliding source identities: %+v", anchors)
	}
	reg := Registry{Root: root, SourceView: view}
	if _, e := selectedTestReference(reg, "src/item.go"); e == nil {
		t.Fatal("ambiguous unqualified reference accepted")
	}
}

func TestSelectedSourceDriftReportsLogicalRepository(t *testing.T) {
	repo, _ := gitProject(t, map[string]string{"src/item.go": "// @facet: tasks/create/quick-add\npackage item\n"}, nil)
	if _, e := AcceptReview(repo, nil, "behavior-unchanged"); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(repo, "src/item.go"), []byte("// @facet: tasks/create/quick-add\npackage changed\n"), 0644); e != nil {
		t.Fatal(e)
	}
	root, reg := gitProject(t, map[string]string{quickAddRecord: driftRecord}, nil)
	reg.SourceView = consumerView(t, root, repo)
	report, e := ComputeDrift(root, []Registry{reg}, "")
	if e != nil {
		t.Fatal(e)
	}
	if len(report.Drift) != 1 || report.Drift[0].Path != "product:src/item.go" {
		t.Fatalf("nonportable drift: %+v", report)
	}
	if len(report.Repositories) != 2 || report.Repositories[0].Repository != "product" || report.Repositories[0].Path != "product" {
		t.Fatalf("nonportable baseline: %+v", report.Repositories)
	}
	reviewed, e := ReviewSourceRepositories(root, []Registry{reg}, "product", []string{"product:src/item.go"}, "behavior-unchanged", false)
	if e != nil {
		t.Fatal(e)
	}
	if len(reviewed) != 1 || reviewed[0].Repository != "product" {
		t.Fatalf("wrong reviewed identity: %+v", reviewed)
	}
	report, e = ComputeDrift(root, []Registry{reg}, "")
	if e != nil {
		t.Fatal(e)
	}
	if len(report.Drift) != 0 {
		t.Fatalf("selected review did not apply: %+v", report.Drift)
	}

}

func TestRegistryChangesRemainIndependentFromSelectedProduct(t *testing.T) {
	repo, _ := gitProject(t, map[string]string{"src/item.go": "// @facet: tasks/create/quick-add\npackage item\n"}, nil)
	root, reg := gitProject(t, map[string]string{quickAddRecord: driftRecord}, nil)
	if e := os.WriteFile(filepath.Join(repo, "src/item.go"), []byte("// @facet: tasks/create/quick-add\npackage changed\n"), 0644); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, quickAddRecord), []byte(driftRecord+"intent:\n  summary: reviewed implementation\n"), 0644); e != nil {
		t.Fatal(e)
	}
	reg.SourceView = consumerView(t, root, repo)
	reg.SourceView.RegistryRepository = "records"
	cs := LoadSourceChangeSet(root, []Registry{reg}, "")
	if !cs.FollowedRecords([]Registry{reg})["tasks/create/quick-add"] {
		t.Fatal("independent record change was not observed")
	}
	report, e := ComputeDrift(root, []Registry{reg}, "")
	if e != nil {
		t.Fatal(e)
	}
	if len(report.Drift) != 0 {
		t.Fatalf("record did not follow product change: %+v", report.Drift)
	}
	inputs := report.Repositories
	if len(inputs) != 2 || !inputs[1].RegistryInput || inputs[1].Repository != "records" || inputs[1].Path != "records" || inputs[1].RecordHashes["tasks/create/quick-add"] == "" {
		t.Fatalf("registry input missing: %+v", inputs)
	}
	// Product bytes are unchanged between these invocations; record-only changes
	// nevertheless change the independent evidence snapshot.
	cfg := &Config{Runners: map[string]Runner{"unit": {Repository: "product", List: "printf 'src/item.go\\n'", Command: "true"}}}
	before, e := ExecuteTests(context.Background(), cfg, reg, "", false, io.Discard, io.Discard)
	if e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, quickAddRecord), []byte(driftRecord+"intent:\n  summary: another record revision\n"), 0644); e != nil {
		t.Fatal(e)
	}
	after, e := ExecuteTests(context.Background(), cfg, reg, "", false, io.Discard, io.Discard)
	if e != nil {
		t.Fatal(e)
	}
	if before.RegistrySnapshot == nil || after.RegistrySnapshot == nil || before.RegistrySnapshot.ContentHash == after.RegistrySnapshot.ContentHash || before.SourceSnapshots["product"] != after.SourceSnapshots["product"] {
		t.Fatalf("record/product evidence not independent: %+v %+v", before, after)
	}

	if before.RegistrySnapshot.Revision == "" || before.RegistrySnapshot.Revision != after.RegistrySnapshot.Revision {
		t.Fatalf("registry revision was not measured or moved on an uncommitted edit: %+v %+v", before.RegistrySnapshot, after.RegistrySnapshot)
	}
	if before.RecordHashes["tasks/create/quick-add"] == "" || before.RecordHashes["tasks/create/quick-add"] == after.RecordHashes["tasks/create/quick-add"] {
		t.Fatalf("record hashes did not follow the record edit: %v %v", before.RecordHashes, after.RecordHashes)
	}
	if !after.RegistrySnapshot.Dirty {
		t.Fatalf("registry dirty flag does not reflect the uncommitted record edit: %+v", after.RegistrySnapshot)
	}
	// Committing the record edit moves the registry revision; the product
	// snapshot, whose bytes nobody touched, stays exactly where it was.
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "record revision"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	committed, e := ExecuteTests(context.Background(), cfg, reg, "", false, io.Discard, io.Discard)
	if e != nil {
		t.Fatal(e)
	}
	if committed.RegistrySnapshot == nil || committed.RegistrySnapshot.Revision == after.RegistrySnapshot.Revision || committed.RegistrySnapshot.Dirty {
		t.Fatalf("registry revision did not follow the record commit: %+v", committed.RegistrySnapshot)
	}
	if committed.RegistrySnapshot.ContentHash != after.RegistrySnapshot.ContentHash || committed.SourceSnapshots["product"] != after.SourceSnapshots["product"] {
		t.Fatalf("a record commit changed evidence it must not: %+v %+v", committed, after)
	}
	portable, e := json.Marshal(after)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(portable), root) || strings.Contains(string(portable), repo) {
		t.Fatalf("checkout path leaked into execution evidence: %s", portable)
	}

}

func TestLintUsesInvocationViewInsteadOfReloadedDefault(t *testing.T) {
	selected, _ := gitProject(t, map[string]string{"src/item.go": "// @facet: tasks/create/quick-add\npackage selected\n"}, nil)
	other, _ := gitProject(t, map[string]string{"src/item.go": "// @facet: tasks/create/other\n// @facet: tasks/create/third\npackage other\n"}, nil)
	root, reg := gitProject(t, map[string]string{quickAddRecord: driftRecord}, nil)
	if e := os.MkdirAll(filepath.Join(root, ".facet"), 0755); e != nil {
		t.Fatal(e)
	}
	local := "bindings:\n  selected:\n    repository: product\n    checkout: " + selected + "\n  other:\n    repository: product\n    checkout: " + other + "\n"
	config := "source_views:\n  repositories:\n    product: {}\n  default_view: default\n  views:\n    default:\n      sources:\n        product: {binding: other}\n    chosen:\n      sources:\n        product: {binding: selected}\n"
	if e := os.WriteFile(filepath.Join(root, ".facet/sources.local.yaml"), []byte(local), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, ConfigName), []byte(config), 0644); e != nil {
		t.Fatal(e)
	}
	cfg, e := LoadConfigWithSourceView(root, "chosen")
	if e != nil {
		t.Fatal(e)
	}
	opts := LintOptions{Scan: ScanOptions{View: cfg.All()[0].SourceView}, Skip: map[string]bool{"schema": true, "knowledge": true}, Today: "2026-11-03"}
	// Invocation already froze its bindings; later consumers must not resolve
	// the default or reread a changed/deleted machine-local binding file.
	if e := os.Remove(filepath.Join(root, ".facet/sources.local.yaml")); e != nil {
		t.Fatal(e)
	}
	report, e := LintAll(root, opts)
	if e != nil {
		t.Fatal(e)
	}
	if report.Checked.Anchors != 1 {
		t.Fatalf("default view replaced invocation: %+v", report)
	}
	reg.SourceView = Open(root).SourceView
	report, e = Lint(reg, opts)
	if e != nil {
		t.Fatal(e)
	}
	if report.Checked.Anchors != 1 {
		t.Fatalf("direct lint ignored invocation: %+v", report)
	}
}

func TestSelectedExecutionRejectsChangedInputsDespitePassingReport(t *testing.T) {
	for _, target := range []string{"product", "registry", "product-exit7"} {
		t.Run(target, func(t *testing.T) {
			repo, _ := gitProject(t, map[string]string{".gitignore": "report.xml\n", "test/check.go": "package check\n"}, nil)
			root, reg := gitProject(t, map[string]string{quickAddRecord: driftRecord + "verification:\n  unit_tests:\n    - id: product:test/check.go\n"}, nil)
			reg.SourceView = consumerView(t, root, repo)
			changed := filepath.Join(repo, "test/check.go")
			if target == "registry" {
				changed = filepath.Join(root, quickAddRecord)
			}
			command := "printf '\\n# changed during execution\\n' >> " + shellQuote(changed) + "; printf '%s' '<testsuite tests=\"1\"><testcase file=\"test/check.go\" name=\"passing\"/></testsuite>' > report.xml"
			expectedExit := 0
			if target == "product-exit7" {
				command += "; exit 7"
				expectedExit = 7
			}
			cfg := &Config{Runners: map[string]Runner{"unit": {Repository: "product", List: "printf 'test/check.go\\n'", Command: command, Report: "junit", ReportFile: "report.xml"}}}
			result, e := ExecuteTests(context.Background(), cfg, reg, "", false, io.Discard, io.Discard)
			if e == nil || result.InputProblem == "" || result.ExitCode() == 0 {
				t.Fatalf("changed %s accepted: error=%v result=%+v", target, e, result)
			}
			if len(result.Runners) != 1 || result.Runners[0].Exit != expectedExit || result.Runners[0].Results["product:test/check.go"] == TestPassed || len(result.Features) != 0 {
				t.Fatalf("passing evidence survived changed inputs: %+v", result)
			}
			if expectedExit != 0 {
				var exit interface{ ExitCode() int }
				if result.ExitCode() != expectedExit || !errors.As(e, &exit) || exit.ExitCode() != expectedExit {
					t.Fatalf("input rejection erased runner exit: result=%+v err=%v", result, e)
				}
			}

		})
	}
}

func TestSelectedExecutionAppliesOwnBaselineCleanupAfterInputGuards(t *testing.T) {
	root, reg := gitProject(t, map[string]string{".gitignore": ".facet/\nreport.xml\n", "test/check.go": "package check\n", quickAddRecord: driftRecord + "verification:\n  unit_tests:\n    - id: product:test/check.go\n", "docs/facets/test-baseline.yaml": "runners:\n  unit: [product:test/obsolete.go]\n"}, nil)
	reg.SourceView = consumerView(t, root, root)
	cfg := &Config{Runners: map[string]Runner{"unit": {Repository: "product", List: "printf 'test/check.go\\n'", Command: "printf '%s' '<testsuite tests=\"1\"><testcase file=\"test/check.go\" name=\"passing\"/></testsuite>' > report.xml", Report: "junit", ReportFile: "report.xml"}}}
	result, e := ExecuteTests(context.Background(), cfg, reg, "", true, io.Discard, io.Discard)
	if e != nil {
		t.Fatal(e)
	}
	if result.InputProblem != "" || len(result.Features) != 1 || result.Features[0].State != TestPassed {
		t.Fatalf("own metadata update invalidated execution: %+v", result)
	}
	baseline, e := readTestBaseline(filepath.Join(reg.RecordsDir, "test-baseline.yaml"))
	if e != nil {
		t.Fatal(e)
	}
	if len(baseline.Runners["unit"]) != 0 {
		t.Fatalf("stale baseline retained: %+v", baseline)
	}
}
