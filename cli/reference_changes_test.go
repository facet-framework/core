// Fixtures here contain example @facet: tags; the file drives the real binary
// through git rather than implementing a feature, so it opts out of scanning.
// @facet-ignore-file

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/facet-framework/core/registry"
)

const (
	privID     = "a/b/private"
	commitID   = "a/b/committed"
	privRecord = "schema_version: \"0.1\"\nid: a/b/private\nlifecycle: active\n"
	commRecord = "schema_version: \"0.1\"\nid: a/b/committed\nlifecycle: active\n"
)

// privateAnchorClone is twoClones plus the two records the anchors name, with
// the private block lifted out of git by the real clean filter. It asserts its
// own premise: the private tag is in the working copy and absent from HEAD.
func privateAnchorClone(t *testing.T) (bin, mine string) {
	t.Helper()
	bin, mine, theirs := twoClones(t)
	write(t, filepath.Join(theirs, "docs/facets/features/a/b/private.yaml"), privRecord)
	write(t, filepath.Join(theirs, "docs/facets/features/a/b/committed.yaml"), commRecord)
	write(t, filepath.Join(theirs, "keep.go"), "// @facet: a/b/committed\n// @facet-role: related\npackage x\n")
	git(t, theirs, "add", "-A")
	git(t, theirs, "commit", "-qm", "records")
	git(t, theirs, "push", "-q", "origin", "HEAD:master")
	git(t, mine, "pull", "-q", "origin", "master")

	write(t, filepath.Join(mine, "x.go"), annotatedFile)
	git(t, mine, "add", "x.go")
	if strings.TrimSpace(git(t, mine, "status", "--porcelain")) != "" {
		t.Fatal("precondition: after the clean filter the tree must be clean")
	}
	if strings.Contains(git(t, mine, "show", "HEAD:x.go"), privID) {
		t.Fatal("precondition: HEAD must not carry the private anchor")
	}
	if !strings.Contains(readFile(t, filepath.Join(mine, "x.go")), privID) {
		t.Fatal("precondition: the working copy must carry the private anchor")
	}
	return bin, mine
}

func driftJSON(t *testing.T, bin, dir string) facet.DriftReport {
	t.Helper()
	out := facetRun(t, bin, dir, "drift", "--json", "--root", dir)
	var r facet.DriftReport
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	return r
}

func lostIDs(r facet.DriftReport) map[string]facet.ReferenceChange {
	out := map[string]facet.ReferenceChange{}
	for _, c := range r.ReferenceChanges {
		if c.Lost() {
			out[c.ID] = c
		}
	}
	return out
}

// Deleting a file whose anchors are private recovers the association from the
// sidecar: HEAD alone has only the committed one.
func TestADeletedFileRecoversItsPrivateAnchorFromTheSidecar(t *testing.T) {
	bin, mine := privateAnchorClone(t)
	if err := os.Remove(filepath.Join(mine, "x.go")); err != nil {
		t.Fatal(err)
	}

	r := driftJSON(t, bin, mine)

	if r.Baseline.Status != facet.BaselineOK {
		t.Fatalf("baseline: %+v", r.Baseline)
	}
	lost := lostIDs(r)
	p, ok := lost[privID]
	if !ok {
		t.Fatalf("the private association must be recovered, got %+v", r.ReferenceChanges)
	}
	if p.Kind != facet.RefDeleted || p.Evidence != facet.EvidencePrivateSidecar || p.Path != "x.go" || p.Role != "related" {
		t.Errorf("private association lost its subject: %+v", p)
	}
	c, ok := lost[commitID]
	if !ok || c.Evidence != facet.EvidenceBaseline || c.Surviving != 1 {
		t.Errorf("the committed association must come from the baseline with keep.go surviving: %+v", c)
	}
}

