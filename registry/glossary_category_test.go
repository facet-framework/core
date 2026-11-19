// Fixtures here contain example @facet: tags; the file demonstrates syntax
// rather than implementing anything, so it opts out of scanning.
// @facet-ignore-file

package facet

import (
	"strings"
	"testing"
)

// A glossary whose terms are grouped, used by most of the cases below. Two
// categories, declared in an order that is not alphabetical, because the order
// of the groups is one of the things under test.
const categorisedGlossary = `schema_version: "0.1"
categories:
  - id: parties
    title: Parties
    purpose: The people and organisations a shipment passes between.
  - id: documents
    title: Documents
terms:
  - term: Consignee
    category: parties
    definition: |
      The party named in the transport document who receives the goods.
  - term: Bill of Lading
    category: documents
    definition: |
      The document the carrier issues for goods received.
  - term: Shipper
    category: parties
    definition: |
      The party handing the goods to the carrier.
`

// The ordinary case, and the one that has to be silent: a term naming a
// category the file declares is simply a grouped term.
func TestDeclaredCategoryIsAccepted(t *testing.T) {
	reg := glossaryProject(t, categorisedGlossary,
		"A booking.", "the booking is released", "Nothing.")
	rep := lintNoSchema(t, reg)
	for _, check := range []string{"undeclared-glossary-category", "duplicate-glossary-category", "empty-glossary-category"} {
		if found := findingsFor(rep, check); len(found) != 0 {
			t.Fatalf("expected no %s findings for a well-formed grouping, got %+v", check, found)
		}
	}
}

// An undeclared category reference is an error because valid category
// IDs are explicitly declared in the same glossary.
func TestUndeclaredCategoryIsError(t *testing.T) {
	reg := glossaryProject(t, `schema_version: "0.1"
categories:
  - id: parties
    title: Parties
terms:
  - term: Consignee
    category: partys
    definition: |
      The party named in the transport document who receives the goods.
`, "A booking.", "the booking is released", "Nothing.")

	found := findingsFor(lintNoSchema(t, reg), "undeclared-glossary-category")
	if len(found) != 1 {
		t.Fatalf("expected one undeclared-category finding, got %+v", found)
	}
	if found[0].Severity != SeverityError {
		t.Fatalf("expected an error, got %q", found[0].Severity)
	}
	// The term, so the author knows which line to open; the id it asked for,
	// so the typo is visible; and the ids that exist, which is what separates a
	// misspelling from a category nobody has declared yet. The two have
	// opposite remedies.
	for _, want := range []string{"Consignee", "partys"} {
		if !strings.Contains(found[0].Message, want) {
			t.Fatalf("expected the message to name %q, got %q", want, found[0].Message)
		}
	}
	if !strings.Contains(found[0].Hint, "parties") {
		t.Fatalf("expected the hint to list the declared categories, got %q", found[0].Hint)
	}
	if !strings.Contains(found[0].Path, GlossaryName) {
		t.Fatalf("expected the finding on %s, got %q", GlossaryName, found[0].Path)
	}
}

// The duplicate-term check one level up, and an error for the same reason: two
// headings under one id leave a reader no way to tell which title and purpose
// hold, and nothing downstream can choose either.
func TestDuplicateCategoryIsError(t *testing.T) {
	reg := glossaryProject(t, `schema_version: "0.1"
categories:
  - id: parties
    title: Parties
  - id: parties
    title: Participants
terms:
  - term: Consignee
    category: parties
    definition: |
      The party named in the transport document who receives the goods.
`, "A booking.", "the booking is released", "Nothing.")

	found := findingsFor(lintNoSchema(t, reg), "duplicate-glossary-category")
	if len(found) != 1 {
		t.Fatalf("expected one duplicate-category finding, got %+v", found)
	}
	if found[0].Severity != SeverityError {
		t.Fatalf("expected an error, got %q", found[0].Severity)
	}
	if !strings.Contains(found[0].Message, "parties") {
		t.Fatalf("expected the message to name the id, got %q", found[0].Message)
	}
}

