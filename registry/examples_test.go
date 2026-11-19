// Fixtures here contain example @facet: tags; the file demonstrates syntax
// rather than implementing anything, so it opts out of scanning.
// @facet-ignore-file
package facet

import (
	"strings"
	"testing"
)

// The examples shape: a list of named blocks, one rule per block,
// each drawing its own headings, and a block that declares no columns taking
// the first declared set. A scenario that declares nothing at all must stay valid —
// every record written before this field omits it.
func TestScenarioExamplesAcceptsNamedBlocksAndInheritedColumns(t *testing.T) {
	reg := backingProject(t, `    - id: an-id-is-three-lowercase-segments
      statement: |
        Given a record id <id>
        When the linter checks it
        Then it is <verdict>
      examples:
        - name: valid ids
          columns: [id, verdict]
          rows:
            - ["billing/invoice/list", "accepted"]
            - ["registry/integrity/record-validation", "accepted"]
        - name: invalid ids
          rows:
            - ["Billing/Invoice", "rejected"]
            - ["billing/invoice/list/extra", "rejected"]
    - id: a-scenario-may-declare-none
      statement: |
        Given a customer with no invoices
        When they open the list
        Then the empty state is shown
`)
	rep := lintWithSchema(t, reg)
	if rep.Failed() {
		t.Fatalf("named blocks with an inherited column set are the design's own shape and must validate, got %+v", errorsOnly(rep))
	}
	for _, f := range rep.Findings {
		if f.Check == "examples" {
			t.Fatalf("a well-formed outline must not itself be a finding, got %+v", f)
		}
	}
}

// Columns are inherited, never invented. With no block declaring any there is
// no heading for any table, and the rows are a list of values sitting under
// nothing — so the schema requires that at least one block says what its
// values are called.
func TestScenarioExamplesRejectsBlocksThatDeclareNoColumnsAtAll(t *testing.T) {
	reg := backingProject(t, `    - id: an-id-is-three-lowercase-segments
      statement: |
        Given a record id <id>
        Then it is <verdict>
      examples:
        - name: valid ids
          rows:
            - ["billing/invoice/list", "accepted"]
        - name: invalid ids
          rows:
            - ["Billing/Invoice", "rejected"]
`)
	rep := lintWithSchema(t, reg)
	if !rep.Failed() {
		t.Fatal("examples where no block declares columns must be rejected: nothing names the values, and every table would draw an empty heading row")
	}
	// And only once. With no columns to measure against, every row is also a
	// row of the wrong length, so a check that did not stand aside here would
	// report one cause as four defects — the reason lintSizing leaves the
	// zero-scenario case to lintNoScenarios.
	if found := findingsFrom(rep, "examples", SeverityError); len(found) != 0 {
		t.Fatalf("the missing columns are the schema's finding; the rows must not be reported a second time, got %+v", found)
	}
}

func TestScenarioExamplesRejectsAnUndeclaredKeyInABlock(t *testing.T) {
	for _, key := range []string{"cols", "headers", "values"} {
		t.Run(key, func(t *testing.T) {
			reg := backingProject(t, `    - id: an-id-is-three-lowercase-segments
      statement: |
        Given a record id <id>
        Then it is accepted
      examples:
        - columns: [id]
          `+key+`: [id]
          rows:
            - ["billing/invoice/list"]
`)
			if !lintWithSchema(t, reg).Failed() {
				t.Fatalf("%q is not a key of an examples block and must be rejected rather than ignored", key)
			}
		})
	}
}

// A cell is substituted into prose, so it has to be a value that can be read
// as one. A null cell is the one scalar with no such reading: it would render
// as either nothing or the word "null", and an author who means an empty
// value has the empty string to write it with.
func TestScenarioExamplesAcceptsScalarCellsAndRejectsNull(t *testing.T) {
	ok := backingProject(t, `    - id: a-taken-port-does-not-stop-the-server
      statement: |
        Given port <port> is <free>
        When the server starts
        Then it listens and prints the address
      examples:
        - columns: [port, free]
          rows:
            - [7331, true]
            - [8080, false]
            - ["", "unset"]
`)
	if rep := lintWithSchema(t, ok); rep.Failed() {
		t.Fatalf("a YAML number, boolean and empty string are all readable cell values, got %+v", errorsOnly(rep))
	}

	null := backingProject(t, `    - id: a-taken-port-does-not-stop-the-server
      statement: |
        Given port <port> is <free>
        Then it listens
      examples:
        - columns: [port, free]
          rows:
            - [7331, null]
`)
	if !lintWithSchema(t, null).Failed() {
		t.Fatal("a null cell has no reading as a substituted value and must be rejected")
	}
}