// The sidecar is shared by every worktree of the clone. A worktree in which
// the file is gone must not erase what another worktree still holds.
func TestAnAbsentFileInOneWorktreeDoesNotEraseAnothersPrivateAnchors(t *testing.T) {
	bin, mine := privateAnchorClone(t)
	wt := filepath.Join(filepath.Dir(mine), "feature")
	git(t, mine, "worktree", "add", "-q", wt, "-b", "feature")
	if !strings.Contains(readFile(t, filepath.Join(wt, "x.go")), privID) {
		t.Fatal("precondition: the second worktree must check out the private anchor")
	}

	if err := os.Remove(filepath.Join(mine, "x.go")); err != nil {
		t.Fatal(err)
	}
	// Every consumer runs once in the worktree where the file is gone. Lint
	// exits 1 here — a/b/private has lost its only anchor, which is
	// orphan-records' error — so its exit code is not the point; that it ran
	// to the end is, and the JSON says so.
	if r := driftJSON(t, bin, mine); len(lostIDs(r)) == 0 {
		t.Fatal("precondition: the deletion must be seen in the first worktree")
	}
	lint := exec.Command(bin, "lint", "--json", "--root", mine, "--skip", "schema")
	if out, _ := lint.Output(); !strings.Contains(string(out), `"lost-anchors"`) {
		t.Fatalf("precondition: lint must have run the lost-anchors check:\n%s", out)
	}
	runStopHook(t, bin, mine)

	// The sidecar itself, read before anything runs in the second worktree:
	// any git command there passes x.go through the clean filter, which would
	// write the working copy's blocks back and hide a deletion made above.
	gitDir, err := facet.GitCommonDir(mine)
	if err != nil {
		t.Fatal(err)
	}
	pa, err := facet.LoadPrivateAnchors(gitDir, "x.go")
	if err != nil || len(pa.Blocks) == 0 {
		t.Fatalf("the shared sidecar must still hold x.go's private anchor (err %v, %+v)", err, pa)
	}

	// The second worktree still has the file and loses nothing.
	if r := driftJSON(t, bin, wt); len(lostIDs(r)) != 0 {
		t.Errorf("the worktree that kept the file lost nothing, got %+v", r.ReferenceChanges)
	}
	if err := os.Remove(filepath.Join(wt, "x.go")); err != nil {
		t.Fatal(err)
	}
	git(t, wt, "checkout", "--", "x.go")
	if !strings.Contains(readFile(t, filepath.Join(wt, "x.go")), privID) {
		t.Fatal("the private anchor must survive another worktree's deletion")
	}
}

// runStopHook feeds the real binary the JSON Claude Code sends on Stop and
// returns what the hook printed.
func runStopHook(t *testing.T, bin, root string) string {
	t.Helper()
	// A fresh session id per call. The once-per-session notice markers live on
	// disk and outlive the process, so a reused id makes every later run
	// silent — which reads exactly like a hook that found nothing.
	payload, _ := json.Marshal(map[string]any{
		"session_id":      fmt.Sprintf("reference-changes-%s-%d", t.Name(), time.Now().UnixNano()),
		"cwd":             root,
		"hook_event_name": "Stop",
	})
	cmd := exec.Command(bin, "hook", "stop")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CLAUDE_PROJECT_DIR="+root)
	cmd.Stdin = strings.NewReader(string(payload))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("facet hook stop: %v\n%s", err, out)
	}
	return string(out)
}

// Every consumer names the same affected associations: the CLI's JSON, the
// lint findings, and the Stop hook's message.
func TestCLILintAndHookAgreeOnTheAffectedRecords(t *testing.T) {
	bin, mine := privateAnchorClone(t)
	// One deletion and one removed anchor.
	if err := os.Remove(filepath.Join(mine, "x.go")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(mine, "keep.go"), "package x\n")

	r := driftJSON(t, bin, mine)
	cli := map[string]bool{}
	for _, c := range r.ReferenceChanges {
		if c.Lost() {
			cli[c.ID+"@"+c.Path] = true
		}
	}
	want := map[string]bool{privID + "@x.go": true, commitID + "@x.go": true, commitID + "@keep.go": true}
	if len(cli) != len(want) {
		t.Fatalf("CLI: want %v, got %+v", want, r.ReferenceChanges)
	}
	for k := range want {
		if !cli[k] {
			t.Errorf("CLI misses %s", k)
		}
	}

	cmd := exec.Command(bin, "lint", "--json", "--root", mine, "--skip", "schema")
	out, _ := cmd.Output() // exits 1: a/b/committed is now an orphaned record
	var rep facet.Report
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("lint JSON: %v\n%s", err, out)
	}
	lint := map[string]bool{}
	orphanError := false
	for _, f := range rep.Findings {
		if f.Check == "lost-anchors" {
			if f.Severity != facet.SeverityWarning {
				t.Errorf("lost-anchors is a warning, got %+v", f)
			}
			lint[f.ID+"@"+f.Path] = true
		}
		if f.Check == "orphan-records" && f.ID == commitID && f.Severity == facet.SeverityError {
			orphanError = true
		}
	}
	for k := range want {
		if !lint[k] {
			t.Errorf("lint misses %s: %+v", k, rep.Findings)
		}
	}
	if !orphanError {
		t.Error("the orphan-records error must stand beside lost-anchors, not be downgraded by it")
	}

	msg := runStopHook(t, bin, mine)
	for _, line := range []string{
		"a/b/private — deleted: x.go (related) [private anchor]; 0 anchor(s) remain",
		"a/b/committed — deleted: x.go (primary); 0 anchor(s) remain",
		"a/b/committed — anchor removed from keep.go (related); 0 anchor(s) remain",
	} {
		if !strings.Contains(msg, line) {
			t.Errorf("hook misses %q:\n%s", line, msg)
		}
	}
	if !strings.Contains(msg, "Losing a reference does not retire the feature") {
		t.Errorf("hook must ask for a check, not announce a retirement:\n%s", msg)
	}
}