// A warning and never an error: declaring a category before writing its first
// term is the intended order. What it catches in practice is the half-finished
// rename — the old id left standing while most terms moved to the new one.
func TestUnusedCategoryIsWarning(t *testing.T) {
	reg := glossaryProject(t, `schema_version: "0.1"
categories:
  - id: parties
    title: Parties
  - id: events
    title: Events
terms:
  - term: Consignee
    category: parties
    definition: |
      The party named in the transport document who receives the goods.
`, "A booking.", "the booking is released", "Nothing.")

	found := findingsFor(lintNoSchema(t, reg), "empty-glossary-category")
	if len(found) != 1 {
		t.Fatalf("expected one empty-category finding, got %+v", found)
	}
	if found[0].Severity != SeverityWarning {
		t.Fatalf("expected a warning, got %q", found[0].Severity)
	}
	if !strings.Contains(found[0].Message, "events") {
		t.Fatalf("expected the message to name the unused id, got %q", found[0].Message)
	}
	if strings.Contains(found[0].Message, "parties") {
		t.Fatalf("expected the used category to be silent, got %q", found[0].Message)
	}
}

// An absent category is silent: it declares no classification and is
// not an invalid reference (FACET §4.4.2).
func TestUncategorisedTermIsNotAFinding(t *testing.T) {
	reg := glossaryProject(t, `schema_version: "0.1"
categories:
  - id: parties
    title: Parties
terms:
  - term: Consignee
    category: parties
    definition: |
      The party named in the transport document who receives the goods.
  - term: Demurrage
    definition: |
      What the carrier charges when a container is held past its free time.
`, "A booking.", "the booking is released", "Nothing.")

	rep := lintNoSchema(t, reg)
	for _, check := range []string{"undeclared-glossary-category", "empty-glossary-category", "duplicate-glossary-category"} {
		if found := findingsFor(rep, check); len(found) != 0 {
			t.Fatalf("expected an uncategorised term to be silent, got %s %+v", check, found)
		}
	}
}

// The migration guarantee: every glossary written before this feature existed
// declares no categories, and must keep linting exactly as it did.
func TestGlossaryWithoutCategoriesIsUnchanged(t *testing.T) {
	reg := glossaryProject(t, consigneeGlossary,
		"A booking.", "the booking is released", "Nothing.")

	rep := lintNoSchema(t, reg)
	for _, check := range []string{"undeclared-glossary-category", "duplicate-glossary-category", "empty-glossary-category"} {
		if found := findingsFor(rep, check); len(found) != 0 {
			t.Fatalf("expected no %s findings without categories, got %+v", check, found)
		}
	}

	g, err := LoadGlossary(reg.GlossaryPath())
	if err != nil {
		t.Fatal(err)
	}
	if g.Categorised() {
		t.Fatal("a glossary declaring no categories must not report itself as categorised")
	}
	// Nil rather than one group holding everything: a surface handed a single
	// unnamed group would draw a heading over a list that has none.
	if groups := g.Grouped(); groups != nil {
		t.Fatalf("expected no grouping at all, got %+v", groups)
	}
}

// The schema half of the same guarantee, against the repository's real schema
// rather than a fixture written to agree with the test.
func TestGlossaryWithoutCategoriesStillValidatesAgainstTheSchema(t *testing.T) {
	reg := glossaryProject(t, consigneeGlossary,
		"A booking.", "the booking is released", "Nothing.")
	copySchemas(t, reg.Root)

	for _, f := range findingsFor(lintWithSchema(t, Open(reg.Root)), "schema") {
		if strings.Contains(f.Path, GlossaryName) {
			t.Fatalf("a glossary without categories must stay valid, got %+v", f)
		}
	}
}

// And the new shape against that same schema, so the file the linter reads and
// the file the schema admits cannot drift apart.
func TestCategorisedGlossaryValidatesAgainstTheSchema(t *testing.T) {
	reg := glossaryProject(t, categorisedGlossary,
		"A booking.", "the booking is released", "Nothing.")
	copySchemas(t, reg.Root)

	for _, f := range findingsFor(lintWithSchema(t, Open(reg.Root)), "schema") {
		if strings.Contains(f.Path, GlossaryName) {
			t.Fatalf("expected the categorised glossary to validate, got %+v", f)
		}
	}
}

