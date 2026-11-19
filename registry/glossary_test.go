// Fixtures here contain example @facet: tags; the file demonstrates syntax
// rather than implementing anything, so it opts out of scanning.
// @facet-ignore-file

package facet

import (
	"path/filepath"
	"strings"
	"testing"
)

const consigneeGlossary = `schema_version: "0.1"
terms:
  - term: Consignee
    definition: |
      The party named in the transport document who receives the goods at
      their destination — not necessarily the buyer, and not the same party as
      the carrier or the shipper.
`

// glossaryProject builds a project holding one record whose prose is supplied
// per test, plus whatever glossary body the test wants. An empty glossary body
// writes no file at all, which is the unconfigured case.
func glossaryProject(t *testing.T, glossary, summary, statement, knowledge string) Registry {
	t.Helper()
	root := t.TempDir()

	mustWrite(t, filepath.Join(root, "docs/facets/features/logistics/booking/release.yaml"),
		`schema_version: "0.1"
id: logistics/booking/release
lifecycle: active
intent:
  summary: |
    `+summary+`
contract:
  scenarios:
    - id: release-marks-the-booking
      statement: |
        Given a booking
        When it is released
        Then `+statement+`
    - id: release-is-refused-twice
      statement: |
        Given a booking already released
        When it is released again
        Then the second attempt is refused
knowledge:
  prerequisite_knowledge:
    question: "What must the user already understand to use this feature?"
    answer: |
      `+knowledge+`
lineage:
  owner:
    team: logistics
  created_at: "2026-09-16"
`)
	mustWrite(t, filepath.Join(root, "src/release.ts"),
		"// @facet: logistics/booking/release\n// @facet-role: primary\nexport function release() {}\n")

	if glossary != "" {
		mustWrite(t, filepath.Join(root, "docs/facets/glossary.yaml"), glossary)
	}
	return Open(root)
}

// The backward-compatibility guarantee, and the reason adoption is a decision
// rather than a migration: a project that declares no glossary gets no finding
// from either check, whatever its prose says.
func TestGlossaryUncheckedWithoutDeclaration(t *testing.T) {
	reg := glossaryProject(t, "",
		"The consignee is notified.", "the Consigne is notified", "Consignee handling.")
	rep := lintNoSchema(t, reg)
	for _, check := range []string{"glossary", "duplicate-glossary-term", "glossary-near-miss"} {
		if found := findingsFor(rep, check); len(found) != 0 {
			t.Fatalf("expected no %s findings without a glossary, got %+v", check, found)
		}
	}
}

// Mechanical, and an error: two definitions of one word leave a reader with no
// way to tell which one holds.
func TestDuplicateGlossaryTermIsError(t *testing.T) {
	reg := glossaryProject(t, consigneeGlossary+`  - term: Consignee
    definition: |
      The buyer.
`, "A booking.", "the booking is released", "Nothing.")

	found := findingsFor(lintNoSchema(t, reg), "duplicate-glossary-term")
	if len(found) != 1 {
		t.Fatalf("expected one duplicate finding, got %+v", found)
	}
	if found[0].Severity != SeverityError {
		t.Fatalf("expected an error, got %q", found[0].Severity)
	}
	if !strings.Contains(found[0].Message, "Consignee") {
		t.Fatalf("expected the message to name the term, got %q", found[0].Message)
	}
	if !strings.Contains(found[0].Path, GlossaryName) {
		t.Fatalf("expected the finding on %s, got %q", GlossaryName, found[0].Path)
	}
}

// Case-sensitively distinct, so two spellings are two terms rather than one
// defined twice. Declaring both is a decision the project is allowed to make;
// it is the near-miss check below that treats the pair as a trap.
func TestGlossaryTermsAreCaseSensitive(t *testing.T) {
	reg := glossaryProject(t, consigneeGlossary+`  - term: consignee
    definition: |
      The lower-case one, deliberately.
`, "A booking.", "the booking is released", "Nothing.")

	if found := findingsFor(lintNoSchema(t, reg), "duplicate-glossary-term"); len(found) != 0 {
		t.Fatalf("expected two case-distinct terms to pass, got %+v", found)
	}
}

