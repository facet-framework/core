package facet

import (
	"os"
	"path/filepath"
	"testing"
)

// recordFromYAML writes the fixture and loads it the way the tool does. A
// shortcut past LoadRecord would test a parse nothing else performs — and the
// alias case below turns on exactly what the real parser does with an anchor.
func recordFromYAML(t *testing.T, body string) Record {
	t.Helper()
	path := filepath.Join(t.TempDir(), "widget.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := LoadRecord(path, "features")
	if err != nil {
		t.Fatalf("the fixture does not parse: %v", err)
	}
	return rec
}

// 1. The three-way split. A record carrying one of each must count one of each,
// or the numbers mean nothing.
func TestKnowledgeCountsSplitsAnsweredOpenAndInapplicable(t *testing.T) {
	rec := recordFromYAML(t, `schema_version: "0.1"
id: demo/thing/widget
knowledge:
  prerequisite_knowledge:
    question: What must be installed?
    answer: A Go toolchain.
  not_for:
    question: What is this not for?
  common_pitfalls:
    question: What goes wrong?
    not_applicable:
      reason: the feature has no runtime failure mode
      confirmed_by: team-facet
`)

	got := rec.KnowledgeCounts()

	if got.Answered != 1 || got.Open != 1 || got.Inapplicable != 1 {
		t.Errorf("want 1/1/1 answered/open/inapplicable, got %d/%d/%d",
			got.Answered, got.Open, got.Inapplicable)
	}
}

// 2. The alias case, and the reason this measurement exists. One YAML anchor
// silences several slots in a single commit; the count has to see all of
// them, or the number it is meant to guard stays flat while the registry
// goes quiet.
func TestKnowledgeCountsSeesSlotsSilencedByOneAlias(t *testing.T) {
	rec := recordFromYAML(t, `schema_version: "0.1"
id: demo/thing/widget
knowledge:
  common_pitfalls:
    question: What goes wrong?
    not_applicable: &na
      reason: internal review
      confirmed_by: team-facet
  not_for:
    question: What is this not for?
    not_applicable: *na
  success_signal:
    question: How do you know it works?
    not_applicable: *na
`)

	// The fixture asserts its own premise: if the alias had not expanded, this
	// case would be measuring one declaration and reporting success for three.
	knowledge, ok := rec.Fields["knowledge"].(map[string]any)
	if !ok || len(knowledge) != 3 {
		t.Fatalf("the fixture must carry three slots, got %d", len(knowledge))
	}
	for name, raw := range knowledge {
		slot, _ := raw.(map[string]any)
		if !slotDeclaredInapplicable(slot) {
			t.Fatalf("the alias did not expand into %q, so this case measures something else", name)
		}
	}

	got := rec.KnowledgeCounts()

	if got.Inapplicable != 3 {
		t.Errorf("three slots were silenced by one alias, counted %d", got.Inapplicable)
	}
	if got.Open != 0 || got.Answered != 0 {
		t.Errorf("nothing here is answered or open, got %d answered and %d open", got.Answered, got.Open)
	}
}

// 3. Half a declaration is not a declaration. slotDeclaredInapplicable requires
// reason AND confirmed_by, so silence is bought by the complete statement
// and never by the keyword — and the slot shows up where the work still is.
func TestAHalfDeclaredExceptionCountsAsOpen(t *testing.T) {
	rec := recordFromYAML(t, `schema_version: "0.1"
id: demo/thing/widget
knowledge:
  common_pitfalls:
    question: What goes wrong?
    not_applicable:
      reason: nobody confirmed this
`)

	got := rec.KnowledgeCounts()

	if got.Open != 1 {
		t.Errorf("a not_applicable without confirmed_by must count as open, got %d open", got.Open)
	}
	if got.Inapplicable != 0 {
		t.Errorf("a half declaration must not count as an exception, got %d", got.Inapplicable)
	}
}

// 4. The three numbers always sum to the slot count. Without this, a slot in a
// shape none of the three branches expects would vanish, and a tally that
// silently drops rows is worse than no tally.
func TestTheThreeNumbersSumToTheSlotCount(t *testing.T) {
	rec := recordFromYAML(t, `schema_version: "0.1"
id: demo/thing/widget
knowledge:
  prerequisite_knowledge:
    question: What must be installed?
    answer: A Go toolchain.
  discoverability:
    question: How is it found?
    derive_from: [anchors]
  not_for:
    question: What is this not for?
  common_pitfalls:
    question: What goes wrong?
    not_applicable:
      reason: no runtime failure mode
      confirmed_by: team-facet
`)

	knowledge, _ := rec.Fields["knowledge"].(map[string]any)
	got := rec.KnowledgeCounts()
	sum := got.Answered + got.Open + got.Inapplicable

	if sum != len(knowledge) {
		t.Errorf("%d slots but %d counted (%d answered, %d open, %d inapplicable)",
			len(knowledge), sum, got.Answered, got.Open, got.Inapplicable)
	}
}

// 5. A record without a knowledge block gives three zeros rather than tripping
// over a nil map.
func TestARecordWithoutKnowledgeCountsZero(t *testing.T) {
	rec := recordFromYAML(t, `schema_version: "0.1"
id: demo/thing/widget
`)

	got := rec.KnowledgeCounts()

	if got != (KnowledgeTally{}) {
		t.Errorf("want an empty tally, got %+v", got)
	}
}
