// Real git fixtures include test anchors. @facet-ignore-file
package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/facet-framework/core/registry"
)

func TestSourceDriftCLIUsesCloneBaselineAndAcceptsWorkspacePath(t *testing.T) {
	root := driftFixture(t, map[string]string{
		".gitignore":    "platform/\n",
		".facetrc.yaml": "sources: [platform/backend]\n",
		"docs/facets/features/tasks/create/quick-add.yaml": driftFixtureRecord,
	}, nil)
	nested := driftFixture(t, map[string]string{"pay.go": "// @facet: tasks/create/quick-add\npackage pay\nfunc Pay() {}\n"}, nil)
	repo := filepath.Join(root, "platform/backend")
	if err := os.MkdirAll(filepath.Dir(repo), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(nested, repo); err != nil {
		t.Fatal(err)
	}
	runDrift(t, "accept", "--repository", "platform/backend", "--root", root)
	write(t, filepath.Join(repo, "pay.go"), "// @facet: tasks/create/quick-add\npackage pay\nfunc Pay() { println(1) }\n")
	git(t, repo, "commit", "-qam", "pulled change")
	var report facet.DriftReport
	if err := json.Unmarshal([]byte(runDrift(t, "--json", "--root", root)), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Drift) != 1 || report.Drift[0].Path != "platform/backend/pay.go" || len(report.Repositories) != 2 {
		t.Fatalf("missing clone: %+v", report)
	}
	text := runDrift(t, "--root", root)
	if !strings.Contains(text, "Repository: platform/backend — Baseline: accepted review") {
		t.Fatalf("text hides clone baseline: %s", text)
	}
	msg := runStopHook(t, facetBinary(t), root)
	if !strings.Contains(msg, "tasks/create/quick-add") || !strings.Contains(msg, "platform/backend/pay.go") {
		t.Fatalf("Stop hook missed source clone: %s", msg)
	}
	// A clone-specific SHA must be resolved in the selected clone.
	parent := strings.TrimSpace(git(t, repo, "rev-parse", "HEAD~1"))
	selected := runDrift(t, "--root", root, "--json", "--repository", "platform/backend", "--baseline", parent)
	if err := json.Unmarshal([]byte(selected), &report); err != nil || len(report.Drift) != 1 || report.Baseline.Kind != facet.BaselineExplicit {
		t.Fatalf("selected clone baseline: %s %v", selected, err)
	}
	runDrift(t, "accept", "--root", root, "--outcome", "record-updated", "platform/backend/pay.go")
	if err := json.Unmarshal([]byte(runDrift(t, "--json", "--root", root)), &report); err != nil || len(report.Drift) != 0 {
		t.Fatalf("accept did not clear clone drift: %+v %v", report, err)
	}
	runDrift(t, "reset", "--root", root, "--repository", "platform/backend")
	if facet.LoadChangeSet(repo, "").Baseline.Kind != facet.BaselineHead {
		t.Fatal("clone reset missed its review ref")
	}
}

func TestPrivateFilterStatusSeparatesLocalForeignAndUnknownOrigins(t *testing.T) {
	bin, mine, _ := twoClones(t)
	write(t, filepath.Join(mine, "x.go"), annotatedFile)
	git(t, mine, "add", "x.go")
	wt := filepath.Join(filepath.Dir(mine), "feature-origin")
	git(t, mine, "worktree", "add", "-q", wt, "-b", "feature-origin")
	// This declaration and its anchor exist only in the linked worktree.
	write(t, filepath.Join(wt, "x.go"), annotatedFile+"\n// @facet: c/d/foreign\nfunc OnlyHere() {}\n")
	git(t, wt, "add", "x.go")
	gitDir, err := facet.GitCommonDir(mine)
	if err != nil {
		t.Fatal(err)
	}
	pa, err := facet.LoadPrivateAnchors(gitDir, "x.go")
	if err != nil {
		t.Fatal(err)
	}
	pa.Blocks = append(pa.Blocks, facet.AnchorBlock{Lines: []string{"// @facet: c/d/legacy"}, Before: "func Gone() {}"})
	if err := facet.SavePrivateAnchors(gitDir, pa); err != nil {
		t.Fatal(err)
	}
	// Remove the main worktree's original placement to exercise all 3 groups.
	write(t, filepath.Join(mine, "x.go"), strings.ReplaceAll(committedFile, "func Move() {}", "func Renamed() {}"))
	status := facetRun(t, bin, mine, "filter", "status", "--root", mine)
	for _, want := range []string{"1 without a place from this worktree", "1 absent here from another worktree", "1 absent here with unknown origin", "originated in worktree", "keep the shared sidecar"} {
		if !strings.Contains(status, want) {
			t.Fatalf("missing %q: %s", want, status)
		}
	}
	after, err := facet.LoadPrivateAnchors(gitDir, "x.go")
	if err != nil || !reflect.DeepEqual(after, pa) {
		t.Fatalf("status removed shared anchors: %+v %v", after, err)
	}
	for _, b := range after.Blocks {
		if strings.Contains(strings.Join(b.Lines, "\n"), "a/b/private") && b.OriginWorktree != "." {
			t.Fatalf("shared checkout changed original provenance: %+v", b)
		}
	}
	// Verify the linked worktree can still smudge the foreign block.
	cmd := exec.Command(bin, "filter", "smudge", "x.go")
	cmd.Dir = wt
	cmd.Stdin = strings.NewReader(committedFile + "\nfunc OnlyHere() {}\n")
	out, err := cmd.Output()
	if err != nil || !strings.Contains(string(out), "c/d/foreign") {
		t.Fatalf("foreign anchor lost: %s %v", out, err)
	}
}

func TestCatchUpPartialAcceptanceFromExplicitRevisionKeepsOtherIncomingCommits(t *testing.T) {
	root := driftFixture(t, map[string]string{
		"docs/facets/features/tasks/create/quick-add.yaml": driftFixtureRecord,
		"src/a.go": "// @facet: tasks/create/quick-add\npackage pay\nfunc A() {}\n",
		"src/b.go": "// @facet: tasks/create/quick-add\npackage pay\nfunc B() {}\n",
	}, nil)
	reviewed := strings.TrimSpace(git(t, root, "rev-parse", "HEAD"))
	for _, file := range []string{"a", "b"} {
		write(t, filepath.Join(root, "src", file+".go"), "// @facet: tasks/create/quick-add\npackage pay\nfunc "+strings.ToUpper(file)+"() { println(1) }\n")
	}
	git(t, root, "commit", "-qam", "incoming changes")
	if facet.LoadChangeSet(root, "").Baseline.Kind != facet.BaselineHead {
		t.Fatal("fixture unexpectedly reviewed")
	}
	runDrift(t, "accept", "--root", root, "--baseline", reviewed, "--outcome", "record-updated", "src/a.go")
	var report facet.DriftReport
	if err := json.Unmarshal([]byte(runDrift(t, "--root", root, "--json")), &report); err != nil {
		t.Fatal(err)
	}
	if report.Baseline.Kind != facet.BaselineAccepted || len(report.Drift) != 1 || report.Drift[0].Path != "src/b.go" {
		t.Fatalf("first acceptance silently certified other incoming changes: %+v", report)
	}
	runDrift(t, "accept", "--root", root, "src/b.go")
	if err := json.Unmarshal([]byte(runDrift(t, "--root", root, "--json")), &report); err != nil || len(report.Drift) != 0 {
		t.Fatalf("next partial acceptance lost first review: %+v %v", report, err)
	}
}

func TestSourceClonePrivateDeletionReachesLintAndStop(t *testing.T) {
	root := driftFixture(t, map[string]string{
		".gitignore":    "platform/\n",
		".facetrc.yaml": "sources: [platform/backend]\n",
		"docs/facets/features/tasks/create/quick-add.yaml": driftFixtureRecord,
	}, nil)
	nested := driftFixture(t, map[string]string{"pay.go": "package pay\nfunc Pay() {}\n"}, nil)
	repo := filepath.Join(root, "platform/backend")
	if err := os.MkdirAll(filepath.Dir(repo), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(nested, repo); err != nil {
		t.Fatal(err)
	}
	bin := facetBinary(t)
	facetRun(t, bin, repo, "filter", "install", "--root", repo)
	write(t, filepath.Join(repo, "pay.go"), "package pay\n// @facet: tasks/create/quick-add\n// @facet-role: primary\nfunc Pay() {}\n")
	git(t, repo, "add", "pay.go")
	if strings.Contains(git(t, repo, "show", "HEAD:pay.go"), "@facet:") {
		t.Fatal("private anchor leaked into baseline")
	}
	facetRun(t, bin, root, "drift", "accept", "--repository", "platform/backend", "--root", root)
	gitDir, err := facet.GitCommonDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	before, err := facet.LoadPrivateAnchors(gitDir, "pay.go")
	if err != nil || len(before.Blocks) == 0 {
		t.Fatalf("missing private sidecar fixture: %+v %v", before, err)
	}
	if err := os.Remove(filepath.Join(repo, "pay.go")); err != nil {
		t.Fatal(err)
	}
	report := driftJSON(t, bin, root)
	if len(report.ReferenceChanges) != 1 || report.ReferenceChanges[0].Path != "platform/backend/pay.go" || report.ReferenceChanges[0].Evidence != facet.EvidencePrivateSidecar {
		t.Fatalf("clone sidecar not recovered: %+v", report)
	}
	lint := exec.Command(bin, "lint", "--json", "--root", root, "--skip", "schema")
	out, _ := lint.Output() // the deleted primary anchor is an orphan-record error
	var rep facet.Report
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("invalid lint JSON: %s %v", out, err)
	}
	found := false
	for _, f := range rep.Findings {
		found = found || (f.Check == "lost-anchors" && f.ID == "tasks/create/quick-add" && f.Path == "platform/backend/pay.go")
	}
	if !found {
		t.Fatalf("lint did not compare declared clone: %+v", rep.Findings)
	}
	msg := runStopHook(t, bin, root)
	if !strings.Contains(msg, "tasks/create/quick-add") || !strings.Contains(msg, "platform/backend/pay.go") || !strings.Contains(msg, "[private anchor]") {
		t.Fatalf("Stop did not recover clone reference: %s", msg)
	}
	after, err := facet.LoadPrivateAnchors(gitDir, "pay.go")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("review removed shared sidecar: %+v %v", after, err)
	}
}