// The first of the two cases the check exists for, and the one a reader cannot
// otherwise see: the lower-case spelling links nowhere.
func TestNearMissOnCaseIsWarning(t *testing.T) {
	reg := glossaryProject(t, consigneeGlossary,
		"The consignee is notified once the booking is released.",
		"the booking is released", "Nothing.")

	found := findingsFor(lintNoSchema(t, reg), "glossary-near-miss")
	if len(found) != 1 {
		t.Fatalf("expected one near-miss finding, got %+v", found)
	}
	if found[0].Severity != SeverityWarning {
		t.Fatalf("expected a warning, got %q", found[0].Severity)
	}
	if !strings.Contains(found[0].Message, "consignee") {
		t.Fatalf("expected the message to quote the word, got %q", found[0].Message)
	}
	// Without the suggestion the author has a complaint rather than a remedy:
	// the whole value of the check is naming the term that was meant.
	if !strings.Contains(found[0].Message, "Consignee") {
		t.Fatalf("expected the message to suggest the defined term, got %q", found[0].Message)
	}
	if found[0].ID != "logistics/booking/release" {
		t.Fatalf("expected the finding to name the record, got %q", found[0].ID)
	}
}

// Case is compared on its own and not as a distance, which matters wherever the
// distance cannot reach: shouting a nine-letter term is eight substitutions,
// far past any budget a typo deserves, and a three-letter term has no distance
// neighbourhood at all. Both are still the same word written a way that links
// nowhere, which is the whole of what this check is for.
func TestNearMissOnCaseIgnoresTheDistanceBudget(t *testing.T) {
	shouted := glossaryProject(t, consigneeGlossary,
		"The CONSIGNEE is notified.", "the booking is released", "Nothing.")
	found := findingsFor(lintNoSchema(t, shouted), "glossary-near-miss")
	if len(found) != 1 || !strings.Contains(found[0].Message, "CONSIGNEE") {
		t.Fatalf("expected the shouted spelling to be a near miss, got %+v", found)
	}

	short := glossaryProject(t, `schema_version: "0.1"
terms:
  - term: BOL
    definition: |
      Bill of Lading.
`, "The bol is stamped.", "the booking is released", "Nothing.")
	found = findingsFor(lintNoSchema(t, short), "glossary-near-miss")
	if len(found) != 1 || !strings.Contains(found[0].Message, "BOL") {
		t.Fatalf("expected a short term's case variant to be a near miss, got %+v", found)
	}
}

// The second case: a term of art one edit from a defined one is a misspelling
// of it far more often than it is a second word.
func TestNearMissOnTypoIsWarning(t *testing.T) {
	reg := glossaryProject(t, consigneeGlossary,
		"A booking.", "the Consigne is notified", "Nothing.")

	found := findingsFor(lintNoSchema(t, reg), "glossary-near-miss")
	if len(found) != 1 {
		t.Fatalf("expected one near-miss finding, got %+v", found)
	}
	if !strings.Contains(found[0].Message, "Consigne") {
		t.Fatalf("expected the message to quote the word, got %q", found[0].Message)
	}
	// The scenario is where the word is, and a record with twelve scenarios
	// makes "somewhere in this record" an unhelpful answer.
	if !strings.Contains(found[0].Message, "release-marks-the-booking") {
		t.Fatalf("expected the message to locate the scenario, got %q", found[0].Message)
	}
}

// Plural and possessive forms are valid inflections, so they produce no
// near-miss warning.
func TestInflectionIsNotANearMiss(t *testing.T) {
	for _, word := range []string{"Consignees", "Consignee's"} {
		reg := glossaryProject(t, consigneeGlossary,
			"Several "+word+" are notified.", "the booking is released", "Nothing.")
		if found := findingsFor(lintNoSchema(t, reg), "glossary-near-miss"); len(found) != 0 {
			t.Fatalf("expected %q to pass as an inflection, got %+v", word, found)
		}
	}
}

// An exact occurrence is what a consumer that links terms links. It is not a finding, and
// reporting it would make using the glossary correctly look like a defect.
func TestExactOccurrenceIsNotANearMiss(t *testing.T) {
	reg := glossaryProject(t, consigneeGlossary,
		"The Consignee is notified.", "the Consignee is notified", "Nothing.")
	if found := findingsFor(lintNoSchema(t, reg), "glossary-near-miss"); len(found) != 0 {
		t.Fatalf("expected an exact occurrence to pass, got %+v", found)
	}
}

// A term is matched whole or not at all. A substring match would link half of
// every sentence for a consumer that links terms and warn about most of the
// rest here.
func TestTermInsideALongerWordIsIgnored(t *testing.T) {
	reg := glossaryProject(t, `schema_version: "0.1"
terms:
  - term: Order
    definition: |
      A customer's request to buy, before it becomes a booking.
`, "Reordering an Order is not the same as Ordering one.",
		"the booking is released", "Nothing.")

	if found := findingsFor(lintNoSchema(t, reg), "glossary-near-miss"); len(found) != 0 {
		t.Fatalf("expected substrings to be left alone, got %+v", found)
	}
}