// The review loop through the real binary: a change is reported, 'drift accept'
// records it without touching the tree, the JSON names the accepted baseline,
// and 'drift reset' brings HEAD back.
func TestDriftAcceptAndResetThroughTheBinary(t *testing.T) {
	bin, mine := privateAnchorClone(t)
	write(t, filepath.Join(mine, "keep.go"), "// @facet: a/b/committed\n// @facet-role: related\npackage x\n\nfunc Changed() {}\n")
	if r := driftJSON(t, bin, mine); len(r.Drift) != 1 || r.Baseline.Kind != facet.BaselineHead {
		t.Fatalf("precondition: one drift against HEAD, got %+v / %+v", r.Drift, r.Baseline)
	}
	// The baseline travels in the JSON in full, under the names a caller reads.
	raw := facetRun(t, bin, mine, "drift", "--json", "--root", mine)
	for _, key := range []string{`"ref": "HEAD"`, `"kind": "head"`, `"commit": "`, `"scope": "working-tree-against-baseline"`, `"status": "ok"`} {
		if !strings.Contains(raw, key) {
			t.Errorf("the JSON baseline lacks %s:\n%s", key, raw)
		}
	}
	status := git(t, mine, "status", "--porcelain")

	out := facetRun(t, bin, mine, "drift", "accept", "--outcome", "behavior-unchanged", "--root", mine)
	if !strings.Contains(out, "Accepted baseline") {
		t.Errorf("accept must confirm what it recorded:\n%s", out)
	}
	if got := git(t, mine, "status", "--porcelain"); got != status {
		t.Errorf("accept must not touch the tree or the index:\nbefore %q\nafter  %q", status, got)
	}
	r := driftJSON(t, bin, mine)
	if len(r.Drift) != 0 || r.Baseline.Kind != facet.BaselineAccepted || r.Baseline.Outcome != "behavior-unchanged" {
		t.Errorf("after accept: want no drift against the accepted baseline, got %+v / %+v", r.Drift, r.Baseline)
	}
	if text := facetRun(t, bin, mine, "drift", "--root", mine); !strings.Contains(text, "Baseline: accepted review") {
		t.Errorf("the text output must name the accepted baseline:\n%s", text)
	}

	facetRun(t, bin, mine, "drift", "reset", "--root", mine)
	if r := driftJSON(t, bin, mine); r.Baseline.Kind != facet.BaselineHead || len(r.Drift) != 1 {
		t.Errorf("after reset: want HEAD and the drift back, got %+v / %+v", r.Drift, r.Baseline)
	}
	if text := facetRun(t, bin, mine, "drift", "--root", mine); !strings.Contains(text, "no review recorded") {
		t.Errorf("against HEAD the output must say no review is recorded:\n%s", text)
	}
}

// Without a baseline the hook says so instead of staying silent, and the CLI
// prints the reason instead of "no drift".
func TestAnUnresolvableBaselineIsSaidNotSilenced(t *testing.T) {
	bin, mine := privateAnchorClone(t)

	out, err := exec.Command(bin, "drift", "--root", mine, "--baseline", "no-such-rev").CombinedOutput()
	if err == nil {
		t.Fatalf("an explicit baseline that does not resolve is an error, got exit 0:\n%s", out)
	}
	if !strings.Contains(string(out), "could not be determined") || strings.Contains(string(out), "No drift found") {
		t.Errorf("the CLI must say it could not compare:\n%s", out)
	}

	// A worktree whose accepted baseline ref points nowhere usable: delete
	// the commit's reachability by pointing the ref at a tree, which
	// ^{commit} refuses.
	tree := strings.TrimSpace(git(t, mine, "rev-parse", "HEAD^{tree}"))
	git(t, mine, "update-ref", facet.AcceptedBaselineRef, tree)
	msg := runStopHook(t, bin, mine)
	if !strings.Contains(msg, "could not be determined") {
		t.Errorf("the hook must say drift could not be determined:\n%s", msg)
	}
}
