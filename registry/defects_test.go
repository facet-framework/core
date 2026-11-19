package facet

import (
	"os"
	"path/filepath"
	"testing"
)

// writeRecord drops a record on disk and loads it, which is the shape every
// test here needs: the parsing under test reads Fields, so a struct literal
// would test nothing.
func writeRecord(t *testing.T, body string) Record {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "record.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := LoadRecord(path, "features")
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

const defectsRecord = `
schema_version: "0.1"
id: billing/invoice/list
lifecycle: active
intent:
  summary: |
    Lists a customer's invoices so they can reconcile their own billing.
contract:
  scenarios:
    - id: empty-list-shown
      statement: Given no invoices exist, then the empty state is shown
defects:
  fixes:
    - commit: "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2"
      date: "2026-03-02"
      files: [internal/billing/list.go, internal/billing/page.go]
      subject: "fix(billing): pagination dropped the last page"
      issue: "#42"
      recorded_by: git-history
    - commit: "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"
      date: "2026-01-09"
      files: [internal/billing/list.go]
      subject: "fix(billing): empty state rendered a zero row"
      recorded_by: manual
  excluded:
    - commit: "c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3"
      reason: touched the anchored file only to reformat imports
lineage:
  owner:
    team: billing
  created_at: "2026-01-01"
`

func TestFixesReadsEveryEntry(t *testing.T) {
	rec := writeRecord(t, defectsRecord)
	fixes := rec.Fixes()
	if len(fixes) != 2 {
		t.Fatalf("want 2 fixes, got %d", len(fixes))
	}
	// Sorted newest first: a hotspot report is read from the top, and the
	// question it answers is "what has been breaking", not "what once broke".
	if fixes[0].Date != "2026-03-02" {
		t.Errorf("want the newest fix first, got %s", fixes[0].Date)
	}
	if fixes[0].Feature != "billing/invoice/list" {
		t.Errorf("fix does not carry its feature id: %q", fixes[0].Feature)
	}
	if fixes[0].Subject != "fix(billing): pagination dropped the last page" {
		t.Errorf("subject not read: %q", fixes[0].Subject)
	}
	if fixes[0].Issue != "#42" {
		t.Errorf("issue not read: %q", fixes[0].Issue)
	}
	if fixes[0].RecordedBy != "git-history" {
		t.Errorf("recorded_by not read: %q", fixes[0].RecordedBy)
	}
	if len(fixes[0].Files) != 2 {
		t.Errorf("want 2 files, got %v", fixes[0].Files)
	}
	if fixes[0].Path != rec.Path {
		t.Errorf("fix does not carry the record path")
	}
}

func TestDefectTotalCountsFixesAndNotExclusions(t *testing.T) {
	rec := writeRecord(t, defectsRecord)
	if got := rec.DefectTotal(); got != 2 {
		t.Errorf("want total 2, got %d", got)
	}
}

func TestDefectsByFileCountsPerPath(t *testing.T) {
	rec := writeRecord(t, defectsRecord)
	byFile := rec.DefectsByFile()
	if len(byFile) != 2 {
		t.Fatalf("want 2 files, got %d: %v", len(byFile), byFile)
	}
	// Sorted by count descending, then path, so the hottest file leads.
	if byFile[0].Path != "internal/billing/list.go" || byFile[0].Count != 2 {
		t.Errorf("want list.go with 2, got %+v", byFile[0])
	}
	if byFile[1].Path != "internal/billing/page.go" || byFile[1].Count != 1 {
		t.Errorf("want page.go with 1, got %+v", byFile[1])
	}
}

func TestExcludedCommitsAreReadWithTheirReason(t *testing.T) {
	rec := writeRecord(t, defectsRecord)
	ex := rec.ExcludedCommits()
	if len(ex) != 1 {
		t.Fatalf("want 1 exclusion, got %d", len(ex))
	}
	if ex[0].Reason == "" {
		t.Error("exclusion carries no reason")
	}
}

func TestKnowsCommitTellsRecordedCommitsApart(t *testing.T) {
	rec := writeRecord(t, defectsRecord)
	cases := []struct {
		commit string
		want   bool
		why    string
	}{
		{"b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2", true, "an exact recorded sha"},
		{"b2b2b2b", true, "an abbreviation of a recorded sha"},
		{"c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3", true, "an excluded sha counts as known"},
		{"d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4", false, "an unseen sha"},
	}
	for _, tc := range cases {
		if got := rec.KnowsCommit(tc.commit); got != tc.want {
			t.Errorf("KnowsCommit(%s) = %v, want %v — %s", tc.commit, got, tc.want, tc.why)
		}
	}
}

func TestAbsentDefectsBlockIsNotAnError(t *testing.T) {
	rec := writeRecord(t, `
schema_version: "0.1"
id: billing/invoice/list
lifecycle: active
intent:
  summary: |
    Lists a customer's invoices so they can reconcile their own billing.
contract:
  scenarios:
    - id: empty-list-shown
      statement: Given no invoices exist, then the empty state is shown
lineage:
  owner:
    team: billing
  created_at: "2026-01-01"
`)
	if rec.Fixes() != nil {
		t.Error("a record with no defects block should report no fixes")
	}
	if rec.DefectTotal() != 0 {
		t.Error("a record with no defects block should total zero")
	}
	if rec.KnowsCommit("a1a1a1a") {
		t.Error("a record with no defects block knows no commit")
	}
}

func TestHotspotsRankFeaturesByFixCount(t *testing.T) {
	hot := writeRecord(t, defectsRecord)
	cold := writeRecord(t, `
schema_version: "0.1"
id: billing/invoice/export
lifecycle: active
intent:
  summary: |
    Exports a customer's invoices so they can file them with their accountant.
contract:
  scenarios:
    - id: csv-written
      statement: Given invoices exist, then a CSV is written
defects:
  fixes:
    - commit: "e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5"
      date: "2026-02-02"
      files: [internal/billing/export.go]
lineage:
  owner:
    team: billing
  created_at: "2026-01-01"
`)
	quiet := writeRecord(t, `
schema_version: "0.1"
id: billing/invoice/print
lifecycle: active
intent:
  summary: |
    Prints a customer's invoices so they can post them to somebody.
contract:
  scenarios:
    - id: printed
      statement: Given an invoice, then it prints
lineage:
  owner:
    team: billing
  created_at: "2026-01-01"
`)

	// Deliberately supplied coldest-first: the ranking must come from the
	// counts, not from the order the registry happened to load.
	spots := Hotspots([]Record{quiet, cold, hot})
	if len(spots) != 2 {
		t.Fatalf("want 2 hotspots (a record with no fixes is not one), got %d", len(spots))
	}
	if spots[0].Feature != "billing/invoice/list" || spots[0].Total != 2 {
		t.Errorf("want the hottest feature first, got %+v", spots[0])
	}
	if spots[1].Feature != "billing/invoice/export" || spots[1].Total != 1 {
		t.Errorf("want the colder feature second, got %+v", spots[1])
	}
	if spots[0].LastFix != "2026-03-02" {
		t.Errorf("hotspot does not carry the most recent fix date: %q", spots[0].LastFix)
	}
	if len(spots[0].Files) != 2 {
		t.Errorf("hotspot does not carry its per-file breakdown: %+v", spots[0].Files)
	}
}

func TestHotspotsBreakTiesByIDSoOutputIsStable(t *testing.T) {
	mk := func(id string) Record {
		return writeRecord(t, `
schema_version: "0.1"
id: `+id+`
lifecycle: active
intent:
  summary: |
    A feature that exists only so this test has two of them to order.
contract:
  scenarios:
    - id: exists
      statement: Given the feature, then it exists
defects:
  fixes:
    - commit: "f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6"
      date: "2026-02-02"
      files: [internal/a.go]
lineage:
  owner:
    team: billing
  created_at: "2026-01-01"
`)
	}
	spots := Hotspots([]Record{mk("billing/z/z"), mk("billing/a/a")})
	if spots[0].Feature != "billing/a/a" {
		t.Errorf("equal counts must order by id, got %s first", spots[0].Feature)
	}
}