// Near-miss warnings exclude owner-written Knowledge answers, which may
// quote product wording; term linking may still cover that prose.
func TestNearMissIgnoresKnowledgeAnswers(t *testing.T) {
	reg := glossaryProject(t, consigneeGlossary,
		"A booking.", "the booking is released",
		"The consignee column in the old export is spelled this way.")

	if found := findingsFor(lintNoSchema(t, reg), "glossary-near-miss"); len(found) != 0 {
		t.Fatalf("expected knowledge answers to be out of scope, got %+v", found)
	}
}

// The same misspelling produces one finding per word per record, grouping
// repeated occurrences of one correction.
func TestNearMissIsReportedOncePerWordPerRecord(t *testing.T) {
	reg := glossaryProject(t, consigneeGlossary,
		"The consignee is notified. The consignee then collects. The consignee signs.",
		"the booking is released", "Nothing.")

	if found := findingsFor(lintNoSchema(t, reg), "glossary-near-miss"); len(found) != 1 {
		t.Fatalf("expected one finding for one misspelling, got %+v", found)
	}
}

// A very short term has no useful edit neighbourhood: at distance one, a
// three-letter word reaches a good part of the language.
func TestShortTermsHaveNoNearMissNeighbourhood(t *testing.T) {
	reg := glossaryProject(t, `schema_version: "0.1"
terms:
  - term: BOL
    definition: |
      Bill of Lading.
`, "The BOX is stamped.", "the booking is released", "Nothing.")

	if found := findingsFor(lintNoSchema(t, reg), "glossary-near-miss"); len(found) != 0 {
		t.Fatalf("expected no near-miss neighbourhood for a short term, got %+v", found)
	}
}

