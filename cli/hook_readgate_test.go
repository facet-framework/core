// @facet: authoring/guardrails/large-record-read
// @facet-role: related

package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/facet-framework/core/registry"
)

// bigRecord is a record well over any threshold used below, with a distinctive
// body per section so a test can tell which one it was handed.
func bigRecord(t *testing.T) (root, path string) {
	t.Helper()
	root = adoptedRepo(t, "billing/invoice/list")
	path = filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml")
	body := "schema_version: \"0.1\"\nid: billing/invoice/list\nlifecycle: active\nintent:\n  summary: |\n    INTENT-MARKER lists invoices.\ncontract:\n  scenarios:\n    - id: shown\n      statement: |\n        SCENARIO-MARKER given invoices\nverification:\n  unit_tests:\n    - id: list_test.go\nknowledge:\n  common_pitfalls:\n    answer: |\n" +
		strings.Repeat("      KNOWLEDGE-MARKER padding padding padding\n", 200) +
		"lineage:\n  owner:\n    team: billing\n"
	write(t, path, body)
	return root, path
}

func readGateFor(t *testing.T, root, path string) (hookContext, hookInput) {
	t.Helper()
	var in hookInput
	in.CWD = root
	in.SessionID = "read-gate"
	in.ToolName = "Read"
	in.ToolInput.FilePath = path
	ctx, ok := resolveHookContext(newHookCmd(), in)
	if !ok {
		t.Fatal("want a context, got none")
	}
	return ctx, in
}

// Mutation: delete the deny(...) call in hookReadGate; the whole record is read.
func TestReadGateSendsALargeRecordToReadFacet(t *testing.T) {
	t.Setenv("FACET_SKIP_READ_GATE", "")
	root, path := bigRecord(t)
	ctx, in := readGateFor(t, root, path)

	out := captureHook(t, func() { hookPreToolUse(ctx, in) })

	for _, want := range []string{`"permissionDecision":"deny"`, "read_facet", "billing/invoice/list", "knowledge", "contract.scenarios", "FACET_SKIP_READ_GATE=1"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in the denial, got %q", want, out)
		}
	}
	// The premise: the table names real sections with real sizes.
	if !strings.Contains(out, "bytes") {
		t.Errorf("want sizes in the denial, got %q", out)
	}
}

// Mutation: in hookReadGate, change `<=` to `>=` against the threshold.
func TestReadGateLeavesASmallRecordAlone(t *testing.T) {
	t.Setenv("FACET_SKIP_READ_GATE", "")
	root := brokenRegistry(t)
	path := filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml")
	ctx, in := readGateFor(t, root, path)
	if raw, _ := readFileString(path); len(raw) >= facet.ReadLargeRecordThreshold() {
		t.Fatalf("premise: fixture is %d bytes, not small", len(raw))
	}
	if out := captureHook(t, func() { hookPreToolUse(ctx, in) }); out != "" {
		t.Fatalf("want silence for a small record, got %q", out)
	}
}

func TestReadGateStepsAsideForASlice(t *testing.T) {
	t.Setenv("FACET_SKIP_READ_GATE", "")
	root, path := bigRecord(t)
	ctx, in := readGateFor(t, root, path)
	limit := 40
	in.ToolInput.Limit = &limit
	if out := captureHook(t, func() { hookPreToolUse(ctx, in) }); out != "" {
		t.Fatalf("want a sliced Read allowed, got %q", out)
	}
}

// Mutation: remove the FACET_SKIP_READ_GATE check.
func TestReadGateHasAnEscapeHatch(t *testing.T) {
	t.Setenv("FACET_SKIP_READ_GATE", "1")
	root, path := bigRecord(t)
	ctx, in := readGateFor(t, root, path)
	if out := captureHook(t, func() { hookPreToolUse(ctx, in) }); out != "" {
		t.Fatalf("want the escape hatch honoured, got %q", out)
	}
}

// Mutation: drop the features-directory prefix test; any large yaml is refused.
func TestReadGateOnlyGuardsRecords(t *testing.T) {
	t.Setenv("FACET_SKIP_READ_GATE", "")
	root, _ := bigRecord(t)
	other := filepath.Join(root, "ci.yaml")
	write(t, other, strings.Repeat("key: value\n", 2000))
	ctx, in := readGateFor(t, root, other)
	if out := captureHook(t, func() { hookPreToolUse(ctx, in) }); out != "" {
		t.Fatalf("want a non-record yaml allowed, got %q", out)
	}
}

// The tool the denial points at has to return the section, and only it.
// Mutation: in ReadSections, return the whole of data.
func TestReadFacetReturnsOnlyTheAskedSection(t *testing.T) {
	root, _ := bigRecord(t)
	set := facet.OpenSet(root)

	list, err := readFacet(set, readFacetInput{ID: "billing/invoice/list"})
	if err != nil {
		t.Fatal(err)
	}
	if list.Text != "" || len(list.Sections) == 0 {
		t.Fatalf("want an outline and no text, got %+v", list)
	}
	sum := 0
	for _, s := range list.Sections {
		if !strings.Contains(s.Name, ".") {
			sum += s.Bytes
		}
	}
	if sum != list.Bytes {
		t.Errorf("top-level sections add up to %d, the file is %d", sum, list.Bytes)
	}

	got, err := readFacet(set, readFacetInput{ID: "billing/invoice/list", Sections: []string{"contract"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, "SCENARIO-MARKER") {
		t.Errorf("want the contract section, got %q", got.Text)
	}
	for _, leak := range []string{"INTENT-MARKER", "KNOWLEDGE-MARKER"} {
		if strings.Contains(got.Text, leak) {
			t.Errorf("section read leaked %s: %q", leak, got.Text)
		}
	}
}

func TestReadFacetRefusesAnUnknownSectionAndListsTheRealOnes(t *testing.T) {
	root, _ := bigRecord(t)
	_, err := readFacet(facet.OpenSet(root), readFacetInput{ID: "billing/invoice/list", Sections: []string{"contrakt"}})
	if err == nil || !strings.Contains(err.Error(), "contract") {
		t.Fatalf("want an error naming the real sections, got %v", err)
	}
}

func TestChildSectionsAreAddressable(t *testing.T) {
	root, _ := bigRecord(t)
	got, err := readFacet(facet.OpenSet(root), readFacetInput{ID: "billing/invoice/list", Sections: []string{"contract.scenarios"}})
	if err != nil || !strings.Contains(got.Text, "SCENARIO-MARKER") {
		t.Fatalf("want the child section, got %v / %q", err, got.Text)
	}
}
