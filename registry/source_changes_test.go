// Test fixtures contain anchor syntax; @facet-ignore-file
package facet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sourceWorkspace(t *testing.T) (string, Registry, string, string) {
	t.Helper()
	root, reg := gitProject(t, map[string]string{
		".gitignore":   "platform/\ncustomers/\n",
		quickAddRecord: driftRecord + "intent:\n  summary: platform/backend/src/pay.ts\n",
	}, nil)
	repos := []string{"platform/backend", "customers/x"}
	for _, rel := range repos {
		repo, _ := gitProject(t, map[string]string{"src/pay.ts": "// @facet: tasks/create/quick-add\nexport function pay() { return 1; }\n"}, nil)
		target := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(repo, target); err != nil {
			t.Fatal(err)
		}
	}
	reg.Sources = repos
	return root, reg, filepath.Join(root, repos[0]), filepath.Join(root, repos[1])
}

// Removing repository discovery makes this fail: the outer repo has no diff.
func TestSourceRepositoriesReportCommittedChangesAfterReview(t *testing.T) {
	root, reg, backend, customer := sourceWorkspace(t)
	for _, repo := range []string{backend, customer} {
		if _, err := AcceptReview(repo, nil, "behavior-unchanged"); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(backend, "src/pay.ts"), []byte("// @facet: tasks/create/quick-add\nexport function pay() { return 2; }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, backend, "commit", "-qam", "incoming")
	if status := gitIn(t, root, "status", "--porcelain"); status != "" {
		t.Fatalf("outer repo not clean: %s", status)
	}
	report, err := ComputeDrift(root, []Registry{reg}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Drift) != 1 || report.Drift[0].Path != "platform/backend/src/pay.ts" {
		t.Fatalf("missed incoming commit: %+v", report)
	}
	if len(report.Repositories) != 3 {
		t.Fatalf("missing baselines: %+v", report.Repositories)
	}
	for _, b := range report.Repositories {
		if b.Path != "." && b.Baseline.Kind != BaselineAccepted {
			t.Fatalf("wrong source baseline: %+v", b)
		}
	}
	head := gitIn(t, backend, "rev-parse", "HEAD")
	explicit, err := ComputeDriftInRepository(root, []Registry{reg}, strings.TrimSpace(head)+"~1", "platform/backend")
	if err != nil || len(explicit.Drift) != 1 || explicit.Baseline.Status != BaselineOK {
		t.Fatalf("clone SHA interpreted in outer repo: %+v, %v", explicit, err)
	}
}

// A declared source that is a submodule is compared in its own repository.
// The superproject sees a commit inside it only as one changed gitlink, a
// path that carries no anchor, so drift reported nothing at all. Named
// runnable mutation: restore the superproject early return in
// SourceRepositories — the drift and repository assertions go red.
func TestADeclaredSubmoduleIsComparedInItsOwnRepository(t *testing.T) {
	upstream, _ := gitProject(t, map[string]string{"src/pay.ts": "// @facet: tasks/create/quick-add\nexport function pay() { return 1; }\n"}, nil)
	root, reg := gitProject(t, map[string]string{
		quickAddRecord: driftRecord + "intent:\n  summary: platform/backend/src/pay.ts\n",
	}, nil)
	gitIn(t, root, "-c", "protocol.file.allow=always", "submodule", "add", "-q", upstream, "platform/backend")
	gitIn(t, root, "commit", "-qm", "add submodule")
	backend := filepath.Join(root, "platform/backend")
	gitIn(t, backend, "config", "user.email", "test@example.invalid")
	gitIn(t, backend, "config", "user.name", "Test")
	// The fixture is a gitlink, not an ignored clone, or this proves nothing.
	if tree := gitIn(t, root, "ls-tree", "HEAD", "platform/backend"); !strings.HasPrefix(tree, "160000 commit ") {
		t.Fatalf("platform/backend is not a submodule: %q", tree)
	}
	reg.Sources = []string{"platform/backend"}
	for _, repo := range []string{root, backend} {
		if _, err := AcceptReview(repo, nil, "behavior-unchanged"); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.WriteFile(filepath.Join(backend, "src/pay.ts"), []byte("// @facet: tasks/create/quick-add\nexport function pay() { return 2; }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, backend, "commit", "-qam", "incoming")
	// What the superproject sees: the gitlink, and no file inside it.
	if status := strings.TrimSpace(gitIn(t, root, "status", "--porcelain")); status != "M platform/backend" {
		t.Fatalf("the superproject should see only the gitlink: %q", status)
	}

	report, err := ComputeDrift(root, []Registry{reg}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Drift) != 1 || report.Drift[0].Path != "platform/backend/src/pay.ts" || report.Drift[0].ID != quickAddID {
		t.Fatalf("a commit inside the submodule reached no record: %+v", report.Drift)
	}
	var paths []string
	for _, b := range report.Repositories {
		paths = append(paths, b.Path)
		if b.Path == "platform/backend" && b.Baseline.Kind != BaselineAccepted {
			t.Fatalf("the submodule does not keep its own accepted baseline: %+v", b)
		}
	}
	if strings.Join(paths, ",") != ".,platform/backend" {
		t.Fatalf("the submodule is not its own repository entry: %v", paths)
	}

	if _, err := ReviewSourceRepositories(root, []Registry{reg}, "platform/backend", nil, "behavior-unchanged", false); err != nil {
		t.Fatal(err)
	}
	after, err := ComputeDrift(root, []Registry{reg}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Drift) != 0 {
		t.Fatalf("accepting the submodule did not clear its change: %+v", after.Drift)
	}
}

// A workspace that is itself a submodule of a larger project still compares
// itself. The superproject check dropped the root too, leaving only the
// declared clones. Named runnable mutation: the same early return as above —
// the root's change goes missing.
func TestAWorkspaceThatIsASubmoduleStillComparesItself(t *testing.T) {
	// The product repository: registry, an anchored file, one ignored clone.
	product, _ := gitProject(t, map[string]string{
		".gitignore":   "platform/\n",
		quickAddRecord: driftRecord + "intent:\n  summary: src/app.ts\n",
		"src/app.ts":   "// @facet: tasks/create/quick-add\nexport const app = 1;\n",
	}, nil)
	outer, _ := gitProject(t, map[string]string{"README.md": "outer\n"}, nil)
	gitIn(t, outer, "-c", "protocol.file.allow=always", "submodule", "add", "-q", product, "product")
	gitIn(t, outer, "commit", "-qm", "add product")
	root := filepath.Join(outer, "product")
	if parent := strings.TrimSpace(gitIn(t, root, "rev-parse", "--show-superproject-working-tree")); parent == "" {
		t.Fatal("the workspace is not a submodule, so this proves nothing")
	}
	gitIn(t, root, "config", "user.email", "test@example.invalid")
	gitIn(t, root, "config", "user.name", "Test")
	clone, _ := gitProject(t, map[string]string{"src/pay.ts": "export const pay = 1;\n"}, nil)
	if err := os.MkdirAll(filepath.Join(root, "platform"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(clone, filepath.Join(root, "platform/backend")); err != nil {
		t.Fatal(err)
	}
	// Declared sources take the per-repository path through SourceRepositories.
	// The workspace's own code must be claimed too: a registry that declares
	// sources answers only for those paths (Registry.Claims).
	reg := Open(root)
	reg.Sources = []string{".", "platform/backend"}
	for _, repo := range []string{root, filepath.Join(root, "platform/backend")} {
		if _, err := AcceptReview(repo, nil, "behavior-unchanged"); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.WriteFile(filepath.Join(root, "src/app.ts"), []byte("// @facet: tasks/create/quick-add\nexport const app = 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "commit", "-qam", "change in the workspace itself")

	report, err := ComputeDrift(root, []Registry{reg}, "")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, b := range report.Repositories {
		paths = append(paths, b.Path)
	}
	if strings.Join(paths, ",") != ".,platform/backend" {
		t.Fatalf("the workspace dropped itself from the comparison: %v", paths)
	}
	if len(report.Drift) != 1 || report.Drift[0].Path != "src/app.ts" {
		t.Fatalf("a change in the workspace itself was not reported: %+v", report.Drift)
	}
}

func TestSourceReviewAcceptsOnlyNamedWorkspacePaths(t *testing.T) {
	root, reg, backend, customer := sourceWorkspace(t)
	for _, repo := range []string{backend, customer} {
		if _, err := AcceptReview(repo, nil, "behavior-unchanged"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, "src/pay.ts"), []byte("// @facet: tasks/create/quick-add\nexport function pay() { return 2; }\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	beforeIndex := gitIn(t, backend, "ls-files", "--stage")
	beforeHead := gitIn(t, backend, "rev-parse", "HEAD")
	baselines, err := ReviewSourceRepositories(root, []Registry{reg}, "", []string{"platform/backend/src/pay.ts"}, "record-updated", false)
	if err != nil || len(baselines) != 1 || baselines[0].Path != "platform/backend" {
		t.Fatalf("accept: %+v %v", baselines, err)
	}
	if gitIn(t, backend, "ls-files", "--stage") != beforeIndex || gitIn(t, backend, "rev-parse", "HEAD") != beforeHead {
		t.Fatal("accept changed index or branch")
	}
	report, err := ComputeDrift(root, []Registry{reg}, "")
	if err != nil || len(report.Drift) != 1 || report.Drift[0].Path != "customers/x/src/pay.ts" {
		t.Fatalf("partial acceptance hid other changes: %+v %v", report, err)
	}
	if _, err := ReviewSourceRepositories(root, []Registry{reg}, "missing", nil, "behavior-unchanged", false); err == nil {
		t.Fatal("unknown repository accepted")
	}
	if _, err := ReviewSourceRepositories(root, []Registry{reg}, "platform/backend", []string{"../outside"}, "behavior-unchanged", false); err == nil {
		t.Fatal("escaping path accepted")
	}
	if _, err := ReviewSourceRepositories(root, []Registry{reg}, "customers/x", nil, "", true); err != nil {
		t.Fatal(err)
	}
	if LoadChangeSet(customer, "").Baseline.Kind != BaselineHead || LoadChangeSet(backend, "").Baseline.Kind != BaselineAccepted {
		t.Fatal("reset affected the wrong repository")
	}
}

func TestSourceRepositoriesReportRenamesAndDeletions(t *testing.T) {
	root, reg, backend, customer := sourceWorkspace(t)
	gitIn(t, backend, "mv", "src/pay.ts", "src/new.ts")
	gitIn(t, customer, "rm", "src/pay.ts")
	report, err := ComputeDrift(root, []Registry{reg}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.ReferenceChanges) != 2 {
		t.Fatalf("lost source associations: %+v", report)
	}
	if len(report.StaleReferences) != 1 || report.StaleReferences[0].Path != "platform/backend/src/pay.ts" || report.StaleReferences[0].RenamedTo != "platform/backend/src/new.ts" {
		t.Fatalf("lost workspace paths: %+v", report.StaleReferences)
	}
	for _, ref := range report.ReferenceChanges {
		if ref.ID != quickAddID || ref.Evidence != EvidenceBaseline || (!strings.HasPrefix(ref.Path, "platform/") && !strings.HasPrefix(ref.Path, "customers/")) {
			t.Fatalf("wrong reference: %+v", ref)
		}
	}
}

func TestIgnoredSourceClonesRequireExplicitSources(t *testing.T) {
	root, reg, _, _ := sourceWorkspace(t)
	implicit, err := ScanAnchors(root, ScanOptions{})
	if err != nil || len(implicit) != 0 {
		t.Fatalf("implicit scan entered ignored clone: %+v %v", implicit, err)
	}
	explicit, err := ScanAnchors(root, reg.ScanOptions())
	if err != nil || len(explicit) != 2 {
		t.Fatalf("explicit sources missing anchors: %+v %v", explicit, err)
	}
}

func TestSourceRepositoryFailureIsNotAHiddenCleanResult(t *testing.T) {
	root, reg, backend, customer := sourceWorkspace(t)
	for _, repo := range []string{backend, customer} {
		if _, err := AcceptReview(repo, nil, "behavior-unchanged"); err != nil {
			t.Fatal(err)
		}
	}
	// A persisted accepted ref pointing to a blob must not silently become HEAD.
	blob := strings.TrimSpace(gitIn(t, customer, "rev-parse", "HEAD:src/pay.ts"))
	gitIn(t, customer, "update-ref", AcceptedBaselineRef, blob)
	report, err := ComputeDrift(root, []Registry{reg}, "")
	if err != nil || report.Baseline.Status != BaselineMissing {
		t.Fatalf("unavailable clone called clean: %+v %v", report, err)
	}
	found := false
	for _, b := range report.Repositories {
		if b.Path == "customers/x" {
			found = b.Baseline.Kind == BaselineAccepted && b.Baseline.Status == BaselineMissing
		}
	}
	if !found {
		t.Fatalf("missing failed source baseline: %+v", report.Repositories)
	}
	original := strings.TrimSpace(gitIn(t, backend, "rev-parse", AcceptedBaselineRef))
	if _, err := ReviewSourceRepositories(root, []Registry{reg}, "", nil, "record-updated", false); err == nil {
		t.Fatal("accepted an incomplete comparison")
	}
	if strings.TrimSpace(gitIn(t, backend, "rev-parse", AcceptedBaselineRef)) != original {
		t.Fatal("preflight failure moved another repo's baseline")
	}
	if _, err := ReviewSourceRepositories(root, []Registry{reg}, "customers/x", nil, "", true); err != nil {
		t.Fatal(err)
	}
	if LoadChangeSet(customer, "").Baseline.Kind != BaselineHead {
		t.Fatal("reset could not repair broken source review")
	}
}

func TestSourceChangesWithoutAnOuterGitRepository(t *testing.T) {
	root, reg, backend, _ := sourceWorkspace(t)
	if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	if _, err := AcceptReview(backend, nil, "behavior-unchanged"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backend, "src/pay.ts"), []byte("// @facet: tasks/create/quick-add\nexport function pay() { return 4; }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	report, err := ComputeDrift(root, []Registry{reg}, "")
	if err != nil || report.Baseline.Status != BaselineOK || len(report.Drift) != 1 {
		t.Fatalf("non-git workspace lost clone history: %+v %v", report, err)
	}
	if len(report.Repositories) != 2 {
		t.Fatalf("wrong repository set: %+v", report.Repositories)
	}
}

func TestMixedSourceBaselinesApplyTouchedRecordHeuristicPerRepository(t *testing.T) {
	root, reg, backend, customer := sourceWorkspace(t)
	if _, err := AcceptReview(backend, nil, "behavior-unchanged"); err != nil {
		t.Fatal(err)
	}
	for _, repo := range []string{backend, customer} {
		if err := os.WriteFile(filepath.Join(repo, "src/pay.ts"), []byte("// @facet: tasks/create/quick-add\nexport function pay() { return 5; }\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	record := filepath.Join(root, quickAddRecord)
	if err := os.WriteFile(record, []byte(driftRecord+"intent:\n  summary: updated with source\n"), 0644); err != nil {
		t.Fatal(err)
	}
	report, err := ComputeDrift(root, []Registry{reg}, "")
	if err != nil || len(report.Drift) != 1 || report.Drift[0].Path != "platform/backend/src/pay.ts" {
		t.Fatalf("mixed comparison did not preserve per-repo policy: %+v %v", report, err)
	}
}

func TestUnreadableSourceEvidenceSurvivesDisjointRegistryComparisons(t *testing.T) {
	root, reg, _, _ := sourceWorkspace(t)
	cs := LoadSourceChangeSet(root, []Registry{reg}, "")
	for i := range cs.sources {
		if cs.sources[i].prefix == "platform/backend" {
			cs.sources[i].changes.Unreadable = []string{"src/pay.ts"}
		}
	}
	// An already-observed failed before-state read must remain in the aggregate
	// when a later registry does not claim that source repository.
	first, second := reg, reg
	first.Name, first.Sources = "backend", []string{"platform/backend"}
	second.Name, second.Sources = "customer", []string{"customers/x"}
	cs.ReferenceChanges(first, nil, knownQuickAdd())
	cs.ReferenceChanges(second, nil, knownQuickAdd())
	if len(cs.Unreadable) != 1 || cs.Unreadable[0] != "platform/backend/src/pay.ts" {
		t.Fatalf("evidence disappeared after disjoint registry: %+v", cs.Unreadable)
	}
	cs.ReferenceChanges(first, nil, knownQuickAdd())
	if len(cs.Unreadable) != 1 || cs.Unreadable[0] != "platform/backend/src/pay.ts" {
		t.Fatalf("evidence disappeared or duplicated: %+v", cs.Unreadable)
	}
}
