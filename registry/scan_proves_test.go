// Fixtures below contain example @facet: tags; this file demonstrates the
// syntax rather than implementing a feature, so it opts out of scanning.
// @facet-ignore-file

package facet

import (
	"path/filepath"
	"reflect"
	"testing"
)

// The scanner reads @facet-role: and @facet-proves: in either order within
// an anchor run. This case places comma-separated scenario ids after the role.
func TestScanAnchorsParsesFacetProvesAfterRole(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "src/discount.rs"), "/// @facet: checkout/pricing/discount-code-redemption\n"+
		"/// @facet-role: primary\n"+
		"/// @facet-proves: discount-cap-applied, expired-code-rejected\n"+
		"pub fn apply_discount_code() {}\n")

	anchors, err := ScanAnchors(root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 {
		t.Fatalf("expected 1 anchor, got %d: %+v", len(anchors), anchors)
	}
	a := anchors[0]
	if a.Role != "primary" {
		t.Errorf("role must still parse, got %q", a.Role)
	}
	want := []string{"discount-cap-applied", "expired-code-rejected"}
	if !reflect.DeepEqual(a.Proves, want) {
		t.Errorf("want Proves %v, got %v", want, a.Proves)
	}
}

// The tag is documented as following the anchor "in either order" alongside
// the role tag — a project writing @facet-proves before @facet-role: must
// see both captured just the same.
func TestScanAnchorsParsesFacetProvesBeforeRole(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "src/discount.rs"), "// @facet: checkout/pricing/discount-code-redemption\n"+
		"// @facet-proves: discount-cap-applied\n"+
		"// @facet-role: primary\n"+
		"pub fn apply_discount_code() {}\n")

	anchors, err := ScanAnchors(root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 {
		t.Fatalf("expected 1 anchor, got %d: %+v", len(anchors), anchors)
	}
	a := anchors[0]
	if a.Role != "primary" {
		t.Errorf("role must parse when it follows proves, got %q", a.Role)
	}
	if want := []string{"discount-cap-applied"}; !reflect.DeepEqual(a.Proves, want) {
		t.Errorf("want Proves %v, got %v", want, a.Proves)
	}
}

// A single scenario id, no role line at all — @facet-proves must not
// require @facet-role: to be present.
func TestScanAnchorsParsesFacetProvesWithNoRoleLine(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "src/discount.rs"), "// @facet: checkout/pricing/discount-code-redemption\n"+
		"// @facet-proves: discount-cap-applied\n"+
		"pub fn apply_discount_code() {}\n")

	anchors, err := ScanAnchors(root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 {
		t.Fatalf("expected 1 anchor, got %d: %+v", len(anchors), anchors)
	}
	if want := []string{"discount-cap-applied"}; !reflect.DeepEqual(anchors[0].Proves, want) {
		t.Errorf("want Proves %v, got %v", want, anchors[0].Proves)
	}
}

// An anchor with no @facet-proves line at all must carry a nil/empty
// Proves — absence must not be confused with an explicit empty list, and
// existing anchors (the overwhelming majority of any corpus today) must
// scan exactly as before.
func TestScanAnchorsAnchorWithNoProvesTagHasEmptyProves(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "src/checkout.ts"), "// @facet: payments/checkout/session\n// @facet-role: primary\n")

	anchors, err := ScanAnchors(root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 {
		t.Fatalf("expected 1 anchor, got %d: %+v", len(anchors), anchors)
	}
	if len(anchors[0].Proves) != 0 {
		t.Errorf("expected no Proves entries, got %v", anchors[0].Proves)
	}
	if anchors[0].Role != "primary" {
		t.Errorf("role must still parse when no proves line follows, got %q", anchors[0].Role)
	}
}

// The first line matching neither @facet-role: nor @facet-proves ends the
// anchor run, preventing later tags from attaching to unrelated code.
func TestScanAnchorsUnrelatedLineEndsTheAnchorRun(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "src/checkout.ts"), "// @facet: payments/checkout/session\n"+
		"// @facet-role: primary\n"+
		"export function create() {}\n"+
		"// @facet-proves: should-not-attach\n")

	anchors, err := ScanAnchors(root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 {
		t.Fatalf("expected 1 anchor (the trailing @facet-proves: line names no anchor), got %d: %+v", len(anchors), anchors)
	}
	if len(anchors[0].Proves) != 0 {
		t.Errorf("a proves line separated from the anchor by unrelated code must not attach, got %v", anchors[0].Proves)
	}
}

// A new @facet: tag immediately following another anchor's role/proves run
// must still flush the first and start a fresh one — multi-line
// accumulation must not swallow a genuinely new anchor.
func TestScanAnchorsBackToBackAnchorsBothParseTheirOwnProves(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "src/discount.rs"), "// @facet: checkout/pricing/discount-code-redemption\n"+
		"// @facet-proves: discount-cap-applied\n"+
		"// @facet: checkout/pricing/expired-code\n"+
		"// @facet-proves: expired-code-rejected\n")

	anchors, err := ScanAnchors(root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 2 {
		t.Fatalf("expected 2 anchors, got %d: %+v", len(anchors), anchors)
	}
	if want := []string{"discount-cap-applied"}; !reflect.DeepEqual(anchors[0].Proves, want) {
		t.Errorf("first anchor: want Proves %v, got %v", want, anchors[0].Proves)
	}
	if want := []string{"expired-code-rejected"}; !reflect.DeepEqual(anchors[1].Proves, want) {
		t.Errorf("second anchor: want Proves %v, got %v", want, anchors[1].Proves)
	}
}

// ScanContent is the entry point callers that already hold a file's bytes
// use; it must behave identically to the file-reading path above.
func TestScanContentParsesFacetProves(t *testing.T) {
	data := []byte("// @facet: checkout/pricing/discount-code-redemption\n" +
		"// @facet-role: primary\n" +
		"// @facet-proves: discount-cap-applied, expired-code-rejected\n")
	anchors := ScanContent("discount.ts", data)
	if len(anchors) != 1 {
		t.Fatalf("expected 1 anchor, got %d: %+v", len(anchors), anchors)
	}
	want := []string{"discount-cap-applied", "expired-code-rejected"}
	if !reflect.DeepEqual(anchors[0].Proves, want) {
		t.Errorf("want Proves %v, got %v", want, anchors[0].Proves)
	}
}
