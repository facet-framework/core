package spec

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	gherkin "github.com/cucumber/gherkin/go/v41"
)

const header = "---\nschema: facet-spec/0.1\nid: pricing/discounts\ntitle: Discount codes\nstatus: approved\nowner: checkout\n"

func fenced(body string) string { return "```gherkin\n" + body + "\n```\n" }

func readFixture(t *testing.T, name, content string) (Document, []Finding) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return ReadFile(root, path)
}

func oneUnit(t *testing.T, name, content string) Unit {
	t.Helper()
	doc, findings := readFixture(t, name, content)
	if doc.Skipped || len(findings) != 0 || len(doc.Units) != 1 {
		t.Fatalf("expected one unit, got %+v and %v", doc, findings)
	}
	unit := doc.Units[0]
	if unit.Strength() != Exact || len(unit.Sequence.Steps) == 0 || unit.Digest != unit.Sequence.Digest() ||
		len(unit.Digest) != 71 || len(unit.ContextDigest) != 71 {
		t.Fatalf("invalid unit %+v", unit)
	}
	return unit
}

func TestSpecValidMarkdown(t *testing.T) {
	content := header + "---\n# Context\n```text\nScenario: Ignored\n```\n" +
		"```gherkin extra\nScenario: Also ignored\n```\n" + fenced("@id:OTHER @case:cap\nScenario: Cap\nGiven a discount")
	unit := oneUnit(t, "discounts.md", content)
	if unit.Address != "pricing/discounts#cap" || unit.Line != 16 {
		t.Fatalf("unexpected unit %+v", unit)
	}
	if (Unit{Address: "requirements#cap", Digest: unit.Digest, Binding: Provenance}).Strength() != Provenance {
		t.Fatal("unit without steps should bind as provenance")
	}
}

func TestSpecFindings(t *testing.T) {
	for _, tc := range []struct {
		name, content, message string
		line                   int
	}{
		{"missing tag", header + "---\n" + fenced("@id:ignored\nScenario: Sample"), "needs one @case", 10},
		{"two tags", header + "---\n" + fenced("@case:one\n@case:two\nScenario: Sample"), "more than one @case", 10},
		{"invalid slug", header + "---\n" + fenced("@case:Upper\nScenario: Sample"), "invalid case ID", 9},
		{"duplicate", header + "---\n" + fenced("@case:same\nScenario: First") + fenced("@case:same\nScenario: Second"), "duplicate case ID", 13},
		{"invalid Gherkin", header + "---\n" + fenced("@case:one\nScenario: Sample\nGiven a step\nFeature: Unexpected"), "invalid Gherkin", 12},
		{"invalid status", strings.Replace(header, "approved", "pending", 1) + "---\n", "invalid spec frontmatter", 5},
		{"extra field", header + "extra: true\n---\n", "invalid spec frontmatter", 7},
		{"missing field", strings.Replace(header, "owner: checkout\n", "", 1) + "---\n", "invalid spec frontmatter", 1},
		{"invalid language", header + "language: made-up\n---\n", "unsupported Gherkin language", 7},
		{"unclosed frontmatter", header, "unclosed spec frontmatter", 1},
		{"unsupported version", strings.Replace(header, "facet-spec/0.1", "facet-spec/9.0", 1) + "---\n", "invalid spec frontmatter", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, findings := readFixture(t, "invalid.md", tc.content)
			if doc.Skipped || len(findings) != 1 {
				t.Fatalf("expected one finding from a recognised spec, got %+v and %v", doc, findings)
			}
			got := findings[0]
			if filepath.Base(got.File) != "invalid.md" || got.Line != tc.line || !strings.Contains(got.Message, tc.message) {
				t.Fatalf("got %v, want line %d and %q", got, tc.line, tc.message)
			}
		})
	}
}

func TestSpecCaseTagAndDialect(t *testing.T) {
	prefix := header + "case_tag: id\nlanguage: de\n---\n"
	german := gherkin.DialectsBuiltin().GetDialect("de")
	keyword := func(name string) string { return strings.TrimSpace(german.Keywords[name][0]) }
	statement := keyword("given") + " a discount\n" + keyword("when") + " it applies\n" + keyword("then") + " the price drops"
	body := "@case:IGNORED @case:also-ignored @id:discount\n" + keyword("scenario") + ": Discount\n" + statement
	unit := oneUnit(t, "discounts.md", prefix+fenced(body))
	if unit.Address != "pricing/discounts#discount" {
		t.Fatalf("unexpected address %s", unit.Address)
	}
	sequence, err := NormaliseStatement(statement, "de")
	if err != nil || sequence.Digest() != unit.Digest {
		t.Fatalf("statement differs: %+v, %v", sequence, err)
	}
	_, findings := readFixture(t, "wrong-tag.md", prefix+fenced(strings.Replace(body, "@id:discount", "", 1)))
	if len(findings) != 1 || !strings.Contains(findings[0].Message, "needs one @id") {
		t.Fatalf("expected missing @id, got %v", findings)
	}
}

