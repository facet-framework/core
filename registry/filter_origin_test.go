// Fixture tags are not implementation anchors. @facet-ignore-file
package facet

import "testing"

func TestPrivateAnchorOriginsSurviveSharedCheckoutAndLegacySidecars(t *testing.T) {
	original := AnchorBlock{Lines: []string{"// @facet: a/b/c"}, Before: "func C() {}", OriginWorktree: "/clone/main"}
	legacy := AnchorBlock{Lines: []string{"// @facet: a/b/legacy"}, Before: "func Old() {}"}
	previous := PrivateAnchors{Path: "x.go", Blocks: []AnchorBlock{original, legacy}}
	found := []AnchorBlock{original, legacy, {Lines: []string{"// @facet: a/b/new"}, Before: "func New() {}"}}
	AttributePrivateAnchors(previous, found, "/clone/linked")
	if found[0].OriginWorktree != "/clone/main" || found[1].OriginWorktree != "" || found[2].OriginWorktree != "/clone/linked" {
		t.Fatalf("provenance rewritten: %+v", found)
	}
	merged := MergeSidecar(previous, found, []byte("func C() {}\nfunc Old() {}\nfunc New() {}\n"))
	if len(merged.Blocks) != 3 || merged.Blocks[0].OriginWorktree != "/clone/main" || merged.Blocks[1].OriginWorktree != "" {
		t.Fatalf("merge lost provenance: %+v", merged)
	}
	dir := t.TempDir()
	if err := SavePrivateAnchors(dir, merged); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadPrivateAnchors(dir, "x.go")
	if err != nil || len(loaded.Blocks) != 3 || loaded.Blocks[0].OriginWorktree != "/clone/main" || loaded.Blocks[1].OriginWorktree != "" {
		t.Fatalf("sidecar lost provenance: %+v %v", loaded, err)
	}
}
