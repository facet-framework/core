// @facet-ignore-file
package facet

import (
	"reflect"
	"testing"
)

func TestPrivateAnchorViewProvenancePreservesOtherVersionPlacement(t *testing.T) {
	original := AnchorBlock{Repository: "backend", OriginRevision: "old-commit", OriginContentHash: "old-bytes", OriginWorktree: "worktrees/old", Lines: []string{"// @facet: sample/core/action"}, Before: "func Old() {}", Occurrence: 1}
	previous := PrivateAnchors{Path: "src/main.go", Blocks: []AnchorBlock{original}}
	found := []AnchorBlock{{Lines: original.Lines, Before: "func New() {}", Occurrence: 1}}
	AttributePrivateAnchorsInView(previous, found, "worktrees/new", "backend", "new-commit", "new-bytes")
	merged := MergeSidecarInView(previous, found, []byte("func New() {}\n"), "worktrees/new", "backend", "new-commit")
	if len(merged.Blocks) != 2 || merged.Blocks[0].Repository != "backend" || merged.Blocks[0].OriginRevision != "new-commit" || !reflect.DeepEqual(merged.Blocks[1], original) {
		t.Fatalf("original association overwritten: %+v", merged)
	}
	for _, body := range []string{"func Renamed() {}\n", ""} {
		out, unresolved := InjectAnchorsInView([]byte(body), merged.Blocks, "backend")
		if string(out) != body || len(unresolved) != 2 {
			t.Fatalf("path alone rebound anchors: %q %+v", out, unresolved)
		}
	}
	out, unresolved := InjectAnchorsInView([]byte("func New() {}\n"), merged.Blocks, "backend")
	if len(unresolved) != 1 || string(out) != "// @facet: sample/core/action\nfunc New() {}\n" {
		t.Fatalf("exact new placement failed: %q %+v", out, unresolved)
	}
	out, unresolved = InjectAnchorsInView([]byte("func New() {}\n"), merged.Blocks, "another-repository")
	if string(out) != "func New() {}\n" || len(unresolved) != 2 {
		t.Fatal("another repository inherited private anchors")
	}
	// Legacy origins stay unknown even when their exact placement is encountered.
	legacy := PrivateAnchors{Path: previous.Path, Blocks: []AnchorBlock{{Lines: original.Lines, Before: "func New() {}", Occurrence: 1}}}
	AttributePrivateAnchorsInView(legacy, found, "relocated-worktree", "backend", "later", "later-bytes")
	if found[0].Repository != "" || found[0].OriginRevision != "" {
		t.Fatal("historical provenance invented for legacy anchor")
	}
}