// Both checks are skippable by name, and so is the file itself.
func TestGlossaryChecksAreSkippable(t *testing.T) {
	reg := glossaryProject(t, consigneeGlossary+`  - term: Consignee
    definition: |
      Defined twice on purpose.
`, "The consignee is notified.", "the booking is released", "Nothing.")

	rep, err := Lint(reg, LintOptions{Skip: map[string]bool{
		"schema":                  true,
		"duplicate-glossary-term": true,
		"glossary-near-miss":      true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []string{"duplicate-glossary-term", "glossary-near-miss"} {
		if found := findingsFor(rep, check); len(found) != 0 {
			t.Fatalf("expected --skip %s to silence the check, got %+v", check, found)
		}
	}
}

// An unparseable glossary is reported as a finding without aborting other
// checks, so defects elsewhere remain visible in the same run.
func TestBrokenGlossaryIsAFindingNotAnAbort(t *testing.T) {
	reg := glossaryProject(t, "schema_version: \"0.1\"\nterms: [\n",
		"A booking.", "the booking is released", "Nothing.")

	rep, err := Lint(reg, LintOptions{Skip: map[string]bool{"schema": true}})
	if err != nil {
		t.Fatalf("a broken glossary must not abort the run: %v", err)
	}
	found := findingsFor(rep, "glossary")
	if len(found) != 1 || found[0].Severity != SeverityError {
		t.Fatalf("want one error naming the file, got %+v", found)
	}
	if !strings.Contains(found[0].Path, GlossaryName) {
		t.Fatalf("want the finding on %s, got %q", GlossaryName, found[0].Path)
	}
	if rep.Checked.Records == 0 {
		t.Fatal("the rest of the suite did not run")
	}
}

// --skip glossary is what somebody reaches for when the glossary is what is
// broken. It is useless if the file is loaded before the skip is read.
func TestSkipGlossarySurvivesABrokenFile(t *testing.T) {
	reg := glossaryProject(t, "schema_version: \"0.1\"\nterms: [\n",
		"A booking.", "the booking is released", "Nothing.")

	rep, err := Lint(reg, LintOptions{Skip: map[string]bool{"schema": true, "glossary": true}})
	if err != nil {
		t.Fatalf("--skip glossary must get past a broken glossary: %v", err)
	}
	if found := findingsFor(rep, "glossary"); len(found) != 0 {
		t.Fatalf("want the check silenced entirely, got %+v", found)
	}
	if rep.Checked.Records == 0 {
		t.Fatal("the rest of the suite did not run")
	}
}

// A mistyped key is otherwise silent: the file unmarshals to an empty list, and
// every term in it quietly stops being defined — so the near-miss check goes
// quiet at exactly the moment it was needed.
func TestMalformedGlossaryIsCaughtBySchema(t *testing.T) {
	reg := glossaryProject(t, `schema_version: "0.1"
termz:
  - term: Consignee
    definition: |
      The party named in the transport document.
`, "A booking.", "the booking is released", "Nothing.")
	copySchemas(t, reg.Root)

	found := findingsFor(lintWithSchema(t, Open(reg.Root)), "schema")
	var named bool
	for _, f := range found {
		if strings.Contains(f.Path, GlossaryName) {
			named = true
		}
	}
	if !named {
		t.Fatalf("expected a schema finding naming %s, got %+v", GlossaryName, found)
	}
}

// --- the loader and the matcher, directly ---

func TestGlossaryAbsentIsNotConfigured(t *testing.T) {
	g, err := LoadGlossary(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if g.Configured() {
		t.Fatal("an absent glossary must not report itself as configured")
	}
	if len(g.TermSet()) != 0 {
		t.Fatal("an absent glossary defines no terms")
	}
}

// FindTerms' rule, stated here so the rule and its implementation cannot
// drift apart: a term is found at a whole-word boundary, case-sensitively,
// and the longest defined term wins where two of them start at the same
// place.
func TestFindTermsMatchesWholeWordsOnly(t *testing.T) {
	terms := []string{"Order", "Order Line"}
	cases := []struct {
		text string
		want []string
	}{
		{"An Order is placed.", []string{"Order"}},
		{"Reordering is not Ordering.", nil},
		{"The Order Line is amended.", []string{"Order Line"}},
		{"order, in lower case, is a different string.", nil},
		// The boundary has to hold at the *start* too, not only at the end:
		// "ReOrder" carries the term capitalised inside it and ends cleanly, so
		// without the check on the byte before, half of that word would match.
		{"A ReOrder is not an Order.", []string{"Order"}},
		{"Order-Line is hyphenated, so the Order matches alone.", []string{"Order", "Order"}},
	}
	for _, c := range cases {
		var got []string
		for _, m := range FindTerms(c.text, terms) {
			got = append(got, m.Term)
		}
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Fatalf("%q: want %v, got %v", c.text, c.want, got)
		}
	}
}

// The offsets are what a consumer slices the prose on, so an off-by-one here is
// a word cut in half.
func TestFindTermsReportsOffsets(t *testing.T) {
	matches := FindTerms("An Order is placed.", []string{"Order"})
	if len(matches) != 1 {
		t.Fatalf("want one match, got %+v", matches)
	}
	if matches[0].Start != 3 || matches[0].End != 8 {
		t.Fatalf("want the match at [3,8), got [%d,%d)", matches[0].Start, matches[0].End)
	}
}

// Exact multi-word terms must not be treated as separate near-miss words,
// so valid terminology produces no warning.
func TestExactMultiWordTermIsNotScannedWordByWord(t *testing.T) {
	glossary := `schema_version: "0.1"
terms:
  - term: Bill of Lading
    definition: |
      The document the carrier issues for goods received.
  - term: Loading
    definition: |
      Putting the goods aboard.
`
	reg := glossaryProject(t, glossary,
		"The Bill of Lading is issued once the booking is released.",
		"the booking is released", "Nothing.")

	if found := findingsFor(lintNoSchema(t, reg), "glossary-near-miss"); len(found) != 0 {
		t.Fatalf("expected a correctly written multi-word term to pass, got %+v", found)
	}
}

// Masking correct occurrences must preserve an adjacent near miss so the
// warning remains precise without suppressing genuine mismatches.
func TestMaskingLeavesTheRestOfTheSentenceJudged(t *testing.T) {
	glossary := `schema_version: "0.1"
terms:
  - term: Bill of Lading
    definition: |
      The document the carrier issues for goods received.
  - term: Consignee
    definition: |
      The party named in the transport document.
`
	reg := glossaryProject(t, glossary,
		"The Bill of Lading names the consignee.",
		"the booking is released", "Nothing.")

	found := findingsFor(lintNoSchema(t, reg), "glossary-near-miss")
	if len(found) != 1 || !strings.Contains(found[0].Message, "consignee") {
		t.Fatalf("expected the lower-case Consignee beside an exact term to be caught, got %+v", found)
	}
}