// An empty block claims a set of cases and supplies none, which is
// indistinguishable from a block somebody forgot to finish. A consumer would
// render a heading row over nothing.
func TestScenarioExamplesRejectsEmptyBlocksAndEmptyRows(t *testing.T) {
	t.Run("no blocks", func(t *testing.T) {
		reg := backingProject(t, `    - id: an-id-is-three-lowercase-segments
      statement: |
        Given a record id <id>
        Then it is accepted
      examples: []
`)
		if !lintWithSchema(t, reg).Failed() {
			t.Fatal("an empty examples list declares an outline with no cases and must be rejected")
		}
	})
	t.Run("no rows", func(t *testing.T) {
		reg := backingProject(t, `    - id: an-id-is-three-lowercase-segments
      statement: |
        Given a record id <id>
        Then it is accepted
      examples:
        - columns: [id]
          rows: []
`)
		if !lintWithSchema(t, reg).Failed() {
			t.Fatal("a block with no rows names a set of cases and gives none, and must be rejected")
		}
	})
}

// The first invariant draft-07 cannot state: a row's length against the column
// count. Nothing outside the record is needed to see it and there is no reading
// under which a short row is correct — a consumer zips values to columns by
// position, so the last column of a short row silently has no value at all.
// That makes it an Error, in the class §11.2 puts schema validation in.
func TestExamplesRowThatDoesNotMatchTheColumnCountIsAnError(t *testing.T) {
	for name, rows := range map[string]string{
		"too few":  `            - ["billing/invoice/list"]`,
		"too many": `            - ["billing/invoice/list", "accepted", "three segments"]`,
	} {
		t.Run(name, func(t *testing.T) {
			reg := backingProject(t, `    - id: an-id-is-three-lowercase-segments
      statement: |
        Given a record id <id>
        Then it is <verdict>
      examples:
        - columns: [id, verdict]
          rows:
`+rows+`
`)
			rep := lintWithSchema(t, reg)
			found := findingsFrom(rep, "examples", SeverityError)
			if len(found) == 0 {
				t.Fatalf("a row whose length disagrees with its columns must be an error, got %+v", rep.Findings)
			}
			if !strings.Contains(found[0].Message, "an-id-is-three-lowercase-segments") {
				t.Errorf("the finding must name the scenario it belongs to, got %q", found[0].Message)
			}
		})
	}
}

// A block that declares no columns is measured against the ones it inherits,
// which is the only reading under which inheritance means anything. Getting
// this wrong in the other direction — measuring an inheriting block against
// zero columns — would fail every well-formed record that uses the feature.
func TestExamplesRowLengthIsMeasuredAgainstInheritedColumns(t *testing.T) {
	reg := backingProject(t, `    - id: an-id-is-three-lowercase-segments
      statement: |
        Given a record id <id>
        Then it is <verdict>
      examples:
        - name: valid ids
          columns: [id, verdict]
          rows:
            - ["billing/invoice/list", "accepted"]
        - name: invalid ids
          rows:
            - ["Billing/Invoice"]
`)
	found := findingsFrom(lintWithSchema(t, reg), "examples", SeverityError)
	if len(found) != 1 {
		t.Fatalf("the inheriting block's short row must be measured against the columns it inherits, got %+v", found)
	}
	if !strings.Contains(found[0].Message, "invalid ids") {
		t.Errorf("the finding must name the block, which is how a reader finds the row, got %q", found[0].Message)
	}
}

// The second invariant draft-07 cannot state. A placeholder naming no column
// never binds: a consumer leaves it as literal text and the table below it
// decorates a sentence that does not use it — the kind of failure that is
// invisible precisely because the record still looks complete.
func TestExamplesPlaceholderNamingNoColumnIsAWarning(t *testing.T) {
	// <result> is written twice on purpose: one misspelling is one mistake,
	// and a check reporting it once per occurrence would tell an author they
	// have two.
	reg := backingProject(t, `    - id: an-id-is-three-lowercase-segments
      statement: |
        Given a record id <id>
        When the linter decides <result>
        Then it reports <result>
      examples:
        - columns: [id, verdict]
          rows:
            - ["billing/invoice/list", "accepted"]
`)
	rep := lintWithSchema(t, reg)
	if rep.Failed() {
		t.Fatalf("an unbound placeholder must not fail the run, got %+v", errorsOnly(rep))
	}
	found := findingsFrom(rep, "examples", SeverityWarning)
	if len(found) != 1 {
		t.Fatalf("expected one warning for the placeholder no column answers, got %+v", rep.Findings)
	}
	if !strings.Contains(found[0].Message, "<result>") {
		t.Errorf("the finding must quote the placeholder, which is the only part the author has to find, got %q", found[0].Message)
	}
	if strings.Contains(found[0].Message, "<id>") {
		t.Errorf("a placeholder that does bind must not be reported, got %q", found[0].Message)
	}
}

// The other direction is deliberately silent. A column no placeholder uses is
// ordinary Gherkin: a `reason` or `notes` column carries why a case belongs in
// the table without the sentence substituting it, and warning about it would
// argue with a table that is doing its job.
func TestExamplesColumnNoPlaceholderUsesIsSilent(t *testing.T) {
	reg := backingProject(t, `    - id: an-id-is-three-lowercase-segments
      statement: |
        Given a record id <id>
        Then it is <verdict>
      examples:
        - columns: [id, verdict, why]
          rows:
            - ["billing/invoice/list", "accepted", "three lowercase segments"]
            - ["Billing/Invoice", "rejected", "two segments, and capitalised"]
`)
	rep := lintWithSchema(t, reg)
	if rep.Failed() {
		t.Fatalf("a column carrying an explanation is valid, got %+v", errorsOnly(rep))
	}
	if found := findingsFrom(rep, "examples", SeverityWarning); len(found) != 0 {
		t.Fatalf("a column no placeholder substitutes must not be reported, got %+v", found)
	}
}

