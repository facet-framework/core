// The fixtures below carry example @facet: tags to exercise the index, so this
// file demonstrates syntax rather than implementing anything: @facet-ignore-file

package facet

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// indexProject builds a project whose records differ in the ways the index has
// to tell apart: lifecycle, team, and how long the intent runs.
func indexProject(t *testing.T) Registry {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml"), `schema_version: "0.1"
id: billing/invoice/list
lifecycle: active
intent:
  summary: |
    Lists a customer's invoices, newest first. The ordering is stable across
    pages, which is what makes paging usable at all.
lineage:
  owner:
    team: billing
  created_at: "2026-01-01"
`)
	mustWrite(t, filepath.Join(root, "docs/facets/features/access/login/sso.yaml"), `schema_version: "0.1"
id: access/login/sso
lifecycle: planned
intent:
  summary: |
    Single sign-on against the customer's identity provider.
lineage:
  owner:
    team: access
  created_at: "2026-01-01"
`)
	return Open(root)
}

// The point of the index: an id, its state, its team and what it is about, on
// one line, so a question can be routed without opening anything.
func TestRecordIndexListsEveryRecordOnOneLine(t *testing.T) {
	text := RecordIndex([]Registry{indexProject(t)}, IndexOptions{Summaries: true})

	for _, want := range []string{
		"- access/login/sso [planned] (access) — Single sign-on against the customer's identity provider.",
		"- billing/invoice/list [active] (billing) — Lists a customer's invoices, newest first.",
		"docs/facets/features/<id>.yaml",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("index is missing %q:\n%s", want, text)
		}
	}
}

// Sorted by id, because an index whose order changes between two runs is a diff
// nobody can read and a cache nobody can keep.
func TestRecordIndexIsSortedByID(t *testing.T) {
	entries := BuildIndex([]Registry{indexProject(t)})

	if len(entries) != 2 {
		t.Fatalf("want two entries, got %d", len(entries))
	}
	if entries[0].ID != "access/login/sso" || entries[1].ID != "billing/invoice/list" {
		t.Errorf("wrong order: %+v", entries)
	}
}

// The summary is the first sentence, not the paragraph the author wrote for
// someone reading one record.
func TestRecordIndexSummaryStopsAtTheFirstSentence(t *testing.T) {
	entries := BuildIndex([]Registry{indexProject(t)})

	for _, entry := range entries {
		if strings.Contains(entry.Summary, "paging") {
			t.Errorf("summary carried the second sentence: %q", entry.Summary)
		}
	}
}

// A full stop that is not a sentence end does not end the summary. Cutting at
// "e.g." would produce a line that reads as a mistake.
func TestFirstSentenceKeepsAbbreviations(t *testing.T) {
	got := firstSentence("Retries a call that failed for a transient reason, e.g. a timeout. Anything else is reported.")

	want := "Retries a call that failed for a transient reason, e.g. a timeout."
	if got != want {
		t.Errorf("firstSentence = %q, want %q", got, want)
	}
}

// A summary with no sentence end at all is still bounded: one record must not
// be able to spend the whole budget.
func TestFirstSentenceIsBounded(t *testing.T) {
	got := firstSentence(strings.Repeat("word ", 200))

	if len(got) > 240 {
		t.Errorf("unbounded summary of %d bytes", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a cut summary should say it was cut: %q", got[len(got)-10:])
	}
}

// Over budget, the summaries go before the roster does: knowing a record exists
// is worth more than knowing what it is about.
func TestRecordIndexDropsSummariesBeforeRecords(t *testing.T) {
	reg := manyRecords(t, 40)

	text := RecordIndex([]Registry{reg}, IndexOptions{Summaries: true, MaxBytes: 2200})

	if strings.Contains(text, "something to run out of room on") {
		t.Errorf("summaries survived a budget that could not hold them:\n%s", text)
	}
	if got := strings.Count(text, "- bulk/records/record-"); got != 40 {
		t.Errorf("listed %d of 40 records; the roster should have survived:\n%s", got, text)
	}
}

// When even the bare roster does not fit, the index says how many records it
// left out. An index that quietly stops is read as "no such record".
func TestRecordIndexSaysWhatItLeftOut(t *testing.T) {
	reg := manyRecords(t, 40)

	text := RecordIndex([]Registry{reg}, IndexOptions{Summaries: true, MaxBytes: 900})

	if !strings.Contains(text, "further record(s) are not listed here") {
		t.Errorf("a truncated index must say so:\n%s", text)
	}
	if len(text) > 900 {
		t.Errorf("index of %d bytes exceeded its 900-byte budget", len(text))
	}
}

// A project with no records yields nothing at all, so a caller can append the
// index unconditionally without announcing an empty registry.
func TestRecordIndexOfAnEmptyProjectIsEmpty(t *testing.T) {
	if got := RecordIndex([]Registry{Open(t.TempDir())}, IndexOptions{}); got != "" {
		t.Errorf("want no index, got %q", got)
	}
}

// A record with variants has no top-level lifecycle. Reporting it as unknown
// would mark every record in a variant project as a mystery.
func TestRecordIndexReadsVariantLifecycles(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docs/facets/features/billing/invoice/export.yaml"), `schema_version: "0.1"
id: billing/invoice/export
variants:
  web:
    lifecycle: active
  mobile:
    lifecycle: planned
intent:
  summary: |
    Exports invoices as a file.
lineage:
  owner:
    team: billing
  created_at: "2026-01-01"
`)

	text := RecordIndex([]Registry{Open(root)}, IndexOptions{Summaries: true})

	if !strings.Contains(text, "billing/invoice/export [active]") {
		t.Errorf("a record active on one variant is active:\n%s", text)
	}
}

func manyRecords(t *testing.T, n int) Registry {
	t.Helper()
	root := t.TempDir()
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("bulk/records/record-%02d", i)
		mustWrite(t, filepath.Join(root, "docs/facets/features", id+".yaml"), fmt.Sprintf(`schema_version: "0.1"
id: %s
lifecycle: active
intent:
  summary: |
    A record that exists so the index has something to run out of room on.
lineage:
  owner:
    team: bulk
  created_at: "2026-01-01"
`, id))
	}
	return Open(root)
}