// The id grammar, against the real schema, on the declaration. It is the
// segment grammar ids already use, and it is what stops a category being
// declared 'Parties' and referenced 'parties' — a split this shape exists to
// prevent, which the linter would otherwise report as undeclared, correctly and
// unhelpfully.
func TestCategoryIDGrammarIsEnforcedOnTheDeclaration(t *testing.T) {
	reg := glossaryProject(t, `schema_version: "0.1"
categories:
  - id: Parties
    title: Parties
terms:
  - term: Consignee
    definition: |
      The party named in the transport document who receives the goods.
`, "A booking.", "the booking is released", "Nothing.")

	if !glossarySchemaComplains(t, reg) {
		t.Fatal("expected a schema finding for a declared id outside the segment grammar")
	}
}

// And on the reference, which is a separate pattern in a separate place: the
// declaration below is well-formed, so only the term's own key can be at fault.
func TestCategoryIDGrammarIsEnforcedOnTheReference(t *testing.T) {
	reg := glossaryProject(t, `schema_version: "0.1"
categories:
  - id: parties
    title: Parties
terms:
  - term: Consignee
    category: Parties
    definition: |
      The party named in the transport document who receives the goods.
`, "A booking.", "the booking is released", "Nothing.")

	if !glossarySchemaComplains(t, reg) {
		t.Fatal("expected a schema finding for a reference outside the segment grammar")
	}
}

// --- the grouping itself, as a consumer of Grouped() receives it ---

// Declared order, not alphabetical: the order of a glossary is the project's
// own, which is already why nothing sorts 'terms'. The fixture declares
// 'parties' before 'documents' precisely because sorting would reverse them.
func TestGroupsFollowDeclaredOrder(t *testing.T) {
	g := mustLoadGlossary(t, categorisedGlossary)
	groups := g.Grouped()

	if got := groupIDs(groups); !equalStrings(got, []string{"parties", "documents"}) {
		t.Fatalf("expected the declared order, got %v", got)
	}
	if got := termNames(groups[0].Terms); !equalStrings(got, []string{"Consignee", "Shipper"}) {
		t.Fatalf("expected the terms in file order, got %v", got)
	}
	if groups[0].Title != "Parties" {
		t.Fatalf("expected the declared title, got %q", groups[0].Title)
	}
	if !strings.Contains(groups[0].Purpose, "shipment passes between") {
		t.Fatalf("expected the declared purpose, got %q", groups[0].Purpose)
	}
	if !groups[0].Declared {
		t.Fatal("a declared category must say so")
	}
}

// A declared category nothing uses still appears. The linter warns about it,
// and a surface that hid it would disagree with both the file and the warning.
func TestAnUnusedCategoryStillHasAGroup(t *testing.T) {
	g := mustLoadGlossary(t, `schema_version: "0.1"
categories:
  - id: parties
    title: Parties
  - id: events
    title: Events
terms:
  - term: Consignee
    category: parties
    definition: |
      The party named in the transport document who receives the goods.
`)
	groups := g.Grouped()
	if got := groupIDs(groups); !equalStrings(got, []string{"parties", "events"}) {
		t.Fatalf("expected the empty category to keep its group, got %v", got)
	}
	if len(groups[1].Terms) != 0 {
		t.Fatalf("expected the empty category to hold nothing, got %+v", groups[1].Terms)
	}
}

// The uncategorised terms are kept in a group with no id, after every declared
// one. No name is invented for them here, because a name invented here is the
// name every surface would then show.
func TestUncategorisedTermsHaveNoCategory(t *testing.T) {
	g := mustLoadGlossary(t, `schema_version: "0.1"
categories:
  - id: parties
    title: Parties
terms:
  - term: Demurrage
    definition: |
      What the carrier charges when a container is held past its free time.
  - term: Consignee
    category: parties
    definition: |
      The party named in the transport document who receives the goods.
`)
	groups := g.Grouped()
	if len(groups) != 2 {
		t.Fatalf("expected the declared group and the uncategorised one, got %+v", groupIDs(groups))
	}
	last := groups[len(groups)-1]
	if last.ID != "" || last.Title != "" || last.Declared {
		t.Fatalf("the uncategorised group must carry no category at all, got %+v", last)
	}
	if got := termNames(last.Terms); !equalStrings(got, []string{"Demurrage"}) {
		t.Fatalf("expected the uncategorised term there, got %v", got)
	}
}