// Blocks may declare different columns from one another — the design says so
// explicitly, and its reader unions them when it offers a parameter's values.
// So a placeholder answered only by the second block is bound, and a check
// that measured against the first block's headings alone would fault a record
// the design calls correct.
func TestExamplesPlaceholderMayBeAnsweredByAnyBlock(t *testing.T) {
	reg := backingProject(t, `    - id: an-id-is-three-lowercase-segments
      statement: |
        Given a record id <id>
        When the linter checks it
        Then it is accepted, or rejected for <why>
      examples:
        - name: valid ids
          columns: [id]
          rows:
            - ["billing/invoice/list"]
        - name: invalid ids
          columns: [id, why]
          rows:
            - ["Billing/Invoice", "capitalised"]
`)
	rep := lintWithSchema(t, reg)
	if rep.Failed() {
		t.Fatalf("blocks declaring different columns are valid, got %+v", errorsOnly(rep))
	}
	if found := findingsFrom(rep, "examples", SeverityWarning); len(found) != 0 {
		t.Fatalf("a placeholder answered by the second block alone is bound and must be silent, got %+v", found)
	}
}

// Placeholder recognition is unicode aware on purpose: under an ASCII-only
// pattern `<naïve>` would fail to become a parameter. A registry written in its
// product's language has placeholders in that language.
// Both halves are asserted, and the second is the one that bites. Silence over
// a bound `<naïve>` is also what an ascii-only pattern produces — it does not
// see the token at all — so a test asserting only that would pass against the
// bug it was written to catch.
func TestExamplesPlaceholdersAreRecognisedBeyondAscii(t *testing.T) {
	bound := backingProject(t, `    - id: an-id-is-three-lowercase-segments
      statement: |
        Given a record id <id>
        Then it is <naïve>
      examples:
        - columns: [id, "naïve"]
          rows:
            - ["billing/invoice/list", "yes"]
`)
	if found := findingsFrom(lintWithSchema(t, bound), "examples", SeverityWarning); len(found) != 0 {
		t.Fatalf("a non-ascii placeholder naming a declared column binds and must be silent, got %+v", found)
	}

	unbound := backingProject(t, `    - id: an-id-is-three-lowercase-segments
      statement: |
        Given a record id <id>
        Then it is <café>
      examples:
        - columns: [id, "naïve"]
          rows:
            - ["billing/invoice/list", "yes"]
`)
	found := findingsFrom(lintWithSchema(t, unbound), "examples", SeverityWarning)
	if len(found) != 1 {
		t.Fatalf("a non-ascii placeholder no column answers must be reported, or the pattern is not reading the sentence at all, got %+v", found)
	}
	if !strings.Contains(found[0].Message, "<café>") {
		t.Errorf("the finding must quote the placeholder as written, got %q", found[0].Message)
	}
}

// The normative claim of FACET.md §3.2.3, held against the code rather than
// asserted: an Examples table is one scenario. A record whose two scenarios
// carry eleven cases between them declares two scenarios — so gate 3 (§2.1.1,
// two to nine) sees two and says nothing, and §11.4's scenarios-per-record
// measure counts two.
func TestAnExamplesTableCountsAsOneScenario(t *testing.T) {
	reg := backingProject(t, `    - id: an-id-is-three-lowercase-segments
      statement: |
        Given a record id <id>
        Then it is <verdict>
      examples:
        - columns: [id, verdict]
          rows:
            - ["billing/invoice/list", "accepted"]
            - ["registry/integrity/record-validation", "accepted"]
            - ["Billing/Invoice", "rejected"]
            - ["billing/invoice", "rejected"]
            - ["billing/invoice/list/extra", "rejected"]
    - id: a-taken-port-does-not-stop-the-server
      statement: |
        Given port <port>
        Then the server listens
      examples:
        - columns: [port]
          rows:
            - [7331]
            - [8080]
            - [0]
            - [65535]
            - [1024]
            - [443]
`)
	rec, err := LoadRecord(reg.PathForID("billing/invoice/list"), "features")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(rec.ContractScenarios()); n != 2 {
		t.Fatalf("eleven cases under two scenarios must count as two scenarios, got %d", n)
	}
	for _, f := range lintWithSchema(t, reg).Findings {
		if f.Check == "sizing" {
			t.Fatalf("the sizing gate must see two scenarios rather than eleven cases, got %+v", f)
		}
	}
}

// findingsFrom narrows a report to one check at one severity. The two
// severities this check emits mean different things — a malformed table versus
// a sentence that may be fine — so a test that could not tell them apart would
// pass against an implementation that got the severity wrong.
func findingsFrom(r Report, check string, sev Severity) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Check == check && f.Severity == sev {
			out = append(out, f)
		}
	}
	return out
}