func TestSpecNormalise(t *testing.T) {
	const original = `Feature: Pricing
  Background: Setup
    Given an open cart
  Rule: Discount limits
    Background: Local setup
      Given a logged in customer
    @case:discount
    Scenario Outline: Apply a discount
      Given a code for <amount>
      And the cart is open
      When the code is applied
        """text/plain
        apply-code
        """
      Then the discount appears
        | code | status |
        | test | active |
      Examples: Percentages
        | amount |
        | 10 |
        | 20 |`
	read := func(body string) Unit { return oneUnit(t, "discounts.md", header+"---\n"+fenced(body)) }
	base := read(original)
	var types []string
	for _, step := range base.Sequence.Steps {
		types = append(types, string(step.Type))
	}
	if !reflect.DeepEqual(types, []string{"Context", "Context", "Action", "Outcome"}) ||
		!reflect.DeepEqual(base.Sequence.Examples, [][][]string{{{"amount"}, {"10"}, {"20"}}}) ||
		base.Sequence.Steps[2].DocString == nil || base.Sequence.Steps[2].DocString.Content != "apply-code" ||
		!reflect.DeepEqual(base.Sequence.Steps[3].DataTable, [][]string{{"code", "status"}, {"test", "active"}}) {
		t.Fatalf("unexpected normal form %+v", base.Sequence)
	}
	same := strings.NewReplacer("@case:discount", "# A comment\n@case:discount @extra", "Apply a discount", "New name",
		"      And the cart is open", "    Given   the   cart\tis open", "Examples: Percentages", "Examples: New title").Replace(original)
	if other := read(same); !reflect.DeepEqual(base.Sequence, other.Sequence) || base.Digest != other.Digest || base.ContextDigest != other.ContextDigest {
		t.Fatalf("presentation edits changed normal form: %+v vs %+v", base, other)
	}
	for _, tc := range []struct {
		name, before, after string
		context             bool
	}{
		{"text", "the cart is open", "the cart is closed", false},
		{"keyword type", "And the cart", "When the cart", false},
		{"example row", "| 10 |", "| 15 |", false},
		{"example order", "| 10 |\n        | 20 |", "| 20 |\n        | 10 |", false},
		{"doc string", "apply-code", "remove-code", false},
		{"data table", "test | active", "test | invalid", false},
		{"feature background", "open cart", "closed cart", true},
		{"rule background", "logged in customer", "guest customer", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := strings.Replace(original, tc.before, tc.after, 1)
			if changed == original {
				t.Fatal("fixture change did not apply")
			}
			other := read(changed)
			if (base.Digest == other.Digest) != tc.context || (base.ContextDigest != other.ContextDigest) != tc.context {
				t.Fatalf("unexpected digest change: %+v vs %+v", base, other)
			}
		})
	}
}

func TestSpecPlainFeatureAndStatement(t *testing.T) {
	statement := "Given a cart\nWhen a discount is applied\nThen the price drops\nBut the cart stays open"
	unit := oneUnit(t, "nested/discount.feature", "Feature: Discounts\n@case:cap\nScenario: Cap\n"+statement)
	if unit.Address != "nested/discount#cap" || unit.Line != 2 || string(unit.Sequence.Steps[3].Type) != "Outcome" {
		t.Fatalf("unexpected unit %+v", unit)
	}
	sequence, err := NormaliseStatement(statement, "")
	if err != nil || !reflect.DeepEqual(sequence, *unit.Sequence) || sequence.Digest() != unit.Digest {
		t.Fatalf("statement differs: %+v, %v", sequence, err)
	}
	for _, invalid := range []string{"prose only", "Given a cart\nScenario: Injected\nThen an outcome", "Given a cart\nExamples:\n| a |\n| b |"} {
		if _, err := NormaliseStatement(invalid, ""); err == nil {
			t.Errorf("accepted invalid statement %q", invalid)
		}
	}
}

func TestSpecFencedBlocks(t *testing.T) {
	content := header + "---\n" +
		"~~~~gherkin\n@case:tilde\nScenario: Tilde\nGiven a tilde fence\n~~~~~\n" +
		"````text\n```gherkin\n@case:hidden\nScenario: Inside another fence\nGiven nothing\n```\n````\n" +
		"    ```gherkin\n    @case:indented\n    Scenario: Indented code\n    Given nothing\n    ```\n" +
		"  ```gherkin\n  @case:shallow\n  Scenario: Shallow\n  Given a shallow fence\n  ```\n"
	doc, findings := readFixture(t, "fences.md", content)
	if len(findings) != 0 {
		t.Fatalf("unexpected findings %v", findings)
	}
	var addresses []string
	for _, unit := range doc.Units {
		addresses = append(addresses, unit.Address)
	}
	if !reflect.DeepEqual(addresses, []string{"pricing/discounts#tilde", "pricing/discounts#shallow"}) {
		t.Fatalf("unexpected units %v", addresses)
	}
	if doc.Units[0].Line != 9 || doc.Units[1].Line != 26 {
		t.Fatalf("unexpected lines %d, %d", doc.Units[0].Line, doc.Units[1].Line)
	}
}

func TestSpecLayoutNeverOverridesADeclaredFacetSpec(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "specs", "001-pricing", "spec.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nschema: facet-spec/0.1\nid: pricing\ntitle: Pricing\nstatus: approved\nowner: checkout\nlanguage: en\n---\n\n```gherkin\n@case:cap\nScenario: Cap\n  Given a cart\n  When the code is applied\n  Then the discount is 50%\n```\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, findings := ReadFile(root, path)
	if len(findings) != 0 || len(doc.Units) != 1 || doc.Units[0].Address != "pricing#cap" || doc.Units[0].Strength() != Exact {
		t.Fatalf("a facet-spec file in a Spec Kit layout was not read as facet-spec: %+v %v", doc, findings)
	}
}
