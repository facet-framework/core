// Fixtures here contain example @facet: tags; this file demonstrates the
// syntax rather than implementing a feature.
// @facet-ignore-file

package facet

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// pairRecord writes one active, anchored record under domain/capability.
func pairRecord(t *testing.T, root, id string, scenarios int) {
	t.Helper()
	var body strings.Builder
	for i := 0; i < scenarios; i++ {
		body.WriteString(fmt.Sprintf("    - id: s%d\n      statement: |\n        Given a customer\n        When they act\n        Then something happens\n", i))
	}
	writeFile(t, filepath.Join(root, "docs/facets/features", filepath.FromSlash(id)+".yaml"),
		"schema_version: \"0.1\"\nid: "+id+"\nlifecycle: active\nintent:\n  summary: |\n    Does a thing a customer can name.\ncontract:\n  scenarios:\n"+body.String()+
			"lineage:\n  owner:\n    team: orders\n  created_at: \"2026-09-21\"\n")
	writeFile(t, filepath.Join(root, "src", strings.ReplaceAll(id, "/", "-")+".ts"),
		"// @facet: "+id+"\n// @facet-role: primary\nexport function run() {}\n")
}

// The fixture includes matching base/suffix pairs and an unpaired suffix.
// Both are needed to distinguish the rule from matching every hyphenated id.
func TestSiblingSuffixPairsFindsTheDoubledFeature(t *testing.T) {
	root := project(t, "")
	pairRecord(t, root, "orders/checkout/order-summary", 9)
	pairRecord(t, root, "orders/checkout/order-summary-mobile", 16)
	pairRecord(t, root, "orders/checkout/cart-panel", 4)
	pairRecord(t, root, "orders/checkout/cart-panel-mobile", 9)
	// Neighbours that merely share a prefix, and must not be paired: no
	// 'promo' record exists, and 'gift-panel' has no partner either.
	pairRecord(t, root, "orders/checkout/promo-panel", 3)
	pairRecord(t, root, "orders/checkout/gift-panel", 3)
	// A hyphenated id whose base lives in a DIFFERENT capability. A pair is
	// two records of one capability; matching across capabilities would name
	// things nobody could fold together.
	pairRecord(t, root, "orders/returns/order-summary-mobile", 5)

	findings := findings(lintProject(t, root), "sibling-suffix")

	var named []string
	for _, f := range findings {
		if f.Severity != SeverityWarning {
			t.Errorf("a suffix pair is a question, not a verdict: %+v", f)
		}
		named = append(named, f.ID)
	}
	want := []string{"orders/checkout/cart-panel-mobile", "orders/checkout/order-summary-mobile"}
	if strings.Join(sorted(named), ",") != strings.Join(want, ",") {
		t.Errorf("paired %v, want %v", sorted(named), want)
	}

	// The finding has to name the OTHER record, or a reader cannot tell which
	// two to compare.
	for _, f := range findings {
		if !strings.Contains(f.Message, "orders/checkout/order-summary\"") &&
			!strings.Contains(f.Message, "orders/checkout/cart-panel\"") {
			t.Errorf("the finding does not name the record it pairs with: %q", f.Message)
		}
	}
}

// The other half: this registry has none, and the check must say so by
// staying quiet rather than by matching something.
func TestSiblingSuffixPairsLeavesUnrelatedNeighboursAlone(t *testing.T) {
	root := project(t, "")
	pairRecord(t, root, "billing/invoice/list", 3)
	pairRecord(t, root, "billing/invoice/export", 3)
	pairRecord(t, root, "billing/invoice/export-queue-retry", 3)

	if got := findings(lintProject(t, root), "sibling-suffix"); len(got) != 0 {
		t.Fatalf("nothing here is a pair, got %+v", got)
	}
}

// The calibration report says the metrics counted the pairs twice.
func TestCalibrationSaysWhenItCountedAPairTwice(t *testing.T) {
	root := t.TempDir()
	pairRecord(t, root, "orders/checkout/order-summary", 9)
	pairRecord(t, root, "orders/checkout/order-summary-mobile", 16)
	for i := 0; i < 6; i++ {
		pairRecord(t, root, fmt.Sprintf("orders/checkout/plain-%d", i), 4)
	}

	cal := calibrate(t, root)

	// The precondition: readings() says nothing at all below five records, so
	// a fixture too small would make this pass for the wrong reason.
	if cal.Records < 5 {
		t.Fatalf("the fixture holds %d records, too few for any reading", cal.Records)
	}
	if cal.SuffixPairs != 1 {
		t.Fatalf("counted %d pairs, want 1", cal.SuffixPairs)
	}
	if !hasReading(cal, "one capability") {
		t.Errorf("the report never mentions the pair:\n%+v", cal.Readings)
	}
	if !hasReading(cal, "count it twice") {
		t.Errorf("the report does not say what the pair does to the numbers:\n%+v", cal.Readings)
	}
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
