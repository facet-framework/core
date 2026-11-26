package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/facet-framework/core/registry"
)

func warn(check string) facet.Finding {
	return facet.Finding{Severity: facet.SeverityWarning, Check: check}
}

// The summary breaks warnings down by check rather than reporting only a
// single total, so changes in warning categories remain visible.
func TestWarningBreakdownNamesEachCheck(t *testing.T) {
	r := facet.Report{Findings: []facet.Finding{
		warn("knowledge"), warn("knowledge"), warn("knowledge"),
		warn("proves"), warn("proves"),
		warn("sizing"),
		{Severity: facet.SeverityError, Check: "schema"},
	}}
	got := warningBreakdown(r)
	if want := " (knowledge 3, proves 2, sizing 1)"; got != want {
		t.Fatalf("warningBreakdown() = %q, want %q", got, want)
	}
	if strings.Contains(got, "schema") {
		t.Error("the warning breakdown must not count errors; they have their own figure")
	}
}

// Warning categories have a total ordering, including ties, so unchanged
// findings produce deterministic summary text.
func TestWarningBreakdownOrdersTiesByName(t *testing.T) {
	r := facet.Report{Findings: []facet.Finding{
		warn("sizing"), warn("proves"), warn("knowledge"),
	}}
	want := " (knowledge 1, proves 1, sizing 1)"
	for i := 0; i < 8; i++ {
		if got := warningBreakdown(r); got != want {
			t.Fatalf("run %d: warningBreakdown() = %q, want %q", i, got, want)
		}
	}
}

// A clean run emits no warning breakdown because it has no categories to list.
func TestWarningBreakdownIsSilentWithoutWarnings(t *testing.T) {
	r := facet.Report{Findings: []facet.Finding{{Severity: facet.SeverityError, Check: "schema"}}}
	if got := warningBreakdown(r); got != "" {
		t.Fatalf("a run with no warnings must add nothing to the summary, got %q", got)
	}
	if got := warningBreakdown(facet.Report{}); got != "" {
		t.Fatalf("an empty report must add nothing to the summary, got %q", got)
	}
}

// A check contributing far more findings than every other must not push them
// out of the line: its own figure leads, and the small standing ones stay
// legible beside it.
func TestOneLargeCheckDoesNotCrowdOutTheSmallOnes(t *testing.T) {
	findings := []facet.Finding{warn("knowledge"), warn("proves"), warn("sizing")}
	for i := 0; i < 342; i++ {
		findings = append(findings, warn("linked-only"))
	}
	got := warningBreakdown(facet.Report{Findings: findings})
	if !strings.HasPrefix(got, " (linked-only 342,") {
		t.Fatalf("the check that moved must lead the line, got %q", got)
	}
	for _, standing := range []string{"knowledge 1", "proves 1", "sizing 1"} {
		if !strings.Contains(got, standing) {
			t.Errorf("the standing figures must stay readable beside it; %q missing from %q", standing, got)
		}
	}
}

// Exercise printReport through stdout to verify that the warning breakdown
// reaches the printed summary as well as the helper return value.
func TestTheSummaryLineCarriesTheBreakdown(t *testing.T) {
	r := facet.Report{Findings: []facet.Finding{warn("knowledge"), warn("knowledge"), warn("proves")}}
	r.Checked.Records = 2

	previous := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	// Restored through defer: a t.Fatalf between here and the assignment below
	// would otherwise leave every later test in this package writing into the
	// pipe, and the failure would show up far away from its cause.
	defer func() { os.Stdout = previous }()
	os.Stdout = writer
	printReport(r)
	writer.Close()
	os.Stdout = previous

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(reader); err != nil {
		t.Fatalf("reading: %v", err)
	}
	line := buf.String()
	if !strings.Contains(line, "(knowledge 2, proves 1)") {
		t.Fatalf("summary without the breakdown:\n%s", line)
	}
}

// Info findings get their own marker, count and per-check breakdown, and never
// enter the warning figure: shared use asks a person to look and owes no work.
func TestInfoFindingsAreCountedApartFromWarnings(t *testing.T) {
	info := func(check string) facet.Finding {
		return facet.Finding{Severity: facet.SeverityInfo, Check: check, Path: "a.yaml", Message: "m"}
	}
	r := facet.Report{Findings: []facet.Finding{warn("knowledge"), info("shared-symbol"), info("identical-scenarios"), info("shared-symbol")}}
	if got := warningBreakdown(r); got != " (knowledge 1)" {
		t.Fatalf("warningBreakdown() = %q, want only the warning", got)
	}

	previous := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	defer func() { os.Stdout = previous }()
	os.Stdout = writer
	printReport(r)
	writer.Close()
	os.Stdout = previous

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(reader); err != nil {
		t.Fatalf("reading: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "1 warning(s) (knowledge 1), 3 info (shared-symbol 2, identical-scenarios 1)") {
		t.Fatalf("summary does not count info apart:\n%s", out)
	}
	if strings.Count(out, "info: a.yaml: m") != 3 {
		t.Fatalf("each info finding is printed with its own marker:\n%s", out)
	}
}