// A term whose category was deleted and a term nobody categorised are opposite
// statements. Folding the first into the second would launder an error into an
// absence, and a consumer of Grouped() would present a decision nobody made.
func TestUndeclaredCategoryKeepsItsOwnGroup(t *testing.T) {
	g := mustLoadGlossary(t, `schema_version: "0.1"
categories:
  - id: parties
    title: Parties
terms:
  - term: Consignee
    category: parties
    definition: |
      The party named in the transport document who receives the goods.
  - term: Bill of Lading
    category: documents
    definition: |
      The document the carrier issues for goods received.
  - term: Demurrage
    definition: |
      What the carrier charges when a container is held past its free time.
`)
	groups := g.Grouped()
	if got := groupIDs(groups); !equalStrings(got, []string{"parties", "documents", ""}) {
		t.Fatalf("expected declared, then undeclared, then uncategorised, got %v", got)
	}
	undeclared := groups[1]
	if undeclared.Declared {
		t.Fatal("a category the file does not declare must not claim to be declared")
	}
	if undeclared.Title != "" {
		t.Fatalf("there is no declared wording to show, got %q", undeclared.Title)
	}
	if got := termNames(undeclared.Terms); !equalStrings(got, []string{"Bill of Lading"}) {
		t.Fatalf("expected the term to stay visible under its stated id, got %v", got)
	}
}

// The extreme of the deleted-category case: the whole 'categories' block is
// gone and the terms still name one. The file is then grouped by terms alone,
// and pretending otherwise would drop every category the terms still state on
// the way to a flat list that looks deliberate.
func TestTermsAloneCanGroupAGlossary(t *testing.T) {
	g := mustLoadGlossary(t, `schema_version: "0.1"
terms:
  - term: Consignee
    category: parties
    definition: |
      The party named in the transport document who receives the goods.
`)
	if !g.Categorised() {
		t.Fatal("a term naming a category means the glossary groups something")
	}
	groups := g.Grouped()
	if got := groupIDs(groups); !equalStrings(got, []string{"parties"}) {
		t.Fatalf("expected the stated category to keep its group, got %v", got)
	}
	if groups[0].Declared {
		t.Fatal("nothing declares it, and the group must not claim otherwise")
	}
}

// A duplicate id is an error, and the grouping must not double it into two
// headings on top of that — one defect should look like one defect.
func TestDuplicateCategoryYieldsOneGroup(t *testing.T) {
	g := mustLoadGlossary(t, `schema_version: "0.1"
categories:
  - id: parties
    title: Parties
  - id: parties
    title: Participants
terms:
  - term: Consignee
    category: parties
    definition: |
      The party named in the transport document who receives the goods.
`)
	groups := g.Grouped()
	if got := groupIDs(groups); !equalStrings(got, []string{"parties"}) {
		t.Fatalf("expected one group for the repeated id, got %v", got)
	}
	// Show the first declaration deterministically while the linter reports
	// the duplicate category as an error.
	if groups[0].Title != "Parties" {
		t.Fatalf("expected the first declaration to win, got %q", groups[0].Title)
	}
}

// --- helpers ---

// glossarySchemaComplains reports whether the repository's real glossary schema
// rejects the file this project wrote — the real one, copied in, rather than a
// fixture written to agree with the test.
func glossarySchemaComplains(t *testing.T, reg Registry) bool {
	t.Helper()
	copySchemas(t, reg.Root)
	for _, f := range findingsFor(lintWithSchema(t, Open(reg.Root)), "schema") {
		if strings.Contains(f.Path, GlossaryName) {
			return true
		}
	}
	return false
}

func mustLoadGlossary(t *testing.T, body string) *Glossary {
	t.Helper()
	reg := glossaryProject(t, body, "A booking.", "the booking is released", "Nothing.")
	g, err := LoadGlossary(reg.GlossaryPath())
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func groupIDs(groups []GlossaryGroup) []string {
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		out = append(out, g.ID)
	}
	return out
}

func termNames(terms []GlossaryTerm) []string {
	out := make([]string, 0, len(terms))
	for _, t := range terms {
		out = append(out, t.Term)
	}
	return out
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