// A commit inside a declared submodule reaches every consumer through the
// real binary: 'facet drift --json', 'facet drift accept --repository' and the
// Stop hook fed real hook JSON. Before, the superproject saw one changed
// gitlink and all three reported nothing. Named runnable mutation: restore the
// superproject early return in facet.SourceRepositories — this goes red.
func TestADeclaredSubmoduleReachesDriftAcceptAndStop(t *testing.T) {
	upstream := driftFixture(t, map[string]string{"pay.go": "package pay\n// @facet: tasks/create/quick-add\n// @facet-role: primary\nfunc Pay() int { return 1 }\n"}, nil)
	root := driftFixture(t, map[string]string{
		".facetrc.yaml": "sources: [platform/backend]\n",
		"docs/facets/features/tasks/create/quick-add.yaml": driftFixtureRecord,
	}, nil)
	git(t, root, "-c", "protocol.file.allow=always", "submodule", "add", "-q", upstream, "platform/backend")
	git(t, root, "commit", "-qm", "add submodule")
	repo := filepath.Join(root, "platform/backend")
	git(t, repo, "config", "user.email", "test@example.invalid")
	git(t, repo, "config", "user.name", "Test")
	if !strings.HasPrefix(git(t, root, "ls-tree", "HEAD", "platform/backend"), "160000 commit ") {
		t.Fatal("platform/backend is not a submodule, so this proves nothing")
	}
	bin := facetBinary(t)
	facetRun(t, bin, root, "drift", "accept", "--root", root)

	write(t, filepath.Join(repo, "pay.go"), "package pay\n// @facet: tasks/create/quick-add\n// @facet-role: primary\nfunc Pay() int { return 2 }\n")
	git(t, repo, "commit", "-qam", "incoming")

	var rep struct {
		Drift []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"drift"`
		Repositories []struct {
			Path string `json:"path"`
		} `json:"repositories"`
	}
	if err := json.Unmarshal([]byte(facetRun(t, bin, root, "drift", "--json", "--root", root)), &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Drift) != 1 || rep.Drift[0].ID != "tasks/create/quick-add" || rep.Drift[0].Path != "platform/backend/pay.go" {
		t.Fatalf("drift did not see the commit inside the submodule: %+v", rep.Drift)
	}
	if len(rep.Repositories) != 2 || rep.Repositories[1].Path != "platform/backend" {
		t.Fatalf("the submodule is not its own repository: %+v", rep.Repositories)
	}
	msg := runStopHook(t, bin, root)
	if !strings.Contains(msg, "tasks/create/quick-add") || !strings.Contains(msg, "platform/backend/pay.go") {
		t.Fatalf("the Stop hook did not report the submodule change: %s", msg)
	}

	out := facetRun(t, bin, root, "drift", "accept", "--repository", "platform/backend", "--root", root)
	if !strings.Contains(out, "[repository: platform/backend]") {
		t.Fatalf("accept did not reach the submodule: %s", out)
	}
	if ref := strings.TrimSpace(git(t, repo, "for-each-ref", "--format=%(refname)", "refs/worktree/facet")); ref != "refs/worktree/facet/drift-baseline" {
		t.Fatalf("the review ref was not written in the submodule: %q", ref)
	}
	rep.Drift = nil
	if err := json.Unmarshal([]byte(facetRun(t, bin, root, "drift", "--json", "--root", root)), &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Drift) != 0 {
		t.Fatalf("accepting the submodule left its change reported: %+v", rep.Drift)
	}
}
